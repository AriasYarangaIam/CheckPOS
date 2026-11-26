// Package metrics mantiene el registro acotado de latencias del proceso (Sprint 9, W9-BE-1).
//
// El proceso necesita percentiles p50/p95 del cierre de venta y del resto de sus rutas de
// negocio (docs/BACKEND.md §8.1), y necesita que ese registro NO crezca sin límite: un POS que
// atiende durante meses no puede acumular una muestra por petición en memoria. Por eso cada
// ruta conserva un anillo de tamaño fijo y los percentiles se calculan sobre esa ventana
// retenida; el contador total y la suma sí son acumulativos y monótonos.
//
// El paquete no depende de nada externo: renderiza el formato de texto de Prometheus a mano,
// que es un contrato estable y evita agregar una dependencia por cuatro líneas de formato.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSampleCapacity es cuántas muestras conserva el anillo de cada ruta. 2048 es suficiente
// para que el p95 de una jornada de mostrador sea estable y sigue siendo memoria despreciable
// (2048 float64 ≈ 16 KiB por ruta).
const DefaultSampleCapacity = 2048

// defaultBucketBounds son los bordes en milisegundos alineados con los presupuestos de
// docs/BACKEND.md §8.1: 50 ms de decode, 200/500 ms del claim, 300 ms p95 de POST /sales y el
// umbral de 3 s del escaneo.
var defaultBucketBounds = []float64{10, 25, 50, 100, 200, 300, 500, 1000, 3000, 10000}

// Registry conserva el registro por ruta. Es seguro para uso concurrente.
type Registry struct {
	mu       sync.Mutex
	capacity int
	bounds   []float64
	routes   map[string]*routeMetrics
}

// routeMetrics es el estado de UNA ruta. Los campos van protegidos por el mutex del registry.
type routeMetrics struct {
	samples      []float64
	next         int
	requests     uint64
	count        uint64
	sumMillis    float64
	bucketCounts []uint64
	statuses     map[int]uint64
}

// NewRegistry construye el registro. Una capacidad no positiva cae al valor por defecto: un
// anillo de tamaño cero no registraría nada y el operador creería que no hay tráfico.
func NewRegistry(capacity int) *Registry {
	if capacity <= 0 {
		capacity = DefaultSampleCapacity
	}
	return &Registry{
		capacity: capacity,
		bounds:   append([]float64(nil), defaultBucketBounds...),
		routes:   make(map[string]*routeMetrics),
	}
}

// Observe registra una petición de la ruta con su estado y duración.
func (r *Registry) Observe(route string, status int, duration time.Duration) {
	if r == nil {
		return
	}
	if strings.TrimSpace(route) == "" {
		route = "unknown"
	}
	millis := float64(duration) / float64(time.Millisecond)

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.routes[route]
	if !ok {
		entry = &routeMetrics{
			samples:      make([]float64, 0, r.capacity),
			bucketCounts: make([]uint64, len(r.bounds)),
			statuses:     make(map[int]uint64),
		}
		r.routes[route] = entry
	}

	if len(entry.samples) < r.capacity {
		entry.samples = append(entry.samples, millis)
	} else {
		entry.samples[entry.next] = millis
		entry.next = (entry.next + 1) % r.capacity
	}
	entry.requests++
	entry.count++
	entry.sumMillis += millis
	entry.statuses[status]++
	for index, bound := range r.bounds {
		if millis <= bound {
			entry.bucketCounts[index]++
		}
	}
}

// Bucket es un borde del histograma acumulativo.
type Bucket struct {
	// Le es el borde superior en milisegundos; +Inf se representa con math.Inf(1) en el render.
	Le    float64
	Count uint64
}

// RouteSnapshot es la vista inmutable de una ruta.
type RouteSnapshot struct {
	Route    string
	Requests uint64
	P50      float64
	P95      float64
	SumMs    float64
	Buckets  []Bucket
	Statuses map[int]uint64
}

// Snapshot devuelve una copia ordenada por ruta de todo el registro.
func (r *Registry) Snapshot() []RouteSnapshot {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	snapshots := make([]RouteSnapshot, 0, len(r.routes))
	for route, entry := range r.routes {
		samples := append([]float64(nil), entry.samples...)
		sort.Float64s(samples)

		buckets := make([]Bucket, len(entry.bucketCounts))
		for index, count := range entry.bucketCounts {
			buckets[index] = Bucket{Le: r.bounds[index], Count: count}
		}

		statuses := make(map[int]uint64, len(entry.statuses))
		for status, count := range entry.statuses {
			statuses[status] = count
		}

		snapshots = append(snapshots, RouteSnapshot{
			Route:    route,
			Requests: entry.requests,
			P50:      percentile(samples, 0.50),
			P95:      percentile(samples, 0.95),
			SumMs:    entry.sumMillis,
			Buckets:  buckets,
			Statuses: statuses,
		})
	}

	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Route < snapshots[j].Route })
	return snapshots
}

// percentile calcula el percentil con el método nearest-rank sobre muestras ya ordenadas: el
// menor valor cuya posición acumulada alcanza q. Con una muestra devuelve esa muestra.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	// nearest-rank: el menor valor cuya posición acumulada alcanza q.
	rank := int(math.Ceil(q * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// Handler responde el estado actual en formato de texto de Prometheus.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = r.Render(w)
	})
}

// Render escribe las métricas en texto de Prometheus. El orden es estable para que un diff de
// dos capturas signifique algo.
func (r *Registry) Render(w io.Writer) error {
	snapshots := r.Snapshot()

	if _, err := fmt.Fprintln(w, "# HELP http_request_duration_ms request duration in milliseconds"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE http_request_duration_ms histogram"); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		label := escapeLabel(snapshot.Route)
		for _, bucket := range snapshot.Buckets {
			if _, err := fmt.Fprintf(w, "http_request_duration_ms_bucket{route=\"%s\",le=\"%s\"} %d\n", label, formatBound(bucket.Le), bucket.Count); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "http_request_duration_ms_bucket{route=\"%s\",le=\"+Inf\"} %d\n", label, snapshot.Requests); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "http_request_duration_ms_count{route=\"%s\"} %d\n", label, snapshot.Requests); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "http_request_duration_ms_sum{route=\"%s\"} %s\n", label, formatFloat(snapshot.SumMs)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "http_request_duration_ms{route=\"%s\",quantile=\"0.5\"} %s\n", label, formatFloat(snapshot.P50)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "http_request_duration_ms{route=\"%s\",quantile=\"0.95\"} %s\n", label, formatFloat(snapshot.P95)); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w, "# HELP http_requests_total total requests by route and status"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE http_requests_total counter"); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		label := escapeLabel(snapshot.Route)
		statuses := make([]int, 0, len(snapshot.Statuses))
		for status := range snapshot.Statuses {
			statuses = append(statuses, status)
		}
		sort.Ints(statuses)
		for _, status := range statuses {
			if _, err := fmt.Fprintf(w, "http_requests_total{route=\"%s\",status=\"%d\"} %d\n", label, status, snapshot.Statuses[status]); err != nil {
				return err
			}
		}
	}
	return nil
}

// formatBound imprime un borde sin decimales cuando es entero: `le="300"` se lee mejor que
// `le="300.000000"` y Prometheus acepta ambas.
func formatBound(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// formatFloat imprime un valor con hasta tres decimales, que es resolución de sobra para
// milisegundos y evita ruido de coma flotante.
func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}

// escapeLabel escapa lo mínimo que el formato exige dentro de un valor de etiqueta.
func escapeLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}
