// Package cashclose implementa el módulo Cash close: el cierre de caja, que es una
// proyección sobre el ledger y no un segundo libro.
//
// Tablas que posee: `cash_closes` y `cash_close_counts` (docs/DATABASE.md §4, grupo 5).
//
// Frontera: no lleva un segundo contador corriente. Lo esperado por método se proyecta
// sumando `sales` con la fórmula F8 —`SUM(CASE kind WHEN 'sale' THEN +total_cents WHEN
// 'void' THEN -total_cents END)`— y lo contado lo declara el cajero: el cierre no acumula
// estado propio entre cierres (docs/BACKEND.md §2.2). `expected_cents` queda congelado en la
// fila del desglose, la diferencia se computa al acusar y nunca se almacena. Hay un cierre
// por fecha de negocio por tenant, garantizado por `UNIQUE (tenant_id, closed_on)` (D10), y
// la fila es de inserción única: el MVP no tiene camino de enmienda. Un cierre duplicado en
// cola es un no-op silencioso —prevalece el existente y el duplicado se confirma con su
// proyección—, nunca un error visible (F38, docs/DATABASE.md §7.7). `counted_cents` es
// siempre cero cuando `payment_method = 'fiado'`, forzado por el CHECK y por la validación
// de superficie (F32).
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31): la proyección LEE
// `sales` sobre la misma transacción de tenant y este módulo escribe solo sus dos tablas.
package cashclose
