// Test de contrato de append-only (docs/DATABASE.md §2.12): las cinco tablas del ledger
// —sales, sale_lines, fiado_movements, stock_adjustments y cash_close_counts— no tienen
// camino posible de UPDATE ni de DELETE.
//
// La mitad de concesiones de §5.5 está probada en grants_contract_test.go: pos_app no
// tiene ninguna concesión de escritura sobre el ledger. Esta prueba cubre la OTRA mitad
// del contrato (W4-DB-1): las guardas de trigger, que defienden el historial incluso contra
// el rol dueño y el superusuario que migra —la única forma de que «history is never edited
// in place» no dependa de la disciplina de concesiones de nadie.
//
// Reutiliza el arnés de schema_contract_test.go (base desechable, exec, queryStrings) y los
// ayudantes de grants_contract_test.go (seedTenantAndUser, expectSQLState). Es una prueba
// de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"database/sql"
	"testing"
)

// sqlStateAppendOnlyViolation es el SQLSTATE que lanza la guarda del ledger: 55006,
// «object_in_use», por la misma semántica —la historia confirmada ya no se toca—.
const sqlStateAppendOnlyViolation = "55006"

// appendOnlyTables son las cinco tablas append-only de §2.12, en orden de bytes de Go. El
// nombre de la guarda por tabla recorre esa misma lista.
var appendOnlyTables = []string{
	"cash_close_counts",
	"fiado_movements",
	"sale_lines",
	"sales",
	"stock_adjustments",
}

// appendOnlyGuardTrigger es el nombre de la guarda de una tabla: un trigger ÚNICO con los
// dos eventos (BEFORE UPDATE OR DELETE, §2.12 / MD-2).
func appendOnlyGuardTrigger(table string) string { return table + "_append_only_guard" }

// Los ids fijos que la siembra comparte entre sus pasos y las sondas.
const (
	appendOnlySaleID = "019a0000-0000-7000-8000-0000000000a7"
	// appendOnlyTenantID es el tenant de la siembra, y appendOnlyUserID su cajero.
	appendOnlyTenantID = "019a0000-0000-7000-8000-0000000000a1"
	// seedAppendOnlyUserID es el usuario de la venta sembrada.
	appendOnlyUserID = "019a0000-0000-7000-8000-0000000000a2"
	// appendOnlyProductID es el producto de la línea sembrada.
	appendOnlyProductID = "019a0000-0000-7000-8000-0000000000a3"
	// appendOnlyAdjustmentID es el delta de stock de la línea sembrada.
	appendOnlyAdjustmentID = "019a0000-0000-7000-8000-0000000000a4"
)

