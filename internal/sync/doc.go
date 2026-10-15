// Package sync implementa el módulo Sync: el claim de idempotencia que hace repetible
// cada POST encolable y el pull por cursor.
//
// Tablas que posee: `idempotency_keys` (docs/DATABASE.md §4, grupo 6).
//
// Frontera: no contiene reglas de negocio. La fila ES el claim: la clave se reclama con
// `UNIQUE (tenant_id, key)` y un INSERT, nunca con un SELECT seguido de un INSERT
// (docs/DATABASE.md §7.1). Tampoco existe una tabla `sync_events`: el evento de
// sincronización se fusionó en esta tabla y el conteo vinculante es 14, no 15
// (docs/DATABASE.md §8 enmienda 5). La tabla no es append-only a propósito: el ciclo de
// vida necesita UPDATE (in_flight -> completed) y la retención de 400 días necesita
// DELETE (§2.12 excepción, MD-3).
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31): este módulo es
// dueño del claim, y cada módulo decide qué hacer después de reclamarlo.
//
// El nombre del paquete coincide con el del módulo y no con la biblioteca estándar `sync`;
// quien importe ambos en el mismo archivo debe usar un alias.
//
// Sprint 1 solo declara la frontera: el protocolo de claim y el cursor son Sprint 6
// (docs/BACKEND.md §10.6, §6).
package sync
