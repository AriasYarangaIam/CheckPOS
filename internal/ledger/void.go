package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// La anulación (docs/BACKEND.md §4.10): un evento COMPENSATORIO, no un borrado. Se anexa
// una fila de `sales` con `kind = 'void'` que espeja el `payment_method` y el `total_cents`
// POSITIVOS del original —la negación vive en la fórmula F8, nunca en el total almacenado—,
// enlazada por la clave compuesta `(original_sold_on, original_sale_id)`, y el stock se
// mueve en reversa con deltas nuevos y positivos `source = 'void'`.
//
// La fila original y sus líneas NUNCA se editan ni se borran: son append-only por concesión
// y por guarda de trigger (docs/DATABASE.md §2.12, MD-2), y la única forma de compensarlas es
// agregar historia (ARCHITECTURE.md:132).
//
// La anulación de una anulación se rechaza al crearla (F39): la aritmética de dos niveles de
// F8 restaría dos veces.

// ErrUnknownSale es el resultado de una anulación que apunta a una venta que el RLS no deja
// ver: puede no existir o pertenecer a otro tenant, y el 404 no distingue las dos causas
// (docs/BACKEND.md §4.10).
var ErrUnknownSale = errors.New("the original sale is not in the tenant for that business date")

// ErrVoidOfVoid es el rechazo de F39: el objetivo de una anulación tiene que ser una venta.
var ErrVoidOfVoid = errors.New("a void can only target a sale, not a void")

// VoidInput es la anulación ya parseada del cuerpo y de la ruta. Los dos ids son DISTINTOS
// por contrato: `OriginalID` viene de `{id}` en la ruta y `ID` del cuerpo es la fila nueva.
type VoidInput struct {
	// ID es el id de la fila compensatoria que generó el cliente (UUIDv7).
	ID uuid.UUID
	// OriginalID es la venta que se anula, tomada del `{id}` de la ruta.
	OriginalID uuid.UUID
	// SoldOn es la fecha de negocio del ORIGINAL. Es obligatoria porque la PK de `sales` es
	// `(sold_on, sale_id)` y la partición se elige por esa fecha: sin ella, la búsqueda
	// recorrería todas las particiones (decisión 1 del documento del sprint).
	SoldOn time.Time
}

// SaleVoidStore es el camino de datos de la anulación. Es una interfaz aparte de SaleStore
// para que los dos handlers se prueben por separado; el store de Postgres implementa las dos.
//
// Devuelve el cuerpo JSON EXACTO del acuse: el sobre de §4.9 con `kind = "void"`, el vínculo
// al original y los niveles de stock refrescados (decisión 2 del documento del sprint). El
// []byte es lo que el claim reenvía byte a byte en la repetición (§7.1 Branch A).
type SaleVoidStore interface {
	VoidSale(ctx context.Context, tenantID, userID uuid.UUID, void VoidInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error)
}

// readOriginalSQL lee la fila original por su PK compuesta. El WHERE nombra el tenant porque
// es la barrera REAL del aislamiento: la fila de otro tenant no vuelve, y la respuesta es la
// misma que la de una fila inexistente (§4.1).
const readOriginalSQL = `
SELECT kind, payment_method, total_cents
FROM sales
WHERE tenant_id = $1 AND sold_on = $2 AND sale_id = $3`

// readOriginalLinesSQL lee las cantidades congeladas de la venta original: es lo que el
// delta de reversa necesita para devolver EXACTAMENTE lo que la venta restó. `qty` viaja
// como TEXTO para que NUMERIC(10,3) no pase por float (§2.4).
const readOriginalLinesSQL = `
SELECT product_id, qty::text
FROM sale_lines
WHERE tenant_id = $1 AND sold_on = $2 AND sale_id = $3
ORDER BY line_no`

