// Pruebas unitarias de POST /stock-adjustments (W8-BE-2; docs/BACKEND.md §4.14; S8 y S13 de
// odd/tasks/sprint-8-admin-hardening.md): el handler contra un store de mentira, sin base.
//
// Lo que se fija acá es la superficie: los TRES motivos exactos de la merma, el delta negativo
// obligatorio, la prohibición de forjar un `source` de venta o anulación por HTTP, el par de
// nulidad del enlace a la venta —nulo en un ajuste manual— y el mapeo de las ramas del claim.
package catalog_test

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

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

const (
	adjustmentID  = "019d0000-0000-7000-8000-0000000000a1"
	adjustmentQty = "-2.500"
)

// stubAdjustmentStore registra la llamada del handler y devuelve el desenlace fijado.
type stubAdjustmentStore struct {
	calls []adjustmentCall
	ack   []byte
	err   error
}

type adjustmentCall struct {
	tenantID   uuid.UUID
	adjustment catalog.StockAdjustmentInput
	receivedAt time.Time
	claim      synckey.ClaimInput
}

func (s *stubAdjustmentStore) Append(_ context.Context, tenantID uuid.UUID, adjustment catalog.StockAdjustmentInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	s.calls = append(s.calls, adjustmentCall{tenantID: tenantID, adjustment: adjustment, receivedAt: receivedAt, claim: claim})
	return s.ack, s.err
}

// newAdjustmentRouter monta el router REAL con el handler sobre el store de mentira.
func newAdjustmentRouter(t *testing.T, store catalog.StockAdjustmentStore) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	handler, err := catalog.NewStockAdjustmentHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStockAdjustmentHandler: %v", err)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	return httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /stock-adjustments", identity.RequireAccessToken(issuer, handler))
		}),
	), issuer
}

// doAdjustmentRequest envía un POST /stock-adjustments. Una entrada Idempotency-Key propia
// —aunque sea vacía— es la única forma de probar la ausencia del encabezado.
func doAdjustmentRequest(t *testing.T, router http.Handler, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/stock-adjustments", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	key, provided := headers["Idempotency-Key"]
	if !provided {
		key = "adjustment-key-0001"
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	for name, value := range headers {
		if name == "Idempotency-Key" {
			continue
		}
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// decodeEnvelopeCode lee `code` del sobre de error único.
func decodeEnvelopeCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("el error no usa el sobre único: %v (%q)", err, recorder.Body.String())
	}
	return envelope.Error.Code
}

// adjustmentErrorField lee `details.field` del sobre de error único (§4.1).
func adjustmentErrorField(t *testing.T, recorder *httptest.ResponseRecorder) any {
	t.Helper()

	var envelope struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("el error no usa el sobre único: %v (%q)", err, recorder.Body.String())
	}
	return envelope.Error.Details["field"]
}

// adjustmentBody arma el cuerpo del contrato de §4.14.
func adjustmentBody(id, productID, qty, source, reason string) string {
	return `{"id":"` + id + `","product_id":"` + productID + `","qty_delta":"` + qty +
		`","source":"` + source + `","reason":"` + reason + `"}`
}

// productIDForTest es el producto de referencia de estas pruebas.
const productIDForTest = "019d0000-0000-7000-8000-0000000000a2"

// TestStockAdjustmentAcceptsExactlyTheThreeReasons: los tres motivos del contrato entran, y el
// `source` que llega al store es SIEMPRE `manual`: el que escribe deltas de venta o anulación es
// el ledger, nunca la superficie (S13).
func TestStockAdjustmentAcceptsExactlyTheThreeReasons(t *testing.T) {
	for _, reason := range []string{"Vencimiento", "Rotura en transporte", "Daño en vitrina"} {
		t.Run(reason, func(t *testing.T) {
			store := &stubAdjustmentStore{ack: []byte(`{"adjustment":{},"product":{}}`)}
			router, issuer := newAdjustmentRouter(t, store)

			recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
				adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", reason), nil)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
			}
			if len(store.calls) != 1 {
				t.Fatalf("llamadas al store = %d, want 1", len(store.calls))
			}

			call := store.calls[0]
			if call.tenantID.String() != tokenTenantID {
				t.Errorf("tenant = %s, want el del token %s", call.tenantID, tokenTenantID)
			}
			if call.adjustment.Source != "manual" {
				t.Errorf("source = %q, want manual", call.adjustment.Source)
			}
			if call.adjustment.Reason != reason {
				t.Errorf("reason = %q, want %q exacto", call.adjustment.Reason, reason)
			}
			if call.adjustment.QtyDelta != adjustmentQty {
				t.Errorf("qty_delta = %q, want el texto verbatim %q", call.adjustment.QtyDelta, adjustmentQty)
			}
			if call.adjustment.SoldOn != nil || call.adjustment.SaleID != nil {
				t.Errorf("un ajuste manual no lleva enlace a venta: %+v/%+v", call.adjustment.SoldOn, call.adjustment.SaleID)
			}
		})
	}
}

