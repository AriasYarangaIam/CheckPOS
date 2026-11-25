package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// Este archivo implementa `maintenance backup` (Sprint 9, W9-DB-1): la instantánea diaria
// verificable del ledger. El dump es lógico (`pg_dump -Fc`) y viaja acompañado de un
// manifiesto que dice QUÉ se respaldó, cuánto pesa, qué huella tiene y cuántas filas había en
// cada tabla del contrato. Sin ese manifiesto el ensayo de restauración no tendría contra qué
// comparar y el respaldo sería una caja negra.
//
// Los binarios de PostgreSQL corren DENTRO del contenedor (`docker compose exec -T`), porque
// el host de desarrollo no tiene cliente instalado (decisión D1 de
// odd/tasks/sprint-9-field-pilot.md). El servicio y los binarios se pueden sustituir por
// variables de entorno para un host con cliente real.

// backupFormat identifica el formato del dump. `-Fc` es el formato custom de pg_dump: se puede
// restaurar con pg_restore y admite restauración selectiva.
const backupFormat = "pg_dump-custom"

// contractTables son las catorce tablas de docs/DATABASE.md §4. Es una lista cerrada a
// propósito: un manifiesto que solo cuente «algo» no prueba que el respaldo tenga el esquema
// que el contrato exige.
var contractTables = []string{
	"cash_close_counts",
	"cash_closes",
	"devices",
	"fiado_accounts",
	"fiado_movements",
	"global_barcodes",
	"global_products",
	"idempotency_keys",
	"products",
	"sale_lines",
	"sales",
	"stock_adjustments",
	"tenants",
	"users",
}

// commandRunner ejecuta un proceso externo. Existe para que las pruebas del subcomando no
// necesiten Docker: el subcomando se prueba con un runner falso que registra el argv y devuelve
// salidas conocidas. En producción es osCommandRunner.
type commandRunner interface {
	Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// osCommandRunner es el runner real.
type osCommandRunner struct{}

// Run ejecuta el proceso y devuelve su error tal cual: quien llama decide qué hacer con él.
func (osCommandRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// backupManifest es el contrato del archivo que acompaña a cada dump. Es JSON legible a
// propósito: el operador del piloto lo abre con un editor y el ensayo de restauración lo lee
// como entrada.
type backupManifest struct {
	Format            string           `json:"format"`
	Database          string           `json:"database"`
	Role              string           `json:"role"`
	DumpFile          string           `json:"dump_file"`
	CreatedAt         string           `json:"created_at"`
	Bytes             int64            `json:"bytes"`
	SHA256            string           `json:"sha256"`
	SchemaFingerprint string           `json:"schema_fingerprint"`
	RowCounts         map[string]int64 `json:"row_counts"`
}

// backupOptions son las entradas del subcomando ya resueltas.
type backupOptions struct {
	Dir     string
	DSN     string
	Service string
	PgDump  string
	Psql    string
}

// backupEnvironment agrupa las dependencias inyectables del subcomando.
type backupEnvironment struct {
	runner commandRunner
	now    func() time.Time
	getenv func(string) string
}

// defaultBackupEnvironment es el entorno real del proceso.
func defaultBackupEnvironment() backupEnvironment {
	return backupEnvironment{
		runner: osCommandRunner{},
		now:    time.Now,
		getenv: os.Getenv,
	}
}

// runBackup atiende `maintenance backup --dir <path>`.
func runBackup(ctx context.Context, logger *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", "", "directory where the dump and its manifest are written")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("backup: unexpected argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*dir) == "" {
		return errors.New("backup: --dir is required: the dump has to be written somewhere durable")
	}

	env := defaultBackupEnvironment()
	manifest, err := createBackup(ctx, env, backupOptions{
		Dir:     strings.TrimSpace(*dir),
		DSN:     config.DatabaseURL(),
		Service: environmentOr("BACKUP_DOCKER_SERVICE", "postgres", env.getenv),
		PgDump:  env.getenv("PG_DUMP"),
		Psql:    env.getenv("PSQL"),
	})
	if err != nil {
		return err
	}

	logger.Info("logical backup finished",
		"event", "backup.completed",
		"database", manifest.Database,
		"dump_file", manifest.DumpFile,
		"bytes", manifest.Bytes,
		"sha256", manifest.SHA256,
	)

	return json.NewEncoder(os.Stdout).Encode(manifest)
}

// createBackup hace el dump, calcula su huella y escribe el manifiesto. Es la parte que las
// pruebas ejercitan con un runner falso: no hay Docker ni base real en el camino.
func createBackup(ctx context.Context, env backupEnvironment, opts backupOptions) (backupManifest, error) {
	var manifest backupManifest

	target, err := parseBackupTarget(opts.DSN)
	if err != nil {
		return manifest, fmt.Errorf("backup: %w", err)
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return manifest, fmt.Errorf("backup: create the destination directory: %w", err)
	}

	base := env.now().UTC().Format("20060102T150405Z") + "-" + target.Database
	dumpPath := filepath.Join(opts.Dir, base+".dump")
	manifestPath := filepath.Join(opts.Dir, base+".manifest.json")

	// La huella y los conteos se leen ANTES del dump: si algo cambia entre la lectura y el
	// dump, el ensayo de restauración falla por diferencia, que es la dirección segura.
	fingerprint, err := schemaFingerprint(ctx, env.runner, opts, target)
	if err != nil {
		return manifest, err
	}
	counts, err := rowCounts(ctx, env.runner, opts, target)
	if err != nil {
		return manifest, err
	}

	file, err := os.Create(dumpPath)
	if err != nil {
		return manifest, fmt.Errorf("backup: create the dump file: %w", err)
	}
	// Un dump truncado es peor que ningún dump: si el proceso falla se borra el archivo
	// parcial, nunca se deja un respaldo a medias con nombre válido.
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(dumpPath)
		}
	}()

	var stderr bytes.Buffer
	if err := env.runner.Run(ctx, pgDumpCommand(opts), pgDumpArgs(opts, target), nil, file, &stderr); err != nil {
		_ = file.Close()
		return manifest, fmt.Errorf("backup: pg_dump failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := file.Close(); err != nil {
		return manifest, fmt.Errorf("backup: close the dump file: %w", err)
	}
	completed = true

	info, err := os.Stat(dumpPath)
	if err != nil {
		return manifest, fmt.Errorf("backup: stat the dump file: %w", err)
	}
	sum, err := fileSHA256(dumpPath)
	if err != nil {
		return manifest, err
	}

	manifest = backupManifest{
		Format:            backupFormat,
		Database:          target.Database,
		Role:              target.Role,
		DumpFile:          filepath.Base(dumpPath),
		CreatedAt:         env.now().UTC().Format(time.RFC3339),
		Bytes:             info.Size(),
		SHA256:            sum,
		SchemaFingerprint: fingerprint,
		RowCounts:         counts,
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return backupManifest{}, err
	}

	return manifest, nil
}

