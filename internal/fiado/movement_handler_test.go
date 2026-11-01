// Pruebas unitarias de la superficie de fiado (W7-BE-1, W7-BE-2): el handler contra un
// store de mentira, sin base de datos. Lo que se fija acá es la FORMA de la superficie —el
// sobre de error único de §4.1, el mapeo de las tres ramas del claim de §7.1 y la respuesta
// de §4.12— y no el SQL, que vive en las pruebas de integración.
package fiado_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/fiado"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// stubMovementStore es el store de las pruebas unitarias: registra lo que el handler le pidió
// y devuelve lo que la prueba fijó —incluido un fallo— sin base de datos.
type stubMovementStore struct {
	calls []movementCall

	ack []byte
	err error
}

// movementCall es UNA llamada del handler al store.
type movementCall struct {
	tenantID   uuid.UUID
	movement   fiado.MovementInput
	receivedAt time.Time
	claim      synckey.ClaimInput
}

// AppendMovement registra la llamada y devuelve el desenlace fijado.
func (s *stubMovementStore) AppendMovement(_ context.Context, tenantID uuid.UUID, movement fiado.MovementInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	s.calls = append(s.calls, movementCall{tenantID: tenantID, movement: movement, receivedAt: receivedAt, claim: claim})
	return s.ack, s.err
}

// newMovementRouter monta el router REAL con el handler sobre el store de mentira.
func newMovementRouter(t *testing.T, store fiado.MovementStore) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	handler, err := fiado.NewFiadoMovementHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewFiadoMovementHandler: %v", err)
	}

	tokenIssuer := newTestIssuer(t)

	return newRouter(t, func(mux *http.ServeMux) {
		mux.Handle("POST /fiado-movements", identity.RequireAccessToken(tokenIssuer, handler))
	}, tokenIssuer)
}

// newRouter arma el router con el middleware de la petición y el sondeo listo.
func newRouter(t *testing.T, mount func(*http.ServeMux), tokenIssuer *identity.TokenIssuer) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	return httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(mount),
	), tokenIssuer
}

// newTestIssuer construye el emisor de tokens de las pruebas.
func newTestIssuer(t *testing.T) *identity.TokenIssuer {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}
	return issuer
}

// issueToken firma un token de empleado para el tenant y el usuario de referencia.
func issueToken(t *testing.T, issuer *identity.TokenIssuer) string {
	t.Helper()

	token, err := issuer.Issue(identity.Claims{
		TenantID: mustParseUUID(t, tokenTenantID),
		UserID:   mustParseUUID(t, tokenUserID),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// dispatch envía una petición al router con el token y las cabeceras pedidas.
func dispatch(t *testing.T, router http.Handler, method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, path, reader)
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

// doMovementRequest envía un POST /fiado-movements. Una entrada Idempotency-Key propia
// —aunque sea vacía— es la única forma de probar la ausencia del encabezado, porque el
// helper inyecta la clave de referencia cuando la prueba no la menciona.
func doMovementRequest(t *testing.T, router http.Handler, token, body string, extraHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	headers := map[string]string{}
	_, keyProvided := extraHeaders["Idempotency-Key"]
	for name, value := range extraHeaders {
		headers[name] = value
	}
	if !keyProvided {
		headers["Idempotency-Key"] = defaultIdempotencyKey
	}

	return dispatch(t, router, http.MethodPost, "/fiado-movements", token, body, headers)
}

// envelopeError es el sobre de error único (§4.1).
type envelopeError struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// decodeEnvelope decodifica el sobre de error de una respuesta.
func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelopeError {
	t.Helper()

	var envelope envelopeError
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("el sobre de error no es JSON: %v (%q)", err, recorder.Body.String())
	}
	return envelope
}

// validMovementBody es el cuerpo de referencia de §4.11 con el registro del deudor incluido
// (decisión D1 de forma de odd/tasks/sprint-7-fiados.md).
func validMovementBody() string {
	return `{
		"id": "` + movementID + `",
		"fiado_account_id": "` + accountID + `",
		"fiado_account": {"name": "Rosa Quispe", "phone": "+51987111222", "updated_at": "2026-10-26T12:00:00Z"},
		"amount_cents": 1500,
		"note": "Venta fiada",
		"sold_on": "` + businessDate + `",
		"sale_id": "` + saleID + `"
	}`
}

// TestMovementHandlerNeedsTheTokenClaims: sin el middleware no hay tenant del que escribir,
// y el handler responde 500 fail-closed sin tocar el store.
func TestMovementHandlerNeedsTheTokenClaims(t *testing.T) {
	store := &stubMovementStore{}
	handler, err := fiado.NewFiadoMovementHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewFiadoMovementHandler: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/fiado-movements", strings.NewReader(validMovementBody())))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 sin claims", recorder.Code)
	}
	if len(store.calls) != 0 {
		t.Errorf("el store corrió %d vez(es) sin token, want 0", len(store.calls))
	}
}

