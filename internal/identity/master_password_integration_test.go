// Pruebas de la credencial maestra del tenant (W8-DB-1; docs/BACKEND.md §4.4 y §11.4 #7;
// decisión D-A = A1 y D-B = B1 de odd/tasks/sprint-8-admin-hardening.md).
//
// Lo que estas pruebas fijan, contra una base migrada de verdad:
//   - un tenant recién creado NO queda sin hash: la columna es `NOT NULL` con un valor por
//     defecto aleatorio e inutilizable, así que «tenant sin hash» deja de ser un estado;
//   - `pos_auth` puede leer las tres columnas que el login de administrador necesita y
//     NINGUNA otra (concesión por columna, no de tabla);
//   - el alta de la contraseña produce un argon2id verificable y solo ese camino la cambia.
package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

const (
	masterTenantID = "019c0000-0000-7000-8000-0000000000a1"
	masterUserID   = "019c0000-0000-7000-8000-0000000000a2"
	masterPhone    = "+51988000111"
)

// seedTenantForMaster inserts a tenant the way the operator does — without a password — and a
// user whose phone is the one the admin login resolves.
func seedTenantForMaster(t *testing.T, db *testDatabase) {
	t.Helper()

	if _, err := db.owner.ExecContext(context.Background(),
		`INSERT INTO tenants (id, name, created_at) VALUES ($1, 'bodega del dueño', now())`,
		masterTenantID); err != nil {
		t.Fatalf("sembrar el tenant: %v", err)
	}

	if _, err := db.owner.ExecContext(context.Background(),
		`INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		 VALUES ($1, $2, $3, 'no-es-un-hash-real', now())`,
		masterUserID, masterTenantID, masterPhone); err != nil {
		t.Fatalf("sembrar el usuario: %v", err)
	}
}

// storedMasterHash lee la cadena guardada como dueño de la base.
func storedMasterHash(t *testing.T, db *testDatabase, tenantID string) string {
	t.Helper()

	var stored string
	if err := db.owner.QueryRowContext(context.Background(),
		`SELECT master_password_hash FROM tenants WHERE id = $1`, tenantID).Scan(&stored); err != nil {
		t.Fatalf("leer master_password_hash: %v", err)
	}
	return stored
}

// TestTenantWithoutAConfiguredMasterPasswordCannotAuthenticate: la columna es NOT NULL con un
// valor por defecto que NADIE puede satisfacer. Un tenant recién creado no puede iniciar
// sesión como administrador hasta que el alta fije la contraseña real (S11, S16; D-A = A1).
func TestTenantWithoutAConfiguredMasterPasswordCannotAuthenticate(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenantForMaster(t, db)

	stored := storedMasterHash(t, db, masterTenantID)

	// La forma es la del contrato PHC: un hash argon2id bien formado, no una cadena rota. Un
	// valor corrupto haría parecer que el problema es de datos y no de credencial ausente.
	if !strings.HasPrefix(stored, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("master_password_hash = %q, want una cadena argon2id PHC del contrato", stored)
	}

	for _, attempt := range []string{"", "admin", "bodega", "12345678", stored} {
		matches, err := identity.VerifyCredential(stored, attempt)
		if err != nil {
			t.Fatalf("verificar %q contra el valor por defecto devolvió un error de formato: %v", attempt, err)
		}
		if matches {
			t.Fatalf("verificar %q contra el valor por defecto dio TRUE: el tenant sin contraseña tendría acceso", attempt)
		}
	}

	// Y dos tenants distintos no comparten el valor por defecto: es aleatorio por fila.
	otherTenantID := "019c0000-0000-7000-8000-0000000000a3"
	if _, err := db.owner.ExecContext(context.Background(),
		`INSERT INTO tenants (id, name, created_at) VALUES ($1, 'otra bodega', now())`, otherTenantID); err != nil {
		t.Fatalf("sembrar el segundo tenant: %v", err)
	}
	if storedMasterHash(t, db, otherTenantID) == stored {
		t.Error("dos tenants comparten el mismo hash por defecto: el valor debe ser aleatorio por fila")
	}
}

