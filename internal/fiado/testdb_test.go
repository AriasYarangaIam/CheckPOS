// Arnés de base desechable para las pruebas de integración del módulo Fiado.
//
// Es el mismo arnés de internal/cashclose/testdb_test.go —crear una base con un sufijo
// aleatorio, migrarla con las migraciones embebidas y eliminarla en t.Cleanup— y está
// duplicado por la misma razón: vive en un archivo `_test.go` de otro paquete y no se puede
// importar. Este paquete necesita el pool de rol `pos_app` apuntando a la MISMA base
// desechable, más la siembra de las dos tablas de fiado y de la venta a la que un movimiento
// puede enlazarse.
//
// La base `pos` del desarrollador nunca se toca (docs/BACKEND.md §9).
package fiado_test

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
)

// testDatabase es una base propia, migrada y aislada del resto de las pruebas.
type testDatabase struct {
	// owner es la conexión del rol de migración contra la base desechable: siembra las filas
	// con el rol que puede escribir sin concesiones de runtime.
	owner *sql.DB
	// name es el nombre de la base desechable, el que necesitan los DSN de rol.
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

// withTenant lee por el CAMINO DE PRODUCCIÓN: el pool de `pos_app` dentro del helper de
// transacción de tenant.
func withTenant(t *testing.T, tenants *postgres.TenantPool, tenantID string, fn func(tx postgres.TenantTx)) {
	t.Helper()

	err := tenants.WithTenant(context.Background(), mustParseUUID(t, tenantID), func(_ context.Context, tx postgres.TenantTx) error {
		fn(tx)
		return nil
	})
	if err != nil {
		t.Fatalf("correr la transacción de tenant %s por pos_app: %v", tenantID, err)
	}
}

// queryCount corre una consulta de conteo por pos_app dentro del contexto del tenant.
func queryCount(t *testing.T, tenants *postgres.TenantPool, tenantID, query string, args ...any) int {
	t.Helper()

	count := -1
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
			t.Fatalf("contar con %q: %v", query, err)
		}
	})
	return count
}

// queryString corre una consulta escalar de texto por pos_app con el contexto del tenant.
func queryString(t *testing.T, tenants *postgres.TenantPool, tenantID, query string, args ...any) string {
	t.Helper()

	value := ""
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), query, args...).Scan(&value); err != nil {
			t.Fatalf("leer con %q: %v", query, err)
		}
	})
	return value
}

// execOwner ejecuta una sentencia como dueño de la base (siembra o intento rechazado).
func execOwner(t *testing.T, db *testDatabase, query string, args ...any) error {
	t.Helper()

	_, err := db.owner.ExecContext(context.Background(), query, args...)
	return err
}

// seedTenant siembra una fila de `tenants` como dueño, sin contexto de tenant.
func seedTenant(t *testing.T, db *testDatabase, tenantID string) {
	t.Helper()

	if err := execOwner(t, db, `INSERT INTO tenants (id, name, created_at) VALUES ($1, 'tenant de prueba', now())`, tenantID); err != nil {
		t.Fatalf("sembrar el tenant %s: %v", tenantID, err)
	}
}

// seedUser siembra la fila de `users` que la venta enlazada necesita.
func seedUser(t *testing.T, db *testDatabase, tenantID, userID string) {
	t.Helper()

	// El phone es globalmente UNIQUE (§4): el sufijo aleatorio de la base desechable
	// garantiza que dos corridas no choquen nunca.
	phone := "+519110000-" + db.name
	if err := execOwner(t, db, `INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, $3, 'no-es-un-hash-real', now())`, userID, tenantID, phone); err != nil {
		t.Fatalf("sembrar el usuario %s: %v", userID, err)
	}
}

// seedSale siembra una venta a fiado como dueño: el enlace opcional de `fiado_movements`
// referencia `sales(sold_on, sale_id)` y la FK no aprobaría una fila inventada.
func seedSale(t *testing.T, db *testDatabase, tenantID, userID, saleID, soldOn string, totalCents int64) {
	t.Helper()

	if err := execOwner(t, db, `INSERT INTO sales
		(sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method, total_cents, created_at, received_at)
		VALUES ($1, $2, $3, $4, 'sale', 'fiado', $5, now(), now())`,
		soldOn, saleID, tenantID, userID, totalCents); err != nil {
		t.Fatalf("sembrar la venta %s: %v", saleID, err)
	}
}

// seedAccount siembra una cuenta de fiado como dueño.
func seedAccount(t *testing.T, db *testDatabase, tenantID, accountID, name, phone string) {
	t.Helper()

	var nullablePhone any
	if phone != "" {
		nullablePhone = phone
	}
	if err := execOwner(t, db, `INSERT INTO fiado_accounts
		(id, tenant_id, name, phone, created_at, updated_at, received_at)
		VALUES ($1, $2, $3, $4, now(), now(), now())`, accountID, tenantID, name, nullablePhone); err != nil {
		t.Fatalf("sembrar la cuenta %s: %v", accountID, err)
	}
}

// seedMovement siembra un movimiento firmado como dueño. `soldOn` y `saleID` viajan juntos o
// vacíos: la tabla impone el par de nulidad.
func seedMovement(t *testing.T, db *testDatabase, tenantID, movementID, accountID string, amountCents int64, soldOn, saleID string) {
	t.Helper()

	var nullableSoldOn, nullableSaleID any
	if soldOn != "" {
		nullableSoldOn = soldOn
		nullableSaleID = saleID
	}
	if err := execOwner(t, db, `INSERT INTO fiado_movements
		(id, tenant_id, fiado_account_id, amount_cents, sold_on, sale_id, created_at, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())`,
		movementID, tenantID, accountID, amountCents, nullableSoldOn, nullableSaleID); err != nil {
		t.Fatalf("sembrar el movimiento %s: %v", movementID, err)
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
