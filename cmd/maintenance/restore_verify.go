package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// Este archivo implementa `maintenance restore-verify` (Sprint 9, W9-DB-1): el ensayo de
// restauración que el plan exige «ejecutado y registrado», no la mera existencia del respaldo
// (SPRINTS.md Sprint 9, W9-DB-1).
//
// El ensayo NO toca la base del servidor. Crea una base desechable `pos_restore_*`, restaura
// ahí el dump, compara la huella del esquema y los conteos contra el manifiesto, y la borra
// siempre —también cuando algo falla—. Una restauración que se queda a medias en el mismo
// clúster es una bomba de tiempo operativa.

// scratchNamePattern acota el nombre de la base desechable. El nombre se interpola en DDL
// (`CREATE DATABASE`), así que la validación no es cosmética: sin ella, el argumento sería una
// vía de inyección SQL en el rol dueño.
var scratchNamePattern = regexp.MustCompile(`^pos_restore_[a-z0-9_]+$`)

// restoreOptions son las entradas del subcomando ya resueltas.
type restoreOptions struct {
	ManifestPath string
	Scratch      string
	DSN          string
	Service      string
	PgRestore    string
	Psql         string
}

// restoreSummary es el registro del ensayo. Se imprime como JSON para que la bitácora del
// piloto lo adjunte tal cual.
type restoreSummary struct {
	DumpFile        string            `json:"dump_file"`
	ScratchDatabase string            `json:"scratch_database"`
	Restored        bool              `json:"restored"`
	SchemaMatch     bool              `json:"schema_match"`
	RowCountMatch   bool              `json:"row_count_match"`
	TablesVerified  int               `json:"tables_verified"`
	Mismatches      map[string]string `json:"mismatches,omitempty"`
	Dropped         bool              `json:"dropped"`
}

