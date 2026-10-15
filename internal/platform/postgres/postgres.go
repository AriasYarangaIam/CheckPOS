// Package postgres construye el pool de conexiones del proceso, responde el sondeo de
// preparación y aplica las migraciones embebidas.
//
// Es el único lugar que sabe cómo se conecta el proceso y cómo se migra la base, para
// que cmd/api, cmd/migrate y los tests de integración no puedan divergir: un solo DSN,
// un solo juego de migraciones y una sola forma de aplicarlas.
//
// Queda deliberadamente fuera de este paquete, hasta el sprint que lo necesite:
//
//   - el ayudante de transacción que fija el GUC del tenant con SET LOCAL. La seguridad
//     frente a connection poolers vive ahí, no en el tamaño del pool (docs/BACKEND.md
//     §7.7), y llega con el RLS de Sprint 2.
//   - el rol por privilegio de la matriz de concesiones (docs/DATABASE.md §5.5): hasta
//     que existan los roles pos_*, el proceso se conecta con el rol de desarrollo.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" de database/sql, usado por goose
	"github.com/pressly/goose/v3"

	"github.com/AriasYarangaIam/CheckPOS/migrations"
)

// migrationsDir es la ruta dentro del sistema de archivos embebido que lee goose. El
// embed.FS de migrations está enraizado en ese directorio, así que es ".".
const migrationsDir = "."

// SetupGoose deja a goose apuntando a las migraciones embebidas y al dialecto de
// PostgreSQL. cmd/migrate y ApplyMigrations comparten esta preparación: hay una sola
// forma de aplicar el esquema, no dos.
func SetupGoose() error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	return nil
}

// ApplyMigrations aplica contra dsn toda migración pendiente de las que están embebidas
// en el binario.
//
// Verifica la conexión antes de migrar, para que un contenedor detenido falle con un
// mensaje accionable y no detrás de un error de migración.
func ApplyMigrations(ctx context.Context, dsn string) error {
	if err := SetupGoose(); err != nil {
		return err
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to the database: %w (is the container up? try `docker compose up -d`)", err)
	}

	if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// New construye el pool de conexiones sin esperar a que PostgreSQL responda.
//
// El proceso arranca aunque la base esté caída, y GET /readyz lo informa: la vida y la
// preparación son preguntas distintas. Un DSN inválido sí falla aquí, porque un error de
// configuración no se arregla solo.
func New(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse the connection string: %w", err)
	}

	// El tamaño del pool queda en los valores por defecto de pgxpool: lo que la
	// seguridad exige no es un número sino el SET LOCAL por transacción (§7.7), y ese
	// llega con el RLS. Fijar un tamaño ahora sería inventar una medición que no existe.
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open the connection pool: %w", err)
	}
	return pool, nil
}

// WithDatabase devuelve el mismo DSN apuntando a otra base del mismo servidor.
//
// Se usa para alcanzar la base de mantenimiento desde la que se crea y se elimina una
// base desechable, que es como los tests de integración evitan tocar la base de
// desarrollo.
func WithDatabase(dsn, database string) (string, error) {
	if strings.TrimSpace(database) == "" {
		return "", errors.New("the database name is empty")
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse the connection string: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("the connection string is not a URL with a host")
	}

	parsed.Path = "/" + database
	return parsed.String(), nil
}
