package catalog

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// Este archivo implementa la superficie HTTP de la mediación del GTIN (W3-BE-2,
// docs/DATABASE.md §5.6; SPRINTS.md W3-BE-2): GET /gtin-lookup?gtin=... es una ruta
// AUTENTICADA y de SOLO LECTURA, y el tenant que la consulta es irrelevante para el
// resultado —el catálogo global es de la plataforma, no de ningún tenant—.
//
// El contrato de respuesta fija a lo sumo UN producto: la forma es una fila en la clave
// «product» del cuerpo, jamás la lista «data» que el catálogo del tenant usa (§4.5). Una
// mediación que pudiera responder con más de un producto estaría contradiciendo la UNIQUE
// de `global_barcodes.gtin`, así que la forma es literal.
//
// El camino de FALLO de la mediación es la AUSENCIA en el catálogo global: se responde 404
// con el mismo sobre genérico que el resto de las lecturas —no 200 con una fila vacía, no
// 500— porque para el dispositivo es un dato corriente, sobre todo offline.

// errNoLookupClaims es la causa que se registra cuando la ruta corre sin el middleware de
// token: es un defecto de cableado, no una petición anónima.
var errNoLookupClaims = errors.New("the route ran without the access token middleware: the lookup is a business route")

// GtinLookupHandler atiende GET /gtin-lookup sobre un mediador.
type GtinLookupHandler struct {
	lookup GtinLookup
	logger *slog.Logger
}

// NewGtinLookupHandler construye el handler sobre el mediador del catálogo global.
func NewGtinLookupHandler(lookup GtinLookup, logger *slog.Logger) (*GtinLookupHandler, error) {
	if lookup == nil {
		return nil, errors.New("the gtin lookup handler needs a gtin mediator")
	}
	if logger == nil {
		return nil, errors.New("the gtin lookup handler needs a logger")
	}
	return &GtinLookupHandler{lookup: lookup, logger: logger}, nil
}

// gtinProductBody es la fila global del contrato de la mediación: las columnas de §4
// `global_products` que el dispositivo puede usar para resolver la adopción (§4.6).
type gtinProductBody struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Attributes json.RawMessage `json:"attributes"`
}

// gtinResponse es la forma de la mediación: a lo sumo UN producto, en la clave «product».
type gtinResponse struct {
	Product gtinProductBody `json:"product"`
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *GtinLookupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// El tenant del token no cambia el resultado —el catálogo es de la plataforma— pero la
	// RUTA es de negocio y la autenticación completa es igual a las demás: sin claims, la
	// respuesta es un 500 y nada corre.
	if _, ok := identity.ClaimsFromContext(r.Context()); !ok {
		httpx.WriteError(w, httpx.Internal(errNoLookupClaims, h.logger))
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, httpx.NotFound("this route only answers GET"))
		return
	}

	// El gtin con espacios alrededor no es un código distinto: se trimea el PROPIO parámetro,
	// y un valor que queda vacío es un cuerpo inservible (422) antes de tocar la base.
	gtin := strings.TrimSpace(r.URL.Query().Get("gtin"))
	if gtin == "" {
		httpx.WriteError(w, validationForField("gtin is required", "gtin"))
		return
	}

	product, err := h.lookup.Resolve(r.Context(), gtin)
	switch {
	case errors.Is(err, ErrUnknownGtin):
		// El camino de fallo esperado: el gtin que el catálogo global no conoce. 404, que
		// es lo mismo que una fila de negocio inexistente (§4.1), sin distinguir.
		httpx.WriteError(w, httpx.NotFound("the gtin is not in the global catalog"))
		return
	case err != nil:
		// Un fallo de la capa de datos (el pool de plataforma no responde) NO es un miss:
		// es un 500 con el sobre genérico y la causa en el registro del operador. Perder
		// los 404 acá haría al dispositivo confundir «no existe» con «no pude mirar».
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, gtinResponse{Product: gtinProductBody{
		ID:         product.ID.String(),
		Name:       product.Name,
		Attributes: attributesToJSON(product.Attributes),
	}})
}
