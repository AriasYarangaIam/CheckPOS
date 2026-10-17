package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// TestRouterMountsModuleRoutes cubre la costura que Sprint 2 necesita para exponer
// `POST /auth/login`: un módulo monta sus patrones sin que este paquete los conozca, y los dos
// sondeos y el fallback siguen comportándose igual.
func TestRouterMountsModuleRoutes(t *testing.T) {
	var logBuf strings.Builder

	mounted := 0
	router := httpx.NewRouter(newLogger(&logBuf), &stubChecker{}, httpx.WithRoutes(func(mux *http.ServeMux) {
		mux.Handle("POST /auth/login", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mounted++
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"token": "issued"})
		}))
	}))

	t.Run("the mounted route answers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader("{}")))

		if rec.Code != http.StatusOK {
			t.Fatalf("POST /auth/login = %d, want 200 (cuerpo %q)", rec.Code, rec.Body.String())
		}
		if mounted != 1 {
			t.Errorf("el handler montado corrió %d vez/veces, want 1", mounted)
		}
	})

	t.Run("the operation probes keep working", func(t *testing.T) {
		for _, path := range []string{"/healthz", "/readyz"} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200 (cuerpo %q)", path, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("the mounted route is method scoped", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))

		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET /auth/login = %d, want que el patrón del módulo sea solo para POST", rec.Code)
		}
	})

	t.Run("an unknown route still answers with the envelope", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("estado = %d, want 404", rec.Code)
		}

		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("el fallback no usa el sobre de error: %v (%q)", err, rec.Body.String())
		}
		if body.Error.Code != httpx.CodeNotFound {
			t.Errorf("body error.code = %q, want %q", body.Error.Code, httpx.CodeNotFound)
		}
	})

	t.Run("the mounted route is logged like any other", func(t *testing.T) {
		logBuf.Reset()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader("{}")))

		if rec.Header().Get(httpx.RequestIDHeader) == "" {
			t.Error("la respuesta de la ruta montada no lleva X-Request-Id")
		}
		if !strings.Contains(logBuf.String(), "/auth/login") || !strings.Contains(logBuf.String(), "status=200") {
			t.Errorf("la ruta montada no se registró como las demás: %q", logBuf.String())
		}
	})
}
