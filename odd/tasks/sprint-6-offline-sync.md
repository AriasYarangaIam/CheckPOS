# sprint-6-offline-sync

Sprint 6 (Semana 6) del plan de entrega: sincronización offline como núcleo del producto.
Rama `feat/sprint-6-offline-sync`, apilada sobre `feat/sprint-5-cash-close-durability` (tip `078422d`).
Fuente normativa: `SPRINTS.md` Sprint 6 (Semana 6) y los contratos que cada tarea cita.

## Specs

### S1 — Objetivo del sprint

> Que una caída de 3G no detenga la venta ni cobre dos veces, y que ningún error de sincronización llegue jamás al mostrador.

### S2 — W6-DB-1 · Ciclo de vida del claim y retención

> Ciclo de vida del claim en `idempotency_keys`: `in_flight` → `completed` con el snapshot de respuesta acoplado al estado, y la retención de **400 días** ejecutada por el rol de mantenimiento

> Un snapshot en vuelo nunca se sirve como éxito fantasma; el borrado de claves corre solo por el rol de mantenimiento, sobre esa tabla y ninguna otra

Contrato: `docs/DATABASE.md` §7.1 (tres ramas del claim), §7.2 (retención 400 días, borrado bajo `pos_maintenance`, DELETE solo sobre `idempotency_keys`), §5.5 (matriz de concesiones). El `CHECK` acoplado al estado ya existe
(`migrations/20261015000007_sync.sql`); esta tarea entrega el job de retención, no un cambio de esquema.

### S3 — W6-DB-2 · Mantenimiento de particiones

> Mantenimiento de particiones: pre-creación idempotente **seis meses** hacia adelante, alerta por filas en `sales_default` y remediación detach/re-attach en una transacción

> Correr el job dos veces el mismo día deja el mismo conjunto de particiones; la remediación deja `sales_default` vacía o revierte completa

Contrato: `docs/DATABASE.md` §6.3 (horizonte de seis meses, job idempotente, split F9 entre scheduler `pos_maintenance` y DDL del rol de migración), §6.4 (`sales_default` como señal de salud), §6.5 (remediación F5 en una transacción), §6.6 (skip-without-blocking), §6.7 (`SELECT count(*) FROM sales_default > 0` como condición de alerta).

### S4 — W6-BE-1 · Soporte del worker de sincronización

> Soporte del worker de sincronización: flush por lotes, jitter, retroceso exponencial y POST idempotente, respetando `Retry-After` en el `409`

> Cien ventas encoladas se vacían sin estampida y los duplicados colapsan a un ack cada uno

Contrato: `ARCHITECTURE.md:84`, `docs/BACKEND.md` §8.2 (parámetros `(propuesta)`: lote máximo 25, base 1 s, factor 2, techo 60 s, jitter ±30 %, `409` respeta `Retry-After`), `docs/DATABASE.md` §7.1 Branch C.

### S5 — W6-BE-2 · Reconciliación silenciosa y orden padre → hijo

> **Reconciliación silenciosa** del lado servidor ante violaciones de unicidad inbound y reordenamiento padre → hijo del lote

> Una violación de unicidad entra, se resuelve del lado servidor y devuelve `200` con la fila resuelta: el POS no recibe ningún error

Contrato: `docs/DATABASE.md` §7.7 (F20 renombrado determinista, F27 `gtin` a `NULL`, F38 cierre duplicado no-op), §7.8 (catálogo → ledger, `fiado_accounts` → `fiado_movements`), §7.9 (política vinculante), `ARCHITECTURE.md:24`, `:111-116`, `:178`.

### S6 — W6-BE-3 · `GET /sync/cursor`

> `GET /sync/cursor`: filas de las tablas mutables con `updated_at > cursor`, incluyendo tombstones, con avance solo hasta el máximo realmente devuelto

> Una fila actualizada mientras se armaba la respuesta se vuelve a traer en el pull siguiente, dentro del solape de 15 minutos

Contrato: `docs/BACKEND.md` §4.8 (auth por token, idempotente, no encolable, parámetro `cursor` `(propuesta)`), `docs/DATABASE.md` §7.3 (solo `products` y `fiado_accounts`; el cursor nunca transporta dinero ni stock; cursor futuro devuelve vacío).

### S7 — W6-FE-1 · IndexedDB acotado y degradación por cuota

> IndexedDB limitado al subconjunto de catálogo más la cola de eventos pendientes, con degradación visible cuando se agota la cuota

> Al agotarse la cuota aparece una venta bloqueada con su motivo, nunca una pantalla en blanco

Contrato: `docs/BACKEND.md` §8.3 (`S5-1`), `docs/DESIGN.md:254-255`.

