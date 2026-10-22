package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// El handler de POST /sales/{id}/void (docs/BACKEND.md §4.10): el `{id}` de la ruta es la
// venta ORIGINAL y el `id` del cuerpo es la fila compensatoria. El tenant y el usuario suben
// SOLO por los claims del token, y el mapa de errores es el del contrato: 404 para un
// original que no existe o está fuera del tenant, 422 para un objetivo que no es una venta,
// 409 para la clave en vuelo.

// errNoVoidClaims es el 500 fail-closed: sin middleware no hay token, y sin token no hay ni
// tenant ni usuario de los que anular.
var errNoVoidClaims = errors.New("the void handler needs the token claims")

// SaleVoidHandler atiende POST /sales/{id}/void sobre un store.
type SaleVoidHandler struct {
	voids  SaleVoidStore
	logger *slog.Logger
}

// NewSaleVoidHandler construye el handler sobre el store de anulaciones.
func NewSaleVoidHandler(voids SaleVoidStore, logger *slog.Logger) (*SaleVoidHandler, error) {
	if voids == nil {
		return nil, errors.New("the void handler needs a sale void store")
	}
	if logger == nil {
		return nil, errors.New("the void handler needs a logger")
	}
	return &SaleVoidHandler{voids: voids, logger: logger}, nil
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *SaleVoidHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoVoidClaims, h.logger))
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}
	h.void(w, r, claims)
}

// voidRequest es el cuerpo de §4.10: la fila compensatoria y la fecha de negocio del
// original. `sold_on` no es redundante con la ruta —la PK de `sales` es
// `(sold_on, sale_id)` y la partición se elige por ella—: sin la fecha, la búsqueda
// recorrería todas las particiones.
type voidRequest struct {
	ID     string `json:"id"`
	SoldOn string `json:"sold_on"`
}

// void atiende el POST completo: la clave de claim, el cuerpo RAW (el hash de §6.2 va sobre
// los mismos bytes que llegaron), la validación de §4.10 y la llamada del store, que es LA
// transacción con el claim dentro.
func (h *SaleVoidHandler) void(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpx.WriteError(w, validation("the void requires the Idempotency-Key header", "Idempotency-Key"))
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the void fields of §4.10", nil))
		return
	}

	var request voidRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the void fields of §4.10", nil))
		return
	}

	void, invalid := parseVoidInput(&request, r.PathValue("id"))
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}

	// Una sola lectura del reloj: la fila compensatoria, el delta de stock y la fila del
	// claim comparten la marca del servidor (§2.5).
	ack, err := h.voids.VoidSale(r.Context(), claims.TenantID, claims.UserID, void, time.Now(), synckey.ClaimInput{
		Key:         key,
		PayloadHash: synckey.PayloadHash(key, raw),
	})
	switch {
	case errors.Is(err, ErrUnknownSale):
		// Fuera del tenant y inexistente son la misma respuesta (§4.10, §4.1).
		httpx.WriteError(w, httpx.NotFound("the sale does not exist in this tenant for that business date"))
		return
	case errors.Is(err, ErrVoidOfVoid):
		// F39: la anulación de una anulación se rechaza al crearla. El campo culpable es el
		// `{id}` de la ruta —el objetivo—, no el id de la fila nueva.
		httpx.WriteError(w, validation("a void can only target a sale: voids of voids are rejected", "id"))
		return
	case errors.Is(err, synckey.ErrPayloadMismatch):
		httpx.WriteError(w, &httpx.Error{
			Status:  http.StatusUnprocessableEntity,
			Code:    payloadMismatchCode,
			Message: "the same idempotency key was already used with a different payload",
			Details: map[string]string{"field": "Idempotency-Key"},
		})
		return
	case errors.Is(err, synckey.ErrInFlight):
		w.Header().Set("Retry-After", "2")
		httpx.WriteError(w, httpx.Conflict("the same idempotency key is still in flight: retry after backoff"))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("sale voided",
		"void_id", void.ID.String(),
		"original_sale_id", void.OriginalID.String(),
		"tenant_id", claims.TenantID.String(),
	)
	// El acuse es el cuerpo EXACTO que armó el store: fresco o snapshot almacenado, los dos
	// llegan byte a byte de la base.
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(ack))
}

// parseVoidInput valida el cuerpo y el `{id}` de la ruta contra §4.10.
//
// Un `{id}` que no es un uuid responde 404 y no 422: no puede nombrar ninguna venta, y la
// respuesta no debe distinguirlo de una venta inexistente o de otro tenant (§4.1). Los
// campos del CUERPO sí son un cuerpo inservible y responden 422 con su nombre.
func parseVoidInput(request *voidRequest, originalID string) (VoidInput, *httpx.Error) {
	voidID, err := uuid.Parse(request.ID)
	if err != nil {
		return VoidInput{}, validation("id must be the uuid of the void row", "id")
	}

	soldOn, err := time.Parse(dayFormat, request.SoldOn)
	if err != nil {
		return VoidInput{}, validation("sold_on must be the business date of the original sale as YYYY-MM-DD", "sold_on")
	}

	original, err := uuid.Parse(originalID)
	if err != nil {
		return VoidInput{}, httpx.NotFound("the sale does not exist in this tenant for that business date")
	}

	// La fila compensatoria no puede tener el id del original: la PK es (sold_on, sale_id),
	// así que el INSERT chocaría con la venta misma. Es un cuerpo inservible —dos ids
	// distintos son el contrato— y se nombra el campo del cuerpo.
	if voidID == original {
		return VoidInput{}, validation("id must be the uuid of the void row, different from the original sale id", "id")
	}

	return VoidInput{
		ID:         voidID,
		OriginalID: original,
		SoldOn:     soldOn,
	}, nil
}
