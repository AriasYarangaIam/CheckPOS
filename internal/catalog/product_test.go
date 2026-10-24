// Pruebas unitarias de /products (W3-BE-1): el contrato de la superficie HTTP sobre un
// store stub, sin base de datos. La respuesta 200, los 422 por campo prohibido y los dos
// criterios no negociables —el tenant sale SOLO del token, y `stock` no viaja en los
// payloads— se prueban acá sobre el router real del proceso, en proceso (httptest).
package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// testSigningSecret es la clave con la que se firman los tokens de estas pruebas. Tiene 32
// bytes: es el piso del contrato (docs/BACKEND.md §2.5).
const testSigningSecret = "test-only-signing-key-0123456789abcdef"

const (
	// tokenTenantID es el tenant que viaja DENTRO del token: la única fuente admisible.
	tokenTenantID = "019a0000-0000-7000-8000-0000000000f1"
	// tokenUserID es el usuario del token.
	tokenUserID = "019a0000-0000-7000-8000-0000000000f2"
	// forgedTenantID es el tenant que las peticiones falsifican en el cuerpo o en un
	// encabezado. Encabezado no es fuente de tenant (ARCHITECTURE.md:23).
	forgedTenantID = "019a0000-0000-7000-8000-0000000000f9"
)

// alwaysReady es el sondeo de preparación del router de estas pruebas: la base ya está
// cubierta por las pruebas de integración y un sondeo de identidad no es lo que se vence.
type alwaysReady struct{}

func (alwaysReady) Ping(context.Context) error { return nil }

// stubStore es el store de las pruebas unitarias: registra lo que el handler le pidió y
// devuelve lo que la prueba fijó —incluido un fallo— sin base de datos.
type stubStore struct {
	listCalls   []listCall
	createCalls []createCall
	updateCalls []updateCall

	list   []catalog.Product
	create catalog.Product
	update catalog.Product
	found  bool

	err error
}

type listCall struct {
	tenant catalog.ProductFilter
}

type createCall struct {
	tenantID   uuid.UUID
	mutation   catalog.ProductMutation
	receivedAt time.Time
	claim      synckey.ClaimInput
}

type updateCall struct {
	tenantID   uuid.UUID
	productID  uuid.UUID
	mutation   catalog.ProductMutation
	receivedAt time.Time
}

func (s *stubStore) List(_ context.Context, _ uuid.UUID, filter catalog.ProductFilter) ([]catalog.Product, error) {
	s.listCalls = append(s.listCalls, listCall{tenant: filter})
	if s.err != nil {
		return nil, s.err
	}
	return s.list, nil
}

func (s *stubStore) Create(_ context.Context, tenantID uuid.UUID, mutation catalog.ProductMutation, receivedAt time.Time, claim synckey.ClaimInput) (catalog.Product, []byte, error) {
	s.createCalls = append(s.createCalls, createCall{tenantID: tenantID, mutation: mutation, receivedAt: receivedAt, claim: claim})
	if s.err != nil {
		return catalog.Product{}, nil, s.err
	}
	// El snapshot es el cuerpo del contrato de la fila, igual que lo arma el store real.
	body, err := json.Marshal(catalog.RowOf(s.create))
	if err != nil {
		return catalog.Product{}, nil, err
	}
	return s.create, body, nil
}

func (s *stubStore) Update(_ context.Context, tenantID, id uuid.UUID, mutation catalog.ProductMutation, receivedAt time.Time) (catalog.Product, error) {
	s.updateCalls = append(s.updateCalls, updateCall{tenantID: tenantID, productID: id, mutation: mutation, receivedAt: receivedAt})
	if s.err != nil {
		return catalog.Product{}, s.err
	}
	return s.update, nil
}

