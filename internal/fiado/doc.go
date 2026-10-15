// Package fiado implementa el módulo Fiado: la venta a crédito contra una cuenta de
// cliente.
//
// Tablas que posee: `fiado_accounts` y `fiado_movements` (docs/DATABASE.md §4, grupo 4).
//
// Frontera: no existe una columna de saldo escribible. El saldo es
// `SUM(fiado_movements.amount_cents)` y se calcula, nunca se guarda: una columna de saldo
// es una segunda fuente de verdad que se desincroniza (docs/BACKEND.md §3.2). El signo de
// `amount_cents` ES el tipo de movimiento —cargo positivo, pago negativo—, así que
// tampoco hay columna `kind`; una anulación escribe una fila compensatoria de signo
// invertido. Y no hay recordatorios por WhatsApp: eso no es de este módulo
// (docs/BACKEND.md §2.2).
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31). Cuando el fiado se
// cobra en el mostrador, el movimiento lo escribe este módulo, no el Ledger.
//
// Sprint 1 solo declara la frontera: el saldo computado y el cargo offline son Sprint 7
// (docs/BACKEND.md §10.7).
package fiado
