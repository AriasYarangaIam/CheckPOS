package postgres_test

import (
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// TestWithDatabase fija la única cirugía de DSN que hace esta capa: apuntar la cadena de
// conexión compartida a otra base del mismo servidor, que es como el test de contrato de
// esquema alcanza la base de mantenimiento y su base desechable.
func TestWithDatabase(t *testing.T) {
	const base = "postgres://pos:pos_local_dev@localhost:5433/pos?sslmode=disable"

	t.Run("swaps the database and keeps everything else", func(t *testing.T) {
		got, err := postgres.WithDatabase(base, "postgres")
		if err != nil {
			t.Fatalf("WithDatabase(%q, %q): error inesperado: %v", base, "postgres", err)
		}

		for _, keep := range []string{"pos:pos_local_dev@", "localhost:5433", "sslmode=disable"} {
			if !strings.Contains(got, keep) {
				t.Errorf("WithDatabase perdió %q; obtuvo %q", keep, got)
			}
		}
		if strings.Contains(got, "/pos?") || strings.HasSuffix(got, "/pos") {
			t.Errorf("WithDatabase(%q, %q) = %q, want el nombre de la base reemplazado", base, "postgres", got)
		}
		if !strings.Contains(got, "/postgres") {
			t.Errorf("WithDatabase(%q, %q) = %q, want que apunte a /postgres", base, "postgres", got)
		}
	})

	t.Run("rejects a connection string it cannot parse", func(t *testing.T) {
		if got, err := postgres.WithDatabase("://not a url", "postgres"); err == nil {
			t.Errorf("WithDatabase sobre un DSN que no se puede parsear devolvió %q sin error, want un error", got)
		}
	})

	t.Run("rejects an empty database name", func(t *testing.T) {
		if got, err := postgres.WithDatabase(base, ""); err == nil {
			t.Errorf("WithDatabase(%q, \"\") devolvió %q sin error, want un error", base, got)
		}
	})
}
