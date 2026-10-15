// Test de contrato de esquema: la prueba de integración que demuestra que la base
// migrada ES el contrato de 14 tablas de docs/DATABASE.md §4, y no apenas un conjunto de
// CREATE TABLE que por casualidad se ejecutaron.
//
// Nunca toca la base `pos` del desarrollador. Se conecta a la base de mantenimiento, crea
// una base desechable con un sufijo aleatorio, migra esa, verifica contra ella y la
// elimina en t.Cleanup. La cadena de conexión sale de DATABASE_URL —la misma variable y el
// mismo valor de respaldo de docker-compose que usan el runner de migraciones y la API
// (internal/platform/config).
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" de database/sql, como en cmd/migrate

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// throwawayDatabasePrefix marca toda base que esta prueba crea. Una corrida que deje una
// atrás se ve con:
//
//	SELECT count(*) FROM pg_database WHERE datname LIKE 'pos_test%'
const throwawayDatabasePrefix = "pos_test_"

// maintenanceDatabase es la base que todo servidor PostgreSQL tiene y desde la que se
// puede ejecutar CREATE DATABASE y DROP DATABASE.
const maintenanceDatabase = "postgres"

// expectedBusinessTables es el conjunto vinculante de 14 tablas del contrato, en orden de
// bytes de Go. Los dos lados de la comparación se ordenan en Go para que la aserción no
// dependa del collation del servidor.
var expectedBusinessTables = []string{
	"cash_close_counts",
	"cash_closes",
	"devices",
	"fiado_accounts",
	"fiado_movements",
	"global_barcodes",
	"global_products",
	"idempotency_keys",
	"products",
	"sale_lines",
	"sales",
	"stock_adjustments",
	"tenants",
	"users",
}

// expectedCentsColumns es toda columna de dinero del contrato: centavos en BIGINT, nunca
// NUMERIC y nunca un tipo de punto flotante (docs/DATABASE.md §2.3).
var expectedCentsColumns = []string{
	"cash_close_counts.counted_cents",
	"cash_close_counts.expected_cents",
	"fiado_movements.amount_cents",
	"products.price_cents",
	"sale_lines.line_total_cents",
	"sale_lines.price_cents",
	"sales.total_cents",
}

// expectedJSONBColumns es la superficie JSONB completa: exactamente dos columnas
// (docs/DATABASE.md §2.9).
var expectedJSONBColumns = []string{
	"global_products.attributes",
	"products.attributes",
}

// quantityColumns son las únicas columnas que pueden tener un tipo exacto no entero: las
// cantidades NUMERIC(10,3) (docs/DATABASE.md §2.4). Fijar el conjunto exacto —y no solo el
// sufijo `*_cents`— es lo que vuelve falsable la regla de que ninguna columna de dinero es
// NUMERIC ni punto flotante.
var quantityColumns = []string{
	"products.stock",
	"sale_lines.qty",
	"stock_adjustments.qty_delta",
}

// businessTablesSQL lista las tablas del contrato excluyendo dos cosas a propósito:
//
//   - Las particiones hijas. `sales` es PARTITION BY RANGE, así que tiene relkind 'p';
//     cada partición mensual y `sales_default` tienen relkind 'r' Y relispartition en
//     true. Sin `NOT c.relispartition` esas ocho relaciones contarían como tablas y la
//     respuesta sería 22, no 14. La tabla particionada padre no es partición, así que
//     sobrevive al filtro exactamente una vez.
//   - `goose_db_version`, la tabla de contabilidad del runner de migraciones. No es parte
//     del contrato de datos: registra qué migraciones corrieron, no datos de negocio.
const businessTablesSQL = `
SELECT c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND c.relname <> 'goose_db_version'
ORDER BY c.relname`

// salesPartitionKeySQL lee la estrategia de particionado directo del catálogo.
const salesPartitionKeySQL = `SELECT pg_get_partkeydef('public.sales'::regclass)`

// salesPrimaryKeySQL lee las columnas de la clave primaria en su orden. `indkey` es un
// arreglo y ANY(i.indkey) perdería el orden, así que se desanida WITH ORDINALITY.
const salesPrimaryKeySQL = `
SELECT a.attname
FROM pg_index i
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE i.indrelid = 'public.sales'::regclass
  AND i.indisprimary
ORDER BY k.ord`

