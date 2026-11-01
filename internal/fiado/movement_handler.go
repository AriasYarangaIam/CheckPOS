package fiado

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

// El handler de POST /fiado-movements (docs/BACKEND.md §4.11): un cargo positivo o un pago
// negativo sobre el ledger firmado de `fiado_movements`, con el sobre de error único de §4.1.
// El tenant y el usuario suben SOLO por los claims del token; el registro del deudor viaja en
// el cuerpo (`fiado_account`) porque la superficie del MVP no tiene un `POST /fiado-accounts`
// (decisión D1 de forma de odd/tasks/sprint-7-fiados.md).

// errNoFiadoClaims es el 500 fail-closed: sin middleware no hay token, y sin token no hay
// tenant del que anexar un movimiento (igual que internal/ledger e internal/cashclose).
var errNoFiadoClaims = errors.New("the fiado movement handler needs the token claims")

// payloadMismatchCode es el código de superficie de Branch A con hashes distintos: la misma
// clave usada para otro payload (docs/DATABASE.md §7.1). No es un 422 de validación del
// cuerpo — son apenas un encabezado y un reclamo que chocan.
const payloadMismatchCode = "idempotency.payload_mismatch"

// retryAfterSeconds es la espera que la superficie anuncia en el 409 del Branch C
// (docs/BACKEND.md §6.3, el mismo valor que la venta y el cierre de caja).
const retryAfterSeconds = "2"

// FiadoMovementHandler atiende POST /fiado-movements sobre un store.
type FiadoMovementHandler struct {
	movements MovementStore
	logger    *slog.Logger
}

// NewFiadoMovementHandler construye el handler sobre el store de movimientos.
func NewFiadoMovementHandler(movements MovementStore, logger *slog.Logger) (*FiadoMovementHandler, error) {
	if movements == nil {
		return nil, errors.New("the fiado movement handler needs a movement store")
	}
	if logger == nil {
		return nil, errors.New("the fiado movement handler needs a logger")
	}
	return &FiadoMovementHandler{movements: movements, logger: logger}, nil
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *FiadoMovementHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoFiadoClaims, h.logger))
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}
	h.append(w, r, claims)
}

// accountRequest es el registro del deudor que viaja con el primer movimiento. Todos sus
// campos son opcionales en el JSON: el objeto entero es opcional, y su presencia es lo que
// habilita el upsert.
type accountRequest struct {
	Name      string  `json:"name"`
	Phone     *string `json:"phone"`
	UpdatedAt *string `json:"updated_at"`
}

// movementRequest es el cuerpo de §4.11. `amount_cents` es puntero para distinguir «vacío» de
// «cero»: el ausente es un cuerpo inservible y el cero no tiene signo. El decoder descarta los
// campos que no conoce, así que un `tenant_id` falso nunca llega a ninguna parte.
type movementRequest struct {
	ID             string          `json:"id"`
	FiadoAccountID string          `json:"fiado_account_id"`
	FiadoAccount   *accountRequest `json:"fiado_account"`
	AmountCents    *int64          `json:"amount_cents"`
	Note           *string         `json:"note"`
	SoldOn         *string         `json:"sold_on"`
	SaleID         *string         `json:"sale_id"`
}

// append atiende el POST completo: la clave de claim, el cuerpo RAW (el hash de §6.2 va sobre
// los mismos bytes que llegaron), la validación de §4.11 y la llamada del store, que es LA
// transacción con el claim dentro. Las ramas del protocolo (§7.1) se resuelven así en la
// superficie: repetición y movimiento fresco devuelven 200 con el snapshot, la clave
// reutilizada con otro payload es el 422 de payload_mismatch, y el duplicado en vuelo es el
// 409 con Retry-After del Branch C.
func (h *FiadoMovementHandler) append(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	// El claim es OBLIGATORIO (§4.11): sin encabezado la petición es inservible y el store ni
	// corre.
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpx.WriteError(w, validation("the fiado movement requires the Idempotency-Key header", "Idempotency-Key"))
		return
	}

	// El hash del claim corre sobre el cuerpo RAW: los mismos bytes que el cliente envió, no
	// una re-serialización del struct (docs/BACKEND.md §6.2).
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the fiado movement fields of §4.11", nil))
		return
	}

	var request movementRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the fiado movement fields of §4.11", nil))
		return
	}

	movement, invalid := parseMovementInput(&request)
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}

	// Una sola lectura del reloj: marca el movimiento que created_at y received_at comparten en
	// `fiado_movements`, la cuenta que nace con el primer cargo, y la fila del claim (§2.5).
	ack, err := h.movements.AppendMovement(r.Context(), claims.TenantID, movement, time.Now(), synckey.ClaimInput{
		Key:         key,
		PayloadHash: synckey.PayloadHash(key, raw),
	})
	switch {
	case errors.Is(err, synckey.ErrPayloadMismatch):
		// Branch A con hashes distintos: la misma clave hablando por otro payload.
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
		w.Header().Set("Retry-After", retryAfterSeconds)
		httpx.WriteError(w, httpx.Conflict("the same idempotency key is still in flight: retry after backoff"))
		return
	case errors.Is(err, ErrUnknownAccount):
		// La cuenta no existe o es de otro tenant: 422 que nombra el campo, JAMÁS el id ajeno
		// ni la existencia de otro tenant (§4.1).
		httpx.WriteError(w, validation(
			"the fiado movement references an account that is not in this tenant", "fiado_account_id"))
		return
	case errors.Is(err, ErrUnknownSale):
		// El enlace opcional apunta a una venta que el tenant no tiene: el par entero es
		// inservible y el campo culpable es el enlace.
		httpx.WriteError(w, validation(
			"the fiado movement references a sale that is not in this tenant", "sale_id"))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("fiado movement appended",
		"movement_id", movement.ID.String(),
		"fiado_account_id", movement.AccountID.String(),
		"tenant_id", claims.TenantID.String(),
	)
	// El acuse es el cuerpo EXACTO que armó el store: en Branch B el fresco y en Branch A el
	// snapshot almacenado — los dos llegan byte a byte de la base.
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(ack))
}

