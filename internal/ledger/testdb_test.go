// Arnés de base desechable para las pruebas de integración del ledger (venta).
//
// Es el mismo arnés de internal/catalog/testdb_test.go —crear una base con un sufijo
// aleatorio, migrarla con las migraciones embebidas y eliminarla en t.Cleanup— y está
// duplicado a propósito: vive en un archivo `_test.go` de otro paquete, así que no se
// puede importar. Este paquete solo necesita el pool de rol `pos_app` apuntando a la MISMA
// base desechable.
//
// La base `pos` del desarrollador nunca se toca (docs/BACKEND.md §9, `newThrowawayDatabase`).
package ledger_test

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
// transacción de tenant. Las aserciones de aislamiento solo miden por acá.
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

// seedSaleUser siembra la fila de `users` del usuario del token: `sales.sold_by_user_id`
// tiene una FK a `users` (migrations/20261015000004_ledger.sql) y el cierre de venta tiene
// que encontrarlo. Es SIEMBRA como dueño, igual que seedTenant/seedProduct.
func seedSaleUser(t *testing.T, db *testDatabase, tenantID, userID string) {
	t.Helper()

	tx, err := db.owner.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra del usuario: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// El phone es globalmente UNIQUE (§4): el sufijo aleatorio de la base desechable
	// garantiza que dos corridas no choquen nunca.
	phone := "+549110000-" + db.name
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, $3, 'test-not-a-real-hash', now())`, userID, tenantID, phone); err != nil {
		t.Fatalf("sembrar el usuario %s: %v", userID, err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra del usuario: %v", err)
	}
}

// seedClaim siembra UNA fila de `idempotency_keys` como DUEÑO, con el estado pedido. Es
// siembra de estados que dos clientes concurrentes solo producirían viviendo el protocolo
// (una fila en vuelo de una transacción ganadora o de un Claim aún abierto).
func seedClaim(t *testing.T, db *testDatabase, tenantID, key string) {
	t.Helper()

	tx, err := db.owner.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra del claim: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("generar el id del claim sembrado: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), `
		INSERT INTO idempotency_keys (id, tenant_id, key, payload_hash, state, created_at)
		VALUES ($1, $2, $3, 'seeded-payload-hash', 'in_flight', now())`,
		id, tenantID, key); err != nil {
		t.Fatalf("sembrar el claim %s: %v", key, err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra del claim: %v", err)
	}
}

// saleQueryCount corre una consulta de conteo por pos_app dentro del contexto del tenant.
func saleQueryCount(t *testing.T, tenants *postgres.TenantPool, tenantID, query string, args ...any) int {
	t.Helper()

	count := -1
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
			t.Fatalf("contar con %q: %v", query, err)
		}
	})
	return count
}

// seedTenant siembra una fila de `tenants` como dueño, sin contexto de tenant: el registro
// vive fuera del RLS de tenant (docs/DATABASE.md §3).
func seedTenant(t *testing.T, db *testDatabase, tenantID string) {
	t.Helper()

	tx, err := db.owner.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción sin contexto: %v", err)
	}

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO tenants (id, name, created_at) VALUES ($1, 'tenant de prueba', now())`, tenantID); err != nil {
		t.Fatalf("sembrar el tenant %s: %v", tenantID, err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la transacción sin contexto: %v", err)
	}
}

// saleProduct es el producto sembrado para una venta.
type saleProduct struct {
	id       string
	tenantID string
	stock    string
}

// seedProduct siembra la fila de `products` como DUEÑO (superusuario, sin RLS). Es SIEMBRA
// solamente: ninguna aserción de aislamiento lee por acá (§5.7).
func seedProduct(t *testing.T, db *testDatabase, product saleProduct) {
	t.Helper()

	tx, err := db.owner.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Las marcas van duplicadas y fijas: la aserción de W4-BE-1 es que el cierre de venta
	// NO las toca (products.updated_at queda con la marca del catálogo, MD-11).
	stamp := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := tx.ExecContext(context.Background(), `
		INSERT INTO products (id, tenant_id, name, price_cents, uom, stock, created_at, updated_at, received_at)
		VALUES ($1, $2, 'arroz especial de venta', 1550, 'kg', $3, $4, $4, now())`,
		product.id, product.tenantID, product.stock, stamp); err != nil {
		t.Fatalf("sembrar el producto %s: %v", product.id, err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra: %v", err)
	}
}

// readProductStock lee el stock y la marca de un producto por pos_app con RLS (el camino de
// producción), en la forma del contrato. La marca vuelve como time.Time porque pgx no puede
// escanear un timestamptz binario en un string.
func readProductStock(t *testing.T, tenants *postgres.TenantPool, tenantID, productID string) (stock string, updatedAt time.Time) {
	t.Helper()

	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), `SELECT stock::text, updated_at FROM products WHERE id = $1`,
			mustParseUUID(t, productID)).Scan(&stock, &updatedAt); err != nil {
			t.Fatalf("leer el producto %s por pos_app: %v", productID, err)
		}
	})
	return stock, updatedAt
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
