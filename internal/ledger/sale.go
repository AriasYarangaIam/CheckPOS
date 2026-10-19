package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// El cierre de venta (docs/BACKEND.md §4.9): un solo INSERT transaccional en `sales` +
// `sale_lines`, con el delta de stock correspondiente en `stock_adjustments`
// (ARCHITECTURE.md:99). Todo dentro de la transacción de tenant del pool de `pos_app`,
// RLS de por medio.
//
// El dinero es del cliente: los `total_cents` y los `line_total_cents` del cuerpo se
// almacenan VERBATIM — el servidor nunca recomputa (docs/DATABASE.md §2.3, MD-7). La
// cantidad `qty` viaja como TEXTO para que NUMERIC(10,3) no pase por float (§2.4).

// ErrUnknownProduct es el resultado de una venta que referencia un producto que el RLS no
// deja ver: puede no existir o pertenecer a otro tenant, y el 422 no distingue las dos
// causas (la misma regla «fuera del tenant no existe en nada distinto» de §4.1, pero para
// una referencia por cuerpo).
var ErrUnknownProduct = errors.New("the sale references a product that is not in the tenant")

// SaleLineInput es una línea de venta ya parseada del cuerpo, con los valores congelados
// del cliente intactos.
type SaleLineInput struct {
	No int
	// ProductID es la referencia por cuerpo al catálogo del tenant: el store la valida por
	// RLS antes de escribir, porque la FK de `sale_lines` NO aísla sola — la comprobación
	// de claves ajenas corre fuera del alcance del RLS.
	ProductID  uuid.UUID
	Name       string
	PriceCents int64
	UoM        string
	// Qty es la cantidad vendida en TEXTO exacto (§2.4) — "2.750", no un float.
	Qty string
	// TotalCents es el line_total_cents congelado que envió el cliente, ya redondeado
	// half-up del lado del cliente (MD-7). El servidor lo guarda tal cual.
	TotalCents int64
}

// SaleInput es el cierre de venta ya parseado: el §4.9 de BACKEND.md, sin el tenant ni el
// usuario — esos viajan aparte y vienen SOLO del token (ARCHITECTURE.md:23).
type SaleInput struct {
	ID uuid.UUID
	// SoldOn es la fecha de negocio del cliente al cerrar: la clave de partición del
	// ledger, y el retraso de sync NUNCA la mueve de día ni de cierre (docs/DATABASE.md
	// §4 `sales`).
	SoldOn        time.Time
	PaymentMethod string
	// TotalCents es el total congelado del cuerpo (§2.3).
	TotalCents int64
	Lines      []SaleLineInput
}

// SaleStore es el único camino de datos del cierre de venta (patrón de
// internal/catalog/product.go): una interfaz, para que el handler se pruebe sin base.
//
// Devuelve el cuerpo JSON EXACTO del acuse de §4.9: la venta cerrada y los niveles de
// stock refrescados que el cliente vuelve a renderizar (ARCHITECTURE.md:100). El []byte es
// la pieza que el claim de idempotencia (W4-BE-2) va a reenviar byte a byte en la
// repetición (docs/DATABASE.md §7.1 Branch A).
type SaleStore interface {
	CloseSale(ctx context.Context, tenantID, userID uuid.UUID, sale SaleInput, receivedAt time.Time) ([]byte, error)
}

