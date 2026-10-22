// Pruebas de integración de POST /sales/{id}/void (W5-BE-2): el ciclo completo sobre una
// base desechable `pos_test_`, router REAL httpx, store de POSTGRES sobre el pool de
// `pos_app` con RLS, y toda aserción leída POR el camino de producción (withTenant).
//
// Las invariantes que solo la base real puede demostrar:
//   - la venta original queda INTACTA —append-only, ningún UPDATE ni DELETE— y la anulación
//     es una fila nueva `kind = 'void'` que espeja método y total POSITIVO (F8, F10);
//   - el stock se mueve en reversa con UN delta nuevo y positivo por línea, `source =
//     'void'`, y la proyección aditiva vuelve al nivel previo a la venta sin tocar
//     `products.updated_at` (MD-11);
//   - una anulación de anulación se rechaza (F39) y un original inexistente es un 404 que
//     no deja ninguna fila escrita.
package ledger_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/ledger"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// voidAckBody es el acuse de §4.9 en su forma de anulación: el mismo sobre de la venta, con
// `kind = "void"` y el vínculo al original (decisión 2 del documento del sprint).
type voidAckBody struct {
	Sale struct {
		SaleID         string `json:"sale_id"`
		SoldOn         string `json:"sold_on"`
		Kind           string `json:"kind"`
		PaymentMethod  string `json:"payment_method"`
		TotalCents     int64  `json:"total_cents"`
		OriginalSaleID string `json:"original_sale_id"`
		OriginalSoldOn string `json:"original_sold_on"`
		Lines          []struct {
			LineNo int `json:"line_no"`
		} `json:"lines"`
	} `json:"sale"`
	Products []struct {
		ProductID string `json:"product_id"`
		Stock     string `json:"stock"`
	} `json:"products"`
}

