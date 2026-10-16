// Test de contrato del helper de transacción de tenant (W2-BE-2).
//
// La propiedad que se prueba no es «fija una variable»: es que el contexto de tenant vive
// exactamente lo que dura su transacción, de modo que reutilizar una conexión —lo que hace un
// pooler en modo transacción— no puede filtrar el tenant de un cliente al siguiente
// (docs/DATABASE.md §5.2; docs/BACKEND.md §7.7).
//
// Reutiliza el arnés de schema_contract_test.go —la base desechable, `exec`, `queryCount`,
// `queryOneString`— y las conexiones por rol de grants_contract_test.go (`roleConnection`,
// `setTenantContext`, `queryInt`, `execOn`).
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// tenantContextSettingSQL lee el contexto de tenant de la sesión o de la transacción actual.
const tenantContextSettingSQL = `SELECT current_setting('app.current_tenant_id')`

// TestTenantPoolExposesOnlyTheTenantHelper fija la regla de «una sola ruta de datos» en el
// tipo, y no en una convención que se puede erosionar sin que nadie lo note.
//
// La aserción es de Reflexión y no es una tautología: afirma el conjunto EXACTO de métodos
// exportados. Agregar `Acquire`, `Query`, `Begin`, `SendBatch`, `Ping` o cualquier otra forma
// de consultar sin contexto rompe la prueba, que es justamente lo que se quiere que pase.
func TestTenantPoolExposesOnlyTheTenantHelper(t *testing.T) {
	poolType := reflect.TypeOf(&postgres.TenantPool{})

	got := make([]string, 0, poolType.NumMethod())
	for index := 0; index < poolType.NumMethod(); index++ {
		got = append(got, poolType.Method(index).Name)
	}
	slices.Sort(got)

	want := []string{"WithTenant"}
	if !slices.Equal(got, want) {
		t.Errorf("los métodos exportados de *TenantPool = %v, want %v: el pool envuelto no puede ofrecer ninguna forma de consultar fuera del helper de tenant", got, want)
	}

	// El contrato que recibe el llamador TAMPOCO puede cerrar la transacción: confirmar o
	// abortar por dentro sería salirse del helper, que es el único que decide el final.
	txType := reflect.TypeOf((*postgres.TenantTx)(nil)).Elem()

	txMethods := make([]string, 0, txType.NumMethod())
	for index := 0; index < txType.NumMethod(); index++ {
		txMethods = append(txMethods, txType.Method(index).Name)
	}
	slices.Sort(txMethods)

	wantTxMethods := []string{"Exec", "Query", "QueryRow"}
	if !slices.Equal(txMethods, wantTxMethods) {
		t.Errorf("los métodos de TenantTx = %v, want %v", txMethods, wantTxMethods)
	}
	for _, forbidden := range []string{"Commit", "Rollback", "Begin", "Conn", "Acquire", "SendBatch", "CopyFrom", "Prepare"} {
		if _, present := txType.MethodByName(forbidden); present {
			t.Errorf("TenantTx expone %s: el final de la transacción lo decide el helper, no el llamador", forbidden)
		}
	}
}

// TestWithTenantRejectsAnUnusableContext cubre los tres rechazos que evitan una transacción de
// tenant que no lo sea. El más importante es el UUID nulo: la base tiene un default de marcador
// igual a ese valor, así que una transacción «con» tenant nulo se comportaría como una sin
// contexto —cero filas— y se leería como un tenant vacío en lugar de como un error.
func TestWithTenantRejectsAnUnusableContext(t *testing.T) {
	called := false
	callback := func(context.Context, postgres.TenantTx) error {
		called = true
		return nil
	}

	cases := []struct {
		name string
		pool *postgres.TenantPool
		id   uuid.UUID
		fn   func(context.Context, postgres.TenantTx) error
	}{
		{name: "the nil tenant id", pool: postgres.NewTenantPool(nil), id: uuid.Nil, fn: callback},
		{name: "no callback", pool: postgres.NewTenantPool(nil), id: uuid.MustParse("019a0000-0000-7000-8000-0000000000a1"), fn: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			if err := tc.pool.WithTenant(context.Background(), tc.id, tc.fn); err == nil {
				t.Fatal("WithTenant = nil, want un error")
			}
			if called {
				t.Error("el callback corrió: no puede ejecutarse sin contexto de tenant")
			}
		})
	}

	t.Run("a tenant pool without a pool", func(t *testing.T) {
		// Un envoltorio sin pool es un defecto de construcción: tiene que devolver un error y
		// no entrar en pánico al desreferenciar.
		if err := postgres.NewTenantPool(nil).WithTenant(context.Background(), uuid.MustParse("019a0000-0000-7000-8000-0000000000a1"), callback); err == nil {
			t.Fatal("WithTenant sobre un pool nulo = nil, want un error")
		}
	})
}