// TestAppendOnlyLedgerRejectsEditsAndDeletes aplica el contrato §2.12 contra la base real.
func TestAppendOnlyLedgerRejectsEditsAndDeletes(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	t.Run("each append-only table carries its BEFORE UPDATE OR DELETE guard trigger", func(t *testing.T) {
		got := queryStrings(t, db, `
			SELECT c.relname || '|' || trg.tgname
			FROM pg_trigger trg
			JOIN pg_class c ON c.oid = trg.tgrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public'
			  AND NOT trg.tgisinternal
			  AND trg.tgtype & 1 = 1                        -- BEFORE
			  AND trg.tgtype & 16 > 0 AND trg.tgtype & 8 > 0 -- UPDATE y DELETE en un solo trigger
			  AND c.relispartition = false
			ORDER BY 1`)

		want := make([]string, 0, len(appendOnlyTables))
		for _, table := range appendOnlyTables {
			want = append(want, table+"|"+appendOnlyGuardTrigger(table))
		}

		if len(got) == 0 {
			t.Fatalf("la consulta devolvió cero guardas: no hay nada que comparar, want %v", want)
		}
		if len(got) != len(want) {
			t.Errorf("guardas de append-only encontradas = %d, want %d: %v", len(got), len(want), got)
		}
		for _, found := range got {
			matched := false
			for _, w := range want {
				if found == w {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("guarda inesperada %q; want exactamente %v", found, want)
			}
		}
	})

	// La otra mitad de §2.12 ya está probada en grants_contract_test.go: pos_app no tiene
	// concesiones de UPDATE ni de DELETE. Acá la sonda corre como DUEÑO, que es el rol al
	// que solo las guardas pueden cerrar.
	t.Run("the ledger is untouchable even by the owner that migrated it", func(t *testing.T) {
		seedAppendOnlyLedgerRow(t, db)

		// Las tres tablas que Sprint 4 ya escribe en producción se sonda con filas REALES:
		// las guardas son de fila, así que un UPDATE con WHERE falso ni siquiera las
		// dispararía. Las otras dos tablas append-only quedan cubiertas por la aserción de
		// catálogo de arriba hasta que sus sprints (S5/S7) las toquen.
		probes := []struct {
			table string
			sql   string
		}{
			{"sales", `UPDATE sales SET total_cents = total_cents WHERE sale_id = $1`},
			{"sales", `DELETE FROM sales WHERE sale_id = $1`},
			{"sale_lines", `UPDATE sale_lines SET line_total_cents = line_total_cents WHERE sale_id = $1`},
			{"sale_lines", `DELETE FROM sale_lines WHERE sale_id = $1`},
			{"stock_adjustments", `UPDATE stock_adjustments SET qty_delta = qty_delta WHERE sale_id = $1`},
			{"stock_adjustments", `DELETE FROM stock_adjustments WHERE sale_id = $1`},
		}
		for _, probe := range probes {
			_, err := db.ExecContext(context.Background(), probe.sql, appendOnlySaleID)
			expectSQLState(t, err, sqlStateAppendOnlyViolation, "guarda de "+probe.table+" ("+probe.sql+")")
		}
	})
}

// seedAppendOnlyLedgerRow siembra UNA venta completa —encabezado, línea y su delta de
// stock— como dueño, superusuario fuera del RLS: siembra solamente. La venta viaja en la
// partición corriente (2026-10), que la migración de Sprint 1 ya crea.
func seedAppendOnlyLedgerRow(t *testing.T, db *sql.DB) {
	t.Helper()

	// El par tenant + usuario con el canal que ya existe en grants_contract_test.go.
	seedTenantAndUser(t, db, appendOnlyTenantID, appendOnlyUserID, "+51900000009")

	seedAppendOnlyExtra(t, db)
}

// seedAppendOnlyExtra termina la venta: el producto, el encabezado, la línea y su delta
// con source 'sale' —el par de columnas sold_on/sale_id obligadas por la FK compuesta.
func seedAppendOnlyExtra(t *testing.T, db *sql.DB) {
	t.Helper()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO products (id, tenant_id, name, price_cents, uom, stock, created_at, updated_at, received_at)
		VALUES ($1, $2, 'arroz de guarda', 100, 'kg', '3.000', now(), now(), now())`, appendOnlyProductID, appendOnlyTenantID); err != nil {
		t.Fatalf("sembrar el producto: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sales (sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method, total_cents, created_at, received_at)
		VALUES ('2026-10-19', $1, $2, $3, 'sale', 'cash', 100, now(), now())`,
		appendOnlySaleID, appendOnlyTenantID, appendOnlyUserID); err != nil {
		t.Fatalf("sembrar la venta: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sale_lines (sale_id, line_no, tenant_id, sold_on, product_id, name, price_cents, uom, qty, line_total_cents, created_at)
		VALUES ($1, 1, $2, '2026-10-19', $3, 'arroz de guarda', 100, 'kg', 1, 100, now())`,
		appendOnlySaleID, appendOnlyTenantID, appendOnlyProductID); err != nil {
		t.Fatalf("sembrar la línea: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO stock_adjustments (id, tenant_id, product_id, qty_delta, source, sold_on, sale_id, created_at, received_at)
		VALUES ($1, $2, $3, -1, 'sale', '2026-10-19', $4, now(), now())`,
		appendOnlyAdjustmentID, appendOnlyTenantID, appendOnlyProductID, appendOnlySaleID); err != nil {
		t.Fatalf("sembrar el delta: %v", err)
	}
}
