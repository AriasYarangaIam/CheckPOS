// Pruebas de integración de POST /sales (W4-BE-1): el ciclo completo sobre una base
// desechable `pos_test_`, router REAL httpx, store de POSTGRES sobre el pool de `pos_app`
// con RLS, y toda aserción leída POR el camino de producción (withTenant).
//
// Las invariantes que solo la base real puede demostrar:
//   - la fila de `sales` (encabezado congelado, partición por sold_on) y sus
//     `sale_lines` (snapshot congelado por línea, MD-6/MD-7);
//   - exactamente un delta por línea en `stock_adjustments`, negativo y con source
//     'sale', más la proyección aditiva sobre `products.stock` (docs/DATABASE.md §4).
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

// anotherTenantID es el SEGUNDO tenant de las pruebas de aislamiento por cuerpo.
const anotherTenantID = "019a0000-0000-7000-8000-0000000000f5"

// foreignProductID es un producto del OTRO tenant: la FK de `sale_lines` lo dejaría pasar
// —la comprobación de claves ajenas corre fuera del RLS— y esta prueba marca la barrera.
const foreignProductID = "019b0000-0000-7000-8000-0000000000e1"

// missingProductID es un producto que nadie tiene.
const missingProductID = "019b0000-0000-7000-8000-0000000000e9"