// TestVoidKeepsTheOriginalAndReversesTheStock: la anulación completa contra la base real —
// original intacto, fila compensatoria espejando el total POSITIVO, y el stock de vuelta en
// su nivel previo a la venta por deltas nuevos.
func TestVoidKeepsTheOriginalAndReversesTheStock(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedSaleUser(t, db, tokenTenantID, tokenUserID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newVoidIntegrationRouter(t, tenants, &strings.Builder{})

	seedProduct(t, db, saleProduct{id: productAID, tenantID: tokenTenantID, stock: "6.500"})
	_, updatedAtBeforeSale := readProductStock(t, tenants, tokenTenantID, productAID)

	closed := withSaleKey(t, router, issuer, "sale-para-anular-0001", validSaleBody())
	if closed.Code != http.StatusOK {
		t.Fatalf("estado del cierre = %d, want 200 (cuerpo %q)", closed.Code, closed.Body.String())
	}

	// La venta original, byte a byte, ANTES de anular: es el patrón contra el que se mide
	// que la anulación no la toca.
	originalHeader := saleRowSnapshot(t, tenants, saleAID)
	originalLines := saleLinesSnapshot(t, tenants, saleAID)
	stockAfterSale, _ := readProductStock(t, tenants, tokenTenantID, productAID)
	if stockAfterSale != "2.750" {
		t.Fatalf("stock tras la venta = %q, want 2.750 (6.500 − 3.750)", stockAfterSale)
	}

	voided := doVoidRequest(t, router, saleToken(t, issuer), saleAID,
		`{"id": "`+voidSaleID+`", "sold_on": "2026-10-19"}`,
		map[string]string{"Idempotency-Key": "void-de-la-venta-0001"})
	if voided.Code != http.StatusOK {
		t.Fatalf("estado de la anulación = %d, want 200 (cuerpo %q)", voided.Code, voided.Body.String())
	}

	var ack voidAckBody
	if err := json.Unmarshal(voided.Body.Bytes(), &ack); err != nil {
		t.Fatalf("el acuse no es JSON: %v (%q)", err, voided.Body.String())
	}
	if ack.Sale.Kind != "void" {
		t.Errorf("sale.kind = %q, want \"void\"", ack.Sale.Kind)
	}
	if ack.Sale.SaleID != voidSaleID {
		t.Errorf("sale.sale_id = %q, want la fila compensatoria %q", ack.Sale.SaleID, voidSaleID)
	}
	if ack.Sale.OriginalSaleID != saleAID || ack.Sale.OriginalSoldOn != "2026-10-19" {
		t.Errorf("original = (%q, %q), want (%q, %q)", ack.Sale.OriginalSaleID, ack.Sale.OriginalSoldOn, saleAID, "2026-10-19")
	}
	// El total de la anulación es el POSITIVO del original: la negación vive en F8.
	if ack.Sale.PaymentMethod != "cash" || ack.Sale.TotalCents != 6549 {
		t.Errorf("anulación = (%q, %d), want el método y el total positivo del original (cash, 6549)", ack.Sale.PaymentMethod, ack.Sale.TotalCents)
	}
	// El acuse refresca el stock como el cierre de venta: el cliente re-renderiza igual.
	if len(ack.Products) != 1 || ack.Products[0].ProductID != productAID || ack.Products[0].Stock != "6.500" {
		t.Errorf("products del acuse = %+v, want el stock de vuelta en 6.500", ack.Products)
	}

	// La venta original, byte a byte, DESPUÉS: idéntica. La anulación es una fila nueva.
	if got := saleRowSnapshot(t, tenants, saleAID); got != originalHeader {
		t.Errorf("la venta original cambió:\n got: %s\nwant: %s", got, originalHeader)
	}
	if got := saleLinesSnapshot(t, tenants, saleAID); got != originalLines {
		t.Errorf("las líneas de la venta original cambiaron:\n got: %s\nwant: %s", got, originalLines)
	}

	// La fila compensatoria en el ledger: mismo día de negocio que el original —la FK
	// compuesta y F8 lo exigen—, kind 'void', y el vínculo completo al original.
	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		var (
			kind             string
			payment          string
			total            int64
			soldOn           string
			originalSoldOn   string
			originalSaleID   string
			storedByUserID   string
			originalSaleUUID string
		)
		err := tx.QueryRow(context.Background(), `SELECT kind, payment_method, total_cents, sold_on::text,
				original_sold_on::text, original_sale_id::text, sold_by_user_id::text
			FROM sales WHERE sale_id = $1`, mustParseUUID(t, voidSaleID)).
			Scan(&kind, &payment, &total, &soldOn, &originalSoldOn, &originalSaleID, &storedByUserID)
		if err != nil {
			t.Fatalf("leer la fila compensatoria: %v", err)
		}
		originalSaleUUID = originalSaleID
		if kind != "void" || payment != "cash" || total != 6549 {
			t.Errorf("fila compensatoria = (%q, %q, %d), want (void, cash, 6549 positivo)", kind, payment, total)
		}
		if soldOn != "2026-10-19" {
			t.Errorf("sold_on de la anulación = %q, want la fecha de negocio del original", soldOn)
		}
		if originalSoldOn != "2026-10-19" || originalSaleUUID != saleAID {
			t.Errorf("vínculo al original = (%q, %q), want (%q, %q)", originalSoldOn, originalSaleUUID, "2026-10-19", saleAID)
		}
		if storedByUserID != tokenUserID {
			t.Errorf("sold_by_user_id = %q, want el usuario del token", storedByUserID)
		}
	})

	// Dos filas en el ledger y las mismas dos líneas: la anulación NO agrega líneas, así que
	// la agregación por producto del catálogo no cuenta dos veces.
	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM sales`); got != 2 {
		t.Errorf("sales = %d fila(s), want 2 (la venta y su compensatoria)", got)
	}
	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM sale_lines WHERE sale_id = $1`, mustParseUUID(t, voidSaleID)); got != 0 {
		t.Errorf("sale_lines de la anulación = %d fila(s), want 0", got)
	}

	// La reversa de stock: DOS deltas nuevos y POSITIVOS, source 'void', con el par
	// (sold_on del original, sale_id de la ANULACIÓN) que la FK compuesta exige.
	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments
		WHERE sale_id = $1 AND source = 'void' AND qty_delta > 0 AND sold_on = DATE '2026-10-19'`,
		mustParseUUID(t, voidSaleID)); got != 2 {
		t.Errorf("deltas de anulación = %d, want exactamente uno por línea de la venta (2)", got)
	}
	if got := saleQueryString(t, tenants, tokenTenantID, `SELECT SUM(qty_delta)::text FROM stock_adjustments WHERE sale_id = $1`,
		mustParseUUID(t, voidSaleID)); got != "3.750" {
		t.Errorf("suma de deltas de la anulación = %q, want 3.750 en NUMERIC exacto", got)
	}

	// El stock vuelve al nivel previo a la venta, y la marca de catálogo sigue intacta.
	stockAfterVoid, updatedAtAfterVoid := readProductStock(t, tenants, tokenTenantID, productAID)
	if stockAfterVoid != "6.500" {
		t.Errorf("products.stock = %q, want 6.500 (el nivel previo a la venta)", stockAfterVoid)
	}
	if !updatedAtAfterVoid.Equal(updatedAtBeforeSale) {
		t.Errorf("products.updated_at = %v, want %v intacto: la reversa de stock no es mutación de catálogo (MD-11)", updatedAtAfterVoid, updatedAtBeforeSale)
	}

	// Y el claim quedó completado con el acuse EXACTO que se respondió.
	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		var snapshot string
		if err := tx.QueryRow(context.Background(), `SELECT response_snapshot FROM idempotency_keys WHERE key = $1`,
			"void-de-la-venta-0001").Scan(&snapshot); err != nil {
			t.Fatalf("leer el snapshot del claim: %v", err)
		}
		if snapshot != strings.TrimSuffix(voided.Body.String(), "\n") {
			t.Errorf("snapshot almacenado = %q, want el acuse EXACTO de la anulación", snapshot)
		}
	})
}

// TestVoidRejectsAVoidOfAVoid: F39 contra la base real — la anulación de una anulación es
// un 422 y no deja una tercera fila. Aceptarla restaría dos veces.
func TestVoidRejectsAVoidOfAVoid(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedSaleUser(t, db, tokenTenantID, tokenUserID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newVoidIntegrationRouter(t, tenants, &strings.Builder{})
	seedProduct(t, db, saleProduct{id: productAID, tenantID: tokenTenantID, stock: "6.500"})

	if closed := withSaleKey(t, router, issuer, "sale-para-anular-0002", validSaleBody()); closed.Code != http.StatusOK {
		t.Fatalf("estado del cierre = %d, want 200 (cuerpo %q)", closed.Code, closed.Body.String())
	}
	first := doVoidRequest(t, router, saleToken(t, issuer), saleAID,
		`{"id": "`+voidSaleID+`", "sold_on": "2026-10-19"}`,
		map[string]string{"Idempotency-Key": "void-primera-0002"})
	if first.Code != http.StatusOK {
		t.Fatalf("estado de la primera anulación = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	// La segunda anulación apunta a la fila compensatoria que la primera escribió.
	second := doVoidRequest(t, router, saleToken(t, issuer), voidSaleID,
		`{"id": "019b0000-0000-7000-8000-0000000000c4", "sold_on": "2026-10-19"}`,
		map[string]string{"Idempotency-Key": "void-de-void-0002"})
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado de la anulación de anulación = %d, want 422 (cuerpo %q)", second.Code, second.Body.String())
	}
	envelope := decodeErrorEnvelope(t, second)
	if envelope.Error.Code != httpx.CodeValidationError || envelope.Error.Details["field"] != "id" {
		t.Errorf("sobre = %+v, want 422 VALIDATION_ERROR con el campo id", envelope.Error)
	}

	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM sales`); got != 2 {
		t.Errorf("sales = %d fila(s), want 2: el rechazo no escribe una tercera fila", got)
	}
	stock, _ := readProductStock(t, tenants, tokenTenantID, productAID)
	if stock != "6.500" {
		t.Errorf("stock = %q, want 6.500 intacto: la anulación rechazada no mueve stock", stock)
	}
}

