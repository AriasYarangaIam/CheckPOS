package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/logging"
)

// RequestIDHeader es la cabecera por la que viaja el identificador de petición, hacia
// adentro y hacia afuera. Se adopta el que envía el cliente para correlacionar con su
// lado, y se devuelve en la respuesta (docs/BACKEND.md §7.9).
const RequestIDHeader = "X-Request-Id"

// NewRouter arma el servidor HTTP del proceso: los dos sondeos de operación y un
// fallback con el sobre de error.
//
// Sprint 1 no expone ningún endpoint de negocio (docs/BACKEND.md §10.1): el router existe
// para probar que el proceso único arranca, responde y se apaga, y para dar a los sprints
// siguientes un lugar donde montar rutas sin inventar otro transporte.
func NewRouter(logger *slog.Logger, checker ReadinessChecker) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", HealthHandler())
	mux.Handle("GET /readyz", ReadinessHandler(checker))
	// El patrón "/" es el fallback de todo lo que no coincida arriba.
	mux.Handle("/", NotFoundHandler())

	return RequestLogging(logger, mux)
}

// RequestLogging registra una línea por petición y propaga el identificador de petición.
//
// Se registran el método, la ruta, el estado y la duración. No se registra el cuerpo, ni
// las cabeceras de credenciales, ni los datos de negocio: §2.5 y §7.9 son explícitos en
// que PIN, fotos y payloads nunca llegan a un log.
func RequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = logging.NewRequestID()
		}

		w.Header().Set(RequestIDHeader, id)

		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()

		next.ServeHTTP(recorder, r.WithContext(logging.WithRequestID(r.Context(), id)))

		logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.statusCode()),
			slog.Duration("duration", time.Since(started)),
		)
	})
}

// statusRecorder recuerda el estado escrito para poder registrarlo: http.ResponseWriter
// no lo expone.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader captura el estado antes de delegarlo.
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Write captura el 200 implícito de un handler que escribe el cuerpo sin declarar estado.
func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

// statusCode devuelve el estado a registrar, asumiendo 200 cuando no se declaró ninguno.
func (r *statusRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