// TestPosAuthReadsOnlyTheThreeTenantColumns: el rol pre-auth recibe una concesión POR COLUMNA.
// Puede resolver el login —id, nombre y hash— y no puede leer ni una columna más del registro
// de tenants (docs/DATABASE.md §5.5, §5.6; W8-DB-1).
func TestPosAuthReadsOnlyTheThreeTenantColumns(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenantForMaster(t, db)

	pool := db.rolePool(t, config.AuthDatabaseURL(), "pos_auth")
	ctx := context.Background()

	var name, stored string
	if err := pool.QueryRow(ctx,
		`SELECT name, master_password_hash FROM tenants WHERE id = $1`, masterTenantID).Scan(&name, &stored); err != nil {
		t.Fatalf("pos_auth debe leer name y master_password_hash del tenant: %v", err)
	}

	// Cualquier otra columna del registro queda fuera de la concesión. Se lee con QueryRow
	// —nunca con Query sin cerrar: una fila que no se drena deja la conexión tomada y el cierre
	// del pool se queda esperándola—.
	var createdAt string
	err := pool.QueryRow(ctx, `SELECT created_at FROM tenants WHERE id = $1`, masterTenantID).Scan(&createdAt)
	if err == nil {
		t.Fatalf("pos_auth pudo leer created_at de tenants (=%q): la concesión es de tabla y no de columna", createdAt)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("leer created_at devolvió %v, want SQLSTATE 42501 (columna sin concesión)", err)
	}
}

// TestSetMasterPasswordMakesTheTenantCredentialVerifiable: el alta produce un argon2id que
// verifica con la contraseña elegida y con ninguna otra, y rechaza un tenant inexistente sin
// escribir nada (W8-DB-1, D-B = B1).
func TestSetMasterPasswordMakesTheTenantCredentialVerifiable(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenantForMaster(t, db)

	if err := identity.SetMasterPassword(context.Background(), db.owner, mustUUID(t, masterTenantID), "clave-maestra-real"); err != nil {
		t.Fatalf("SetMasterPassword: %v", err)
	}

	stored := storedMasterHash(t, db, masterTenantID)
	if strings.HasPrefix(stored, "$argon2id$v=19$m=65536,t=3,p=2$") != true {
		t.Fatalf("la contraseña guardada no es argon2id: %q", stored)
	}

	matches, err := identity.VerifyCredential(stored, "clave-maestra-real")
	if err != nil {
		t.Fatalf("VerifyCredential de la contraseña correcta: %v", err)
	}
	if !matches {
		t.Error("la contraseña recién fijada no verifica")
	}

	wrong, err := identity.VerifyCredential(stored, "clave-maestra-real ")
	if err != nil {
		t.Fatalf("VerifyCredential de la contraseña incorrecta: %v", err)
	}
	if wrong {
		t.Error("una contraseña distinta verificó: la comparación no es estricta")
	}

	// Un tenant que no existe no se crea por accidente y el alta falla.
	err = identity.SetMasterPassword(context.Background(), db.owner, uuid.MustParse("019c0000-0000-7000-8000-0000000000ff"), "otra")
	if !errors.Is(err, identity.ErrUnknownTenant) {
		t.Fatalf("alta sobre un tenant inexistente: %v, want ErrUnknownTenant", err)
	}
}

// TestSetMasterPasswordRejectsAnEmptyCredential: una contraseña vacía no es una contraseña; el
// alta la rechaza antes de tocar la fila.
func TestSetMasterPasswordRejectsAnEmptyCredential(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenantForMaster(t, db)

	before := storedMasterHash(t, db, masterTenantID)

	for _, empty := range []string{"", "   ", "\n"} {
		if err := identity.SetMasterPassword(context.Background(), db.owner, mustUUID(t, masterTenantID), empty); err == nil {
			t.Fatalf("SetMasterPassword(%q) no falló", empty)
		}
	}

	if after := storedMasterHash(t, db, masterTenantID); after != before {
		t.Error("un alta inválida escribió la fila")
	}
}

// mustUUID parsea un uuid del arnés o aborta.
func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()

	parsed, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("%q no es un uuid: %v", raw, err)
	}
	return parsed
}
