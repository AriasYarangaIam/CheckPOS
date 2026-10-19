package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// El handler de POST /sales (docs/BACKEND.md §4.9): anclado a `sales` + `sale_lines` de
// docs/DATABASE.md §4, con el sobre de error único de §4.1. El tenant y el usuario suben
// SOLO por los claims del token; payment_method es el CHECK de seis valores; y los valores
// congelados del cuerpo viajan al store SIN recompute (docs/DATABASE.md §2.3, MD-7).

// paymentMethods son EXACTAMENTE los seis valores del CHECK `sales_payment_method_chk`
// (§4 `sales`, D11). No existe split tender (docs/DATABASE.md §8, D9): el medio de pago es
// una sola columna, y el set es cerrado en la superficie igual de cerrado que en la base.
var paymentMethods = map[string]bool{
	"cash":  true,
	"yape":  true,
	"plin":  true,
	"card":  true,
	"other": true,
	"fiado": true,
}

// qtyPattern es la forma exacta de una cantidad NUMERIC(10,3) positiva (§2.4): hasta 7
// dígitos enteros, hasta 3 decimales, el punto como separador. Y un dígito distinto de
// cero en algún lado: una línea vendida de cantidad cero no existe.
var qtyPattern = regexp.MustCompile(`^[0-9]{1,7}(\.[0-9]{1,3})?$`)

// dayFormat es el formato de sold_on: fecha de negocio, ninguna hora (§4 `sales`).
const dayFormat = "2006-01-02"

// errNoSaleClaims es el 500 fail-closed: sin middleware no hay token, y sin token no hay
// ni tenant ni usuario de los que cerrar una venta (igual que internal/catalog).
var errNoSaleClaims = errors.New("the sales handler needs the token claims")

// payloadMismatchCode es el código de superficie de Branch A con hashes distintos: la
// misma clave usada para otro payload (docs/DATABASE.md §7.1, docs/BACKEND.md §4.9). No
// es un 422 de validación del cuerpo — son apenas un encabezado y un reclamo que chocan —
// y el código propio le marca la diferencia al cliente.
const payloadMismatchCode = "idempotency.payload_mismatch"

// SalesHandler atiende POST /sales sobre un store.
type SalesHandler struct {
	sales  SaleStore
	logger *slog.Logger
}

// NewSalesHandler construye el handler sobre el store de ventas.
func NewSalesHandler(sales SaleStore, logger *slog.Logger) (*SalesHandler, error) {
	if sales == nil {
		return nil, errors.New("the sales handler needs a sale store")
	}
	if logger == nil {
		return nil, errors.New("the sales handler needs a logger")
	}
	return &SalesHandler{sales: sales, logger: logger}, nil
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *SalesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// El tenant y el usuario son los del token, y solo existen si el middleware corrió. Sin
	// claims no hay tenant del que cerrar una venta: la respuesta es un 500 y nada corre.
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoSaleClaims, h.logger))
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}
	h.close(w, r, claims)
}

// saleLineRequest es UNA línea del cuerpo de §4.9. Los campos numéricos son punteros para
// distinguir «vacío» de «cero»: el ausente es un cuerpo inservible; el valor tácito por
// decodificación sería aceptarlo de contrabando.
type saleLineRequest struct {
	LineNo         *int   `json:"line_no"`
	ProductID      string `json:"product_id"`
	Name           string `json:"name"`
	PriceCents     *int64 `json:"price_cents"`
	UoM            string `json:"uom"`
	Qty            string `json:"qty"`
	LineTotalCents *int64 `json:"line_total_cents"`
}

// saleRequest es el cuerpo de §4.9. El decoder descarta los campos que no conoce, así que
// un `tenant_id` falso nunca llega a ninguna parte: el tenant es SOLO el del token
// (ARCHITECTURE.md:23).
type saleRequest struct {
	SaleID        string            `json:"sale_id"`
	SoldOn        string            `json:"sold_on"`
	PaymentMethod string            `json:"payment_method"`
	TotalCents    *int64            `json:"total_cents"`
	Lines         []saleLineRequest `json:"lines"`
}

