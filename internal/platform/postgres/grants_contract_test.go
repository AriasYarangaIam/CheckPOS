// Test de contrato de concesiones: demuestra que la base migrada TIENE la matriz de cinco
// roles de docs/DATABASE.md §5.5 —columna por columna— y que los dos caminos de bootstrap
// pre-auth del §5.4 son verificables.
//
// Reutiliza el arnés de schema_contract_test.go: la base desechable, `exec`, `queryCount`,
// `queryStrings`, `queryOneString` y `randomSuffix` son los mismos. Se agrega solo lo que
// esos ayudantes no pueden hacer: consultar con un rol de runtime distinto del de
// migración (una conexión propia, con su propio DSN) y consultar dentro de una transacción,
// donde vive el contexto de tenant.
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" de database/sql, como en cmd/migrate

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// sqlStateInsufficientPrivilege es el SQLSTATE 42501 de PostgreSQL. Lo comparten dos cosas
// que el contrato distingue pero la base reporta igual: la falta de una concesión y la
// violación de un `WITH CHECK` de una política (docs/DATABASE.md §5.3, §5.5). Por eso cada
// aserción dice en su mensaje cuál de las dos esperaba.
const sqlStateInsufficientPrivilege = "42501"

// setTenantSettingSQL fija el contexto de tenant de la transacción actual. `SET LOCAL` no
// acepta marcadores de posición, así que la forma parametrizable es `set_config(..., true)`
// con el tercer argumento ligado a la transacción; las políticas leen siempre
// `current_setting('app.current_tenant_id')` (docs/DATABASE.md §5.2).
const setTenantSettingSQL = `SELECT set_config('app.current_tenant_id', $1, true)`

// runtimeRoles son los cuatro roles de runtime de la matriz. El quinto, owner/migration, es
// el rol que ya existe y que aplicó esta migración: no se crea y no se le concede nada
// (docs/DATABASE.md §5.5).
var runtimeRoles = []string{"pos_app", "pos_auth", "pos_platform", "pos_maintenance"}

// tenantTables son las once tablas de tenant de docs/DATABASE.md §5.1, en orden de bytes de
// Go. Son exactamente las once con `SELECT` para `pos_app` y exactamente las once con RLS de
// tenant (`rls_contract_test.go`).
var tenantTables = []string{
	"cash_close_counts",
	"cash_closes",
	"devices",
	"fiado_accounts",
	"fiado_movements",
	"idempotency_keys",
	"products",
	"sale_lines",
	"sales",
	"stock_adjustments",
	"users",
}

// posAppUpdateTables son las únicas cuatro tablas con UPDATE para `pos_app`: LWW de catálogo
// y perfil, el ciclo de vida del claim y el ciclo de vida de autenticación del dispositivo.
var posAppUpdateTables = []string{
	"devices",
	"fiado_accounts",
	"idempotency_keys",
	"products",
}

// posPlatformTables son las dos tablas globales curadas por plataforma. Son las únicas
// tablas fuera del RLS de tenant con concesiones para un rol que no es el de migración.
var posPlatformTables = []string{
	"global_barcodes",
	"global_products",
}

// roleTableGrantsSQL lee la matriz efectiva del catálogo, no el texto de la migración: si la
// migración concede algo que §5.5 no pide, la comparación falla por exceso, y si le falta
// algo, falla por defecto.
//
// Se excluyen dos cosas a propósito, por la misma razón que en el contrato de esquema:
// `goose_db_version` (contabilidad del runner, no datos de negocio) y las particiones de
// `sales` (relispartition en true). Las particiones no son tablas del contrato: son el
// almacenamiento físico de `sales`, y su espejo de concesiones se verifica aparte.
const roleTableGrantsSQL = `
SELECT g.grantee || '|' || g.table_name || '|' || g.privilege_type
FROM information_schema.role_table_grants g
JOIN pg_class c
  ON c.relname = g.table_name
 AND c.relnamespace = to_regnamespace(g.table_schema)
WHERE g.table_schema = 'public'
  AND g.grantee IN ('pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance')
  AND NOT c.relispartition
  AND g.table_name <> 'goose_db_version'
ORDER BY 1`

// salesPartitionsWithoutSelectSQL cuenta las particiones de `sales` que NO llevan la
// concesión de `SELECT` de su tabla padre. PostgreSQL no propaga las concesiones del padre a
// las particiones, así que esta consulta mide el espejo que la migración tiene que aplicar.
const salesPartitionsWithoutSelectSQL = `
SELECT count(*)
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = 'public.sales'::regclass
  AND NOT has_table_privilege('pos_app', c.oid, 'SELECT')`

