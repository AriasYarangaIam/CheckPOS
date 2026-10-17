package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// testJWTSecret es la clave de firma de las pruebas: 32 bytes o más, y sin ninguna relación
// con una clave real. clearEnvironment la fija porque JWT_SECRET no tiene valor por defecto;
// sin ella cada caso de abajo probaría la validación de la firma en lugar de la suya.
const testJWTSecret = "test-only-signing-key-0123456789abcdef"

// clearEnvironment vacía toda variable que Load lee, para que cada prueba arranque desde
// los valores por defecto documentados y no desde lo que exporte la terminal del
// desarrollador.
func clearEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_APP_URL", "")
	t.Setenv("DATABASE_AUTH_URL", "")
	t.Setenv("JWT_SECRET", testJWTSecret)
	t.Setenv("JWT_TTL", "")
}

func TestLoadUsesDocumentedDefaults(t *testing.T) {
	clearEnvironment(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() sin variables de entorno: error inesperado: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.DatabaseURL != config.DefaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, want el valor por defecto de docker-compose %q", cfg.DatabaseURL, config.DefaultDatabaseURL)
	}
	if cfg.AppDatabaseURL != config.DefaultAppDatabaseURL {
		t.Errorf("AppDatabaseURL = %q, want el valor por defecto del rol pos_app %q", cfg.AppDatabaseURL, config.DefaultAppDatabaseURL)
	}
	if cfg.AuthDatabaseURL != config.DefaultAuthDatabaseURL {
		t.Errorf("AuthDatabaseURL = %q, want el valor por defecto del rol pos_auth %q", cfg.AuthDatabaseURL, config.DefaultAuthDatabaseURL)
	}
}

func TestLoadPrefersTheEnvironmentOverDefaults(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DATABASE_URL", "postgres://someone:secret@db.internal:5432/other?sslmode=require")
	t.Setenv("DATABASE_APP_URL", "postgres://pos_app:secret@db.internal:5432/other?sslmode=require")
	t.Setenv("DATABASE_AUTH_URL", "postgres://pos_auth:secret@db.internal:5432/other?sslmode=require")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() con el entorno completo: error inesperado: %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, "127.0.0.1:9090")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.DatabaseURL != "postgres://someone:secret@db.internal:5432/other?sslmode=require" {
		t.Errorf("DatabaseURL = %q, want el valor del entorno", cfg.DatabaseURL)
	}
	if cfg.AppDatabaseURL != "postgres://pos_app:secret@db.internal:5432/other?sslmode=require" {
		t.Errorf("AppDatabaseURL = %q, want el valor del entorno", cfg.AppDatabaseURL)
	}
	if cfg.AuthDatabaseURL != "postgres://pos_auth:secret@db.internal:5432/other?sslmode=require" {
		t.Errorf("AuthDatabaseURL = %q, want el valor del entorno", cfg.AuthDatabaseURL)
	}
}

func TestLoadNormalizesTheLogLevelCase(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("LOG_LEVEL", "DEBUG")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() con LOG_LEVEL=DEBUG: error inesperado: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want el valor en minúsculas %q", cfg.LogLevel, "debug")
	}
}

