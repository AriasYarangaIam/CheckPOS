// Router REAL de las pruebas de integración del módulo Fiado: el mismo cableado que
// cmd/api monta, sobre el pool de `pos_app` de la base desechable.
package fiado_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/fiado"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// newFiadoIntegrationRouter monta el router REAL con los stores de POSTGRES sobre el pool de
// `pos_app` de la base desechable, igual que cmd/api los monta.
func newFiadoIntegrationRouter(t *testing.T, tenants *postgres.TenantPool, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	movements, err := fiado.NewFiadoMovementHandler(fiado.NewPostgresMovementStore(tenants), logger)
	if err != nil {
		t.Fatalf("NewFiadoMovementHandler: %v", err)
	}

	tokenIssuer := newTestIssuer(t)

	return httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /fiado-movements", identity.RequireAccessToken(tokenIssuer, movements))
		}),
	), tokenIssuer
}
