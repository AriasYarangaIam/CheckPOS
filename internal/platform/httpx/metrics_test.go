package httpx_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/metrics"
)

// Pruebas de la conexión entre el mux y el registro de latencias (Sprint 9, W9-BE-1): se mide
// el patrón de la ruta, las rutas de operación no se miden y /metrics no expone payloads.

func TestRouterWithMetricsRecordsBusinessRoutesOnly(t *testing.T) {
	registry := metrics.NewRegistry(0)
	var logBuffer strings.Builder

	router := httpx.NewRouterWithMetrics(newLogger(&logBuffer), &stubChecker{}, registry,
		httpx.WithMetrics(registry),
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("GET /products", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				httpx.WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
			}))
		}),
	)

	for _, path := range []string{"/healthz", "/readyz", "/products"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, recorder.Code)
		}
	}

	snapshots := registry.Snapshot()
	if len(snapshots) != 1 || snapshots[0].Route != "GET /products" {
		t.Fatalf("snapshots = %+v, want only the business route GET /products", snapshots)
	}
	if snapshots[0].Requests != 1 {
		t.Fatalf("requests = %d, want 1", snapshots[0].Requests)
	}
}

func TestMetricsRouteIsAnOperationRouteThatDoesNotMeasureItself(t *testing.T) {
	registry := metrics.NewRegistry(0)
	router := httpx.NewRouterWithMetrics(newLogger(&strings.Builder{}), &stubChecker{}, registry,
		httpx.WithMetrics(registry),
	)

	for attempt := 0; attempt < 3; attempt++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("GET /metrics = %d, want 200", recorder.Code)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
			t.Fatalf("Content-Type = %q, want text/plain", contentType)
		}
	}

	if snapshots := registry.Snapshot(); len(snapshots) != 0 {
		t.Fatalf("snapshots = %+v, want /metrics not measured", snapshots)
	}
}

func TestMetricsOutputCarriesNoQueryPayload(t *testing.T) {
	registry := metrics.NewRegistry(0)
	router := httpx.NewRouterWithMetrics(newLogger(&strings.Builder{}), &stubChecker{}, registry,
		httpx.WithMetrics(registry),
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /auth/login", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				httpx.WriteError(w, httpx.Unauthenticated("invalid credentials"))
			}))
		}),
	)

	request := httptest.NewRequest(http.MethodPost, "/auth/login?phone=999999999&password=supersecret", strings.NewReader(`{"password":"supersecret"}`))
	router.ServeHTTP(httptest.NewRecorder(), request)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body, _ := io.ReadAll(recorder.Body)
	for _, forbidden := range []string{"supersecret", "999999999", `{"password"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("metrics body contains %q:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), `route="POST /auth/login"`) {
		t.Fatalf("metrics body does not contain the route pattern:\n%s", body)
	}
}

func TestRouterWithoutMetricsKeepsTheOperationSurfaceUnchanged(t *testing.T) {
	router := httpx.NewRouter(newLogger(&strings.Builder{}), &stubChecker{})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics without a registry = %d, want 404", recorder.Code)
	}
}