// newCatalogRouter arma el router REAL del proceso —los sondeos, el registro de peticiones
// y las rutas montadas por opción, como en cmd/api— con las tres rutas de /products detrás
// del middleware de token. Devuelve el emisor para firmar tokens de prueba.
func newCatalogRouter(t *testing.T, store catalog.ProductStore, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := catalog.NewProductsHandler(store, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewProductsHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("GET /products", identity.RequireAccessToken(issuer, handler))
			mux.Handle("POST /products", identity.RequireAccessToken(issuer, handler))
			mux.Handle("PATCH /products/{id}", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// productToken firma un access token de empleado para el tenant y el usuario dados.
func productToken(t *testing.T, issuer *identity.TokenIssuer) string {
	t.Helper()

	token, err := issuer.Issue(identity.Claims{
		TenantID: uuid.MustParse(tokenTenantID),
		UserID:   uuid.MustParse(tokenUserID),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// doCatalogRequest envía una petición al router con el método y el cuerpo pedidos. Un token
// vacío omite la cabecera de credencial.
func doCatalogRequest(t *testing.T, router http.Handler, method, target, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost && target == "/products" {
		if _, provided := headers["Idempotency-Key"]; !provided {
			// Un POST encolable lleva su clave de claim (§7.6); la prueba que NO la quiere
			// la fija explícitamente en el mapa.
			request.Header.Set("Idempotency-Key", uuid.NewString())
		}
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// decodeEnvelope lee el sobre de error único (§4.1) de una respuesta.
func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details"`
	} `json:"error"`
} {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details any    `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error único: %v (%q)", err, recorder.Body.String())
	}
	return envelope
}

// validCreateBody es el cuerpo válido de referencia para las tablas de rechazo: los casos
// inválidos derivan de él cambiando UN campo a la vez.
const validCreateBody = `{` +
	`"id":"019a0000-0000-7000-8000-0000000000b1",` +
	`"name":"arroz especial 1kg",` +
	`"gtin":"7791234567890",` +
	`"price_cents":1550,` +
	`"uom":"kg",` +
	`"photo_url":"http://localhost:8080/photos/1/arroz.jpg",` +
	`"attributes":{"marca":"la especial"},` +
	`"updated_at":"2026-10-18T12:00:00Z"` +
	`}`

// productAPIRow es la fila de producto del contrato HTTP: todas las columnas de
// docs/DATABASE.md §4 `products`, con nulos como JSON null.
type productAPIRow struct {
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

// productsPage es la respuesta 200 del listado de §4.5: { "data": [...productos...] }.
type productsPage struct {
	Data []productAPIRow `json:"data"`
}

// decodeProductRow lee UNA fila de producto del contrato y falla si la respuesta no la trae.
func decodeProductRow(t *testing.T, recorder *httptest.ResponseRecorder) productAPIRow {
	t.Helper()

	var row productAPIRow
	if err := json.Unmarshal(recorder.Body.Bytes(), &row); err != nil {
		t.Fatalf("la respuesta no es una fila de producto JSON: %v (%q)", err, recorder.Body.String())
	}
	if row.ID == "" || row.Name == "" || row.UoM == "" {
		t.Fatalf("la fila no trae id, name y uom: %q", recorder.Body.String())
	}
	return row
}

// TestProductsRowAnswersTheContractShape: la respuesta es la fila de §4 con exactamente las
// columnas de docs/DATABASE.md §4 `products` —ni una clave de menos ni una de más.
func TestProductsRowAnswersTheContractShape(t *testing.T) {
	store := &stubStore{create: catalog.Product{
		ID:         uuid.MustParse("019a0000-0000-7000-8000-0000000000b1"),
		TenantID:   uuid.MustParse(tokenTenantID),
		Name:       "arroz especial 1kg",
		GTIN:       ptr("7791234567890"),
		PriceCents: 1550,
		UoM:        "kg",
		Stock:      "0.000",
		PhotoURL:   ptr("http://localhost:8080/photos/t1/arroz.jpg"),
		Attributes: []byte(`{"marca":"la especial"}`),
		CreatedAt:  time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 10, 18, 12, 0, 1, 0, time.UTC),
	}}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200: la fila resuelta vuelve con el estado de §7.7 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("la respuesta no es un objeto JSON: %v", err)
	}
	want := []string{"id", "name", "gtin", "price_cents", "uom", "stock", "photo_url", "attributes", "global_product_id", "created_at", "updated_at", "received_at", "deleted_at"}
	if len(decoded) != len(want) {
		t.Errorf("la fila tiene %d claves (%v), want exactamente las %d columnas de §4", len(decoded), decoded, len(want))
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Errorf("a la fila le falta la columna %q de §4: %s", key, decoded)
		}
	}

	row := decodeProductRow(t, recorder)
	if row.GTIN == nil || *row.GTIN != "7791234567890" {
		t.Errorf("gtin = %v, want 7791234567890", row.GTIN)
	}
	if row.Stock != "0.000" {
		t.Errorf("stock = %q, want el valor NUMERIC textual", row.Stock)
	}
	if row.DeletedAt != nil {
		t.Errorf("deleted_at = %v, want null en una fila activa", row.DeletedAt)
	}
}

// ptr crea un puntero a un valor copyable, para las filas de las pruebas.
func ptr[T any](value T) *T {
	copied := value
	return &copied
}

// TestProductsGetServesTheTenantCatalogPage: el listado de §4.5 es { "data": [...] } y el
// store recibe el tenant del TOKEN —jamás el del cuerpo ni de un encabezado.
func TestProductsGetServesTheTenantCatalogPage(t *testing.T) {
	store := &stubStore{list: []catalog.Product{{
		ID:         uuid.MustParse("019a0000-0000-7000-8000-0000000000c1"),
		TenantID:   uuid.MustParse(tokenTenantID),
		Name:       "aceite girasol 900ml",
		PriceCents: 890,
		UoM:        "unit",
		Stock:      "12.500",
		UpdatedAt:  time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC),
	}}}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodGet, "/products", token, "", map[string]string{
		"X-Tenant-Id": forgedTenantID,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	var page productsPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("la respuesta no es { \"data\": [...] }: %v (%q)", err, recorder.Body.String())
	}
	if len(page.Data) != 1 {
		t.Fatalf("data tiene %d filas, want 1 (%q)", len(page.Data), recorder.Body.String())
	}
	if page.Data[0].Name != "aceite girasol 900ml" {
		t.Errorf("data[0].name = %q, want el del store", page.Data[0].Name)
	}
	if strings.Contains(recorder.Body.String(), forgedTenantID) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", recorder.Body.String())
	}

	// El listado no es gratis: la forma del filtro se propaga al store y el tenant es el del token.
	if len(store.listCalls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(store.listCalls))
	}
}

// TestProductsGetRejectsAnUnusableLimit: `limit` presente pero ilegible es 422; un límite
// válido o ausente no rompe la lectura.
func TestProductsGetRejectsAnUnusableLimit(t *testing.T) {
	cases := map[string]string{
		"a word":        "abc",
		"a float":       "1.5",
		"a list":        "2,3",
		"an empty one":  "",
		"a huge number": "99999999999999999999",
	}

	for name, limit := range cases {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			target := "/products?limit=" + limit
			recorder := doCatalogRequest(t, router, http.MethodGet, target, token, "", nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.listCalls) != 0 {
				t.Errorf("el store corrió con un limit inválido")
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeValidationError)
			}
		})
	}
}

// TestProductsGetNeedsAValidToken: sin token o con uno inservible la ruta responde 401 y el
// store no corre.
func TestProductsGetNeedsAValidToken(t *testing.T) {
	cases := map[string]string{
		"no token":         "",
		"not a jwt at all": "no-soy-un-token",
	}

	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{}
			router, _ := newCatalogRouter(t, store, &strings.Builder{})

			recorder := doCatalogRequest(t, router, http.MethodGet, "/products", token, "", nil)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.listCalls) != 0 {
				t.Errorf("el store corrió %d vez/veces sin un token válido, want 0", len(store.listCalls))
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeUnauthenticated {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeUnauthenticated)
			}
		})
	}
}

// TestProductsHandlerWithoutTheMiddlewareFailsClosed: si un montaje olvidara el middleware,
// el handler no puede leer un tenant inexistente como si fuera un tenant válido. Responde 500
// y no consulta nada —el mismo fail-closed que identity (W2-BE-2).
func TestProductsHandlerWithoutTheMiddlewareFailsClosed(t *testing.T) {
	store := &stubStore{}
	var logBuf strings.Builder

	handler, err := catalog.NewProductsHandler(store, slog.New(slog.NewTextHandler(&logBuf, nil)))
	if err != nil {
		t.Fatalf("NewProductsHandler: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/products", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500: sin claims no hay tenant del que consultar (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
	}
	if len(store.listCalls) != 0 {
		t.Errorf("el store corrió %d vez/veces sin claims, want 0", len(store.listCalls))
	}
	if !strings.Contains(logBuf.String(), "middleware") {
		t.Errorf("la causa no quedó registrada: %q", logBuf.String())
	}
}

// TestProductsListFailsClosedWhenTheStoreIsBroken: un fallo del driver no es un 404 ni un
// 422; es un 500 con el sobre genérico y la causa en el registro del operador.
func TestProductsListFailsClosedWhenTheStoreIsBroken(t *testing.T) {
	store := &stubStore{err: errors.New("the tenant pool has no connection pool")}
	var logBuf strings.Builder
	router, issuer := newCatalogRouter(t, store, &logBuf)
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodGet, "/products", token, "", nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
	}
	if strings.Contains(recorder.Body.String(), "pool") {
		t.Errorf("el 500 filtró la causa interna: %q", recorder.Body.String())
	}
	if !strings.Contains(logBuf.String(), "pool") {
		t.Errorf("la causa no llegó al registro del operador: %q", logBuf.String())
	}
}

// TestProductsCreateTakesTheTenantOnlyFromTheToken: el cuerpo y los encabezados pueden traer
// el tenant que quieran; el único tenant que llega al store es el del token. La respuesta es
// la fila RESUELTA del store: una creación podrida de colisión vuelve con el nombre
// renombrado o el gtin en NULL, en silencio (docs/DATABASE.md §7.7).
func TestProductsCreateTakesTheTenantOnlyFromTheToken(t *testing.T) {
	store := &stubStore{create: catalog.Product{
		ID:         uuid.MustParse("019a0000-0000-7000-8000-0000000000b1"),
		TenantID:   uuid.MustParse(tokenTenantID),
		Name:       "arroz especial 1kg (019a0000d1)",
		GTIN:       nil,
		PriceCents: 1550,
		UoM:        "kg",
		Stock:      "0.000",
		CreatedAt:  time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 10, 18, 12, 0, 1, 0, time.UTC),
	}}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	body := `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"arroz especial 1kg",` +
		`"price_cents":1550,"uom":"kg","updated_at":"2026-10-18T12:00:00Z",` +
		`"tenant_id":"` + forgedTenantID + `","tenantId":"` + forgedTenantID + `"}`
	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, body, map[string]string{
		"X-Tenant-Id": forgedTenantID,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200: el campo de tenant desconocido se ignora, no se rechaza (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), forgedTenantID) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", recorder.Body.String())
	}
	if len(store.createCalls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(store.createCalls))
	}

	call := store.createCalls[0]
	if call.tenantID.String() != tokenTenantID {
		t.Errorf("tenant de la creación = %v, want el del token %s (nunca el del cuerpo ni el del encabezado)", call.tenantID, tokenTenantID)
	}
	if call.mutation.Name == nil || *call.mutation.Name != "arroz especial 1kg" {
		t.Errorf("name de la creación = %v, want \"arroz especial 1kg\"", call.mutation.Name)
	}
	if call.mutation.GTIN != nil {
		t.Errorf("gtin de la creación = %v, want nil: el campo no viajó", call.mutation.GTIN)
	}
	if call.mutation.PriceCents == nil || *call.mutation.PriceCents != 1550 {
		t.Errorf("price_cents de la creación = %v, want 1550", call.mutation.PriceCents)
	}
	if call.mutation.UoM == nil || *call.mutation.UoM != "kg" {
		t.Errorf("uom de la creación = %v, want \"kg\"", call.mutation.UoM)
	}
	if !call.mutation.UpdatedAt.Equal(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("updated_at de la creación = %v, want la marca del cliente verbatim", call.mutation.UpdatedAt)
	}
	if time.Since(call.receivedAt) > time.Minute {
		t.Errorf("received_at = %v, want el momento de la petición", call.receivedAt)
	}

	// La respuesta es la fila resuelta, no lo que el cliente mandó: con el renombrado y el
	// gtin en NULL del stub, tal cual los entrega la resolución silenciosa.
	row := decodeProductRow(t, recorder)
	if row.Name != "arroz especial 1kg (019a0000d1)" {
		t.Errorf("name de la respuesta = %q, want el renombrado del store", row.Name)
	}
	if row.GTIN != nil {
		t.Errorf("gtin de la respuesta = %v, want null", row.GTIN)
	}
	if row.Stock != "0.000" {
		t.Errorf("stock de la respuesta = %q, want el del store", row.Stock)
	}
}

// TestProductsCreateRejectsABodyThatIsNotTheContractBody: cada campo invalidado una vez
// devuelve 422 y NUNCA llega al store.
func TestProductsCreateRejectsABodyThatIsNotTheContractBody(t *testing.T) {
	cases := map[string]string{
		"absent id":             `{"name":"a","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"the nil uuid id":       `{"id":"00000000-0000-0000-0000-000000000000","name":"a","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"a garbage id":          `{"id":"no-soy-uuid","name":"a","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"a null id":             `{"id":null,"name":"a","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"absent name":           `{"id":"019a0000-0000-7000-8000-0000000000b1","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"a blank name":          `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"   ","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"absent price":          `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"a null price":          `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":null,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"a negative price":      `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":-1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"a float price":         `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":15.5,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"absent uom":            `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":1,"updated_at":"2026-10-18T12:00:00Z"}`,
		"a blank uom":           `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":1,"uom":"  ","updated_at":"2026-10-18T12:00:00Z"}`,
		"an empty gtin":         `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","gtin":"","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"an empty photo_url":    `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","photo_url":"","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"attributes as a list":  `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","attributes":[1,2],"price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"attributes as a text":  `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","attributes":"x","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"attributes as garbage": `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","attributes":{,"price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z"}`,
		"absent updated_at":     `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":1,"uom":"unit"}`,
		"a garbage updated_at":  `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":1,"uom":"unit","updated_at":"ayer"}`,
		"garbage adoption":      `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":1,"uom":"unit","updated_at":"2026-10-18T12:00:00Z","global_product_id":"no-soy-uuid"}`,
		"not json":              `name=arroz`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.createCalls) != 0 {
				t.Errorf("el store corrió con un cuerpo inválido")
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeValidationError)
			}
		})
	}
}

// TestProductsCreateRejectsAStockField: `stock` está EXCLUIDO de los payloads de §4.6; el
// stock converge solo por deltas firmados (docs/DATABASE.md §4; ARCHITECTURE.md:176), así
// que un cuerpo que lo traiga, con cualquier valor, es 422 y no escribe nada.
func TestProductsCreateRejectsAStockField(t *testing.T) {
	cases := map[string]string{
		"a number":  `"stock":12.5`,
		"a text":    `"stock":"a dedo"`,
		"an object": `"stock":{"qty":1}`,
		"a null":    `"stock":null`,
	}
	const prefix = `{"id":"019a0000-0000-7000-8000-0000000000b1","name":"a","price_cents":1,"uom":"unit",`

	for name, stock := range cases {
		t.Run(name, func(t *testing.T) {
			body := prefix + stock + `,"updated_at":"2026-10-18T12:00:00Z"}`

			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.createCalls) != 0 {
				t.Errorf("el store corrió con un campo prohibido en el cuerpo")
			}
			envelope := decodeEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			details, ok := envelope.Error.Details.(map[string]any)
			if !ok || details["field"] != "stock" {
				t.Errorf("details = %#v, want un objeto que nombre el campo stock", envelope.Error.Details)
			}
		})
	}
}

// TestProductsCreateMapsADuplicateIdToValidation: el id cliente-generado colisiona con una
// fila que este tenant no alcanza a ver → 422 de validación. Contra una fila PROPIA la
// creación es naturalmente idempotente (la prueba de integración cubre ese camino).
func TestProductsCreateMapsADuplicateIdToValidation(t *testing.T) {
	store := &stubStore{err: catalog.ErrDuplicateProductID}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeValidationError {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
	}
	details, ok := envelope.Error.Details.(map[string]any)
	if !ok || details["field"] != "id" {
		t.Errorf("details = %#v, want un objeto que nombre el campo id", envelope.Error.Details)
	}
}

// TestProductsCreateFailsClosedWhenTheStoreIsBroken: un fallo inesperado del store es 500
// con el sobre genérico; la causa queda en el registro del operador.
func TestProductsCreateFailsClosedWhenTheStoreIsBroken(t *testing.T) {
	store := &stubStore{err: errors.New("the tenant pool has no connection pool")}
	var logBuf strings.Builder
	router, issuer := newCatalogRouter(t, store, &logBuf)
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)

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

// TestProductsPatchSendsOnlyTheProvidedFields: PATCH es parcial —se envía al store solo lo
// que el cuerpo trajo, y la respuesta es la fila resuelta.
func TestProductsPatchSendsOnlyTheProvidedFields(t *testing.T) {
	productID := "019a0000-0000-7000-8000-0000000000b1"
	store := &stubStore{update: catalog.Product{
		ID:         uuid.MustParse(productID),
		TenantID:   uuid.MustParse(tokenTenantID),
		Name:       "arroz premium 1kg",
		PriceCents: 1600,
		UoM:        "kg",
		Stock:      "0.000",
		UpdatedAt:  time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC),
	}}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	body := `{"name":"arroz premium 1kg","price_cents":1600,"updated_at":"2026-10-19T08:00:00Z"}`
	recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, body, map[string]string{
		"X-Tenant-Id": forgedTenantID,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), forgedTenantID) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", recorder.Body.String())
	}
	if len(store.updateCalls) != 1 {
		t.Fatalf("el store corrió %d vez/veces, want 1", len(store.updateCalls))
	}

	call := store.updateCalls[0]
	if call.productID.String() != productID {
		t.Errorf("product_id del patch = %v, want el de la ruta", call.productID)
	}
	if call.tenantID.String() != tokenTenantID {
		t.Errorf("tenant del patch = %v, want el del token %s", call.tenantID, tokenTenantID)
	}
	if call.mutation.Name == nil || *call.mutation.Name != "arroz premium 1kg" {
		t.Errorf("name del patch = %v, want el provisto", call.mutation.Name)
	}
	if call.mutation.PriceCents == nil || *call.mutation.PriceCents != 1600 {
		t.Errorf("price_cents del patch = %v, want el provisto", call.mutation.PriceCents)
	}
	if call.mutation.GTIN != nil {
		t.Errorf("gtin del patch = %v, want nil: el campo no viajó", call.mutation.GTIN)
	}
	if call.mutation.UoM != nil {
		t.Errorf("uom del patch = %v, want nil: el campo no viajó", call.mutation.UoM)
	}
	if call.mutation.PhotoURL != nil {
		t.Errorf("photo_url del patch = %v, want nil: el campo no viajó", call.mutation.PhotoURL)
	}
	if !call.mutation.UpdatedAt.Equal(time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("updated_at del patch = %v, want la marca del cliente verbatim", call.mutation.UpdatedAt)
	}

	row := decodeProductRow(t, recorder)
	if row.Name != "arroz premium 1kg" || row.PriceCents != 1600 {
		t.Errorf("la respuesta no es la fila resuelta: %+v", row)
	}
}

