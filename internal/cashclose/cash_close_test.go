// Pruebas unitarias de POST /cash-closes (W5-BE-1/W5-BE-3): el contrato de la superficie
// HTTP sobre un store stub, sin base de datos. El router es el real del proceso, en proceso
// (httptest), porque la respuesta y el sobre de error ÚNICO de §4.1 se deciden en el
// transporte.
//
// Lo no negociable que se prueba acá: el tenant y el usuario son SOLO los del token
// (ARCHITECTURE.md:23); el cuerpo trae EXACTAMENTE seis conteos, uno por cada método del
// CHECK (docs/DATABASE.md §4 `cash_close_counts`, D11); el fiado contado distinto de cero
// es un 422 de superficie además del CHECK estructural (F32); y el `expected_cents` NO
// viaja en el cuerpo — lo congela el servidor con F8—.
package cashclose_test

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

	"github.com/AriasYarangaIam/CheckPOS/internal/cashclose"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

const (
	// testSigningSecret es la clave de firma de las pruebas: 32 bytes, el piso del contrato.
	testSigningSecret = "test-only-signing-key-0123456789abcdef"
	// tokenTenantID es el tenant DENTRO del token: la única fuente admisible.
	tokenTenantID = "019a0000-0000-7000-8000-0000000000a1"
	// tokenUserID es el usuario del token: el cajero que cierra.
	tokenUserID = "019a0000-0000-7000-8000-0000000000a2"
	// closeID es el id del cierre que el cuerpo de referencia envía.
	closeID = "019a0000-0000-7000-8000-0000000000a3"
	// otherCloseID es el id del cierre de la segunda petición del duplicado.
	otherCloseID = "019a0000-0000-7000-8000-0000000000a4"
	// defaultIdempotencyKey es la clave que el helper inyecta cuando la prueba no pide otra.
	defaultIdempotencyKey = "close-key-0001"
	// payloadMismatchCode es el código de superficie de Branch A con hashes distintos.
	payloadMismatchCode = "idempotency.payload_mismatch"
)

// alwaysReady es el sondeo de preparación del router de estas pruebas.
type alwaysReady struct{}

func (alwaysReady) Ping(context.Context) error { return nil }

// stubCashCloseStore es el store de las pruebas unitarias: registra lo que el handler le
// pidió y devuelve lo que la prueba fijó —incluido un fallo— sin base de datos.
type stubCashCloseStore struct {
	calls []closeCall

	ack []byte
	err error
}

type closeCall struct {
	tenantID   uuid.UUID
	userID     uuid.UUID
	close      cashclose.CloseInput
	receivedAt time.Time
	claim      synckey.ClaimInput
}

func (s *stubCashCloseStore) Close(_ context.Context, tenantID, userID uuid.UUID, close cashclose.CloseInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	s.calls = append(s.calls, closeCall{tenantID: tenantID, userID: userID, close: close, receivedAt: receivedAt, claim: claim})
	if s.err != nil {
		return nil, s.err
	}
	return s.ack, nil
}

// validCloseBody es el cuerpo de referencia: las seis filas, una por método, con conteos
// distintos para que un reordenamiento se note. El fiado va en cero por contrato.
func validCloseBody() string {
	return `{
		"id": "` + closeID + `",
		"closed_on": "2026-10-19",
		"counts": [
			{"payment_method": "cash", "counted_cents": 4500},
			{"payment_method": "yape", "counted_cents": 3000},
			{"payment_method": "plin", "counted_cents": 0},
			{"payment_method": "card", "counted_cents": 1200},
			{"payment_method": "other", "counted_cents": 0},
			{"payment_method": "fiado", "counted_cents": 0}
		]
	}`
}