### S8 — W6-FE-2 · Shell de PWA con caché versionada

> Shell de PWA con caché versionada, sin Cache-First ciego para la lógica de la aplicación

> Un despliegue nuevo llega al dispositivo y la ruta de cobro no queda fijada a una versión vieja

### S9 — W6-FE-3 · Motor de sincronización del cliente

> Motor de sincronización del cliente: flush por lotes al recuperar conectividad, con retroceso y sin abrir jamás una segunda venta

> Con la red intermitente, la cola se vacía sola y el cajero nunca ve un error, un prompt ni un reintento manual

Contrato: `ARCHITECTURE.md:91` (sin WebSocket, sin Background Sync API, el motor corre en el cliente), `docs/BACKEND.md` §8.2.

### S10 — Criterios de aceptación (verbatim del plan)

> - Cien ventas encoladas se vacían en lotes sin estampida y cada duplicado colapsa a un solo ack (`docs/BACKEND.md` §8.2; `ARCHITECTURE.md:84`).
> - **Ninguna violación de unicidad inbound llega al cajero**: el renombrado determinista por nombre, el GTIN puesto en `NULL` y el cierre duplicado como no-op se resuelven del lado servidor y el POS no recibe ningún error (`ARCHITECTURE.md:24`, `:111-116`, `:178`; `docs/DATABASE.md` §7.7, §7.8, §7.9; `docs/DESIGN.md:256`).
> - Dinero y stock **no** se resuelven por last-write-wins: convergen solo por eventos append-only y deltas firmados (`ARCHITECTURE.md:25`; `docs/DATABASE.md` §7.4, §7.5).
> - El cursor avanza solo hasta el máximo `updated_at` realmente devuelto, con solape de 15 minutos, y nunca transporta dinero ni stock (`docs/DATABASE.md` §7.3).
> - El motor de sincronización corre en el cliente: no hay WebSocket y no se depende de la Background Sync API (`ARCHITECTURE.md:91`).
> - Al agotarse la cuota local, la venta se bloquea con motivo visible en lugar de fallar en blanco.
> - Los únicos POST encolables son los seis de la enumeración exhaustiva; la edición de producto con `PATCH` es solo en línea (`docs/DATABASE.md` §7.6; `docs/FRONTEND.md:53`).

### S11 — Definition of Done (verbatim del plan)

> El cierre offline → reconexión → flush por lotes → ack queda probado por tests de cola y de repetición; ninguna violación de unicidad inbound llegó al cajero en la prueba; ninguna venta se perdió al matar la pestaña a mitad de la cola.

### S12 — Corrección de arrastre del plan (vinculante)

> Las dos cosas quedaron invertidas en el contrato: el servidor reconcilia en silencio y el POS **nunca** expone un error de sincronización (`ARCHITECTURE.md:24`, `:111-116`, `:178`; `docs/DATABASE.md` §7.9 y §8 enmienda 3; `docs/DESIGN.md:256`). Esta semana entrega reconciliación silenciosa

Ninguna superficie de interfaz puede mostrar un conflicto, un prompt de reintento ni un error de sincronización.

## Decisiones de forma (`(propuesta)` donde el contrato no fija el JSON)

Contrato congelado para que las dos capas implementen la misma forma; ninguna de estas decisiones
agrega requisitos.

1. **Contrato de `GET /sync/cursor`.** `GET /sync/cursor?cursor=<RFC3339>`; el parámetro es opcional y
   su ausencia significa pull inicial completo. Respuesta `200`:

   ```json
   {
     "cursor": "2026-10-22T03:06:38.123456Z",
     "products": [ <fila idéntica a productsBody de GET /products> ],
     "fiado_accounts": [
       { "id": "...", "name": "...", "phone": null, "created_at": "...",
         "updated_at": "...", "received_at": "..." }
     ]
   }
   ```

   `cursor` es el `MAX(updated_at)` realmente devuelto entre las dos tablas; si no se devolvió ninguna
   fila, se repite el cursor recibido (o `null` en el pull inicial vacío) y nunca se usa `now()`.
   Se reutiliza la forma de fila de producto ya servida por `GET /products` (`internal/catalog`):
   el cursor no inventa un segundo contrato de producto.

2. **Tombstones.** `products` los propaga con `deleted_at` no nulo. `fiado_accounts` **no** tiene
   columna `deleted_at` en `migrations/20261015000005_fiado.sql` y el MVP no define borrado de
   cuentas: el cursor no fabrica un tombstone donde el esquema no lo sostiene. Se documenta como
   límite, no se inventa.

