// Package config reúne la configuración del proceso: se lee del entorno, tiene valores
// por defecto que coinciden con docker-compose.yml y falla rápido con un mensaje que
// nombra la variable culpable cuando un valor falta o es inválido.
//
// Los nombres de las variables no están fijados en las fuentes (docs/BACKEND.md §2.5):
// este paquete fija los que Sprint 1 necesita. Las de los sprints siguientes
// (JWT_SECRET, REDIS_URL, OBJECT_STORAGE_*) se agregan aquí cuando esos módulos
// existan, no antes.
//
// Ninguna función imprime la configuración ni el DSN: lleva credenciales y no debe
// llegar a un log (§2.5). Para mostrarlo se usa RedactDSN.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultDatabaseURL apunta al contenedor local publicado por docker-compose.yml.
	// El puerto del host es 5433 porque el 5432 ya está ocupado en la máquina de
	// desarrollo (ver docker-compose.yml).
	DefaultDatabaseURL = "postgres://pos:pos_local_dev@localhost:5433/pos?sslmode=disable"

	defaultHTTPAddr = ":8080"
	defaultLogLevel = "info"
)

// Config es la configuración del proceso, ya validada.
type Config struct {
	// HTTPAddr es la dirección host:puerto del servidor HTTP único.
	HTTPAddr string
	// LogLevel es uno de debug, info, warn o error, en minúsculas.
	LogLevel string
	// DatabaseURL es el DSN de PostgreSQL. Nunca se registra en un log.
	DatabaseURL string
}

// Load lee el entorno, aplica los valores por defecto y valida el resultado.
//
// Un valor presente pero inválido es un error, nunca una caída silenciosa al valor por
// defecto: eso convertiría un error de tipeo en un servicio que arranca apuntando a otra
// base sin que nadie lo note.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    environmentOr("HTTP_ADDR", defaultHTTPAddr),
		LogLevel:    strings.ToLower(environmentOr("LOG_LEVEL", defaultLogLevel)),
		DatabaseURL: DatabaseURL(),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate comprueba cada valor y devuelve el primer problema encontrado, nombrando la
// variable de entorno que lo causó.
func (c Config) Validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL: unknown level %q (want one of debug, info, warn, error)", c.LogLevel)
	}

	_, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR: %q must be host:port, for example \":8080\": %w", c.HTTPAddr, err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("HTTP_ADDR: %q has an invalid port %q", c.HTTPAddr, port)
	}

	parsed, err := url.Parse(c.DatabaseURL)
	if err != nil {
		return fmt.Errorf("DATABASE_URL: not a valid URL: %w", err)
	}
	switch parsed.Scheme {
	case "postgres", "postgresql":
	default:
		return fmt.Errorf("DATABASE_URL: unsupported scheme %q (want postgres or postgresql)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("DATABASE_URL: %q has no host", RedactDSN(c.DatabaseURL))
	}

	return nil
}

// DatabaseURL devuelve el DSN de PostgreSQL: DATABASE_URL si está definida, y si no el
// valor por defecto de docker-compose.yml.
//
// Es la misma resolución que usa el runner de migraciones y la misma variable, para que
// cmd/api, cmd/migrate y los tests de integración no puedan apuntar a bases distintas.
func DatabaseURL() string {
	return environmentOr("DATABASE_URL", DefaultDatabaseURL)
}

// environmentOr devuelve el valor de la variable, o el fallback cuando no está definida
// o está vacía.
func environmentOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// DotEnv carga un archivo KEY=VALUE en el entorno. Lo que ya está exportado gana: el
// archivo es comodidad de desarrollo, nunca una forma de pisar la configuración real.
// Un archivo ausente no es un error, y las líneas vacías, comentadas o sin "=" se
// ignoran.
func DotEnv(path string) {
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

// RedactDSN oculta la contraseña antes de que un DSN llegue a un log o a una terminal.
// Un DSN que no se puede parsear devuelve un marcador, nunca el texto original: un fallo
// al parsear es justamente el caso en el que no se sabe qué parte era secreta.
func RedactDSN(dsn string) string {
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
