package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pruebas del subcomando `maintenance backup` (Sprint 9, W9-DB-1). Sin Docker y sin base: el
// runner falso devuelve las salidas que producirían psql y pg_dump, y lo que se afirma es el
// contrato del manifiesto, la huella y el argv que se ejecutaría.

// fakeCall registra una invocación del runner.
type fakeCall struct {
	name string
	args []string
}

// fakeResponse es la salida programada para la llamada i-ésima.
type fakeResponse struct {
	stdout string
	stderr string
	err    error
}

// fakeRunner sustituye a osCommandRunner y responde en orden.
type fakeRunner struct {
	calls     []fakeCall
	responses []fakeResponse
	stdinSeen []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, fakeCall{name: name, args: append([]string(nil), args...)})
	if stdin != nil {
		payload, _ := io.ReadAll(stdin)
		f.stdinSeen = append(f.stdinSeen, string(payload))
	}

	index := len(f.calls) - 1
	if index >= len(f.responses) {
		return fmt.Errorf("unexpected call %d: %s %v", index, name, args)
	}
	response := f.responses[index]
	if response.stdout != "" {
		_, _ = io.WriteString(stdout, response.stdout)
	}
	if response.stderr != "" {
		_, _ = io.WriteString(stderr, response.stderr)
	}
	return response.err
}

// countsOutput arma la salida `nombre|conteo` que psql devolvería para las catorce tablas.
func countsOutput(counts map[string]int64) string {
	var builder strings.Builder
	for _, table := range contractTables {
		fmt.Fprintf(&builder, "%s|%d\n", table, counts[table])
	}
	return builder.String()
}

// allCounts devuelve un conteo para cada tabla del contrato.
func allCounts(value int64) map[string]int64 {
	counts := make(map[string]int64, len(contractTables))
	for _, table := range contractTables {
		counts[table] = value
	}
	return counts
}

func testBackupEnvironment(runner commandRunner) backupEnvironment {
	return backupEnvironment{
		runner: runner,
		now: func() time.Time {
			return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
		},
		getenv: func(string) string { return "" },
	}
}

func TestBackupWritesDumpAndManifest(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: "products.id:uuid:NO\n"},
		{stdout: countsOutput(allCounts(3))},
		{stdout: "DUMP"},
	}}
	dir := t.TempDir()

	manifest, err := createBackup(context.Background(), testBackupEnvironment(runner), backupOptions{
		Dir:     dir,
		DSN:     "postgres://pos:pos_local_dev@localhost:5433/pos",
		Service: "postgres",
	})
	if err != nil {
		t.Fatalf("createBackup: %v", err)
	}

	if manifest.Format != backupFormat || manifest.Database != "pos" || manifest.Role != "pos" {
		t.Fatalf("manifest = %+v, want the pg_dump custom format of database pos", manifest)
	}
	if manifest.CreatedAt != "2026-11-25T12:00:00Z" {
		t.Fatalf("CreatedAt = %q, want the injected UTC clock", manifest.CreatedAt)
	}

	sum := sha256.Sum256([]byte("DUMP"))
	if manifest.SHA256 != hex.EncodeToString(sum[:]) || manifest.Bytes != 4 {
		t.Fatalf("manifest sha256/bytes = %q/%d, want the dump hash and size", manifest.SHA256, manifest.Bytes)
	}
	if manifest.RowCounts["sales"] != 3 {
		t.Fatalf("row_counts[sales] = %d, want 3", manifest.RowCounts["sales"])
	}

	dumpPath := filepath.Join(dir, manifest.DumpFile)
	payload, err := os.ReadFile(dumpPath)
	if err != nil || string(payload) != "DUMP" {
		t.Fatalf("dump = %q (%v), want the runner output", payload, err)
	}

	// El manifiesto es el contrato del ensayo: tiene que poder releerse.
	readBack, err := readManifest(filepath.Join(dir, strings.TrimSuffix(manifest.DumpFile, ".dump")+".manifest.json"))
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}
	if readBack.SHA256 != manifest.SHA256 {
		t.Fatalf("read back sha256 = %q, want %q", readBack.SHA256, manifest.SHA256)
	}

	// El dump se ejecuta DENTRO del contenedor, no en el host.
	last := runner.calls[len(runner.calls)-1]
	if last.name != "docker" || !equalArgs(last.args, []string{"compose", "exec", "-T", "postgres", "pg_dump", "-Fc", "-U", "pos", "-d", "pos"}) {
		t.Fatalf("pg_dump call = %s %v, want docker compose exec", last.name, last.args)
	}
}

