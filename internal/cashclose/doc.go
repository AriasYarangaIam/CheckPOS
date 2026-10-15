// Package cashclose implementa el módulo Cash close: el cierre de caja, que es una
// proyección sobre el ledger y no un segundo libro.
//
// Tablas que posee: `cash_closes` y `cash_close_counts` (docs/DATABASE.md §4, grupo 5).
//
// Frontera: no lleva un segundo contador corriente. Lo esperado por método se proyecta
// sumando `sales`, y lo contado lo declara el cajero: el cierre no acumula estado propio
// entre cierres (docs/BACKEND.md §2.2). Hay un cierre por fecha de negocio por tenant,
// garantizado por `UNIQUE (tenant_id, closed_on)` (D10), y la fila es de inserción única:
// el MVP no tiene camino de enmienda. `counted_cents` es siempre cero cuando
// `payment_method = 'fiado'`, forzado por un CHECK y no por una convención (F32).
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31): este módulo lee
// `sales` a través del servicio de Ledger y escribe solo sus dos tablas.
//
// Sprint 1 solo declara la frontera: la fórmula de la proyección y el POST del cierre son
// Sprint 5 (docs/BACKEND.md §10.5).
package cashclose
