package identity_test

import (
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
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// bindCall es una llamada registrada al enlazador. Guarda el hash y NO el PIN: el handler no
// tiene por qué entregar la credencial en claro ni siquiera a su colaborador.
type bindCall struct {
	tenantID uuid.UUID
	userID   uuid.UUID
	deviceID uuid.UUID
	pinHash  string
	bindTime time.Time
}

// stubBinder es el enlazador de las pruebas unitarias: registra lo que el handler le pidió y
// devuelve lo que la prueba fije —incluido un fallo— sin base de datos.
type stubBinder struct {
	calls   []bindCall
	binding identity.DeviceBinding
	err     error
}

func (s *stubBinder) BindDevice(_ context.Context, tenantID, userID, deviceID uuid.UUID, pinHash string, bindTime time.Time) (identity.DeviceBinding, error) {
	s.calls = append(s.calls, bindCall{tenantID: tenantID, userID: userID, deviceID: deviceID, pinHash: pinHash, bindTime: bindTime})
	if s.err != nil {
		return identity.DeviceBinding{}, s.err
	}
	return s.binding, nil
}

// newDeviceBindRouter arma el router REAL del proceso —los sondeos, el registro de peticiones
// y la ruta montada por opción, como en cmd/api— con `POST /auth/device-bind` detrás del
// middleware de token. Devuelve el emisor para firmar tokens de prueba.
func newDeviceBindRouter(t *testing.T, binder identity.DeviceBinder, ttl time.Duration, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, ttl)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := identity.NewDeviceBindHandler(binder, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewDeviceBindHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /auth/device-bind", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// deviceToken firma un access token de empleado para el tenant y el usuario dados.
func deviceToken(t *testing.T, issuer *identity.TokenIssuer, tenantID, userID string) string {
	t.Helper()

	token, err := issuer.Issue(identity.Claims{
		TenantID: uuid.MustParse(tenantID),
		UserID:   uuid.MustParse(userID),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// postDeviceBind envía un cuerpo a la ruta. Un token vacío omite la cabecera de credencial.
func postDeviceBind(t *testing.T, router http.Handler, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// deviceBindPayload es la respuesta 200 del contrato §4.3.
type deviceBindPayload struct {
	Device struct {
		ID         string `json:"id"`
		ValidUntil string `json:"offline_session_valid_until"`
	} `json:"device"`
}

// decodeDeviceBindPayload lee la respuesta 200 y falla si no tiene la forma del contrato.
func decodeDeviceBindPayload(t *testing.T, recorder *httptest.ResponseRecorder) deviceBindPayload {
	t.Helper()

	var payload deviceBindPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("la respuesta no es el JSON de §4.3: %v (%q)", err, recorder.Body.String())
	}
	if payload.Device.ID == "" || payload.Device.ValidUntil == "" {
		t.Fatalf("la respuesta no trae device.id y device.offline_session_valid_until: %q", recorder.Body.String())
	}
	return payload
}

// decodeEnvelope lee el sobre de error único (§4.1) de una respuesta.
func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) loginEnvelope {
	t.Helper()

	var envelope loginEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error único: %v (%q)", err, recorder.Body.String())
	}
	return envelope
}

// TestDeviceBindRejectsAPinThatIsNotExactlyFourDigits cubre la validación del cuerpo: el PIN
// tiene que ser de cuatro dígitos (docs/DESIGN.md:105). Ninguna forma inválida llega al
// enlazador ni devuelve el valor recibido.
func TestDeviceBindRejectsAPinThatIsNotExactlyFourDigits(t *testing.T) {
	cases := map[string]string{
		"absent":            `{}`,
		"empty":             `{"pin":""}`,
		"three digits":      `{"pin":"123"}`,
		"five digits":       `{"pin":"12345"}`,
		"a letter":          `{"pin":"12a4"}`,
		"a space inside":    `{"pin":"12 4"}`,
		"padded":            `{"pin":" 1234"}`,
		"a trailing space":  `{"pin":"1234 "}`,
		"a newline":         `{"pin":"1234\n"}`,
		"full-width digits": `{"pin":"１２３４"}`,
		"not json":          `pin=1234`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			binder := &stubBinder{}
			var logBuf strings.Builder
			router, issuer := newDeviceBindRouter(t, binder, time.Hour, &logBuf)
			token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

			recorder := postDeviceBind(t, router, token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(binder.calls) != 0 {
				t.Errorf("el enlazador corrió %d vez/veces con un cuerpo inválido, want 0", len(binder.calls))
			}

			envelope := decodeEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			// El detalle puede nombrar el campo; nunca el valor recibido.
			for _, received := range []string{"12345", "12a4", "1234", "１２３４"} {
				if strings.Contains(recorder.Body.String(), received) {
					t.Errorf("el 422 repite el PIN recibido (%q): %q", received, recorder.Body.String())
				}
			}
		})
	}
}

// TestDeviceBindNamesThePinFieldInTheDetails documenta la forma del detalle: el campo que
// falló, sin el valor.
func TestDeviceBindNamesThePinFieldInTheDetails(t *testing.T) {
	binder := &stubBinder{}
	router, issuer := newDeviceBindRouter(t, binder, time.Hour, &strings.Builder{})
	token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

	recorder := postDeviceBind(t, router, token, `{"pin":"123"}`, nil)

	envelope := decodeEnvelope(t, recorder)
	details, ok := envelope.Error.Details.(map[string]any)
	if !ok {
		t.Fatalf("details = %#v, want un objeto que nombre el campo", envelope.Error.Details)
	}
	if details["field"] != "pin" {
		t.Errorf("details.field = %v, want \"pin\"", details["field"])
	}
}

// TestDeviceBindRejectsADeviceIDThatIsNotAUsableUUID: el `device_id` es opcional, pero cuando
// viaja tiene que ser un UUID con el que se pueda identificar una fila. Un valor presente y
// vacío no se lee como ausencia: tratarlo así crearía un dispositivo nuevo en silencio.
func TestDeviceBindRejectsADeviceIDThatIsNotAUsableUUID(t *testing.T) {
	cases := map[string]string{
		"not a uuid":     `{"pin":"1234","device_id":"no-soy-un-uuid"}`,
		"the nil uuid":   `{"pin":"1234","device_id":"00000000-0000-0000-0000-000000000000"}`,
		"a number":       `{"pin":"1234","device_id":7}`,
		"an empty value": `{"pin":"1234","device_id":""}`,
		"a blank value":  `{"pin":"1234","device_id":"   "}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			binder := &stubBinder{}
			router, issuer := newDeviceBindRouter(t, binder, time.Hour, &strings.Builder{})
			token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

			recorder := postDeviceBind(t, router, token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(binder.calls) != 0 {
				t.Errorf("el enlazador corrió con un device_id inválido")
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeValidationError)
			}
		})
	}
}

// TestDeviceBindCreatesTheDeviceWhenTheDeviceIDIsAbsentOrNull: «no tengo ninguno» se expresa
// omitiendo el campo o mandándolo nulo, y las dos formas significan que el servidor crea la
// fila (§4.3). Es el control que hace falsable la tabla de rechazo de arriba.
func TestDeviceBindCreatesTheDeviceWhenTheDeviceIDIsAbsentOrNull(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"pin":"1234"}`,
		"null":   `{"pin":"1234","device_id":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			binder := &stubBinder{binding: identity.DeviceBinding{
				DeviceID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000e5"),
				ValidUntil: time.Now().Add(identity.MinimumOfflineSessionWindow),
			}}
			router, issuer := newDeviceBindRouter(t, binder, time.Hour, &strings.Builder{})
			token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

			recorder := postDeviceBind(t, router, token, body, nil)

			if recorder.Code != http.StatusOK {
				t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(binder.calls) != 1 {
				t.Fatalf("el enlazador corrió %d vez/veces, want 1", len(binder.calls))
			}
			if got := binder.calls[0].deviceID; got != uuid.Nil {
				t.Errorf("device_id del enlace = %v, want el UUID nulo: el servidor genera el id", got)
			}
		})
	}
}

// TestDeviceBindTakesTheTenantOnlyFromTheToken es el criterio de aceptación de aislamiento en
// la capa del handler: el cuerpo y los encabezados pueden traer el tenant que quieran, y el
// único tenant que llega al enlazador es el del token. Un campo de tenant desconocido se
// ignora —no se rechaza con 422— porque el contrato no define uno.
func TestDeviceBindTakesTheTenantOnlyFromTheToken(t *testing.T) {
	binder := &stubBinder{binding: identity.DeviceBinding{
		DeviceID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000e1"),
		ValidUntil: time.Now().Add(7 * 24 * time.Hour),
	}}
	router, issuer := newDeviceBindRouter(t, binder, time.Hour, &strings.Builder{})
	token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

	body := `{"pin":"1234","tenant_id":"` + forgedTenantID + `","tenantId":"` + forgedTenantID + `"}`
	recorder := postDeviceBind(t, router, token, body, map[string]string{
		"X-Tenant-Id": forgedTenantID,
		"X-Tenant":    forgedTenantID,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200: el tenant desconocido del cuerpo se ignora, no se rechaza (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), forgedTenantID) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", recorder.Body.String())
	}
	if len(binder.calls) != 1 {
		t.Fatalf("el enlazador corrió %d vez/veces, want 1", len(binder.calls))
	}

	call := binder.calls[0]
	if call.tenantID.String() != tokenTenantID {
		t.Errorf("tenant del enlace = %v, want el del token %s (nunca el del cuerpo ni el del encabezado)", call.tenantID, tokenTenantID)
	}
	if call.userID.String() != tokenUserID {
		t.Errorf("usuario del enlace = %v, want el del token %s", call.userID, tokenUserID)
	}
	if call.deviceID != uuid.Nil {
		t.Errorf("device_id = %v, want el UUID nulo: sin device_id el servidor crea la fila", call.deviceID)
	}
}

// TestDeviceBindAnswersTheContractEnvelope: la respuesta 200 es exactamente la de §4.3, el PIN
// se guarda como hash argon2id y la credencial en claro no aparece ni en la respuesta ni en el
// registro del proceso (§7.9).
func TestDeviceBindAnswersTheContractEnvelope(t *testing.T) {
	const pin = "4321"

	deviceID := uuid.MustParse("019a0000-0000-7000-8000-0000000000e2")
	validUntil := time.Date(2026, 10, 23, 15, 4, 5, 123456789, time.UTC)

	binder := &stubBinder{binding: identity.DeviceBinding{DeviceID: deviceID, ValidUntil: validUntil}}
	var logBuf strings.Builder
	router, issuer := newDeviceBindRouter(t, binder, time.Hour, &logBuf)
	token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

	recorder := postDeviceBind(t, router, token, `{"pin":"`+pin+`"}`, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	payload := decodeDeviceBindPayload(t, recorder)
	if payload.Device.ID != deviceID.String() {
		t.Errorf("device.id = %q, want %q", payload.Device.ID, deviceID)
	}
	// La ventana viaja en RFC3339, en UTC y sin fracción de segundo: el formato del contrato.
	if want := validUntil.UTC().Format(time.RFC3339); payload.Device.ValidUntil != want {
		t.Errorf("device.offline_session_valid_until = %q, want %q", payload.Device.ValidUntil, want)
	}

	// El cuerpo es el del contrato: la clave `device` y nada más.
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("la respuesta no es un objeto JSON: %v", err)
	}
	if len(decoded) != 1 {
		t.Errorf("la respuesta tiene %d claves (%v), want solo \"device\"", len(decoded), decoded)
	}

	if len(binder.calls) != 1 {
		t.Fatalf("el enlazador corrió %d vez/veces, want 1", len(binder.calls))
	}

	// El PIN se guarda como hash: el enlazador recibe una cadena argon2id verificable y nunca
	// la credencial en claro.
	hash := binder.calls[0].pinHash
	if strings.HasPrefix(hash, pin) || strings.Contains(hash, pin) {
		t.Errorf("el enlazador recibió el PIN en claro: %q", hash)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("el hash no es argon2id en formato PHC: %q", hash)
	}
	if matches, err := identity.VerifyCredential(hash, pin); err != nil || !matches {
		t.Errorf("VerifyCredential(hash, pin) = (%v, %v), want (true, nil)", matches, err)
	}

	if strings.Contains(recorder.Body.String(), pin) {
		t.Errorf("la respuesta contiene el PIN: %q", recorder.Body.String())
	}
	if strings.Contains(logBuf.String(), pin) {
		t.Errorf("el registro del proceso contiene el PIN: %q", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), deviceID.String()) {
		t.Errorf("el registro del proceso no lleva el id del dispositivo: %q", logBuf.String())
	}

	// El instante del bind es la hora en la que el handler atendió: el enlazador no lo inventa.
	if binder.calls[0].bindTime.IsZero() {
		t.Error("el enlazador recibió un instante de bind cero")
	}
	if time.Since(binder.calls[0].bindTime) > time.Minute {
		t.Errorf("el instante de bind = %v, want el momento de la petición", binder.calls[0].bindTime)
	}
}

// TestDeviceBindSendsAProvidedDeviceID: con `device_id` en el cuerpo, el handler lo entrega
// como el uuid que se va a actualizar —no crea otro dispositivo (decisión D5)—.
func TestDeviceBindSendsAProvidedDeviceID(t *testing.T) {
	const existing = "019a0000-0000-7000-8000-0000000000e3"

	binder := &stubBinder{binding: identity.DeviceBinding{
		DeviceID:   uuid.MustParse(existing),
		ValidUntil: time.Now().Add(7 * 24 * time.Hour),
	}}
	router, issuer := newDeviceBindRouter(t, binder, time.Hour, &strings.Builder{})
	token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

	recorder := postDeviceBind(t, router, token, `{"pin":"1234","device_id":"`+existing+`"}`, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if len(binder.calls) != 1 {
		t.Fatalf("el enlazador corrió %d vez/veces, want 1", len(binder.calls))
	}
	if got := binder.calls[0].deviceID.String(); got != existing {
		t.Errorf("device_id del enlace = %q, want %q", got, existing)
	}
	if got := decodeDeviceBindPayload(t, recorder).Device.ID; got != existing {
		t.Errorf("device.id = %q, want %q", got, existing)
	}
}

// TestDeviceBindAnswers404ForADeviceOutsideTheTenant cubre la traducción del 404 de §4.1: un
// device_id que el RLS no deja ver es «inexistente o fuera del tenant», y las dos causas
// devuelven la misma respuesta.
func TestDeviceBindAnswers404ForADeviceOutsideTheTenant(t *testing.T) {
	binder := &stubBinder{err: identity.ErrUnknownDevice}
	var logBuf strings.Builder
	router, issuer := newDeviceBindRouter(t, binder, time.Hour, &logBuf)
	token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

	recorder := postDeviceBind(t, router, token, `{"pin":"1234","device_id":"019a0000-0000-7000-8000-0000000000e4"}`, nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("estado = %d, want 404 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeNotFound {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeNotFound)
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "tenant") {
		t.Errorf("el 404 distingue «fuera del tenant» de «no existe»: %q", recorder.Body.String())
	}
	if strings.Contains(logBuf.String(), "1234") {
		t.Errorf("el registro del proceso contiene el PIN: %q", logBuf.String())
	}
}

// TestDeviceBindFailsClosedWhenTheStoreIsBroken: un fallo del driver no es un 404 ni un 422; es
// un 500 con el sobre genérico, y la causa queda en el registro del operador.
func TestDeviceBindFailsClosedWhenTheStoreIsBroken(t *testing.T) {
	binder := &stubBinder{err: errors.New("the tenant pool has no connection pool")}
	var logBuf strings.Builder
	router, issuer := newDeviceBindRouter(t, binder, time.Hour, &logBuf)
	token := deviceToken(t, issuer, tokenTenantID, tokenUserID)

	recorder := postDeviceBind(t, router, token, `{"pin":"1234"}`, nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
	}
	if strings.Contains(recorder.Body.String(), "pool") {
		t.Errorf("el 500 filtró la causa interna: %q", recorder.Body.String())
	}
	if !strings.Contains(logBuf.String(), "pool") {
		t.Errorf("la causa no llegó al registro del operador: %q", logBuf.String())
	}
}

// TestDeviceBindAnswers401WithoutAValidToken cierra el criterio de aceptación del 401 sobre el
// router real: sin token, con un token vencido y con un token de otra clave, la ruta responde
// 401 y el enlazador no corre.
func TestDeviceBindAnswers401WithoutAValidToken(t *testing.T) {
	cases := map[string]string{
		"no token":         "",
		"an expired token": expiredToken(t),
		"not a jwt at all": "no-soy-un-token",
	}

	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			binder := &stubBinder{}
			router, _ := newDeviceBindRouter(t, binder, time.Hour, &strings.Builder{})

			recorder := postDeviceBind(t, router, token, `{"pin":"1234"}`, nil)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(binder.calls) != 0 {
				t.Errorf("el enlazador corrió %d vez/veces sin un token válido, want 0", len(binder.calls))
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeUnauthenticated {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeUnauthenticated)
			}
		})
	}
}

// TestDeviceBindWithoutTheMiddlewareFailsClosed: si un montaje olvidara el middleware, el
// handler no puede leer un tenant inexistente como si fuera un tenant válido. Responde 500 y
// no escribe nada.
func TestDeviceBindWithoutTheMiddlewareFailsClosed(t *testing.T) {
	binder := &stubBinder{}
	var logBuf strings.Builder

	handler, err := identity.NewDeviceBindHandler(binder, slog.New(slog.NewTextHandler(&logBuf, nil)))
	if err != nil {
		t.Fatalf("NewDeviceBindHandler: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(`{"pin":"1234"}`)))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500: sin claims no hay tenant del que escribir la fila (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
	}
	if len(binder.calls) != 0 {
		t.Errorf("el enlazador corrió %d vez/veces sin claims, want 0", len(binder.calls))
	}
	if !strings.Contains(logBuf.String(), "middleware") {
		t.Errorf("la causa no quedó registrada: %q", logBuf.String())
	}
}
