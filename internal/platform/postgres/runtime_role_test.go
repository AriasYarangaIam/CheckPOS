// Prueba de la aserción del rol de runtime: dos veredictos sobre roles REALES de las
// migraciones —el dueño, que tiene que ser rechazado, y pos_app y pos_auth, que tienen que
// pasar— más el caso trivial del pool ausente.
//
// Usa la base desechable del arnés de schema_contract_test.go por la misma razón que el resto
// de las pruebas de integración: los roles son del clúster, pero el DSN de cada uno apunta a
// una base concreta y la de desarrollo no se toca (docs/BACKEND.md §9).
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// TestTheAssertionRefusesAPrivilegedRole mide los dos lados de la decisión con los roles que
// las migraciones crean y con el dueño del contenedor. Si esta prueba pasa con el dueño, la
// aserción de arranque de cmd/api no está mirando el rol real y el aislamiento del sprint queda
// apagable sin que nada falle.
func TestTheAssertionRefusesAPrivilegedRole(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	database := queryOneString(t, db, "SELECT current_database()")
	ownerRole := queryOneString(t, db, "SELECT current_user")

	ctx := context.Background()

	t.Run("the owner DSN is refused and names its environment variable", func(t *testing.T) {
		// El DSN del dueño es el que `pos` —el POSTGRES_USER de docker-compose.yml— usa para
		// migrar: `rolsuper` y `rolbypassrls` en este contenedor. Se arma con postgres.New y
		// no con un helper de test justamente para dejar medido que New NO aplica la
		// aserción: si la aplicara, ninguna prueba de integración podría migrar una base
		// desechable.
		pool := runtimePool(t, config.DatabaseURL(), database)

		err := postgres.AssertLeastPrivilegeRole(ctx, pool, "DATABASE_APP_URL")
		if err == nil {
			t.Fatalf("el DSN del dueño (%s, rol %s) fue aceptado: un arranque con DATABASE_APP_URL apuntado ahí serviría todos los tenants sin error", config.RedactDSN(config.DatabaseURL()), ownerRole)
		}

		if !errors.Is(err, postgres.ErrPrivilegedRuntimeRole) {
			t.Errorf("la causa del rechazo no es ErrPrivilegedRuntimeRole: %v", err)
		}

		message := err.Error()
		for _, want := range []string{"DATABASE_APP_URL", `"` + ownerRole + `"`, "rolsuper", "rolbypassrls"} {
			if !strings.Contains(message, want) {
				t.Errorf("el mensaje no nombra %s: %q", want, message)
			}
		}
	})

	t.Run("pos_app and pos_auth pass", func(t *testing.T) {
		// Los dos roles de la matriz de concesiones que el proceso usa en runtime: ninguno
		// tiene `rolsuper` ni `rolbypassrls`, así que la aserción tiene que dejarlos pasar.
		// Sin este lado, un helper que rechazara todo pasaría la prueba de arriba.
		for _, role := range []struct {
			variable string
			dsn      string
		}{
			{variable: "DATABASE_APP_URL", dsn: config.AppDatabaseURL()},
			{variable: "DATABASE_AUTH_URL", dsn: config.AuthDatabaseURL()},
		} {
			err := postgres.AssertLeastPrivilegeRole(ctx, runtimePool(t, role.dsn, database), role.variable)
			if err != nil {
				t.Errorf("%s (%s) fue rechazado y no debería: %v", role.variable, config.RedactDSN(role.dsn), err)
			}
		}
	})
}