3. **Ventana de solape de 15 minutos.** Se implementa con `updated_at > cursor`, sin restar el solape
   en la consulta: el cursor devuelto es el máximo real, no `now()`, que es exactamente el mecanismo
   que hace que una fila estampada durante el armado de la respuesta se re-traiga en el pull
   siguiente. No se agrega un delta de 15 minutos al `WHERE`.

4. **Jobs de mantenimiento.** Un binario `cmd/maintenance` con dos subcomandos explícitos
   (`retention`, `partitions`) para que la operación los agende por separado y cada uno sea
   verificable aislado. El de particiones corre el DDL con el rol de migración (split F9) y expone la
   alerta como salida no-cero más línea estructurada cuando `sales_default` no está vacía.

5. **Retención.** `DELETE FROM idempotency_keys WHERE state = 'completed' AND completed_at < now() - interval '400 days'`.
   Nunca toca filas `in_flight`. Se ejecuta solo con el rol de mantenimiento; `pos_app` no recibe ese
   DELETE.

6. **Alerta de particiones.** La remediación F5 solo corre cuando se invoca explícitamente; el job
   periódico crea particiones y **reporta** la condición de alerta sin remediar por su cuenta, para
   que la remediación sea una decisión operativa y no un efecto colateral silencioso.

7. **Motor cliente.** Se prefiere la `BroadcastChannel`/evento de conectividad del navegador; sin
   WebSocket y sin Background Sync API. Un solo drenaje activo a la vez por pestaña (bandera en
   memoria + coordinación entre pestañas) para que dos pestañas no abran la misma venta.

8. **PWA.** El service worker usa network-first para navegación y API, y cache-first solo para
   assets con hash de contenido; el nombre de la caché incluye la versión del build y el `activate`
   borra las cachés anteriores.

9. **Alcance real del orden padre → hijo (límite, no omisión).** `docs/DATABASE.md` §7.6 enumera los
   seis POST encolables como peticiones individuales y el contrato **no define un endpoint de lote**.
   Sin endpoint de lote no existe una lista que el servidor pueda reordenar: el orden padre → hijo se
   materializa en el **orden de drenaje de la cola del cliente** (catálogo antes que ledger, cuenta
   antes que movimiento). No se inventa un endpoint de lote para satisfacer una frase que el contrato
   no acompaña. De los seis POST encolables hoy existen cuatro (`POST /sales`,
   `POST /sales/{id}/void`, `POST /cash-closes`, `POST /products`); `POST /fiado-movements` llega en
   Sprint 7 y `POST /stock-adjustments` en Sprint 8, y heredan la misma disciplina cuando se
   construyan. La verificación de W6-BE-2 se limita a los endpoints existentes.

10. **W6-DB-1 no trae migración.** El `CHECK` acoplado al estado y el `GRANT DELETE ON TABLE
    idempotency_keys TO pos_maintenance` ya están en `migrations/20261015000007_sync.sql` y
    `migrations/20261016000001_roles_and_grants.sql:134`. La tarea entrega el job de retención y su
    evidencia, no DDL nuevo.

## Tasks

- [ ] **T1** Documento del sprint + rama `feat/sprint-6-offline-sync` — RUTA: inline. S: S1.
- [ ] **T2** W6-DB-1 · Job de retención de 400 días por rol de mantenimiento — RUTA: delegada (writer BE). S: S2. Commit: —
- [ ] **T3** W6-DB-2 · Job de particiones: pre-creación idempotente 6 meses, alerta `sales_default`, remediación F5 — RUTA: delegada (writer BE). S: S3. Commit: —
- [ ] **T4** W6-BE-1 · Soporte del worker: lotes, jitter, retroceso exponencial, `Retry-After` — RUTA: delegada (writer BE). S: S4. Commit: —
- [ ] **T5** W6-BE-2 · Reconciliación silenciosa inbound y orden padre → hijo — RUTA: delegada (writer BE). S: S5. Commit: —
- [ ] **T6** W6-BE-3 · `GET /sync/cursor` — RUTA: delegada (writer BE). S: S6. Commit: —
- [ ] **T7** W6-FE-1 · IndexedDB acotado + degradación visible por cuota — RUTA: delegada (writer FE). S: S7. Commit: —
- [ ] **T8** W6-FE-2 · Shell PWA con caché versionada — RUTA: delegada (writer FE). S: S8. Commit: —
- [ ] **T9** W6-FE-3 · Motor de sincronización del cliente — RUTA: delegada (writer FE). S: S9. Commit: —
- [ ] **T10** T-CLOSE · Verificación completa (`go test ./...`, `vitest --run`, `tsc --noEmit`) y criterios 1:1 — RUTA: inline. S: S10, S11, S12.

## Log

- **L1** (solicitud del usuario, verbatim): «Ahora ejecuta los planes indicados para el Sprint 6»
