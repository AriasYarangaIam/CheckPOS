package synccursor_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/fiado"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/synccursor"
)

// Estas pruebas fijan el contrato de `GET /sync/cursor` (Sprint 6, W6-BE-3; docs/BACKEND.md
// §4.8; docs/DATABASE.md §7.3) en su forma congelada por la decisión D1 de
// odd/tasks/sprint-6-offline-sync.md:
//
//   - la respuesta es `{cursor, products[], fiado_accounts[]}`, con la fila de producto de
//     `GET /products` —la MISMA, no una copia— y la cuenta sin saldo ni tenant;
//   - el cursor avanza SOLO al máximo `updated_at` realmente devuelto y jamás a `now()`;
//   - sin filas se repite el cursor recibido, y en el pull inicial vacío es `null`;
//   - los tombstones de producto viajan; y
//   - un cursor ilegible es 422 y no se lee ninguna tabla.

const (
	testSigningSecret = "secreto-de-prueba-solo-para-tests-0123456789"
	testTenantID      = "0192f3a4-5b6c-7d8e-9f01-23456789abcd"
	testUserID        = "0192f3a4-5b6c-7d8e-9f01-23456789abcf"
)

// alwaysReady es el sondeo de preparación del router real, siempre verde.
type alwaysReady struct{}

// Ready cumple la interfaz del sondeo de preparación.
func (alwaysReady) Ping(context.Context) error { return nil }

// fakeProducts es la fuente de `products` del pull: devuelve lo que la prueba sembró y
// registra con qué cursor se la llamó, para poder afirmar el avance del cursor.
type fakeProducts struct {
	rows  []catalog.Product
	err   error
	calls []*time.Time
}

