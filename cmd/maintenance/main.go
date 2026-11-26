// Command maintenance runs the two scheduled maintenance jobs of the POS SaaS database
// layer (Sprint 6, W6-DB-1 and W6-DB-2; docs/DATABASE.md §6.3-§6.7, §7.2).
//
//	go run ./cmd/maintenance retention     delete the idempotency keys older than 400 days
//	go run ./cmd/maintenance partitions    pre-create the six-month partition horizon and report health
//	go run ./cmd/maintenance partitions --remediate
//	                                       run the F5 remediation of a non-empty sales_default
//	go run ./cmd/maintenance admin-password --tenant <uuid>
//	                                       set the tenant master password (read from stdin)
//
// The two subcommands are separate on purpose: the operator schedules each one on its own
// and each is verifiable in isolation (decisión D4 de odd/tasks/sprint-6-offline-sync.md).
//
// Roles (F9 split, docs/DATABASE.md §6.3 and §7.2):
//
//   - `retention` connects with DATABASE_MAINTENANCE_URL — the `pos_maintenance` role, whose
//     only data DELETE is this one, over `idempotency_keys` and no other table;
//   - `partitions` connects with DATABASE_URL — the migration/owner role, because creating
//     partitions is DDL. The scheduler may be a low-privilege process; the DDL executor is a
//     short-lived high-privilege one.
//   - `admin-password` also connects with DATABASE_URL: writing the tenant master credential is
//     an administrative operation, and the HTTP surface has no path for it (W8-DB-1, decisión
//     D-B = B1 de odd/tasks/sprint-8-admin-hardening.md). The credential travels on stdin, never
//     as an argument, so it does not land in the shell history or the process table.
//
// `partitions` reports the `sales_default` health signal as a non-zero exit plus one
// structured log line (docs/DATABASE.md §6.7). It does NOT remediate by itself: remediation
// is an operational decision, never a silent side effect (decisión D6).
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/maintenance"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "maintenance: %v\n", err)
		os.Exit(1)
	}
}

// errSalesDefaultNotEmpty is the alert of docs/DATABASE.md §6.7: rows are stranded in the
// DEFAULT partition. It is an operational signal, not a crash.
var errSalesDefaultNotEmpty = errors.New("sales_default is not empty: the partitioning subsystem needs attention")

