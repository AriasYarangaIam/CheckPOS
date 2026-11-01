// Pruebas del CONTRATO DE ESQUEMA del módulo Fiado (W7-DB-1, S2 y S11): no agregan DDL,
// fijan por prueba lo que el esquema ya sostiene y lo que el sprint prohíbe.
//
// Las cinco invariantes que solo la base real puede demostrar:
//   - `fiado_accounts` NO tiene columna de saldo escribible; el saldo es
//     `SUM(fiado_movements.amount_cents)` (docs/DATABASE.md §4 `fiado_accounts`);
//   - `fiado_movements` NO tiene columna `kind`: el signo de `amount_cents` ES el tipo
//     (docs/DATABASE.md §4 `fiado_movements`);
//   - el ledger es append-only por guarda de disparo, también para el rol dueño
//     (docs/DATABASE.md §2.12; migrations/20261019000001_append_only_guards.sql);
//   - el enlace opcional a la venta es un par de nulidad
//     (`fiado_movements_sold_on_sale_id_pair_nullity_chk`, MD-9/F19);
//   - el saldo computado coincide con la suma de los movimientos firmados.
package fiado_test

import (
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// TestFiadoAccountsHaveNoWritableBalance: el saldo no existe como columna —ni con otro
// nombre de la misma familia— y cualquier intento de escribirlo es un error de columna
// inexistente (42703), no una escritura silenciosa (S11, docs/DATABASE.md §4).
func TestFiadoAccountsHaveNoWritableBalance(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "+51987111222")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))

	// La forma del esquema: ninguna columna de saldo en la tabla de cuentas.
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'fiado_accounts' AND column_name IN ('balance_cents', 'balance', 'saldo_cents')`); got != 0 {
		t.Fatalf("fiado_accounts tiene %d columna(s) de saldo, want 0: el saldo es computado, nunca escrito", got)
	}

	// El intento de escritura directa: PostgreSQL rechaza la columna inexistente.
	err := execOwner(t, db, `UPDATE fiado_accounts SET balance_cents = 9999 WHERE id = $1`, mustParseUUID(t, accountID))
	assertSQLState(t, err, sqlStateUndefinedColumn, "escribir un saldo directo sobre fiado_accounts")

	// Y el saldo computado es exactamente la suma firmada de los movimientos.
	seedMovement(t, db, tokenTenantID, movementID, accountID, 1500, "", "")
	seedMovement(t, db, tokenTenantID, paymentID, accountID, -500, "", "")
	got := queryCount(t, tenants, tokenTenantID, `SELECT COALESCE(SUM(amount_cents), 0) FROM fiado_movements WHERE fiado_account_id = $1`,
		mustParseUUID(t, accountID))
	if got != 1000 {
		t.Errorf("saldo computado = %d, want 1000 (1500 de cargo menos 500 de abono)", got)
	}
}

// TestFiadoMovementsHaveNoKindColumn: el signo ES el tipo. Una columna `kind` —o su familia—
// no existe en el ledger, así que no hay una segunda forma de decir lo que el signo ya dice
// (S11, docs/DATABASE.md §4 `fiado_movements`).
func TestFiadoMovementsHaveNoKindColumn(t *testing.T) {
	db := newThrowawayDatabase(t)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))

	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'fiado_movements' AND column_name IN ('kind', 'type', 'direction', 'movement_type')`); got != 0 {
		t.Fatalf("fiado_movements tiene %d columna(s) de tipo, want 0: el signo de amount_cents ES el tipo", got)
	}

	err := execOwner(t, db, `SELECT kind FROM fiado_movements LIMIT 1`)
	assertSQLState(t, err, sqlStateUndefinedColumn, "leer una columna kind del ledger de fiado")
}

// TestFiadoLedgerIsAppendOnly: la historia confirmada no se edita ni se borra. La guarda de
// disparo responde 55006 incluso para el rol dueño de la base —el superusuario que migra—, y
// la fila queda intacta después de los dos intentos (docs/DATABASE.md §2.12, MD-2).
func TestFiadoLedgerIsAppendOnly(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")
	seedMovement(t, db, tokenTenantID, movementID, accountID, 1500, "", "")

	updateErr := execOwner(t, db, `UPDATE fiado_movements SET amount_cents = 1 WHERE id = $1`, mustParseUUID(t, movementID))
	assertSQLState(t, updateErr, sqlStateAppendOnly, "editar un movimiento confirmado")

	deleteErr := execOwner(t, db, `DELETE FROM fiado_movements WHERE id = $1`, mustParseUUID(t, movementID))
	assertSQLState(t, deleteErr, sqlStateAppendOnly, "borrar un movimiento confirmado")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements WHERE amount_cents = 1500`); got != 1 {
		t.Errorf("el movimiento original quedó en %d fila(s), want 1: la guarda rechaza sin tocar la historia", got)
	}
}

// TestFiadoMovementSaleLinkageRequiresBothColumnsOrNeither: el enlace a la venta es un par de
// nulidad (`fiado_movements_sold_on_sale_id_pair_nullity_chk`, MD-9/F19). Un `sold_on` sin
// `sale_id` y un `sale_id` sin `sold_on` son los dos cuerpos inservibles; los dos juntos y los
// dos ausentes son las dos formas válidas.
func TestFiadoMovementSaleLinkageRequiresBothColumnsOrNeither(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedUser(t, db, tokenTenantID, tokenUserID)
	seedSale(t, db, tokenTenantID, tokenUserID, saleID, businessDate, 1500)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")

	insertSQL := `INSERT INTO fiado_movements
		(id, tenant_id, fiado_account_id, amount_cents, sold_on, sale_id, created_at, received_at)
		VALUES ($1, $2, $3, 1500, $4, $5, now(), now())`

	// sold_on sin sale_id.
	soldOnOnly := execOwner(t, db, insertSQL, mustParseUUID(t, "019a0000-0000-7000-8000-0000000000d1"),
		mustParseUUID(t, tokenTenantID), mustParseUUID(t, accountID), businessDate, nil)
	assertSQLState(t, soldOnOnly, sqlStateCheckViolation, "un sold_on sin sale_id")

	// sale_id sin sold_on.
	saleIDOnly := execOwner(t, db, insertSQL, mustParseUUID(t, "019a0000-0000-7000-8000-0000000000d2"),
		mustParseUUID(t, tokenTenantID), mustParseUUID(t, accountID), nil, mustParseUUID(t, saleID))
	assertSQLState(t, saleIDOnly, sqlStateCheckViolation, "un sale_id sin sold_on")

	// Las dos formas válidas: el par completo y la ausencia del par.
	both := execOwner(t, db, insertSQL, mustParseUUID(t, "019a0000-0000-7000-8000-0000000000d3"),
		mustParseUUID(t, tokenTenantID), mustParseUUID(t, accountID), businessDate, mustParseUUID(t, saleID))
	if both != nil {
		t.Errorf("el par completo (sold_on, sale_id) fue rechazado: %v", both)
	}

	seedMovement(t, db, tokenTenantID, movementID, accountID, -500, "", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements WHERE fiado_account_id = $1`,
		mustParseUUID(t, accountID)); got != 2 {
		t.Errorf("movimientos del deudor = %d, want 2 (uno enlazado a la venta y uno de abono)", got)
	}
}
