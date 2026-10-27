package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Este archivo entrega la mitad de `products` del pull por cursor (Sprint 6, W6-BE-3;
// docs/DATABASE.md §7.3, docs/BACKEND.md §4.8). Es una lectura de la MISMA tabla y la MISMA
// forma de fila que §4.5: el cursor no inventa un segundo contrato de producto (decisión
// D1 de odd/tasks/sprint-6-offline-sync.md).
//
// Tres diferencias con `List`, todas deliberadas y fijadas por el contrato:
//
//   - el predicado es `updated_at > cursor` y NADA más: la ventana de solape de 15 minutos
//     no se resta del WHERE (D3), porque el cursor devuelto es el máximo realmente
//     entregado y eso es lo que hace que una fila estampada durante el armado de la
//     respuesta se vuelva a traer en el pull siguiente;
//   - NO filtra `deleted_at IS NULL`: los tombstones son parte del pull (§4.8, §7.3), y por
//     eso `products_tenant_updated_at_idx` se creó sin predicado parcial;
//   - el orden es `updated_at, id`, no `name, id`: el pull es un flujo temporal, no una
//     página de catálogo.

// productsUpdatedSinceSQL es el pull por cursor. El parámetro nulo es el pull inicial
// completo: `$1::timestamptz IS NULL` deja pasar todas las filas del tenant.
const productsUpdatedSinceSQL = `SELECT ` + productColumns + ` FROM products
	WHERE ($1::timestamptz IS NULL OR updated_at > $1)
	ORDER BY updated_at, id`

// ListUpdatedSince devuelve las filas de `products` del tenant con `updated_at` posterior
// al cursor, tombstones incluidos, ordenadas por `updated_at, id`. Un cursor nulo es el
// pull inicial completo. Un cursor futuro devuelve el conjunto vacío, nunca un error: es
// la propiedad de avance seguro de §7.3.
func (s *PostgresProductStore) ListUpdatedSince(ctx context.Context, tenantID uuid.UUID, after *time.Time) ([]Product, error) {
	if s == nil || s.tenants == nil {
		return nil, errors.New("the product store has no tenant pool")
	}
	if tenantID == uuid.Nil {
		return nil, errors.New("the cursor pull needs a tenant id")
	}

	var products []Product
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		rows, err := tx.Query(ctx, productsUpdatedSinceSQL, after)
		if err != nil {
			return fmt.Errorf("pull the products after the cursor: %w", err)
		}
		defer rows.Close()

		products = make([]Product, 0)
		for rows.Next() {
			product, err := scanProductRow(rows)
			if err != nil {
				return err
			}
			products = append(products, *product)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return products, nil
}

// Row es la fila de §4.5 en su forma de contrato, exportada para que el pull por cursor
// sirva EXACTAMENTE la misma forma que `GET /products` en lugar de una copia que pueda
// derivar. Es un alias, no un tipo nuevo: hay una sola definición de la fila.
type Row = productBody

// RowOf traduce una fila leída a la forma de contrato del catálogo.
func RowOf(product Product) Row {
	return toBody(product)
}
