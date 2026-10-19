// Pruebas unitarias de POST /sales (W4-BE-1): el contrato de la superficie HTTP sobre un
// store stub, sin base de datos. El router es el real del proceso, en proceso (httptest,
// igual que internal/identity/devicebind_test.go), porque la respuesta y el sobre de error
// ÚNICO de §4.1 se deciden en el transporte.
//
// Lo no negociable que se prueba acá: el tenant es SOLO el del token
// (ARCHITECTURE.md:23), payment_method acepta exactamente los seis valores del CHECK
// (docs/DATABASE.md §4 `sales`), y el store recibe los valores congelados TAL COMO
// llegaron —el servidor no recomputa dinero (§2.3)—.
package ledger_test

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

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/ledger"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

const (
	// testSigningSecret es la clave de firma de las pruebas: 32 bytes, el piso del contrato.
	testSigningSecret = "test-only-signing-key-0123456789abcdef"
	// tokenTenantID es el tenant DENTRO del token: la única fuente admisible.
	tokenTenantID = "019a0000-0000-7000-8000-0000000000f1"
	// tokenUserID es el usuario del token.
	tokenUserID = "019a0000-0000-7000-8000-0000000000f2"
	// forgedTenantID es el tenant que el cuerpo falsifica. Ni el cuerpo ni un encabezado
	// son fuente de tenant (ARCHITECTURE.md:23).
	forgedTenantID = "019a0000-0000-7000-8000-0000000000f9"
	// productAID es el producto de las líneas de venta.
	productAID = "019b0000-0000-7000-8000-0000000000a1"
	// saleAID es el id de la venta que el cuerpo de referencia cierra.
	saleAID = "019b0000-0000-7000-8000-0000000000c1"
)

// alwaysReady es el sondeo de preparación del router de estas pruebas.
type alwaysReady struct{}

func (alwaysReady) Ping(context.Context) error { return nil }

// stubSaleStore es el store de las pruebas unitarias: registra lo que el handler le pidió y
// devuelve lo que la prueba fijó —incluido un fallo— sin base de datos.
type stubSaleStore struct {
	calls []saleCloseCall

	ack []byte
	err error
}

type saleCloseCall struct {
	tenantID   uuid.UUID
	userID     uuid.UUID
	sale       ledger.SaleInput
	receivedAt time.Time
}

func (s *stubSaleStore) CloseSale(_ context.Context, tenantID, userID uuid.UUID, sale ledger.SaleInput, receivedAt time.Time) ([]byte, error) {
	s.calls = append(s.calls, saleCloseCall{tenantID: tenantID, userID: userID, sale: sale, receivedAt: receivedAt})
	if s.err != nil {
		return nil, s.err
	}
	return s.ack, nil
}

// validSaleBody es el cuerpo de referencia: dos líneas del MISMO producto —el caso exacto
// que exige la proyección aditiva del stock— y un line_total que NO coincide con price×qty
// a propósito: el servidor lo guarda verbatim, sin recompute (§2.3).
func validSaleBody() string {
	return `{
		"sale_id": "` + saleAID + `",
		"sold_on": "2026-10-19",
		"payment_method": "cash",
		"total_cents": 6549,
		"lines": [
			{"line_no": 1, "product_id": "` + productAID + `", "name": "arroz especial 1kg", "price_cents": 1550, "uom": "kg", "qty": "2.750", "line_total_cents": 4999},
			{"line_no": 2, "product_id": "` + productAID + `", "name": "arroz especial 1kg", "price_cents": 1550, "uom": "kg", "qty": "1.000", "line_total_cents": 1550}
		]
	}`
}

