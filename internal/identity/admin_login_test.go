// Pruebas de `POST /auth/admin-login` (W8-BE-1; docs/BACKEND.md §4.4 y §11.4 #7;
// ARCHITECTURE.md:126).
//
// Lo que estas pruebas fijan:
//   - el 200 devuelve el token y el NOMBRE del tenant, y el token lleva `role: "admin"` con el
//     tenant y el usuario que resolvió el teléfono;
//   - el 401 es genérico y CONSTANTE: teléfono inexistente, contraseña incorrecta, tenant
//     inexistente y hash de la base ilegible producen exactamente el mismo cuerpo;
//   - el cuerpo incompleto es 422 antes de tocar la base;
//   - ni el teléfono, ni la contraseña, ni el hash, ni el token llegan jamás al registro.
package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
)

const (
	adminTenantID = "019c0000-0000-7000-8000-0000000000c1"
	adminUserID   = "019c0000-0000-7000-8000-0000000000c2"
	adminPhone    = "+51988000222"
	adminPassword = "clave-maestra-de-la-bodega"
)

// stubUserDirectory resuelve el teléfono a una identidad fija o devuelve el fallo pedido.
type stubUserDirectory struct {
	credentials identity.Credentials
	err         error
}

func (s stubUserDirectory) CredentialsByPhone(context.Context, string) (identity.Credentials, error) {
	return s.credentials, s.err
}

// stubTenantDirectory devuelve la credencial maestra fija o el fallo pedido.
type stubTenantDirectory struct {
	credential identity.TenantCredential
	err        error
}

func (s stubTenantDirectory) MasterCredential(context.Context, uuid.UUID) (identity.TenantCredential, error) {
	return s.credential, s.err
}

// adminIssuer construye el emisor de la prueba.
func adminIssuer(t *testing.T) *identity.TokenIssuer {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}
	return issuer
}

// hashOf genera un argon2id verificable para la prueba.
func hashOf(t *testing.T, credential string) string {
	t.Helper()

	hash, err := identity.HashCredential(credential)
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}
	return hash
}

// adminFixture arma el handler con directorios de mentira y una credencial real.
func adminFixture(t *testing.T, users identity.UserDirectory, tenants identity.TenantDirectory, logBuffer *strings.Builder) http.Handler {
	t.Helper()

	var logger *slog.Logger
	if logBuffer == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	} else {
		logger = slog.New(slog.NewJSONHandler(logBuffer, nil))
	}

	handler, err := identity.NewAdminLoginHandler(users, tenants, adminIssuer(t), logger)
	if err != nil {
		t.Fatalf("NewAdminLoginHandler: %v", err)
	}
	return handler
}

// defaultAdminFixture es el camino feliz: el teléfono resuelve a un usuario del tenant y la
// contraseña maestra del tenant verifica.
func defaultAdminFixture(t *testing.T, logBuffer *strings.Builder) (http.Handler, string) {
	t.Helper()

	stored := hashOf(t, adminPassword)
	return adminFixture(t,
		stubUserDirectory{credentials: identity.Credentials{
			UserID:       uuid.MustParse(adminUserID),
			TenantID:     uuid.MustParse(adminTenantID),
			PasswordHash: hashOf(t, "la-contraseña-del-empleado"),
		}},
		stubTenantDirectory{credential: identity.TenantCredential{
			Name:               "Bodega Doña Rosa",
			MasterPasswordHash: stored,
		}},
		logBuffer,
	), stored
}

// doAdminLogin envía el cuerpo al handler.
func doAdminLogin(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/auth/admin-login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder
}

// adminLoginBody arma el cuerpo del contrato.
func adminLoginBody(phone, password string) string {
	return `{"phone":"` + phone + `","master_password":"` + password + `"}`
}

