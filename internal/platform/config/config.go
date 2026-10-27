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
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultDatabaseURL apunta al contenedor local publicado por docker-compose.yml.
	// El puerto del host es 5433 porque el 5432 ya está ocupado en la máquina de
	// desarrollo (ver docker-compose.yml).
	DefaultDatabaseURL = "postgres://pos:pos_local_dev@localhost:5433/pos?sslmode=disable"

	// DefaultAppDatabaseURL es el DSN del rol de aplicación `pos_app`: el único rol de
	// runtime con acceso a las tablas de negocio, siempre bajo RLS de tenant
	// (docs/DATABASE.md §5.5). La contraseña es el valor de desarrollo local que ya
	// existe en el repositorio (docker-compose.yml); producción lo rota fuera de banda.
	DefaultAppDatabaseURL = "postgres://pos_app:pos_local_dev@localhost:5433/pos?sslmode=disable"

	// DefaultAuthDatabaseURL es el DSN del rol pre-auth `pos_auth`: existe para una sola
	// forma de consulta, la búsqueda de login sobre `users` (docs/DATABASE.md §5.4).
	DefaultAuthDatabaseURL = "postgres://pos_auth:pos_local_dev@localhost:5433/pos?sslmode=disable"

	// DefaultPlatformDatabaseURL es el DSN del rol de catálogo global `pos_platform`
	// (Sprint 3, W3-BE-2): su única consulta es la mediación del GTIN sobre las tablas
	// globales, que viven fuera del RLS de tenant y a las que pos_app no puede entrar
	// (docs/DATABASE.md §5.5, §5.6, §7.7).
	DefaultPlatformDatabaseURL = "postgres://pos_platform:pos_local_dev@localhost:5433/pos?sslmode=disable"

	// DefaultMaintenanceDatabaseURL es el DSN del rol de mantenimiento `pos_maintenance`:
	// la retención de 400 días de `idempotency_keys` (Sprint 6, W6-DB-1, docs/DATABASE.md
	// §7.2) es su único DELETE de la matriz de concesiones (§5.5). La contraseña es el
	// valor de desarrollo local que ya existe en el repositorio; producción la rota fuera
	// de banda.
	DefaultMaintenanceDatabaseURL = "postgres://pos_maintenance:pos_local_dev@localhost:5433/pos?sslmode=disable"

	// DefaultObjectStorageDir es el directorio del almacenamiento de objetos de las fotos
	// (Sprint 3, W3-BE-3, docs/BACKEND.md §10.3). Es relativo al directorio de trabajo del
	// proceso: un almacenamiento de objetos real (SDK externo) lo pisará con su propia
	// configuración, sin cambiar la forma de la URL que este módulo devuelve.
	DefaultObjectStorageDir = "data/photos"

	// DefaultObjectStorageURL es la base pública con la que el store local arma las URLs
	// que devuelve: es la base con la que el cliente referencia la foto después en
	// photo_url dentro del cuerpo de POST /products (§10.3).
	DefaultObjectStorageURL = "http://localhost:8080"

	defaultHTTPAddr = ":8080"
	defaultLogLevel = "info"

	// DefaultJWTTTL es la vida por defecto del access token. docs/BACKEND.md §2.5 la
	// propone sin fijarla, así que la elección se documenta: doce horas cubren un turno
	// completo de mostrador sin obligar a un re-login en medio de la jornada. La ventana
	// que habilita el mostrador sin red es la del dispositivo
	// (`devices.offline_session_valid_until`, al menos 7 días) y no esta (§5.2).
	DefaultJWTTTL = 12 * time.Hour

	// minJWTSecretBytes es el piso de la clave de firma: docs/BACKEND.md §2.5 pide 32
	// bytes o más para HS256. Una clave más corta que el tamaño de salida del hash es
	// adivinable por fuerza bruta y vuelve falsificable cualquier token.
	minJWTSecretBytes = 32
)