// TestGrantsContract verifica la matriz de docs/DATABASE.md §5.5 contra el catálogo y contra
// el comportamiento real de los roles. La idempotencia de la creación de roles queda probada
// por la propia corrida: cada prueba de integración migra su base desechable y los roles
// —que son del clúster— ya existen desde la segunda, así que la migración no puede abortar.
func TestGrantsContract(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	t.Run("the four runtime roles exist, can log in and carry no BYPASSRLS", func(t *testing.T) {
		// La matriz nombra cinco roles; el quinto es el rol dueño que ya migró. Que estén
		// los cuatro de runtime y que ninguno pueda saltarse el RLS es el entregable de
		// W2-DB-2: sin `BYPASSRLS`, ninguna consulta de runtime puede esquivar las
		// políticas de la migración siguiente.
		got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_roles
			WHERE rolname IN ('pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance')`)
		if got != len(runtimeRoles) {
			t.Fatalf("roles de runtime presentes = %d, want %d", got, len(runtimeRoles))
		}

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_roles
			WHERE rolname IN ('pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance')
			  AND NOT rolcanlogin`); got != 0 {
			t.Errorf("%d rol(es) de runtime sin LOGIN, want 0", got)
		}

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_roles
			WHERE rolname IN ('pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance')
			  AND rolbypassrls`); got != 0 {
			t.Errorf("%d rol(es) de runtime con BYPASSRLS, want 0 (la matriz no lo concede nunca)", got)
		}
	})

	t.Run("each role holds exactly the §5.5 grant set", func(t *testing.T) {
		got := queryStrings(t, db, roleTableGrantsSQL)
		slices.Sort(got)

		want := expectedGrantMatrix()
		if !slices.Equal(got, want) {
			t.Errorf("la matriz de concesiones difiere:\n got: %v\nwant: %v", got, want)
		}
	})

	t.Run("pos_app holds no SELECT on tenants and no DELETE anywhere", func(t *testing.T) {
		if got := queryCount(t, db, `
			SELECT count(*)
			FROM information_schema.role_table_grants
			WHERE grantee = 'pos_app'
			  AND table_name = 'tenants'
			  AND privilege_type = 'SELECT'`); got != 0 {
			t.Errorf("pos_app tiene %d concesión(es) de SELECT sobre tenants, want 0 (F14)", got)
		}

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM information_schema.role_table_grants
			WHERE grantee IN ('pos_app', 'pos_auth', 'pos_platform')
			  AND privilege_type = 'DELETE'`); got != 0 {
			t.Errorf("%d concesión(es) de DELETE para roles de runtime distintos de pos_maintenance, want 0", got)
		}

		// El único DELETE de la matriz es el de la retención de 400 días, sobre una sola
		// tabla (MD-3).
		got := queryStrings(t, db, `
			SELECT grantee || '|' || table_name
			FROM information_schema.role_table_grants
			WHERE privilege_type = 'DELETE'
			  AND grantee IN ('pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance')`)
		want := []string{"pos_maintenance|idempotency_keys"}
		if !slices.Equal(got, want) {
			t.Errorf("las concesiones de DELETE difieren:\n got: %v\nwant: %v", got, want)
		}
	})

	t.Run("pos_app can read every sales partition directly, not just the parent", func(t *testing.T) {
		// PostgreSQL no propaga las concesiones de la tabla particionada a sus particiones
		// ni a las futuras. La migración las espeja una por una, y esta consulta es la que
		// mide el espejo.
		if got := queryCount(t, db, salesPartitionsWithoutSelectSQL); got != 0 {
			t.Errorf("%d partición(es) de sales sin SELECT para pos_app, want 0", got)
		}

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_inherits i
			JOIN pg_class c ON c.oid = i.inhrelid
			WHERE i.inhparent = 'public.sales'::regclass`); got == 0 {
			t.Fatal("sales no tiene particiones: la prueba del espejo de concesiones no probó nada")
		}
	})

	t.Run("pos_auth reads users and cannot read products", func(t *testing.T) {
		// Se prueba con una conexión real del rol, no con `has_table_privilege`: lo que el
		// contrato pide es que el login pueda hacer exactamente una consulta y que no
		// alcance ninguna otra tabla (docs/DATABASE.md §5.4).
		seedTenantAndUser(t, db,
			"019a0000-0000-7000-8000-0000000000c1",
			"019a0000-0000-7000-8000-0000000000c2",
			"+51900000001")

		posAuth := roleConnection(t, db, config.AuthDatabaseURL(), "pos_auth")

		if got := queryInt(t, posAuth, `SELECT count(*) FROM users`); got != 1 {
			t.Errorf("pos_auth ve %d fila(s) de users, want 1", got)
		}

		_, err := posAuth.ExecContext(context.Background(), `SELECT count(*) FROM products`)
		expectSQLState(t, err, sqlStateInsufficientPrivilege, "pos_auth leyendo products")
	})

	t.Run("the signup bootstrap path works as pos_app without SELECT on tenants", func(t *testing.T) {
		// §5.4: una transacción que inserta el tenant, fija el GUC al id recién creado e
		// inserta el usuario. La decisión D3 del documento de tarea quita el `RETURNING`
		// del readback: la aplicación ya genera el UUIDv7 (§2.2), así que no necesita leer
		// la fila que acaba de escribir. Ninguna sentencia de este camino usa SELECT sobre
		// `tenants`.
		const (
			tenantID = "019a0000-0000-7000-8000-0000000000c3"
			userID   = "019a0000-0000-7000-8000-0000000000c4"
			phone    = "+51900000002"
		)

		posApp := roleConnection(t, db, config.AppDatabaseURL(), "pos_app")

		tx, err := posApp.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("abrir la transacción de alta: %v", err)
		}

		execOn(t, tx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, now())`, tenantID, "alta de prueba")
		setTenantContext(t, tx, tenantID)
		execOn(t, tx, `
			INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
			VALUES ($1, $2, $3, $4, now())`, userID, tenantID, phone, "no-es-un-hash-real")

		if err := tx.Commit(); err != nil {
			t.Fatalf("confirmar la transacción de alta: %v", err)
		}

		// Se verifica como dueño: la fila existe, con el tenant del GUC y no otro.
		if got := queryCount(t, db, `SELECT count(*) FROM tenants WHERE id = $1`, tenantID); got != 1 {
			t.Errorf("el alta dejó %d fila(s) en tenants, want 1", got)
		}
		if got := queryCount(t, db, `
			SELECT count(*)
			FROM users
			WHERE id = $1 AND tenant_id = $2`, userID, tenantID); got != 1 {
			t.Errorf("el alta dejó %d fila(s) en users con el tenant esperado, want 1", got)
		}
	})

	t.Run("INSERT tenants ... RETURNING id fails for pos_app (decision D3 evidence)", func(t *testing.T) {
		// Esta prueba es la evidencia de que el mecanismo que declara docs/DATABASE.md §5.4
		// no puede funcionar bajo la matriz F14 del §5.5: PostgreSQL exige privilegio
		// SELECT sobre las columnas que devuelve un `RETURNING`, y `pos_app` no tiene
		// SELECT sobre `tenants` por diseño. §5.4 dice literalmente que el readback "no
		// necesita SELECT sobre tenants", y eso es falso en PostgreSQL. La decisión D3
		// registra la divergencia y elige la invariante vinculante (F14) más el id generado
		// por la aplicación (§2.2): el alta no lee lo que escribió.
		posApp := roleConnection(t, db, config.AppDatabaseURL(), "pos_app")

		tx, err := posApp.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("abrir la transacción de la sonda: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		var returnedID string
		err = tx.QueryRowContext(context.Background(), `
			INSERT INTO tenants (id, name, created_at)
			VALUES ($1, $2, now())
			RETURNING id`, "019a0000-0000-7000-8000-0000000000c5", "sonda D3").Scan(&returnedID)

		expectSQLState(t, err, sqlStateInsufficientPrivilege, "pos_app haciendo INSERT ... RETURNING sobre tenants")
	})
}

