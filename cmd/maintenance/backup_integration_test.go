package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// Ensayo completo contra una base real: dump, manifiesto, restauración en una base desechable
// y comparación. Se salta cuando Docker o el contenedor no están disponibles —el repositorio no
// convierte la falta de infraestructura en un fallo de código— y siempre borra lo que creó.
func TestBackupAndRestoreRoundTrip(t *testing.T) {
	dsn := config.DatabaseURL()
	if strings.TrimSpace(dsn) == "" {
		t.Skip("DATABASE_URL is not set: no real database to drill against")
	}

	var stdout, stderr bytes.Buffer
	runner := osCommandRunner{}
	if err := runner.Run(context.Background(), "docker", []string{"compose", "ps", "-q", "postgres"}, nil, &stdout, &stderr); err != nil || strings.TrimSpace(stdout.String()) == "" {
		t.Skip("the postgres compose service is not running: start it with `docker compose up -d`")
	}

	dir := t.TempDir()
	manifest, err := createBackup(context.Background(), defaultBackupEnvironment(), backupOptions{
		Dir:     dir,
		DSN:     dsn,
		Service: "postgres",
	})
	if err != nil {
		t.Fatalf("createBackup: %v", err)
	}
	if manifest.Bytes == 0 || manifest.SHA256 == "" || len(manifest.RowCounts) != len(contractTables) {
		t.Fatalf("manifest = %+v, want a complete snapshot", manifest)
	}

	// Nombre desechable y único: la base se crea y se borra dentro de este test.
	scratch := "pos_restore_it_" + time.Now().UTC().Format("20060102t150405")
	summary, err := verifyRestore(context.Background(), defaultBackupEnvironment(), restoreOptions{
		ManifestPath: dir + "/" + strings.TrimSuffix(manifest.DumpFile, ".dump") + ".manifest.json",
		Scratch:      scratch,
		DSN:          dsn,
		Service:      "postgres",
	})
	if err != nil {
		t.Fatalf("verifyRestore: %v", err)
	}
	if !summary.Restored || !summary.SchemaMatch || !summary.RowCountMatch || !summary.Dropped {
		t.Fatalf("summary = %+v, want a complete verified drill", summary)
	}
	if summary.TablesVerified != len(contractTables) {
		t.Fatalf("TablesVerified = %d, want %d", summary.TablesVerified, len(contractTables))
	}
}
