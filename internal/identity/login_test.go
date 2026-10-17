package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// stubDirectory es el directorio de usuarios de las pruebas unitarias: permite fijar qué
// devuelve la búsqueda por teléfono —incluido un fallo del driver— sin base de datos.
type stubDirectory struct {
	credentials identity.Credentials
	err         error
	phones      []string
}

func (s *stubDirectory) CredentialsByPhone(_ context.Context, phone string) (identity.Credentials, error) {
	s.phones = append(s.phones, phone)
	return s.credentials, s.err
}

// newLoginHandler construye el handler con un directorio y un registro capturados.
func newLoginHandler(t *testing.T, directory identity.UserDirectory) (*identity.LoginHandler, *strings.Builder) {
	t.Helper()

	var logBuf strings.Builder
	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := identity.NewLoginHandler(directory, issuer, slog.New(slog.NewTextHandler(&logBuf, nil)))
	if err != nil {
		t.Fatalf("NewLoginHandler: %v", err)
	}
	return handler, &logBuf
}

// postLogin envía un cuerpo al handler y devuelve la respuesta.
func postLogin(t *testing.T, handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// loginEnvelope lee el sobre de error de una respuesta.
type loginEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details"`
	} `json:"error"`
}

// TestLoginIssuesAnEmployeeToken cubre el camino feliz del §4.2: `200` con el token y el id
// del usuario, y un token que verifica con el tenant y el usuario del usuario resuelto y SIN
// claim `role`.
func TestLoginIssuesAnEmployeeToken(t *testing.T) {
	const password = "una-contraseña-en-claro"

	storedHash, err := identity.HashCredential(password)
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}

	directory := &stubDirectory{credentials: identity.Credentials{
		UserID:       testUserID,
		TenantID:     testTenantID,
		PasswordHash: storedHash,
	}}
	handler, logBuf := newLoginHandler(t, directory)

	recorder := postLogin(t, handler, `{"phone":" +51900000101 ","password":"`+password+`"}`, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Token string `json:"token"`
		User  struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es el JSON del contrato: %v (%q)", err, recorder.Body.String())
	}
	if body.Token == "" {
		t.Fatal("la respuesta no trae token")
	}
	if body.User.ID != testUserID.String() {
		t.Errorf("user.id = %q, want %q", body.User.ID, testUserID.String())
	}
	if len(directory.phones) != 1 || directory.phones[0] != "+51900000101" {
		t.Errorf("el directorio recibió %q, want el teléfono sin espacios alrededor", directory.phones)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}
	claims, err := issuer.Verify(body.Token)
	if err != nil {
		t.Fatalf("el token emitido no verifica: %v", err)
	}
	if claims.TenantID != testTenantID {
		t.Errorf("claims.TenantID = %v, want %v (el tenant del usuario resuelto)", claims.TenantID, testTenantID)
	}
	if claims.UserID != testUserID {
		t.Errorf("claims.UserID = %v, want %v", claims.UserID, testUserID)
	}
	if claims.Role != "" {
		t.Errorf("claims.Role = %q, want vacío: /auth/login emite un token de empleado", claims.Role)
	}

	// §7.9: ni el teléfono, ni la contraseña, ni el hash, ni el token llegan al log.
	for _, forbidden := range []string{"+51900000101", password, storedHash, body.Token} {
		if strings.Contains(logBuf.String(), forbidden) {
			t.Errorf("el log del login contiene %q: %q", forbidden, logBuf.String())
		}
	}
}

// TestLoginAnswersInvalidCredentialsGenerically cubre el `401` genérico del §4.2 y la regla
// de no enumerar usuarios: un teléfono desconocido y una contraseña incorrecta tienen que
// devolver EXACTAMENTE la misma respuesta. La comparación es entre los dos cuerpos completos,
// no entre los códigos: distinguirlos por el mensaje ya enumeraría usuarios.
func TestLoginAnswersInvalidCredentialsGenerically(t *testing.T) {
	const password = "la-contraseña-correcta"

	storedHash, err := identity.HashCredential(password)
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}

	const phone = "+51900000101"

	responses := map[string]*httptest.ResponseRecorder{}
	for name, directory := range map[string]identity.UserDirectory{
		"wrong password": &stubDirectory{credentials: identity.Credentials{
			UserID:       testUserID,
			TenantID:     testTenantID,
			PasswordHash: storedHash,
		}},
		"unknown phone": &stubDirectory{err: identity.ErrUnknownPhone},
	} {
		handler, _ := newLoginHandler(t, directory)
		responses[name] = postLogin(t, handler, `{"phone":"`+phone+`","password":"la-contraseña-equivocada"}`, nil)
	}

	unknownPhone, wrongPassword := responses["unknown phone"], responses["wrong password"]

	for name, recorder := range responses {
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("[%s] estado = %d, want 401", name, recorder.Code)
		}

		var envelope loginEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("[%s] la respuesta no es el sobre de error único: %v (%q)", name, err, recorder.Body.String())
		}
		if envelope.Error.Code != httpx.CodeUnauthenticated {
			t.Errorf("[%s] error.code = %q, want %q", name, envelope.Error.Code, httpx.CodeUnauthenticated)
		}
		if envelope.Error.Message == "" {
			t.Errorf("[%s] error.message vacío: el sobre siempre lleva un mensaje humano", name)
		}
		if envelope.Error.Details != nil {
			t.Errorf("[%s] error.details = %#v, want ausente: un detalle por causa enumeraría usuarios", name, envelope.Error.Details)
		}
	}

	if unknownPhone.Body.String() != wrongPassword.Body.String() {
		t.Errorf("las dos respuestas 401 difieren y delatan cuál falló:\n  teléfono desconocido: %q\n  contraseña incorrecta: %q",
			unknownPhone.Body.String(), wrongPassword.Body.String())
	}

	// Ni la respuesta ni su cuerpo mencionan el teléfono.
	for name, recorder := range responses {
		if strings.Contains(recorder.Body.String(), phone) {
			t.Errorf("[%s] el cuerpo del 401 repite el teléfono: %q", name, recorder.Body.String())
		}
	}
}