// TestProductsPatchRejectsARemovalAttempt: PATCH es aditivo —nunca quita por PATCH
// (docs/DATABASE.md §4 `products` mutability; SPRINTS.md W3-BE-1), así que un campo
// explícitamente nulo es un intento de quitar y devuelve 422 sin tocar el store.
func TestProductsPatchRejectsARemovalAttempt(t *testing.T) {
	cases := map[string]struct {
		body  string
		field string
	}{
		"a null name":       {body: `{"name":null,"updated_at":"2026-10-19T08:00:00Z"}`, field: "name"},
		"a null gtin":       {body: `{"gtin":null,"updated_at":"2026-10-19T08:00:00Z"}`, field: "gtin"},
		"a null price":      {body: `{"price_cents":null,"updated_at":"2026-10-19T08:00:00Z"}`, field: "price_cents"},
		"a null uom":        {body: `{"uom":null,"updated_at":"2026-10-19T08:00:00Z"}`, field: "uom"},
		"a null photo_url":  {body: `{"photo_url":null,"updated_at":"2026-10-19T08:00:00Z"}`, field: "photo_url"},
		"a null attributes": {body: `{"attributes":null,"updated_at":"2026-10-19T08:00:00Z"}`, field: "attributes"},
		"a null updated_at": {body: `{"name":"another","updated_at":null}`, field: "updated_at"},
		"everything null":   {body: `{"name":null,"gtin":null,"price_cents":null,"uom":null,"updated_at":"2026-10-19T08:00:00Z"}`},
	}
	productID := "019a0000-0000-7000-8000-0000000000b1"

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, tc.body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.updateCalls) != 0 {
				t.Errorf("el store corrió con un intento de quitar campo")
			}
			envelope := decodeEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			if tc.field != "" {
				details, ok := envelope.Error.Details.(map[string]any)
				if !ok || details["field"] != tc.field {
					t.Errorf("details = %#v, want un objeto que nombre el campo %s", envelope.Error.Details, tc.field)
				}
			}
		})
	}
}

