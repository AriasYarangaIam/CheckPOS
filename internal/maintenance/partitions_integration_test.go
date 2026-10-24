package maintenance_test

import (
	"context"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/maintenance"
)

// Pruebas de integración del mantenimiento de particiones (Sprint 6, W6-DB-2;
// docs/DATABASE.md §6.3-§6.7) contra PostgreSQL real: la idempotencia del job periódico y
// la garantía todo-o-nada de la remediación F5.

const (
	strandedTenantID = "0192f3a4-5b6c-7d8e-9f01-23456789af01"
	strandedUserID   = "0192f3a4-5b6c-7d8e-9f01-23456789af02"
)

// TestEnsurePartitionsIsIdempotent: correr el job dos veces el mismo día deja el mismo
// conjunto de particiones —el horizonte completo— y ninguna fila varada (§6.3, §6.6).
func TestEnsurePartitionsIsIdempotent(t *testing.T) {
	db := newThrowawayDatabase(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	first, err := maintenance.EnsurePartitions(context.Background(), db.owner, now)
	if err != nil {
		t.Fatalf("primera corrida: %v", err)
	}
	second, err := maintenance.EnsurePartitions(context.Background(), db.owner, now)
	if err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("la segunda corrida devolvió %d particiones, want las mismas %d", len(second), len(first))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Errorf("partición %d = %q en la segunda corrida, want %q", index, second[index], first[index])
		}
	}

	for _, name := range first {
		if exists := scalarInt(t, db.owner,
			`SELECT count(*) FROM pg_class WHERE relname = $1`, name); exists != 1 {
			t.Errorf("la partición %q no existe en el esquema", name)
		}

		// El espejo de aislamiento viaja con la partición: PostgreSQL no propaga RLS,
		// políticas ni concesiones desde el padre (§5.5, §5.7).
		if pinned := scalarInt(t, db.owner,
			`SELECT count(*) FROM pg_class WHERE relname = $1 AND relrowsecurity AND relforcerowsecurity`,
			name); pinned != 1 {
			t.Errorf("la partición %q no lleva el pin de RLS (enable + force)", name)
		}
		if policies := scalarInt(t, db.owner,
			`SELECT count(*) FROM pg_policies WHERE tablename = $1`, name); policies != 4 {
			t.Errorf("la partición %q tiene %d políticas de tenant, want las 4 del espejo", name, policies)
		}
		if readable := scalarInt(t, db.owner,
			`SELECT CASE WHEN has_table_privilege('pos_app', $1, 'SELECT') THEN 1 ELSE 0 END`,
			name); readable != 1 {
			t.Errorf("pos_app no puede leer %q: la partición quedó sin la concesión de su padre", name)
		}
	}

	if stranded := scalarInt(t, db.owner, `SELECT count(*) FROM sales_default`); stranded != 0 {
		t.Errorf("sales_default tiene %d filas, want 0", stranded)
	}
}

// TestRemediateDefaultMovesTheStrandedRowsAndLeavesTheDefaultEmpty: F5 con filas de un solo
// mes —el caso de reloj adelantado— deja la DEFAULT vacía y la fila en su partición mensual
// (§6.5).
func TestRemediateDefaultMovesTheStrandedRowsAndLeavesTheDefaultEmpty(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedOwningRows(t, db, strandedTenantID, strandedUserID)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	// Una venta de un mes que todavía no tiene partición: aterriza en sales_default.
	seedSale(t, db, strandedTenantID, strandedUserID, "0192f3a4-5b6c-7d8e-9f01-23456789af10", "2030-01-15")

	if stranded := scalarInt(t, db.owner, `SELECT count(*) FROM sales_default`); stranded != 1 {
		t.Fatalf("filas varadas = %d, want 1 antes de remediar", stranded)
	}

	result, err := maintenance.RemediateDefault(context.Background(), db.owner, now)
	if err != nil {
		t.Fatalf("remediar sales_default: %v", err)
	}

	if result.RowsMoved != 1 {
		t.Errorf("filas movidas = %d, want 1", result.RowsMoved)
	}
	if result.TargetPartition != "sales_2030_01" {
		t.Errorf("partición destino = %q, want sales_2030_01", result.TargetPartition)
	}
	if stranded := scalarInt(t, db.owner, `SELECT count(*) FROM sales_default`); stranded != 0 {
		t.Errorf("sales_default quedó con %d filas, want 0: la remediación deja la DEFAULT vacía o revierte", stranded)
	}
	if moved := scalarInt(t, db.owner, `SELECT count(*) FROM sales_2030_01`); moved != 1 {
		t.Errorf("sales_2030_01 tiene %d filas, want 1", moved)
	}
}

// TestRemediateDefaultRollsBackWhenTheRowsSpanTwoMonths: sin un rango único al que
// re-adjuntar, la remediación revierte completa —DEFAULT intacta, ninguna partición nueva—
// y el caso queda para intervención humana (§6.5).
func TestRemediateDefaultRollsBackWhenTheRowsSpanTwoMonths(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedOwningRows(t, db, strandedTenantID, strandedUserID)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	seedSale(t, db, strandedTenantID, strandedUserID, "0192f3a4-5b6c-7d8e-9f01-23456789af11", "2030-01-15")
	seedSale(t, db, strandedTenantID, strandedUserID, "0192f3a4-5b6c-7d8e-9f01-23456789af12", "2030-02-15")

	if _, err := maintenance.RemediateDefault(context.Background(), db.owner, now); err == nil {
		t.Fatal("la remediación de dos meses distintos no falló: tiene que revertir y pedir intervención humana")
	}

	if stranded := scalarInt(t, db.owner, `SELECT count(*) FROM sales_default`); stranded != 2 {
		t.Errorf("sales_default quedó con %d filas, want las 2 originales: la transacción revierte completa", stranded)
	}
	if exists := scalarInt(t, db.owner,
		`SELECT count(*) FROM pg_class WHERE relname = 'sales_2030_01'`); exists != 0 {
		t.Error("la partición sales_2030_01 quedó creada: el rollback tiene que deshacer también el DDL")
	}
	if count := scalarInt(t, db.owner, `SELECT count(*) FROM sales`); count != 2 {
		t.Errorf("sales tiene %d filas, want 2: ninguna fila se perdió ni se duplicó", count)
	}
}
