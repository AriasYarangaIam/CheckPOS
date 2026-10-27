// Arnés de base desechable para las pruebas de integración del pull por cursor.
//
// Es el mismo arnés de internal/catalog/testdb_test.go —crear una base con un sufijo
// aleatorio, migrarla con las migraciones embebidas y eliminarla en t.Cleanup— y está
// duplicado a propósito: vive en un archivo `_test.go` de otro paquete, así que no se puede
// importar. Este paquete necesita el pool de `pos_app` para leer por el camino de
// producción (RLS incluido) y el del dueño para sembrar las filas.
//
// La base `pos` del desarrollador nunca se toca (docs/BACKEND.md §9, `newThrowawayDatabase`).
package synccursor_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" de database/sql, como en cmd/migrate

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

const (
	// throwawayDatabasePrefix marca toda base que estas pruebas crean: `pos_test_`.
	throwawayDatabasePrefix = "pos_test_"

	// maintenanceDatabase es la base desde la que se puede CREATE DATABASE / DROP DATABASE.
	maintenanceDatabase = "postgres"

	// tenantSettingSQL fija el contexto de tenant de la transacción actual, con la forma
	// parametrizable que el helper de producción usa (internal/platform/postgres/tenant.go).
	tenantSettingSQL = `SELECT set_config('app.current_tenant_id', $1, true)`
)

// testDatabase es una base propia, migrada y aislada del resto de las pruebas.
type testDatabase struct {
	// owner es la conexión del rol de migración contra la base desechable: siembra las filas
	// con el rol que puede escribir sin concesiones de runtime.
	owner *sql.DB
	// name es el nombre de la base desechable, el que necesita el DSN de rol.
	name string
}

// newThrowawayDatabase crea, migra y devuelve una base propia, y registra su eliminación.
func newThrowawayDatabase(t *testing.T) *testDatabase {
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

	var owner *sql.DB
	t.Cleanup(func() {
		if owner != nil {
			if err := owner.Close(); err != nil {
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

	if err := postgres.ApplyMigrations(createCtx, throwawayDSN); err != nil {
		t.Fatalf("migrar la base desechable %s: %v", name, err)
	}

	owner, err = sql.Open("pgx", throwawayDSN)
	if err != nil {
		t.Fatalf("abrir la base desechable: %v", err)
	}
	if err := owner.PingContext(createCtx); err != nil {
		t.Fatalf("ping a la base desechable: %v", err)
	}

	return &testDatabase{owner: owner, name: name}
}

// rolePool abre un pool de un rol de runtime contra esta MISMA base desechable.
func (db *testDatabase) rolePool(t *testing.T, baseDSN, role string) *pgxpool.Pool {
	t.Helper()

	dsn, err := postgres.WithDatabase(baseDSN, db.name)
	if err != nil {
		t.Fatalf("derivar el DSN del rol %s para la base desechable %s: %v", role, db.name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := postgres.New(ctx, dsn)
	if err != nil {
		t.Fatalf("construir el pool del rol %s: %v", role, err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("conectar como %s a %s: %v", role, db.name, err)
	}

	return pool
}

// inTenant corre fn dentro de una transacción del dueño con el contexto de tenant fijado.
//
// El dueño del contenedor local es `pos`, superusuario: por acá SOLO se siembran filas con
// columnas explícitas. Las aserciones leen por `pos_app` (withTenant), que es el camino de
// producción con RLS (docs/DATABASE.md §5.2).
func inTenant(t *testing.T, db *testDatabase, tenantID string, fn func(tx *sql.Tx)) {
	t.Helper()

	ctx := context.Background()
	tx, err := db.owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("abrir la transacción del tenant %s: %v", tenantID, err)
	}

	var setting string
	if err := tx.QueryRowContext(ctx, tenantSettingSQL, tenantID).Scan(&setting); err != nil {
		t.Fatalf("fijar el contexto de tenant %s: %v", tenantID, err)
	}

	fn(tx)

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la transacción del tenant %s: %v", tenantID, err)
	}
}

// execOn ejecuta una sentencia sobre un querier y falla si hay error.
func execOn(t *testing.T, querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, query string, args ...any) {
	t.Helper()

	if _, err := querier.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("ejecutar %q: %v", query, err)
	}
}

// seedTenant siembra una fila de `tenants`, el registro que vive fuera del RLS de tenant.
func seedTenant(t *testing.T, db *testDatabase, tenantID string) {
	t.Helper()

	tx, err := db.owner.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción sin contexto: %v", err)
	}
	execOn(t, tx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, 'tenant de prueba', now())`, tenantID)
	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra del tenant: %v", err)
	}
}

// randomSuffix devuelve 16 caracteres hexadecimales aleatorios, para que dos corridas —o dos
// binarios de prueba en paralelo— no puedan chocar en un nombre de base.
func randomSuffix(t *testing.T) string {
	t.Helper()

	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generar un sufijo aleatorio para el nombre de la base: %v", err)
	}
	return hex.EncodeToString(buf)
}

// mustParseUUID es un helper de las pruebas: parsear o abortar.
func mustParseUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()

	parsed, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("%q no es un uuid: %v", raw, err)
	}
	return parsed
}
