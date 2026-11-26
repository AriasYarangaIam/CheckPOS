// Pruebas de integración de POST /stock-adjustments (W8-BE-2) sobre una base desechable
// `pos_test_`, router REAL httpx, store de POSTGRES sobre el pool de `pos_app` con RLS, y toda
// aserción leída POR el camino de producción.
//
// Las invariantes que solo la base real puede demostrar:
//   - la merma anexa UNA fila al log con `source = 'manual'` y el motivo EXACTO, y el stock se
//     mueve de forma ADITIVA (docs/DATABASE.md §4 `stock_adjustments`);
//   - `products.updated_at` NO cambia: esa marca es del PATCH de catálogo y de nadie más (MD-11);
//   - el stock negativo se permite (OI-10): el log registra la realidad;
//   - la repetición de la misma clave deja UNA fila y el mismo acuse (§7.1 Branch A).
package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

const (
	// mermaProductID es el producto sobre el que se registra la merma.
	mermaProductID = "019d0000-0000-7000-8000-0000000000b1"
	// mermaAdjustmentID es el id del ajuste de referencia.
	mermaAdjustmentID = "019d0000-0000-7000-8000-0000000000b2"
	// mermaKey es la clave de idempotencia de la primera petición.
	mermaKey = "merma-key-0001"
)

// newAdjustmentIntegrationRouter monta el router REAL con el store de POSTGRES sobre el pool de
// `pos_app`, igual que cmd/api lo monta.
func newAdjustmentIntegrationRouter(t *testing.T, tenants *postgres.TenantPool) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	handler, err := catalog.NewStockAdjustmentHandler(
		catalog.NewPostgresStockAdjustmentStore(tenants),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
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

// adjustmentCount corre una consulta de conteo por pos_app con el contexto del tenant y
// argumentos —el `queryCount` del arnés del paquete no los acepta—.
func adjustmentCount(t *testing.T, tenants *postgres.TenantPool, tenantID, query string, args ...any) int {
	t.Helper()

	count := -1
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
			t.Fatalf("contar con %q: %v", query, err)
		}
	})
	return count
}

// stockOfProduct lee el stock proyectado por pos_app con el contexto del tenant.
func stockOfProduct(t *testing.T, tenants *postgres.TenantPool, tenantID, productID string) string {
	t.Helper()

	stock := ""
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), `SELECT stock::text FROM products WHERE id = $1`,
			mustParseUUID(t, productID)).Scan(&stock); err != nil {
			t.Fatalf("leer el stock del producto: %v", err)
		}
	})
	return stock
}

// updatedAtOfProduct lee la marca LWW del catálogo.
func updatedAtOfProduct(t *testing.T, tenants *postgres.TenantPool, tenantID, productID string) string {
	t.Helper()

	stamp := ""
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), `SELECT updated_at::text FROM products WHERE id = $1`,
			mustParseUUID(t, productID)).Scan(&stamp); err != nil {
			t.Fatalf("leer updated_at del producto: %v", err)
		}
	})
	return stamp
}