// TestProductsPatchRejectsAStockField: el daño acotado del LWW (§7.4) no toca stock —un
// patch que lo traiga es 422, aunque el valor sea nulo o absurdo.
func TestProductsPatchRejectsAStockField(t *testing.T) {
	cases := map[string]string{
		"a number": `"stock":3`,
		"a text":   `"stock":"algo"`,
		"a null":   `"stock":null`,
	}
	productID := "019a0000-0000-7000-8000-0000000000b1"

	for name, stock := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"name":"another",` + stock + `,"updated_at":"2026-10-19T08:00:00Z"}`

			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.updateCalls) != 0 {
				t.Errorf("el store corrió con stock en el cuerpo")
			}
		})
	}
}

// TestProductsPatchRejectsUnusableProvidedFields: un campo PROVISTO pero inservible (blanco,
// negativo, no objeto) es 422 de validación —quitar es distinto de mandar basura—.
func TestProductsPatchRejectsUnusableProvidedFields(t *testing.T) {
	cases := map[string][]string{
		"a blank name":         {`"name":"   "`, "name"},
		"an empty gtin":        {`"gtin":""`, "gtin"},
		"a negative price":     {`"price_cents":-5`, "price_cents"},
		"a float price":        {`"price_cents":15.5`, "price_cents"},
		"a blank uom":          {`"uom":" "`, "uom"},
		"an empty photo_url":   {`"photo_url":""`, "photo_url"},
		"attributes as list":   {`"attributes":[1]`, "attributes"},
		"attributes as text":   {`"attributes":"x"`, "attributes"},
		"a garbage updated_at": {`"updated_at":"mañana"`, "updated_at"},
	}
	productID := "019a0000-0000-7000-8000-0000000000b1"

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{` + tc[0] + `}`
			if tc[0] != `"updated_at":"mañana"` {
				body = `{` + tc[0] + `,"updated_at":"2026-10-19T08:00:00Z"}`
			}

			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.updateCalls) != 0 {
				t.Errorf("el store corrió con un campo provisto inservible")
			}
			envelope := decodeEnvelope(t, recorder)
			details, ok := envelope.Error.Details.(map[string]any)
			if !ok || details["field"] != tc[1] {
				t.Errorf("details = %#v, want un objeto que nombre el campo %s", envelope.Error.Details, tc[1])
			}
		})
	}
}

