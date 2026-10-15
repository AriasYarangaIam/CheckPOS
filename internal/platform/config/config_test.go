package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
)

// clearEnvironment vacía toda variable que Load lee, para que cada prueba arranque desde
// los valores por defecto documentados y no desde lo que exporte la terminal del
// desarrollador.
func clearEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DATABASE_URL", "")
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
}

func TestLoadPrefersTheEnvironmentOverDefaults(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DATABASE_URL", "postgres://someone:secret@db.internal:5432/other?sslmode=require")

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
