package metrics

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// Pruebas del registro de latencias (Sprint 9, W9-BE-1). Lo que se afirma es el método del
// percentil, la acumulación del histograma y el límite de memoria: el registro no puede crecer
// con el tráfico ni mentir sobre la ventana que conserva.

func TestPercentileUsesNearestRank(t *testing.T) {
	samples := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	if got := percentile(samples, 0.50); got != 50 {
		t.Fatalf("p50 = %v, want 50", got)
	}
	if got := percentile(samples, 0.95); got != 100 {
		t.Fatalf("p95 = %v, want 100", got)
	}
	if got := percentile([]float64{7}, 0.95); got != 7 {
		t.Fatalf("p95 of one sample = %v, want 7", got)
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Fatalf("p50 of no samples = %v, want 0", got)
	}
}

func TestObserveComputesP50AndP95(t *testing.T) {
	registry := NewRegistry(0)
	for millis := 1; millis <= 100; millis++ {
		registry.Observe("GET /products", 200, time.Duration(millis)*time.Millisecond)
	}

	snapshots := registry.Snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1 route", len(snapshots))
	}
	if snapshots[0].P50 != 50 || snapshots[0].P95 != 95 {
		t.Fatalf("p50/p95 = %v/%v, want 50/95", snapshots[0].P50, snapshots[0].P95)
	}
	if snapshots[0].Requests != 100 {
		t.Fatalf("requests = %d, want 100", snapshots[0].Requests)
	}
}

func TestBucketsAreCumulative(t *testing.T) {
	registry := NewRegistry(0)
	for _, millis := range []int{5, 60, 400} {
		registry.Observe("POST /sales", 201, time.Duration(millis)*time.Millisecond)
	}

	snapshot := registry.Snapshot()[0]
	want := map[float64]uint64{10: 1, 25: 1, 50: 1, 100: 2, 200: 2, 300: 2, 500: 3, 1000: 3, 3000: 3, 10000: 3}
	for _, bucket := range snapshot.Buckets {
		if bucket.Count != want[bucket.Le] {
			t.Fatalf("bucket le=%v count = %d, want %d", bucket.Le, bucket.Count, want[bucket.Le])
		}
	}
}

func TestRingIsBoundedAndKeepsTheLatestSamples(t *testing.T) {
	registry := NewRegistry(3)
	for millis := 1; millis <= 6; millis++ {
		registry.Observe("POST /sales", 200, time.Duration(millis)*time.Millisecond)
	}

	snapshot := registry.Snapshot()[0]
	if snapshot.Requests != 6 {
		t.Fatalf("requests = %d, want the monotonic total 6", snapshot.Requests)
	}
	// Retiene las últimas tres muestras: 4, 5 y 6; p50 = 5 y p95 = 6.
	if snapshot.P50 != 5 || snapshot.P95 != 6 {
		t.Fatalf("p50/p95 = %v/%v, want 5/6 from the retained window", snapshot.P50, snapshot.P95)
	}
}

func TestConcurrentObserveIsSafe(t *testing.T) {
	registry := NewRegistry(128)

	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := 0; index < 100; index++ {
				registry.Observe("GET /products", 200, time.Millisecond)
			}
		}()
	}
	wait.Wait()

	if got := registry.Snapshot()[0].Requests; got != 800 {
		t.Fatalf("requests = %d, want 800", got)
	}
}

func TestRenderIsPrometheusTextAndEscapesLabels(t *testing.T) {
	registry := NewRegistry(0)
	registry.Observe(`POST /products "unexpected"`, 422, 120*time.Millisecond)

	var builder strings.Builder
	if err := registry.Render(&builder); err != nil {
		t.Fatalf("Render: %v", err)
	}
	output := builder.String()

	for _, want := range []string{
		"# TYPE http_request_duration_ms histogram",
		`http_request_duration_ms_bucket{route="POST /products \"unexpected\"",le="+Inf"} 1`,
		`http_request_duration_ms_count{route="POST /products \"unexpected\""} 1`,
		`http_request_duration_ms{route="POST /products \"unexpected\"",quantile="0.95"} 120.000`,
		`http_requests_total{route="POST /products \"unexpected\"",status="422"} 1`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("render does not contain %q:\n%s", want, output)
		}
	}
}

func TestRenderCarriesNoRequestPayload(t *testing.T) {
	registry := NewRegistry(0)
	registry.Observe("POST /auth/login", 401, time.Millisecond)

	var builder strings.Builder
	if err := registry.Render(&builder); err != nil {
		t.Fatalf("Render: %v", err)
	}
	output := builder.String()

	// El registro solo conoce la ruta, el estado y la duración: no hay campo para un cuerpo,
	// un PIN ni una cabecera de credenciales.
	for _, forbidden := range []string{"password", "pin", "Bearer", "secret"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("render contains %q, want no payload", forbidden)
		}
	}
}

func TestEmptyRegistryRendersWithoutFailing(t *testing.T) {
	var builder strings.Builder
	if err := NewRegistry(0).Render(&builder); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(builder.String(), "# TYPE http_requests_total counter") {
		t.Fatalf("empty render = %q, want the metric type headers", builder.String())
	}
}