func TestBackupRequiresADestinationDirectory(t *testing.T) {
	_, err := createBackup(context.Background(), testBackupEnvironment(&fakeRunner{}), backupOptions{
		DSN: "postgres://pos:pos_local_dev@localhost:5433/pos",
	})
	if err == nil || !strings.Contains(err.Error(), "create the destination directory") {
		t.Fatalf("err = %v, want a directory error", err)
	}
}

func TestBackupRejectsAMissingDSN(t *testing.T) {
	_, err := createBackup(context.Background(), testBackupEnvironment(&fakeRunner{}), backupOptions{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is not set") {
		t.Fatalf("err = %v, want the missing DSN error", err)
	}
}

func TestBackupRemovesThePartialDumpWhenPgDumpFails(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: "products.id:uuid:NO\n"},
		{stdout: countsOutput(allCounts(0))},
		{stderr: "connection refused", err: errors.New("exit status 1")},
	}}
	dir := t.TempDir()

	_, err := createBackup(context.Background(), testBackupEnvironment(runner), backupOptions{
		Dir: dir,
		DSN: "postgres://pos:pos_local_dev@localhost:5433/pos",
	})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want the pg_dump failure with its stderr", err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read the destination directory: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("destination has %d entries, want a removed partial dump", len(entries))
	}
}

func TestBackupHonorsThePgDumpOverride(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: "products.id:uuid:NO\n"},
		{stdout: countsOutput(allCounts(0))},
		{stdout: "DUMP"},
	}}

	_, err := createBackup(context.Background(), testBackupEnvironment(runner), backupOptions{
		Dir:    t.TempDir(),
		DSN:    "postgres://pos:pos_local_dev@localhost:5433/pos",
		PgDump: "/usr/bin/pg_dump",
	})
	if err != nil {
		t.Fatalf("createBackup: %v", err)
	}

	last := runner.calls[len(runner.calls)-1]
	if last.name != "/usr/bin/pg_dump" || !equalArgs(last.args, []string{"-Fc", "-U", "pos", "-d", "pos"}) {
		t.Fatalf("call = %s %v, want the PG_DUMP override", last.name, last.args)
	}
}

func TestRowCountsQueryCoversEveryContractTable(t *testing.T) {
	query := rowCountsQuery()
	for _, table := range contractTables {
		if !strings.Contains(query, "FROM "+table) {
			t.Fatalf("row-count query does not read %s", table)
		}
	}
	if len(sortedTables(contractTables)) != len(contractTables) {
		t.Fatalf("contractTables has duplicates")
	}
	for index := 1; index < len(sortedTables(contractTables)); index++ {
		if sortedTables(contractTables)[index-1] >= sortedTables(contractTables)[index] {
			t.Fatalf("contractTables is not sorted at %d", index)
		}
	}
}

func TestBackupSubcommandRequiresTheDirectoryFlag(t *testing.T) {
	err := run(context.Background(), []string{"backup"})
	if err == nil || !strings.Contains(err.Error(), "--dir is required") {
		t.Fatalf("err = %v, want the missing --dir error", err)
	}
}

func equalArgs(got, want []string) bool {
	return strings.Join(got, "\x00") == strings.Join(want, "\x00")
}

var _ commandRunner = (*fakeRunner)(nil)
