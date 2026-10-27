package maintenance_test

import (
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/maintenance"
)

// Pruebas unitarias de la aritmética de particiones: el calendario es el contrato, así que
// los nombres, los rangos y el horizonte se fijan acá sin base de datos.

// TestPartitionNameFollowsTheMigrationConvention: el nombre de la partición es el que las
// migraciones ya usan (`sales_AAAA_MM`); el job no inventa una segunda convención.
func TestPartitionNameFollowsTheMigrationConvention(t *testing.T) {
	cases := map[string]string{
		"2026-10-23T15:04:05Z": "sales_2026_10",
		"2026-12-01T00:00:00Z": "sales_2026_12",
		"2027-01-31T23:59:59Z": "sales_2027_01",
	}
	for moment, want := range cases {
		parsed, err := time.Parse(time.RFC3339, moment)
		if err != nil {
			t.Fatalf("parsear %q: %v", moment, err)
		}
		if got := maintenance.PartitionName(parsed); got != want {
			t.Errorf("PartitionName(%s) = %q, want %q", moment, got, want)
		}
	}
}

// TestMonthsToEnsureCoversTheSixMonthHorizon: el job deja el mes corriente y los seis
// siguientes —siete particiones—, en orden y normalizadas al día 1 (docs/DATABASE.md §6.3).
func TestMonthsToEnsureCoversTheSixMonthHorizon(t *testing.T) {
	now := time.Date(2026, 10, 23, 15, 4, 5, 0, time.UTC)

	months := maintenance.MonthsToEnsure(now, maintenance.HorizonMonths)

	if len(months) != 7 {
		t.Fatalf("meses = %d, want 7 (el corriente y seis hacia adelante)", len(months))
	}
	want := []string{
		"2026-10-01", "2026-11-01", "2026-12-01", "2027-01-01",
		"2027-02-01", "2027-03-01", "2027-04-01",
	}
	for index, month := range months {
		if got := month.Format(time.DateOnly); got != want[index] {
			t.Errorf("mes %d = %s, want %s", index, got, want[index])
		}
	}
}
