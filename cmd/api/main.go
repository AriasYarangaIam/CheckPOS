// Command api es el proceso único del backend. Sprint 2 expone sus dos sondeos de operación
// y `POST /auth/login`; el resto de la superficie de negocio llega en los sprints siguientes
// (docs/BACKEND.md §10).
//
//	go run ./cmd/api
//
// La configuración viene del entorno (internal/platform/config), con un `.env` del
// directorio de trabajo como comodidad de desarrollo. Los pools de datos son dos, uno por
// rol de la matriz de concesiones (docs/DATABASE.md §5.5): DATABASE_APP_URL (`pos_app`) para
// los datos de negocio bajo RLS de tenant y DATABASE_AUTH_URL (`pos_auth`) para la única
// lectura pre-auth del login. El DSN de migración (DATABASE_URL) no se abre acá: el proceso
// que atiende peticiones no se conecta con el rol dueño.
//
//	GET  /healthz      sondeo de vida: 200 sin consultar ninguna dependencia
//	GET  /readyz       sondeo de preparación: 200 si los dos pools responden, 503 si no
//	POST /auth/login   ruta pre-auth: 200 con el access token de empleado, 401 genérico
//	POST /auth/device-bind  ruta autenticada: 200 con el dispositivo y su ventana offline,
//	                        401 si el access token falta o no verifica
//	GET  /products          rutas autenticadas del catálogo por tenant (Sprint 3, W3-BE-1)
//	POST /products          —la digitalización del catálogo vive en `products`, con uom
//	PATCH /products/{id}     columna tipada, stock inmutable por la superficie y LWW
//	GET  /gtin-lookup       mediación read-only del catálogo global (Sprint 3, W3-BE-2)
//
// SIGINT y SIGTERM disparan un apagado ordenado: el servidor deja de aceptar conexiones
// y espera las peticiones en vuelo antes de cerrar los pools.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/logging"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// shutdownTimeout es lo que se espera a las peticiones en vuelo antes de cerrar. Es
// mayor que el presupuesto de la venta más lenta (3 s, docs/BACKEND.md §8.1) y corto
// frente a cualquier orquestador.
const shutdownTimeout = 10 * time.Second

// dependencies es el sondeo de preparación del proceso: los dos pools de datos que la API
// necesita para responder.
//
// El ping es la EXCEPCIÓN documentada a «ninguna consulta corre fuera del helper de tenant»:
// no consulta filas de ningún tenant, pregunta si la conexión existe. Por eso vive sobre el
// pool crudo y no sobre el envoltorio de transacción de tenant, que no expone ninguna forma
// de consultar sin contexto (docs/BACKEND.md §7.7).
type dependencies struct {
	app  *pgxpool.Pool
	auth *pgxpool.Pool
}

