package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// W3-BE-2: la lectura del catálogo global es MEDIADA POR API. El tenant nunca consulta las
// tablas globales por sí mismo: `pos_app` no tiene ninguna concesión sobre ellas, y la
// consulta corre por el rol `pos_platform` con conexión propia (docs/DATABASE.md §5.6,
// §7.7). El stub acá responde con AT MOST UN producto —global_barcodes tiene gtin UNIQUE,
// así que una mediación que devolviera una lista estaría mintiendo sobre el índice que la
// respalda—, y el camino de fallo del mediador (200 con datos ausentes) queda verificado
// por prueba.
//
// El GTIN viaja como el cliente lo escribió: TEXT preserva los ceros a la izquierda
// (docs/DATABASE.md §4 `global_barcodes`), así que no se normaliza ni se valida contra un
// formato de dígitos.

// stubLookup es el mediador de las pruebas unitarias: registra el gtin que el handler pidió
// y devuelve lo que la prueba fijó —la fila global o el fallo— sin base.
type stubLookup struct {
	resolveCalls []string

	product catalog.GlobalProduct
	err     error
}

func (s *stubLookup) Resolve(_ context.Context, gtin string) (catalog.GlobalProduct, error) {
	s.resolveCalls = append(s.resolveCalls, gtin)
	if s.err != nil {
		return catalog.GlobalProduct{}, s.err
	}
	return s.product, nil
}

// newGtinRouter arma el router REAL con la ruta de mediación detrás del middleware de
// token, igual que cmd/api la monta.
func newGtinRouter(t *testing.T, lookup catalog.GtinLookup, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := catalog.NewGtinLookupHandler(lookup, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewGtinLookupHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("GET /gtin-lookup", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// gtinGlobalProduct es el global de referencia para las pruebas de la mediación.
func gtinGlobalProduct() catalog.GlobalProduct {
	return catalog.GlobalProduct{
		ID:         uuid.MustParse("019a0000-0000-7000-8000-0000000000d1"),
		Name:       "yerba playa 1kg",
		Attributes: []byte(`{"marca":"playa"}`),
	}
}

// TestGtinLookupAnswers200WithTheSingleProduct: un gtin del catálogo global vuelve con UN
// producto —la clave product en el cuerpo, con id, name y atributos— y el mediador recibió
// el gtin tal como el cliente lo escribió.
func TestGtinLookupAnswers200WithTheSingleProduct(t *testing.T) {
	lookup := &stubLookup{product: gtinGlobalProduct()}
	var logBuf strings.Builder
	router, issuer := newGtinRouter(t, lookup, &logBuf)
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodGet, "/gtin-lookup?gtin=7791009010014", token, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if len(lookup.resolveCalls) != 1 || lookup.resolveCalls[0] != "7791009010014" {
		t.Errorf("el mediador corrió %v, want el gtin escrito por el cliente", lookup.resolveCalls)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("la respuesta no es un objeto JSON: %v (%q)", err, recorder.Body.String())
	}
	productRaw, ok := decoded["product"]
	if !ok {
		t.Fatalf("la respuesta no trae la clave product: %q", recorder.Body.String())
	}
	if _, exists := decoded["data"]; exists {
		t.Errorf("la respuesta trae una lista: la mediación devuelve a LO SUMO un producto (%q)", recorder.Body.String())
	}

	var row struct {
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if err := json.Unmarshal(productRaw, &row); err != nil {
		t.Fatalf("product no es un objeto: %v (%q)", err, productRaw)
	}
	if row.ID != gtinGlobalProduct().ID.String() || row.Name != "yerba playa 1kg" {
		t.Errorf("product = id %q name %q, want el de la fila global", row.ID, row.Name)
	}
	if !strings.Contains(string(row.Attributes), "playa") {
		t.Errorf("attributes = %q, want el JSONB de la fila global", row.Attributes)
	}
}

// TestGtinLookupAnswers404OnTheMissPath: el gtin que el catálogo global no conoce es el
// camino de FALLO de la mediación y responde 404 genérico —no un 200 vacío, no una lista
// vacía, no un error 500 (docs/BACKEND.md §5.6; SPRINTS.md W3-BE-2).
func TestGtinLookupAnswers404OnTheMissPath(t *testing.T) {
	lookup := &stubLookup{err: catalog.ErrUnknownGtin}
	var logBuf strings.Builder
	router, issuer := newGtinRouter(t, lookup, &logBuf)
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodGet, "/gtin-lookup?gtin=7790000000000", token, "", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("estado = %d, want 404: la ausencia en el catálogo global no es un error del servidor (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeNotFound {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeNotFound)
	}
	if len(lookup.resolveCalls) != 1 {
		t.Errorf("el mediador corrió %d vez/veces, want 1: la ausencia se resuelve consultando", len(lookup.resolveCalls))
	}
}

// TestGtinLookupRejectsAGtinThatIsNotThere: sin el parámetro o con uno vacío no hay nada que
// resolver: 422 con el campo nombrado, y el mediador no corre.
func TestGtinLookupRejectsAGtinThatIsNotThere(t *testing.T) {
	cases := map[string]string{
		"absent param": "/gtin-lookup",
		"an empty one": "/gtin-lookup?gtin=",
		"only spaces":  "/gtin-lookup?gtin=%20%20",
	}

	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := &stubLookup{}
			router, issuer := newGtinRouter(t, lookup, &strings.Builder{})
			token := productToken(t, issuer)

			recorder := doCatalogRequest(t, router, http.MethodGet, target, token, "", nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(lookup.resolveCalls) != 0 {
				t.Errorf("el mediador corrió sin un gtin utilizable")
			}
			envelope := decodeEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			details, ok := envelope.Error.Details.(map[string]any)
			if !ok || details["field"] != "gtin" {
				t.Errorf("details = %#v, want un objeto que nombre el campo gtin", envelope.Error.Details)
			}
		})
	}
}

// TestGtinLookupNeedsAValidToken: la mediación es una ruta autenticada como cualquier otra
// de negocio (§5.6): sin token o con uno inservible responde 401 y el mediador no corre.
func TestGtinLookupNeedsAValidToken(t *testing.T) {
	cases := map[string]string{
		"no token":       "",
		"a fake token":   "no-soy-un-token",
	}

	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := &stubLookup{}
			router, _ := newGtinRouter(t, lookup, &strings.Builder{})

			recorder := doCatalogRequest(t, router, http.MethodGet, "/gtin-lookup?gtin=7791009010014", token, "", nil)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(lookup.resolveCalls) != 0 {
				t.Errorf("el mediador corrió %d vez/veces sin un token válido, want 0", len(lookup.resolveCalls))
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeUnauthenticated {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeUnauthenticated)
			}
		})
	}
}

