package httpx_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// newLogger es el logger en el que los tests del router capturan sus líneas.
func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

// discardLogger es el logger que necesitan los constructores que reportan una causa.
func discardLogger() *slog.Logger {
	return newLogger(io.Discard)
}

// TestErrorEnvelopeIsExactlyTheContractShape fija el JSON byte a byte del sobre de error
// único (docs/BACKEND.md §4.1): sin clave envolvente, sin `null`, sin texto plano, y
// `details` presente solo cuando lleva algo.
func TestErrorEnvelopeIsExactlyTheContractShape(t *testing.T) {
	cases := []struct {
		name     string
		err      *httpx.Error
		wantBody string
	}{
		{
			name:     "without details",
			err:      httpx.Validation("invalid body", nil),
			wantBody: `{"error":{"code":"VALIDATION_ERROR","message":"invalid body"}}` + "\n",
		},
		{
			name:     "with details",
			err:      httpx.Validation("invalid body", map[string]string{"field": "name"}),
			wantBody: `{"error":{"code":"VALIDATION_ERROR","message":"invalid body","details":{"field":"name"}}}` + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpx.WriteError(rec, tc.err)

			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("cuerpo =\n  %q\nwant\n  %q", got, tc.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want un tipo de medio application/json", ct)
			}
		})
	}
}

// TestErrorStatusMapping fija constructor -> estado HTTP -> código de máquina. Son los
// códigos de negocio de docs/BACKEND.md §4.1 más el 503 operativo del sondeo de
// preparación.
func TestErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        *httpx.Error
		wantStatus int
		wantCode   string
	}{
		{name: "unauthenticated", err: httpx.Unauthenticated("authentication required"), wantStatus: http.StatusUnauthorized, wantCode: httpx.CodeUnauthenticated},
		{name: "not found", err: httpx.NotFound("product not found"), wantStatus: http.StatusNotFound, wantCode: httpx.CodeNotFound},
		{name: "conflict", err: httpx.Conflict("request already in flight"), wantStatus: http.StatusConflict, wantCode: httpx.CodeConflict},
		{name: "validation", err: httpx.Validation("invalid body", nil), wantStatus: http.StatusUnprocessableEntity, wantCode: httpx.CodeValidationError},
		{name: "internal", err: httpx.Internal(errors.New("pq: relation \"secrets\" does not exist"), discardLogger()), wantStatus: http.StatusInternalServerError, wantCode: httpx.CodeInternalError},
		{name: "not ready", err: httpx.NotReady(), wantStatus: http.StatusServiceUnavailable, wantCode: httpx.CodeNotReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Status != tc.wantStatus {
				t.Errorf("Status = %d, want %d", tc.err.Status, tc.wantStatus)
			}
			if tc.err.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", tc.err.Code, tc.wantCode)
			}

			rec := httptest.NewRecorder()
			httpx.WriteError(rec, tc.err)

			if rec.Code != tc.wantStatus {
				t.Errorf("estado HTTP = %d, want %d", rec.Code, tc.wantStatus)
			}

			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("el cuerpo no es el sobre de error: %v (%q)", err, rec.Body.String())
			}
			if body.Error.Code != tc.wantCode {
				t.Errorf("body error.code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if body.Error.Message == "" {
				t.Error("body error.message está vacío; el sobre siempre lleva un mensaje humano")
			}
		})
	}
}

// TestInternalNeverLeaksTheCause cubre la regla de que un 500 "nunca expone detalles
// internos" (docs/BACKEND.md §4.1): la causa llega al log, no al cliente.
func TestInternalNeverLeaksTheCause(t *testing.T) {
	const secret = "pq: password authentication failed for user \"pos_app\""
	cause := errors.New(secret)

	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	rec := httptest.NewRecorder()
	httpx.WriteError(rec, httpx.Internal(cause, logger))

	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("el cuerpo del 500 filtró la causa: %q", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "password authentication failed") {
		t.Errorf("la causa no llegó al log del operador; log = %q", logBuf.String())
	}
}

// TestErrorImplementsError comprueba que el sobre se pueda usar como un error normal.
func TestErrorImplementsError(t *testing.T) {
	var err error = httpx.NotFound("product not found")

	if got := err.Error(); !strings.Contains(got, httpx.CodeNotFound) || !strings.Contains(got, "product not found") {
		t.Errorf("Error() = %q, want que lleve el código y el mensaje", got)
	}
}

func TestWriteJSONWritesTheStatusAndOmitsEmptyDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, http.StatusCreated, map[string]string{"status": "created"})

	if rec.Code != http.StatusCreated {
		t.Errorf("estado HTTP = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got, want := rec.Body.String(), `{"status":"created"}`+"\n"; got != want {
		t.Errorf("cuerpo = %q, want %q", got, want)
	}
}
