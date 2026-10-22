// Test de contrato del cierre de caja: demuestra que la base migrada cumple las dos piezas
// de esquema que el Sprint 5 exige —el cierre insert-once y el conteo por método
// append-only (W5-DB-1), y la proyección de dinero por método servida por índice con
// pruning de partición (W5-DB-2)— sin agregar una sola migración.
//
// Es la mitad de evidencia de dos tareas declaradas «verificación, migración solo si
// falla» (odd/tasks/sprint-5-cash-close-durability.md): por eso cada subprueba mide el
// CATÁLOGO y el PLAN de la base real, no el texto de una migración.
//
// Reutiliza el arnés de schema_contract_test.go: la base desechable, `exec`, `queryCount`,
// `queryStrings`, `queryOneString` y `randomSuffix` son los mismos. Se agrega solo lo que
// esos ayudantes no pueden hacer: leer el SQLSTATE junto con el nombre de la restricción
// que rechazó la escritura, y leer un plan de ejecución.
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" de database/sql, como en cmd/migrate
)

// SQLSTATE de las dos familias que este contrato distingue: una violación de unicidad (el
// segundo cierre de la misma fecha) y una violación de CHECK (el fiado contado distinto de
// cero). Verificar el código exacto es lo que separa «la restricción correcta rechazó» de
// «algún error hubo».
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateCheckViolation      = "23514"
	cashCloseDateForPartitions  = "2026-10-19"
	cashCloseTenantForFixtures  = "019a0000-0000-7000-8000-0000000000d1"
	cashCloseUserForFixtures    = "019a0000-0000-7000-8000-0000000000d2"
	cashCloseFirstFixtureID     = "019a0000-0000-7000-8000-0000000000d3"
	cashCloseSecondFixtureID    = "019a0000-0000-7000-8000-0000000000d4"
	projectionIndexName         = "sales_tenant_id_sold_on_payment_method_idx"
	cashCloseCountsPrimaryKeyOn = "public.cash_close_counts"
)

// primaryKeyColumnsSQL lee las columnas de la clave primaria en su ORDEN declarado.
// `indkey` es un arreglo y ANY(i.indkey) perdería el orden, así que se desanida WITH
// ORDINALITY: el orden es justamente lo que la PK del conteo afirma —(cash_close_id,
// payment_method), nunca el inverso—.
const primaryKeyColumnsSQL = `
SELECT a.attname
FROM pg_index i
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE i.indrelid = $1::regclass
  AND i.indisprimary
ORDER BY k.ord`

// indexColumnsSQL lee las columnas de UN índice en su orden declarado, por el mismo motivo
// que la PK: (tenant_id, sold_on, payment_method) sirve la proyección; el orden inverso no.
const indexColumnsSQL = `
SELECT a.attname
FROM pg_index i
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE i.indexrelid = $1::regclass
ORDER BY k.ord`

// indexFamilySQL lista el índice del PADRE y los índices hijos que PostgreSQL clonó en cada
// partición. Los hijos no conservan el nombre del padre —toman el de la partición como
// prefijo—, y el plan nombra al hijo que recorrió: la aserción del índice tiene que aceptar
// la familia completa en lugar de un nombre que solo existe en la tabla particionada.
const indexFamilySQL = `
SELECT c.relname
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = $1::regclass
ORDER BY c.relname`

// runtimePrivilegesOnCashCloseTablesSQL cuenta los pares (rol, tabla, privilegio) de
// UPDATE y DELETE concedidos a los cuatro roles de runtime sobre las dos tablas del cierre.
// El contador tiene que ser CERO: la matriz de §5.5 no concede ninguno, y la guarda de
// trigger de 20261019000001_append_only_guards.sql es la segunda mitad del contrato
// (grants_contract_test.go ya mide la matriz COMPLETA; esto es el complemento puntual de
// las dos tablas que esta tarea nombra).
const runtimePrivilegesOnCashCloseTablesSQL = `
SELECT count(*)
FROM unnest(ARRAY['pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance']) AS r(role_name)
CROSS JOIN unnest(ARRAY['cash_closes', 'cash_close_counts']) AS t(table_name)
CROSS JOIN unnest(ARRAY['UPDATE', 'DELETE']) AS p(privilege_name)
WHERE has_table_privilege(r.role_name, ('public.' || t.table_name)::regclass, p.privilege_name)`

// posAppSelectOnCashCloseTablesSQL es el contador de NO VACUIDAD de arriba: si los roles
// no tuvieran ninguna concesión sobre las tablas, el cero anterior no probaría nada.
const posAppSelectOnCashCloseTablesSQL = `
SELECT count(*)
FROM unnest(ARRAY['cash_closes', 'cash_close_counts']) AS t(table_name)
WHERE has_table_privilege('pos_app', ('public.' || t.table_name)::regclass, 'SELECT')`