// saleAckLine es una línea del acuse de §4.9, con los valores congelados que la fila
// guardó — el ticket se rinde desde lo persistido, ninguna vez desde el catálogo vivo
// (OI-11).
type saleAckLine struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Name           string `json:"name"`
	PriceCents     int64  `json:"price_cents"`
	UoM            string `json:"uom"`
	Qty            string `json:"qty"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// saleAckSale es el encabezado congelado de la venta.
type saleAckSale struct {
	SaleID        string        `json:"sale_id"`
	SoldOn        string        `json:"sold_on"`
	TenantID      string        `json:"tenant_id"`
	SoldByUserID  string        `json:"sold_by_user_id"`
	Kind          string        `json:"kind"`
	PaymentMethod string        `json:"payment_method"`
	TotalCents    int64         `json:"total_cents"`
	CreatedAt     string        `json:"created_at"`
	ReceivedAt    string        `json:"received_at"`
	Lines         []saleAckLine `json:"lines"`
}

// saleAckProduct es el refresco de nivel de stock proyectado de UN producto después del
// cierre (ARCHITECTURE.md:100). El stock sale como TEXTO del NUMERIC — "4.750" —, exacto
// (§2.4).
type saleAckProduct struct {
	ProductID string `json:"product_id"`
	Stock     string `json:"stock"`
}

// saleAck es el cuerpo completo de la respuesta de §4.9.
type saleAck struct {
	Sale     saleAckSale      `json:"sale"`
	Products []saleAckProduct `json:"products"`
}

// visibleProductsSQL lee los productos pedidos dentro del contexto de tenant: el RLS deja
// caer todas las filas que no son del token, así que lo que vuelve ES la barrera de
// aislamiento (§5.2, §5.7). El stock viaja como texto del NUMERIC (§2.4).
const visibleProductsSQL = `SELECT id::text, stock::text FROM products WHERE id = ANY($1::uuid[])`

// insertSaleSQL escribe el encabezado, con kind 'sale' por construcción del endpoint.
//
// created_at y received_at reciben el MISMO instante: la marca del cierre la pone el
// servidor — el cuerpo de §4.9 no trae created_at, solo la fecha de negocio sold_on — y
// una sola lectura del reloj deja el par negocio/auditoría coherente.
const insertSaleSQL = `
INSERT INTO sales (sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method, total_cents, created_at, received_at)
VALUES ($1, $2, $3, $4, 'sale', $5, $6, $7, $7)`

// insertSaleLineSQL escribe una línea congelada: hija de `sales`, comparte su transacción,
// su sold_on y no lleva received_at propio (MD-6).
const insertSaleLineSQL = `
INSERT INTO sale_lines (sale_id, line_no, tenant_id, sold_on, product_id, name, price_cents, uom, qty, line_total_cents, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

// PostgresSaleStore cierra ventas sobre el pool de aplicación envuelto en el helper de
// transacción de tenant.
type PostgresSaleStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresSaleStore construye el store sobre el pool de `pos_app`.
func NewPostgresSaleStore(tenants *postgres.TenantPool) *PostgresSaleStore {
	return &PostgresSaleStore{tenants: tenants}
}

// CloseSale cierra la venta: UNA TRANSACCIÓN. El orden interno es el que el §4.9 exige —
// validar el producto contra el catálogo por RLS, apendizar el encabezado y sus líneas
// congeladas, escribir el delta de stock por línea y proyectar el stock aditivo— antes de
// armar el acuse con los niveles refrescados. Cualquier paso que falla revierte TODO:
// el claim de idempotencia que W4-BE-2 acopla a este cierre comparte la misma
// transacción, así que claim y venta nacen o mueren juntos (MD-4).
func (s *PostgresSaleStore) CloseSale(ctx context.Context, tenantID, userID uuid.UUID, sale SaleInput, receivedAt time.Time) ([]byte, error) {
	if err := guardSaleInputs(tenantID, userID, sale, receivedAt); err != nil {
		return nil, err
	}

	productIDs := make([]uuid.UUID, 0, len(sale.Lines))
	for _, line := range sale.Lines {
		productIDs = append(productIDs, line.ProductID)
	}

	var ack []byte
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		// El producto tiene que ser visible por RLS: la FK de `sale_lines` corre FUERA del
		// alcance del RLS — aprobaría la fila de otro tenant como aprueba la propia. Esta
		// lectura es la barrera de aislamiento REAL de la referencia por cuerpo (§4.1).
		seen, err := visibleProductStocks(ctx, tx, productIDs)
		if err != nil {
			return err
		}
		for _, line := range sale.Lines {
			if _, ok := seen[line.ProductID.String()]; !ok {
				return ErrUnknownProduct
			}
		}

		if _, err := tx.Exec(ctx, insertSaleSQL, sale.SoldOn, sale.ID, tenantID, userID,
			sale.PaymentMethod, sale.TotalCents, receivedAt); err != nil {
			return fmt.Errorf("insert the sale %s: %w", sale.ID, err)
		}

		for _, line := range sale.Lines {
			if _, err := tx.Exec(ctx, insertSaleLineSQL, sale.ID, line.No, tenantID, sale.SoldOn,
				line.ProductID, line.Name, line.PriceCents, line.UoM, line.Qty, line.TotalCents,
				receivedAt); err != nil {
				return fmt.Errorf("insert the sale line %d of %s: %w", line.No, sale.ID, err)
			}
		}

		// El delta y la proyección son SQL del módulo Catalog (ARCHITECTURE.md:31), ejecutado
		// sobre ESTA transacción para que el commit sea todo-o-nada (docs/BACKEND.md §4.9).
		movement := catalog.SaleStockMovement{
			SaleID:     sale.ID,
			SoldOn:     sale.SoldOn,
			ReceivedAt: receivedAt,
			CloseAt:    receivedAt,
		}
		for _, line := range sale.Lines {
			movement.Lines = append(movement.Lines, catalog.SaleStockDelta{ProductID: line.ProductID, Qty: line.Qty})
		}
		if err := catalog.WriteSaleStockDeltas(ctx, tx, tenantID, movement); err != nil {
			return fmt.Errorf("move the stock of the sale %s: %w", sale.ID, err)
		}

		// El acuse lleva el nivel de stock final por producto (§4.9, ARCHITECTURE.md:100):
		// la misma lectura RLS que validó la existencia, re-corrida después de proyectar.
		stocks, err := visibleProductStocks(ctx, tx, productIDs)
		if err != nil {
			return err
		}

		body, err := saleAcknowledgeJSON(tenantID, userID, sale, receivedAt, stocks)
		if err != nil {
			return err
		}
		ack = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ack, nil
}

// guardSaleInputs valida las precondiciones de escritura ANTES de abrir la primera
// transacción: son defectos de programación del llamador, no peticiones erróneas (patrón
// de catalog.guardWrite). El handler ya validó el cuerpo; esto cierra el tipo.
func guardSaleInputs(tenantID, userID uuid.UUID, sale SaleInput, receivedAt time.Time) error {
	switch {
	case tenantID == uuid.Nil:
		return errors.New("the sale store needs the tenant id of the token")
	case userID == uuid.Nil:
		return errors.New("the sale store needs the user id of the token")
	case sale.ID == uuid.Nil:
		return errors.New("the sale needs the client sale id")
	case sale.SoldOn.IsZero():
		return errors.New("the sale needs the client business date")
	case len(sale.Lines) == 0:
		return errors.New("the sale needs at least one line")
	case receivedAt.IsZero():
		return errors.New("the sale close needs the server close time")
	}
	return nil
}

// visibleProductStocks lee el stock proyectado de los productos pedidos por el CAMINO DE
// PRODUCCIÓN: dentro del RLS. Las filas que vuelven son SOLO las del token.
func visibleProductStocks(ctx context.Context, tx postgres.TenantTx, productIDs []uuid.UUID) (map[string]string, error) {
	rows, err := tx.Query(ctx, visibleProductsSQL, productIDs)
	if err != nil {
		return nil, fmt.Errorf("read the visible products: %w", err)
	}
	defer rows.Close()

	stocks := map[string]string{}
	for rows.Next() {
		var id, stock string
		if err := rows.Scan(&id, &stock); err != nil {
			return nil, fmt.Errorf("read the visible products: %w", err)
		}
		stocks[id] = stock
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate the visible products: %w", err)
	}
	return stocks, nil
}

// saleAcknowledgeJSON arma el acuse de §4.9: la forma exacta que el claim de idempotencia
// (W4-BE-2) almacenará en response_snapshot para que la repetición devuelva esta MISMA
// respuesta (docs/DATABASE.md §7.1 Branch A).
func saleAcknowledgeJSON(tenantID, userID uuid.UUID, sale SaleInput, closeAt time.Time, stocks map[string]string) ([]byte, error) {
	lines := make([]saleAckLine, 0, len(sale.Lines))
	for _, line := range sale.Lines {
		lines = append(lines, saleAckLine{
			LineNo:         line.No,
			ProductID:      line.ProductID.String(),
			Name:           line.Name,
			PriceCents:     line.PriceCents,
			UoM:            line.UoM,
			Qty:            line.Qty,
			LineTotalCents: line.TotalCents,
		})
	}

	// Un solo orden en los productos del acuse, por id: la forma determinista es la que la
	// repetición idempotente tiene que reproducir.
	ids := make([]string, 0, len(stocks))
	for id := range stocks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	products := make([]saleAckProduct, 0, len(ids))
	for _, id := range ids {
		products = append(products, saleAckProduct{ProductID: id, Stock: stocks[id]})
	}

	ack := saleAck{
		Sale: saleAckSale{
			SaleID:        sale.ID.String(),
			SoldOn:        sale.SoldOn.Format("2006-01-02"),
			TenantID:      tenantID.String(),
			SoldByUserID:  userID.String(),
			Kind:          "sale",
			PaymentMethod: sale.PaymentMethod,
			TotalCents:    sale.TotalCents,
			CreatedAt:     rfcTime(closeAt),
			ReceivedAt:    rfcTime(closeAt),
			Lines:         lines,
		},
		Products: products,
	}

	body, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("marshal the sale acknowledgement: %w", err)
	}
	return body, nil
}

// rfcTime es el formato RFC3339 en UTC del contrato para todo instante de superficie
// (docs/BACKEND.md §4.3, igual que internal/catalog).
func rfcTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}