// newCashCloseRouter arma el router REAL del proceso con /cash-closes detrás del middleware
// de token, igual que cmd/api la monte.
func newCashCloseRouter(t *testing.T, closes cashclose.CashCloseStore, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := cashclose.NewCashCloseHandler(closes, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewCashCloseHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /cash-closes", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// closeToken firma un access token de empleado para el tenant y el usuario de referencia.
func closeToken(t *testing.T, issuer *identity.TokenIssuer) string {
	t.Helper()

	token, err := issuer.Issue(identity.Claims{
		TenantID: uuid.MustParse(tokenTenantID),
		UserID:   uuid.MustParse(tokenUserID),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// doCloseRequest envía una petición al router. Una entrada Idempotency-Key propia —aunque
// sea vacía— es la única forma de probar la ausencia del encabezado, porque el helper
// inyecta la clave de referencia cuando la prueba no la menciona.
func doCloseRequest(t *testing.T, router http.Handler, token, body string, extraHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(http.MethodPost, "/cash-closes", reader)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	_, keyProvided := extraHeaders["Idempotency-Key"]
	for name, value := range extraHeaders {
		request.Header.Set(name, value)
	}
	if !keyProvided {
		request.Header.Set("Idempotency-Key", defaultIdempotencyKey)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// envelopeError es el sobre de error único (§4.1).
type envelopeError struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelopeError {
	t.Helper()

	var envelope envelopeError
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error único: %v (%q)", err, recorder.Body.String())
	}
	return envelope
}

// TestCashCloseHandsTheTokenTenantAndTheSixCountsToTheStore: el store recibe SOLO el tenant
// y el usuario del token, el cierre con su fecha de negocio, y las seis filas en el orden
// determinista del contrato —sin ningún expected_cents del cliente—.
func TestCashCloseHandsTheTokenTenantAndTheSixCountsToTheStore(t *testing.T) {
	var logs strings.Builder
	closes := &stubCashCloseStore{ack: []byte(`{"cash_close":{}}`)}
	router, issuer := newCashCloseRouter(t, closes, &logs)

	// Los métodos llegan DESORDENADOS a propósito: el handler los normaliza al orden fijo
	// del acuse (cash, yape, plin, card, other, fiado).
	body := `{
		"id": "` + closeID + `",
		"closed_on": "2026-10-19",
		"tenant_id": "019a0000-0000-7000-8000-0000000000ff",
		"counts": [
			{"payment_method": "fiado", "counted_cents": 0},
			{"payment_method": "card", "counted_cents": 1200},
			{"payment_method": "cash", "counted_cents": 4500},
			{"payment_method": "yape", "counted_cents": 3000},
			{"payment_method": "other", "counted_cents": 0},
			{"payment_method": "plin", "counted_cents": 0}
		]
	}`
	recorder := doCloseRequest(t, router, closeToken(t, issuer), body, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	if len(closes.calls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(closes.calls))
	}
	carried := closes.calls[0]
	if carried.tenantID != uuid.MustParse(tokenTenantID) {
		t.Errorf("tenantID = %s, want el del token; el tenant_id del cuerpo fue ignorado", carried.tenantID)
	}
	if carried.userID != uuid.MustParse(tokenUserID) {
		t.Errorf("userID = %s, want el del token", carried.userID)
	}
	if carried.close.ID != uuid.MustParse(closeID) {
		t.Errorf("close.ID = %s, want %s", carried.close.ID, closeID)
	}
	if carried.close.ClosedOn != time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) {
		t.Errorf("closed_on = %v, want la fecha de negocio del cuerpo", carried.close.ClosedOn)
	}

	wantOrder := []string{"cash", "yape", "plin", "card", "other", "fiado"}
	if len(carried.close.Counts) != len(wantOrder) {
		t.Fatalf("counts = %d fila(s), want %d", len(carried.close.Counts), len(wantOrder))
	}
	for index, method := range wantOrder {
		if carried.close.Counts[index].PaymentMethod != method {
			t.Errorf("counts[%d].payment_method = %q, want %q (orden fijo del acuse)", index, carried.close.Counts[index].PaymentMethod, method)
		}
	}
	if carried.close.Counts[0].CountedCents != 4500 || carried.close.Counts[3].CountedCents != 1200 {
		t.Errorf("conteos = %+v, want los del cuerpo verbatim", carried.close.Counts)
	}
}

// TestCashCloseRejectsAnUnusableBody: cada camino de 422 con el campo culpable en details
// —y el store NUNCA corrió—. El conjunto de métodos es cerrado de seis filas: omitir uno
// escondería dinero y duplicarlo contaría dos veces.
func TestCashCloseRejectsAnUnusableBody(t *testing.T) {
	base := validCloseBody()
	fiveRows := `{
		"id": "` + closeID + `",
		"closed_on": "2026-10-19",
		"counts": [
			{"payment_method": "cash", "counted_cents": 4500},
			{"payment_method": "yape", "counted_cents": 3000},
			{"payment_method": "plin", "counted_cents": 0},
			{"payment_method": "card", "counted_cents": 1200},
			{"payment_method": "other", "counted_cents": 0}
		]
	}`
	sevenRows := strings.Replace(base, `{"payment_method": "fiado", "counted_cents": 0}`,
		`{"payment_method": "fiado", "counted_cents": 0}, {"payment_method": "cash", "counted_cents": 0}`, 1)

	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{"the body is not a JSON object", `{`, ""},
		{"id missing", strings.Replace(base, `"id": "`+closeID+`",`, "", 1), "id"},
		{"id is not a uuid", strings.Replace(base, `"id": "`+closeID+`"`, `"id": "no-es-uuid"`, 1), "id"},
		{"closed_on is not a business date", strings.Replace(base, `"closed_on": "2026-10-19"`, `"closed_on": "19/10/2026"`, 1), "closed_on"},
		{"five count rows", fiveRows, "counts"},
		{"seven count rows", sevenRows, "counts"},
		{"no count rows", strings.Replace(base, `"counts": [`, `"counts": [] , "ignored": [`, 1), "counts"},
		{"a duplicated method", strings.Replace(base, `{"payment_method": "other", "counted_cents": 0}`, `{"payment_method": "cash", "counted_cents": 0}`, 1), "counts"},
		{"a method outside the six", strings.Replace(base, `"payment_method": "other"`, `"payment_method": "transfer"`, 1), "counts"},
		{"a negative counted amount", strings.Replace(base, `"payment_method": "cash", "counted_cents": 4500`, `"payment_method": "cash", "counted_cents": -1`, 1), "counted_cents"},
		{"a counted amount missing", strings.Replace(base, `"payment_method": "cash", "counted_cents": 4500`, `"payment_method": "cash"`, 1), "counted_cents"},
		{"a fiado counted amount", strings.Replace(base, `"payment_method": "fiado", "counted_cents": 0`, `"payment_method": "fiado", "counted_cents": 100`, 1), "payment_method"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closes := &stubCashCloseStore{}
			router, issuer := newCashCloseRouter(t, closes, &strings.Builder{})

			recorder := doCloseRequest(t, router, closeToken(t, issuer), tt.body, nil)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			envelope := decodeErrorEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			if tt.wantField != "" && envelope.Error.Details["field"] != tt.wantField {
				t.Errorf("details = %v, want el campo %s", envelope.Error.Details, tt.wantField)
			}
			if len(closes.calls) != 0 {
				t.Errorf("el store corrió %d vez/veces con el cuerpo inservible, want 0", len(closes.calls))
			}
		})
	}
}

// TestCashCloseNeedsTheIdempotencyKey: sin encabezado no hay claim en el que caer el cierre
// — la petición es inservible ANTES del store (docs/BACKEND.md §4.13).
func TestCashCloseNeedsTheIdempotencyKey(t *testing.T) {
	closes := &stubCashCloseStore{}
	router, issuer := newCashCloseRouter(t, closes, &strings.Builder{})

	recorder := doCloseRequest(t, router, closeToken(t, issuer), validCloseBody(),
		map[string]string{"Idempotency-Key": ""})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeErrorEnvelope(t, recorder)
	if envelope.Error.Details["field"] != "Idempotency-Key" {
		t.Errorf("details = %v, want el campo Idempotency-Key", envelope.Error.Details)
	}
	if len(closes.calls) != 0 {
		t.Errorf("el store corrió %d vez/veces sin clave, want 0", len(closes.calls))
	}
}

// TestCashCloseCarriesTheClaimToTheStore: la clave del encabezado y el hash del payload RAW
// (los mismos bytes que entraron, docs/BACKEND.md §6.2) viajan al store.
func TestCashCloseCarriesTheClaimToTheStore(t *testing.T) {
	key := "close-key-integracion"
	closes := &stubCashCloseStore{ack: []byte(`{}`)}
	router, issuer := newCashCloseRouter(t, closes, &strings.Builder{})

	body := validCloseBody()
	recorder := doCloseRequest(t, router, closeToken(t, issuer), body,
		map[string]string{"Idempotency-Key": key})
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	if len(closes.calls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(closes.calls))
	}
	claim := closes.calls[0].claim
	if claim.Key != key {
		t.Errorf("claim.Key = %q, want %q del encabezado", claim.Key, key)
	}
	if claim.PayloadHash != synckey.PayloadHash(key, []byte(body)) {
		t.Errorf("claim.PayloadHash = %q, want el hash del cuerpo RAW (§6.2)", claim.PayloadHash)
	}
}

// TestCashCloseMapsTheReplayBranch: una repetición verdadera (Branch A) es el snapshot
// ALMACENADO tal cual, HTTP 200 (docs/DATABASE.md §7.1).
func TestCashCloseMapsTheReplayBranch(t *testing.T) {
	stored := []byte(`{"cash_close":{"id":"019a"},"counts":[]}`)
	closes := &stubCashCloseStore{ack: stored}
	router, issuer := newCashCloseRouter(t, closes, &strings.Builder{})

	recorder := doCloseRequest(t, router, closeToken(t, issuer), validCloseBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	// El transporte añade el newline final a cualquier respuesta JSON: lo que se exige es
	// que el cuerpo SEA el snapshot almacenado hasta ese carácter.
	if strings.TrimSuffix(recorder.Body.String(), "\n") != string(stored) {
		t.Errorf("cuerpo = %q, want el snapshot ALMACENADO tal cual (raw, sin re-marshalling)", recorder.Body.String())
	}
}

// TestCashCloseMapsThePayloadMismatchBranch: la misma clave con otro cuerpo es el 422 del
// contrato con el código idempotency.payload_mismatch.
func TestCashCloseMapsThePayloadMismatchBranch(t *testing.T) {
	closes := &stubCashCloseStore{err: synckey.ErrPayloadMismatch}
	router, issuer := newCashCloseRouter(t, closes, &strings.Builder{})

	recorder := doCloseRequest(t, router, closeToken(t, issuer), validCloseBody(), nil)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeErrorEnvelope(t, recorder)
	if envelope.Error.Code != payloadMismatchCode {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, payloadMismatchCode)
	}
	if envelope.Error.Details["field"] != "Idempotency-Key" {
		t.Errorf("details = %v, want el campo Idempotency-Key", envelope.Error.Details)
	}
}

// TestCashCloseMapsTheInFlightBranch: un duplicado concurrente (Branch C) es el 409 del
// contrato con Retry-After para el backoff del cliente.
func TestCashCloseMapsTheInFlightBranch(t *testing.T) {
	closes := &stubCashCloseStore{err: synckey.ErrInFlight}
	router, issuer := newCashCloseRouter(t, closes, &strings.Builder{})

	recorder := doCloseRequest(t, router, closeToken(t, issuer), validCloseBody(), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("estado = %d, want 409 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeConflict {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeConflict)
	}
	if got := recorder.Header().Get("Retry-After"); got == "" {
		t.Errorf("sin Retry-After: el cliente no tiene backoff que respetar (Branch C)")
	}
}

// TestCashCloseFailurePaths: sin claims el handler falla CERRADO (500, nada corre) y un
// error del store es un 500 genérico, nunca el detalle interno (§4.1).
func TestCashCloseFailurePaths(t *testing.T) {
	t.Run("no claims in the request", func(t *testing.T) {
		closes := &stubCashCloseStore{}
		handler, err := cashclose.NewCashCloseHandler(closes, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatalf("NewCashCloseHandler: %v", err)
		}

		request := httptest.NewRequest(http.MethodPost, "/cash-closes", strings.NewReader(validCloseBody()))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", defaultIdempotencyKey)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
		}
		if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
			t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
		}
		if len(closes.calls) != 0 {
			t.Errorf("el store corrió %d vez/veces sin claims, want 0", len(closes.calls))
		}
	})

	t.Run("a store failure is the generic 500", func(t *testing.T) {
		var logs strings.Builder
		closes := &stubCashCloseStore{err: context.DeadlineExceeded}
		router, issuer := newCashCloseRouter(t, closes, &logs)

		recorder := doCloseRequest(t, router, closeToken(t, issuer), validCloseBody(), nil)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
		}
		if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
			t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
		}
	})
}