// TestProductsPatchRequiresAnUpdatedAt: el comparador LWW (§7.4) es la marca de mutación del
// cliente, así que un patch sin `updated_at` no es resolvible: 422.
func TestProductsPatchRequiresAnUpdatedAt(t *testing.T) {
	productID := "019a0000-0000-7000-8000-0000000000b1"

	store := &stubStore{}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, `{"name":"another"}`, nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if len(store.updateCalls) != 0 {
		t.Errorf("el store corrió sin updated_at en el cuerpo")
	}
	envelope := decodeEnvelope(t, recorder)
	details, ok := envelope.Error.Details.(map[string]any)
	if !ok || details["field"] != "updated_at" {
		t.Errorf("details = %#v, want un objeto que nombre el campo updated_at", envelope.Error.Details)
	}
}

// TestProductsPatchRejectsAProductIdThatIsNotAUsableUUID: el id de la ruta tiene que ser un
// uuid utilizable; una ruta con id ilegible es 422 y el store no corre.
func TestProductsPatchRejectsAProductIdThatIsNotAUsableUUID(t *testing.T) {
	cases := map[string]string{
		"not a uuid":   "no-soy-uuid",
		"the nil uuid": "00000000-0000-0000-0000-000000000000",
	}

	for name, productID := range cases {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})
			token := productToken(t, issuer)

			body := `{"name":"another","updated_at":"2026-10-19T08:00:00Z"}`
			recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, body, nil)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if len(store.updateCalls) != 0 {
				t.Errorf("el store corrió con un id de ruta ilegible")
			}
		})
	}
}

