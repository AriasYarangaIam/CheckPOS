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
//     obliga (MD-9); la anulación es la MISMA forma con el signo opuesto y source 'void',
//     como fila nueva, nunca como edición de la existente;
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

// insertSaleDeltaSQL escribe el delta de una línea. La dirección vive en la SQL —el signo
// del factor— y no en Go: venta ⇒ qty_delta negativo, anulación ⇒ positivo (§4). El factor
// se pasa como argumento para que las DOS direcciones compartan una sola sentencia y un
// solo lugar donde la regla está escrita.
const insertSaleDeltaSQL = `
INSERT INTO stock_adjustments (id, tenant_id, product_id, qty_delta, source, sold_on, sale_id, created_at, received_at)
VALUES ($1, $2, $3, ($4::numeric) * $5, $6, $7, $8, $9, $10)`

// Las dos direcciones del movimiento de venta: la venta resta la cantidad vendida y la
// anulación la devuelve. El `source` viaja con ellas porque `stock_adjustments` lo usa para
// distinguir el origen de cada delta (§4).
const (
	saleStockSource = "sale"
	voidStockSource = "void"

	// saleDirection invierte la cantidad vendida; voidDirection la repone.
	saleDirection = -1
	voidDirection = 1
)

// projectStockSQL aplica la proyección aditiva: SUM(qty_delta) por producto —agregada por
// PostgreSQL sobre las filas que ESTA MISMA transacción acaba de insertar, en NUMERIC
// exacto— y no una aritmética paralela en Go. El UPDATE no menciona updated_at: una
// proyección de cantidades no es una mutación de catálogo (§7.3, MD-11).
//
// Es la ÚNICA proyección del flujo de venta: la comparten el cierre y la anulación, que solo
// se diferencian en el signo y el source de los deltas que agrega.
const projectStockSQL = `
UPDATE products p
SET stock = p.stock + d.total_delta
FROM (SELECT product_id, SUM(qty_delta) AS total_delta
	  FROM stock_adjustments
	  WHERE tenant_id = $1 AND sold_on = $2 AND sale_id = $3
	  GROUP BY product_id) d
WHERE p.id = d.product_id AND p.tenant_id = $1`

// WriteSaleStockDeltas ejecuta el movimiento de stock de una venta sobre la transacción
// del llamador: una fila de delta por línea, negativa, y UNA proyección aditiva colectiva
// sobre products.stock. Es el único consumidor del sale-flow de stock.
func WriteSaleStockDeltas(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, movement SaleStockMovement) error {
	return writeStockDeltas(ctx, tx, tenantID, movement, saleStockSource, saleDirection)
}

// WriteVoidStockDeltas ejecuta la REVERSA del movimiento de una venta: una fila de delta
// nueva y POSITIVA por línea, con source 'void', y la misma proyección aditiva. La fila
// original de `stock_adjustments` no se toca: el stock se mueve con una fila nueva, igual
// que la venta original se conserva en `sales` (docs/BACKEND.md §4.10).
//
// El par (sold_on, sale_id) del movimiento es el de la fila de ANULACIÓN —el `SaleID` es el
// de la compensatoria—, porque la FK compuesta apunta a esa fila y la proyección agrega por
// ese par (MD-9).
func WriteVoidStockDeltas(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, movement SaleStockMovement) error {
	return writeStockDeltas(ctx, tx, tenantID, movement, voidStockSource, voidDirection)
}

// writeStockDeltas es el camino único del movimiento de stock por venta: escribe un delta
// por línea con la dirección y el source pedidos, y proyecta el stock aditivamente.
func writeStockDeltas(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, movement SaleStockMovement, source string, direction int) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("write %s stock deltas for %s: the tenant id is the nil uuid", source, movement.SaleID)
	}
	if movement.SaleID == uuid.Nil {
		return fmt.Errorf("write %s stock deltas: the sale id is the nil uuid", source)
	}

	for _, line := range movement.Lines {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate the delta id: %w", err)
		}
		if _, err := tx.Exec(ctx, insertSaleDeltaSQL,
			id, tenantID, line.ProductID, line.Qty, direction, source, movement.SoldOn, movement.SaleID,
			movement.CloseAt, movement.ReceivedAt); err != nil {
			return fmt.Errorf("insert the %s stock delta of %s: %w", source, line.ProductID, err)
		}
	}

	if _, err := tx.Exec(ctx, projectStockSQL, tenantID, movement.SoldOn, movement.SaleID); err != nil {
		return fmt.Errorf("project the %s stock: %w", source, err)
	}

	return nil
}