// TestVoidReportsAnUnknownOriginalAsNotFoundWithoutWritingAnything: la comprobación de
// existencia corre primero, dentro de la transacción: el 404 no deja venta, ni delta, ni
// claim reclamado.
func TestVoidReportsAnUnknownOriginalAsNotFoundWithoutWritingAnything(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedSaleUser(t, db, tokenTenantID, tokenUserID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newVoidIntegrationRouter(t, tenants, &strings.Builder{})

	missing := "019b0000-0000-7000-8000-0000000000ff"
	recorder := doVoidRequest(t, router, saleToken(t, issuer), missing,
		`{"id": "`+voidSaleID+`", "sold_on": "2026-10-19"}`,
		map[string]string{"Idempotency-Key": "void-original-inexistente"})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("estado = %d, want 404 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeErrorEnvelope(t, recorder).Error.Code; got != httpx.CodeNotFound {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeNotFound)
	}

	for _, query := range []string{`SELECT count(*) FROM sales`, `SELECT count(*) FROM stock_adjustments`, `SELECT count(*) FROM idempotency_keys`} {
		if got := saleQueryCount(t, tenants, tokenTenantID, query); got != 0 {
			t.Errorf("%s = %d, want 0: el fallido no escribe nada (claim incluido)", query, got)
		}
	}
}

// TestVoidReplaysTheStoredSnapshotWithoutASecondRow: la misma clave con el mismo cuerpo
// devuelve el snapshot ALMACENADO byte a byte y no deja una segunda fila compensatoria.
func TestVoidReplaysTheStoredSnapshotWithoutASecondRow(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedSaleUser(t, db, tokenTenantID, tokenUserID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newVoidIntegrationRouter(t, tenants, &strings.Builder{})
	seedProduct(t, db, saleProduct{id: productAID, tenantID: tokenTenantID, stock: "6.500"})

	if closed := withSaleKey(t, router, issuer, "sale-para-anular-0003", validSaleBody()); closed.Code != http.StatusOK {
		t.Fatalf("estado del cierre = %d, want 200 (cuerpo %q)", closed.Code, closed.Body.String())
	}

	body := `{"id": "` + voidSaleID + `", "sold_on": "2026-10-19"}`
	headers := map[string]string{"Idempotency-Key": "void-replay-0003"}
	first := doVoidRequest(t, router, saleToken(t, issuer), saleAID, body, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("estado de la anulación = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	replay := doVoidRequest(t, router, saleToken(t, issuer), saleAID, body, headers)
	if replay.Code != http.StatusOK {
		t.Fatalf("estado de la repetición = %d, want 200 (cuerpo %q)", replay.Code, replay.Body.String())
	}
	if replay.Body.String() != first.Body.String() {
		t.Errorf("cuerpo repetido = %q, want el snapshot ALMACENADO byte a byte %q", replay.Body.String(), first.Body.String())
	}

	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM sales`); got != 2 {
		t.Errorf("sales = %d fila(s), want 2: el replay no anula dos veces", got)
	}
	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments`); got != 4 {
		t.Errorf("stock_adjustments = %d fila(s), want 4: el replay no mueve stock otra vez", got)
	}
	stock, _ := readProductStock(t, tenants, tokenTenantID, productAID)
	if stock != "6.500" {
		t.Errorf("stock tras el replay = %q, want 6.500", stock)
	}
}

// newVoidIntegrationRouter arma el router REAL con el store de POSTGRES sobre el pool de
// `pos_app` de la base desechable, con las dos rutas del ledger montadas como cmd/api.
func newVoidIntegrationRouter(t *testing.T, tenants *postgres.TenantPool, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	store := ledger.NewPostgresSaleStore(tenants)
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	sales, err := ledger.NewSalesHandler(store, logger)
	if err != nil {
		t.Fatalf("NewSalesHandler: %v", err)
	}
	voids, err := ledger.NewSaleVoidHandler(store, logger)
	if err != nil {
		t.Fatalf("NewSaleVoidHandler: %v", err)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	return httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /sales", identity.RequireAccessToken(issuer, sales))
			mux.Handle("POST /sales/{id}/void", identity.RequireAccessToken(issuer, voids))
		}),
	), issuer
}

// saleRowSnapshot rinde la superficie mutable de una fila de `sales` como una sola cadena:
// comparar la cadena antes y después de la anulación es lo que prueba que la fila original
// no se editó.
func saleRowSnapshot(t *testing.T, tenants *postgres.TenantPool, saleID string) string {
	t.Helper()

	return saleQueryString(t, tenants, tokenTenantID, `SELECT kind || '|' || payment_method || '|' || total_cents || '|' ||
			sold_on::text || '|' || sold_by_user_id::text || '|' || created_at::text || '|' || received_at::text
		FROM sales WHERE sale_id = $1`, mustParseUUID(t, saleID))
}

// saleLinesSnapshot rinde las líneas congeladas de una venta como una sola cadena.
func saleLinesSnapshot(t *testing.T, tenants *postgres.TenantPool, saleID string) string {
	t.Helper()

	return saleQueryString(t, tenants, tokenTenantID, `SELECT COALESCE(string_agg(
			line_no || ':' || product_id::text || ':' || name || ':' || price_cents || ':' || uom || ':' || qty::text || ':' || line_total_cents,
			',' ORDER BY line_no), '')
		FROM sale_lines WHERE sale_id = $1`, mustParseUUID(t, saleID))
}