// TestJWTSigningConfiguration cubre la configuración del access token
// (docs/BACKEND.md §2.5, §5.1): el secreto de firma es obligatorio y no tiene valor por
// defecto —una clave por defecto en el repositorio es un token falsificable por cualquiera
// que lea el código— y la vida del token tiene un valor documentado.
func TestJWTSigningConfiguration(t *testing.T) {
	t.Run("the signing secret is required and has no committed default", func(t *testing.T) {
		clearEnvironment(t)
		t.Setenv("JWT_SECRET", "")

		cfg, err := config.Load()
		if err == nil {
			t.Fatalf("Load() sin JWT_SECRET: no dio error y devolvió %+v, want un error de validación", cfg)
		}
		if !strings.Contains(err.Error(), "JWT_SECRET") {
			t.Errorf("el error %q no nombra la variable culpable %q", err.Error(), "JWT_SECRET")
		}
	})

	t.Run("a signing secret shorter than 32 bytes is rejected", func(t *testing.T) {
		// docs/BACKEND.md §2.5 fija el piso en 32 bytes para HS256.
		clearEnvironment(t)
		t.Setenv("JWT_SECRET", strings.Repeat("a", 31))

		if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
			t.Fatalf("Load() con un secreto de 31 bytes: err = %v, want un error que nombre JWT_SECRET", err)
		}
	})

	t.Run("the signing secret comes from the environment", func(t *testing.T) {
		clearEnvironment(t)
		t.Setenv("JWT_SECRET", testJWTSecret)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load(): error inesperado: %v", err)
		}
		if cfg.JWTSecret != testJWTSecret {
			t.Errorf("JWTSecret = %q, want el valor del entorno", cfg.JWTSecret)
		}
	})

	t.Run("the token lifetime defaults to the documented value", func(t *testing.T) {
		clearEnvironment(t)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load(): error inesperado: %v", err)
		}
		if cfg.JWTTTL != config.DefaultJWTTTL {
			t.Errorf("JWTTTL = %v, want el valor por defecto %v", cfg.JWTTTL, config.DefaultJWTTTL)
		}
		if config.DefaultJWTTTL <= 0 {
			t.Errorf("DefaultJWTTTL = %v, want una vida positiva", config.DefaultJWTTTL)
		}
	})

	t.Run("the token lifetime comes from JWT_TTL", func(t *testing.T) {
		clearEnvironment(t)
		t.Setenv("JWT_TTL", "45m")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load(): error inesperado: %v", err)
		}
		if cfg.JWTTTL != 45*time.Minute {
			t.Errorf("JWTTTL = %v, want %v", cfg.JWTTTL, 45*time.Minute)
		}
	})
}

// TestLoadFailsFastOnInvalidValues cubre la regla de fallar rápido: un valor presente
// pero equivocado se rechaza con un mensaje que nombra la variable culpable, en lugar de
// caer en silencio al valor por defecto.
func TestLoadFailsFastOnInvalidValues(t *testing.T) {
	cases := []struct {
		name      string
		variable  string
		value     string
		wantField string
	}{
		{name: "unknown log level", variable: "LOG_LEVEL", value: "verbose", wantField: "LOG_LEVEL"},
		{name: "address without a port", variable: "HTTP_ADDR", value: "localhost", wantField: "HTTP_ADDR"},
		{name: "non numeric port", variable: "HTTP_ADDR", value: ":http", wantField: "HTTP_ADDR"},
		{name: "port out of range", variable: "HTTP_ADDR", value: ":70000", wantField: "HTTP_ADDR"},
		{name: "another database engine", variable: "DATABASE_URL", value: "mysql://pos@localhost:3306/pos", wantField: "DATABASE_URL"},
		{name: "database url without a host", variable: "DATABASE_URL", value: "postgres:///pos", wantField: "DATABASE_URL"},
		{name: "database url that is not a url", variable: "DATABASE_URL", value: "not a connection string", wantField: "DATABASE_URL"},
		{name: "app database engine", variable: "DATABASE_APP_URL", value: "mysql://pos_app@localhost:3306/pos", wantField: "DATABASE_APP_URL"},
		{name: "app database url without a host", variable: "DATABASE_APP_URL", value: "postgres:///pos", wantField: "DATABASE_APP_URL"},
		{name: "app database url that is not a url", variable: "DATABASE_APP_URL", value: "not a connection string", wantField: "DATABASE_APP_URL"},
		{name: "auth database engine", variable: "DATABASE_AUTH_URL", value: "mysql://pos_auth@localhost:3306/pos", wantField: "DATABASE_AUTH_URL"},
		{name: "auth database url without a host", variable: "DATABASE_AUTH_URL", value: "postgres:///pos", wantField: "DATABASE_AUTH_URL"},
		{name: "auth database url that is not a url", variable: "DATABASE_AUTH_URL", value: "not a connection string", wantField: "DATABASE_AUTH_URL"},
		{name: "token lifetime that is not a duration", variable: "JWT_TTL", value: "banana", wantField: "JWT_TTL"},
		{name: "token lifetime of zero", variable: "JWT_TTL", value: "0s", wantField: "JWT_TTL"},
		{name: "negative token lifetime", variable: "JWT_TTL", value: "-1h", wantField: "JWT_TTL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(tc.variable, tc.value)

			cfg, err := config.Load()
			if err == nil {
				t.Fatalf("Load() con %s=%q: no dio error y devolvió %+v, want un error de validación", tc.variable, tc.value, cfg)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("el error %q no nombra la variable culpable %q", err.Error(), tc.wantField)
			}
		})
	}
}

