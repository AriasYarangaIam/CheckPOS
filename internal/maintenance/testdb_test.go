package maintenance_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" de database/sql, como en cmd/migrate

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Arnés de base desechable para las pruebas de integración del mantenimiento.
//
// Es el mismo arnés de internal/ledger/testdb_test.go —crear una base con un sufijo
// aleatorio, migrarla con las migraciones embebidas y eliminarla en t.Cleanup— y está
// duplicado a propósito: vive en un archivo `_test.go` de otro paquete, así que no se puede
// importar. Este paquete necesita DOS roles: el de migración (DDL de particiones) y el de
// mantenimiento (el DELETE de la retención). Ninguno de los dos es `pos_app`: el job de
// operación no pasa por RLS de tenant, pasa por las concesiones de §5.5.
//
// La base `pos` del desarrollador nunca se toca.
const (
	// throwawayDatabasePrefix marca toda base que estas pruebas crean: `pos_test_`.
	throwawayDatabasePrefix = "pos_test_"

	// maintenanceDatabase es la base desde la que se puede CREATE DATABASE / DROP DATABASE.
	maintenanceDatabase = "postgres"
)

// testDatabase es una base propia, migrada y aislada del resto de las pruebas.
type testDatabase struct {
	// owner es la conexión del rol de migración contra la base desechable.
	owner *sql.DB
	// name es el nombre de la base desechable, el que necesita el DSN de cada rol.
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

// roleDB abre la conexión de un rol contra esta MISMA base desechable. El DSN base es el del
// rol y solo se le cambia la base, así que no hay una segunda copia de la cadena que pueda
// apuntar a otra base.
func (db *testDatabase) roleDB(t *testing.T, baseDSN, role string) *sql.DB {
	t.Helper()

	dsn, err := postgres.WithDatabase(baseDSN, db.name)
	if err != nil {
		t.Fatalf("derivar el DSN del rol %s para la base desechable %s: %v", role, db.name, err)
	}

	handle, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("abrir la conexión del rol %s: %v", role, err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("cerrar la conexión del rol %s: %v", role, err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := handle.PingContext(ctx); err != nil {
		t.Fatalf("conectar como %s a %s: %v", role, db.name, err)
	}
	return handle
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

// scalarInt cuenta o lee un escalar entero de una consulta y falla si hay error.
func scalarInt(t *testing.T, querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, query string, args ...any) int64 {
	t.Helper()

	var value int64
	if err := querier.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("leer un escalar con %q: %v", query, err)
	}
	return value
}

// seedOwningRows siembra el tenant y el usuario que las FKs de `sales` exigen.
func seedOwningRows(t *testing.T, db *testDatabase, tenantID, userID string) {
	t.Helper()

	execOn(t, db.owner, `INSERT INTO tenants (id, name, created_at) VALUES ($1, 'tenant de prueba', now())`, tenantID)
	execOn(t, db.owner,
		`INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		 VALUES ($1, $2, '51999000111', 'hash-de-prueba', now())`, userID, tenantID)
}

// seedSale siembra una venta con la fecha de negocio dada; si no existe la partición de ese
// mes, la fila aterriza en `sales_default`, que es justo lo que la remediación tiene que
// mover.
func seedSale(t *testing.T, db *testDatabase, tenantID, userID, saleID, soldOn string) {
	t.Helper()

	execOn(t, db.owner,
		`INSERT INTO sales (sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method,
		                    total_cents, created_at, received_at)
		 VALUES ($1, $2, $3, $4, 'sale', 'cash', 1250, now(), now())`,
		soldOn, saleID, tenantID, userID)
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
