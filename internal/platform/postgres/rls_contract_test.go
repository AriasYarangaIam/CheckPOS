// Test de contrato de RLS: demuestra que el aislamiento por tenant de docs/DATABASE.md §5
// es un invariante de la base y no una convención del código.
//
// Reutiliza el arnés de schema_contract_test.go —la base desechable, `exec`, `queryCount`,
// `queryStrings`, `queryOneString` y las constantes de particionado— y las conexiones por rol
// de grants_contract_test.go. La forma *fail-then-pass* del §5.7 se demuestra, no se afirma:
// con el RLS apagado la misma consulta cross-tenant devuelve las filas del otro tenant y con
// el RLS puesto devuelve cero.
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// fixtureSoldOn es la fecha de negocio de las ventas sembradas. Cae dentro de una partición
// mensual que la migración de ledger crea, así que la fila aterriza en una partición real y
// no en `sales_default`.
const fixtureSoldOn = "2026-11-15"

// nilUUID es el marcador del parámetro de tenant: el default a nivel de base que la migración
// deja en `app.current_tenant_id`. No es el id de ningún tenant, así que una sesión que no
// fijó su contexto compara contra un id que no existe y no ve ninguna fila.
const nilUUID = "00000000-0000-0000-0000-000000000000"

// tenantPolicyPredicate es el fragmento literal de docs/DATABASE.md §5.3, el mismo que la
// migración escribe en las políticas. La prueba compara las expresiones guardadas contra este
// texto: cualquier `NULLIF`, cualquier segundo argumento de `current_setting` o cualquier
// wrapper cambia el texto y falla.
const tenantPolicyPredicate = `tenant_id = current_setting('app.current_tenant_id')::uuid`

// tenantPoliciesSQL lee el texto de cada política de tenant tal como quedó guardado, con sus
// dos expresiones: `polqual` (el USING, que filtra las filas visibles) y `polwithcheck` (el
// WITH CHECK, que rechaza escrituras con otro tenant). Una política de INSERT no tiene USING y
// una de DELETE no tiene WITH CHECK, así que la ausencia se marca con `-`.
const tenantPoliciesSQL = `
SELECT c.relname || '|' || p.polname
     || '|' || coalesce(pg_get_expr(p.polqual, p.polrelid), '-')
     || '|' || coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '-')
FROM pg_policy p
JOIN pg_class c ON c.oid = p.polrelid
WHERE p.polname ~ '_tenant_(select|insert|update|delete)$'
ORDER BY 1`

// policyResidueSQL busca cualquier rastro de la forma tolerante que la migración NO usa:
// `NULLIF`, `missing_ok` o el segundo argumento de `current_setting`. La cadena vacía sería el
// único motivo para escribirla, y el default del marcador la vuelve innecesaria.
const policyResidueSQL = `
SELECT count(*)
FROM pg_policy p
WHERE p.polname ~ '_tenant_(select|insert|update|delete)$'
  AND (coalesce(pg_get_expr(p.polqual, p.polrelid), '') ~* 'nullif|missing_ok|, true'
    OR coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '') ~* 'nullif|missing_ok|, true')`

// databasePlaceholderSQL cuenta las entradas de default de `app.current_tenant_id` de esta
// base con el rol indicado (0 = todos los roles).
const databasePlaceholderSQL = `
SELECT count(*)
FROM pg_db_role_setting s
JOIN pg_database d ON d.oid = s.setdatabase
WHERE d.datname = current_database()
  AND s.setrole = $1
  AND s.setconfig @> ARRAY['app.current_tenant_id=' || $2]::text[]`

// tenantFixture son los identificadores de las once tablas de tenant para un tenant de
// prueba: uno por tabla, para que cada conteo sea inequívoco (una fila por tabla y por
// tenant).
type tenantFixture struct {
	label            string
	tenantID         string
	userID           string
	phone            string
	deviceID         string
	productID        string
	adjustmentID     string
	saleID           string
	fiadoAccountID   string
	fiadoMovementID  string
	cashCloseID      string
	idempotencyKeyID string
}