// Config es la configuración del proceso, ya validada.
type Config struct {
	// HTTPAddr es la dirección host:puerto del servidor HTTP único.
	HTTPAddr string
	// LogLevel es uno de debug, info, warn o error, en minúsculas.
	LogLevel string
	// DatabaseURL es el DSN de PostgreSQL del rol de migración/dueño. Nunca se registra
	// en un log.
	DatabaseURL string
	// AppDatabaseURL es el DSN del rol de aplicación `pos_app` (datos de negocio bajo
	// RLS de tenant). Nunca se registra en un log.
	AppDatabaseURL string
	// AuthDatabaseURL es el DSN del rol pre-auth `pos_auth` (lectura de `users` para el
	// login). Nunca se registra en un log.
	AuthDatabaseURL string
	// PlatformDatabaseURL es el DSN del rol de catálogo global `pos_platform` (mediación
	// read-only del GTIN, W3-BE-2). Nunca se registra en un log.
	PlatformDatabaseURL string
	// ObjectStorageDir es el directorio del almacenamiento de objetos de las fotos
	// (W3-BE-3). No lleva credenciales, así que su vacío es un defecto de arrranque que la
	// validación del proceso no niega (docs/BACKEND.md §10.3).
	ObjectStorageDir string
	// ObjectStorageURL es la base pública de las URLs de foto que devuelve el upload
	// (W3-BE-3): el cliente la referencia después en photo_url (docs/BACKEND.md §10.3).
	ObjectStorageURL string
	// JWTSecret firma y verifica el access token. No tiene valor por defecto a propósito:
	// una clave commiteada es pública, y con ella cualquiera firma un token de cualquier
	// tenant (docs/BACKEND.md §2.5). Nunca se registra en un log.
	JWTSecret string
	// JWTTTL es la vida del access token. No es la ventana offline del PIN, que vive en
	// `devices.offline_session_valid_until` (docs/BACKEND.md §2.5, §5.2).
	JWTTTL time.Duration
}

// Load lee el entorno, aplica los valores por defecto y valida el resultado.
//
// Un valor presente pero inválido es un error, nunca una caída silenciosa al valor por
// defecto: eso convertiría un error de tipeo en un servicio que arranca apuntando a otra
// base sin que nadie lo note.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        environmentOr("HTTP_ADDR", defaultHTTPAddr),
		LogLevel:        strings.ToLower(environmentOr("LOG_LEVEL", defaultLogLevel)),
		DatabaseURL:          DatabaseURL(),
		AppDatabaseURL:       AppDatabaseURL(),
		AuthDatabaseURL:      AuthDatabaseURL(),
		PlatformDatabaseURL:  PlatformDatabaseURL(),
		ObjectStorageDir:     ObjectStorageDir(),
		ObjectStorageURL:     ObjectStorageURL(),
		// JWT_SECRET no pasa por environmentOr: no hay respaldo que devolver. Ausente es un
		// error de validación, no una caída silenciosa a una clave conocida.
		JWTSecret: strings.TrimSpace(os.Getenv("JWT_SECRET")),
		JWTTTL:    DefaultJWTTTL,
	}

	// JWT_TTL sí tiene valor por defecto, y uno presente pero ilegible es un error: un
	// error de tipeo no puede convertirse en un token con otra vida de la pedida.
	if raw := strings.TrimSpace(os.Getenv("JWT_TTL")); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("JWT_TTL: %q is not a duration such as \"12h\": %w", raw, err)
		}
		cfg.JWTTTL = ttl
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

	// Las tres cadenas se validan igual: el rol de migración, el de aplicación y el
	// pre-auth. Cada error nombra su propia variable, nunca la de otro DSN.
	for _, dsn := range []struct {
		variable string
		value    string
	}{
		{variable: "DATABASE_URL", value: c.DatabaseURL},
		{variable: "DATABASE_APP_URL", value: c.AppDatabaseURL},
		{variable: "DATABASE_AUTH_URL", value: c.AuthDatabaseURL},
		{variable: "DATABASE_PLATFORM_URL", value: c.PlatformDatabaseURL},
	} {
		if err := validateDatabaseURL(dsn.variable, dsn.value); err != nil {
			return err
		}
	}

	// La firma del access token: obligatoria y con un piso de longitud. El mensaje nombra
	// la variable y dice por qué no hay valor por defecto, porque el operador que la vea va
	// a estar mirando un arranque fallido (docs/BACKEND.md §2.5).
	if c.JWTSecret == "" {
		return errors.New("JWT_SECRET: required and has no default (a committed signing key is a forgeable token); set it to at least 32 bytes of entropy")
	}
	if len(c.JWTSecret) < minJWTSecretBytes {
		return fmt.Errorf("JWT_SECRET: %d bytes is shorter than the %d-byte minimum (docs/BACKEND.md §2.5)", len(c.JWTSecret), minJWTSecretBytes)
	}

	if c.JWTTTL <= 0 {
		return fmt.Errorf("JWT_TTL: %v is not a positive lifetime", c.JWTTTL)
	}

	return nil
}

