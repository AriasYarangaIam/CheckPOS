// Command api es el proceso único del backend: un servidor HTTP que en Sprint 1 expone
// los sondeos de operación y nada más. Los endpoints de negocio llegan en los sprints
// siguientes (docs/BACKEND.md §10).
//
//	go run ./cmd/api
//
// La configuración viene del entorno (internal/platform/config), con un `.env` del
// directorio de trabajo como comodidad de desarrollo, y la conexión a PostgreSQL de
// DATABASE_URL: el mismo DSN que usa el runner de migraciones.
//
//	GET /healthz   sondeo de vida: 200 sin consultar ninguna dependencia
//	GET /readyz    sondeo de preparación: 200 si PostgreSQL responde, 503 si no
//
// SIGINT y SIGTERM disparan un apagado ordenado: el servidor deja de aceptar conexiones
// y espera las peticiones en vuelo antes de cerrar el pool.
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

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/logging"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// shutdownTimeout es lo que se espera a las peticiones en vuelo antes de cerrar. Es
// mayor que el presupuesto de la venta más lenta (3 s, docs/BACKEND.md §8.1) y corto
// frente a cualquier orquestador.
const shutdownTimeout = 10 * time.Second

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

	pool, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Un fallo al arrancar no impide servir: /readyz reporta 503 hasta que la base
	// responda, y el proceso se recupera solo cuando vuelva. Se avisa para que el
	// operador no tenga que adivinar por qué el sondeo está en rojo.
	if err := pool.Ping(ctx); err != nil {
		logger.Warn("the database is not reachable yet; /readyz will report 503", "error", err)
	} else {
		logger.Info("connected to the database")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpx.NewRouter(logger, pool),
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