// TestStockAdjustmentAppendsTheDeltaAndMovesTheStockAdditively: el registro de merma del contrato
// —delta firmado negativo, `source` manual, motivo exacto— con el stock proyectado de forma
// aditiva y la marca LWW del catálogo intacta.
func TestStockAdjustmentAppendsTheDeltaAndMovesTheStockAdditively(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedProduct(t, db, productFixture{
		id: mermaProductID, tenantID: tokenTenantID, name: "Arroz 1kg", priceCents: 450, uom: "unit",
		stock: "10.000", updatedAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond),
	})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newAdjustmentIntegrationRouter(t, tenants)

	beforeUpdatedAt := updatedAtOfProduct(t, tenants, tokenTenantID, mermaProductID)

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(mermaAdjustmentID, mermaProductID, "-2.500", "manual", "Rotura en transporte"), nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	var ack struct {
		Adjustment struct {
			ID       string  `json:"id"`
			QtyDelta string  `json:"qty_delta"`
			Source   string  `json:"source"`
			Reason   string  `json:"reason"`
			SoldOn   *string `json:"sold_on"`
			SaleID   *string `json:"sale_id"`
		} `json:"adjustment"`
		Product struct {
			ProductID string `json:"product_id"`
			Stock     string `json:"stock"`
		} `json:"product"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &ack); err != nil {
		t.Fatalf("el acuse no es JSON: %v (%q)", err, recorder.Body.String())
	}

	if ack.Adjustment.QtyDelta != "-2.500" {
		t.Errorf("qty_delta del acuse = %q, want el texto verbatim del cuerpo", ack.Adjustment.QtyDelta)
	}
	if ack.Adjustment.Source != "manual" || ack.Adjustment.Reason != "Rotura en transporte" {
		t.Errorf("el acuse no conserva source/reason: %+v", ack.Adjustment)
	}
	if ack.Adjustment.SoldOn != nil || ack.Adjustment.SaleID != nil {
		t.Errorf("un ajuste manual viaja sin enlace a venta: %+v/%+v", ack.Adjustment.SoldOn, ack.Adjustment.SaleID)
	}
	if ack.Product.Stock != "7.500" {
		t.Errorf("product.stock del acuse = %q, want 7.500", ack.Product.Stock)
	}

	// Lo guardado: una fila del log y el stock proyectado.
	if got := adjustmentCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments WHERE id = $1`, mustParseUUID(t, mermaAdjustmentID)); got != 1 {
		t.Errorf("stock_adjustments con el ajuste = %d, want 1", got)
	}
	if got := adjustmentCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments
		WHERE id = $1 AND source = 'manual' AND reason = 'Rotura en transporte' AND qty_delta = '-2.500'`,
		mustParseUUID(t, mermaAdjustmentID)); got != 1 {
		t.Errorf("la fila del log no tiene source/reason/qty exactos: %d", got)
	}
	if got := stockOfProduct(t, tenants, tokenTenantID, mermaProductID); got != "7.500" {
		t.Errorf("stock proyectado = %q, want 7.500 (10.000 − 2.500)", got)
	}

	// Y la marca LWW del catálogo no se movió: la merma no es una edición de producto.
	if after := updatedAtOfProduct(t, tenants, tokenTenantID, mermaProductID); after != beforeUpdatedAt {
		t.Errorf("products.updated_at cambió con la merma:\n before: %s\n after:  %s", beforeUpdatedAt, after)
	}
}

// TestStockAdjustmentAllowsNegativeStock: el oversell offline se acepta —el log registra la
// realidad— y el stock proyectado puede quedar bajo cero (OI-10).
func TestStockAdjustmentAllowsNegativeStock(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedProduct(t, db, productFixture{
		id: mermaProductID, tenantID: tokenTenantID, name: "Leche", priceCents: 380, uom: "unit",
		stock: "1.000", updatedAt: time.Now().UTC(),
	})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newAdjustmentIntegrationRouter(t, tenants)

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(mermaAdjustmentID, mermaProductID, "-3.000", "manual", "Vencimiento"), nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}
	if got := stockOfProduct(t, tenants, tokenTenantID, mermaProductID); got != "-2.000" {
		t.Errorf("stock proyectado = %q, want -2.000: el stock negativo se permite", got)
	}
}

// TestStockAdjustmentRefusesAProductFromAnotherTenant: la FK de `stock_adjustments` corre fuera
// del RLS y aprobaría el producto ajeno, así que la barrera es la lectura por RLS. El 422 nombra
// el campo y no escribe nada.
func TestStockAdjustmentRefusesAProductFromAnotherTenant(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedTenant(t, db, anotherTenantID)
	seedProduct(t, db, productFixture{
		id: mermaProductID, tenantID: anotherTenantID, name: "Producto ajeno", priceCents: 100, uom: "unit",
		stock: "5.000", updatedAt: time.Now().UTC(),
	})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newAdjustmentIntegrationRouter(t, tenants)

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(mermaAdjustmentID, mermaProductID, "-1.000", "manual", "Vencimiento"), nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
	}
	if field := adjustmentErrorField(t, recorder); field != "product_id" {
		t.Errorf("details.field = %v, want product_id", field)
	}

	if got := adjustmentCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments`); got != 0 {
		t.Errorf("stock_adjustments del tenant = %d, want 0", got)
	}
	if got := stockOfProduct(t, tenants, anotherTenantID, mermaProductID); got != "5.000" {
		t.Errorf("el stock del otro tenant cambió a %q: la merma cruzó el aislamiento", got)
	}
}

// TestStockAdjustmentReplayKeepsOneRow: la misma clave con el mismo cuerpo devuelve el mismo
// acuse y no anexa un segundo delta —la merma no se cobra dos veces— (§7.1 Branch A).
func TestStockAdjustmentReplayKeepsOneRow(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedProduct(t, db, productFixture{
		id: mermaProductID, tenantID: tokenTenantID, name: "Aceite", priceCents: 900, uom: "unit",
		stock: "20.000", updatedAt: time.Now().UTC(),
	})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newAdjustmentIntegrationRouter(t, tenants)
	token := productToken(t, issuer)
	body := adjustmentBody(mermaAdjustmentID, mermaProductID, "-1.500", "manual", "Daño en vitrina")

	first := doAdjustmentRequest(t, router, token, body, map[string]string{"Idempotency-Key": mermaKey})
	if first.Code != http.StatusOK {
		t.Fatalf("primer envío: status = %d (%q)", first.Code, first.Body.String())
	}

	second := doAdjustmentRequest(t, router, token, body, map[string]string{"Idempotency-Key": mermaKey})
	if second.Code != http.StatusOK {
		t.Fatalf("repetición: status = %d (%q)", second.Code, second.Body.String())
	}
	if strings.TrimSpace(first.Body.String()) != strings.TrimSpace(second.Body.String()) {
		t.Errorf("la repetición no devolvió el snapshot almacenado:\n first: %q\n second: %q",
			first.Body.String(), second.Body.String())
	}

	if got := adjustmentCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments`); got != 1 {
		t.Errorf("stock_adjustments tras dos envíos = %d, want 1", got)
	}
	if got := stockOfProduct(t, tenants, tokenTenantID, mermaProductID); got != "18.500" {
		t.Errorf("stock tras dos envíos = %q, want 18.500 (un solo delta de -1.500)", got)
	}
}