// seedCatalog inserta un tenant, su usuario y un producto como DUEÑO, con el contexto de tenant
// fijado en la misma transacción: `users` y `products` están bajo FORCE RLS, así que la
// escritura pasa por el mismo camino que la aplicación (docs/DATABASE.md §5.2).
func seedCatalog(t *testing.T, db *sql.DB, tenantID, userID, phone, productID, productName string) {
	t.Helper()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra del tenant %s: %v", tenantID, err)
	}

	execOn(t, tx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, now())`, tenantID, "tenant de "+tenantID)
	setTenantContext(t, tx, tenantID)
	execOn(t, tx, `
		INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, $3, $4, now())`, userID, tenantID, phone, "no-es-un-hash-real")
	execOn(t, tx, `
		INSERT INTO products (id, tenant_id, name, gtin, price_cents, uom, created_at, updated_at, received_at)
		VALUES ($1, $2, $3, NULL, 1000, 'unit', now(), now(), now())`, productID, tenantID, productName)

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra del tenant %s: %v", tenantID, err)
	}
}

// tenantProductNames lee el catálogo por el contrato que el helper entrega.
func tenantProductNames(t *testing.T, ctx context.Context, tx postgres.TenantTx) []string {
	t.Helper()

	rows, err := tx.Query(ctx, `SELECT name FROM products ORDER BY name`)
	if err != nil {
		t.Fatalf("leer products dentro de la transacción de tenant: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("leer el nombre de un producto: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer products: %v", err)
	}
	return names
}

// tenantContextOf lee el GUC tal como lo ve la transacción en curso.
func tenantContextOf(t *testing.T, ctx context.Context, tx postgres.TenantTx) string {
	t.Helper()

	var value string
	if err := tx.QueryRow(ctx, tenantContextSettingSQL).Scan(&value); err != nil {
		t.Fatalf("leer el contexto de tenant de la transacción: %v", err)
	}
	return value
}

// poolRowCount cuenta filas sobre el pool crudo de pgx. `queryInt` del arnés habla el
// contrato de database/sql, y el pool del helper no: por eso hay un ayudante aparte en lugar
// de un envoltorio que no probaría lo mismo.
func poolRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()

	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("consultar %q: %v", query, err)
	}
	return got
}

// tenantFixtureIDs es el par de tenants de estas pruebas, con un producto cada uno.
type tenantFixtureIDs struct {
	a, b string
}

// newTenantTestPool abre un pool del rol `pos_app` contra la base desechable, con el tamaño de
// pool que la prueba necesite.
func newTenantTestPool(t *testing.T, db *sql.DB, maxConns int32) (*pgxpool.Pool, *postgres.TenantPool) {
	t.Helper()

	name := queryOneString(t, db, "SELECT current_database()")
	dsn, err := postgres.WithDatabase(config.AppDatabaseURL(), name)
	if err != nil {
		t.Fatalf("derivar el DSN de pos_app para %s: %v", name, err)
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parsear el DSN de pos_app: %v", err)
	}
	// Un pool de UNA conexión es la forma más fuerte de modelar un pooler en modo transacción
	// con un solo backend: la conexión física es la misma por construcción, así que cualquier
	// contexto que sobreviva a su transacción se vería en la transacción siguiente.
	poolConfig.MaxConns = maxConns

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("construir el pool de pos_app: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("conectar como pos_app a %s: %v", name, err)
	}

	return pool, postgres.NewTenantPool(pool)
}

// TestWithTenantScopesEveryReadToItsTenant cubre el camino completo del helper: la primera
// sentencia de la transacción es el contexto, y con él las políticas de RLS devuelven las
// filas de ese tenant y ninguna otra.
func TestWithTenantScopesEveryReadToItsTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	fixtures := tenantFixtureIDs{
		a: "019a0000-0000-7000-8000-0000000000a1",
		b: "019a0000-0000-7000-8000-0000000000b1",
	}
	seedCatalog(t, db, fixtures.a, "019a0000-0000-7000-8000-0000000000a2", "+51900000101",
		"019a0000-0000-7000-8000-0000000000a3", "producto de A")
	seedCatalog(t, db, fixtures.b, "019a0000-0000-7000-8000-0000000000b2", "+51900000102",
		"019a0000-0000-7000-8000-0000000000b3", "producto de B")

	_, tenantPool := newTenantTestPool(t, db, 4)
	ctx := context.Background()

	// El control que hace falsable la prueba: sin contexto no hay filas. Si el helper no fijara
	// nada, las dos lecturas de abajo devolverían el mismo resultado y la prueba no probaría
	// nada.
	posApp := roleConnection(t, db, config.AppDatabaseURL(), "pos_app")
	if got := queryInt(t, posApp, `SELECT count(*) FROM products`); got != 0 {
		t.Fatalf("products sin contexto de tenant = %d fila(s), want 0", got)
	}

	for _, tc := range []struct {
		name          string
		tenantID      string
		otherTenantID string
		want          string
		other         string
	}{
		{name: "tenant A", tenantID: fixtures.a, otherTenantID: fixtures.b, want: "producto de A", other: "producto de B"},
		{name: "tenant B", tenantID: fixtures.b, otherTenantID: fixtures.a, want: "producto de B", other: "producto de A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tenantPool.WithTenant(ctx, uuid.MustParse(tc.tenantID), func(ctx context.Context, tx postgres.TenantTx) error {
				if got := tenantContextOf(t, ctx, tx); got != tc.tenantID {
					t.Errorf("app.current_tenant_id dentro de la transacción = %q, want %q", got, tc.tenantID)
				}

				names := tenantProductNames(t, ctx, tx)
				if !slices.Equal(names, []string{tc.want}) {
					t.Errorf("products visibles = %v, want [%q]", names, tc.want)
				}
				for _, name := range names {
					if name == tc.other {
						t.Errorf("products visibles incluye %q, que es de otro tenant", name)
					}
				}

				// Y la comprobación explícita por tenant_id, para que el mensaje diga qué se
				// filtró si alguna vez se filtra algo.
				var otherRows int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id = $1`, tc.otherTenantID).Scan(&otherRows); err != nil {
					t.Fatalf("contar los productos del otro tenant: %v", err)
				}
				if otherRows != 0 {
					t.Errorf("productos del otro tenant visibles = %d, want 0", otherRows)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
		})
	}
}