// Ping informa si los dos pools responden. El primer fallo corta: el nombre del pool viaja en
// el error para el log del operador, nunca al cliente (httpx.NotReady arma un mensaje
// genérico).
func (d dependencies) Ping(ctx context.Context) error {
	if err := d.app.Ping(ctx); err != nil {
		return fmt.Errorf("the application pool: %w", err)
	}
	if err := d.auth.Ping(ctx); err != nil {
		return fmt.Errorf("the authentication pool: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "api: %v\n", err)
		os.Exit(1)
	}
}

// run arma el proceso y bloquea hasta que llegue una señal de apagado o el servidor
// falle. Devuelve el error en lugar de llamar a os.Exit para que main sea el único lugar
// que decide el código de salida.
func run() error {
	config.DotEnv(".env")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger := logging.New(cfg.LogLevel, os.Stdout)

	// El contexto se cancela con SIGINT o SIGTERM y es lo único que hace terminar a run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.New(ctx, cfg.AppDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// El rol detrás del DSN es una precondición de TODO el aislamiento del sprint, y su único
	// modo de falla es silencioso: `pos` —el POSTGRES_USER del contenedor de desarrollo— tiene
	// `rolsuper` y `rolbypassrls`, y FORCE RLS no liga a un superusuario. Con ese DSN cada
	// consulta devolvería las filas de todos los tenants sin un solo error, y nada más lo
	// detecta: los tests de integración arman sus propios pools y nunca pasan por acá.
	//
	// Se evalúa antes de armar el servidor, y una base que no responde también es un rechazo de
	// arranque: la aserción es una precondición de corrección, no un sondeo de salud —para eso
	// está /readyz— y un proceso que no puede probar con qué rol habla no atiende.
	if err := postgres.AssertLeastPrivilegeRole(ctx, pool, "DATABASE_APP_URL"); err != nil {
		return err
	}

	// El privilegio mínimo no basta: un DSN válido y poco privilegiado puede ser el DSN
	// EQUIVOCADO. Cada pool acredita su rol exacto de la matriz de concesiones, así que un
	// DSN intercambiado o mal escrito detiene el arranque en vez de terminar en la caída
	// silenciosa de autenticación que describe W1: /readyz en 200 y 401 en cada endpoint
	// para siempre.
	if err := postgres.AssertExpectedRole(ctx, pool, "DATABASE_APP_URL", "pos_app"); err != nil {
		return err
	}

	// El pool pre-auth es distinto a propósito: `pos_auth` tiene `SELECT` sobre `users` y
	// sobre ninguna otra tabla, y su política permisiva es lo que permite resolver el
	// teléfono del login antes de que exista un JWT (docs/BACKEND.md §4.2, §5.4).
	authPool, err := postgres.New(ctx, cfg.AuthDatabaseURL)
	if err != nil {
		return err
	}
	defer authPool.Close()

	// La misma aserción sobre el segundo DSN: `pos_auth` es un rol de la matriz de concesiones
	// y un error de tipeo puede apuntarlo al DSN dueño tan fácilmente como al primero.
	if err := postgres.AssertLeastPrivilegeRole(ctx, authPool, "DATABASE_AUTH_URL"); err != nil {
		return err
	}

	// La misma pareja sobre el segundo pool, con su rol esperado: acá un tipeo puede
	// apuntar el DSN pre-auth a cualquier otro DSN válido del clúster.
	if err := postgres.AssertExpectedRole(ctx, authPool, "DATABASE_AUTH_URL", "pos_auth"); err != nil {
		return err
	}

	// El tercer pool (Sprint 3, W3-BE-2): `pos_platform` existe para UNA forma de consulta,
	// la mediación read-only del catálogo global. Es un pool CRUDO a propósito —las tablas
	// globales no tienen RLS de tenant y no reciben contexto que no existe (docs/DATABASE.md
	// §5.6)—, y sus dos aserciones de arranque valen lo mismo que las de los otros dos DSN.
	platformPool, err := postgres.New(ctx, cfg.PlatformDatabaseURL)
	if err != nil {
		return err
	}
	defer platformPool.Close()

	if err := postgres.AssertLeastPrivilegeRole(ctx, platformPool, "DATABASE_PLATFORM_URL"); err != nil {
		return err
	}

	if err := postgres.AssertExpectedRole(ctx, platformPool, "DATABASE_PLATFORM_URL", "pos_platform"); err != nil {
		return err
	}

	readiness := dependencies{app: pool, auth: authPool}

	if err := readiness.Ping(ctx); err != nil {
		// Un fallo al arrancar no impide servir: /readyz reporta 503 hasta que la base
		// responda, y el proceso se recupera solo cuando vuelva. Se avisa para que el
		// operador no tenga que adivinar por qué el sondeo está en rojo.
		logger.Warn("the database is not reachable yet; /readyz will report 503", "error", err)
	} else {
		logger.Info("connected to the database")
	}

	issuer, err := identity.NewTokenIssuer(cfg.JWTSecret, cfg.JWTTTL)
	if err != nil {
		return err
	}

	login, err := identity.NewLoginHandler(identity.NewPostgresUserDirectory(authPool), issuer, logger)
	if err != nil {
		return err
	}

	// El pool de datos entra al helper de transacción de tenant, que es la disciplina de TIPOS
	// de la ruta de datos de negocio: `TenantPool` no ofrece ninguna forma de consultar sin
	// contexto (internal/platform/postgres/tenant.go), y `POST /auth/device-bind` es su primer
	// consumidor de producción (docs/BACKEND.md §7.7). No es la única ruta de datos de la
	// aplicación, ni puede serlo: la lectura pre-auth del login corre contra `users` fuera de
	// `WithTenant` —no hay token todavía, así que no hay contexto que fijar, y así lo manda
	// docs/DATABASE.md §5.4— y el ping de /readyz vive sobre el pool crudo porque no es una
	// consulta de tenant.
	deviceBind, err := identity.NewDeviceBindHandler(identity.NewPostgresDeviceBinder(postgres.NewTenantPool(pool)), logger)
	if err != nil {
		return err
	}

	// El catálogo por tenant (Sprint 3, W3-BE-1) corre sobre el MISMO pool de negocio
	// (pos_app + WithTenant). El store es el único camino de datos de `products`.
	products, err := catalog.NewProductsHandler(catalog.NewPostgresProductStore(postgres.NewTenantPool(pool)), logger)
	if err != nil {
		return err
	}

	// La mediación del catálogo global (Sprint 3, W3-BE-2) corre sobre el pool de
	// `pos_platform`, que es el ÚNICO rol con concesiones de lectura sobre las tablas
	// globales: este handler es su único consumidor de producción (docs/DATABASE.md §5.6).
	gtinLookup, err := catalog.NewGtinLookupHandler(catalog.NewPlatformGtinLookup(platformPool), logger)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpx.NewRouter(logger, readiness, httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /auth/login", login)
			// El middleware de token es el único camino por el que este handler se entera de su
			// tenant: leerlo del cuerpo o de un encabezado no existe en el contrato
			// (ARCHITECTURE.md:23).
			mux.Handle("POST /auth/device-bind", identity.RequireAccessToken(issuer, deviceBind))
			// El catálogo por tenant: las tres rutas de /products detrás del MISMO middleware,
			// y el tenant y el usuario salen de los claims del token para las tres.
			mux.Handle("GET /products", identity.RequireAccessToken(issuer, products))
			mux.Handle("POST /products", identity.RequireAccessToken(issuer, products))
			mux.Handle("PATCH /products/{id}", identity.RequireAccessToken(issuer, products))
			// La mediación read-only del catálogo global: autenticada como el resto, y
			// respondiendo a lo sumo UN producto por gtin (docs/DATABASE.md §5.6).
			mux.Handle("GET /gtin-lookup", identity.RequireAccessToken(issuer, gtinLookup))
		})),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// El servidor corre en su propia goroutine para que el hilo principal pueda esperar
	// la señal de apagado. El canal tiene buffer 1 para que esa goroutine nunca se
	// quede bloqueada si nadie lee.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("the API is listening", "addr", cfg.HTTPAddr)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}

	// Desde acá el contexto ya está cancelado, así que el apagado ordenado necesita uno
	// propio con su propio plazo.
	logger.Info("shutting down: waiting for in-flight requests")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	<-serverErr

	logger.Info("shutdown complete")
	return nil
}