// TestProductsPatchAnswers404ForAProductOutsideTheTenant: el id existe en OTRO tenant → el
// RLS devuelve cero filas y las dos causas del 404 (§4.1) comparten la MISMA respuesta.
func TestProductsPatchAnswers404ForAProductOutsideTheTenant(t *testing.T) {
	store := &stubStore{err: catalog.ErrUnknownProduct}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	token := productToken(t, issuer)

	productID := "019a0000-0000-7000-8000-0000000000b1"
	body := `{"name":"another","updated_at":"2026-10-19T08:00:00Z"}`
	recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, body, nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("estado = %d, want 404 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeNotFound {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeNotFound)
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "tenant") {
		t.Errorf("el 404 distingue «fuera del tenant» de «no existe»: %q", recorder.Body.String())
	}
}

// TestProductsPatchFailsClosedWhenTheStoreIsBroken: un fallo inesperado del store es 500
// con el sobre genérico; la causa queda en el registro del operador.
func TestProductsPatchFailsClosedWhenTheStoreIsBroken(t *testing.T) {
	store := &stubStore{err: errors.New("the tenant pool has no connection pool")}
	var logBuf strings.Builder
	router, issuer := newCatalogRouter(t, store, &logBuf)
	token := productToken(t, issuer)

	productID := "019a0000-0000-7000-8000-0000000000b1"
	body := `{"name":"another","updated_at":"2026-10-19T08:00:00Z"}`
	recorder := doCatalogRequest(t, router, http.MethodPatch, "/products/"+productID, token, body, nil)

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

// Pruebas del claim de idempotencia en la superficie de creación (Sprint 6, W6-BE-1;
// docs/DATABASE.md §7.1, §7.6; docs/BACKEND.md §6.1). `POST /products` es uno de los seis
// POST encolables, así que lleva la clave del cliente y responde con el snapshot: la
// repetición y la creación fresca devuelven la MISMA respuesta para la MISMA clave.

// postProductWithoutDefault envía el POST con EXACTAMENTE las cabeceras indicadas: es la
// única forma de probar la ausencia de la clave, que el helper general completa por defecto.
func postProductWithoutDefault(t *testing.T, router http.Handler, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/products", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// decodeErrorEnvelope lee el sobre de error único y lo devuelve con su código y detalles.
func decodeErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) (string, map[string]string) {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error: %v (%q)", err, recorder.Body.String())
	}
	return envelope.Error.Code, envelope.Error.Details
}