// validateDatabaseURL aplica a un DSN la misma regla que Sprint 1 fijó para
// DATABASE_URL: esquema de PostgreSQL, con host, y un mensaje que nombra la variable
// culpable.
func validateDatabaseURL(variable, dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("%s: not a valid URL: %w", variable, err)
	}
	switch parsed.Scheme {
	case "postgres", "postgresql":
	default:
		return fmt.Errorf("%s: unsupported scheme %q (want postgres or postgresql)", variable, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s: %q has no host", variable, RedactDSN(dsn))
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

// AppDatabaseURL devuelve el DSN del rol de aplicación: DATABASE_APP_URL si está
// definida, y si no el valor por defecto.
//
// El rol es distinto del de migración a propósito: `pos_app` no tiene DDL ni `SELECT`
// sobre `tenants`, y todo su acceso a datos pasa por las políticas de RLS de tenant
// (docs/DATABASE.md §5.5).
func AppDatabaseURL() string {
	return environmentOr("DATABASE_APP_URL", DefaultAppDatabaseURL)
}

// PlatformDatabaseURL devuelve el DSN del rol de catálogo global `pos_platform`:
// DATABASE_PLATFORM_URL si está definida, y si no el valor por defecto (W3-BE-2, docs/
// DATABASE.md §5.6). El rol existe SOLO para la mediación del GTIN: no es otro rol de
// negocio, y las concesiones que describa roles_and_grants.sql valen acá y nada más.
func PlatformDatabaseURL() string {
	return environmentOr("DATABASE_PLATFORM_URL", DefaultPlatformDatabaseURL)
}

// MaintenanceDatabaseURL devuelve el DSN del rol de mantenimiento `pos_maintenance`:
// DATABASE_MAINTENANCE_URL si está definida, y si no el valor por defecto (W6-DB-1,
// docs/DATABASE.md §5.5, §7.2). Su única concesión de datos es DELETE sobre
// `idempotency_keys`; el DDL de particiones corre con el rol de migración (split F9).
func MaintenanceDatabaseURL() string {
	return environmentOr("DATABASE_MAINTENANCE_URL", DefaultMaintenanceDatabaseURL)
}

// ObjectStorageDir devuelve el directorio del almacenamiento de objetos de las fotos:
// OBJECT_STORAGE_DIR si está definida y si no el valor por defecto (W3-BE-3, docs/
// BACKEND.md §2.5 ya anuncia los nombres OBJECT_STORAGE_* de este módulo).
func ObjectStorageDir() string {
	return environmentOr("OBJECT_STORAGE_DIR", DefaultObjectStorageDir)
}

// ObjectStorageURL devuelve la base pública de las URLs de foto: OBJECT_STORAGE_URL si
// está definida y si no la del propio proceso en desarrollo local (docs/BACKEND.md §10.3).
func ObjectStorageURL() string {
	return environmentOr("OBJECT_STORAGE_URL", DefaultObjectStorageURL)
}

// AuthDatabaseURL devuelve el DSN del rol pre-auth: DATABASE_AUTH_URL si está definida, y
// si no el valor por defecto.
func AuthDatabaseURL() string {
	return environmentOr("DATABASE_AUTH_URL", DefaultAuthDatabaseURL)
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