// salesPartitionsSQL lista toda partición enganchada a la tabla particionada padre.
const salesPartitionsSQL = `
SELECT c.relname
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = 'public.sales'::regclass
ORDER BY c.relname`

// businessColumnsCTE expone solo las columnas de las tablas del contrato: descarta las
// particiones hijas —que si no duplicarían las columnas del padre— y la tabla de
// contabilidad del runner de migraciones.
const businessColumnsCTE = `
WITH business_columns AS (
    SELECT col.table_name, col.column_name, col.data_type
    FROM information_schema.columns col
    JOIN pg_class c
      ON c.relname = col.table_name
     AND c.relnamespace = to_regnamespace(col.table_schema)
    WHERE col.table_schema = 'public'
      AND NOT c.relispartition
      AND col.table_name <> 'goose_db_version'
)`

// nonIntegerColumnsSQL lista las columnas exactas no enteras: solo pueden ser cantidades.
const nonIntegerColumnsSQL = businessColumnsCTE + `
SELECT table_name || '.' || column_name AS column_identity, data_type
FROM business_columns
WHERE data_type IN ('numeric', 'double precision', 'real')
ORDER BY column_identity`

// jsonbColumnsSQL lista las columnas JSONB.
const jsonbColumnsSQL = businessColumnsCTE + `
SELECT table_name || '.' || column_name AS column_identity, data_type
FROM business_columns
WHERE data_type = 'jsonb'
ORDER BY column_identity`

// La barra invertida escapa el comodín de LIKE, así que `%_cents` coincide con un guion
// bajo literal: el sufijo de dinero `*_cents` de docs/DATABASE.md §2.1.
const centsColumnsSQL = businessColumnsCTE + `
SELECT table_name || '.' || column_name AS column_identity, data_type
FROM business_columns
WHERE column_name LIKE '%\_cents'
ORDER BY column_identity`

// supersededTablesSQL cuenta las dos relaciones que los borradores anteriores pedían como
// tablas y que el contrato no tiene.
const supersededTablesSQL = `
SELECT count(*)
FROM information_schema.tables
WHERE table_schema = 'public'
  AND table_name IN ('tenders', 'sync_events')`

// storedInPartitionSQL lee la partición física que guardó la fila: `tableoid` es la
// columna de sistema que la nombra, y eso es lo que demuestra el ruteo de particiones.
const storedInPartitionSQL = `
SELECT c.relname
FROM sales s
JOIN pg_class c ON c.oid = s.tableoid
WHERE s.sale_id = $1`