// TestProductCreateRequiresTheIdempotencyKey: sin la clave del cliente no hay claim que
// ganar, así que la petición es 422 con el campo nombrado y el store NO corre.
func TestProductCreateRequiresTheIdempotencyKey(t *testing.T) {
	store := &stubStore{}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})

	recorder := postProductWithoutDefault(t, router, productToken(t, issuer), validCreateBody, nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	code, details := decodeErrorEnvelope(t, recorder)
	if code != httpx.CodeValidationError {
		t.Errorf("code = %q, want %q", code, httpx.CodeValidationError)
	}
	if details["field"] != "Idempotency-Key" {
		t.Errorf("details.field = %q, want Idempotency-Key", details["field"])
	}
	if len(store.createCalls) != 0 {
		t.Errorf("el store corrió sin clave de claim: %v", store.createCalls)
	}
}

// TestProductCreateClaimsTheRawBodyAndAnswersWithTheSnapshot: el claim viaja con la clave
// del encabezado y el hash del cuerpo RAW, y la respuesta es el snapshot del store byte a
// byte —no una re-serialización del handler—.
func TestProductCreateClaimsTheRawBodyAndAnswersWithTheSnapshot(t *testing.T) {
	store := &stubStore{create: catalog.Product{
		ID:         uuid.MustParse("019a0000-0000-7000-8000-0000000000b1"),
		TenantID:   uuid.MustParse(tokenTenantID),
		Name:       "arroz especial 1kg",
		PriceCents: 1550,
		UoM:        "kg",
		Stock:      "0.000",
		CreatedAt:  time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 10, 18, 12, 0, 1, 0, time.UTC),
	}}
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})

	recorder := postProductWithoutDefault(t, router, productToken(t, issuer), validCreateBody,
		map[string]string{"Idempotency-Key": "clave-producto-1"})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if len(store.createCalls) != 1 {
		t.Fatalf("llamadas al store = %d, want 1", len(store.createCalls))
	}
	claim := store.createCalls[0].claim
	if claim.Key != "clave-producto-1" {
		t.Errorf("claim.Key = %q, want la clave del encabezado", claim.Key)
	}
	if want := synckey.PayloadHash("clave-producto-1", []byte(validCreateBody)); claim.PayloadHash != want {
		t.Errorf("claim.PayloadHash = %q, want el hash del cuerpo RAW %q", claim.PayloadHash, want)
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &row); err != nil {
		t.Fatalf("el snapshot no es una fila de producto: %v (%q)", err, recorder.Body.String())
	}
	if len(row) != 13 {
		t.Errorf("el snapshot trae %d columnas, want las 13 de §4: %v", len(row), row)
	}
}