// close atiende el POST completo: la clave de claim, el cuerpo RAW (el hash de §6.2 va
// sobre los mismos bytes que llegaron), la validación §4.9 y la llamada del store, que es
// LA transacción con el claim dentro. Las ramas del protocolo (docs/DATABASE.md §7.1)
// se resuelven así en la superficie: repetición y cierre fresco devuelven 200 con el
// snapshot, la clave reutilizada con otro payload es el 422 de payload_mismatch, y el
// duplicado en vuelo es el 409 con Retry-After del Branch C.
func (h *SalesHandler) close(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	// El claim es OBLIGATORIO (docs/BACKEND.md §4.9): sin encabezado la petición es
	// inservible y el store ni corre.
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpx.WriteError(w, validation("the sale close requires the Idempotency-Key header", "Idempotency-Key"))
		return
	}

	// El hash del claim corre sobre el cuerpo RAW: los mismos bytes que el cliente envió,
	// no una re-serialización del struct (docs/BACKEND.md §6.2).
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the sale fields of §4.9", nil))
		return
	}

	var request saleRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the sale fields of §4.9", nil))
		return
	}

	sale, invalid := parseSaleInput(&request)
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}

	// Una sola lectura del reloj: marca el cierre que created_at y received_at comparten
	// en `sales`, que el delta de stock comparte en `stock_adjustments`, y que la fila del
	// claim comparte en `idempotency_keys`.
	ack, err := h.sales.CloseSale(r.Context(), claims.TenantID, claims.UserID, sale, time.Now(), synckey.ClaimInput{
		Key:         key,
		PayloadHash: synckey.PayloadHash(key, raw),
	})
	switch {
	case errors.Is(err, synckey.ErrPayloadMismatch):
		// Branch A con hashes distintos: la misma clave hablar por otro payload es el 422
		// del §4.9 con su código propio. Nadie escribió nada detrás del conflicto.
		httpx.WriteError(w, &httpx.Error{
			Status:  http.StatusUnprocessableEntity,
			Code:    payloadMismatchCode,
			Message: "the same idempotency key was already used with a different payload",
			Details: map[string]string{"field": "Idempotency-Key"},
		})
		return
	case errors.Is(err, synckey.ErrInFlight):
		// Branch C: el ganador sigue en vuelo — el 409 del §6.3 con el Retry-After para el
		// backoff del cliente. La cabecera va ANTES de WriteHeader: tiene que salir en la
		// misma respuesta del conflicto.
		w.Header().Set("Retry-After", "2")
		httpx.WriteError(w, httpx.Conflict("the same idempotency key is still in flight: retry after backoff"))
		return
	case errors.Is(err, ErrUnknownProduct):
		// La referencia por cuerpo al catálogo de otro tenant es inservible (422): se nombra
		// el campo, JAMÁS el id ajeno ni la existencia de otro tenant (§4.1).
		httpx.WriteError(w, httpx.Validation(
			"the sale references a product that is not in this tenant", map[string]string{"field": "product_id"}))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("sale closed",
		"sale_id", sale.ID.String(),
		"tenant_id", claims.TenantID.String(),
	)
	// El acuse es el cuerpo EXACTO que armó el store: en Branch B es el acuse fresco y en
	// Branch A es el snapshot almacenado — los dos llegan byte a byte de la base.
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(ack))
}

