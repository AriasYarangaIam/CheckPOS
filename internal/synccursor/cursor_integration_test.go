// Pruebas de integración del pull por cursor contra PostgreSQL real: las dos consultas de
// §7.3 (`updated_at > cursor` sobre `products` y sobre `fiado_accounts`) y la aritmética del
// cursor, medidas a través del endpoint público con el router real y el RLS de `pos_app`.
package synccursor_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/fiado"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// seedProductRow siembra una fila de `products` del tenant con la marca de mutación dada.
// Un `deletedAt` no vacío siembra el tombstone.
func seedProductRow(t *testing.T, db *testDatabase, id, name, updatedAt, deletedAt string) {
	t.Helper()

	var deleted any
	if deletedAt != "" {
		deleted = deletedAt
	}

	inTenant(t, db, testTenantID, func(tx *sql.Tx) {
		execOn(t, tx, `INSERT INTO products (id, tenant_id, name, price_cents, uom, created_at, updated_at, received_at, deleted_at)
			VALUES ($1, $2, $3, 1250, 'unit', $4, $4, $4, $5)`, id, testTenantID, name, updatedAt, deleted)
	})
}

// seedAccountRow siembra una fila de `fiado_accounts` del tenant con la marca de mutación
// dada. No hay saldo: el saldo se computa de los movimientos (§3.2).
func seedAccountRow(t *testing.T, db *testDatabase, id, name, updatedAt string) {
	t.Helper()

	inTenant(t, db, testTenantID, func(tx *sql.Tx) {
		execOn(t, tx, `INSERT INTO fiado_accounts (id, tenant_id, name, phone, created_at, updated_at, received_at)
			VALUES ($1, $2, $3, NULL, $4, $4, $4)`, id, testTenantID, name, updatedAt)
	})
}

// pullEndpoint arma el router real del proceso con las DOS fuentes reales sobre el pool de
// `pos_app` de la base desechable.
func pullEndpoint(t *testing.T, db *testDatabase) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	return newCursorRouter(t, catalog.NewPostgresProductStore(tenants), fiado.NewPostgresAccountStore(tenants))
}

// TestCursorEndpointPullsOnlyTheRowsNewerThanTheCursor: el pull con cursor entrega
// exactamente las filas con `updated_at` posterior —tombstones incluidos— de las dos tablas,
// y devuelve como cursor nuevo el máximo realmente entregado.
func TestCursorEndpointPullsOnlyTheRowsNewerThanTheCursor(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, testTenantID)

	seedProductRow(t, db, "0192f3a4-5b6c-7d8e-9f01-23456789ad01", "Arroz", "2026-10-21T10:00:00Z", "")
	seedProductRow(t, db, "0192f3a4-5b6c-7d8e-9f01-23456789ad02", "Fideo", "2026-10-21T11:00:00Z", "")
	seedProductRow(t, db, "0192f3a4-5b6c-7d8e-9f01-23456789ad03", "Azúcar", "2026-10-21T12:00:00Z", "2026-10-21T12:00:00Z")
	seedAccountRow(t, db, "0192f3a4-5b6c-7d8e-9f01-23456789ad04", "Rosa Quispe", "2026-10-21T13:00:00Z")

	router, issuer := pullEndpoint(t, db)

	// Pull inicial: el conjunto completo de las dos tablas.
	initial := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), ""))
	if len(initial.Products) != 3 {
		t.Fatalf("pull inicial productos = %d, quería 3 (los tres, tombstone incluido)", len(initial.Products))
	}
	if len(initial.FiadoAccounts) != 1 {
		t.Fatalf("pull inicial cuentas = %d, quería 1", len(initial.FiadoAccounts))
	}
	if initial.Cursor == nil || *initial.Cursor != "2026-10-21T13:00:00Z" {
		t.Fatalf("cursor del pull inicial = %v, quería el máximo real 2026-10-21T13:00:00Z", initial.Cursor)
	}

	// Pull incremental desde el primer producto: solo lo posterior a esa marca.
	incremental := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), "2026-10-21T10:00:00Z"))
	if len(incremental.Products) != 2 {
		t.Fatalf("incremental productos = %d, quería 2 (Fideo y el tombstone; Arroz NO)", len(incremental.Products))
	}
	var names []string
	for _, raw := range incremental.Products {
		var row struct {
			Name      string  `json:"name"`
			DeletedAt *string `json:"deleted_at"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("decodificar la fila: %v", err)
		}
		names = append(names, row.Name)
		if row.Name == "Azúcar" && row.DeletedAt == nil {
			t.Error("el tombstone viajó sin deleted_at: el pull del cursor tiene que traerlo (§7.3)")
		}
	}
	if len(names) != 2 || names[0] != "Fideo" || names[1] != "Azúcar" {
		t.Errorf("orden del pull = %v, quería [Fideo Azúcar] (updated_at, id)", names)
	}
	if len(incremental.FiadoAccounts) != 1 {
		t.Errorf("incremental cuentas = %d, quería 1 (la cuenta es posterior al cursor)", len(incremental.FiadoAccounts))
	}
	if incremental.Cursor == nil || *incremental.Cursor != "2026-10-21T13:00:00Z" {
		t.Errorf("cursor incremental = %v, quería el máximo realmente entregado", incremental.Cursor)
	}

	// Pull con el cursor al día: no hay nada nuevo y el cursor se repite.
	caughtUp := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), "2026-10-21T13:00:00Z"))
	if len(caughtUp.Products) != 0 || len(caughtUp.FiadoAccounts) != 0 {
		t.Errorf("con el cursor al día quería listas vacías, obtuve %d productos y %d cuentas",
			len(caughtUp.Products), len(caughtUp.FiadoAccounts))
	}
	if caughtUp.Cursor == nil || *caughtUp.Cursor != "2026-10-21T13:00:00Z" {
		t.Errorf("cursor al día = %v, quería el eco del recibido (nunca now())", caughtUp.Cursor)
	}
}