// TestWithTenantIsPoolerSafeAcrossConsecutiveTransactions es la propiedad del §5.2 que hace
// compatible el helper con un pooler en modo transacción: dos transacciones consecutivas sobre
// la MISMA conexión física no comparten el contexto de tenant, y después de las dos la conexión
// vuelve al marcador de la base, así que una consulta fuera de todo contexto ve cero filas.
func TestWithTenantIsPoolerSafeAcrossConsecutiveTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	fixtures := tenantFixtureIDs{
		a: "019a0000-0000-7000-8000-0000000000a1",
		b: "019a0000-0000-7000-8000-0000000000b1",
	}
	seedCatalog(t, db, fixtures.a, "019a0000-0000-7000-8000-0000000000a2", "+51900000101",
		"019a0000-0000-7000-8000-0000000000a3", "producto de A")
	seedCatalog(t, db, fixtures.b, "019a0000-0000-7000-8000-0000000000b2", "+51900000102",
		"019a0000-0000-7000-8000-0000000000b3", "producto de B")

	// Una sola conexión: todo lo que sigue corre en la misma conexión física, que es el caso
	// que el pooler produce y el que la prueba tiene que medir.
	posApp, tenantPool := newTenantTestPool(t, db, 1)
	ctx := context.Background()

	readTenant := func(t *testing.T, tenantID, want, forbidden string) int32 {
		t.Helper()

		var backendID int32
		err := tenantPool.WithTenant(ctx, uuid.MustParse(tenantID), func(ctx context.Context, tx postgres.TenantTx) error {
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendID); err != nil {
				return err
			}

			names := tenantProductNames(t, ctx, tx)
			if !slices.Equal(names, []string{want}) {
				t.Errorf("products visibles con el contexto de %s = %v, want [%q]", tenantID, names, want)
			}
			for _, name := range names {
				if name == forbidden {
					t.Errorf("el contexto de %s dejó ver %q, que es de otro tenant", tenantID, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("WithTenant(%s): %v", tenantID, err)
		}
		return backendID
	}

	backendA := readTenant(t, fixtures.a, "producto de A", "producto de B")
	backendB := readTenant(t, fixtures.b, "producto de B", "producto de A")

	// La prueba de que las dos transacciones usaron la MISMA conexión: sin esto, «no filtra
	// entre clientes que reutilizan la conexión» no estaría medido.
	if backendA != backendB {
		t.Fatalf("las dos transacciones corrieron en conexiones distintas (backend %d y %d): la prueba del pooler no probó nada", backendA, backendB)
	}

	// Y lo que hace segura la reutilización: al terminar la transacción el parámetro vuelve al
	// marcador que la migración dejó como default de la base, no a la cadena vacía.
	var leftover string
	if err := posApp.QueryRow(ctx, tenantContextSettingSQL).Scan(&leftover); err != nil {
		t.Fatalf("leer el contexto de tenant fuera de la transacción: %v", err)
	}
	if leftover != nilUUID {
		t.Errorf("app.current_tenant_id fuera de la transacción = %q, want el marcador %q: el contexto se filtró a la conexión", leftover, nilUUID)
	}

	// La consecuencia observable: una consulta que corre fuera del helper ve cero filas. Es la
	// dirección fail-closed —no un canal de error—: un `SELECT` extraviado no fuga, devuelve
	// vacío.
	if got := poolRowCount(t, ctx, posApp, `SELECT count(*) FROM products`); got != 0 {
		t.Errorf("products fuera de toda transacción de tenant = %d fila(s), want 0", got)
	}

	// El control posterior: el mismo pool sigue sirviendo transacciones de tenant bien
	// formadas, así que lo de arriba no es un pool roto.
	_ = readTenant(t, fixtures.a, "producto de A", "producto de B")
}

// TestWithTenantRollsBackEverythingItDid cubre el final de la transacción: un error del
// callback vuelve tal cual —el llamador tiene que poder reconocerlo con errors.Is— y no deja
// ninguna escritura; un panic también revierte; y un callback sin error confirma, que es el
// control que hace falsables a los otros dos.
func TestWithTenantRollsBackEverythingItDid(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	fixtures := tenantFixtureIDs{
		a: "019a0000-0000-7000-8000-0000000000a1",
		b: "019a0000-0000-7000-8000-0000000000b1",
	}
	seedCatalog(t, db, fixtures.a, "019a0000-0000-7000-8000-0000000000a2", "+51900000101",
		"019a0000-0000-7000-8000-0000000000a3", "producto de A")

	_, tenantPool := newTenantTestPool(t, db, 4)
	ctx := context.Background()

	insertProduct := func(ctx context.Context, tx postgres.TenantTx, id, name string) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO products (id, tenant_id, name, gtin, price_cents, uom, created_at, updated_at, received_at)
			VALUES ($1, $2, $3, NULL, 500, 'unit', now(), now(), now())`, id, fixtures.a, name)
		return err
	}

	countVisible := func(t *testing.T) []string {
		t.Helper()

		var names []string
		if err := tenantPool.WithTenant(ctx, uuid.MustParse(fixtures.a), func(ctx context.Context, tx postgres.TenantTx) error {
			names = tenantProductNames(t, ctx, tx)
			return nil
		}); err != nil {
			t.Fatalf("leer el catálogo de A: %v", err)
		}
		return names
	}

	t.Run("a callback error is returned as it was and nothing is written", func(t *testing.T) {
		sentinel := errors.New("the caller decided to stop")

		err := tenantPool.WithTenant(ctx, uuid.MustParse(fixtures.a), func(ctx context.Context, tx postgres.TenantTx) error {
			if err := insertProduct(ctx, tx, "019a0000-0000-7000-8000-0000000000a4", "producto revertido"); err != nil {
				return err
			}
			return sentinel
		})

		if !errors.Is(err, sentinel) {
			t.Fatalf("WithTenant = %v, want el error del callback (%v) para que el llamador lo reconozca", err, sentinel)
		}
		if got := countVisible(t); !slices.Equal(got, []string{"producto de A"}) {
			t.Errorf("products después del error = %v, want solo el sembrado: la transacción no revirtió", got)
		}
	})

	t.Run("a panic still rolls back", func(t *testing.T) {
		const panicValue = "falla deliberada en el callback"

		func() {
			defer func() {
				if recovered := recover(); recovered != panicValue {
					t.Errorf("el panic recuperado = %#v, want %q: un panic del callback tiene que propagarse, no tragarse", recovered, panicValue)
				}
			}()

			_ = tenantPool.WithTenant(ctx, uuid.MustParse(fixtures.a), func(ctx context.Context, tx postgres.TenantTx) error {
				if err := insertProduct(ctx, tx, "019a0000-0000-7000-8000-0000000000a5", "producto del panic"); err != nil {
					return err
				}
				panic(panicValue)
			})
		}()

		if got := countVisible(t); !slices.Equal(got, []string{"producto de A"}) {
			t.Errorf("products después del panic = %v, want solo el sembrado: el panic dejó una escritura confirmada", got)
		}
	})

	t.Run("a callback without an error commits", func(t *testing.T) {
		// El control: sin este caso, «no se escribió nada» también sería cierto si el INSERT
		// nunca hubiera funcionado.
		err := tenantPool.WithTenant(ctx, uuid.MustParse(fixtures.a), func(_ context.Context, tx postgres.TenantTx) error {
			return insertProduct(context.Background(), tx, "019a0000-0000-7000-8000-0000000000a6", "producto confirmado")
		})
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}

		got := countVisible(t)
		if !slices.Contains(got, "producto confirmado") {
			t.Errorf("products después del commit = %v, want que incluya el insertado", got)
		}
		if len(got) != 2 {
			t.Errorf("products después del commit = %v, want exactamente el sembrado más el insertado", got)
		}
	})

	// Y la escritura del otro tenant no aparece en el catálogo de A: la única fila que se ve es
	// la de A, con o sin las escrituras de arriba.
	t.Run("nothing crossed into another tenant", func(t *testing.T) {
		tx, err := roleConnection(t, db, config.AppDatabaseURL(), "pos_app").BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("abrir la transacción de B: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		setTenantContext(t, tx, fixtures.b)

		if got := queryInt(t, tx, `SELECT count(*) FROM products`); got != 0 {
			t.Errorf("products visibles para el tenant B = %d, want 0: el tenant B no tiene catálogo", got)
		}
	})
}

// queryTextOn es `queryOneString` para una transacción o una conexión fijada.
func queryTextOn(t *testing.T, q querier, query string, args ...any) string {
	t.Helper()

	var got string
	if err := q.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("consultar %s: %v", oneline(query), err)
	}
	return got
}

// TestWithTenantUsesTheTransactionScopedSetting es la prueba de la equivalencia de la primera
// sentencia: el GUC con el que trabaja la transacción es el que se fijó con
// `set_config(..., true)`, y su forma parametrizable es la de `SET LOCAL` (decisión D4).
func TestWithTenantUsesTheTransactionScopedSetting(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	fixtures := tenantFixtureIDs{a: "019a0000-0000-7000-8000-0000000000a1"}
	seedCatalog(t, db, fixtures.a, "019a0000-0000-7000-8000-0000000000a2", "+51900000101",
		"019a0000-0000-7000-8000-0000000000a3", "producto de A")

	posApp, tenantPool := newTenantTestPool(t, db, 2)
	ctx := context.Background()

	// El valor que un `SET LOCAL` dejaría y el que deja la primera sentencia del helper son el
	// mismo: se compara contra la forma textual de la variante no parametrizable, aplicada a
	// mano.
	manual := roleConnection(t, db, config.AppDatabaseURL(), "pos_app")
	tx, err := manual.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("abrir la transacción manual: %v", err)
	}
	setTenantContext(t, tx, fixtures.a)
	want := queryTextOn(t, tx, tenantContextSettingSQL)
	if err := tx.Rollback(); err != nil {
		t.Fatalf("abortar la transacción manual: %v", err)
	}

	var got string
	if err := tenantPool.WithTenant(ctx, uuid.MustParse(fixtures.a), func(ctx context.Context, tx postgres.TenantTx) error {
		got = tenantContextOf(t, ctx, tx)
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	if got != want {
		t.Errorf("el contexto del helper = %q, want %q (la misma forma que SET LOCAL)", got, want)
	}

	// El contexto del helper tampoco sobrevive a su transacción en el pool que la sirvió.
	var after string
	if err := posApp.QueryRow(ctx, tenantContextSettingSQL).Scan(&after); err != nil {
		t.Fatalf("leer el GUC después de la transacción: %v", err)
	}
	if after != nilUUID {
		t.Errorf("app.current_tenant_id después de la transacción = %q, want %q", after, nilUUID)
	}
	if strings.Contains(after, fixtures.a) {
		t.Errorf("el contexto de la transacción quedó en la conexión: %q", after)
	}
}

// interfaceCompliance deja constancia en tiempo de compilación de que el tipo de la
// transacción que entrega el helper es el que el driver produce, sin envoltorios que agreguen
// métodos.
var _ postgres.TenantTx = (pgx.Tx)(nil)