// TestStockAdjustmentRejectsAnInvalidBody: cada cuerpo inservible devuelve 422 y NOMBRA el campo,
// sin correr el store.
func TestStockAdjustmentRejectsAnInvalidBody(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{name: "id que no es uuid", body: adjustmentBody("nope", productIDForTest, adjustmentQty, "manual", "Vencimiento"), field: "id"},
		{name: "producto que no es uuid", body: adjustmentBody(adjustmentID, "nope", adjustmentQty, "manual", "Vencimiento"), field: "product_id"},
		{name: "sin cantidad", body: `{"id":"` + adjustmentID + `","product_id":"` + productIDForTest + `","source":"manual","reason":"Vencimiento"}`, field: "qty_delta"},
		{name: "cantidad cero", body: adjustmentBody(adjustmentID, productIDForTest, "0", "manual", "Vencimiento"), field: "qty_delta"},
		{name: "cantidad cero con decimales", body: adjustmentBody(adjustmentID, productIDForTest, "0.000", "manual", "Vencimiento"), field: "qty_delta"},
		{name: "cantidad positiva", body: adjustmentBody(adjustmentID, productIDForTest, "2.500", "manual", "Vencimiento"), field: "qty_delta"},
		{name: "cantidad no numérica", body: adjustmentBody(adjustmentID, productIDForTest, "dos", "manual", "Vencimiento"), field: "qty_delta"},
		{name: "cantidad con cuatro decimales", body: adjustmentBody(adjustmentID, productIDForTest, "-2.5000", "manual", "Vencimiento"), field: "qty_delta"},
		{name: "source de venta", body: adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "sale", "Vencimiento"), field: "source"},
		{name: "source de anulación", body: adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "void", "Vencimiento"), field: "source"},
		{name: "sin source", body: `{"id":"` + adjustmentID + `","product_id":"` + productIDForTest + `","qty_delta":"` + adjustmentQty + `","reason":"Vencimiento"}`, field: "source"},
		{name: "motivo fuera de la lista", body: adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Robo"), field: "reason"},
		{name: "motivo con minúsculas", body: adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "vencimiento"), field: "reason"},
		{name: "motivo sin tilde", body: adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Dano en vitrina"), field: "reason"},
		{name: "motivo con otro texto", body: adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Vencido"), field: "reason"},
		{name: "sin motivo", body: `{"id":"` + adjustmentID + `","product_id":"` + productIDForTest + `","qty_delta":"` + adjustmentQty + `","source":"manual"}`, field: "reason"},
		{name: "con enlace a venta", body: `{"id":"` + adjustmentID + `","product_id":"` + productIDForTest + `","qty_delta":"` + adjustmentQty + `","source":"manual","reason":"Vencimiento","sold_on":"2026-10-26","sale_id":"019d0000-0000-7000-8000-0000000000a9"}`, field: "sold_on"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &stubAdjustmentStore{}
			router, issuer := newAdjustmentRouter(t, store)

			recorder := doAdjustmentRequest(t, router, productToken(t, issuer), testCase.body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
			}
			if field := adjustmentErrorField(t, recorder); field != testCase.field {
				t.Errorf("details.field = %v, want %q", field, testCase.field)
			}
			if len(store.calls) != 0 {
				t.Errorf("el store corrió %d vez(es) con un cuerpo inservible, want 0", len(store.calls))
			}
		})
	}
}

// TestStockAdjustmentRequiresTheIdempotencyKey: es uno de los seis POST encolables (§7.6) y sin
// claim la petición es inservible.
func TestStockAdjustmentRequiresTheIdempotencyKey(t *testing.T) {
	store := &stubAdjustmentStore{}
	router, issuer := newAdjustmentRouter(t, store)

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Vencimiento"),
		map[string]string{"Idempotency-Key": ""})

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 sin Idempotency-Key", recorder.Code)
	}
	if field := adjustmentErrorField(t, recorder); field != "Idempotency-Key" {
		t.Errorf("details.field = %v, want Idempotency-Key", field)
	}
	if len(store.calls) != 0 {
		t.Errorf("el store corrió sin claim, want 0")
	}
}