// runRestoreVerify atiende `maintenance restore-verify --file <manifest> --scratch <name>`.
func runRestoreVerify(ctx context.Context, logger *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("restore-verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "path to the .manifest.json written by `maintenance backup`")
	scratch := flags.String("scratch", "", "throwaway database name, must start with pos_restore_")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("restore-verify: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("restore-verify: unexpected argument %q", flags.Arg(0))
	}

	env := defaultBackupEnvironment()
	summary, err := verifyRestore(ctx, env, restoreOptions{
		ManifestPath: strings.TrimSpace(*file),
		Scratch:      strings.TrimSpace(*scratch),
		DSN:          config.DatabaseURL(),
		Service:      environmentOr("BACKUP_DOCKER_SERVICE", "postgres", env.getenv),
		PgRestore:    env.getenv("PG_RESTORE"),
		Psql:         env.getenv("PSQL"),
	})
	encodeErr := json.NewEncoder(os.Stdout).Encode(summary)
	if err != nil {
		return err
	}
	if encodeErr != nil {
		return fmt.Errorf("restore-verify: write the summary: %w", encodeErr)
	}

	logger.Info("restore drill finished",
		"event", "restore_verify.completed",
		"dump_file", summary.DumpFile,
		"scratch_database", summary.ScratchDatabase,
		"tables_verified", summary.TablesVerified,
	)
	return nil
}

// verifyRestore ejecuta el ensayo completo. Devuelve el resumen SIEMPRE, también cuando falla:
// el registro del intento es parte de la evidencia del piloto. Los resultados son nombrados
// para que la limpieza del defer quede reflejada en lo que se devuelve.
func verifyRestore(ctx context.Context, env backupEnvironment, opts restoreOptions) (summary restoreSummary, err error) {
	summary = restoreSummary{ScratchDatabase: opts.Scratch, Mismatches: map[string]string{}}

	manifest, err := readManifest(opts.ManifestPath)
	if err != nil {
		return summary, fmt.Errorf("restore-verify: %w", err)
	}
	summary.DumpFile = manifest.DumpFile

	if err := validateScratchName(opts.Scratch); err != nil {
		return summary, fmt.Errorf("restore-verify: %w", err)
	}

	target, err := parseBackupTarget(opts.DSN)
	if err != nil {
		return summary, fmt.Errorf("restore-verify: %w", err)
	}

	dumpPath := filepath.Join(filepath.Dir(opts.ManifestPath), manifest.DumpFile)
	sum, err := fileSHA256(dumpPath)
	if err != nil {
		return summary, fmt.Errorf("restore-verify: %w", err)
	}
	if sum != manifest.SHA256 {
		return summary, fmt.Errorf("restore-verify: the dump sha256 %s does not match the manifest %s", sum, manifest.SHA256)
	}

	restoreOpts := backupOptions{DSN: opts.DSN, Service: opts.Service, Psql: opts.Psql}
	if err := createScratchDatabase(ctx, env.runner, restoreOpts, target, opts.Scratch); err != nil {
		return summary, err
	}
	// La limpieza va en defer y se registra: si el borrado falla, el ensayo falla. Dejar una
	// base pos_restore_* viva no es un detalle cosmético.
	defer func() {
		if dropErr := dropScratchDatabase(ctx, env.runner, restoreOpts, target, opts.Scratch); dropErr != nil {
			summary.Dropped = false
			summary.Mismatches["cleanup"] = dropErr.Error()
			if err == nil {
				err = dropErr
			}
			return
		}
		summary.Dropped = true
	}()

	if err := pgRestore(ctx, env.runner, opts, target, dumpPath); err != nil {
		return summary, err
	}
	summary.Restored = true

	fingerprint, err := schemaFingerprint(ctx, env.runner, restoreOpts, backupTarget{Database: opts.Scratch, Role: target.Role})
	if err != nil {
		return summary, err
	}
	summary.SchemaMatch = fingerprint == manifest.SchemaFingerprint
	if !summary.SchemaMatch {
		summary.Mismatches["schema_fingerprint"] = "the restored schema differs from the manifest"
	}

	counts, err := rowCounts(ctx, env.runner, restoreOpts, backupTarget{Database: opts.Scratch, Role: target.Role})
	if err != nil {
		return summary, err
	}
	summary.RowCountMatch = true
	for table, expected := range manifest.RowCounts {
		actual, ok := counts[table]
		if !ok {
			summary.Mismatches[table] = "missing in the restored database"
			summary.RowCountMatch = false
			continue
		}
		if actual != expected {
			summary.Mismatches[table] = fmt.Sprintf("manifest %d, restored %d", expected, actual)
			summary.RowCountMatch = false
			continue
		}
		summary.TablesVerified++
	}

	if !summary.SchemaMatch || !summary.RowCountMatch {
		return summary, errors.New("restore-verify: the restored database does not match the manifest")
	}
	return summary, nil
}

// validateScratchName rechaza cualquier nombre que no sea una base pos_restore_*.
func validateScratchName(name string) error {
	if !scratchNamePattern.MatchString(name) {
		return fmt.Errorf("--scratch must match %s, got %q", scratchNamePattern.String(), name)
	}
	return nil
}

// createScratchDatabase crea la base desechable desde la base de mantenimiento `postgres`.
func createScratchDatabase(ctx context.Context, runner commandRunner, opts backupOptions, target backupTarget, scratch string) error {
	if err := validateScratchName(scratch); err != nil {
		return err
	}
	statement := fmt.Sprintf(`CREATE DATABASE "%s"`, scratch)
	if _, err := commandOutput(ctx, runner, opts.psqlCommand(), opts.psqlArgs(target, "postgres", statement)); err != nil {
		return fmt.Errorf("restore-verify: create the scratch database: %w", err)
	}
	return nil
}

// dropScratchDatabase borra la base desechable con FORCE: una conexión colgada no puede
// impedir la limpieza.
func dropScratchDatabase(ctx context.Context, runner commandRunner, opts backupOptions, target backupTarget, scratch string) error {
	if err := validateScratchName(scratch); err != nil {
		return err
	}
	statement := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, scratch)
	if _, err := commandOutput(ctx, runner, opts.psqlCommand(), opts.psqlArgs(target, "postgres", statement)); err != nil {
		return fmt.Errorf("restore-verify: drop the scratch database: %w", err)
	}
	return nil
}

// pgRestore restaura el dump en la base desechable leyéndolo por la entrada estándar.
func pgRestore(ctx context.Context, runner commandRunner, opts restoreOptions, target backupTarget, dumpPath string) error {
	file, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("restore-verify: open the dump: %w", err)
	}
	defer func() { _ = file.Close() }()

	args := []string{"pg_restore", "-U", target.Role, "-d", opts.Scratch, "--no-owner", "--no-privileges"}
	name := "docker"
	if strings.TrimSpace(opts.PgRestore) != "" {
		name = strings.TrimSpace(opts.PgRestore)
		args = args[1:]
	} else {
		args = append([]string{"compose", "exec", "-T", opts.Service}, args...)
	}

	var stderr bytes.Buffer
	if err := runner.Run(ctx, name, args, file, io.Discard, &stderr); err != nil {
		return fmt.Errorf("restore-verify: pg_restore failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
