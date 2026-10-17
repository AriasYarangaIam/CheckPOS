package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Este archivo cierra la única forma en que el aislamiento de tenant de Sprint 2 se puede
// apagar sin que nada falle: que el proceso se conecte con un rol que el FORCE RLS no liga.
//
// docs/DATABASE.md:667 afirma que «FORCE RLS binds the table owner too». Es cierto para un
// dueño común y FALSO para un superusuario, que es el rol con el que se crea este contenedor:
// `docker-compose.yml` declara `POSTGRES_USER: pos`, y `pos` queda con `rolsuper=t` y
// `rolbypassrls=t`. Medido sobre una base migrada con la misma fila visible para los dos
// tenants, el rol dueño ve las dos y el de aplicación ninguna:
//
//	psql -U pos     -d <db> -c "select current_user,(select count(*) from products)" -> pos     | 2
//	psql -U pos_app -d <db> -c "select current_user,(select count(*) from products)" -> pos_app | 0
//
// El modo de falla es lo que lo vuelve grave: apuntar DATABASE_APP_URL a ese DSN no rompe
// ninguna prueba —los tests de integración arman sus propios pools— y no produce ningún error
// en ejecución. Produce una base sin aislamiento que responde como si el aislamiento
// funcionara, y por eso la aserción existe y por qué es fatal.
//
// La comprobación NO vive en New: los tests de integración y `cmd/migrate` construyen pools
// con el rol dueño para crear, migrar y leer el catálogo de una base desechable, y una
// aserción dentro de New los rompería a todos. Es una precondición del proceso que atiende
// peticiones, no de la construcción del pool, así que la llama cmd/api.

// ErrPrivilegedRuntimeRole es el rechazo de un pool de runtime cuyo rol autenticado puede
// esquivar el RLS. Se exporta para que un test pueda afirmar la CAUSA del rechazo y no
// solamente que hubo un error.
var ErrPrivilegedRuntimeRole = errors.New("the runtime pool is connected as a role that can bypass row-level security")

// currentRolePrivilegesSQL lee del catálogo del clúster el rol de ESTA conexión y los dos
// atributos que anulan el FORCE RLS: `rolsuper` —la política de fila no se aplica al
// superusuario ni con FORCE— y `rolbypassrls`. `pg_roles` es una vista que cualquier rol
// puede leer: enmascara `rolpassword` y expone estos dos atributos tal cual.
const currentRolePrivilegesSQL = `
SELECT current_user, rolsuper, rolbypassrls
FROM pg_roles
WHERE rolname = current_user`

// AssertLeastPrivilegeRole comprueba que el rol detrás de pool no puede esquivar el RLS, y
// devuelve un error que nombra variable —la variable de entorno de la que salió el DSN de ese
// pool— cuando sí puede.
//
// variable es lo único que hace accionable el rechazo: quien lo lee tiene que saber cuál de
// los DSN del proceso corregir, y un mensaje que sólo dijera «rol privilegiado» obligaría a
// adivinar entre DATABASE_APP_URL y DATABASE_AUTH_URL.
//
// Un fallo de conexión también es un error: sin respuesta de PostgreSQL el rol no se puede
// leer, y un proceso que no puede probar con qué rol habla no está en condiciones de atender
// tráfico de tenants.
func AssertLeastPrivilegeRole(ctx context.Context, pool *pgxpool.Pool, variable string) error {
	if pool == nil {
		return fmt.Errorf("%s: the connection pool is nil, so the role behind it cannot be checked", variable)
	}

	var (
		role        string
		superuser   bool
		bypassesRLS bool
	)
	if err := pool.QueryRow(ctx, currentRolePrivilegesSQL).Scan(&role, &superuser, &bypassesRLS); err != nil {
		return fmt.Errorf("%s: ask PostgreSQL which role this pool authenticates as: %w", variable, err)
	}

	var attributes []string
	if superuser {
		attributes = append(attributes, "rolsuper")
	}
	if bypassesRLS {
		attributes = append(attributes, "rolbypassrls")
	}
	if len(attributes) == 0 {
		return nil
	}

	return fmt.Errorf(
		"%s: the pool authenticates as the role %q, which has %s: %w; FORCE ROW LEVEL SECURITY binds a plain table owner but not a superuser, so a plain read of any tenant table would return every tenant's rows without an error (docs/DATABASE.md §5.5)",
		variable, role, strings.Join(attributes, " and "), ErrPrivilegedRuntimeRole)
}