// TestDatabaseURLSharesTheMigrationRunnerSource fija que la API y el runner de
// migraciones lean la misma variable con el mismo valor de respaldo.
func TestDatabaseURLSharesTheMigrationRunnerSource(t *testing.T) {
	t.Run("falls back to the docker compose default", func(t *testing.T) {
		clearEnvironment(t)
		if got := config.DatabaseURL(); got != config.DefaultDatabaseURL {
			t.Errorf("DatabaseURL() = %q, want %q", got, config.DefaultDatabaseURL)
		}
	})

	t.Run("reads DATABASE_URL", func(t *testing.T) {
		clearEnvironment(t)
		t.Setenv("DATABASE_URL", "postgres://pos:pos_local_dev@localhost:5433/another")

		want := "postgres://pos:pos_local_dev@localhost:5433/another"
		if got := config.DatabaseURL(); got != want {
			t.Errorf("DatabaseURL() = %q, want %q", got, want)
		}

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load(): error inesperado: %v", err)
		}
		if cfg.DatabaseURL != config.DatabaseURL() {
			t.Errorf("Load().DatabaseURL = %q pero DatabaseURL() = %q; ambos deben leer la misma fuente", cfg.DatabaseURL, config.DatabaseURL())
		}
	})
}

// TestRoleDatabaseURLsReadTheirOwnVariables fija que los tres DSN tengan una fuente
// propia: el rol de migración no hereda el de la aplicación y viceversa, así que una
// variable mal escrita no puede hacer que la API arranque con el rol de migración.
func TestRoleDatabaseURLsReadTheirOwnVariables(t *testing.T) {
	t.Run("falls back to the documented defaults", func(t *testing.T) {
		clearEnvironment(t)

		if got := config.AppDatabaseURL(); got != config.DefaultAppDatabaseURL {
			t.Errorf("AppDatabaseURL() = %q, want %q", got, config.DefaultAppDatabaseURL)
		}
		if got := config.AuthDatabaseURL(); got != config.DefaultAuthDatabaseURL {
			t.Errorf("AuthDatabaseURL() = %q, want %q", got, config.DefaultAuthDatabaseURL)
		}
		if config.DefaultDatabaseURL == config.DefaultAppDatabaseURL || config.DefaultDatabaseURL == config.DefaultAuthDatabaseURL {
			t.Errorf("los tres valores por defecto deben diferir en el rol: %q, %q, %q",
				config.DefaultDatabaseURL, config.DefaultAppDatabaseURL, config.DefaultAuthDatabaseURL)
		}
	})

	t.Run("reads DATABASE_APP_URL", func(t *testing.T) {
		clearEnvironment(t)
		t.Setenv("DATABASE_APP_URL", "postgres://pos_app:pos_local_dev@localhost:5433/another")

		want := "postgres://pos_app:pos_local_dev@localhost:5433/another"
		if got := config.AppDatabaseURL(); got != want {
			t.Errorf("AppDatabaseURL() = %q, want %q", got, want)
		}

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load(): error inesperado: %v", err)
		}
		if cfg.AppDatabaseURL != config.AppDatabaseURL() {
			t.Errorf("Load().AppDatabaseURL = %q pero AppDatabaseURL() = %q; ambos deben leer la misma fuente", cfg.AppDatabaseURL, config.AppDatabaseURL())
		}
		if cfg.DatabaseURL != config.DefaultDatabaseURL {
			t.Errorf("Load().DatabaseURL = %q, want el valor por defecto %q: DATABASE_APP_URL no debe pisar el DSN de migración", cfg.DatabaseURL, config.DefaultDatabaseURL)
		}
	})

	t.Run("reads DATABASE_AUTH_URL", func(t *testing.T) {
		clearEnvironment(t)
		t.Setenv("DATABASE_AUTH_URL", "postgres://pos_auth:pos_local_dev@localhost:5433/another")

		want := "postgres://pos_auth:pos_local_dev@localhost:5433/another"
		if got := config.AuthDatabaseURL(); got != want {
			t.Errorf("AuthDatabaseURL() = %q, want %q", got, want)
		}

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load(): error inesperado: %v", err)
		}
		if cfg.AuthDatabaseURL != config.AuthDatabaseURL() {
			t.Errorf("Load().AuthDatabaseURL = %q pero AuthDatabaseURL() = %q; ambos deben leer la misma fuente", cfg.AuthDatabaseURL, config.AuthDatabaseURL())
		}
	})
}

