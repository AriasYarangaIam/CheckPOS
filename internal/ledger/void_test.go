// Pruebas unitarias de POST /sales/{id}/void (W5-BE-2): el contrato de la superficie HTTP
// sobre un store stub, sin base de datos. El router es el real del proceso, en proceso
// (httptest), con el patrón de ruta `POST /sales/{id}/void` que monta cmd/api.
//
// Lo no negociable que se prueba acá: el tenant y el usuario son SOLO los del token; el
// `{id}` de la ruta es la venta ORIGINAL y el `id` del cuerpo es la fila compensatoria
// —dos uuids distintos que no se pueden confundir—; `sold_on` es obligatorio porque la PK
// de `sales` es `(sold_on, sale_id)` y la partición se elige por esa fecha; y el mapa de
// errores del §4.10 (404, 422, 409) sale del sobre único de §4.1.
package ledger_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/ledger"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

const (
	// voidSaleID es el id de la FILA COMPENSATORIA que el cuerpo envía. Es distinto del id
	// de la venta original a propósito: confundirlos escribiría la anulación encima de la
	// venta.
	voidSaleID = "019b0000-0000-7000-8000-0000000000c3"
)

// stubVoidStore es el store de las pruebas unitarias: registra lo que el handler le pidió y
// devuelve lo que la prueba fijó —incluido un fallo— sin base de datos.
type stubVoidStore struct {
	calls []voidCall

	ack []byte
	err error
}

type voidCall struct {
	tenantID   uuid.UUID
	userID     uuid.UUID
	void       ledger.VoidInput
	receivedAt time.Time
	claim      synckey.ClaimInput
}

func (s *stubVoidStore) VoidSale(_ context.Context, tenantID, userID uuid.UUID, void ledger.VoidInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	s.calls = append(s.calls, voidCall{tenantID: tenantID, userID: userID, void: void, receivedAt: receivedAt, claim: claim})
	if s.err != nil {
		return nil, s.err
	}
	return s.ack, nil
}

// validVoidBody es el cuerpo de referencia de §4.10: la fila compensatoria y la fecha de
// negocio del ORIGINAL.
func validVoidBody() string {
	return `{"id": "` + voidSaleID + `", "sold_on": "2026-10-19"}`
}

