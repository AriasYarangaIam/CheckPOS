package catalog

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

// Este archivo implementa la superficie HTTP de /products (W3-BE-1, docs/BACKEND.md
// §4.5-§4.7): GET listado del catálogo activo del tenant, POST creación con las
// resoluciones silenciosas de §7.7 y PATCH aditivo resuelto por LWW.
//
// Tres invariantes de la superficie que este handler sostiene:
//
//   - el tenant y el usuario salen de los claims verificados del token y de NINGÚN otro
//     lado: el cuerpo y un encabezado pueden traer el tenant que quieran, y se ignoran
//     (§5.1; ARCHITECTURE.md:23). El middleware de token es la única fuente;
//   - `stock` NO viaja en los payloads de POST ni de PATCH y nunca se muta: el stock
//     converge solo por deltas firmados (docs/DATABASE.md §4 `products`;
//     ARCHITECTURE.md:176; SPRINTS.md W3-BE-1). Un cuerpo que lo traiga, con cualquier
//     valor —incluso nulo— es 422;
//   - PATCH no quita campos: un campo explícitamente nulo es un intento de quitar y devuelve
//     422 (docs/DATABASE.md §4 `products` mutability; SPRINTS.md W3-BE-1).
//
// El import de identity es la costura declarada del contrato: son los claims que el
// middleware deja en el contexto, nunca una segunda extracción en este módulo. La
// respuesta de POST es 200 y no 201 aunque cree: §4.6 la admite, y §7.7 fija 200 con la
// fila resuelta —que acá puede volver con el nombre renombrado o el gtin en NULL.

// errNoClaims es la causa que se registra cuando la ruta corre sin el middleware de token:
// es un defecto de cableado, no una petición anónima.
var errNoClaims = errors.New("the route ran without the access token middleware: there is no tenant in the request context")

// patchFields son los campos que el PATCH puede tocar, en el orden en que se inspecciona
// el cuerpo: es la lista que define lo que significa «quitar» (§4.7). `stock` NO está
// porque no es un campo que PATCH pueda tocar: su presencia es 422 por sí sola.
var patchFields = []string{"name", "gtin", "price_cents", "uom", "photo_url", "attributes", "updated_at"}

// ProductsHandler atiende las tres rutas de /products (GET, POST y PATCH) sobre un store.
type ProductsHandler struct {
	store  ProductStore
	logger *slog.Logger
}

// NewProductsHandler construye el handler sobre el store de producto.
func NewProductsHandler(store ProductStore, logger *slog.Logger) (*ProductsHandler, error) {
	if store == nil {
		return nil, errors.New("the products handler needs a product store")
	}
	if logger == nil {
		return nil, errors.New("the products handler needs a logger")
	}
	return &ProductsHandler{store: store, logger: logger}, nil
}

// ServeHTTP despacha por método: el mux de httpx ya enruta los tres patrones hacia acá.
func (h *ProductsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// El tenant y el usuario son los del token, y solo existen si el middleware corrió. Sin
	// claims no hay tenant del que ni siquiera leer: la respuesta es un 500 y nada corre.
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoClaims, h.logger))
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.list(w, r, claims)
	case http.MethodPost:
		h.create(w, r, claims)
	case http.MethodPatch:
		h.update(w, r, claims)
	default:
		httpx.WriteError(w, httpx.NotFound("this route only answers GET, POST and PATCH"))
	}
}