func TestSchemaContract(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	ctx := context.Background()

	t.Run("exactly the 14 contract tables exist", func(t *testing.T) {
		got := queryStrings(t, db, businessTablesSQL)

		if len(got) != len(expectedBusinessTables) {
			t.Fatalf("cantidad de tablas de negocio = %d (%v), want %d", len(got), got, len(expectedBusinessTables))
		}
		slices.Sort(got)
		if !slices.Equal(got, expectedBusinessTables) {
			t.Errorf("las tablas de negocio difieren:\n got: %v\nwant: %v", got, expectedBusinessTables)
		}
	})

	t.Run("tenders and sync_events do not exist as tables", func(t *testing.T) {
		// `tenders` es la columna sales.payment_method; el evento de sincronización vive
		// en idempotency_keys. Ninguna de las dos es tabla (docs/DATABASE.md §8
		// enmiendas 1 y 5).
		if got := queryCount(t, db, supersededTablesSQL); got != 0 {
			t.Errorf("se encontraron %d relación(es) llamadas tenders o sync_events, want 0", got)
		}
	})

	t.Run("sales is range partitioned on sold_on", func(t *testing.T) {
		if got, want := queryOneString(t, db, salesPartitionKeySQL), "RANGE (sold_on)"; got != want {
			t.Errorf("pg_get_partkeydef(sales) = %q, want %q", got, want)
		}
	})

	t.Run("the sales primary key is (sold_on, sale_id)", func(t *testing.T) {
		got := queryStrings(t, db, salesPrimaryKeySQL)
		want := []string{"sold_on", "sale_id"}

		if !slices.Equal(got, want) {
			t.Errorf("clave primaria de sales = %v, want %v (la clave de partición DEBE ser parte de ella)", got, want)
		}
	})

	t.Run("the current month partition and sales_default exist", func(t *testing.T) {
		partitions := queryStrings(t, db, salesPartitionsSQL)

		if !slices.Contains(partitions, "sales_default") {
			t.Errorf("falta sales_default; particiones = %v", partitions)
		}

		currentMonth := "sales_" + time.Now().UTC().Format("2006_01")
		if !slices.Contains(partitions, currentMonth) {
			t.Errorf("falta la partición del mes actual %s; particiones = %v (la ventana rodante de precreación necesita una migración nueva)", currentMonth, partitions)
		}
	})

	t.Run("a sold_on outside the created range lands in sales_default", func(t *testing.T) {
		// Criterio de aceptación de W1-DB-2: la red de seguridad es real y la fila sigue
		// siendo consultable a través de la tabla particionada padre.
		const (
			tenantID = "019a0000-0000-7000-8000-000000000001"
			userID   = "019a0000-0000-7000-8000-000000000002"
			saleID   = "019a0000-0000-7000-8000-000000000003"
		)

		exec(t, db, `INSERT INTO tenants (id, name, created_at) VALUES ($1, 'contract fixture', now())`, tenantID)
		exec(t, db, `INSERT INTO users (id, tenant_id, phone, password_hash, created_at) VALUES ($1, $2, '+51900000000', 'not-a-real-hash', now())`, userID, tenantID)
		exec(t, db, `
			INSERT INTO sales (sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method, total_cents, created_at, received_at)
			VALUES ('2099-12-31', $1, $2, $3, 'sale', 'cash', 100, now(), now())`, saleID, tenantID, userID)

		var landed string
		if err := db.QueryRowContext(ctx, storedInPartitionSQL, saleID).Scan(&landed); err != nil {
			t.Fatalf("leer la venta de prueba a través de la tabla particionada padre: %v", err)
		}
		if landed != "sales_default" {
			t.Errorf("sold_on = 2099-12-31 aterrizó en %q, want %q", landed, "sales_default")
		}
	})

	t.Run("every *_cents column is bigint", func(t *testing.T) {
		facts := queryColumnFacts(t, db, centsColumnsSQL)

		got := make([]string, 0, len(facts))
		for _, fact := range facts {
			if fact.DataType != "bigint" {
				t.Errorf("%s es %s, want bigint (el dinero es BIGINT en centavos, docs/DATABASE.md §2.3)", fact.Column, fact.DataType)
			}
			got = append(got, fact.Column)
		}

		slices.Sort(got)
		if !slices.Equal(got, expectedCentsColumns) {
			t.Errorf("las columnas de dinero difieren:\n got: %v\nwant: %v", got, expectedCentsColumns)
		}
	})

	t.Run("no column is numeric, real or double precision except the quantities", func(t *testing.T) {
		facts := queryColumnFacts(t, db, nonIntegerColumnsSQL)

		got := make([]string, 0, len(facts))
		for _, fact := range facts {
			got = append(got, fact.Column)
		}

		slices.Sort(got)
		if !slices.Equal(got, quantityColumns) {
			t.Errorf("las columnas exactas no enteras difieren:\n got: %v\nwant: %v (solo cantidades NUMERIC(10,3); ninguna puede ser dinero)", got, quantityColumns)
		}
	})

	t.Run("jsonb exists in exactly two columns", func(t *testing.T) {
		facts := queryColumnFacts(t, db, jsonbColumnsSQL)

		got := make([]string, 0, len(facts))
		for _, fact := range facts {
			got = append(got, fact.Column)
		}

		slices.Sort(got)
		if !slices.Equal(got, expectedJSONBColumns) {
			t.Errorf("las columnas jsonb difieren:\n got: %v\nwant: %v", got, expectedJSONBColumns)
		}
	})
}

