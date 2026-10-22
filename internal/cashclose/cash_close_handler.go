package cashclose

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// El handler de POST /cash-closes (docs/BACKEND.md §4.13): el cierre como proyección del
// ledger, con el sobre de error único de §4.1. El tenant y el usuario suben SOLO por los
// claims del token; el cuerpo trae el id del cierre, la fecha de negocio y los seis conteos
// del operador — el `expected_cents` lo congela el servidor.

// errNoCloseClaims es el 500 fail-closed: sin middleware no hay token, y sin token no hay
// ni tenant ni usuario de los que cerrar una caja.
var errNoCloseClaims = errors.New("the cash close handler needs the token claims")

// payloadMismatchCode es el código de superficie de Branch A con hashes distintos: la misma
// clave usada para otro payload (docs/DATABASE.md §7.1). No es un 422 de validación del
// cuerpo — son apenas un encabezado y un reclamo que chocan — y el código propio le marca
// la diferencia al cliente.
const payloadMismatchCode = "idempotency.payload_mismatch"

// CashCloseHandler atiende POST /cash-closes sobre un store.
type CashCloseHandler struct {
	closes CashCloseStore
	logger *slog.Logger
}

// NewCashCloseHandler construye el handler sobre el store de cierres.
func NewCashCloseHandler(closes CashCloseStore, logger *slog.Logger) (*CashCloseHandler, error) {
	if closes == nil {
		return nil, errors.New("the cash close handler needs a cash close store")
	}
	if logger == nil {
		return nil, errors.New("the cash close handler needs a logger")
	}
	return &CashCloseHandler{closes: closes, logger: logger}, nil
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *CashCloseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoCloseClaims, h.logger))
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}
	h.close(w, r, claims)
}

// countRequest es UNA fila del cuerpo. `counted_cents` es puntero para distinguir «vacío»
// de «cero»: el ausente es un cuerpo inservible, y el valor tácito de la decodificación
// sería aceptarlo de contrabando.
type countRequest struct {
	PaymentMethod string `json:"payment_method"`
	CountedCents  *int64 `json:"counted_cents"`
}

// closeRequest es el cuerpo de §4.13. El decoder descarta los campos que no conoce, así que
// un `expected_cents` o un `tenant_id` falsos nunca llegan a ninguna parte: lo esperado lo
// proyecta el servidor y el tenant es SOLO el del token (ARCHITECTURE.md:23).
type closeRequest struct {
	ID       string         `json:"id"`
	ClosedOn string         `json:"closed_on"`
	Counts   []countRequest `json:"counts"`
}

// close atiende el POST completo: la clave de claim, el cuerpo RAW (el hash de §6.2 va
// sobre los mismos bytes que llegaron), la validación de §4.13 y la llamada del store, que
// es LA transacción con el claim dentro. Las ramas del protocolo (§7.1) se resuelven así en
// la superficie: repetición y cierre fresco —o duplicado de fecha— devuelven 200 con el
// snapshot, la clave reutilizada con otro payload es el 422 de payload_mismatch, y el
// duplicado en vuelo es el 409 con Retry-After del Branch C.
func (h *CashCloseHandler) close(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpx.WriteError(w, validation("the cash close requires the Idempotency-Key header", "Idempotency-Key"))
		return
	}

	// El hash del claim corre sobre el cuerpo RAW: los mismos bytes que el cliente envió,
	// no una re-serialización del struct (docs/BACKEND.md §6.2).
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the cash close fields of §4.13", nil))
		return
	}

	var request closeRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the cash close fields of §4.13", nil))
		return
	}

	close, invalid := parseCloseInput(&request)
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}

	// Una sola lectura del reloj: marca el cierre que created_at y received_at comparten en
	// `cash_closes`, el desglose y la fila del claim (§2.5).
	ack, err := h.closes.Close(r.Context(), claims.TenantID, claims.UserID, close, time.Now(), synckey.ClaimInput{
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
		// Branch C: el ganador sigue en vuelo — el 409 del §6.3 con el Retry-After para el
		// backoff del cliente. La cabecera va ANTES de WriteHeader.
		w.Header().Set("Retry-After", "2")
		httpx.WriteError(w, httpx.Conflict("the same idempotency key is still in flight: retry after backoff"))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("cash close registered",
		"cash_close_id", close.ID.String(),
		"tenant_id", claims.TenantID.String(),
	)
	// El acuse es el cuerpo EXACTO que armó el store: en el cierre fresco es la proyección
	// recién congelada, en el duplicado es la del cierre existente y en la repetición es el
	// snapshot almacenado — los tres llegan byte a byte de la base.
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(ack))
}

// parseCloseInput valida el cuerpo contra §4.13 y normaliza los conteos al orden fijo del
// contrato. El conjunto es CERRADO de seis filas: omitir un método escondería dinero y
// duplicarlo lo contaría dos veces, así que los dos casos son un 422 que nombra `counts`.
func parseCloseInput(request *closeRequest) (CloseInput, *httpx.Error) {
	closeID, err := uuid.Parse(request.ID)
	if err != nil {
		return CloseInput{}, validation("id must be a uuid", "id")
	}

	closedOn, err := time.Parse(businessDayFormat, request.ClosedOn)
	if err != nil {
		return CloseInput{}, validation("closed_on must be the business date being closed as YYYY-MM-DD", "closed_on")
	}

	if len(request.Counts) != len(paymentMethodsInOrder) {
		return CloseInput{}, validation("a cash close needs exactly six counts, one per payment method", "counts")
	}

	counted := make(map[string]int64, len(paymentMethodsInOrder))
	for index := range request.Counts {
		row := &request.Counts[index]
		if !paymentMethods[row.PaymentMethod] {
			return CloseInput{}, validation("payment_method must be one of cash, yape, plin, card, other, fiado", "counts", "index", strconv.Itoa(index))
		}
		if _, duplicate := counted[row.PaymentMethod]; duplicate {
			return CloseInput{}, validation("each payment method appears exactly once", "counts", "index", strconv.Itoa(index))
		}
		if row.CountedCents == nil || *row.CountedCents < 0 {
			return CloseInput{}, validation("counted_cents is a required non-negative integer", "counted_cents", "index", strconv.Itoa(index))
		}
		// La mitad de superficie del CHECK estructural de F32/D11: el fiado contado es
		// siempre cero porque su esperado es el crédito extendido del día.
		if row.PaymentMethod == fiadoMethod && *row.CountedCents != 0 {
			return CloseInput{}, validation("the counted amount of fiado is always zero", "payment_method", "index", strconv.Itoa(index))
		}
		counted[row.PaymentMethod] = *row.CountedCents
	}

	// El orden del acuse es el fijo del contrato, no el del cuerpo: la repetición
	// idempotente tiene que reproducir los mismos bytes.
	counts := make([]CountInput, 0, len(paymentMethodsInOrder))
	for _, method := range paymentMethodsInOrder {
		counts = append(counts, CountInput{PaymentMethod: method, CountedCents: counted[method]})
	}

	return CloseInput{
		ID:       closeID,
		ClosedOn: closedOn,
		Counts:   counts,
	}, nil
}

// validation arma el 422 del contrato con el detalle del campo culpable (§4.1), igual que
// internal/ledger; extras añaden pares clave/valor al details.
func validation(message, field string, extras ...string) *httpx.Error {
	details := map[string]string{"field": field}
	for i := 0; i+1 < len(extras); i += 2 {
		details[extras[i]] = extras[i+1]
	}
	return httpx.Validation(message, details)
}