// ListUpdatedSince implementa la fuente de producto del pull.
func (f *fakeProducts) ListUpdatedSince(_ context.Context, _ uuid.UUID, after *time.Time) ([]catalog.Product, error) {
	f.calls = append(f.calls, after)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// fakeAccounts es la fuente de `fiado_accounts` del pull, con el mismo registro de cursor.
type fakeAccounts struct {
	rows  []fiado.Account
	err   error
	calls []*time.Time
}

// ListUpdatedSince implementa la fuente de cuentas del pull.
func (f *fakeAccounts) ListUpdatedSince(_ context.Context, _ uuid.UUID, after *time.Time) ([]fiado.Account, error) {
	f.calls = append(f.calls, after)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// newCursorRouter arma el router REAL del proceso con la ruta del cursor detrás del
// middleware de token, como cmd/api, y devuelve el emisor para firmar el token de prueba.
func newCursorRouter(t *testing.T, products synccursor.ProductSource, accounts synccursor.AccountSource) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := synccursor.NewCursorHandler(products, accounts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewCursorHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("GET /sync/cursor", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// cursorToken firma el access token del tenant de prueba.
func cursorToken(t *testing.T, issuer *identity.TokenIssuer) string {
	t.Helper()

	token, err := issuer.Issue(identity.Claims{
		TenantID: uuid.MustParse(testTenantID),
		UserID:   uuid.MustParse(testUserID),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// pullCursor envía el pull con el cursor crudo indicado (vacío = sin parámetro).
func pullCursor(t *testing.T, router http.Handler, token, cursor string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/sync/cursor"
	if cursor != "" {
		target += "?cursor=" + cursor
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// cursorResponse es el cuerpo del contrato tal como viaja en el cable.
type cursorResponse struct {
	Cursor        *string           `json:"cursor"`
	Products      []json.RawMessage `json:"products"`
	FiadoAccounts []struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		Phone      *string `json:"phone"`
		CreatedAt  string  `json:"created_at"`
		UpdatedAt  string  `json:"updated_at"`
		ReceivedAt string  `json:"received_at"`
	} `json:"fiado_accounts"`
}

// decodeCursor lee la respuesta del pull.
func decodeCursor(t *testing.T, recorder *httptest.ResponseRecorder) cursorResponse {
	t.Helper()

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, quería 200; cuerpo %s", recorder.Code, recorder.Body.String())
	}
	var body cursorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar la respuesta del cursor: %v (%s)", err, recorder.Body.String())
	}
	return body
}

// mustTime parsea una marca del contrato.
func mustTime(t *testing.T, raw string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parsear %q: %v", raw, err)
	}
	return parsed
}

// sampleProduct arma una fila de producto del tenant con la marca de mutación dada.
func sampleProduct(t *testing.T, id, name, updatedAt string) catalog.Product {
	t.Helper()

	return catalog.Product{
		ID:         uuid.MustParse(id),
		TenantID:   uuid.MustParse(testTenantID),
		Name:       name,
		PriceCents: 1250,
		UoM:        "unit",
		Stock:      "0.000",
		CreatedAt:  mustTime(t, "2026-10-01T00:00:00Z"),
		UpdatedAt:  mustTime(t, updatedAt),
		ReceivedAt: mustTime(t, "2026-10-01T00:00:00Z"),
	}
}

// TestCursorPullServesTheFrozenShapeAndAdvancesToTheNewestRow: el pull inicial devuelve las
// dos tablas en la forma congelada y un cursor que es el MAX(updated_at) realmente devuelto.
func TestCursorPullServesTheFrozenShapeAndAdvancesToTheNewestRow(t *testing.T) {
	newest := "2026-10-22T03:06:38.123456Z"
	products := &fakeProducts{rows: []catalog.Product{
		sampleProduct(t, "0192f3a4-5b6c-7d8e-9f01-23456789ac01", "Arroz", "2026-10-21T10:00:00.000001Z"),
		sampleProduct(t, "0192f3a4-5b6c-7d8e-9f01-23456789ac02", "Fideo", "2026-10-21T11:00:00Z"),
	}}
	accounts := &fakeAccounts{rows: []fiado.Account{{
		ID:         uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789ac03"),
		TenantID:   uuid.MustParse(testTenantID),
		Name:       "Rosa Quispe",
		Phone:      nil,
		CreatedAt:  mustTime(t, "2026-10-20T09:00:00Z"),
		UpdatedAt:  mustTime(t, newest),
		ReceivedAt: mustTime(t, "2026-10-20T09:00:00Z"),
	}}}
	router, issuer := newCursorRouter(t, products, accounts)

	body := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), ""))

	if body.Cursor == nil {
		t.Fatal("cursor = null en un pull con filas: quería el máximo devuelto")
	}
	if *body.Cursor != newest {
		t.Errorf("cursor = %q, quería %q (el MAX realmente devuelto, nunca now())", *body.Cursor, newest)
	}
	if len(body.Products) != 2 {
		t.Fatalf("productos = %d, quería 2", len(body.Products))
	}

	// La fila de producto es la MISMA de GET /products: las columnas de §4 completas.
	var row map[string]any
	if err := json.Unmarshal(body.Products[0], &row); err != nil {
		t.Fatalf("decodificar la fila de producto: %v", err)
	}
	for _, column := range []string{"id", "name", "gtin", "price_cents", "uom", "stock",
		"photo_url", "attributes", "global_product_id", "created_at", "updated_at", "received_at", "deleted_at"} {
		if _, ok := row[column]; !ok {
			t.Errorf("la fila de producto del cursor no trae la columna %q de GET /products", column)
		}
	}
	if len(row) != 13 {
		t.Errorf("la fila de producto trae %d columnas, quería las 13 del contrato: %v", len(row), row)
	}

	if len(body.FiadoAccounts) != 1 {
		t.Fatalf("cuentas = %d, quería 1", len(body.FiadoAccounts))
	}
	account := body.FiadoAccounts[0]
	if account.Name != "Rosa Quispe" || account.Phone != nil {
		t.Errorf("cuenta = %+v, quería la fila de §4 con el teléfono nulo explícito", account)
	}
	if account.UpdatedAt != newest {
		t.Errorf("updated_at de la cuenta = %q, quería %q", account.UpdatedAt, newest)
	}

	// Sin cursor recibido, las dos fuentes se consultan por el pull inicial completo.
	if len(products.calls) != 1 || products.calls[0] != nil {
		t.Errorf("llamadas a products = %v, quería una con cursor nulo", products.calls)
	}
	if len(accounts.calls) != 1 || accounts.calls[0] != nil {
		t.Errorf("llamadas a accounts = %v, quería una con cursor nulo", accounts.calls)
	}
}

// TestCursorPullIncludesTheTombstones: un producto borrado viaja con `deleted_at` puesto:
// el pull del cursor es justo la ruta que trae los tombstones (§4.8, §7.3).
func TestCursorPullIncludesTheTombstones(t *testing.T) {
	tombstone := sampleProduct(t, "0192f3a4-5b6c-7d8e-9f01-23456789ac04", "Azúcar", "2026-10-22T04:00:00Z")
	deletedAt := mustTime(t, "2026-10-22T04:00:00Z")
	tombstone.DeletedAt = &deletedAt

	router, issuer := newCursorRouter(t, &fakeProducts{rows: []catalog.Product{tombstone}}, &fakeAccounts{})

	body := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), ""))

	if len(body.Products) != 1 {
		t.Fatalf("productos = %d, quería 1 tombstone", len(body.Products))
	}
	var row struct {
		DeletedAt *string `json:"deleted_at"`
	}
	if err := json.Unmarshal(body.Products[0], &row); err != nil {
		t.Fatalf("decodificar la fila: %v", err)
	}
	if row.DeletedAt == nil || *row.DeletedAt != "2026-10-22T04:00:00Z" {
		t.Errorf("deleted_at = %v, quería la marca del tombstone: el cursor no filtra las filas borradas", row.DeletedAt)
	}
}