func TestRedactDSNHidesThePassword(t *testing.T) {
	const dsn = "postgres://pos:pos_local_dev@localhost:5433/pos?sslmode=disable"

	redacted := config.RedactDSN(dsn)

	if strings.Contains(redacted, "pos_local_dev") {
		t.Errorf("RedactDSN filtró la contraseña: %q", redacted)
	}
	for _, keep := range []string{"pos", "localhost:5433", "/pos", "sslmode=disable"} {
		if !strings.Contains(redacted, keep) {
			t.Errorf("RedactDSN(%q) perdió %q; obtuvo %q", dsn, keep, redacted)
		}
	}

	noPassword := "postgres://pos@localhost:5433/pos"
	if got := config.RedactDSN(noPassword); got != noPassword {
		t.Errorf("RedactDSN(%q) = %q, want la cadena sin cambios", noPassword, got)
	}

	if got := config.RedactDSN("://not a url"); got == "" || strings.Contains(got, "not a url") {
		t.Errorf("RedactDSN sobre un DSN que no se puede parsear = %q, want un marcador que no filtre nada", got)
	}
}

func TestDotEnvSetsMissingVariablesAndKeepsExistingOnes(t *testing.T) {
	const (
		fresh    = "CHECKPOS_DOTENV_PROBE"
		existing = "CHECKPOS_DOTENV_EXISTING"
	)

	_ = os.Unsetenv(fresh)
	t.Cleanup(func() { _ = os.Unsetenv(fresh) })
	t.Setenv(existing, "from-the-shell")

	path := filepath.Join(t.TempDir(), ".env")
	content := "# a comment\n" +
		"\n" +
		fresh + "=from-the-file\n" +
		existing + "=from-the-file\n" +
		"QUOTED=  \"spaced value\"  \n" +
		"NOT_A_PAIR\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("escribir %s: %v", path, err)
	}

	config.DotEnv(path)

	if got := os.Getenv(fresh); got != "from-the-file" {
		t.Errorf("%s = %q, want %q", fresh, got, "from-the-file")
	}
	if got := os.Getenv(existing); got != "from-the-shell" {
		t.Errorf("%s = %q, want que gane el valor de la terminal %q", existing, got, "from-the-shell")
	}
	if got := os.Getenv("QUOTED"); got != "spaced value" {
		t.Errorf("QUOTED = %q, want el valor sin comillas %q", got, "spaced value")
	}
	if _, ok := os.LookupEnv("NOT_A_PAIR"); ok {
		t.Error("una línea sin '=' debe ignorarse, no exportarse")
	}
}

func TestDotEnvIgnoresAMissingFile(t *testing.T) {
	// Un archivo ausente es el caso normal fuera de la raíz del repositorio: no es un
	// error, y tampoco debe provocar un pánico.
	config.DotEnv(filepath.Join(t.TempDir(), ".env-does-not-exist"))
}
