// Package catalog implementa el módulo Catalog: el catálogo de un inquilino y el
// catálogo global curado por la plataforma.
//
// Tablas que posee: `products`, `stock_adjustments`, `global_products` y
// `global_barcodes` (docs/DATABASE.md §4, grupos 2 y 7).
//
// Frontera: no posee blobs. `products.photo_url` guarda una URL de object storage y los
// bytes nunca entran a la base (ARCHITECTURE.md:21,87). Las tablas globales no tienen
// tenant_id ni policy: el tenant las lee solo a través de la API y nunca escribe en
// ellas (docs/DATABASE.md §5.6).
//
// Invariantes que este módulo deberá sostener, no que deba escribir ahora:
// `products.stock = SUM(stock_adjustments.qty_delta)`, y las escrituras de proyección de
// stock nunca modifican `products.updated_at` (docs/BACKEND.md §3.2, MD-11).
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31). Ledger no toca
// `products`: lo consulta por el servicio de Catalog (docs/BACKEND.md §2.4).
//
// Sprint 1 solo declara la frontera: el CRUD de producto es Sprint 3
// (docs/BACKEND.md §10.3).
package catalog