// backupTarget es lo que el DSN dice del destino: base y rol. El host y el puerto no se usan,
// porque los comandos corren dentro del contenedor que sirve esa misma base.
type backupTarget struct {
	Database string
	Role     string
}

// parseBackupTarget lee base y rol del DSN de migración. Un DSN sin base o sin usuario es un
// error de configuración, no un respaldo que se puede intentar.
func parseBackupTarget(dsn string) (backupTarget, error) {
	if strings.TrimSpace(dsn) == "" {
		return backupTarget{}, errors.New("DATABASE_URL is not set: the backup needs the owner DSN")
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return backupTarget{}, fmt.Errorf("DATABASE_URL is not a usable PostgreSQL DSN: %w", err)
	}
	if strings.TrimSpace(cfg.Database) == "" {
		return backupTarget{}, errors.New("DATABASE_URL does not name a database")
	}
	if strings.TrimSpace(cfg.User) == "" {
		return backupTarget{}, errors.New("DATABASE_URL does not name a role")
	}

	return backupTarget{Database: cfg.Database, Role: cfg.User}, nil
}

// pgDumpCommand elige el binario: el del PATH cuando PG_DUMP lo fija, o docker compose exec.
func pgDumpCommand(opts backupOptions) string {
	if strings.TrimSpace(opts.PgDump) != "" {
		return strings.TrimSpace(opts.PgDump)
	}
	return "docker"
}

// pgDumpArgs arma los argumentos del dump.
func pgDumpArgs(opts backupOptions, target backupTarget) []string {
	pgDump := []string{"pg_dump", "-Fc", "-U", target.Role, "-d", target.Database}
	if strings.TrimSpace(opts.PgDump) != "" {
		return pgDump[1:]
	}
	return append([]string{"compose", "exec", "-T", opts.Service}, pgDump...)
}

// psqlCommand/psqlArgs resuelven la misma elección para las consultas de lectura.
func (opts backupOptions) psqlCommand() string {
	if strings.TrimSpace(opts.Psql) != "" {
		return strings.TrimSpace(opts.Psql)
	}
	return "docker"
}

