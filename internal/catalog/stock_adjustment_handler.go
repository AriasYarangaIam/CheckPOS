package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// El handler de POST /stock-adjustments (W8-BE-2; docs/BACKEND.md §4.14): el registro de una
// merma con el sobre de error único de §4.1. El tenant y el usuario suben SOLO por los claims del
// token, y `source` viaja en el cuerpo únicamente para poder RECHAZAR cualquier valor que no sea
// `manual`: el cliente no elige de dónde viene el delta.
//
// Los tres motivos son la lista EXACTA del contrato —`Vencimiento`, `Rotura en transporte` y
// `Daño en vitrina`—, y la merma no tiene captura de foto en esta versión (delta D5).

// signedQtyPattern es la forma exacta de un delta NUMERIC(10,3): hasta 7 dígitos enteros, hasta 3
// decimales, el punto como separador y un signo opcional que el handler exige negativo (§2.4).
var signedQtyPattern = regexp.MustCompile(`^-?[0-9]{1,7}(\.[0-9]{1,3})?$`)

// retryAfterSeconds es la espera que la superficie anuncia en el 409 del Branch C
// (docs/BACKEND.md §6.3), el mismo valor que el resto de los POST encolables.
const retryAfterSeconds = "2"

// errNoAdjustmentClaims es el 500 fail-closed: sin middleware no hay token, y sin token no hay
// tenant del que escribir.
var errNoAdjustmentClaims = errors.New("the stock adjustment handler needs the token claims")

// StockAdjustmentHandler atiende POST /stock-adjustments sobre un store.
type StockAdjustmentHandler struct {
	store  StockAdjustmentStore
	logger *slog.Logger
}

// NewStockAdjustmentHandler construye el handler completo o falla.
func NewStockAdjustmentHandler(store StockAdjustmentStore, logger *slog.Logger) (*StockAdjustmentHandler, error) {
	if store == nil {
		return nil, errors.New("the stock adjustment handler needs a store")
	}
	if logger == nil {
		return nil, errors.New("the stock adjustment handler needs a logger")
	}
	return &StockAdjustmentHandler{store: store, logger: logger}, nil
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *StockAdjustmentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoAdjustmentClaims, h.logger))
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}
	h.append(w, r, claims)
}

// stockAdjustmentRequest es el cuerpo de §4.14. Los opcionales son punteros para distinguir
// «ausente» de «presente con el valor cero», y `qty_delta` es puntero porque el ausente es un
// cuerpo inservible.
type stockAdjustmentRequest struct {
	ID        string  `json:"id"`
	ProductID string  `json:"product_id"`
	QtyDelta  *string `json:"qty_delta"`
	Source    *string `json:"source"`
	Reason    *string `json:"reason"`
	SoldOn    *string `json:"sold_on"`
	SaleID    *string `json:"sale_id"`
}

// append atiende el POST completo: la clave de claim, el cuerpo RAW (el hash de §6.2 va sobre los
// mismos bytes), la validación de §4.14 y la llamada del store, que es LA transacción con el
// claim dentro.
func (h *StockAdjustmentHandler) append(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpx.WriteError(w, validationForField("the stock adjustment requires the Idempotency-Key header", "Idempotency-Key"))
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the stock adjustment fields of §4.14", nil))
		return
	}

	var request stockAdjustmentRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the stock adjustment fields of §4.14", nil))
		return
	}

	adjustment, invalid := parseStockAdjustment(&request)
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}

	ack, err := h.store.Append(r.Context(), claims.TenantID, adjustment, time.Now(), synckey.ClaimInput{
		Key:         key,
		PayloadHash: synckey.PayloadHash(key, raw),
	})
	switch {
	case errors.Is(err, synckey.ErrPayloadMismatch):
		httpx.WriteError(w, &httpx.Error{
			Status:  http.StatusUnprocessableEntity,
			Code:    payloadMismatchCode,
			Message: "the same idempotency key was already used with a different payload",
			Details: map[string]string{"field": "Idempotency-Key"},
		})
		return
	case errors.Is(err, synckey.ErrInFlight):
		w.Header().Set("Retry-After", retryAfterSeconds)
		httpx.WriteError(w, httpx.Conflict("the same idempotency key is still in flight: retry after backoff"))
		return
	case errors.Is(err, ErrUnknownProduct):
		// La referencia por cuerpo a un producto que el RLS no deja ver: 422 que nombra el campo,
		// jamás el id ajeno ni la existencia de otro tenant (§4.1).
		httpx.WriteError(w, validationForField(
			"the stock adjustment references a product that is not in this tenant", "product_id"))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("stock adjustment recorded",
		"adjustment_id", adjustment.ID.String(),
		"product_id", adjustment.ProductID.String(),
		"tenant_id", claims.TenantID.String(),
	)
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(ack))
}

// parseStockAdjustment valida el cuerpo contra §4.14. El `field` del sobre nombra el campo
// culpable, jamás el valor recibido (§4.1).
func parseStockAdjustment(request *stockAdjustmentRequest) (StockAdjustmentInput, *httpx.Error) {
	adjustmentID, err := uuid.Parse(request.ID)
	if err != nil {
		return StockAdjustmentInput{}, validationForField("id must be a uuid", "id")
	}

	productID, err := uuid.Parse(request.ProductID)
	if err != nil {
		return StockAdjustmentInput{}, validationForField("product_id must be a uuid", "product_id")
	}

	if request.QtyDelta == nil {
		return StockAdjustmentInput{}, validationForField("qty_delta is a required signed quantity", "qty_delta")
	}
	qty := strings.TrimSpace(*request.QtyDelta)
	if !signedQtyPattern.MatchString(qty) || !negativeQuantity(qty) {
		return StockAdjustmentInput{}, validationForField(
			"qty_delta must be a negative quantity with at most three decimals: a loss reduces the stock", "qty_delta")
	}

	// `source` viaja para poder rechazarlo: el único valor que esta superficie escribe es
	// `manual`, porque los deltas de venta y de anulación los escribe el ledger (S8).
	if request.Source == nil || *request.Source != SourceManual {
		return StockAdjustmentInput{}, validationForField(
			"source must be manual: sale and void deltas are written by the ledger, never by this route", "source")
	}

	if request.Reason == nil || !IsStockAdjustmentReason(*request.Reason) {
		return StockAdjustmentInput{}, validationForField(
			"reason must be one of Vencimiento, Rotura en transporte, Daño en vitrina", "reason")
	}

	// Un ajuste manual no se enlaza a una venta: el par de nulidad del esquema queda en NULL, y
	// un cuerpo que lo traiga es inservible.
	if request.SoldOn != nil || request.SaleID != nil {
		field := "sold_on"
		if request.SoldOn == nil {
			field = "sale_id"
		}
		return StockAdjustmentInput{}, validationForField(
			"a manual stock adjustment carries no sale linkage", field)
	}

	return StockAdjustmentInput{
		ID:        adjustmentID,
		ProductID: productID,
		QtyDelta:  qty,
		Source:    SourceManual,
		Reason:    *request.Reason,
	}, nil
}

// negativeQuantity exige el signo y al menos un dígito distinto de cero: `-0.000` no es una
// merma.
func negativeQuantity(qty string) bool {
	if !strings.HasPrefix(qty, "-") {
		return false
	}
	return strings.ContainsFunc(qty, func(char rune) bool {
		return char >= '1' && char <= '9'
	})
}
