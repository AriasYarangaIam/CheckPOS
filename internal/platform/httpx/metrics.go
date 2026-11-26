package httpx

import (
	"net/http"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/metrics"
)

// Este archivo conecta el registro de métricas con el mux del proceso (Sprint 9, W9-BE-1).
//
// La regla que importa: se mide el PATRÓN de la ruta, no la URL. Una ruta con id en el camino
// (`/sales/{id}/void`) es un solo punto de la serie temporal; si se midiera la URL, cada venta
// crearía una serie nueva y el registro sería un agujero de memoria disfrazado de observabilidad.

// operationPatterns son las rutas de operación que no se miden: sondas y el propio /metrics.
// Medir /metrics con /metrics sería un contador que se observa a sí mismo.
var operationPatterns = map[string]bool{
	"GET /healthz": true,
	"GET /readyz":  true,
	"GET /metrics": true,
}

// WithMetrics monta `GET /metrics` como ruta de operación. No es una ruta de negocio y no
// aparece en docs/BACKEND.md §4: comparte la naturaleza de /healthz y /readyz.
func WithMetrics(registry *metrics.Registry) Option {
	return func(mux *http.ServeMux) {
		if registry == nil {
			return
		}
		mux.Handle("GET /metrics", registry.Handler())
	}
}

// RequestMetrics envuelve el mux y registra duración y estado de cada ruta de negocio.
//
// Necesita el mux —no solo el handler— porque el patrón que resolvió la petición se obtiene con
// `mux.Handler(r)`; después de servirla ya no está disponible en la request original.
func RequestMetrics(registry *metrics.Registry, mux *http.ServeMux) http.Handler {
	if registry == nil {
		return mux
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if operationPatterns[pattern] {
			mux.ServeHTTP(w, r)
			return
		}

		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()

		mux.ServeHTTP(recorder, r)

		route := pattern
		if route == "" {
			// Sin patrón registrado: el fallback. Se agrupa en una sola etiqueta para no
			// convertir cada 404 en una serie distinta.
			route = "unmatched"
		}
		registry.Observe(route, recorder.statusCode(), time.Since(started))
	})
}