// TestStockAdjustmentMapsTheClaimAndProductFailures: las ramas del claim de §7.1 y la referencia
// por cuerpo que el RLS no deja ver.
func TestStockAdjustmentMapsTheClaimAndProductFailures(t *testing.T) {
	cases := []struct {
		name       string
		storeErr   error
		wantStatus int
		wantCode   string
		wantField  string
		wantRetry  string
	}{
		{name: "misma clave con otro payload", storeErr: synckey.ErrPayloadMismatch, wantStatus: http.StatusUnprocessableEntity, wantCode: "idempotency.payload_mismatch", wantField: "Idempotency-Key"},
		{name: "duplicado en vuelo", storeErr: synckey.ErrInFlight, wantStatus: http.StatusConflict, wantRetry: "2"},
		{name: "producto fuera del tenant", storeErr: catalog.ErrUnknownProduct, wantStatus: http.StatusUnprocessableEntity, wantField: "product_id"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router, issuer := newAdjustmentRouter(t, &stubAdjustmentStore{err: testCase.storeErr})

			recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
				adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Vencimiento"), nil)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (%q)", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if testCase.wantCode != "" {
				if code := decodeEnvelopeCode(t, recorder); code != testCase.wantCode {
					t.Errorf("error.code = %q, want %q", code, testCase.wantCode)
				}
			}
			if testCase.wantField != "" {
				if field := adjustmentErrorField(t, recorder); field != testCase.wantField {
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

// TestStockAdjustmentAnswersTheStoreBodyVerbatim: la respuesta es el acuse que el store armó.
func TestStockAdjustmentAnswersTheStoreBodyVerbatim(t *testing.T) {
	ack := []byte(`{"adjustment":{"id":"` + adjustmentID + `","qty_delta":"-2.500"},"product":{"product_id":"` + productIDForTest + `","stock":"7.500"}}`)
	router, issuer := newAdjustmentRouter(t, &stubAdjustmentStore{ack: ack})

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Vencimiento"), nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if strings.TrimSpace(recorder.Body.String()) != string(ack) {
		t.Errorf("body = %q, want el acuse del store %q", recorder.Body.String(), ack)
	}
}

// TestStockAdjustmentFailsClosedWithoutTheMiddleware: sin claims no hay tenant del que escribir.
func TestStockAdjustmentFailsClosedWithoutTheMiddleware(t *testing.T) {
	store := &stubAdjustmentStore{}
	handler, err := catalog.NewStockAdjustmentHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStockAdjustmentHandler: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/stock-adjustments",
		strings.NewReader(adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Vencimiento"))))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 sin claims", recorder.Code)
	}
	if len(store.calls) != 0 {
		t.Error("el store corrió sin claims")
	}
}

// TestStockAdjustmentAnswersOnlyPost: la ruta es de escritura y nada más.
func TestStockAdjustmentAnswersOnlyPost(t *testing.T) {
	router, issuer := newAdjustmentRouter(t, &stubAdjustmentStore{})

	// La ruta POST montada no acepta el método GET: el router responde con el sobre del 404.
	get := httptest.NewRequest(http.MethodGet, "/stock-adjustments", nil)
	get.Header.Set("Authorization", "Bearer "+productToken(t, issuer))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, get)

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 para GET", recorder.Code)
	}
}

// TestStockAdjustmentStoreRejectsAnEmptyAck: un store que no devuelve acuse no puede responder
// 200 con un cuerpo vacío; el contrato de tipos lo deja explícito.
func TestStockAdjustmentBuilderNeedsItsStore(t *testing.T) {
	if _, err := catalog.NewStockAdjustmentHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Error("NewStockAdjustmentHandler(nil) no falló")
	}
	if _, err := catalog.NewStockAdjustmentHandler(&stubAdjustmentStore{}, nil); err == nil {
		t.Error("NewStockAdjustmentHandler con logger nil no falló")
	}
}

// TestStockAdjustmentEnvelopeIsTheSharedOne: toda respuesta de error del endpoint usa el sobre
// único de §4.1, sin un segundo formato.
func TestStockAdjustmentEnvelopeIsTheSharedOne(t *testing.T) {
	router, issuer := newAdjustmentRouter(t, &stubAdjustmentStore{err: errors.New("boom")})

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(adjustmentID, productIDForTest, adjustmentQty, "manual", "Vencimiento"), nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}

	var envelope struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("el error no usa el sobre único: %v (%q)", err, recorder.Body.String())
	}
	if envelope.Error.Code == "" || envelope.Error.Message == "" {
		t.Errorf("el sobre no tiene code y message: %q", recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("boom")) {
		t.Errorf("el 500 filtra la causa: %q", recorder.Body.String())
	}
}
