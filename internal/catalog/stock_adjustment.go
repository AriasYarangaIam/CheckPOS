package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// El registro de merma (W8-BE-2; docs/BACKEND.md §4.14; docs/DATABASE.md §4
// `stock_adjustments`): un delta FIRMADO negativo sobre el log append-only del stock, con
// `source = 'manual'` y uno de los TRES motivos exactos del contrato.
//
// Tres reglas mandan sobre este archivo:
//
//   - **El stock converge por deltas firmados, nunca por LWW.** El ajuste anexa una fila a
//     `stock_adjustments` y proyecta `products.stock` de forma ADITIVA; `products.updated_at`
//     no se toca, porque esa marca es del PATCH de catálogo y de nadie más (MD-11).
//   - **El stock negativo está permitido.** Oversell offline aceptado (OI-10): el log registra
//     la realidad, no la corrige.
//   - **La superficie no puede forjar deltas de venta ni de anulación.** El `source` que escribe
//     este camino es `manual` por construcción: los otros dos los escribe el ledger dentro de la
//     transacción de la venta, y `source` NO viaja en la entrada del store.

// Los tres motivos EXACTOS de una merma manual (docs/DESIGN.md:217; S13). Son la lista cerrada:
// el borde rechaza cualquier otro texto, con la tilde, las mayúsculas y los espacios del
// contrato. `Rotura en transporte` y `Daño en vitrina` llevan los suyos.
const (
	ReasonExpiry    = "Vencimiento"
	ReasonTransport = "Rotura en transporte"
	ReasonShowcase  = "Daño en vitrina"

	// SourceManual es el único `source` que esta superficie escribe.
	SourceManual = "manual"
)

// StockAdjustmentReasons es la lista cerrada, en el orden del contrato.
var StockAdjustmentReasons = []string{ReasonExpiry, ReasonTransport, ReasonShowcase}

// IsStockAdjustmentReason informa si el texto es uno de los tres motivos exactos.
func IsStockAdjustmentReason(reason string) bool {
	for _, known := range StockAdjustmentReasons {
		if reason == known {
			return true
		}
	}
	return false
}

// StockAdjustmentInput es la merma ya parseada del cuerpo. `QtyDelta` viaja como TEXTO exacto
// —«-2.500»— para que NUMERIC(10,3) no pase por float (§2.4).
type StockAdjustmentInput struct {
	ID        uuid.UUID
	ProductID uuid.UUID
	// QtyDelta es el delta firmado en TEXTO: negativo en una merma, y distinto de cero.
	QtyDelta string
	// Source es SIEMPRE `manual`: no lo elige el cliente.
	Source string
	// Reason es uno de los tres motivos exactos.
	Reason string
	// SoldOn y SaleID existen para que el tipo pueda representar un delta del ledger, pero la
	// superficie de la merma los rechaza: un ajuste manual no tiene venta.
	SoldOn *time.Time
	SaleID *uuid.UUID
}

// StockAdjustmentStore es el único camino de datos de la merma (patrón de
// internal/catalog/product.go). Devuelve el cuerpo JSON EXACTO del acuse, que el claim almacena
// para la repetición byte a byte (§7.1 Branch A).
type StockAdjustmentStore interface {
	Append(ctx context.Context, tenantID uuid.UUID, adjustment StockAdjustmentInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error)
}

// stockAdjustmentBody es el ajuste del acuse, con los valores congelados de la fila.
type stockAdjustmentBody struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenant_id"`
	ProductID  string  `json:"product_id"`
	QtyDelta   string  `json:"qty_delta"`
	Source     string  `json:"source"`
	Reason     string  `json:"reason"`
	CreatedAt  string  `json:"created_at"`
	ReceivedAt string  `json:"received_at"`
	SoldOn     *string `json:"sold_on"`
	SaleID     *string `json:"sale_id"`
}

// stockAdjustmentProduct es el refresco del nivel de stock del producto, la misma pieza que el
// acuse de la venta publica (§4.9): el cliente re-renderiza con lo que la base confirmó.
type stockAdjustmentProduct struct {
	ProductID string `json:"product_id"`
	Stock     string `json:"stock"`
}

// stockAdjustmentAck es el cuerpo completo de la respuesta de §4.14.
type stockAdjustmentAck struct {
	Adjustment stockAdjustmentBody    `json:"adjustment"`
	Product    stockAdjustmentProduct `json:"product"`
}

// insertStockAdjustmentSQL anexa el delta. El signo viaja en el TEXTO del cuerpo: esta sentencia
// no lo reinterpreta ni lo recalcula (§2.4).
const insertStockAdjustmentSQL = `
INSERT INTO stock_adjustments (id, tenant_id, product_id, qty_delta, source, reason, sold_on, sale_id, created_at, received_at)
VALUES ($1, $2, $3, $4::numeric, $5, $6, $7, $8, $9, $9)`

// addStockDeltaSQL proyecta el delta de forma ADITIVA sobre `products.stock`. `updated_at` no
// aparece: la edición de esa marca es del PATCH de catálogo y de nadie más (MD-11). Es una
// sentencia PROPIA y no la de la venta —aquella suma por (sold_on, sale_id)— porque acá el delta
// es de una fila sola.
const addStockDeltaSQL = `UPDATE products SET stock = stock + $2::numeric WHERE id = $1`

// readStockSQL lee el nivel proyectado, como TEXTO exacto del NUMERIC (§2.4).
const readStockSQL = `SELECT stock::text FROM products WHERE id = $1`

// PostgresStockAdjustmentStore escribe la merma sobre el pool de aplicación envuelto en el helper
// de transacción de tenant.
type PostgresStockAdjustmentStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresStockAdjustmentStore construye el store sobre el pool de `pos_app`.
func NewPostgresStockAdjustmentStore(tenants *postgres.TenantPool) *PostgresStockAdjustmentStore {
	return &PostgresStockAdjustmentStore{tenants: tenants}
}