// cashCloseProjectionSQL es la proyección F8 tal como la ejecuta el servidor: suma firmada
// por método de una fecha de negocio. El CAST a bigint es el que la superficie escanea
// (SUM sobre BIGINT devuelve NUMERIC; el acuse es int64), y la fecha va LITERAL porque es
// lo que el pruning de plan necesita para elegir la partición.
const cashCloseProjectionSQL = `
SELECT payment_method,
       COALESCE(SUM(CASE kind WHEN 'sale' THEN total_cents WHEN 'void' THEN -total_cents END), 0)::bigint
FROM sales
WHERE tenant_id = '` + cashCloseTenantForFixtures + `'::uuid
  AND sold_on = DATE '` + cashCloseDateForPartitions + `'
GROUP BY payment_method`

// TestCashCloseContract verifica las dos piezas de esquema del Sprint 5 sobre la base real.
func TestCashCloseContract(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	ctx := context.Background()

	exec(t, db, `INSERT INTO tenants (id, name, created_at) VALUES ($1, 'cierre de prueba', now())`,
		cashCloseTenantForFixtures)
	exec(t, db, `INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, '+51900000010', 'no-es-un-hash-real', now())`,
		cashCloseUserForFixtures, cashCloseTenantForFixtures)

	t.Run("a second close for the same tenant and business date is rejected by the unique constraint", func(t *testing.T) {
		// D10: un cierre por fecha de negocio por tenant. La primera fila entra.
		exec(t, db, `INSERT INTO cash_closes (id, tenant_id, closed_on, closed_by_user_id, created_at, received_at)
			VALUES ($1, $2, DATE '2026-10-19', $3, now(), now())`,
			cashCloseFirstFixtureID, cashCloseTenantForFixtures, cashCloseUserForFixtures)

		// La segunda —mismo tenant, misma fecha, OTRO id— la rechaza la UNIQUE nombrada.
		_, err := db.ExecContext(ctx, `INSERT INTO cash_closes (id, tenant_id, closed_on, closed_by_user_id, created_at, received_at)
			VALUES ($1, $2, DATE '2026-10-19', $3, now(), now())`,
			cashCloseSecondFixtureID, cashCloseTenantForFixtures, cashCloseUserForFixtures)
		expectSQLStateOnConstraint(t, err, sqlStateUniqueViolation, "cash_closes_tenant_closed_on_key",
			"insertar un segundo cierre de la misma fecha")

		if got := queryCount(t, db, `SELECT count(*) FROM cash_closes WHERE tenant_id = $1`, cashCloseTenantForFixtures); got != 1 {
			t.Errorf("cash_closes = %d fila(s), want 1: el segundo cierre no puede insertarse", got)
		}
	})

	t.Run("the cash_close_counts primary key is (cash_close_id, payment_method) in that order", func(t *testing.T) {
		got := queryStrings(t, db, primaryKeyColumnsSQL, cashCloseCountsPrimaryKeyOn)
		want := []string{"cash_close_id", "payment_method"}

		if !slices.Equal(got, want) {
			t.Errorf("clave primaria de cash_close_counts = %v, want %v (F1: la clave compuesta natural, sin id sustituto)", got, want)
		}
	})

	t.Run("counted fiado is structurally zero", func(t *testing.T) {
		// El CHECK es la mitad estructural de D11/F32: el fiado esperado es el crédito
		// extendido del día y lo contado siempre es cero.
		_, err := db.ExecContext(ctx, `INSERT INTO cash_close_counts
			(cash_close_id, payment_method, tenant_id, expected_cents, counted_cents, created_at)
			VALUES ($1, 'fiado', $2, 5000, 100, now())`,
			cashCloseFirstFixtureID, cashCloseTenantForFixtures)
		expectSQLStateOnConstraint(t, err, sqlStateCheckViolation, "cash_close_counts_fiado_counted_chk",
			"insertar un fiado contado distinto de cero")

		exec(t, db, `INSERT INTO cash_close_counts
			(cash_close_id, payment_method, tenant_id, expected_cents, counted_cents, created_at)
			VALUES ($1, 'fiado', $2, 5000, 0, now())`,
			cashCloseFirstFixtureID, cashCloseTenantForFixtures)

		if got := queryCount(t, db, `SELECT count(*) FROM cash_close_counts
			WHERE cash_close_id = $1 AND payment_method = 'fiado' AND counted_cents = 0`,
			cashCloseFirstFixtureID); got != 1 {
			t.Errorf("fiado contado en cero = %d fila(s), want 1: la regla es estructural, no convencional", got)
		}
	})

	t.Run("no runtime role holds UPDATE or DELETE on the two cash-close tables", func(t *testing.T) {
		if got := queryCount(t, db, posAppSelectOnCashCloseTablesSQL); got != 2 {
			t.Fatalf("pos_app tiene SELECT sobre %d de las 2 tablas del cierre, want 2: sin lectura el contador de abajo sería un verde vacío", got)
		}
		if got := queryCount(t, db, runtimePrivilegesOnCashCloseTablesSQL); got != 0 {
			t.Errorf("%d concesión(es) de UPDATE/DELETE sobre las tablas del cierre, want 0: el cierre es insert-once y el conteo append-only (§5.5, MD-2)", got)
		}
	})

	t.Run("sales_tenant_id_sold_on_payment_method_idx exists with its columns in order", func(t *testing.T) {
		got := queryStrings(t, db, indexColumnsSQL, "public."+projectionIndexName)
		want := []string{"tenant_id", "sold_on", "payment_method"}

		if !slices.Equal(got, want) {
			t.Errorf("columnas de %s = %v, want %v (las tres en ese orden son lo que sirve la proyección F8)", projectionIndexName, got, want)
		}
	})

	t.Run("the per-method projection prunes to the single partition of the business date", func(t *testing.T) {
		plan := explainPlan(t, db, cashCloseProjectionSQL)
		t.Logf("plan de la proyección:\n%s", plan)

		// El pruning de plan elige la partición por el literal de la fecha: el plan nombra
		// la partición del mes y NINGUNA otra, ni la red de seguridad.
		target := "sales_" + strings.ReplaceAll(cashCloseDateForPartitions[:7], "-", "_")
		if !strings.Contains(plan, target) {
			t.Errorf("el plan no nombra la partición %s:\n%s", target, plan)
		}

		for _, partition := range queryStrings(t, db, `SELECT c.relname
			FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
			WHERE i.inhparent = 'public.sales'::regclass`) {
			if partition == target {
				continue
			}
			if strings.Contains(plan, partition) {
				t.Errorf("el plan nombra la partición %s, que la fecha de negocio no toca:\n%s", partition, plan)
			}
		}
	})

	t.Run("the same projection is served by the index, not by a sequential scan", func(t *testing.T) {
		// La fecha literal acota la tabla a UNA partición, pero eso no obliga al planner a
		// usar el índice: sobre una tabla vacía el recorrido secuencial es más barato y el
		// plan elegiría Seq Scan igual. `enable_seqscan = off` es la perilla de la sesión
		// que sube el costo del recorrido secuencial para que la pregunta sea la que
		// importa: con el índice disponible, ¿el planner lo usa para esta consulta? Es SET
		// LOCAL, así que vive en esta transacción y no toca la siguiente.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción del plan: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
			t.Fatalf("desactivar el recorrido secuencial: %v", err)
		}

		plan := explainPlanOn(t, tx, cashCloseProjectionSQL)
		t.Logf("plan con enable_seqscan off:\n%s", plan)

		if !planUsesAnyIndex(t, db, plan) {
			t.Errorf("el plan no usa la familia de %s:\n%s", projectionIndexName, plan)
		}
		if strings.Contains(plan, "Seq Scan on sales") {
			t.Errorf("el plan recorre sales secuencialmente con el índice disponible:\n%s", plan)
		}
	})
}