func (opts backupOptions) psqlArgs(target backupTarget, database, statement string) []string {
	psql := []string{"psql", "-At", "-F", "|", "-U", target.Role, "-d", database, "-c", statement}
	if strings.TrimSpace(opts.Psql) != "" {
		return psql[1:]
	}
	return append([]string{"compose", "exec", "-T", opts.Service}, psql...)
}

// schemaFingerprint devuelve un sha256 estable de tablas y columnas del esquema público. Se usa
// en lugar de un número de migración porque el runner aplica archivos idempotentes y no
// mantiene una tabla de versiones: la huella describe el esquema REAL, que es lo que el
// ensayo de restauración debe reproducir.
func schemaFingerprint(ctx context.Context, runner commandRunner, opts backupOptions, target backupTarget) (string, error) {
	statement := `SELECT table_name || '.' || column_name || ':' || data_type || ':' || is_nullable
FROM information_schema.columns
WHERE table_schema = 'public'
ORDER BY table_name, ordinal_position`
	out, err := commandOutput(ctx, runner, opts.psqlCommand(), opts.psqlArgs(target, target.Database, statement))
	if err != nil {
		return "", fmt.Errorf("backup: read the schema fingerprint: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(out)))
	return hex.EncodeToString(sum[:]), nil
}

// rowCountsQuery cuenta las catorce tablas del contrato en UNA sola consulta.
func rowCountsQuery() string {
	parts := make([]string, 0, len(contractTables))
	for _, table := range contractTables {
		parts = append(parts, fmt.Sprintf("SELECT '%s', count(*) FROM %s", table, table))
	}
	return strings.Join(parts, " UNION ALL ") + " ORDER BY 1"
}

// rowCounts lee los conteos de las catorce tablas. Un conteo ilegible es un error: el
// manifiesto incompleto no se escribe.
func rowCounts(ctx context.Context, runner commandRunner, opts backupOptions, target backupTarget) (map[string]int64, error) {
	out, err := commandOutput(ctx, runner, opts.psqlCommand(), opts.psqlArgs(target, target.Database, rowCountsQuery()))
	if err != nil {
		return nil, fmt.Errorf("backup: read the row counts: %w", err)
	}

	counts := make(map[string]int64, len(contractTables))
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, raw, ok := strings.Cut(line, "|")
		if !ok {
			return nil, fmt.Errorf("backup: unexpected row-count line %q", line)
		}
		value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("backup: row count of %s is not a number: %w", name, err)
		}
		counts[strings.TrimSpace(name)] = value
	}

	for _, table := range contractTables {
		if _, ok := counts[table]; !ok {
			return nil, fmt.Errorf("backup: the row count of %s is missing", table)
		}
	}
	return counts, nil
}

// commandOutput ejecuta un comando de lectura y devuelve su salida estándar.
func commandOutput(ctx context.Context, runner commandRunner, name string, args []string) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := runner.Run(ctx, name, args, nil, &stdout, &stderr); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// fileSHA256 calcula la huella del dump en streaming: un ledger de meses no se carga en
// memoria para hashearlo.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("backup: open the dump to hash it: %w", err)
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("backup: hash the dump: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// writeManifest escribe el manifiesto de forma atómica: se escribe un temporal y se renombra,
// así un corte de energía no deja un JSON a medias junto a un dump válido.
func writeManifest(path string, manifest backupManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: encode the manifest: %w", err)
	}
	payload = append(payload, '\n')

	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return fmt.Errorf("backup: write the manifest: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("backup: publish the manifest: %w", err)
	}
	return nil
}

// readManifest lee el manifiesto del ensayo de restauración.
func readManifest(path string) (backupManifest, error) {
	var manifest backupManifest

	payload, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("read the manifest %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return manifest, fmt.Errorf("the manifest %s is not valid JSON: %w", filepath.Base(path), err)
	}
	if manifest.DumpFile == "" || manifest.SHA256 == "" || manifest.SchemaFingerprint == "" || len(manifest.RowCounts) == 0 {
		return manifest, fmt.Errorf("the manifest %s is incomplete", filepath.Base(path))
	}
	return manifest, nil
}

// environmentOr devuelve la variable o el respaldo. Recibe el lector para que las pruebas no
// dependan del entorno del proceso.
func environmentOr(key, fallback string, getenv func(string) string) string {
	if getenv != nil {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}

// sortedTables existe para que un test pueda afirmar que la lista del contrato está ordenada y
// sin duplicados: dos conteos de la misma tabla esconderían una tabla ausente.
func sortedTables(tables []string) []string {
	copied := append([]string(nil), tables...)
	sort.Strings(copied)
	return copied
}