// TestTheExpectedRoleAssertionRefusesASwappedDSN fija el complemento de la aserción de
// privilegio mínimo (W1): no alcanza con que el rol no sea privilegiado, cada pool tiene
// que conectar con SU rol de la matriz de concesiones. Un DSN intercambiado —el DSN de
// `pos_app` dentro de DATABASE_AUTH_URL— es válido, alcanza una base viva y no es
// privilegiado, así que sin esta aserción el proceso arrancaría, /readyz respondería 200
// y el login devolvería 401 para siempre.
func TestTheExpectedRoleAssertionRefusesASwappedDSN(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	database := queryOneString(t, db, "SELECT current_database()")

	ctx := context.Background()

	t.Run("the auth variable pointing at the app DSN is refused and names variable and role", func(t *testing.T) {
		// El DSN de `pos_app` en DATABASE_AUTH_URL: ningún privilegio de más, ninguna
		// falla de conexión y aun así una caída total de autenticación disfrazada de
		// arranque sano. El rechazo tiene que nombrar qué variable apuntó mal y con qué
		// rol conectó de verdad, para que el operador no tenga que adivinar.
		pool := runtimePool(t, config.AppDatabaseURL(), database)

		err := postgres.AssertExpectedRole(ctx, pool, "DATABASE_AUTH_URL", "pos_auth")
		if err == nil {
			t.Fatalf("el DSN de %s dentro de DATABASE_AUTH_URL fue aceptado: /readyz quedaria en 200 con el login en 401 para siempre (W1)", config.RedactDSN(config.AppDatabaseURL()))
		}

		message := err.Error()
		for _, want := range []string{"DATABASE_AUTH_URL", "pos_app", "pos_auth"} {
			if !strings.Contains(message, want) {
				t.Errorf("el mensaje no nombra %s: %q", want, message)
			}
		}
	})

	t.Run("each runtime pool connected as its expected role passes", func(t *testing.T) {
		// Sin este lado, un helper que rechazara cualquier par pasaría la prueba de arriba.
		for _, expected := range []struct {
			variable string
			dsn      string
			role     string
		}{
			{variable: "DATABASE_APP_URL", dsn: config.AppDatabaseURL(), role: "pos_app"},
			{variable: "DATABASE_AUTH_URL", dsn: config.AuthDatabaseURL(), role: "pos_auth"},
		} {
			err := postgres.AssertExpectedRole(ctx, runtimePool(t, expected.dsn, database), expected.variable, expected.role)
			if err != nil {
				t.Errorf("%s conectado como %s fue rechazado y no debería: %v", expected.variable, expected.role, err)
			}
		}
	})

	t.Run("a cancelled context surfaces as an error instead of silence", func(t *testing.T) {
		pool := runtimePool(t, config.AppDatabaseURL(), database)

		failedCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := postgres.AssertExpectedRole(failedCtx, pool, "DATABASE_APP_URL", "pos_app")
		if err == nil {
			t.Fatal("un contexto cancelado fue aceptado: la aserción no puede probar con qué rol habla el pool")
		}
		if !strings.Contains(err.Error(), "DATABASE_APP_URL") {
			t.Errorf("el mensaje no nombra la variable: %q", err.Error())
		}
	})

	t.Run("a missing pool is refused", func(t *testing.T) {
		err := postgres.AssertExpectedRole(context.Background(), nil, "DATABASE_APP_URL", "pos_app")
		if err == nil {
			t.Fatal("un pool nulo fue aceptado: la aserción no probó nada")
		}
		if !strings.Contains(err.Error(), "DATABASE_APP_URL") {
			t.Errorf("el mensaje no nombra la variable: %q", err.Error())
		}
	})
}

// TestTheAssertionRefusesAMissingPool fija el borde sin base: un pool ausente no se puede
// interrogar, así que la aserción falla en lugar de dejarlo pasar. Es la misma decisión que el
// resto del paquete toma con un pool nulo (internal/platform/postgres/tenant.go).
func TestTheAssertionRefusesAMissingPool(t *testing.T) {
	err := postgres.AssertLeastPrivilegeRole(context.Background(), nil, "DATABASE_APP_URL")
	if err == nil {
		t.Fatal("un pool nulo fue aceptado: la aserción no probó nada")
	}
	if !strings.Contains(err.Error(), "DATABASE_APP_URL") {
		t.Errorf("el mensaje no nombra la variable: %q", err.Error())
	}
}

// runtimePool abre un pool del rol del DSN dado contra la base desechable. El nombre de la
// base se pasa como argumento y no se rearma el DSN: es la misma cirugía de postgres.WithDatabase
// que usan cmd/api y el arnés, no una segunda forma de construir la cadena de conexión.
func runtimePool(t *testing.T, baseDSN, database string) *pgxpool.Pool {
	t.Helper()

	dsn, err := postgres.WithDatabase(baseDSN, database)
	if err != nil {
		t.Fatalf("derivar el DSN para la base %s: %v", database, err)
	}

	pool, err := postgres.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("construir el pool para %s: %v", config.RedactDSN(dsn), err)
	}
	t.Cleanup(pool.Close)

	return pool
}