// newThrowawayDatabase crea, migra y devuelve una base propia, y registra su eliminación:
// la base `pos` del desarrollador nunca se toca, y una aserción que falle igual limpia.
func newThrowawayDatabase(t *testing.T) *sql.DB {
	t.Helper()

	base := config.DatabaseURL()

	maintenanceDSN, err := postgres.WithDatabase(base, maintenanceDatabase)
	if err != nil {
		t.Fatalf("derivar la cadena de conexión de mantenimiento desde DATABASE_URL: %v", err)
	}

	name := throwawayDatabasePrefix + randomSuffix(t)
	throwawayDSN, err := postgres.WithDatabase(base, name)
	if err != nil {
		t.Fatalf("derivar la cadena de conexión desechable desde DATABASE_URL: %v", err)
	}

	// Entrecomillado, para que un nombre generado no pueda leerse nunca como sintaxis SQL.
	quoted := pgx.Identifier{name}.Sanitize()

	// Se declara antes de registrar la limpieza para que el cierre pueda cerrarla incluso
	// si la conexión de abajo falla: ni base filtrada ni conexión filtrada.
	var db *sql.DB
	t.Cleanup(func() {
		if db != nil {
			if err := db.Close(); err != nil {
				t.Errorf("cerrar la base desechable %s: %v", name, err)
			}
		}

		admin, err := sql.Open("pgx", maintenanceDSN)
		if err != nil {
			t.Errorf("abrir la conexión de mantenimiento para eliminar %s: %v", name, err)
			return
		}
		defer func() { _ = admin.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// WITH (FORCE) termina una sesión filtrada, así que ninguna base desechable puede
		// sobrevivir a esta corrida.
		if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("eliminar la base desechable %s: %v", name, err)
		}
	})

	admin, err := sql.Open("pgx", maintenanceDSN)
	if err != nil {
		t.Fatalf("abrir la base de mantenimiento: %v", err)
	}

	createCtx, cancelCreate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCreate()

	if err := admin.PingContext(createCtx); err != nil {
		_ = admin.Close()
		t.Fatalf("conectar a %s: %v (¿está el contenedor arriba? probá `docker compose up -d`)", maintenanceDatabase, err)
	}
	if _, err := admin.ExecContext(createCtx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close()
		t.Fatalf("crear la base desechable %s: %v", name, err)
	}
	if err := admin.Close(); err != nil {
		t.Fatalf("cerrar la conexión de mantenimiento: %v", err)
	}

	// Mismas migraciones embebidas, misma preparación de goose y mismo driver que
	// cmd/migrate.
	if err := postgres.ApplyMigrations(createCtx, throwawayDSN); err != nil {
		t.Fatalf("migrar la base desechable %s: %v", name, err)
	}

	db, err = sql.Open("pgx", throwawayDSN)
	if err != nil {
		t.Fatalf("abrir la base desechable %s: %v", name, err)
	}
	if err := db.PingContext(createCtx); err != nil {
		t.Fatalf("ping a la base desechable %s: %v", name, err)
	}

	return db
}

// randomSuffix devuelve 16 caracteres hexadecimales aleatorios, para que dos corridas —o
// dos binarios de prueba en paralelo— no puedan chocar en un nombre de base.
func randomSuffix(t *testing.T) string {
	t.Helper()

	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generar un sufijo aleatorio para el nombre de la base: %v", err)
	}
	return hex.EncodeToString(buf)
}

// columnFact es una fila de una consulta de columnas del catálogo.
type columnFact struct {
	Column   string
	DataType string
}

func queryStrings(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("consultar %s: %v", oneline(query), err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("leer %s: %v", oneline(query), err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer %s: %v", oneline(query), err)
	}
	return got
}

func queryColumnFacts(t *testing.T, db *sql.DB, query string) []columnFact {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("consultar %s: %v", oneline(query), err)
	}
	defer func() { _ = rows.Close() }()

	var got []columnFact
	for rows.Next() {
		var fact columnFact
		if err := rows.Scan(&fact.Column, &fact.DataType); err != nil {
			t.Fatalf("leer %s: %v", oneline(query), err)
		}
		got = append(got, fact)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer %s: %v", oneline(query), err)
	}
	return got
}

func queryOneString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()

	var got string
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("consultar %s: %v", oneline(query), err)
	}
	return got
}

func queryCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var got int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("consultar %s: %v", oneline(query), err)
	}
	return got
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()

	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("ejecutar %s: %v", oneline(query), err)
	}
}

// oneline deja una constante SQL de varias líneas legible en un mensaje de fallo.
func oneline(query string) string {
	return fmt.Sprintf("%q", strings.Join(strings.Fields(query), " "))
}