// TestSaleClosePersistsTheFrozenLedgerAndTheAdditiveStock: el cierre completo contra la
// base real — encabezado congelado, líneas congeladas, exactamente un delta por línea, la
// invariante aditiva del §4 y products.updated_at intacto.
func TestSaleClosePersistsTheFrozenLedgerAndTheAdditiveStock(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedTenant(t, db, anotherTenantID)
	seedSaleUser(t, db, tokenTenantID, tokenUserID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newLedgerIntegrationRouter(t, tenants, &strings.Builder{})

	// El producto arranca en 6.500: 2.750 + 1.000 tienen que caer a 2.750.
	seedProduct(t, db, saleProduct{id: productAID, tenantID: tokenTenantID, stock: "6.500"})
	beforeStock, beforeUpdatedAt := readProductStock(t, tenants, tokenTenantID, productAID)

	recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), validSaleBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	// El acuse refleja lo guardado: la venta congelada y el stock refrescado (§4.9,
	// ARCHITECTURE.md:100) — la misma forma que la repetición idempotente reenviará.
	var ack struct {
		Sale struct {
			SaleID        string `json:"sale_id"`
			SoldOn        string `json:"sold_on"`
			PaymentMethod string `json:"payment_method"`
			TotalCents    int64  `json:"total_cents"`
			Lines         []struct {
				LineNo         int    `json:"line_no"`
				ProductID      string `json:"product_id"`
				LineTotalCents int64  `json:"line_total_cents"`
			} `json:"lines"`
		} `json:"sale"`
		Products []struct {
			ProductID string `json:"product_id"`
			Stock     string `json:"stock"`
		} `json:"products"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &ack); err != nil {
		t.Fatalf("el acuse no es JSON: %v (%q)", err, recorder.Body.String())
	}
	if ack.Sale.SaleID != saleAID || ack.Sale.SoldOn != "2026-10-19" || ack.Sale.TotalCents != 6549 || ack.Sale.PaymentMethod != "cash" {
		t.Errorf("encabezado del acuse = %+v, want la venta tal como se cerró", ack.Sale)
	}
	if len(ack.Sale.Lines) != 2 || ack.Sale.Lines[0].LineTotalCents != 4999 {
		t.Errorf("líneas del acuse = %+v, want las líneas congeladas del cuerpo", ack.Sale.Lines)
	}
	if len(ack.Products) != 1 || ack.Products[0].ProductID != productAID || ack.Products[0].Stock != "2.750" {
		t.Errorf("products del acuse = %+v, want el stock proyectado refrescado (2.750)", ack.Products)
	}

	// El encabezado de `sales` por el camino de producción: kind, medio, total congelado,
	// la fecha de negocio del cliente y UNA sola marca de cierre negocio/auditoría.
	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		var (
			kind       string
			payment    string
			total      int64
			soldOn     string
			receivedAt time.Time
			createdAt  time.Time
		)
		err := tx.QueryRow(context.Background(), `SELECT kind, payment_method, total_cents, sold_on::text, received_at, created_at
			FROM sales WHERE sale_id = $1`, mustParseUUID(t, saleAID)).
			Scan(&kind, &payment, &total, &soldOn, &receivedAt, &createdAt)
		if err != nil {
			t.Fatalf("leer la venta cerrada: %v", err)
		}
		if kind != "sale" {
			t.Errorf("kind = %q, want \"sale\"", kind)
		}
		if payment != "cash" {
			t.Errorf("payment_method = %q, want \"cash\"", payment)
		}
		if total != 6549 {
			t.Errorf("total_cents = %d, want 6549 congelado del cliente", total)
		}
		if soldOn != "2026-10-19" {
			t.Errorf("sold_on = %q, want la fecha de negocio del cliente", soldOn)
		}
		if d := time.Since(receivedAt); d < -2*time.Minute || d > 2*time.Minute {
			t.Errorf("received_at = %v, want la marca de auditoría del momento de la petición", receivedAt)
		}
		if !receivedAt.Equal(createdAt) {
			t.Errorf("created_at = %v, want la MISMA marca del cierre que received_at (una sola lectura del reloj)", createdAt)
		}
	})

	// La línea congelada, incluido el line_total_cents que NO cuadra con price×qty: el
	// servidor NO recomputa (docs/DATABASE.md §2.3, MD-7).
	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		var (
			name      string
			price     int64
			uom       string
			lineTotal int64
		)
		err := tx.QueryRow(context.Background(), `SELECT name, price_cents, uom, line_total_cents
			FROM sale_lines WHERE sale_id = $1 AND line_no = 1`, mustParseUUID(t, saleAID)).
			Scan(&name, &price, &uom, &lineTotal)
		if err != nil {
			t.Fatalf("leer la línea 1: %v", err)
		}
		if name != "arroz especial 1kg" || price != 1550 || uom != "kg" || lineTotal != 4999 {
			t.Errorf("línea congelada = (%q, %d, %q, %d), want los congelados del cliente (4999 verbatim)", name, price, uom, lineTotal)
		}
	})

	// Exactamente UN delta por línea: dos líneas del mismo producto → dos filas.
	if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments WHERE sale_id = $1`,
		mustParseUUID(t, saleAID)); got != 2 {
		t.Errorf("stock_adjustments de la venta = %d fila(s), want exactamente una por línea (2)", got)
	}

	// Cada delta: source 'sale', la negación EXACTA de la qty vendida, y el vínculo
	// (sold_on, sale_id) completo que la FK compuesta obliga (MD-9).
	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		rows, err := tx.Query(context.Background(), `SELECT qty_delta::text FROM stock_adjustments
			WHERE sale_id = $1 ORDER BY qty_delta::text`, mustParseUUID(t, saleAID))
		if err != nil {
			t.Fatalf("leer los deltas: %v", err)
		}
		defer rows.Close()

		var deltas []string
		for rows.Next() {
			var delta string
			if err := rows.Scan(&delta); err != nil {
				t.Fatalf("escanear el delta: %v", err)
			}
			deltas = append(deltas, delta)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("recorrer los deltas: %v", err)
		}

		want := []string{"-1.000", "-2.750"}
		if len(deltas) != 2 || deltas[0] != want[0] || deltas[1] != want[1] {
			t.Errorf("qty_delta = %v, want %v: una fila por línea, negativa y en NUMERIC exacto", deltas, want)
		}
	})

	// La invariante aditiva del §4 y la marca de catálogo intacta.
	afterStock, afterUpdatedAt := readProductStock(t, tenants, tokenTenantID, productAID)
	if beforeStock != "6.500" {
		t.Errorf("stock antes del cierre = %q, want 6.500 sembrado", beforeStock)
	}
	if afterStock != "2.750" {
		t.Errorf("products.stock = %q, want 6.500 - (2.750 + 1.000) = 2.750", afterStock)
	}
	if !afterUpdatedAt.Equal(beforeUpdatedAt) {
		t.Errorf("products.updated_at = %v, want %v intacto: la proyección de stock no es mutación de catálogo (MD-11)", afterUpdatedAt, beforeUpdatedAt)
	}
}