// TestProductCreateAnswersTheClaimBranches: la clave reutilizada con otro payload es el 422
// con el código propio del claim, y el duplicado en vuelo es el 409 con Retry-After.
func TestProductCreateAnswersTheClaimBranches(t *testing.T) {
	cases := []struct {
		name        string
		storeErr    error
		wantStatus  int
		wantCode    string
		wantField   string
		wantBackoff bool
	}{
		{
			name:       "payload mismatch",
			storeErr:   synckey.ErrPayloadMismatch,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "idempotency.payload_mismatch",
			wantField:  "Idempotency-Key",
		},
		{
			name:        "in flight",
			storeErr:    synckey.ErrInFlight,
			wantStatus:  http.StatusConflict,
			wantCode:    httpx.CodeConflict,
			wantBackoff: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &stubStore{err: testCase.storeErr}
			router, issuer := newCatalogRouter(t, store, &strings.Builder{})

			recorder := postProductWithoutDefault(t, router, productToken(t, issuer), validCreateBody,
				map[string]string{"Idempotency-Key": "clave-producto-2"})

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("estado = %d, want %d (cuerpo %q)", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			code, details := decodeErrorEnvelope(t, recorder)
			if code != testCase.wantCode {
				t.Errorf("code = %q, want %q", code, testCase.wantCode)
			}
			if testCase.wantField != "" && details["field"] != testCase.wantField {
				t.Errorf("details.field = %q, want %q", details["field"], testCase.wantField)
			}
			if testCase.wantBackoff && recorder.Header().Get("Retry-After") == "" {
				t.Error("sin Retry-After: el worker no tiene backoff que respetar (§7.1 Branch C)")
			}
		})
	}
}