// TestAdminLoginIssuesAnAdminTokenWithTheTenantName: el 200 del contrato —token más nombre del
// tenant— y el token con `role: "admin"`, el tenant y el usuario que resolvió el teléfono.
func TestAdminLoginIssuesAnAdminTokenWithTheTenantName(t *testing.T) {
	handler, _ := defaultAdminFixture(t, nil)

	recorder := doAdminLogin(t, handler, adminLoginBody(adminPhone, adminPassword))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Token  string `json:"token"`
		Tenant struct {
			Name string `json:"name"`
		} `json:"tenant"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (%q)", err, recorder.Body.String())
	}
	if body.Tenant.Name != "Bodega Doña Rosa" {
		t.Errorf("tenant.name = %q, want el nombre del tenant", body.Tenant.Name)
	}

	claims, err := adminIssuer(t).Verify(body.Token)
	if err != nil {
		t.Fatalf("el token no verifica: %v", err)
	}
	if !claims.IsAdmin() {
		t.Errorf("role = %q, want admin: el token de administrador es el único con el claim", claims.Role)
	}
	if claims.TenantID.String() != adminTenantID {
		t.Errorf("tenant del token = %s, want %s", claims.TenantID, adminTenantID)
	}
	if claims.UserID.String() != adminUserID {
		t.Errorf("user del token = %s, want el usuario que resolvió el teléfono %s", claims.UserID, adminUserID)
	}

	// El cuerpo no lleva nada más: ni hash, ni tenant_id, ni datos del registro.
	if strings.Contains(recorder.Body.String(), "password") || strings.Contains(recorder.Body.String(), "tenant_id") {
		t.Errorf("la respuesta expone campos fuera del contrato: %q", recorder.Body.String())
	}
}

// TestAdminLoginAnswersTheSameGeneric401ForEveryFailure: sin enumeración de usuarios ni
// distinción de causa. El cuerpo es el MISMO byte a byte en los cuatro caminos (S13).
func TestAdminLoginAnswersTheSameGeneric401ForEveryFailure(t *testing.T) {
	stored := hashOf(t, adminPassword)

	cases := []struct {
		name    string
		users   identity.UserDirectory
		tenants identity.TenantDirectory
	}{
		{
			name:  "teléfono inexistente",
			users: stubUserDirectory{err: identity.ErrUnknownPhone},
			tenants: stubTenantDirectory{credential: identity.TenantCredential{
				Name: "Bodega", MasterPasswordHash: stored}},
		},
		{
			name: "contraseña incorrecta",
			users: stubUserDirectory{credentials: identity.Credentials{
				UserID: uuid.MustParse(adminUserID), TenantID: uuid.MustParse(adminTenantID), PasswordHash: stored}},
			tenants: stubTenantDirectory{credential: identity.TenantCredential{
				Name: "Bodega", MasterPasswordHash: stored}},
		},
		{
			name: "tenant sin registro",
			users: stubUserDirectory{credentials: identity.Credentials{
				UserID: uuid.MustParse(adminUserID), TenantID: uuid.MustParse(adminTenantID), PasswordHash: stored}},
			tenants: stubTenantDirectory{err: identity.ErrUnknownTenant},
		},
		{
			name: "hash ilegible en la base",
			users: stubUserDirectory{credentials: identity.Credentials{
				UserID: uuid.MustParse(adminUserID), TenantID: uuid.MustParse(adminTenantID), PasswordHash: stored}},
			tenants: stubTenantDirectory{credential: identity.TenantCredential{
				Name: "Bodega", MasterPasswordHash: "no-es-un-argon2id"}},
		},
	}

	var reference string
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := adminFixture(t, testCase.users, testCase.tenants, nil)

			attempt := adminPassword
			if testCase.name == "contraseña incorrecta" {
				attempt = "otra-clave"
			}
			recorder := doAdminLogin(t, handler, adminLoginBody(adminPhone, attempt))

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (%q)", recorder.Code, recorder.Body.String())
			}

			body := recorder.Body.String()
			if reference == "" {
				reference = body

				return
			}
			if body != reference {
				t.Errorf("el cuerpo del 401 no es constante:\n got: %q\nwant: %q", body, reference)
			}
		})
	}
}

// TestAdminLoginValidatesTheBodyBeforeTouchingTheStore: un cuerpo incompleto es 422 y no corre
// ninguna consulta.
func TestAdminLoginValidatesTheBodyBeforeTouchingTheStore(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "sin teléfono", body: `{"master_password":"clave"}`},
		{name: "teléfono en blanco", body: `{"phone":"   ","master_password":"clave"}`},
		{name: "sin contraseña", body: `{"phone":"` + adminPhone + `"}`},
		{name: "contraseña vacía", body: `{"phone":"` + adminPhone + `","master_password":""}`},
		{name: "cuerpo que no es JSON", body: `{`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler, _ := defaultAdminFixture(t, nil)

			recorder := doAdminLogin(t, handler, testCase.body)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"error"`) {
				t.Errorf("la respuesta no usa el sobre único: %q", recorder.Body.String())
			}
		})
	}
}