// TestMovementHandlerRequiresTheIdempotencyKey: el claim es obligatorio (§4.11) y sin
// encabezado la petición es inservible.
func TestMovementHandlerRequiresTheIdempotencyKey(t *testing.T) {
	router, issuer := newMovementRouter(t, &stubMovementStore{})

	recorder := doMovementRequest(t, router, issueToken(t, issuer), validMovementBody(), map[string]string{"Idempotency-Key": ""})

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 sin Idempotency-Key (%q)", recorder.Code, recorder.Body.String())
	}
	if field := decodeEnvelope(t, recorder).Error.Details["field"]; field != "Idempotency-Key" {
		t.Errorf("details.field = %v, want Idempotency-Key", field)
	}
}

// TestMovementHandlerValidatesTheBody: cada cuerpo inservible devuelve 422 y nombra el campo
// culpable, jamás el valor recibido (§4.1; §4.11 para el par de nulidad y el monto).
func TestMovementHandlerValidatesTheBody(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{
			name:  "un id que no es uuid",
			body:  `{"id": "no-es-uuid", "fiado_account_id": "` + accountID + `", "amount_cents": 1500}`,
			field: "id",
		},
		{
			name:  "una cuenta que no es uuid",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "nope", "amount_cents": 1500}`,
			field: "fiado_account_id",
		},
		{
			name:  "un monto ausente",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `"}`,
			field: "amount_cents",
		},
		{
			name:  "un monto cero",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "amount_cents": 0}`,
			field: "amount_cents",
		},
		{
			name:  "un sold_on sin sale_id",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "amount_cents": 1500, "sold_on": "` + businessDate + `"}`,
			field: "sold_on",
		},
		{
			name:  "un sale_id sin sold_on",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "amount_cents": 1500, "sale_id": "` + saleID + `"}`,
			field: "sale_id",
		},
		{
			name:  "un sold_on que no es fecha",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "amount_cents": 1500, "sold_on": "26/10/2026", "sale_id": "` + saleID + `"}`,
			field: "sold_on",
		},
		{
			name:  "un deudor sin nombre",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "fiado_account": {"name": "   "}, "amount_cents": 1500}`,
			field: "fiado_account.name",
		},
		{
			name:  "una marca de mutación ilegible",
			body:  `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "fiado_account": {"name": "Rosa", "updated_at": "ayer"}, "amount_cents": 1500}`,
			field: "fiado_account.updated_at",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &stubMovementStore{}
			router, issuer := newMovementRouter(t, store)

			recorder := doMovementRequest(t, router, issueToken(t, issuer), testCase.body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
			}
			if field := decodeEnvelope(t, recorder).Error.Details["field"]; field != testCase.field {
				t.Errorf("details.field = %v, want %q", field, testCase.field)
			}
			if len(store.calls) != 0 {
				t.Errorf("el store corrió %d vez(es) con un cuerpo inservible, want 0", len(store.calls))
			}
		})
	}
}

// TestMovementHandlerMapsTheClaimBranches: el 422 del payload reutilizado, el 409 con
// Retry-After del duplicado en vuelo, y el 422 de una cuenta fuera del tenant (§7.1, §4.11).
func TestMovementHandlerMapsTheClaimBranches(t *testing.T) {
	cases := []struct {
		name       string
		storeErr   error
		wantStatus int
		wantCode   string
		wantField  string
		wantRetry  string
	}{
		{
			name:       "la misma clave con otro payload",
			storeErr:   synckey.ErrPayloadMismatch,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   payloadMismatchCode,
			wantField:  "Idempotency-Key",
		},
		{
			name:       "el duplicado en vuelo",
			storeErr:   synckey.ErrInFlight,
			wantStatus: http.StatusConflict,
			wantRetry:  "2",
		},
		{
			name:       "una cuenta que el tenant no ve",
			storeErr:   fiado.ErrUnknownAccount,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "fiado_account_id",
		},
		{
			name:       "una venta enlazada que el tenant no ve",
			storeErr:   fiado.ErrUnknownSale,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "sale_id",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router, issuer := newMovementRouter(t, &stubMovementStore{err: testCase.storeErr})

			recorder := doMovementRequest(t, router, issueToken(t, issuer), validMovementBody(), nil)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (%q)", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if testCase.wantCode != "" {
				if code := decodeEnvelope(t, recorder).Error.Code; code != testCase.wantCode {
					t.Errorf("error.code = %q, want %q", code, testCase.wantCode)
				}
			}
			if testCase.wantField != "" {
				if field := decodeEnvelope(t, recorder).Error.Details["field"]; field != testCase.wantField {
					t.Errorf("details.field = %v, want %q", field, testCase.wantField)
				}
			}
			if testCase.wantRetry != "" {
				if got := recorder.Header().Get("Retry-After"); got != testCase.wantRetry {
					t.Errorf("Retry-After = %q, want %q", got, testCase.wantRetry)
				}
			}
		})
	}
}

// TestMovementHandlerPassesTheParsedInputToTheStore: el tenant sale SOLO del token, el claim
// lleva la clave y el hash del cuerpo RAW, y el par de nulidad y el deudor llegan parseados.
func TestMovementHandlerPassesTheParsedInputToTheStore(t *testing.T) {
	store := &stubMovementStore{ack: []byte(`{"movement":{},"account":{}}`)}
	router, issuer := newMovementRouter(t, store)

	recorder := doMovementRequest(t, router, issueToken(t, issuer), validMovementBody(), nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}
	if len(store.calls) != 1 {
		t.Fatalf("llamadas al store = %d, want 1", len(store.calls))
	}

	call := store.calls[0]
	if call.tenantID.String() != tokenTenantID {
		t.Errorf("tenant del store = %s, want el del token %s", call.tenantID, tokenTenantID)
	}
	if call.claim.Key != defaultIdempotencyKey {
		t.Errorf("claim.key = %q, want %q", call.claim.Key, defaultIdempotencyKey)
	}
	if call.claim.PayloadHash != synckey.PayloadHash(defaultIdempotencyKey, []byte(validMovementBody())) {
		t.Errorf("claim.payload_hash no es el hash del cuerpo RAW")
	}
	if call.movement.AmountCents != 1500 {
		t.Errorf("amount_cents = %d, want 1500", call.movement.AmountCents)
	}
	if call.movement.Account == nil || call.movement.Account.Name != "Rosa Quispe" {
		t.Fatalf("fiado_account.name no llegó parseado: %+v", call.movement.Account)
	}
	if call.movement.Account.Phone == nil || *call.movement.Account.Phone != "+51987111222" {
		t.Errorf("fiado_account.phone no llegó parseado: %+v", call.movement.Account.Phone)
	}
	if call.movement.SaleID == nil || call.movement.SaleID.String() != saleID {
		t.Errorf("sale_id no llegó parseado: %+v", call.movement.SaleID)
	}
	if call.movement.SoldOn == nil || call.movement.SoldOn.Format("2006-01-02") != businessDate {
		t.Errorf("sold_on no llegó parseado: %+v", call.movement.SoldOn)
	}
}

// TestMovementHandlerAnswersTheFrozenAck: la respuesta es el acuse que el store armó, byte a
// byte — en Branch B el fresco y en Branch A el snapshot almacenado.
func TestMovementHandlerAnswersTheFrozenAck(t *testing.T) {
	ack := []byte(`{"movement":{"id":"` + movementID + `"},"account":{"id":"` + accountID + `","balance_cents":1500}}`)
	router, issuer := newMovementRouter(t, &stubMovementStore{ack: ack})

	recorder := doMovementRequest(t, router, issueToken(t, issuer), validMovementBody(), nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if strings.TrimSpace(recorder.Body.String()) != string(ack) {
		t.Errorf("body = %q, want el acuse almacenado %q", recorder.Body.String(), ack)
	}
}

// TestMovementHandlerFailsClosedWithoutTheStore: un handler sin store no se construye — el
// contrato de tipos no deja un camino donde el handler corra sin dueño de datos.
func TestMovementHandlerFailsClosedWithoutTheStore(t *testing.T) {
	if _, err := fiado.NewFiadoMovementHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Error("NewFiadoMovementHandler(nil) no falló")
	}
}
