// Package ledger implementa el módulo Ledger: el libro de dinero, que es lo único
// append-only del sistema.
//
// Tablas que posee: `sales` y `sale_lines` (docs/DATABASE.md §4, grupo 3).
//
// Frontera: no borra historia. Una anulación es un evento compensatorio —una fila nueva
// de `kind = 'void'` que referencia la original—, nunca un DELETE ni una edición
// (docs/BACKEND.md §10.5). Y no existe una tabla `tenders`: el medio de pago es la columna
// `sales.payment_method`, con seis valores (docs/DATABASE.md §8 enmienda 1).
//
// `sales` está particionada por rango mensual sobre `sold_on` y su clave primaria es
// `(sold_on, sale_id)`: la partición obliga a que la clave de partición sea parte de la
// clave primaria, así que no hay UNIQUE(sale_id) a nivel de esquema (docs/DATABASE.md
// §6.1). La unicidad entre particiones descansa en el UUIDv7 del cliente más el claim de
// idempotencia.
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31). El ledger fija
// `sales` y `sale_lines`; el stock se mueve escribiendo `stock_adjustments` a través del
// servicio de Catalog.
//
// Sprint 4 entregó el cierre de venta (`POST /sales`, docs/BACKEND.md §10.4) y Sprint 5 la
// anulación (`POST /sales/{id}/void`, §10.5): una fila `kind = 'void'` nueva que espeja el
// método y el total POSITIVO del original, enlazada por `(original_sold_on, original_sale_id)`
// y con la reversa de stock delegada a Catalog como deltas nuevos `source = 'void'`.
package ledger