// TestCursorPullEchoesTheReceivedCursorWhenNothingWasReturned: sin filas se repite el
// cursor recibido, nunca el reloj del servidor.
func TestCursorPullEchoesTheReceivedCursorWhenNothingWasReturned(t *testing.T) {
	received := "2026-10-22T03:06:38.123456Z"
	products := &fakeProducts{}
	accounts := &fakeAccounts{}
	router, issuer := newCursorRouter(t, products, accounts)

	body := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), received))

	if body.Cursor == nil {
		t.Fatal("cursor = null, quería el cursor recibido de vuelta")
	}
	if *body.Cursor != received {
		t.Errorf("cursor = %q, quería %q (eco del recibido, no now())", *body.Cursor, received)
	}
	if body.Products == nil || body.FiadoAccounts == nil {
		t.Errorf("las listas vacías viajan como [] y no como null: products=%v fiado_accounts=%v", body.Products, body.FiadoAccounts)
	}
	if len(body.Products) != 0 || len(body.FiadoAccounts) != 0 {
		t.Errorf("quería listas vacías, obtuve %d productos y %d cuentas", len(body.Products), len(body.FiadoAccounts))
	}

	// El cursor recibido es el que se consultó: el pull es incremental, no un re-barrido.
	if len(products.calls) != 1 || products.calls[0] == nil || !products.calls[0].Equal(mustTime(t, received)) {
		t.Errorf("el cursor consultado no fue el recibido: %v", products.calls)
	}
	if len(accounts.calls) != 1 || accounts.calls[0] == nil || !accounts.calls[0].Equal(mustTime(t, received)) {
		t.Errorf("el cursor de cuentas consultado no fue el recibido: %v", accounts.calls)
	}
}

// TestCursorPullOfAnEmptyInitialAnswersNullCursor: el pull inicial vacío no inventa un
// cursor: `null` hasta que exista una fila que lo mueva.
func TestCursorPullOfAnEmptyInitialAnswersNullCursor(t *testing.T) {
	router, issuer := newCursorRouter(t, &fakeProducts{}, &fakeAccounts{})

	body := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), ""))

	if body.Cursor != nil {
		t.Errorf("cursor = %q, quería null: un pull inicial vacío no tiene máximo que devolver", *body.Cursor)
	}
}

// TestCursorPullOfAFutureCursorIsEmpty: un cursor adelantado al reloj es seguro hacia
// adelante — devuelve vacío, nunca un error (§7.3).
func TestCursorPullOfAFutureCursorIsEmpty(t *testing.T) {
	future := "2030-01-01T00:00:00Z"
	router, issuer := newCursorRouter(t, &fakeProducts{}, &fakeAccounts{})

	body := decodeCursor(t, pullCursor(t, router, cursorToken(t, issuer), future))

	if body.Cursor == nil || *body.Cursor != future {
		t.Errorf("cursor = %v, quería el eco de %q", body.Cursor, future)
	}
	if len(body.Products) != 0 || len(body.FiadoAccounts) != 0 {
		t.Errorf("un cursor futuro devuelve vacío: %d productos, %d cuentas", len(body.Products), len(body.FiadoAccounts))
	}
}

// TestCursorRejectsAnUnparseableCursor: un cursor que no es RFC3339 es 422 con el campo, y
// no se lee ninguna tabla: el rechazo ocurre antes del viaje a la base.
func TestCursorRejectsAnUnparseableCursor(t *testing.T) {
	products := &fakeProducts{}
	accounts := &fakeAccounts{}
	router, issuer := newCursorRouter(t, products, accounts)

	recorder := pullCursor(t, router, cursorToken(t, issuer), "ayer-a-las-tres")

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, quería 422; cuerpo %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decodificar el sobre de error: %v", err)
	}
	if envelope.Error.Code != httpx.CodeValidationError {
		t.Errorf("code = %q, quería %q", envelope.Error.Code, httpx.CodeValidationError)
	}
	if envelope.Error.Details["field"] != "cursor" {
		t.Errorf("details.field = %q, quería cursor", envelope.Error.Details["field"])
	}
	if len(products.calls) != 0 || len(accounts.calls) != 0 {
		t.Errorf("el cursor ilegible tocó la base: %v / %v", products.calls, accounts.calls)
	}
}

// TestCursorPullRequiresAnAccessToken: la ruta está detrás del middleware de token y sin
// credencial no se lee ninguna tabla (§4.8, auth por token).
func TestCursorPullRequiresAnAccessToken(t *testing.T) {
	products := &fakeProducts{}
	accounts := &fakeAccounts{}
	router, _ := newCursorRouter(t, products, accounts)

	recorder := pullCursor(t, router, "", "")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("estado = %d, quería 401; cuerpo %s", recorder.Code, recorder.Body.String())
	}
	if len(products.calls) != 0 || len(accounts.calls) != 0 {
		t.Errorf("una petición sin token leyó las tablas: %v / %v", products.calls, accounts.calls)
	}
	if strings.Contains(recorder.Body.String(), "cursor ") {
		t.Errorf("el error no debe filtrar el contrato interno: %s", recorder.Body.String())
	}
}
