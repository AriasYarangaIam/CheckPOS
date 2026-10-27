// Package synccursor implementa el pull por cursor de `GET /sync/cursor` (Sprint 6,
// W6-BE-3; docs/BACKEND.md §4.8, docs/DATABASE.md §7.3).
//
// Frontera: este paquete NO es dueño de ninguna tabla. Pide las dos lecturas a los módulos
// que sí las poseen —`internal/catalog` para `products` e `internal/fiado` para
// `fiado_accounts` (ARCHITECTURE.md:31)— y se queda con lo único que es suyo: la aritmética
// del cursor y la forma de la respuesta.
//
// Las tres reglas del cursor que este paquete sostiene, y que son la razón de existir del
// endpoint:
//
//   - el cursor avanza SOLO hasta el máximo `updated_at` realmente devuelto, jamás a
//     `now()`: así una fila estampada mientras la respuesta se armaba se vuelve a traer en
//     el pull siguiente, dentro del solape de 15 minutos (§7.3, decisión D3);
//   - sin filas se repite el cursor recibido, y en el pull inicial vacío es `null`; y
//   - un cursor futuro devuelve vacío sin error: el avance es siempre seguro hacia
//     adelante.
//
// El cursor NO transporta dinero ni stock (§7.3): solo propaga ediciones de catálogo y el
// registro de deudores.
package synccursor

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/fiado"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// errNoClaims es la causa que se registra cuando la ruta corre sin el middleware de token:
// es un defecto de cableado, no una petición anónima.
var errNoClaims = errors.New("the route ran without the access token middleware: there is no tenant in the request context")

// cursorField es el nombre del parámetro del pull (docs/BACKEND.md §4.8) y el campo que
// viaja en el detalle del 422 cuando no se puede leer.
const cursorField = "cursor"

// ProductSource es la mitad de `products` del pull, servida por el módulo de catálogo.
type ProductSource interface {
	ListUpdatedSince(ctx context.Context, tenantID uuid.UUID, after *time.Time) ([]catalog.Product, error)
}

// AccountSource es la mitad de `fiado_accounts` del pull, servida por el módulo de fiado.
type AccountSource interface {
	ListUpdatedSince(ctx context.Context, tenantID uuid.UUID, after *time.Time) ([]fiado.Account, error)
}

// CursorHandler atiende `GET /sync/cursor`.
type CursorHandler struct {
	products ProductSource
	accounts AccountSource
	logger   *slog.Logger
}

// NewCursorHandler construye el handler del pull sobre las dos fuentes de lectura.
func NewCursorHandler(products ProductSource, accounts AccountSource, logger *slog.Logger) (*CursorHandler, error) {
	if products == nil {
		return nil, errors.New("the cursor pull needs a product source")
	}
	if accounts == nil {
		return nil, errors.New("the cursor pull needs a fiado account source")
	}
	if logger == nil {
		return nil, errors.New("the cursor pull needs a logger")
	}

	return &CursorHandler{products: products, accounts: accounts, logger: logger}, nil
}

// cursorResponse es el cuerpo congelado por la decisión D1 de
// odd/tasks/sprint-6-offline-sync.md.
type cursorResponse struct {
	Cursor        *string       `json:"cursor"`
	Products      []catalog.Row `json:"products"`
	FiadoAccounts []accountBody `json:"fiado_accounts"`
}

// accountBody es la fila de `fiado_accounts` del pull: las columnas de §4 sin `tenant_id`
// (el tenant ya es el del token) y sin saldo —el saldo se computa y nunca viaja (§3.2).
type accountBody struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Phone      *string `json:"phone"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	ReceivedAt string  `json:"received_at"`
}

// ServeHTTP atiende el pull: tenant y usuario salen SOLO de los claims del token, nunca del
// parámetro ni de un encabezado (docs/BACKEND.md §5.1; ARCHITECTURE.md:23).
func (h *CursorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoClaims, h.logger))
		return
	}

	cursor, err := parseCursor(r.URL.Query().Get(cursorField))
	if err != nil {
		httpx.WriteError(w, httpx.Validation("cursor must be an RFC3339 timestamp", map[string]string{"field": cursorField}))
		return
	}

	products, err := h.products.ListUpdatedSince(r.Context(), claims.TenantID, cursor)
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	accounts, err := h.accounts.ListUpdatedSince(r.Context(), claims.TenantID, cursor)
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, buildResponse(cursor, products, accounts))
}

// parseCursor lee el parámetro del pull: ausente o vacío es el pull inicial completo (nil),
// legible es el instante del cursor, e ilegible es el 422 que la superficie no deja pasar.
func parseCursor(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// buildResponse arma el cuerpo del contrato y calcula el cursor nuevo: el máximo
// `updated_at` realmente devuelto entre las dos tablas, o el eco del recibido cuando no
// volvió ninguna fila. Nunca lee el reloj del servidor.
func buildResponse(received *time.Time, products []catalog.Product, accounts []fiado.Account) cursorResponse {
	response := cursorResponse{
		Cursor:        formatCursor(received),
		Products:      make([]catalog.Row, 0, len(products)),
		FiadoAccounts: make([]accountBody, 0, len(accounts)),
	}

	newest := received
	advance := func(updatedAt time.Time) {
		if newest == nil || updatedAt.After(*newest) {
			moment := updatedAt
			newest = &moment
		}
	}

	for _, product := range products {
		response.Products = append(response.Products, catalog.RowOf(product))
		advance(product.UpdatedAt)
	}

	for _, account := range accounts {
		response.FiadoAccounts = append(response.FiadoAccounts, accountBody{
			ID:         account.ID.String(),
			Name:       account.Name,
			Phone:      account.Phone,
			CreatedAt:  formatMoment(account.CreatedAt),
			UpdatedAt:  formatMoment(account.UpdatedAt),
			ReceivedAt: formatMoment(account.ReceivedAt),
		})
		advance(account.UpdatedAt)
	}

	response.Cursor = formatCursor(newest)
	return response
}

// formatCursor traduce un instante a la marca del contrato (RFC3339 en UTC). El formato
// conserva la precisión de microsegundos de PostgreSQL para que el cursor vuelva a entrar
// como el MISMO instante en el WHERE `updated_at > cursor`.
func formatCursor(moment *time.Time) *string {
	if moment == nil {
		return nil
	}
	formatted := formatMoment(*moment)
	return &formatted
}

// formatMoment es la marca del contrato para un instante siempre presente: las columnas de
// marca de una fila leída nunca son nulas.
func formatMoment(moment time.Time) string {
	return moment.UTC().Format(time.RFC3339Nano)
}