// TestLoginValidatesTheBodyBeforeTouchingTheDirectory cubre que la validación ocurra en el
// límite (ARCHITECTURE.md:120): un cuerpo que no sirve es 422 y no llega a la búsqueda.
func TestLoginValidatesTheBodyBeforeTouchingTheDirectory(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "not json", body: `not json at all`},
		{name: "an empty object", body: `{}`},
		{name: "no password", body: `{"phone":"+51900000101"}`},
		{name: "no phone", body: `{"password":"x"}`},
		{name: "a blank phone", body: `{"phone":"   ","password":"x"}`},
		{name: "an empty password", body: `{"phone":"+51900000101","password":""}`},
		{name: "an empty body", body: ``},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			directory := &stubDirectory{}
			handler, _ := newLoginHandler(t, directory)

			recorder := postLogin(t, handler, tc.body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Errorf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}

			var envelope loginEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("la respuesta no es el sobre de error único: %v", err)
			}
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			if len(directory.phones) != 0 {
				t.Errorf("el directorio se consultó con %q: el cuerpo se valida antes de tocar la base", directory.phones)
			}
		})
	}
}

// TestLoginReportsAStoreFailureAsAnInternalError cubre que un fallo de la base no se disfrace
// de credenciales inválidas: es un 500 con el sobre genérico, la causa no llega al cliente y
// el teléfono no llega ni al log ni al cuerpo.
func TestLoginReportsAStoreFailureAsAnInternalError(t *testing.T) {
	const phone = "+51900000101"

	directory := &stubDirectory{err: errors.New("no connection available: the pool is closed")}
	handler, logBuf := newLoginHandler(t, directory)

	recorder := postLogin(t, handler, `{"phone":"`+phone+`","password":"cualquiera"}`, nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	var envelope loginEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error único: %v", err)
	}
	if envelope.Error.Code != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeInternalError)
	}
	if strings.Contains(recorder.Body.String(), "pool") {
		t.Errorf("el cuerpo del 500 filtró la causa interna: %q", recorder.Body.String())
	}
	if !strings.Contains(logBuf.String(), "pool") {
		t.Errorf("la causa no llegó al log del operador: %q", logBuf.String())
	}
	if strings.Contains(logBuf.String(), phone) {
		t.Errorf("el log contiene el teléfono: %q", logBuf.String())
	}
}

// TestLoginDoesNotReadATenantFromTheRequest deja escrito en una prueba lo que el tipo ya
// garantiza: `X-Tenant-Id` y un `tenant_id` en el cuerpo no tienen ningún efecto sobre la
// respuesta. El único tenant posible es el del usuario que resolvió el teléfono.
func TestLoginDoesNotReadATenantFromTheRequest(t *testing.T) {
	const password = "una-contraseña-en-claro"

	storedHash, err := identity.HashCredential(password)
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}

	forged := "019a0000-0000-7000-8000-0000000000b1"

	directory := &stubDirectory{credentials: identity.Credentials{
		UserID:       testUserID,
		TenantID:     testTenantID,
		PasswordHash: storedHash,
	}}
	handler, _ := newLoginHandler(t, directory)

	body := `{"phone":"+51900000101","password":"` + password + `","tenant_id":"` + forged + `","tenantId":"` + forged + `"}`
	recorder := postLogin(t, handler, body, map[string]string{"X-Tenant-Id": forged})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("leer la respuesta: %v", err)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}
	claims, err := issuer.Verify(response.Token)
	if err != nil {
		t.Fatalf("el token emitido no verifica: %v", err)
	}
	if claims.TenantID.String() == forged {
		t.Fatalf("el token quedó con el tenant falsificado %q: el tenant NUNCA sale del cuerpo ni de un encabezado", forged)
	}
	if claims.TenantID != testTenantID {
		t.Errorf("claims.TenantID = %v, want %v", claims.TenantID, testTenantID)
	}
	if strings.Contains(recorder.Body.String(), forged) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", recorder.Body.String())
	}
}
