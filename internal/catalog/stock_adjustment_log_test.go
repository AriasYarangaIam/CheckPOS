// Seguridad del registro del endpoint de merma (W8-BE-4, S10): la línea estructurada nombra los
// identificadores que ya viajan en el acuse y NO repite el cuerpo de la petición. Un payload
// volcado en un log termina en un agregador y sobrevive a la petición que lo produjo.
package catalog_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// TestStockAdjustmentNeverLogsThePayload: el cuerpo del operador —cantidad, motivo, notas— no se
// copia al registro; la traza queda con los ids y el tenant.
func TestStockAdjustmentNeverLogsThePayload(t *testing.T) {
	var logBuffer strings.Builder

	handler, err := catalog.NewStockAdjustmentHandler(&stubAdjustmentStore{ack: []byte(`{"adjustment":{},"product":{}}`)},
		slog.New(slog.NewJSONHandler(&logBuffer, nil)))
	if err != nil {
		t.Fatalf("NewStockAdjustmentHandler: %v", err)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /stock-adjustments", identity.RequireAccessToken(issuer, handler))
		}),
	)

	recorder := doAdjustmentRequest(t, router, productToken(t, issuer),
		adjustmentBody(adjustmentID, productIDForTest, "-7.250", "manual", "Daño en vitrina"), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	logged := logBuffer.String()
	for _, forbidden := range []string{"-7.250", "Daño en vitrina", `\"reason\"`, `\"qty_delta\"`} {
		if strings.Contains(logged, forbidden) {
			t.Errorf("el registro repite el cuerpo de la petición (%q): %q", forbidden, logged)
		}
	}
	if !strings.Contains(logged, adjustmentID) || !strings.Contains(logged, productIDForTest) {
		t.Errorf("el registro no nombra el ajuste y el producto: %q", logged)
	}
}