// productBody es la fila del contrato (§4.5): todas las columnas de
// docs/DATABASE.md §4 `products`, con los nulos explícitos. Es una sola forma de fila para los
// tres endpoints: el mismo literal —incluido `stock` en su texto NUMERIC verbatim— vuelve
// de una lectura y de una escritura.
type productBody struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	GTIN            *string         `json:"gtin"`
	PriceCents      int64           `json:"price_cents"`
	UoM             string          `json:"uom"`
	Stock           string          `json:"stock"`
	PhotoURL        *string         `json:"photo_url"`
	Attributes      json.RawMessage `json:"attributes"`
	GlobalProductID *string         `json:"global_product_id"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	ReceivedAt      string          `json:"received_at"`
	DeletedAt       *string         `json:"deleted_at"`
}

// toBody traduce la fila a la forma del contrato. Las marcas viajan RFC3339 en UTC, el
// formato del contrato para todo instante de la superficie (§4.3).
func toBody(product Product) productBody {
	return productBody{
		ID:              product.ID.String(),
		Name:            product.Name,
		GTIN:            product.GTIN,
		PriceCents:      product.PriceCents,
		UoM:             product.UoM,
		Stock:           product.Stock,
		PhotoURL:        product.PhotoURL,
		Attributes:      attributesToJSON(product.Attributes),
		GlobalProductID: pointerIDToString(product.GlobalProductID),
		CreatedAt:       rfc3339(product.CreatedAt),
		UpdatedAt:       rfc3339(product.UpdatedAt),
		ReceivedAt:      rfc3339(product.ReceivedAt),
		DeletedAt:       deletedAtToJSON(product.DeletedAt),
	}
}

// attributesToJSON es el JSONB home #1 de §2.9: sin contenido, el campo vale null en el
// protocolo; con contenido, sale byte a byte tal como está guardado.
func attributesToJSON(attributes []byte) json.RawMessage {
	if attributes == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(attributes)
}

// pointerIDToString mapea un uuid opcional a la forma del contrato: nil → JSON null.
func pointerIDToString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	return newFrom(id.String())
}

// deletedAtToJSON mantiene la tumba en null cuando no hay; el pull del cursor de §7.3 es
// lo único que la transporta con valor.
func deletedAtToJSON(deletedAt *time.Time) *string {
	if deletedAt == nil {
		return nil
	}
	return newFrom(rfc3339(*deletedAt))
}

// newFrom copia el valor para no fugar punteros hacia arriba del llamador.
func newFrom[T any](value T) *T {
	copied := value
	return &copied
}

// rfc3339 es el formato de instante del contrato, SIEMPRE en UTC.
func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// listProductsResponse es la respuesta del listado de §4.5: { "data": [...productos...] }.
type listProductsResponse struct {
	Data []productBody `json:"data"`
}

// list atiende GET: la página del catálogo ACTIVO del tenant —nada global, ninguna fila de
// otro tenant; las filas tombstoned las trae el pull del cursor, no acá (§7.3).
//
// Propagación de parámetros (propuesta §4.5): `q` busca por nombre subcadena o GTIN
// exacto; `limit` ausente → default del store, presente e ilegible → 422.
func (h *ProductsHandler) list(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	filter := ProductFilter{
		Query: strings.TrimSpace(r.URL.Query().Get("q")),
	}

	query := r.URL.Query()
	if query.Has("limit") {
		limit, err := strconv.Atoi(strings.TrimSpace(query.Get("limit")))
		if err != nil {
			httpx.WriteError(w, validationForField("limit must be an integer", "limit"))
			return
		}
		filter.Limit = limit
	}

	products, err := h.store.List(r.Context(), claims.TenantID, filter)
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	rows := make([]productBody, 0, len(products))
	for _, product := range products {
		rows = append(rows, toBody(product))
	}
	httpx.WriteJSON(w, http.StatusOK, listProductsResponse{Data: rows})
}

// createProductRequest es el cuerpo de §4.6. `stock` existe en el struct SOLO para
// detectar su presencia y rechazarla: nunca hay lectura de valor.
type createProductRequest struct {
	ID              *string         `json:"id"`
	Name            *string         `json:"name"`
	GTIN            *string         `json:"gtin"`
	PriceCents      *int64          `json:"price_cents"`
	UoM             *string         `json:"uom"`
	Stock           json.RawMessage `json:"stock"`
	PhotoURL        *string         `json:"photo_url"`
	Attributes      json.RawMessage `json:"attributes"`
	UpdatedAt       *string         `json:"updated_at"`
	GlobalProductID *string         `json:"global_product_id"`
}

// updateProductRequest es el cuerpo de §4.7: los campos son individuales y parciales. Un
// campo nulo ya fue rechazado como intento de quitar antes de llegar acá, así que un
// puntero no nulo es «el cliente lo provisto».
type updateProductRequest struct {
	Name       *string         `json:"name"`
	GTIN       *string         `json:"gtin"`
	PriceCents *int64          `json:"price_cents"`
	UoM        *string         `json:"uom"`
	Stock      json.RawMessage `json:"stock"`
	PhotoURL   *string         `json:"photo_url"`
	Attributes json.RawMessage `json:"attributes"`
	UpdatedAt  *string         `json:"updated_at"`
}

// create atiende POST: el cuerpo anclado a `products`, SIN stock, con el id del Cliente.
// La fila resuelta del store es la respuesta: una colisión de §7.7 vuelve en 200 con
// el nombre renombrado o el gtin en NULL, en silencio.
func (h *ProductsHandler) create(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	// El claim es OBLIGATORIO en un POST encolable (§7.6): sin encabezado la petición es
	// inservible y no se escribe nada, ni siquiera la fila del claim.
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpx.WriteError(w, validationForField(
			"the product creation requires the Idempotency-Key header", "Idempotency-Key"))
		return
	}

	// El hash del claim corre sobre el cuerpo RAW: los mismos bytes que el cliente envió,
	// no una re-serialización del struct (docs/BACKEND.md §6.2).
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the product fields of §4.6", nil))
		return
	}

	var request createProductRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with the product fields of §4.6", nil))
		return
	}

	// `stock` está excluido del payload: con cualquier valor —incluso nulo— el cuerpo no es
	// el cuerpo del contrato. Es el criterio de aceptación del sprint, no cortesía.
	if len(request.Stock) > 0 {
		httpx.WriteError(w, validationForField(
			"stock must not travel in the product payload: stock converges only via signed deltas", "stock"))
		return
	}

	productID, err := usableUUID(request.ID, "id", true)
	if err != nil {
		httpx.WriteError(w, httpx.Validation(err.Error(), map[string]string{"field": "id"}))
		return
	}

	mutation, invalid := mutationFromBody(request.Name, request.GTIN, request.PriceCents,
		request.UoM, request.PhotoURL, request.Attributes, request.UpdatedAt, true)
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}
	mutation.ID = productID

	// El puntero de adopción F13 es campo de la creación (§4.6); el PATCH no lo toca.
	if request.GlobalProductID != nil {
		globalID, err := usableUUID(request.GlobalProductID, "global_product_id", false)
		if err != nil {
			httpx.WriteError(w, httpx.Validation(err.Error(), map[string]string{"field": "global_product_id"}))
			return
		}
		mutation.GlobalProductID = newFrom(globalID)
	}

	// El store es LA transacción: reclama la clave, escribe la fila con sus resoluciones
	// silenciosas y completa el claim con el snapshot de la respuesta (docs/DATABASE.md
	// §7.1). Un cuerpo inválido nunca llegó hasta acá, así que ninguna fila del claim queda
	// registrada para una petición que no era el contrato.
	_, snapshot, err := h.store.Create(r.Context(), claims.TenantID, mutation, time.Now(), synckey.ClaimInput{
		Key:         key,
		PayloadHash: synckey.PayloadHash(key, raw),
	})
	switch {
	case errors.Is(err, synckey.ErrPayloadMismatch):
		// Branch A con hashes distintos: la misma clave hablando por otro payload es el 422
		// del §7.1 con su código propio. Nadie escribió nada detrás del conflicto.
		httpx.WriteError(w, &httpx.Error{
			Status:  http.StatusUnprocessableEntity,
			Code:    payloadMismatchCode,
			Message: "the same idempotency key was already used with a different payload",
			Details: map[string]string{"field": "Idempotency-Key"},
		})
		return
	case errors.Is(err, synckey.ErrInFlight):
		// Branch C: el ganador sigue en vuelo — el 409 con el Retry-After que el worker
		// respeta antes de reintentar (§7.1, docs/BACKEND.md §8.2).
		w.Header().Set("Retry-After", "2")
		httpx.WriteError(w, httpx.Conflict("the same idempotency key is still in flight: retry after backoff"))
		return
	case errors.Is(err, ErrDuplicateProductID):
		// Un id que el tenant no puede ver ya existe: es un cuerpo inservible (422), no una
		// fila ajena que se confiesa (el 404 no revela el aislamiento de otro tenant).
		httpx.WriteError(w, validationForField("the product id is already in use", "id"))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("product stored",
		"idempotency_key", key,
		"tenant_id", claims.TenantID.String(),
	)

	// El cuerpo de la respuesta es el snapshot: en Branch B el que el store acaba de
	// completar en el claim y en Branch A el almacenado, byte a byte. Las dos ramas
	// devuelven la MISMA respuesta para la MISMA clave (§7.1).
	writeSnapshot(w, snapshot)
}

// payloadMismatchCode es el código de superficie de Branch A con hashes distintos: la
// misma clave de idempotencia reutilizada para otro payload (§7.1). Es el MISMO código que
// usan las demás superficies encolables (§7.6), no un vocabulario por endpoint.
const payloadMismatchCode = "idempotency.payload_mismatch"

// writeSnapshot escribe el cuerpo del contrato byte a byte, sin re-serializar: la
// repetición de una petición tiene que devolver exactamente la respuesta almacenada.
func writeSnapshot(w http.ResponseWriter, snapshot []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(snapshot)
}

// update atiende PATCH: parcial, aditivo, LWW (§4.7). El id viene de la RUTA —un id en el
// cuerpo no existe y se ignora como cualquier campo desconocido— y el store resuelve el
// empate: una mutación que no gana vuelve en 200 con la fila ganadora (§7.4).
func (h *ProductsHandler) update(w http.ResponseWriter, r *http.Request, claims identity.Claims) {
	productID, err := uuidFromPath(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.Validation(err.Error(), map[string]string{"field": "id"}))
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object", nil))
		return
	}

	// `stock` en un patch es el intento de mutar el campo prohibido: 422, con NINGÚN intento
	// de leer el valor (§7.4: LWW no toca stock).
	if _, forbidden := body["stock"]; forbidden {
		httpx.WriteError(w, validationForField(
			"stock must not travel in the product payload: stock converges only via signed deltas", "stock"))
		return
	}

	// PATCH es aditivo: nunca quita por PATCH. Un campo explícitamente nulo es un intento de
	// quitar y devuelve 422 (docs/DATABASE.md §4 `products` mutability; SPRINTS.md W3-BE-1).
	for _, field := range patchFields {
		value, present := body[field]
		if !present {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(value)), "null") {
			httpx.WriteError(w, validationForField(
				"the field must not be removed: the patch can only add or change", field))
			return
		}
	}

	// El cuerpo ya pasó la forma; ahora el tipo de cada campo PROVISTO, de a uno: un valor
	// del tipo equivocado (un float en price_cents, un objeto en name) es un 422 que nombra
	// SU campo, no un error genérico de cuerpo.
	request := updateProductRequest{}
	for _, field := range []struct {
		key  string
		dest any
	}{
		{"name", &request.Name},
		{"gtin", &request.GTIN},
		{"price_cents", &request.PriceCents},
		{"uom", &request.UoM},
		{"photo_url", &request.PhotoURL},
		{"updated_at", &request.UpdatedAt},
	} {
		raw, present := body[field.key]
		if !present {
			continue
		}
		if err := json.Unmarshal(raw, field.dest); err != nil {
			httpx.WriteError(w, validationForField(field.key+" has the wrong type", field.key))
			return
		}
	}
	if raw, present := body["attributes"]; present {
		request.Attributes = raw
	}

	mutation, invalid := mutationFromBody(request.Name, request.GTIN, request.PriceCents,
		request.UoM, request.PhotoURL, request.Attributes, request.UpdatedAt, false)
	if invalid != nil {
		httpx.WriteError(w, invalid)
		return
	}
	mutation.ID = productID

	product, err := h.store.Update(r.Context(), claims.TenantID, productID, mutation, time.Now())
	switch {
	case errors.Is(err, ErrUnknownProduct):
		// §4.1: el recurso no existe o está fuera del tenant, y las dos causas devuelven la
		// MISMA respuesta —ni siquiera el mensaje nombra el aislamiento.
		httpx.WriteError(w, httpx.NotFound("the product does not exist"))
		return
	case err != nil:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("product updated",
		"product_id", productID.String(),
		"tenant_id", claims.TenantID.String(),
	)
	httpx.WriteJSON(w, http.StatusOK, toBody(product))
}

// mutationFromBody valida los campos mutables compartidos entre POST (§4.6, required) y
// PATCH (§4.7, parcial). Devuelvan mutation y el primer problema del cuerpo, que es el 422
// con el CAMPO nombrado y nunca el valor recibido (§4.1: codificación del detalle).
func mutationFromBody(name, gtin *string, priceCents *int64, uom *string,
	photoURL *string, attributes json.RawMessage, updatedAt *string, required bool) (ProductMutation, *httpx.Error) {

	mutation := ProductMutation{}

	// name: requerido en POST; en PATCH, si viaja, no puede venir blanco (quitar es un
	// intento y ni siquiera con basura se llega al store).
	if required && name == nil {
		return mutation, validationForField("name is required", "name")
	}
	if name != nil && strings.TrimSpace(*name) == "" {
		return mutation, validationForField("name must not be blank", "name")
	}
	if name != nil {
		mutation.Name = name
	}

	// gtin: opcional; presente tiene que ser utilizable. TEXT preserva los ceros a la
	// izquierda —no se normaliza, no se trimea, no se valida contra ningún formato más.
	if gtin != nil && strings.TrimSpace(*gtin) == "" {
		return mutation, validationForField("gtin must not be empty", "gtin")
	}
	mutation.GTIN = gtin

	// price_cents: requering post; integer sin signo. El dinero es BIGINT integer cents en
	// todo el sistema (§2.3): un float o un negativo no es un dinero válido.
	if required && priceCents == nil {
		return mutation, validationForField("price_cents is required", "price_cents")
	}
	if priceCents != nil && *priceCents < 0 {
		return mutation, validationForField("price_cents must not be negative", "price_cents")
	}
	mutation.PriceCents = priceCents

	// uom: requerido en POST (`pack` vs `unidad` vive ACÁ, como CAMPO, y nunca como JSONB —
	// es el entregable del sprint: un `kg` persiste columna, no JSONB).
	if required && (uom == nil || strings.TrimSpace(*uom) == "") {
		return mutation, validationForField("uom is required", "uom")
	}
	if uom != nil && strings.TrimSpace(*uom) == "" {
		return mutation, validationForField("uom must not be blank", "uom")
	}
	mutation.UoM = uom

	// photo_url: opcional; presente tiene que ser un valor no vacío (no se valida contra
	// ninguna forma de URL: la dirección es propiedad del módulo photos, no del catálogo).
	if photoURL != nil && strings.TrimSpace(*photoURL) == "" {
		return mutation, validationForField("photo_url must not be empty", "photo_url")
	}
	mutation.PhotoURL = photoURL

	// attributes: home #1 del JSONB (§2.9); ausente o nulo → NULL de SQL; presente, SOLO
	// objeto: `[1,2]` o `"x"` es un cuerpo ilegal, no un atributo raro.
	attrs, err := attributesObject(attributes)
	if err != nil {
		return mutation, validationForField("attributes must be a JSON object", "attributes")
	}
	mutation.Attributes = attrs

	// updated_at: el comparador de LWW (§7.4). Requerido SIEMPRE: sin la marca del cliente
	// no hay resolución que aplicar, y un servidor que estampara su hora propia rompería el
	// verbatim del contrato.
	if updatedAt == nil {
		return mutation, validationForField("updated_at is required: it is the LWW mutation time of the client", "updated_at")
	}
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(*updatedAt))
	if err != nil {
		return mutation, validationForField("updated_at must be RFC3339", "updated_at")
	}
	mutation.UpdatedAt = stamp

	return mutation, nil
}

// attributesObject acepta ausencia o JSON null → nil (sin atributos); cualquier otra cosa
// que no sea un objeto es cuerpo ilegal.
func attributesObject(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return nil, nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// usableUUID construye un uuid utilizable: no vacío, parseable, no nulo. required define
// si la ausencia vale —solo vale para campos opcionales del perfil de §4.6—.
func usableUUID(raw *string, field string, required bool) (uuid.UUID, error) {
	if raw == nil {
		if !required {
			return uuid.Nil, nil
		}
		return uuid.Nil, errors.New(field + " is required")
	}

	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return uuid.Nil, errors.New(field + " must not be empty")
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return uuid.Nil, errors.New(field + " must be a uuid")
	}
	if parsed == uuid.Nil {
		return uuid.Nil, errors.New(field + " must not be the nil uuid")
	}
	return parsed, nil
}

// uuidFromPath lee el id de la ruta de §4.7: tiene que ser utilizable, porque una rura que
// el router ya matcheó con un id ilegible no puede pedir una fila que no puede existir.
func uuidFromPath(pathValue string) (uuid.UUID, error) {
	if pathValue == "" {
		return uuid.Nil, errors.New("the product id is required in the path")
	}
	return usableUUID(&pathValue, "id", true)
}

// validationForField arma el 422 del contrato con el detalle del campo (§4.1): nombrar el
// campo, jamás el valor recibido.
func validationForField(message, field string) *httpx.Error {
	return httpx.Validation(message, map[string]string{"field": field})
}
