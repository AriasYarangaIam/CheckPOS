package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Proyección de stock de venta (docs/BACKEND.md §4.9): el SQL del módulo Catalog sobre las
// dos tablas que le son propias —`stock_adjustments` y `products`—, ejecutado SOBRE la
// transacción que el llamador trae. El cierre de venta es un solo INSERT transaccional en
// `sales` + `sale_lines` + `stock_adjustments`; el ledger orquesta la transacción, y las
// tablas del catálogo solo el módulo dueño las escribe (ARCHITECTURE.md:31).
//
// Las dos reglas no negociables de §4 `stock_adjustments` que este archivo fija:
//
//   - exactamente UNA fila de delta por línea de venta, con la sign inversionada —venta =
//     qty_delta negativo—, source 'sale', y el par (sold_on, sale_id) que la FK compuesta
//     obliga (MD-9);
//   - la proyección sobre `products.stock` es ADITIVA sobre SUM(qty_delta) hecha por
//     PostgreSQL en NUMERIC exacto, y jamás toca `products.updated_at` —el catalog PATCH
//     LWW es el único camino de esa marca (MD-11).

// SaleStockDelta es la línea vendida: la cantidad comercial ya validada por el handler del
// llamador. Este módulo no la revalida — corrige el camino de venta, que no puede viajar
// ni cero ni cantidad negativa: el guard del §4.9 vive en el handler del ledger.
type SaleStockDelta struct {
	ProductID uuid.UUID
	// Qty es la cantidad vendida en TEXTO, sin leerse como float (§2.4). El delta en
	// `stock_adjustments` es su negación: venta ⇒ qty_delta negativo.
	Qty string
}

// SaleStockMovement es el movimiento de stock de UNA venta ya reclamada y legítima.
type SaleStockMovement struct {
	SaleID uuid.UUID
	// SoldOn es la fecha de negocio del cierre —la MISMA que la fila de `sales`, porque la
	// FK compuesta (sold_on, sale_id) apunta a la partición de esa venta (MD-9).
	SoldOn time.Time
	Lines  []SaleStockDelta
	// CloseAt es la marca del encabezado de la venta: es la `created_at` del delta, porque
	// el delta nace con el cierre, no después (§2.5).
	CloseAt time.Time
	// ReceivedAt es el sello de auditoría del servidor (§2.5); nunca un comparador de sync.
	ReceivedAt time.Time
}

// insertSaleDeltaSQL escribe el delta de una línea. La negación vive en la SQL
// (-($3::numeric)) para no duplicar la regla en Go: sale ⇒ qty_delta negativo (§4).
const insertSaleDeltaSQL = `
INSERT INTO stock_adjustments (id, tenant_id, product_id, qty_delta, source, sold_on, sale_id, created_at, received_at)
VALUES ($1, $2, $3, -($4::numeric), 'sale', $5, $6, $7, $8)`

// projectStockSQL aplica la proyección aditiva: SUM(qty_delta) por producto —agregada por
// PostgreSQL sobre las filas que ESTA MISMA transacción acaba de insertar, en NUMERIC
// exacto— y no una aritmética paralela en Go. El UPDATE no menciona updated_at: una
// proyección de cantidades no es una mutación de catálogo (§7.3, MD-11).
const projectStockSQL = `
UPDATE products p
SET stock = p.stock + d.total_delta
FROM (SELECT product_id, SUM(qty_delta) AS total_delta
	  FROM stock_adjustments
	  WHERE tenant_id = $1 AND sold_on = $2 AND sale_id = $3
	  GROUP BY product_id) d
WHERE p.id = d.product_id AND p.tenant_id = $1`

// WriteSaleStockDeltas ejecuta el movimiento de stock de una venta sobre la transacción
// del llamador: una fila de delta por línea y UNA proyección aditiva colectiva sobre
// products.stock. Es el único consumidor del sale-flow de stock.
func WriteSaleStockDeltas(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, movement SaleStockMovement) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("write sale stock deltas for %s: the tenant id is the nil uuid", tenantID)
	}
	if movement.SaleID == uuid.Nil {
		return fmt.Errorf("write sale stock deltas: the sale id is the nil uuid")
	}

	for _, line := range movement.Lines {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate the delta id: %w", err)
		}
		if _, err := tx.Exec(ctx, insertSaleDeltaSQL,
			id, tenantID, line.ProductID, line.Qty, movement.SoldOn, movement.SaleID,
			movement.CloseAt, movement.ReceivedAt); err != nil {
			return fmt.Errorf("insert the stock delta of %s: %w", line.ProductID, err)
		}
	}

	if _, err := tx.Exec(ctx, projectStockSQL, tenantID, movement.SoldOn, movement.SaleID); err != nil {
		return fmt.Errorf("project the sale stock: %w", err)
	}

	return nil
}