// Append registra la merma: UNA TRANSACCIÓN. El orden es el de §7.1 y §4.14 — reclamar la clave,
// validar el producto por RLS, anexar el delta, proyectar el stock, completar el claim con el
// acuse —. Cualquier fallo revierte todo, claim incluido (MD-4).
func (s *PostgresStockAdjustmentStore) Append(ctx context.Context, tenantID uuid.UUID, adjustment StockAdjustmentInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	if err := guardStockAdjustment(tenantID, adjustment, receivedAt, claim); err != nil {
		return nil, err
	}

	var ack []byte
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		claimed, err := synckey.Claim(ctx, tx, tenantID, claim.Key, claim.PayloadHash, receivedAt)
		if err != nil {
			return err
		}
		if claimed.Replay != nil {
			// Branch A: la respuesta es el snapshot ALMACENADO, sin correr ningún camino de
			// negocio (§7.1).
			ack = claimed.Replay
			return nil
		}

		// La referencia por cuerpo al catálogo del tenant: la FK de `stock_adjustments` corre
		// FUERA del alcance del RLS y aprobaría el producto ajeno como aprueba el propio. Esta
		// lectura es la barrera de aislamiento REAL (§4.1).
		var before string
		err = tx.QueryRow(ctx, readStockSQL, adjustment.ProductID).Scan(&before)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownProduct
		}
		if err != nil {
			return fmt.Errorf("read the product %s of the stock adjustment: %w", adjustment.ProductID, err)
		}

		if _, err := tx.Exec(ctx, insertStockAdjustmentSQL, adjustment.ID, tenantID, adjustment.ProductID,
			adjustment.QtyDelta, adjustment.Source, adjustment.Reason, adjustment.SoldOn, adjustment.SaleID, receivedAt); err != nil {
			return fmt.Errorf("insert the stock adjustment %s: %w", adjustment.ID, err)
		}

		if _, err := tx.Exec(ctx, addStockDeltaSQL, adjustment.ProductID, adjustment.QtyDelta); err != nil {
			return fmt.Errorf("project the stock of the product %s: %w", adjustment.ProductID, err)
		}

		var after string
		if err := tx.QueryRow(ctx, readStockSQL, adjustment.ProductID).Scan(&after); err != nil {
			return fmt.Errorf("re-read the stock of the product %s: %w", adjustment.ProductID, err)
		}

		body, err := stockAdjustmentAcknowledgeJSON(tenantID, adjustment, receivedAt, after)
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

// guardStockAdjustment cierra el tipo antes de abrir la transacción: son defectos de
// programación del llamador, no peticiones erróneas (patrón de catalog.guardWrite).
func guardStockAdjustment(tenantID uuid.UUID, adjustment StockAdjustmentInput, receivedAt time.Time, claim synckey.ClaimInput) error {
	switch {
	case tenantID == uuid.Nil:
		return errors.New("the stock adjustment needs the tenant id of the token")
	case adjustment.ID == uuid.Nil:
		return errors.New("the stock adjustment needs the client adjustment id")
	case adjustment.ProductID == uuid.Nil:
		return errors.New("the stock adjustment needs the product id")
	case strings.TrimSpace(adjustment.QtyDelta) == "":
		return errors.New("the stock adjustment needs a signed quantity")
	case !strings.HasPrefix(adjustment.QtyDelta, "-"):
		return errors.New("a manual stock adjustment is a negative delta: a loss reduces the stock")
	case adjustment.Source != SourceManual:
		return errors.New("the stock adjustment surface only writes source manual")
	case !IsStockAdjustmentReason(adjustment.Reason):
		return errors.New("the stock adjustment needs one of the three exact reasons")
	case adjustment.SoldOn != nil || adjustment.SaleID != nil:
		return errors.New("a manual stock adjustment carries no sale linkage")
	case receivedAt.IsZero():
		return errors.New("the stock adjustment needs the server receive time")
	case claim.Key == "":
		return errors.New("the stock adjustment needs the idempotency key of the client")
	case claim.PayloadHash == "":
		return errors.New("the stock adjustment needs the payload hash of the claim")
	}
	return nil
}

// stockAdjustmentAcknowledgeJSON arma el acuse de §4.14 (decisión de forma del sprint 8): el
// ajuste resuelto y el nivel de stock que la base confirmó.
func stockAdjustmentAcknowledgeJSON(tenantID uuid.UUID, adjustment StockAdjustmentInput, receivedAt time.Time, stock string) ([]byte, error) {
	var soldOn, saleID *string
	if adjustment.SoldOn != nil {
		value := adjustment.SoldOn.UTC().Format("2006-01-02")
		soldOn = &value
	}
	if adjustment.SaleID != nil {
		value := adjustment.SaleID.String()
		saleID = &value
	}

	ack := stockAdjustmentAck{
		Adjustment: stockAdjustmentBody{
			ID:         adjustment.ID.String(),
			TenantID:   tenantID.String(),
			ProductID:  adjustment.ProductID.String(),
			QtyDelta:   adjustment.QtyDelta,
			Source:     adjustment.Source,
			Reason:     adjustment.Reason,
			CreatedAt:  rfc3339(receivedAt),
			ReceivedAt: rfc3339(receivedAt),
			SoldOn:     soldOn,
			SaleID:     saleID,
		},
		Product: stockAdjustmentProduct{ProductID: adjustment.ProductID.String(), Stock: stock},
	}

	body, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("marshal the stock adjustment acknowledgement: %w", err)
	}
	return body, nil
}