// TestAdminLoginNeverLogsTheCredential: la línea del 200 nombra tenant y usuario; la del 401 no
// nombra ni el teléfono. Ni la contraseña, ni el hash, ni el token aparecen nunca (W8-BE-4).
func TestAdminLoginNeverLogsTheCredential(t *testing.T) {
	var logBuffer strings.Builder
	handler, stored := defaultAdminFixture(t, &logBuffer)

	success := doAdminLogin(t, handler, adminLoginBody(adminPhone, adminPassword))
	if success.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", success.Code)
	}

	failed := doAdminLogin(t, handler, adminLoginBody(adminPhone, "clave-equivocada"))
	if failed.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", failed.Code)
	}

	var token struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(success.Body.Bytes(), &token)

	logged := logBuffer.String()
	for _, forbidden := range []string{adminPassword, "clave-equivocada", stored, adminPhone, token.Token} {
		if forbidden != "" && strings.Contains(logged, forbidden) {
			t.Errorf("el registro contiene un dato que no debe aparecer (%q): %q", forbidden, logged)
		}
	}
	if !strings.Contains(logged, adminTenantID) {
		t.Errorf("el registro no nombra el tenant: %q", logged)
	}
}

// TestAdminLoginFailsClosedWithoutItsDependencies: un handler a medias no se construye.
func TestAdminLoginFailsClosedWithoutItsDependencies(t *testing.T) {
	users := stubUserDirectory{}
	tenants := stubTenantDirectory{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := identity.NewAdminLoginHandler(nil, tenants, adminIssuer(t), logger); err == nil {
		t.Error("NewAdminLoginHandler sin directorio de usuarios no falló")
	}
	if _, err := identity.NewAdminLoginHandler(users, nil, adminIssuer(t), logger); err == nil {
		t.Error("NewAdminLoginHandler sin directorio de tenants no falló")
	}
	if _, err := identity.NewAdminLoginHandler(users, tenants, nil, logger); err == nil {
		t.Error("NewAdminLoginHandler sin emisor no falló")
	}
	if _, err := identity.NewAdminLoginHandler(users, tenants, adminIssuer(t), nil); err == nil {
		t.Error("NewAdminLoginHandler sin logger no falló")
	}
}

// TestAdminLoginRejectsADatabaseFailureAsTheGenericEnvelope: un fallo real de la base no es
// «credenciales inválidas» —sería mentirle al cliente— y tampoco filtra la causa.
func TestAdminLoginRejectsADatabaseFailureAsTheGenericEnvelope(t *testing.T) {
	handler := adminFixture(t,
		stubUserDirectory{err: errors.New("connection refused: no route to host")},
		stubTenantDirectory{},
		nil,
	)

	recorder := doAdminLogin(t, handler, adminLoginBody(adminPhone, adminPassword))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%q)", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("connection refused")) {
		t.Errorf("el 500 filtra la causa: %q", recorder.Body.String())
	}
}