// TestSaleCloseLandsTheRowOnTheClientPartition: una venta con fecha de negocio en una
// partición PRE-CREADA (2027-03, ventana estática de Sprint 1) insert por el padre — la
// clave compuesta (sold_on, sale_id) es la que decide la partición (docs/DATABASE.md §6).
func TestSaleCloseLandsTheRowOnTheClientPartition(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedSaleUser(t, db, tokenTenantID, tokenUserID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newLedgerIntegrationRouter(t, tenants, &strings.Builder{})
	seedProduct(t, db, saleProduct{id: productAID, tenantID: tokenTenantID, stock: "3.000"})

	body := strings.Replace(validSaleBody(), `"sold_on": "2026-10-19"`, `"sold_on": "2027-03-15"`, 1)
	recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), body, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	saleID := mustParseUUID(t, saleAID)
	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		var partition string
		err := tx.QueryRow(context.Background(), `SELECT tableoid::regclass::text FROM sales WHERE sale_id = $1`, saleID).
			Scan(&partition)
		if err != nil {
			t.Fatalf("leer la partición de la venta: %v", err)
		}
		if partition != "sales_2027_03" {
			t.Errorf("la venta de sold_on 2027-03-15 aterrizó en %s, want la partición del mes del cliente", partition)
		}
	})
}

// TestSaleCloseRejectsBodyReferencesOutsideTheTenant: la FK NO aísla — un producto ajeno o
// inexistente es un 422 del campo product_id y el cierre NO deja ninguna fila escrita.
func TestSaleCloseRejectsBodyReferencesOutsideTheTenant(t *testing.T) {
	tests := []struct {
		name       string
		productID  string
		wantTenant string
	}{
		{"a product of another tenant", foreignProductID, tokenTenantID},
		{"a product nobody has", missingProductID, tokenTenantID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newThrowawayDatabase(t)
			seedTenant(t, db, tokenTenantID)
			seedTenant(t, db, anotherTenantID)

			tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
			router, issuer := newLedgerIntegrationRouter(t, tenants, &strings.Builder{})

			// El producto ajeno del PRIMER subcaso existe, pero para el otro tenant.
			seedProduct(t, db, saleProduct{id: foreignProductID, tenantID: anotherTenantID, stock: "1.000"})

			body := `{"sale_id":"` + saleAID + `","sold_on":"2026-10-19","payment_method":"cash","total_cents":100,
				"lines":[{"line_no":1,"product_id":"` + tt.productID + `","name":"x","price_cents":100,"uom":"kg","qty":"1.000","line_total_cents":100}]}`
			recorder := doSaleRequest(t, router, http.MethodPost, "/sales", saleToken(t, issuer), body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			envelope := decodeErrorEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError || envelope.Error.Details["field"] != "product_id" {
				t.Errorf("sobre = %+v, want 422 VALIDATION_ERROR con el campo product_id", envelope.Error)
			}

			// El intento NO deja nada: ni la venta, ni una línea, ni un delta de stock.
			if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM sales`); got != 0 {
				t.Errorf("sales tiene %d fila(s), want 0: el cierre fallido no escribe nada", got)
			}
			if got := saleQueryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM stock_adjustments`); got != 0 {
				t.Errorf("stock_adjustments tiene %d fila(s), want 0", got)
			}
		})
	}
}

// newLedgerIntegrationRouter arma el router REAL con el store de POSTGRES sobre el pool de
// `pos_app` de la base desechable, igual que cmd/api lo monte.
func newLedgerIntegrationRouter(t *testing.T, tenants *postgres.TenantPool, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	store := ledger.NewPostgresSaleStore(tenants)
	logger := slog.New(slog.NewTextHandler(logBuf, nil))
	handler, err := ledger.NewSalesHandler(store, logger)
	if err != nil {
		t.Fatalf("NewSalesHandler: %v", err)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	return httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /sales", identity.RequireAccessToken(issuer, handler))
		}),
	), issuer
}