// expectedGrantMatrix construye la matriz de §5.5 como el mismo conjunto de cadenas
// "rol|tabla|privilegio" que devuelve roleTableGrantsSQL, para que la comparación sea
// columna por columna y no un conteo.
func expectedGrantMatrix() []string {
	var grants []string
	add := func(role, table, privilege string) {
		grants = append(grants, role+"|"+table+"|"+privilege)
	}

	// pos_app: SELECT sobre las once tablas de negocio, nunca sobre `tenants` ni sobre las
	// dos globales; INSERT sobre las once más `tenants` (el alta pre-auth); UPDATE sobre
	// cuatro; DELETE sobre ninguna.
	for _, table := range tenantTables {
		add("pos_app", table, "SELECT")
		add("pos_app", table, "INSERT")
	}
	add("pos_app", "tenants", "INSERT")
	for _, table := range posAppUpdateTables {
		add("pos_app", table, "UPDATE")
	}

	// pos_auth: SELECT sobre `users` y sobre nada más.
	add("pos_auth", "users", "SELECT")

	// pos_platform: SELECT, INSERT y UPDATE sobre las dos tablas globales; DELETE sobre
	// ninguna (§5.5 es la matriz vinculante; la prosa de §4 `global_barcodes` que menciona
	// DELETE por `pos_platform` queda por debajo de la matriz).
	for _, table := range posPlatformTables {
		add("pos_platform", table, "SELECT")
		add("pos_platform", table, "INSERT")
		add("pos_platform", table, "UPDATE")
	}

	// pos_maintenance: DELETE sobre `idempotency_keys` únicamente (retención de 400 días).
	add("pos_maintenance", "idempotency_keys", "DELETE")

	slices.Sort(grants)
	return grants
}

