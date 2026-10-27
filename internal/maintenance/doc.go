// Package maintenance implementa los dos trabajos de mantenimiento programado de la base
// del POS (Sprint 6): la retención de 400 días de las claves de idempotencia (W6-DB-1,
// docs/DATABASE.md §7.2) y el mantenimiento de particiones de `sales` (W6-DB-2,
// docs/DATABASE.md §6.3-§6.7).
//
// Es un paquete de operación, no una superficie de negocio: no hay HTTP acá y ningún
// handler lo importa. El ejecutable que lo usa es cmd/maintenance, con un subcomando por
// trabajo.
//
// Frontera de privilegios (split F9): la retención corre con el rol `pos_maintenance`, cuyo
// único DELETE de datos es sobre `idempotency_keys`; el DDL de particiones corre con el rol
// de migración. El paquete no elige DSNs: los recibe del ejecutable, que lee
// internal/platform/config.
package maintenance