// seed inserta una fila en cada una de las once tablas de tenant como DUEÑO, con el contexto
// de tenant fijado en la misma transacción.
//
// El contexto hace falta incluso siendo el dueño: FORCE RLS liga al dueño de la tabla (§5.5),
// de modo que la siembra se escribe por el mismo camino que la aplicación. En el clúster
// local el rol de migración es además superusuario, y un superusuario sí queda fuera del RLS
// incluso con FORCE (medido: sin GUC y con FORCE puesto, ve las filas de los dos tenants);
// por eso la mitad «liga al dueño» de FORCE se verifica contra el catálogo —relforcerowsecurity
// en verdadero— y el comportamiento se verifica con conexiones reales de pos_app y pos_auth.
func (f tenantFixture) seed(t *testing.T, db *sql.DB) {
	t.Helper()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra del tenant %s: %v", f.label, err)
	}

	execOn(t, tx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, now())`, f.tenantID, "tenant "+f.label)
	setTenantContext(t, tx, f.tenantID)

	execOn(t, tx, `
		INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, $3, $4, now())`, f.userID, f.tenantID, f.phone, "no-es-un-hash-real")

	execOn(t, tx, `
		INSERT INTO devices (id, tenant_id, user_id, pin_hash, offline_session_valid_until, created_at)
		VALUES ($1, $2, $3, $4, now() + interval '7 days', now())`, f.deviceID, f.tenantID, f.userID, "no-es-un-hash-real")

	execOn(t, tx, `
		INSERT INTO products (id, tenant_id, name, gtin, price_cents, uom, created_at, updated_at, received_at)
		VALUES ($1, $2, $3, NULL, 1000, 'unit', now(), now(), now())`, f.productID, f.tenantID, "producto "+f.label)

	execOn(t, tx, `
		INSERT INTO stock_adjustments (id, tenant_id, product_id, qty_delta, source, created_at, received_at)
		VALUES ($1, $2, $3, -1, 'manual', now(), now())`, f.adjustmentID, f.tenantID, f.productID)

	execOn(t, tx, `
		INSERT INTO sales (sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method, total_cents, created_at, received_at)
		VALUES ($1, $2, $3, $4, 'sale', 'cash', 1000, now(), now())`, fixtureSoldOn, f.saleID, f.tenantID, f.userID)

	execOn(t, tx, `
		INSERT INTO sale_lines (sale_id, line_no, tenant_id, sold_on, product_id, name, price_cents, uom, qty, line_total_cents, created_at)
		VALUES ($1, 1, $2, $3, $4, $5, 1000, 'unit', 1, 1000, now())`,
		f.saleID, f.tenantID, fixtureSoldOn, f.productID, "producto "+f.label)

	execOn(t, tx, `
		INSERT INTO fiado_accounts (id, tenant_id, name, created_at, updated_at, received_at)
		VALUES ($1, $2, $3, now(), now(), now())`, f.fiadoAccountID, f.tenantID, "cliente "+f.label)

	execOn(t, tx, `
		INSERT INTO fiado_movements (id, tenant_id, fiado_account_id, amount_cents, created_at, received_at)
		VALUES ($1, $2, $3, 500, now(), now())`, f.fiadoMovementID, f.tenantID, f.fiadoAccountID)

	execOn(t, tx, `
		INSERT INTO cash_closes (id, tenant_id, closed_on, closed_by_user_id, created_at, received_at)
		VALUES ($1, $2, $3, $4, now(), now())`, f.cashCloseID, f.tenantID, fixtureSoldOn, f.userID)

	execOn(t, tx, `
		INSERT INTO cash_close_counts (cash_close_id, payment_method, tenant_id, expected_cents, counted_cents, created_at)
		VALUES ($1, 'cash', $2, 1000, 1000, now())`, f.cashCloseID, f.tenantID)

	execOn(t, tx, `
		INSERT INTO idempotency_keys (id, tenant_id, key, payload_hash, state, created_at)
		VALUES ($1, $2, $3, 'hash-de-prueba', 'in_flight', now())`, f.idempotencyKeyID, f.tenantID, "clave-"+f.label)

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra del tenant %s: %v", f.label, err)
	}
}

// TestRLSContract verifica el pin de FORCE RLS, el predicado de las políticas y la forma
// fail-then-pass, sobre las once tablas de tenant de §5.1 y sobre las particiones físicas de
// `sales`.
func TestRLSContract(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	ctx := context.Background()

	tenantA := tenantFixture{
		label:            "A",
		tenantID:         "019a0000-0000-7000-8000-0000000000a1",
		userID:           "019a0000-0000-7000-8000-0000000000a2",
		phone:            "+51900000101",
		deviceID:         "019a0000-0000-7000-8000-0000000000a3",
		productID:        "019a0000-0000-7000-8000-0000000000a4",
		adjustmentID:     "019a0000-0000-7000-8000-0000000000a5",
		saleID:           "019a0000-0000-7000-8000-0000000000a6",
		fiadoAccountID:   "019a0000-0000-7000-8000-0000000000a7",
		fiadoMovementID:  "019a0000-0000-7000-8000-0000000000a8",
		cashCloseID:      "019a0000-0000-7000-8000-0000000000a9",
		idempotencyKeyID: "019a0000-0000-7000-8000-0000000000aa",
	}
	tenantB := tenantFixture{
		label:            "B",
		tenantID:         "019a0000-0000-7000-8000-0000000000b1",
		userID:           "019a0000-0000-7000-8000-0000000000b2",
		phone:            "+51900000102",
		deviceID:         "019a0000-0000-7000-8000-0000000000b3",
		productID:        "019a0000-0000-7000-8000-0000000000b4",
		adjustmentID:     "019a0000-0000-7000-8000-0000000000b5",
		saleID:           "019a0000-0000-7000-8000-0000000000b6",
		fiadoAccountID:   "019a0000-0000-7000-8000-0000000000b7",
		fiadoMovementID:  "019a0000-0000-7000-8000-0000000000b8",
		cashCloseID:      "019a0000-0000-7000-8000-0000000000b9",
		idempotencyKeyID: "019a0000-0000-7000-8000-0000000000ba",
	}

	tenantA.seed(t, db)
	tenantB.seed(t, db)

	posApp := roleConnection(t, db, config.AppDatabaseURL(), "pos_app")

	t.Run("ENABLE and FORCE row level security on the eleven tenant tables", func(t *testing.T) {
		// Las once tablas de §5.1, y ninguna otra de negocio: `tenants` es el registro y las
		// dos globales son curadas por plataforma, así que viven fuera del RLS de tenant
		// (§3, §5.1).
		got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public'
			  AND c.relname = ANY($1)
			  AND c.relrowsecurity
			  AND c.relforcerowsecurity`, tenantTables)
		if got != len(tenantTables) {
			t.Errorf("tablas de tenant con RLS y FORCE RLS = %d, want %d", got, len(tenantTables))
		}

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public'
			  AND c.relname IN ('tenants', 'global_products', 'global_barcodes')
			  AND c.relrowsecurity`); got != 0 {
			t.Errorf("%d relación(es) fuera del RLS de tenant con RLS puesta, want 0", got)
		}

		// Las particiones de `sales` no heredan el pin del padre: PostgreSQL no propaga ni las
		// políticas ni el RLS a las particiones, así que la migración las recorre una por una.
		// Sin eso, un SELECT directo sobre una partición vería todos los tenants (medido).
		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_inherits i
			JOIN pg_class c ON c.oid = i.inhrelid
			WHERE i.inhparent = 'public.sales'::regclass
			  AND NOT (c.relrowsecurity AND c.relforcerowsecurity)`); got != 0 {
			t.Errorf("%d partición(es) de sales sin RLS y FORCE RLS, want 0", got)
		}

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_inherits i
			WHERE i.inhparent = 'public.sales'::regclass`); got == 0 {
			t.Fatal("sales no tiene particiones: la prueba del pin en particiones no probó nada")
		}
	})

	t.Run("every tenant policy carries the literal §5.3 predicate and nothing else", func(t *testing.T) {
		// El texto que queda guardado es el que la base evalúa, así que se compara la
		// expresión real —`polqual` y `polwithcheck`— contra el fragmento literal de §5.3. La
		// comparación es sobre la forma canónica: se quitan espacios, los paréntesis que
		// agrega el serializador y el `::text` que PostgreSQL pone alrededor de un literal.
		// Lo que queda es la comparación misma, así que un `NULLIF`, un `missing_ok` o
		// cualquier envoltorio cambian el texto y fallan.
		partitions := queryStrings(t, db, salesPartitionsSQL)
		if len(partitions) == 0 {
			t.Fatal("sales no tiene particiones: la prueba del texto de las políticas no probó nada")
		}

		var relations []string
		relations = append(relations, tenantTables...)
		relations = append(relations, partitions...)

		// La comparación es entre formas canónicas de los DOS lados: lo que hay que afirmar es
		// el texto del predicado, no cómo una versión del servidor elige parentizar.
		predicate := canonicalPolicyExpr(tenantPolicyPredicate)

		want := make([]string, 0, len(relations)*4)
		for _, relation := range relations {
			want = append(want,
				relation+"|"+relation+"_tenant_select|"+predicate+"|-",
				relation+"|"+relation+"_tenant_insert|-|"+predicate,
				relation+"|"+relation+"_tenant_update|"+predicate+"|"+predicate,
				relation+"|"+relation+"_tenant_delete|"+predicate+"|-",
			)
		}
		slices.Sort(want)

		got := make([]string, 0, len(want))
		for _, row := range queryStrings(t, db, tenantPoliciesSQL) {
			got = append(got, canonicalPolicyExpr(row))
		}
		slices.Sort(got)

		if !slices.Equal(got, want) {
			t.Errorf("los predicados de las políticas difieren:\n got: %v\nwant: %v", got, want)
		}

		// Y el control directo: ningún rastro de la forma tolerante.
		if got := queryCount(t, db, policyResidueSQL); got != 0 {
			t.Errorf("%d política(s) con NULLIF, missing_ok o un segundo argumento de current_setting, want 0", got)
		}
	})

	t.Run("the tenant placeholder is defined at the database level with the nil UUID", func(t *testing.T) {
		// El default del marcador es lo que hace que el literal de §5.3 devuelva cero filas en
		// lugar de abortar. Vive en la base, no en los roles: la política de tenant de `users`
		// se evalúa también para `pos_auth` y `pos_maintenance` borra filas de una tabla con
		// política de tenant, así que un default por rol tendría que mantenerse en sincronía
		// en tres roles y el dueño quedaría afuera.
		if got := queryCount(t, db, databasePlaceholderSQL, 0, nilUUID); got != 1 {
			t.Errorf("entradas de default de app.current_tenant_id para toda la base = %d, want 1 (SQLSTATE del testigo: sin este default la consulta de abajo falla con 42704)", got)
		}
		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_db_role_setting s
			JOIN pg_database d ON d.oid = s.setdatabase
			WHERE d.datname = current_database()
			  AND s.setrole <> 0
			  AND s.setconfig @> ARRAY['app.current_tenant_id=' || $1]::text[]`, nilUUID); got != 0 {
			t.Errorf("%d default(s) por rol, want 0: el marcador se define una vez por base", got)
		}
	})

	t.Run("a session that never set the GUC reads zero rows and no error", func(t *testing.T) {
		// El criterio de aceptación de W2-DB-1, medido en la forma que lo rompe: una conexión
		// cuya sesión nunca fijó `app.current_tenant_id`. Sin el default de base esta misma
		// consulta falla con 42704 —y con el parámetro ya creado y vacío, con 22P02— en lugar
		// de devolver cero filas; con el default devuelve cero filas y ningún error.
		freshApp := roleConnection(t, db, config.AppDatabaseURL(), "pos_app")

		if got := queryOneString(t, freshApp, `SELECT current_setting('app.current_tenant_id')`); got != nilUUID {
			t.Errorf("app.current_tenant_id en una sesión nueva = %q, want %q (el marcador que deja la migración)", got, nilUUID)
		}

		for _, table := range tenantTables {
			got, err := countRows(freshApp, table)
			if err != nil {
				t.Errorf("%s falló en lugar de devolver cero filas: %v (el literal de §5.3 aborta con 42704 si el default de base desaparece)", table, err)
				continue
			}
			if got != 0 {
				t.Errorf("%s devolvió %d fila(s) sin contexto de tenant, want 0", table, got)
			}
		}
	})

	t.Run("the pos_auth login read still works with the literal predicate evaluated for it", func(t *testing.T) {
		// `pos_auth` no fija contexto de tenant: corre la política permisiva `USING (true)` de
		// §5.4. La política de tenant de `users` también se evalúa para él, así que con el
		// literal y el default el resultado tiene que ser la lectura completa de `users` —una
		// fila por tenant, que es lo que el login necesita para resolver un teléfono— y
		// ningún error.
		posAuth := roleConnection(t, db, config.AuthDatabaseURL(), "pos_auth")

		var sessionValue string
		if err := posAuth.QueryRowContext(ctx, `SELECT current_setting('app.current_tenant_id')`).Scan(&sessionValue); err != nil {
			t.Fatalf("leer el marcador en la sesión de pos_auth: %v", err)
		}

		if got := queryInt(t, posAuth, `SELECT count(*) FROM users`); got != 2 {
			t.Errorf("pos_auth ve %d fila(s) de users, want 2 (una por tenant: la política permisiva de §5.4 lee por encima del aislamiento, que es lo que el login necesita)", got)
		}
	})

	t.Run("pos_app with no tenant context sees zero rows on each of the eleven tables", func(t *testing.T) {
		// Fail-closed: sin `SET LOCAL app.current_tenant_id`, el predicado compara contra el
		// marcador y no encuentra a nadie. `pos_app` no tiene BYPASSRLS, así que no hay forma
		// de esquivarlo (§5.2).
		for _, table := range tenantTables {
			if got := queryInt(t, posApp, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()); got != 0 {
				t.Errorf("%s devolvió %d fila(s) sin contexto de tenant, want 0", table, got)
			}
		}
	})

	t.Run("pos_app with tenant A's context sees only tenant A's rows", func(t *testing.T) {
		tx, err := posApp.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción de tenant A: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		setTenantContext(t, tx, tenantA.tenantID)

		for _, table := range tenantTables {
			quoted := pgx.Identifier{table}.Sanitize()

			if got := queryInt(t, tx, "SELECT count(*) FROM "+quoted); got != 1 {
				t.Errorf("%s devolvió %d fila(s) con el contexto de A, want 1", table, got)
			}
			// La aserción que importa: la fila del otro tenant no es visible. Se cuenta por
			// `tenant_id` de B y no por total, para que el mensaje diga exactamente qué se
			// filtró.
			if got := queryInt(t, tx, "SELECT count(*) FROM "+quoted+" WHERE tenant_id = $1", tenantB.tenantID); got != 0 {
				t.Errorf("%s devolvió %d fila(s) del tenant B con el contexto de A, want 0", table, got)
			}
		}
	})

	t.Run("the tenant context does not outlive its transaction on the same session", func(t *testing.T) {
		// El helper de transacción de W2-BE-2 fija el GUC con `set_config(..., true)`, que es
		// el `SET LOCAL` parametrizable. Aquí se comprueba la propiedad que hace segura la
		// reutilización de una conexión: la transacción siguiente, en la MISMA sesión, no ve
		// el tenant anterior. Y se comprueba contra qué vuelve el parámetro: al marcador que
		// la migración dejó como default de la base —no a la cadena vacía, que es el estado
		// que obligaría a envolver el predicado de §5.3—, así que el literal sigue sirviendo.
		conn, err := posApp.Conn(ctx)
		if err != nil {
			t.Fatalf("fijar una conexión del pool: %v", err)
		}
		defer func() { _ = conn.Close() }()

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción de tenant A: %v", err)
		}
		setTenantContext(t, tx, tenantA.tenantID)
		if got := queryInt(t, tx, `SELECT count(*) FROM products`); got != 1 {
			t.Errorf("dentro de la transacción de A, products = %d fila(s), want 1", got)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("confirmar la transacción de tenant A: %v", err)
		}

		var leftover string
		if err := conn.QueryRowContext(ctx, `SELECT current_setting('app.current_tenant_id')`).Scan(&leftover); err != nil {
			t.Fatalf("leer el GUC después de la transacción: %v", err)
		}
		if leftover != nilUUID {
			t.Fatalf("después del commit app.current_tenant_id = %q, want el marcador %q: el contexto de tenant de la transacción anterior no debe quedar ni filtrarse", leftover, nilUUID)
		}

		if got := queryInt(t, conn, `SELECT count(*) FROM products`); got != 0 {
			t.Errorf("products = %d fila(s) fuera de la transacción, want 0: el contexto de tenant se filtró a la conexión", got)
		}
	})

	t.Run("an INSERT carrying another tenant's id is rejected by WITH CHECK", func(t *testing.T) {
		tx, err := posApp.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción de tenant A: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		setTenantContext(t, tx, tenantA.tenantID)

		// Control positivo: una escritura legítima del propio tenant pasa. Sin esto, un
		// rechazo por falta de concesión se leería como si el predicado funcionara.
		execOn(t, tx, `
			INSERT INTO products (id, tenant_id, name, price_cents, uom, created_at, updated_at, received_at)
			VALUES ($1, $2, 'escritura legítima', 500, 'unit', now(), now(), now())`,
			"019a0000-0000-7000-8000-0000000000ab", tenantA.tenantID)

		// El rechazo: la fila lleva el `tenant_id` de B bajo el contexto de A.
		_, err = tx.ExecContext(ctx, `
			INSERT INTO products (id, tenant_id, name, price_cents, uom, created_at, updated_at, received_at)
			VALUES ($1, $2, 'escritura cross-tenant', 500, 'unit', now(), now(), now())`,
			"019a0000-0000-7000-8000-0000000000ac", tenantB.tenantID)
		expectSQLState(t, err, sqlStateInsufficientPrivilege, "INSERT de un producto con el tenant_id de B bajo el contexto de A")
	})

	t.Run("fail-then-pass: with RLS off the same query leaks the other tenant", func(t *testing.T) {
		// §5.7: el contrato de verificación es que el test FALLA por diseño antes del RLS.
		// Se demuestra, no se afirma: con el RLS apagado en `products`, la misma consulta
		// cross-tenant como pos_app devuelve la fila del otro tenant.
		//
		// La tabla se vuelve a proteger en la misma prueba y otra vez en la limpieza: ninguna
		// corrida deja una tabla sin RLS, ni siquiera si una aserción falla en el medio.
		restoreRLS := func() {
			exec(t, db, `ALTER TABLE products ENABLE ROW LEVEL SECURITY`)
			exec(t, db, `ALTER TABLE products FORCE ROW LEVEL SECURITY`)
		}
		t.Cleanup(restoreRLS)

		exec(t, db, `ALTER TABLE products DISABLE ROW LEVEL SECURITY`)

		if got := queryInt(t, posApp, `SELECT count(*) FROM products WHERE tenant_id = $1`, tenantB.tenantID); got != 1 {
			t.Errorf("antes: products con el tenant_id de B = %d fila(s) sin contexto de tenant, want 1 (la fuga que el contrato exige ver)", got)
		}
		if got := queryInt(t, posApp, `SELECT count(*) FROM products`); got != 2 {
			t.Errorf("antes: products = %d fila(s) sin contexto de tenant, want 2 (las de los dos tenants)", got)
		}

		restoreRLS()

		if got := queryCount(t, db, `
			SELECT count(*)
			FROM pg_class
			WHERE relname = 'products' AND relrowsecurity AND relforcerowsecurity`); got != 1 {
			t.Fatalf("products quedó sin el pin de RLS después del paso «antes»: la tabla quedó desprotegida")
		}

		if got := queryInt(t, posApp, `SELECT count(*) FROM products WHERE tenant_id = $1`, tenantB.tenantID); got != 0 {
			t.Errorf("después: products con el tenant_id de B = %d fila(s) sin contexto de tenant, want 0", got)
		}

		tx, err := posApp.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción de tenant A: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		setTenantContext(t, tx, tenantA.tenantID)
		if got := queryInt(t, tx, `SELECT count(*) FROM products`); got != 1 {
			t.Errorf("después: products = %d fila(s) con el contexto de A, want 1", got)
		}
	})

	t.Run("a direct query on a sales partition is tenant-filtered too", func(t *testing.T) {
		// Las particiones de `sales` no heredan ni las políticas ni las concesiones de su
		// tabla padre; la migración las recorre una por una. Esta prueba es la que mide el
		// efecto sobre una consulta que llega a la partición directamente, no a través del
		// padre.
		partitions := queryStrings(t, db, salesPartitionsSQL)
		if len(partitions) == 0 {
			t.Fatal("sales no tiene particiones: la prueba de acceso directo no probó nada")
		}

		tx, err := posApp.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción de tenant A: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		setTenantContext(t, tx, tenantA.tenantID)

		visibleToA := 0
		for _, partition := range partitions {
			quoted := pgx.Identifier{partition}.Sanitize()

			if got := queryInt(t, tx, "SELECT count(*) FROM "+quoted+" WHERE tenant_id = $1", tenantB.tenantID); got != 0 {
				t.Errorf("la partición %s devolvió %d fila(s) del tenant B, want 0", partition, got)
			}
			visibleToA += queryInt(t, tx, "SELECT count(*) FROM "+quoted+" WHERE tenant_id = $1", tenantA.tenantID)
		}

		if visibleToA != 1 {
			t.Errorf("las particiones visibles para A suman %d fila(s), want 1", visibleToA)
		}
	})
}

// canonicalPolicyExpr reduce el texto de una política —tal como lo serializa el servidor— a su
// forma canónica: sin espacios, sin paréntesis y sin el `::text` que PostgreSQL pone alrededor
// de un literal. Los paréntesis se quitan de los DOS lados de la comparación porque el
// serializador agrega los suyos: lo que la prueba afirma es el predicado —la comparación de
// `tenant_id` contra el GUC—, no cómo una versión del servidor elige parentizarlo. La
// comparación sigue siendo estricta para lo que importa: un `NULLIF`, un `missing_ok` o un
// segundo argumento de `current_setting` cambian el texto y fallan.
func canonicalPolicyExpr(expr string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "(", "", ")", "", "::text", "").Replace(expr)
}

// countRows cuenta las filas de una tabla y devuelve el error en lugar de fallar la prueba, que
// es lo que hace falta para distinguir «devolvió un error» de «devolvió filas»: el criterio de
// aceptación de W2-DB-1 exige cero filas SIN error.
func countRows(q querier, table string) (int, error) {
	var got int
	err := q.QueryRowContext(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&got)
	return got, err
}