// parseSaleInput valida el cuerpo contra §4.9 y devuelve los valores congelados verbatim.
// El `field` del sobre nombra el campo culpable, jamás el valor recibido (§4.1).
func parseSaleInput(request *saleRequest) (SaleInput, *httpx.Error) {
	saleID, err := uuid.Parse(request.SaleID)
	if err != nil {
		return SaleInput{}, validation("sale_id must be a uuid", "sale_id")
	}

	soldOn, err := time.Parse(dayFormat, request.SoldOn)
	if err != nil {
		return SaleInput{}, validation("sold_on must be the business date of the close as YYYY-MM-DD", "sold_on")
	}

	if !paymentMethods[request.PaymentMethod] {
		return SaleInput{}, validation("payment_method must be one of cash, yape, plin, card, other, fiado", "payment_method")
	}

	if request.TotalCents == nil || *request.TotalCents < 0 {
		return SaleInput{}, validation("total_cents is a required non-negative integer", "total_cents")
	}
	totalCents := *request.TotalCents

	if len(request.Lines) == 0 {
		return SaleInput{}, validation("a sale closes with at least one line", "lines")
	}

	lines := make([]SaleLineInput, 0, len(request.Lines))
	seenLineNo := map[int]bool{}
	var lineSum int64
	for index := range request.Lines {
		line, invalid := parseSaleLine(index, &request.Lines[index])
		if invalid != nil {
			return SaleInput{}, invalid
		}
		if seenLineNo[line.No] {
			return SaleInput{}, validation("line_no must be unique within the sale", "lines")
		}
		seenLineNo[line.No] = true
		lineSum += line.TotalCents
		lines = append(lines, line)
	}

	// La coherencia del total congelado NO es un recompute: §4.9 define total_cents como la
	// suma de los line_total_cents redondeados, y un cuerpo que no cuadra con esa
	// definición es inservible. La validación no toca ningún valor — el store escribe
	// los congelados verbatim.
	if lineSum != totalCents {
		return SaleInput{}, validation("total_cents must equal the sum of the frozen line totals", "total_cents")
	}

	return SaleInput{
		ID:            saleID,
		SoldOn:        soldOn,
		PaymentMethod: request.PaymentMethod,
		TotalCents:    totalCents,
		Lines:         lines,
	}, nil
}

// parseSaleLine valida una línea individual; el índice viaja en details para nombrar la
// línea del arreglo culpable.
func parseSaleLine(index int, line *saleLineRequest) (SaleLineInput, *httpx.Error) {
	if line.LineNo == nil || *line.LineNo < 1 {
		return SaleLineInput{}, validation("line_no must be a positive integer", "lines", "index", strconv.Itoa(index))
	}

	productID, err := uuid.Parse(line.ProductID)
	if err != nil {
		return SaleLineInput{}, validation("product_id must be a uuid", "lines", "index", strconv.Itoa(index))
	}

	if line.Name == "" {
		return SaleLineInput{}, validation("the frozen product name does not travel empty", "lines", "index", strconv.Itoa(index))
	}

	if line.PriceCents == nil || *line.PriceCents < 0 {
		return SaleLineInput{}, validation("price_cents is a required non-negative integer", "lines", "index", strconv.Itoa(index))
	}

	if line.UoM == "" {
		return SaleLineInput{}, validation("the frozen unit of measure does not travel empty", "lines", "index", strconv.Itoa(index))
	}

	if !positiveQuantity(line.Qty) {
		return SaleLineInput{}, validation("qty must be a positive quantity with at most three decimals", "lines", "index", strconv.Itoa(index))
	}

	if line.LineTotalCents == nil || *line.LineTotalCents < 0 {
		return SaleLineInput{}, validation("line_total_cents is a required non-negative integer", "lines", "index", strconv.Itoa(index))
	}

	return SaleLineInput{
		No:         *line.LineNo,
		ProductID:  productID,
		Name:       line.Name,
		PriceCents: *line.PriceCents,
		UoM:        line.UoM,
		Qty:        line.Qty,
		TotalCents: *line.LineTotalCents,
	}, nil
}

// positiveQuantity comprueba la forma y el signo de una cantidad §2.4 sin leerla como
// float: la expresión regular fija la forma (escala ≤ 3), y el recorrido de dígitos exige
// que ALGÚN dígito sea distinto de cero.
func positiveQuantity(qty string) bool {
	if !qtyPattern.MatchString(qty) {
		return false
	}
	return strings.ContainsFunc(qty, func(char rune) bool {
		return char >= '1' && char <= '9'
	})
}

// validation arma el 422 del contrato con el detalle del campo culpable (§4.1), igual que
// internal/catalog/validationForField; extras añaden pares clave/valor al details.
func validation(message, field string, extras ...string) *httpx.Error {
	details := map[string]string{"field": field}
	for i := 0; i+1 < len(extras); i += 2 {
		details[extras[i]] = extras[i+1]
	}
	return httpx.Validation(message, details)
}
