package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Este archivo implementa la mitad de datos de la mediación del GTIN (W3-BE-2,
// docs/DATABASE.md §5.6): la resolución de un código de barras en el catálogo global de la
// plataforma.
//
// El acceso es MEDIADO POR API y es de plataforma: las tablas `global_products` y
// `global_barcodes` viven fuera del RLS de tenant (no hay tenant_id en ellas, §3) y el rol
// `pos_app` no tiene NINGUNA concesión sobre ellas —la matriz de concesiones de §5.5 lo
// impide a propósito—. La única conexión con derecho de lectura es la de `pos_platform`,
// y lo que este mediador devuelve es de SOLO LECTURA: la actualización del catálogo
// global es un trabajo de plataforma, nunca de este módulo.
//
// Dos cosas que este mediador nunca hace, porque el contrato lo prohíbe:
//
//   - devolver más de un producto: `global_barcodes.gtin` es UNIQUE (docs/DATABASE.md §4),
//     así que un gtin resuelve a LO SUMO una fila del catálogo global;
//   - normalizar el código: el GTIN viaja como el cliente lo escribió —TEXT preserva los
//     ceros a la izquierda en GTIN-8/12/13/14 (§4)—.
//
// Este stub es el camino SIN caché: la lectura del cache (F13/F14) y su invalidación son
// de sprints posteriores. La mediarea acá corre SIEMPRE a la base; solo la estadística
// de respuesta cambia si el cache aparece encima.

// GlobalProduct es la fila del catálogo global que un tenant puede ver por mediación:
// id, nombre y atributos JSONB home #2 (docs/DATABASE.md §4 `global_products`).
type GlobalProduct struct {
	ID         uuid.UUID
	Name       string
	Attributes []byte
}

// ErrUnknownGtin es la AUSENCIA en el catálogo global: no es un error del servidor, es el
// camino de fallo esperado de la mediación, y la respuesta que corresponde es 404.
var ErrUnknownGtin = errors.New("no gtin like that exists in the global catalog")

// GtinLookup es la única forma por la que un request de negocio lee el catálogo global.
type GtinLookup interface {
	Resolve(ctx context.Context, gtin string) (GlobalProduct, error)
}

// resolveGtinSQL es la consulta del mediador. El JOIN vive entero en la base y la UNIQUE de
// gtin garantiza a lo sumo una fila: QueryRow es la forma correcta, no una admisión por
// casualidad. `attributes` nulos llegan como `{}` —el objeto vacío del home #2 (§2.9)—.
const resolveGtinSQL = `SELECT p.id, p.name, COALESCE(p.attributes, '{}'::jsonb)
	FROM global_barcodes b JOIN global_products p ON p.id = b.global_product_id
	WHERE b.gtin = $1`

// PlatformGtinLookup resuelve gtins contra el pool del rol `pos_platform`. Es un pool
// CRUDO, sin helper de tenant a propósito: el catálogo global no tiene RLS de tenant y NO
// se le fija un contexto que no existe (§5.6).
type PlatformGtinLookup struct {
	platform *pgxpool.Pool
}

// NewPlatformGtinLookup construye el mediador sobre el pool de `pos_platform`.
func NewPlatformGtinLookup(platform *pgxpool.Pool) *PlatformGtinLookup {
	return &PlatformGtinLookup{platform: platform}
}

// Resolve resuelve UN gtin contra el catálogo global. La ausencia es ErrUnknownGtin y el
// fallo de la capa de datos sube tal cual: el handler los separa (404 vs 500).
func (l *PlatformGtinLookup) Resolve(ctx context.Context, gtin string) (GlobalProduct, error) {
	if l == nil || l.platform == nil {
		return GlobalProduct{}, errors.New("the gtin lookup has no platform pool")
	}
	if gtin == "" {
		return GlobalProduct{}, errors.New("the gtin lookup needs a gtin")
	}

	var product GlobalProduct
	err := l.platform.QueryRow(ctx, resolveGtinSQL, gtin).
		Scan(&product.ID, &product.Name, &product.Attributes)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return GlobalProduct{}, ErrUnknownGtin
	case err != nil:
		return GlobalProduct{}, fmt.Errorf("resolve the gtin in the global catalog: %w", err)
	default:
		return product, nil
	}
}
