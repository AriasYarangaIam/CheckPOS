package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pruebas del subcomando `maintenance restore-verify` (Sprint 9, W9-DB-1). Lo que se afirma es
// la disciplina del ensayo: la base desechable se borra SIEMPRE, el dump alterado no se
// restaura, un conteo distinto falla el ensayo y un nombre fuera de `pos_restore_*` se rechaza
// antes de tocar el clúster.

// fixtureBackup escribe un dump y su manifiesto coherentes en un directorio temporal y devuelve
// la ruta del manifiesto.
func fixtureBackup(t *testing.T, payload string, counts map[string]int64) (string, backupManifest) {
	t.Helper()

	dir := t.TempDir()
	dumpName := "20261125T120000Z-pos.dump"
	if err := os.WriteFile(filepath.Join(dir, dumpName), []byte(payload), 0o600); err != nil {
		t.Fatalf("write the dump fixture: %v", err)
	}
	sum := sha256.Sum256([]byte(payload))

	manifest := backupManifest{
		Format:            backupFormat,
		Database:          "pos",
		Role:              "pos",
		DumpFile:          dumpName,
		CreatedAt:         "2026-11-25T12:00:00Z",
		Bytes:             int64(len(payload)),
		SHA256:            hex.EncodeToString(sum[:]),
		SchemaFingerprint: fingerprintOf("schema-fingerprint"),
		RowCounts:         counts,
	}
	manifestPath := filepath.Join(dir, dumpName+".manifest.json")
	if err := writeManifest(manifestPath, manifest); err != nil {
		t.Fatalf("write the manifest fixture: %v", err)
	}
	return manifestPath, manifest
}

// fingerprintOf reproduce la huella que schemaFingerprint calcula sobre la salida de psql.
func fingerprintOf(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func TestRestoreVerifySucceedsAndDropsTheScratchDatabase(t *testing.T) {
	manifestPath, manifest := fixtureBackup(t, "DUMP", allCounts(2))
	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: ""},                     // CREATE DATABASE
		{stdout: ""},                     // pg_restore
		{stdout: "schema-fingerprint\n"}, // fingerprint
		{stdout: countsOutput(manifest.RowCounts)}, // row counts
		{stdout: ""}, // DROP DATABASE
	}}

	summary, err := verifyRestore(context.Background(), testBackupEnvironment(runner), restoreOptions{
		ManifestPath: manifestPath,
		Scratch:      "pos_restore_drill",
		DSN:          "postgres://pos:pos_local_dev@localhost:5433/pos",
		Service:      "postgres",
	})
	if err != nil {
		t.Fatalf("verifyRestore: %v", err)
	}
	if !summary.Restored || !summary.SchemaMatch || !summary.RowCountMatch || !summary.Dropped {
		t.Fatalf("summary = %+v, want a complete successful drill", summary)
	}
	if summary.TablesVerified != len(contractTables) {
		t.Fatalf("TablesVerified = %d, want %d", summary.TablesVerified, len(contractTables))
	}

	last := runner.calls[len(runner.calls)-1]
	if !strings.Contains(strings.Join(last.args, " "), `DROP DATABASE IF EXISTS "pos_restore_drill" WITH (FORCE)`) {
		t.Fatalf("last call = %v, want the scratch database drop", last.args)
	}
	if len(runner.stdinSeen) == 0 || runner.stdinSeen[0] != "DUMP" {
		t.Fatalf("pg_restore stdin = %v, want the dump payload", runner.stdinSeen)
	}
}

func TestRestoreVerifyDropsTheScratchDatabaseWhenRestoreFails(t *testing.T) {
	manifestPath, _ := fixtureBackup(t, "DUMP", allCounts(0))
	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: ""}, // CREATE
		{stderr: "corrupt input", err: errors.New("exit status 1")}, // pg_restore
		{stdout: ""}, // DROP
	}}

	summary, err := verifyRestore(context.Background(), testBackupEnvironment(runner), restoreOptions{
		ManifestPath: manifestPath,
		Scratch:      "pos_restore_drill",
		DSN:          "postgres://pos:pos_local_dev@localhost:5433/pos",
		Service:      "postgres",
	})
	if err == nil || !strings.Contains(err.Error(), "corrupt input") {
		t.Fatalf("err = %v, want the pg_restore failure", err)
	}
	if !summary.Dropped {
		t.Fatalf("summary = %+v, want the scratch database dropped even after a failure", summary)
	}
	if !strings.Contains(strings.Join(runner.calls[len(runner.calls)-1].args, " "), "DROP DATABASE") {
		t.Fatalf("calls = %+v, want a drop after the failure", runner.calls)
	}
}

