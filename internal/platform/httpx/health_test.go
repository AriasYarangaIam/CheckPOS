package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// stubChecker es lo mínimo que un sondeo de preparación necesita: algo que responda si
// sus dependencias están alcanzables ahora mismo.
type stubChecker struct {
	err   error
	calls int
}

func (s *stubChecker) Ping(ctx context.Context) error {
	s.calls++
	return s.err
}

func TestHealthHandlerIsLivenessOnly(t *testing.T) {
	rec := httptest.NewRecorder()

	// Un contexto ya cancelado demuestra que el sondeo de vida no consulta nada externo:
	// si tocara PostgreSQL, esta petición no podría responder 200.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	httpx.HealthHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("estado HTTP = %d, want 200", rec.Code)
	}
	if got, want := rec.Body.String(), `{"status":"ok"}`+"\n"; got != want {
		t.Errorf("cuerpo = %q, want %q", got, want)
	}
}

func TestReadinessHandlerReportsTheDependencyState(t *testing.T) {
	cases := []struct {
		name       string
		checkerErr error
		wantStatus int
		wantCode   string
	}{
		{name: "dependencies reachable", checkerErr: nil, wantStatus: http.StatusOK},
		{name: "postgres unreachable", checkerErr: errors.New("dial tcp 127.0.0.1:5433: connectex: connection refused"), wantStatus: http.StatusServiceUnavailable, wantCode: httpx.CodeNotReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubChecker{err: tc.checkerErr}
			rec := httptest.NewRecorder()

			httpx.ReadinessHandler(checker).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if checker.calls != 1 {
				t.Errorf("el sondeo de preparación consultó la dependencia %d veces, want exactamente 1", checker.calls)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("estado HTTP = %d, want %d", rec.Code, tc.wantStatus)
			}

			if tc.wantCode == "" {
				if got, want := rec.Body.String(), `{"status":"ok"}`+"\n"; got != want {
					t.Errorf("cuerpo = %q, want %q", got, want)
				}
				return
			}

			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("un 503 debe usar el sobre de error único: %v (%q)", err, rec.Body.String())
			}
			if body.Error.Code != tc.wantCode {
				t.Errorf("body error.code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			// Un fallo de preparación lo lee un sondeo anónimo: no puede repetir el error
			// del driver, que lleva host y usuario.
			if strings.Contains(rec.Body.String(), "127.0.0.1") {
				t.Errorf("el cuerpo del 503 filtró el error de conexión: %q", rec.Body.String())
			}
		})
	}
}

func TestRouterServesBothProbes(t *testing.T) {
	router := httpx.NewRouter(discardLogger(), &stubChecker{})

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200 (cuerpo %q)", path, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRouterAnswersUnknownRoutesWithTheEnvelope cubre la regla de que ningún endpoint
// devuelve texto plano como error: el fallback del mux también es el sobre, y no el
// "404 page not found" de net/http.
func TestRouterAnswersUnknownRoutesWithTheEnvelope(t *testing.T) {
	router := httpx.NewRouter(discardLogger(), &stubChecker{})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("estado HTTP = %d, want 404 (cuerpo %q)", rec.Code, rec.Body.String())
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
}

// TestRouterPropagatesTheRequestID cubre docs/BACKEND.md §7.9: el identificador de
// petición se adopta o se genera en el transporte, viaja a los logs y se devuelve en la
// respuesta para poder correlacionar.
func TestRouterPropagatesTheRequestID(t *testing.T) {
	t.Run("an inbound id is adopted", func(t *testing.T) {
		var logBuf strings.Builder
		router := httpx.NewRouter(newLogger(&logBuf), &stubChecker{})

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Request-Id", "caller-supplied-id")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if got := rec.Header().Get("X-Request-Id"); got != "caller-supplied-id" {
			t.Errorf("X-Request-Id de la respuesta = %q, want el id entrante devuelto tal cual", got)
		}
		if !strings.Contains(logBuf.String(), "caller-supplied-id") {
			t.Errorf("la línea de log no lleva el identificador de petición; log = %q", logBuf.String())
		}
		if !strings.Contains(logBuf.String(), "status=200") {
			t.Errorf("la línea de log no lleva el estado; log = %q", logBuf.String())
		}
	})

	t.Run("a missing id is generated", func(t *testing.T) {
		var logBuf strings.Builder
		router := httpx.NewRouter(newLogger(&logBuf), &stubChecker{})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

		id := rec.Header().Get("X-Request-Id")
		if len(id) != 32 {
			t.Errorf("X-Request-Id generado = %q, want 32 caracteres hexadecimales", id)
		}
		if !strings.Contains(logBuf.String(), id) {
			t.Errorf("el id generado %q no aparece en el log %q", id, logBuf.String())
		}
	})
}

func TestRouterLogsOneRecordPerRequest(t *testing.T) {
	var logBuf strings.Builder
	router := httpx.NewRouter(newLogger(&logBuf), &stubChecker{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactamente 1 línea de log por petición, hay %d: %q", len(lines), logBuf.String())
	}
	for _, want := range []string{"/healthz", "status=200", "duration"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("la línea de log %q no contiene %q", lines[0], want)
		}
	}
}

// TestRouterLoggerNeverReceivesSecrets protege la regla de que el transporte registra la
// ruta, nunca el cuerpo ni las credenciales.
func TestRouterLoggerNeverReceivesSecrets(t *testing.T) {
	var logBuf strings.Builder
	router := httpx.NewRouter(newLogger(&logBuf), &stubChecker{})

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"hunter2"}`))
	req.Header.Set("Authorization", "Bearer hunter2")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if strings.Contains(logBuf.String(), "hunter2") {
		t.Errorf("el log de peticiones filtró credenciales: %q", logBuf.String())
	}
}
