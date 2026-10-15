// Command migrate applies the embedded SQL schema migrations of the POS SaaS
// database layer to PostgreSQL.
//
// It is a thin wrapper over github.com/pressly/goose/v3. The migration files live in
// migrations/*.sql, are embedded into this binary with embed.FS, and are applied in
// filename order. No external goose binary and no `make` are required — `make` is not
// assumed to exist on Windows.
//
//	go run ./cmd/migrate up        apply every pending migration
//	go run ./cmd/migrate status    list applied and pending migrations
//	go run ./cmd/migrate down      roll the schema back down to version 0
//	go run ./cmd/migrate down 20261015000004   roll back down to a specific version
//	go run ./cmd/migrate version   print the versions currently recorded as applied
//	go run ./cmd/migrate ping      connectivity smoke test: server version + table count
//
// `down` with no argument reverses the whole schema. That is intentionally different
// from the goose CLI, whose bare `down` reverses a single migration: this runner is
// used to bring a local database back to a clean slate, so "all the way down" is the
// useful default.
//
// The connection string comes from DATABASE_URL. A `.env` file in the working
// directory is loaded first when the variable is not already set; the fallback points
// at the container declared in docker-compose.yml (host port 5433).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"github.com/pressly/goose/v3"

	"github.com/AriasYarangaIam/CheckPOS/migrations"
)

// defaultDatabaseURL matches the local container published by docker-compose.yml.
// Host port 5433 because port 5432 is already occupied on the development machine.
const defaultDatabaseURL = "postgres://pos:pos_local_dev@localhost:5433/pos?sslmode=disable"

// migrationsDir is the path inside the embedded filesystem that goose reads.
const migrationsDir = "."

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	command, rest := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		usage()
		return nil
	case "up", "down", "status", "version", "ping":
	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}

	loadDotEnv(".env")

	// The SQL files are compiled into this binary; no filesystem lookup happens.
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	switch command {
	case "up":
		if len(rest) != 0 {
			return errors.New("up takes no arguments")
		}
		return goose.Up(db, migrationsDir)

	case "down":
		if len(rest) > 1 {
			return errors.New("down takes at most one argument: the target version")
		}
		target := int64(0) // 0 = reverse everything
		if len(rest) == 1 {
			target, err = strconv.ParseInt(rest[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid target version %q: %w", rest[0], err)
			}
		}
		return goose.DownTo(db, migrationsDir, target)

	case "status":
		if len(rest) != 0 {
			return errors.New("status takes no arguments")
		}
		return goose.Status(db, migrationsDir)

	case "version":
		if len(rest) != 0 {
			return errors.New("version takes no arguments")
		}
		return goose.Version(db, migrationsDir)

	case "ping":
		if len(rest) != 0 {
			return errors.New("ping takes no arguments")
		}
		return ping(db)
	}

	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: go run ./cmd/migrate <command> [args]

commands:
  up                        apply every pending migration
  down [version]            reverse the schema down to <version> (default 0: everything)
  status                    list applied and pending migrations
  version                   print the versions currently recorded as applied
  ping                      connectivity smoke test
`)
}

// openDatabase connects with DATABASE_URL and verifies the connection immediately, so
// a stopped container fails loudly instead of behind a later migration error.
func openDatabase() (*sql.DB, error) {
	dsn := databaseURL()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to %s: %w (is the container up? try `docker compose up -d`)", redactDSN(dsn), err)
	}

	return db, nil
}

// ping is the minimal connectivity check for the migration runner: it proves the
// driver, the credentials and the database are all reachable, and reports how many
// business tables the schema currently holds.
func ping(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var server string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&server); err != nil {
		return fmt.Errorf("server version: %w", err)
	}
	if i := strings.Index(server, " on "); i > 0 {
		server = server[:i]
	}
	fmt.Printf("connected to %s\n", server)

	var tables int
	if err := db.QueryRowContext(ctx, countBusinessTablesSQL).Scan(&tables); err != nil {
		return fmt.Errorf("count business tables: %w", err)
	}
	fmt.Printf("business tables in schema public: %d (excluding goose_db_version and partitions)\n", tables)

	return nil
}

// countBusinessTablesSQL counts the contract's 14 tables: it excludes the migration
// runner's own bookkeeping table and the child partitions of `sales`, which
// information_schema.tables also reports.
const countBusinessTablesSQL = `
SELECT count(*)
FROM information_schema.tables t
JOIN pg_class c
  ON c.relname = t.table_name
 AND c.relnamespace = to_regnamespace(t.table_schema)
WHERE t.table_schema = 'public'
  AND t.table_type = 'BASE TABLE'
  AND NOT c.relispartition
  AND t.table_name <> 'goose_db_version'`

func databaseURL() string {
	if v := strings.TrimSpace(os.Getenv("DATABASE_URL")); v != "" {
		return v
	}
	return defaultDatabaseURL
}

// loadDotEnv sets DATABASE_URL from a KEY=VALUE file when the environment does not
// already define it. Anything already exported wins. Missing file is not an error.
func loadDotEnv(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

// redactDSN hides the password before a connection string reaches a log or a terminal.
func redactDSN(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "<database url>"
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "****")
		}
	}
	return parsed.String()
}