// run dispatches the subcommand. It is separated from main so a test can drive it without
// spawning a process.
func run(ctx context.Context, args []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	switch args[0] {
	case "retention":
		return runRetention(ctx, logger)
	case "partitions":
		return runPartitions(ctx, logger, args[1:])
	case "admin-password":
		return runAdminPassword(ctx, logger, args[1:], os.Stdin, setMasterPasswordWithMigrationRole)
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// usage prints the two documented invocations.
func usage() {
	fmt.Fprint(os.Stderr, `usage:
  maintenance retention       delete the idempotency keys completed more than 400 days ago
  maintenance partitions      pre-create the six-month partition horizon and report sales_default
  maintenance partitions --remediate
                              move the rows stranded in sales_default to their monthly partition
                              (one transaction), then recreate an empty sales_default
  maintenance admin-password --tenant <uuid>
                              set the tenant master password, read from standard input
                              (never from an argument: it would stay in the shell history)
`)
}

// runRetention executes the 400-day retention over the maintenance role.
func runRetention(ctx context.Context, logger *slog.Logger) error {
	db, err := open(ctx, config.MaintenanceDatabaseURL(), "pos_maintenance")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	result, err := maintenance.DeleteExpiredClaims(ctx, db)
	if err != nil {
		return err
	}

	logger.Info("idempotency key retention finished",
		"event", "retention.completed",
		"deleted", result.Deleted,
		"window_days", int(maintenance.RetentionWindow/(24*time.Hour)),
	)
	return nil
}

// runPartitions pre-creates the horizon under the migration role, reports the sales_default
// health signal, and remediates only when the operator asked for it.
func runPartitions(ctx context.Context, logger *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("partitions", flag.ContinueOnError)
	remediate := flags.Bool("remediate", false, "run the F5 remediation of a non-empty sales_default")
	if err := flags.Parse(args); err != nil {
		return err
	}

	db, err := open(ctx, config.DatabaseURL(), "migration")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	now := time.Now()

	if *remediate {
		result, err := maintenance.RemediateDefault(ctx, db, now)
		if err != nil {
			return err
		}
		logger.Info("sales_default remediation finished",
			"event", "partitions.remediated",
			"rows_moved", result.RowsMoved,
			"target_partition", result.TargetPartition,
		)
	} else {
		created, err := maintenance.EnsurePartitions(ctx, db, now)
		if err != nil {
			return err
		}
		logger.Info("partition horizon ensured",
			"event", "partitions.ensured",
			"partitions", created,
			"horizon_months", maintenance.HorizonMonths,
		)
	}

	// LA señal de salud del subsistema (docs/DATABASE.md §6.4, §6.7): una DEFAULT vacía es el
	// estado estable. Alerta = línea estructurada + salida no cero.
	stranded, err := maintenance.DefaultPartitionRows(ctx, db)
	if err != nil {
		return err
	}
	if stranded > 0 {
		logger.Error("sales_default has stranded rows",
			"event", "partitions.alert",
			"stranded_rows", stranded,
			"remediation", "run `maintenance partitions --remediate`",
		)
		return errSalesDefaultNotEmpty
	}

	logger.Info("sales_default is empty", "event", "partitions.healthy", "stranded_rows", stranded)
	return nil
}

// open connects to the given DSN and refuses to continue if the database is unreachable: a
// maintenance job that silently does nothing is worse than one that fails loudly.
// setMasterPasswordFunc is the injected write of the subcommand: the command owns the
// connection and this signature owns the operation, so the subcommand test needs no database.
type setMasterPasswordFunc func(ctx context.Context, tenantID uuid.UUID, password string) error

// runAdminPassword fija la contraseña maestra de UN tenant (W8-DB-1).
//
// El argumento lleva solo el tenant; la credencial entra por la entrada estándar. Ese es el
// punto entero del subcomando: una contraseña pasada por argumento queda en el historial del
// shell y en la tabla de procesos, donde cualquiera la lee.
//
// El registro estructurado nombra el tenant y jamás la contraseña (W8-BE-4): esa línea la lee
// el operador y puede terminar en un agregador de logs.
func runAdminPassword(ctx context.Context, logger *slog.Logger, args []string, stdin io.Reader, set setMasterPasswordFunc) error {
	flags := flag.NewFlagSet("admin-password", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenant := flags.String("tenant", "", "the tenant whose master password is set")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("admin-password: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("admin-password: unexpected argument %q", flags.Arg(0))
	}

	tenantID, err := uuid.Parse(strings.TrimSpace(*tenant))
	if err != nil {
		return fmt.Errorf("admin-password: --tenant must be the uuid of the tenant: %w", err)
	}

	password, err := readCredential(stdin)
	if err != nil {
		return fmt.Errorf("admin-password: %w", err)
	}

	if err := set(ctx, tenantID, password); err != nil {
		return fmt.Errorf("admin-password: %w", err)
	}

	logger.Info("the tenant master password was set", "event", "admin_password.set", "tenant_id", tenantID.String())
	return nil
}

// readCredential reads ONE line from standard input. Only the line ending is stripped: a
// trailing space is part of the owner's password, and trimming it would make a correct
// credential fail at login. An empty or blank credential is rejected here, before the write.
func readCredential(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", errors.New("the master password is read from standard input")
	}

	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the master password from standard input: %w", err)
	}
	if strings.TrimSpace(line) == "" {
		return "", errors.New("the master password cannot be empty")
	}
	// Solo se quita el salto de línea del canal: un espacio final es parte de la contraseña del
	// dueño y recortarlo en silencio haría que una credencial correcta fallara en el login.
	return strings.TrimRight(line, "\r\n"), nil
}

// setMasterPasswordWithMigrationRole opens the migration role for the duration of the write:
// the tenant credential is administrative data, not runtime data (W8-DB-1).
func setMasterPasswordWithMigrationRole(ctx context.Context, tenantID uuid.UUID, password string) error {
	db, err := open(ctx, config.DatabaseURL(), "migration")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	return identity.SetMasterPassword(ctx, db, tenantID, password)
}

func open(ctx context.Context, dsn, role string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open the %s connection: %w", role, err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect with the %s role: %w", role, err)
	}
	return db, nil
}