// newVoidRouter arma el router REAL del proceso con la ruta de anulación detrás del
// middleware de token, igual que cmd/api la monte.
func newVoidRouter(t *testing.T, voids ledger.SaleVoidStore, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := ledger.NewSaleVoidHandler(voids, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewSaleVoidHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /sales/{id}/void", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// doVoidRequest envía una anulación de la venta original indicada. Una entrada
// Idempotency-Key propia —aunque sea vacía— es la única forma de probar la ausencia del
// encabezado, porque el helper inyecta la clave de referencia cuando no se menciona.
func doVoidRequest(t *testing.T, router http.Handler, token, originalID, body string, extraHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(http.MethodPost, "/sales/"+originalID+"/void", reader)
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

// TestVoidHandsTheOriginalAndTheCompensatingIDToTheStore: el store recibe SOLO el tenant y
// el usuario del token, la venta original de la RUTA, la fila compensatoria del CUERPO y la
// fecha de negocio —los tres valores que la PK compuesta necesita para encontrar el
// original y elegir la partición (docs/DATABASE.md §4 `sales`, §6.1)—.
func TestVoidHandsTheOriginalAndTheCompensatingIDToTheStore(t *testing.T) {
	voids := &stubVoidStore{ack: []byte(`{"sale":{"kind":"void"}}`)}
	router, issuer := newVoidRouter(t, voids, &strings.Builder{})

	recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	if len(voids.calls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(voids.calls))
	}
	carried := voids.calls[0]
	if carried.tenantID != uuid.MustParse(tokenTenantID) {
		t.Errorf("tenantID = %s, want el del token", carried.tenantID)
	}
	if carried.userID != uuid.MustParse(tokenUserID) {
		t.Errorf("userID = %s, want el del token", carried.userID)
	}
	if carried.void.OriginalID != uuid.MustParse(saleAID) {
		t.Errorf("OriginalID = %s, want la venta de la RUTA %s", carried.void.OriginalID, saleAID)
	}
	if carried.void.ID != uuid.MustParse(voidSaleID) {
		t.Errorf("ID = %s, want la fila compensatoria del CUERPO %s", carried.void.ID, voidSaleID)
	}
	if carried.void.SoldOn != time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) {
		t.Errorf("SoldOn = %v, want la fecha de negocio del original", carried.void.SoldOn)
	}
}

// TestVoidRejectsAnUnusableBody: cada camino de 422 con el campo culpable en details, el
// 404 de un id de ruta que no puede nombrar una venta, y el store NUNCA corrió.
func TestVoidRejectsAnUnusableBody(t *testing.T) {
	tests := []struct {
		name       string
		originalID string
		body       string
		headers    map[string]string
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name:       "the body is not a JSON object",
			originalID: saleAID,
			body:       `{`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   httpx.CodeValidationError,
		},
		{
			name:       "id missing",
			originalID: saleAID,
			body:       `{"sold_on": "2026-10-19"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   httpx.CodeValidationError,
			wantField:  "id",
		},
		{
			name:       "id is not a uuid",
			originalID: saleAID,
			body:       `{"id": "no-es-uuid", "sold_on": "2026-10-19"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   httpx.CodeValidationError,
			wantField:  "id",
		},
		{
			name:       "sold_on missing",
			originalID: saleAID,
			body:       `{"id": "` + voidSaleID + `"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   httpx.CodeValidationError,
			wantField:  "sold_on",
		},
		{
			name:       "sold_on is not a business date",
			originalID: saleAID,
			body:       `{"id": "` + voidSaleID + `", "sold_on": "2026-10-19T10:00:00Z"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   httpx.CodeValidationError,
			wantField:  "sold_on",
		},
		{
			name:       "the path id cannot name a sale",
			originalID: "no-es-uuid",
			body:       validVoidBody(),
			wantStatus: http.StatusNotFound,
			wantCode:   httpx.CodeNotFound,
		},
		{
			name:       "the idempotency key is missing",
			originalID: saleAID,
			body:       validVoidBody(),
			headers:    map[string]string{"Idempotency-Key": ""},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   httpx.CodeValidationError,
			wantField:  "Idempotency-Key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			voids := &stubVoidStore{}
			router, issuer := newVoidRouter(t, voids, &strings.Builder{})

			recorder := doVoidRequest(t, router, saleToken(t, issuer), tt.originalID, tt.body, tt.headers)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("estado = %d, want %d (cuerpo %q)", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			envelope := decodeErrorEnvelope(t, recorder)
			if envelope.Error.Code != tt.wantCode {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, tt.wantCode)
			}
			if tt.wantField != "" && envelope.Error.Details["field"] != tt.wantField {
				t.Errorf("details = %v, want el campo %s", envelope.Error.Details, tt.wantField)
			}
			if len(voids.calls) != 0 {
				t.Errorf("el store corrió %d vez/veces con la petición inservible, want 0", len(voids.calls))
			}
		})
	}
}

// TestVoidMapsTheUnknownOriginalTo404: un original que el RLS no deja ver —no existe o es
// de otro tenant— es el 404 del §4.10, indistinguible en los dos casos (§4.1).
func TestVoidMapsTheUnknownOriginalTo404(t *testing.T) {
	voids := &stubVoidStore{err: ledger.ErrUnknownSale}
	router, issuer := newVoidRouter(t, voids, &strings.Builder{})

	recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("estado = %d, want 404 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeNotFound {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeNotFound)
	}
}

// TestVoidMapsAVoidOfAVoidTo422: la anulación de una anulación se rechaza al crearla (F39,
// docs/DATABASE.md §4 `sales`): aceptarla restaría dos veces.
func TestVoidMapsAVoidOfAVoidTo422(t *testing.T) {
	voids := &stubVoidStore{err: ledger.ErrVoidOfVoid}
	router, issuer := newVoidRouter(t, voids, &strings.Builder{})

	recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeErrorEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeValidationError {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
	}
	if envelope.Error.Details["field"] != "id" {
		t.Errorf("details = %v, want el campo id", envelope.Error.Details)
	}
}

// TestVoidMapsTheReplayBranch: una repetición verdadera es el snapshot ALMACENADO tal cual.
func TestVoidMapsTheReplayBranch(t *testing.T) {
	stored := []byte(`{"sale":{"kind":"void"},"products":[]}`)
	voids := &stubVoidStore{ack: stored}
	router, issuer := newVoidRouter(t, voids, &strings.Builder{})

	recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.TrimSuffix(recorder.Body.String(), "\n") != string(stored) {
		t.Errorf("cuerpo = %q, want el snapshot ALMACENADO tal cual", recorder.Body.String())
	}
}

// TestVoidMapsThePayloadMismatchBranch: la misma clave con otro cuerpo es el 422 con el
// código idempotency.payload_mismatch.
func TestVoidMapsThePayloadMismatchBranch(t *testing.T) {
	voids := &stubVoidStore{err: synckey.ErrPayloadMismatch}
	router, issuer := newVoidRouter(t, voids, &strings.Builder{})

	recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
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

// TestVoidMapsTheInFlightBranch: un duplicado concurrente es el 409 con Retry-After.
func TestVoidMapsTheInFlightBranch(t *testing.T) {
	voids := &stubVoidStore{err: synckey.ErrInFlight}
	router, issuer := newVoidRouter(t, voids, &strings.Builder{})

	recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
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

// TestVoidFailurePaths: sin claims el handler falla CERRADO (500, nada corre) y un error
// del store es un 500 genérico, nunca el detalle interno (§4.1).
func TestVoidFailurePaths(t *testing.T) {
	t.Run("no claims in the request", func(t *testing.T) {
		voids := &stubVoidStore{}
		handler, err := ledger.NewSaleVoidHandler(voids, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatalf("NewSaleVoidHandler: %v", err)
		}

		request := httptest.NewRequest(http.MethodPost, "/sales/"+saleAID+"/void", strings.NewReader(validVoidBody()))
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
		if len(voids.calls) != 0 {
			t.Errorf("el store corrió %d vez/veces sin claims, want 0", len(voids.calls))
		}
	})

	t.Run("a store failure is the generic 500", func(t *testing.T) {
		voids := &stubVoidStore{err: context.DeadlineExceeded}
		router, issuer := newVoidRouter(t, voids, &strings.Builder{})

		recorder := doVoidRequest(t, router, saleToken(t, issuer), saleAID, validVoidBody(), nil)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
		}
		if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
			t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
		}
	})
}