// newSaleRouter arma el router REAL del proceso con la ruta /sales detrás del middleware de
// token, igual que cmd/api la monte.
func newSaleRouter(t *testing.T, sales ledger.SaleStore, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := ledger.NewSalesHandler(sales, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewSalesHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /sales", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// saleToken firma un access token de empleado para el tenant y el usuario de referencia.
func saleToken(t *testing.T, issuer *identity.TokenIssuer) string {
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

// doSaleRequest envía una petición al router con el método y el cuerpo pedidos. Un token
// vacío omite la cabecera de credencial.
func doSaleRequest(t *testing.T, router http.Handler, method, target, token, body string, extraHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range extraHeaders {
		request.Header.Set(name, value)
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

// TestSaleCloseHandsTheTokenTenantToTheStore: el store recibe SOLO el tenant y el usuario
// del token —ni el cuerpo ni un encabezado son fuente de tenant (ARCHITECTURE.md:23)— y la
// venta se guarda con sold_by_user_id trazable al token (docs/DATABASE.md §4 `sales`).
func TestSaleCloseHandsTheTokenTenantToTheStore(t *testing.T) {
	var logs strings.Builder
	sales := &stubSaleStore{ack: []byte(`{"saleId":"ok"}`)}
	router, issuer := newSaleRouter(t, sales, &logs)

	body := strings.Replace(validSaleBody(), `"payment_method": "cash"`,
		`"payment_method": "cash", "tenant_id": "`+forgedTenantID+`"`, 1)
	recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), body, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if len(sales.calls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(sales.calls))
	}
	carried := sales.calls[0]
	if carried.tenantID != uuid.MustParse(tokenTenantID) {
		t.Errorf("tenantID = %s, want el del token; el cuerpo con tenant_id falso fue ignorado", carried.tenantID)
	}
	if carried.userID != uuid.MustParse(tokenUserID) {
		t.Errorf("userID = %s, want el del token", carried.userID)
	}
}

// TestSaleCloseForwardsTheFrozenValues: los valores congelados llegan al store verbatim,
// aunque no cuadren con price×qty — recompute es un defecto del contrato (§2.3, MD-7).
func TestSaleCloseForwardsTheFrozenValues(t *testing.T) {
	sales := &stubSaleStore{ack: []byte(`{}`)}
	router, issuer := newSaleRouter(t, sales, &strings.Builder{})

	recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), validSaleBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	sale := sales.calls[0].sale
	if sale.ID != uuid.MustParse(saleAID) {
		t.Errorf("sale.ID = %s, want %s", sale.ID, saleAID)
	}
	if sale.PaymentMethod != "cash" {
		t.Errorf("payment_method = %q, want \"cash\"", sale.PaymentMethod)
	}
	if sale.TotalCents != 6549 {
		t.Errorf("total_cents = %d, want 6549: el total vive congelado", sale.TotalCents)
	}
	if sale.SoldOn != time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) {
		t.Errorf("sold_on = %v, want la fecha de negocio del cliente", sale.SoldOn)
	}
	if len(sale.Lines) != 2 {
		t.Fatalf("lines totales = %d, want 2", len(sale.Lines))
	}
	if sale.Lines[0].TotalCents != 4999 {
		t.Errorf("lines[0].line_total_cents = %d, want 4999: mismatch con price×qty, el servidor no recompute", sale.Lines[0].TotalCents)
	}
	if sale.Lines[0].Qty != "2.750" {
		t.Errorf("lines[0].qty = %q, want \"2.750\" exacto (NUMERIC(10,3) como texto)", sale.Lines[0].Qty)
	}
	if sale.Lines[1].No != 2 {
		t.Errorf("lines[1].line_no = %d, want 2", sale.Lines[1].No)
	}
}

// TestSaleCloseRejectsAnUnusableBody: cada camino de 422 del §4.9 con el campo culpable en
// details —y el store NUNCA corrió: un cuerpo inservible no llega a la base—.
func TestSaleCloseRejectsAnUnusableBody(t *testing.T) {
	base := validSaleBody()
	minimalLines := `{"sale_id":"` + saleAID + `","sold_on":"2026-10-19","payment_method":"cash","total_cents":0,"lines":[]}`

	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{"the body is not a JSON object", `{`, ""},
		{"sale_id missing", strings.Replace(base, `"sale_id": "`+saleAID+`",`, "", 1), "sale_id"},
		{"sold_on is not the business date", strings.Replace(base, `"sold_on": "2026-10-19"`, `"sold_on": "otro dia"`, 1), "sold_on"},
		{"payment_method outside the six", strings.Replace(base, `"payment_method": "cash"`, `"payment_method": "split"`, 1), "payment_method"},
		{"total does not match the frozen line totals", strings.Replace(base, `"total_cents": 6549`, `"total_cents": 1234`, 1), "total_cents"},
		{"lines empty", minimalLines, "lines"},
		{"duplicate line_no", strings.Replace(base, `"line_no": 2`, `"line_no": 1`, 1), "lines"},
		{"qty beyond the 3-decimal scale", strings.Replace(base, `"qty": "2.750"`, `"qty": "2.7500"`, 1), "lines"},
		{"qty not positive", strings.Replace(base, `"qty": "2.750"`, `"qty": "-1.000"`, 1), "lines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sales := &stubSaleStore{}
			router, issuer := newSaleRouter(t, sales, &strings.Builder{})

			recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), tt.body, nil)
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
			if len(sales.calls) != 0 {
				t.Errorf("el store corrió %d vez/veces con el cuerpo inservible, want 0", len(sales.calls))
			}
		})
	}
}

// TestSaleCloseFailurePaths: sin claims el handler falla CERRADO (500, nada corre) y un
// error del store es un 500 genérico, nunca el detalle interno (§4.1).
func TestSaleCloseFailurePaths(t *testing.T) {
	t.Run("no claims in the request", func(t *testing.T) {
		var logs strings.Builder
		sales := &stubSaleStore{}
		handler, err := ledger.NewSalesHandler(sales, slog.New(slog.NewTextHandler(&logs, nil)))
		if err != nil {
			t.Fatalf("NewSalesHandler: %v", err)
		}

		request := httptest.NewRequest(http.MethodPost, "/sales", strings.NewReader(validSaleBody()))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
		}
		if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
			t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
		}
		if len(sales.calls) != 0 {
			t.Errorf("el store corrió %d vez/veces sin claims, want 0", len(sales.calls))
		}
	})

	t.Run("an unknown product of the body is a 422", func(t *testing.T) {
		sales := &stubSaleStore{err: ledger.ErrUnknownProduct}
		router, issuer := newSaleRouter(t, sales, &strings.Builder{})

		recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), validSaleBody(), nil)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
		}
		envelope := decodeErrorEnvelope(t, recorder)
		if envelope.Error.Code != httpx.CodeValidationError {
			t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
		}
		if envelope.Error.Details["field"] != "product_id" {
			t.Errorf("details = %v, want el campo product_id", envelope.Error.Details)
		}
	})

	t.Run("a store failure is the generic 500", func(t *testing.T) {
		var logs strings.Builder
		sales := &stubSaleStore{err: context.DeadlineExceeded}
		router, issuer := newSaleRouter(t, sales, &logs)

		recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), validSaleBody(), nil)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
		}
		if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
			t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
		}
		if strings.Contains(logs.String(), validSaleBody()) {
			t.Errorf("la causa quedó registrada SIN el payload (§7.9): %q", logs.String())
		}
	})
}
