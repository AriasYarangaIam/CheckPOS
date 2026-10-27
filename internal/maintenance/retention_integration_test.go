package maintenance_test

import (
	"context"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/maintenance"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// Pruebas de la retención de claves de idempotencia (Sprint 6, W6-DB-1; docs/DATABASE.md
// §7.2) contra PostgreSQL real, por el rol `pos_maintenance`: es la única forma de probar
// que la concesión de §5.5 alcanza y que el WHERE respeta las filas en vuelo.

const (
	testTenantID = "0192f3a4-5b6c-7d8e-9f01-23456789ae01"
	testUserID   = "0192f3a4-5b6c-7d8e-9f01-23456789ae02"
)

// seedClaim siembra una clave de idempotencia con su estado y su marca de creación.
func seedClaim(t *testing.T, db *testDatabase, key, state string, createdAt time.Time) {
	t.Helper()

	var snapshot any
	if state == "completed" {
		snapshot = `{"seeded":true}`
	}

	execOn(t, db.owner,
		`INSERT INTO idempotency_keys (id, tenant_id, key, payload_hash, state, response_snapshot, created_at)
		 VALUES (gen_random_uuid(), $1, $2, 'hash-de-prueba', $3, $4, $5)`,
		testTenantID, key, state, snapshot, createdAt.UTC())
}

// claimCount cuenta las claves que quedan por nombre de clave.
func claimCount(t *testing.T, db *testDatabase, key string) int64 {
	t.Helper()

	return scalarInt(t, db.owner, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

// TestRetentionDeletesOnlyTheCompletedKeysOlderThanTheWindow: con el rol de mantenimiento,
// el borrado alcanza la clave completada y vencida, y NO toca la completada reciente ni la
// que está en vuelo (docs/DATABASE.md §7.2).
func TestRetentionDeletesOnlyTheCompletedKeysOlderThanTheWindow(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedOwningRows(t, db, testTenantID, testUserID)

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	seedClaim(t, db, "clave-vencida", "completed", now.Add(-401*24*time.Hour))
	seedClaim(t, db, "clave-reciente", "completed", now.Add(-399*24*time.Hour))
	seedClaim(t, db, "clave-en-vuelo", "in_flight", now.Add(-401*24*time.Hour))

	maintenanceDB := db.roleDB(t, config.MaintenanceDatabaseURL(), "pos_maintenance")
	result, err := maintenance.DeleteExpiredClaimsBefore(context.Background(), maintenanceDB, now)
	if err != nil {
		t.Fatalf("correr la retención con pos_maintenance: %v", err)
	}

	if result.Deleted != 1 {
		t.Errorf("claves borradas = %d, want 1 (solo la completada y vencida)", result.Deleted)
	}
	if rows := claimCount(t, db, "clave-vencida"); rows != 0 {
		t.Errorf("la clave vencida sigue ahí (%d filas)", rows)
	}
	if rows := claimCount(t, db, "clave-reciente"); rows != 1 {
		t.Errorf("la clave reciente se borró: %d filas, want 1 — la ventana es de 400 días", rows)
	}
	if rows := claimCount(t, db, "clave-en-vuelo"); rows != 1 {
		t.Errorf("la clave en vuelo se borró: %d filas, want 1 — una operación en vuelo no tiene snapshot", rows)
	}
}

// TestRetentionRunTwiceDeletesNothingTheSecondTime: el job es repetible; una segunda corrida
// sobre el mismo instante no encuentra nada que borrar.
func TestRetentionRunTwiceDeletesNothingTheSecondTime(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedOwningRows(t, db, testTenantID, testUserID)

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	seedClaim(t, db, "clave-vencida", "completed", now.Add(-401*24*time.Hour))

	maintenanceDB := db.roleDB(t, config.MaintenanceDatabaseURL(), "pos_maintenance")
	first, err := maintenance.DeleteExpiredClaimsBefore(context.Background(), maintenanceDB, now)
	if err != nil {
		t.Fatalf("primera corrida: %v", err)
	}
	if first.Deleted != 1 {
		t.Fatalf("primera corrida borró %d claves, want 1", first.Deleted)
	}

	second, err := maintenance.DeleteExpiredClaimsBefore(context.Background(), maintenanceDB, now)
	if err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}
	if second.Deleted != 0 {
		t.Errorf("segunda corrida borró %d claves, want 0: el job es idempotente", second.Deleted)
	}
}