// insertVoidSQL escribe la fila compensatoria. `sold_on` es la fecha de negocio del ORIGINAL
// —y por eso `original_sold_on` repite el mismo parámetro—: F8 resta la anulación del cierre
// del día en que ocurrió la venta, y la FK compuesta del stock apunta a esta fila.
//
// created_at y received_at reciben el MISMO instante, la única lectura del reloj del
// llamador (docs/DATABASE.md §2.5): el cuerpo de §4.10 no trae una marca del cliente.
const insertVoidSQL = `
INSERT INTO sales (sold_on, sale_id, tenant_id, sold_by_user_id, kind, payment_method, total_cents,
	original_sold_on, original_sale_id, created_at, received_at)
VALUES ($1, $2, $3, $4, 'void', $5, $6, $1, $7, $8, $8)`

// VoidSale anula una venta: UNA TRANSACCIÓN. Reclama la clave de idempotencia, lee el
// original bajo RLS (404 si no está, 422 si ya es una anulación), espeja su método y su
// total positivo en una fila nueva, revierte el stock con deltas `source = 'void'`, y
// completa el claim con el acuse como ÚLTIMO paso. Cualquier falla revierte todo, claim
// incluido (MD-4).
func (s *PostgresSaleStore) VoidSale(ctx context.Context, tenantID, userID uuid.UUID, void VoidInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	if err := guardVoidInputs(tenantID, userID, void, receivedAt, claim); err != nil {
		return nil, err
	}

	var ack []byte
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		// Branch B ANTES de la lectura: la fila de `idempotency_keys` ES el claim y se gana
		// INSERTANDO sobre la UNIQUE (tenant_id, key) — jamás SELECT-then-INSERT (§7.1).
		claimed, err := synckey.Claim(ctx, tx, tenantID, claim.Key, claim.PayloadHash, receivedAt)
		if err != nil {
			return err
		}
		if claimed.Replay != nil {
			// Branch A, repetición verdadera: el snapshot ALMACENADO tal cual (§7.1).
			ack = claimed.Replay
			return nil
		}

		var (
			kind          string
			paymentMethod string
			totalCents    int64
		)
		err = tx.QueryRow(ctx, readOriginalSQL, tenantID, void.SoldOn, void.OriginalID).
			Scan(&kind, &paymentMethod, &totalCents)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Fuera del tenant y inexistente son la misma respuesta: el RLS devolvió cero
				// filas y el 404 no distingue los dos casos (§4.1).
				return ErrUnknownSale
			}
			return fmt.Errorf("read the original sale %s: %w", void.OriginalID, err)
		}
		if kind != "sale" {
			// F39: el objetivo de una anulación tiene que ser una venta.
			return ErrVoidOfVoid
		}

		// La anulación ESPEJA los valores positivos del original: el método, el total, y
		// nada se recomputa (§2.3, MD-7).
		if _, err := tx.Exec(ctx, insertVoidSQL, void.SoldOn, void.ID, tenantID, userID,
			paymentMethod, totalCents, void.OriginalID, receivedAt); err != nil {
			return fmt.Errorf("insert the void %s of the sale %s: %w", void.ID, void.OriginalID, err)
		}

		lines, err := readOriginalLines(ctx, tx, tenantID, void.SoldOn, void.OriginalID)
		if err != nil {
			return err
		}

		// La reversa es un movimiento de Catalog sobre ESTA transacción, con el par
		// (sold_on del original, sale_id de la ANULACIÓN) que la FK compuesta exige.
		movement := catalog.SaleStockMovement{
			SaleID:     void.ID,
			SoldOn:     void.SoldOn,
			ReceivedAt: receivedAt,
			CloseAt:    receivedAt,
			Lines:      lines,
		}
		if err := catalog.WriteVoidStockDeltas(ctx, tx, tenantID, movement); err != nil {
			return fmt.Errorf("reverse the stock of the sale %s: %w", void.OriginalID, err)
		}

		// El acuse lleva el nivel de stock refrescado de los productos del original: la
		// misma lectura RLS del cierre de venta, re-corrida después de proyectar.
		productIDs := make([]uuid.UUID, 0, len(lines))
		for _, line := range lines {
			productIDs = append(productIDs, line.ProductID)
		}
		stocks, err := visibleProductStocks(ctx, tx, productIDs)
		if err != nil {
			return err
		}

		// Complete es el ÚLTIMO paso (§7.1 Branch B): el snapshot viaja con el estado
		// 'completed' y el COMMIT los consolida juntos.
		body, err := voidAcknowledgeJSON(tenantID, userID, void, paymentMethod, totalCents, receivedAt, stocks)
		if err != nil {
			return err
		}
		if err := synckey.Complete(ctx, tx, claimed.ID, body); err != nil {
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

// readOriginalLines lee las líneas del original ya normalizadas como deltas de stock.
func readOriginalLines(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, soldOn time.Time, saleID uuid.UUID) ([]catalog.SaleStockDelta, error) {
	rows, err := tx.Query(ctx, readOriginalLinesSQL, tenantID, soldOn, saleID)
	if err != nil {
		return nil, fmt.Errorf("read the lines of the sale %s: %w", saleID, err)
	}
	defer rows.Close()

	var lines []catalog.SaleStockDelta
	for rows.Next() {
		var (
			productID uuid.UUID
			qty       string
		)
		if err := rows.Scan(&productID, &qty); err != nil {
			return nil, fmt.Errorf("read the lines of the sale %s: %w", saleID, err)
		}
		lines = append(lines, catalog.SaleStockDelta{ProductID: productID, Qty: qty})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate the lines of the sale %s: %w", saleID, err)
	}
	return lines, nil
}

// voidAcknowledgeJSON arma el acuse de la anulación: el MISMO sobre de §4.9, con
// `kind = "void"` y el vínculo al original, para que el claim almacene un snapshot de la
// misma forma y el cliente re-renderice el stock como en el cierre (decisión 2).
//
// La anulación no lleva líneas: `sale_lines` es el log congelado de la venta, y agregar las
// líneas espejadas haría que la agregación por producto del catálogo contara dos veces. El
// acuse viaja con `lines: []`.
func voidAcknowledgeJSON(tenantID, userID uuid.UUID, void VoidInput, paymentMethod string, totalCents int64, receivedAt time.Time, stocks map[string]string) ([]byte, error) {
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

	body, err := json.Marshal(saleAck{
		Sale: saleAckSale{
			SaleID:         void.ID.String(),
			SoldOn:         void.SoldOn.Format(dayFormat),
			TenantID:       tenantID.String(),
			SoldByUserID:   userID.String(),
			Kind:           "void",
			PaymentMethod:  paymentMethod,
			TotalCents:     totalCents,
			CreatedAt:      rfcTime(receivedAt),
			ReceivedAt:     rfcTime(receivedAt),
			OriginalSaleID: void.OriginalID.String(),
			OriginalSoldOn: void.SoldOn.Format(dayFormat),
			Lines:          []saleAckLine{},
		},
		Products: products,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal the void acknowledgement: %w", err)
	}
	return body, nil
}

// guardVoidInputs valida las precondiciones de escritura ANTES de abrir la transacción: son
// defectos de programación del llamador. El handler ya validó el cuerpo, la ruta y el
// encabezado del claim; esto cierra el tipo.
func guardVoidInputs(tenantID, userID uuid.UUID, void VoidInput, receivedAt time.Time, claim synckey.ClaimInput) error {
	switch {
	case tenantID == uuid.Nil:
		return errors.New("the void store needs the tenant id of the token")
	case userID == uuid.Nil:
		return errors.New("the void store needs the user id of the token")
	case void.ID == uuid.Nil:
		return errors.New("the void needs the client void id")
	case void.OriginalID == uuid.Nil:
		return errors.New("the void needs the original sale id")
	case void.ID == void.OriginalID:
		return errors.New("the void id and the original sale id must differ")
	case void.SoldOn.IsZero():
		return errors.New("the void needs the original business date")
	case receivedAt.IsZero():
		return errors.New("the void needs the server receive time")
	case claim.Key == "":
		return errors.New("the void needs the idempotency key of the client")
	case claim.PayloadHash == "":
		return errors.New("the void needs the payload hash of the claim")
	}
	return nil
}