// seedTenantAndUser inserta un tenant y su usuario como dueño, con el contexto de tenant
// fijado en la misma transacción: el camino es correcto antes y después de la migración de
// RLS, porque `users` queda con FORCE RLS y el GUC tiene que estar puesto para escribir la
// fila (docs/DATABASE.md §5.2).
func seedTenantAndUser(t *testing.T, db *sql.DB, tenantID, userID, phone string) {
	t.Helper()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra: %v", err)
	}

	execOn(t, tx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, now())`, tenantID, "tenant de prueba")
	setTenantContext(t, tx, tenantID)
	execOn(t, tx, `
		INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, $3, $4, now())`, userID, tenantID, phone, "no-es-un-hash-real")

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra: %v", err)
	}
}

// setTenantContext fija el contexto de tenant de la transacción y comprueba el valor
// devuelto, para que una prueba no siga adelante creyendo que el GUC quedó puesto.
func setTenantContext(t *testing.T, q querier, tenantID string) {
	t.Helper()

	var got string
	if err := q.QueryRowContext(context.Background(), setTenantSettingSQL, tenantID).Scan(&got); err != nil {
		t.Fatalf("fijar el contexto de tenant: %v", err)
	}
	if got != tenantID {
		t.Fatalf("app.current_tenant_id = %q, want %q", got, tenantID)
	}
}

// roleConnection abre una conexión con el rol de runtime dado contra la MISMA base
// desechable que la prueba ya migró. El nombre de la base se lee de la conexión abierta en
// lugar de volver a construir el DSN: así no hay una segunda copia de la cadena de conexión
// que pueda apuntar a otra base.
func roleConnection(t *testing.T, ownerDB *sql.DB, baseDSN, role string) *sql.DB {
	t.Helper()

	name := queryOneString(t, ownerDB, "SELECT current_database()")
	dsn, err := postgres.WithDatabase(baseDSN, name)
	if err != nil {
		t.Fatalf("derivar el DSN del rol %s para la base desechable %s: %v", role, name, err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("abrir la conexión del rol %s: %v", role, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("conectar como %s a %s: %v", role, name, err)
	}

	return db
}

// querier es lo que comparten *sql.DB, *sql.Conn y *sql.Tx. Las pruebas de RLS corren la
// misma consulta en tres contextos —sin contexto de tenant, dentro de una transacción con el
// GUC fijado y sobre una conexión fijada de un solo hilo—, y los ayudantes del arnés de
// schema_contract_test.go solo aceptan *sql.DB.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// queryInt es queryCount() para una transacción o una conexión fijada.
func queryInt(t *testing.T, q querier, query string, args ...any) int {
	t.Helper()

	var got int
	if err := q.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("consultar %s: %v", oneline(query), err)
	}
	return got
}

// execOn es exec() para una transacción o una conexión fijada.
func execOn(t *testing.T, q querier, query string, args ...any) {
	t.Helper()

	if _, err := q.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("ejecutar %s: %v", oneline(query), err)
	}
}

// expectSQLState exige que la operación haya fallado con el SQLSTATE exacto, no con
// "algún error": un `permission denied` y un `WITH CHECK` rechazado comparten el 42501, pero
// un error de sintaxis o una tabla inexistente también serían "algún error" y no probarían
// nada.
func expectSQLState(t *testing.T, err error, want, doing string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: no hubo error, want SQLSTATE %s", doing, want)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: el error no es de PostgreSQL y no tiene SQLSTATE: %v", doing, err)
	}
	if pgErr.Code != want {
		t.Fatalf("%s: SQLSTATE = %q (%s), want %q", doing, pgErr.Code, pgErr.Message, want)
	}
}