// TestGtinLookupWithoutTheMiddlewareFailsClosed: un montaje sin el middleware no puede leer
// un tenant inexistente —el mismo fail-closed de toda ruta autenticada (W2-BE-2)—.
func TestGtinLookupWithoutTheMiddlewareFailsClosed(t *testing.T) {
	lookup := &stubLookup{product: gtinGlobalProduct()}
	var logBuf strings.Builder

	handler, err := catalog.NewGtinLookupHandler(lookup, slog.New(slog.NewTextHandler(&logBuf, nil)))
	if err != nil {
		t.Fatalf("NewGtinLookupHandler: %v", err)
	}

	recorder := doCatalogRequest(t, handler, http.MethodGet, "/gtin-lookup?gtin=7791009010014", "", "", nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500: sin claims no hay mediación que sirva (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if len(lookup.resolveCalls) != 0 {
		t.Errorf("el mediador corrió %d vez/veces sin claims, want 0", len(lookup.resolveCalls))
	}
	if !strings.Contains(logBuf.String(), "middleware") {
		t.Errorf("la causa no quedó registrada: %q", logBuf.String())
	}
}

// TestGtinLookupFailsClosedWhenTheMediatorIsBroken: un fallo del pool de plataforma no es
// un 404 ni pasa desapercibido: es 500 con el sobre genérico y la causa en el registro del
// operador.
func TestGtinLookupFailsClosedWhenTheMediatorIsBroken(t *testing.T) {
	lookup := &stubLookup{err: errors.New("the platform pool has no connection")}
	var logBuf strings.Builder
	router, issuer := newGtinRouter(t, lookup, &logBuf)
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodGet, "/gtin-lookup?gtin=7791009010014", token, "", nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "pool") {
		t.Errorf("el 500 filtró la causa interna: %q", recorder.Body.String())
	}
	if !strings.Contains(logBuf.String(), "pool") {
		t.Errorf("la causa no llegó al registro del operador: %q", logBuf.String())
	}
}