// parseMovementInput valida el cuerpo contra §4.11 y devuelve los valores congelados verbatim.
// El `field` del sobre nombra el campo culpable, jamás el valor recibido (§4.1).
func parseMovementInput(request *movementRequest) (MovementInput, *httpx.Error) {
	movementID, err := uuid.Parse(request.ID)
	if err != nil {
		return MovementInput{}, validation("id must be a uuid", "id")
	}

	accountID, err := uuid.Parse(request.FiadoAccountID)
	if err != nil {
		return MovementInput{}, validation("fiado_account_id must be a uuid", "fiado_account_id")
	}

	// El monto es distinto de cero: el signo ES el tipo y un cero no tiene tipo (§4.11).
	if request.AmountCents == nil || *request.AmountCents == 0 {
		return MovementInput{}, validation("amount_cents is a required non-zero integer: the sign is the kind", "amount_cents")
	}

	// El enlace a la venta es un par de nulidad: los dos o ninguno (MD-9, F19).
	if (request.SoldOn == nil) != (request.SaleID == nil) {
		field := "sold_on"
		if request.SoldOn == nil {
			field = "sale_id"
		}
		return MovementInput{}, validation("sold_on and sale_id travel together or not at all", field)
	}

	var soldOn *time.Time
	var saleID *uuid.UUID
	if request.SoldOn != nil {
		parsed, err := time.Parse(businessDayFormat, *request.SoldOn)
		if err != nil {
			return MovementInput{}, validation("sold_on must be the business date as YYYY-MM-DD", "sold_on")
		}
		soldOn = &parsed

		parsedSale, err := uuid.Parse(*request.SaleID)
		if err != nil {
			return MovementInput{}, validation("sale_id must be a uuid", "sale_id")
		}
		saleID = &parsedSale
	}

	account, invalid := parseAccountInput(request.FiadoAccount)
	if invalid != nil {
		return MovementInput{}, invalid
	}

	return MovementInput{
		ID:          movementID,
		AccountID:   accountID,
		Account:     account,
		AmountCents: *request.AmountCents,
		Note:        trimmedOrNil(request.Note),
		SoldOn:      soldOn,
		SaleID:      saleID,
	}, nil
}

// parseAccountInput valida el registro del deudor: nombre obligatorio, teléfono opcional y la
// marca de mutación del cliente cuando viaja (decisión D1 de forma; §7.4 para el comparador).
func parseAccountInput(request *accountRequest) (*AccountInput, *httpx.Error) {
	if request == nil {
		return nil, nil
	}

	name := strings.TrimSpace(request.Name)
	if name == "" {
		return nil, validation("the fiado account registration needs a name", "fiado_account.name")
	}

	account := &AccountInput{Name: name, Phone: trimmedOrNil(request.Phone)}

	if request.UpdatedAt != nil {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*request.UpdatedAt))
		if err != nil {
			return nil, validation("fiado_account.updated_at must be an RFC3339 instant", "fiado_account.updated_at")
		}
		account.UpdatedAt = &parsed
	}

	return account, nil
}

// trimmedOrNil normaliza un opcional de texto: el vacío no es un valor.
func trimmedOrNil(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// validation arma el 422 del contrato con el detalle del campo culpable (§4.1).
func validation(message, field string) *httpx.Error {
	return httpx.Validation(message, map[string]string{"field": field})
}