// explainPlan devuelve el plan de una consulta como texto, sin costos: la aserción mira
// nombres de relación, y los costos cambian con cada corrida.
func explainPlan(t *testing.T, db *sql.DB, query string) string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), "EXPLAIN (COSTS OFF) "+query)
	if err != nil {
		t.Fatalf("explicar %s: %v", oneline(query), err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("leer el plan: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer el plan: %v", err)
	}
	return strings.Join(lines, "\n")
}

// explainPlanOn es explainPlan() para una transacción, donde vive el `SET LOCAL` de la
// prueba que desactiva el recorrido secuencial.
func explainPlanOn(t *testing.T, tx *sql.Tx, query string) string {
	t.Helper()

	rows, err := tx.QueryContext(context.Background(), "EXPLAIN (COSTS OFF) "+query)
	if err != nil {
		t.Fatalf("explicar %s: %v", oneline(query), err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("leer el plan: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer el plan: %v", err)
	}
	return strings.Join(lines, "\n")
}

// planUsesAnyIndex informa si el plan nombra el índice del padre o alguno de sus hijos: el
// recorrido real ocurre sobre la partición, cuyo índice lleva el nombre de la partición
// como prefijo, así que exigir el nombre del padre sería exigir el nombre equivocado.
func planUsesAnyIndex(t *testing.T, db *sql.DB, plan string) bool {
	t.Helper()

	names := append([]string{projectionIndexName}, queryStrings(t, db, indexFamilySQL, "public."+projectionIndexName)...)
	for _, name := range names {
		if strings.Contains(plan, name) {
			return true
		}
	}
	return false
}

// expectSQLStateOnConstraint exige el SQLSTATE Y el nombre de la restricción: dos UNIQUE
// distintas del mismo esquema comparten el 23505, así que el código solo no prueba que la
// guarda correcta haya sido la que rechazó la escritura.
func expectSQLStateOnConstraint(t *testing.T, err error, wantCode, wantConstraint, doing string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: no hubo error, want SQLSTATE %s de %s", doing, wantCode, wantConstraint)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: el error no es de PostgreSQL y no tiene SQLSTATE: %v", doing, err)
	}
	if pgErr.Code != wantCode {
		t.Fatalf("%s: SQLSTATE = %q (%s), want %q", doing, pgErr.Code, pgErr.Message, wantCode)
	}
	if pgErr.ConstraintName != wantConstraint {
		t.Fatalf("%s: la restricción que rechazó = %q, want %q", doing, pgErr.ConstraintName, wantConstraint)
	}
}