func TestRestoreVerifyFailsOnARowCountMismatch(t *testing.T) {
	manifestPath, _ := fixtureBackup(t, "DUMP", allCounts(2))
	restored := allCounts(2)
	restored["sales"] = 7

	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: ""},
		{stdout: ""},
		{stdout: "schema-fingerprint\n"},
		{stdout: countsOutput(restored)},
		{stdout: ""},
	}}

	summary, err := verifyRestore(context.Background(), testBackupEnvironment(runner), restoreOptions{
		ManifestPath: manifestPath,
		Scratch:      "pos_restore_drill",
		DSN:          "postgres://pos:pos_local_dev@localhost:5433/pos",
		Service:      "postgres",
	})
	if err == nil {
		t.Fatal("verifyRestore succeeded, want a mismatch error")
	}
	if summary.RowCountMatch {
		t.Fatalf("summary = %+v, want RowCountMatch false", summary)
	}
	if summary.Mismatches["sales"] == "" {
		t.Fatalf("summary = %+v, want the sales mismatch recorded", summary)
	}
	if !summary.Dropped {
		t.Fatalf("summary = %+v, want the scratch database dropped", summary)
	}
}

func TestRestoreVerifyFailsOnASchemaMismatch(t *testing.T) {
	manifestPath, _ := fixtureBackup(t, "DUMP", allCounts(1))
	runner := &fakeRunner{responses: []fakeResponse{
		{stdout: ""},
		{stdout: ""},
		{stdout: "another-schema\n"},
		{stdout: countsOutput(allCounts(1))},
		{stdout: ""},
	}}

	summary, err := verifyRestore(context.Background(), testBackupEnvironment(runner), restoreOptions{
		ManifestPath: manifestPath,
		Scratch:      "pos_restore_drill",
		DSN:          "postgres://pos:pos_local_dev@localhost:5433/pos",
		Service:      "postgres",
	})
	if err == nil || summary.SchemaMatch {
		t.Fatalf("err = %v, summary = %+v, want a schema mismatch", err, summary)
	}
}

func TestRestoreVerifyRejectsAScratchNameOutsideThePrefix(t *testing.T) {
	manifestPath, _ := fixtureBackup(t, "DUMP", allCounts(0))
	runner := &fakeRunner{}

	_, err := verifyRestore(context.Background(), testBackupEnvironment(runner), restoreOptions{
		ManifestPath: manifestPath,
		Scratch:      "pos",
		DSN:          "postgres://pos:pos_local_dev@localhost:5433/pos",
	})
	if err == nil || !strings.Contains(err.Error(), "pos_restore_") {
		t.Fatalf("err = %v, want the scratch-name rejection", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %+v, want no database touched", runner.calls)
	}
}

func TestRestoreVerifyRejectsATamperedDump(t *testing.T) {
	manifestPath, manifest := fixtureBackup(t, "DUMP", allCounts(0))
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifestPath), manifest.DumpFile), []byte("TAMPERED"), 0o600); err != nil {
		t.Fatalf("tamper the dump: %v", err)
	}
	runner := &fakeRunner{}

	_, err := verifyRestore(context.Background(), testBackupEnvironment(runner), restoreOptions{
		ManifestPath: manifestPath,
		Scratch:      "pos_restore_drill",
		DSN:          "postgres://pos:pos_local_dev@localhost:5433/pos",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match the manifest") {
		t.Fatalf("err = %v, want the sha256 mismatch", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %+v, want no database touched", runner.calls)
	}
}

func TestValidateScratchName(t *testing.T) {
	valid := []string{"pos_restore_a", "pos_restore_drill_2026_11_25"}
	for _, name := range valid {
		if err := validateScratchName(name); err != nil {
			t.Fatalf("validateScratchName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{"", "pos", "pos_restore_", "pos_restore_UPPER", "pos_restore_a;DROP DATABASE pos"}
	for _, name := range invalid {
		if err := validateScratchName(name); err == nil {
			t.Fatalf("validateScratchName(%q) = nil, want a rejection", name)
		}
	}
}

func TestRestoreVerifyRequiresTheManifestFlag(t *testing.T) {
	err := run(context.Background(), []string{"restore-verify", "--scratch", "pos_restore_drill"})
	if err == nil || !strings.Contains(err.Error(), "read the manifest") {
		t.Fatalf("err = %v, want the missing manifest error", err)
	}
}
