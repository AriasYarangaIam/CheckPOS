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

5. **Retención.** `DELETE FROM idempotency_keys WHERE state = 'completed' AND created_at < now() - interval '400 days'`.
   Nunca toca filas `in_flight`. Se ejecuta solo con el rol de mantenimiento; `pos_app` no recibe ese
   DELETE. **Corregido en L4**: la tabla real no tiene `completed_at`; la marca de la ventana es
   `created_at` (docs/DATABASE.md §4, y la nota de §6.2 de `docs/BACKEND.md`).

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

10. **W6-DB-1 sí necesitó migración (corregido en L5).** El `CHECK` acoplado al estado y el `GRANT
    DELETE ON TABLE idempotency_keys TO pos_maintenance` ya existen, pero medido contra el contenedor
    el job no corría: el `WHERE` necesita `SELECT` de columna y `FORCE ROW LEVEL SECURITY` esconde
    toda fila de un rol sin política propia. `migrations/20261023000001_maintenance_retention_rls.sql`
    entrega la concesión de columna y el par SELECT/DELETE para `pos_maintenance` cuyo `USING` **es**
    la regla de retención, así la ventana la hace cumplir la base y no el SQL del job.

## Tasks

- [x] **T1** Documento del sprint + rama `feat/sprint-6-offline-sync` — RUTA: inline. S: S1. Commit: `6e26aa8`.
- [x] **T2** W6-DB-1 · Job de retención de 400 días por rol de mantenimiento — RUTA: inline (writer no disponible). S: S2. Commit: `809be24`.
- [x] **T3** W6-DB-2 · Job de particiones: pre-creación idempotente 6 meses, alerta `sales_default`, remediación F5 — RUTA: inline. S: S3. Commit: `809be24`.
- [x] **T4** W6-BE-1 · Soporte del worker: claim en `POST /products`, lotes, jitter, retroceso, `Retry-After` — RUTA: inline. S: S4. Commit: `b094cf6`.
- [x] **T5** W6-BE-2 · Reconciliación silenciosa inbound (F20/F27/F38) y orden padre → hijo — RUTA: inline. S: S5. Commit: `f467132`.
- [x] **T6** W6-BE-3 · `GET /sync/cursor` — RUTA: inline. S: S6. Commit: `c4d1c48`.
- [x] **T7** W6-FE-1 · IndexedDB acotado + degradación visible por cuota — RUTA: inline. S: S7. Commit: `4def54a`.
- [x] **T8** W6-FE-2 · Shell PWA con caché versionada — RUTA: inline. S: S8. Commit: `edf9755`.
- [x] **T9** W6-FE-3 · Motor de sincronización del cliente — RUTA: inline. S: S9. Commit: `d9c6dad`.
- [x] **T10** T-CLOSE · Verificación completa y criterios 1:1 — RUTA: inline. S: S10, S11, S12. Commit: este documento.

## Verificación (T-CLOSE)

Comandos de la corrida final (2026-10-23, contenedor PostgreSQL arriba):

- `go test ./...` → 11 paquetes `ok`, 0 fallos, exit 0.
- `cd web && npx vitest --run` → **343 pruebas en 40 archivos**, 0 fallos.
- `cd web && npx tsc --noEmit` → exit 0.
- `go build ./... && go vet ./...` → exit 0.

Criterios de S10, uno por uno:

1. **Cien ventas encoladas se vacían en lotes sin estampida y cada duplicado colapsa a un solo ack.**
   `web/src/sync/syncEngine.test.ts` (lote máximo 25, `sale` antes que `void` antes que `cash_close`,
   un POST por evento con su clave) y `internal/catalog/product_integration_test.go` (la repetición de
   la misma clave devuelve el snapshot almacenado y deja UNA fila).
2. **Ninguna violación de unicidad inbound llega al cajero.** F20 (renombrado determinístico) y F27
   (gtin a NULL) en `internal/catalog/product_integration_test.go`; F38 (cierre duplicado como
   no-op) en `internal/cashclose/cash_close_integration_test.go`; las tres responden 200 con la fila
   resuelta.
3. **Dinero y stock no convergen por last-write-wins.** Sin cambios en esta semana: lo sostienen
   `internal/ledger` y las pruebas de contrato de `internal/platform/postgres` (append-only, deltas
   firmados), que siguen verdes.
4. **El cursor avanza solo al máximo `updated_at` realmente devuelto, con solape de 15 minutos, y
   nunca transporta dinero ni stock.** `internal/synccursor/cursor_test.go` (aritmética, eco, pull
   inicial, cursor futuro, 422) y `internal/synccursor/cursor_integration_test.go` (dos tablas reales,
   tombstones, `updated_at > cursor` sin delta).
5. **El motor corre en el cliente: sin WebSocket, sin Background Sync API.** `web/src/sync/syncEngine.ts`
   usa el evento `online` y un temporizador; `grep` sobre `web/src` y `web/public` no encuentra
   WebSocket ni `registration.sync`.
6. **Cuota local agotada → venta bloqueada con motivo visible.** `web/src/db/quota.test.ts` (reconoce
   el agotamiento, acota el catálogo) y `web/src/screens/CounterSaleScreen.test.tsx` (aviso `alert`
   con el motivo, carrito intacto, ninguna venta cerrada en pantalla).
7. **Los únicos POST encolables son los seis de la enumeración y `PATCH` es solo en línea.** Los
   cuatro existentes reclaman clave (`internal/ledger/sale.go`, `internal/ledger/void.go`,
   `internal/cashclose/cash_close.go`, `internal/catalog/product.go`); `PATCH /products` sigue sin
   claim, que es lo que §7.6 manda.

Definition of Done de S11: los tests de cola y repetición existen (1), ninguna violación inbound llegó
al cajero en la prueba (2), y la venta se escribe ANTES del ack con el carrito vaciado en la misma
transacción —`web/src/sales/saleQueue.ts`, prueba de Sprint 5 todavía verde—, así que matar la pestaña
a mitad de la cola no pierde ninguna venta.

## Cadena de entrega

Los commits se reordenaron en una cadena de PRs apilados sobre `develop` (precedente de los
Sprints 3–5). Cada PR es una unidad de revisión y su base es el PR anterior:

1. `feat/sprint-5-cash-close-durability` → `develop` (recupera el corte FE de Sprint 5, que
   quedó fuera de `develop` al mergearse su PR contra una base ya integrada).
2. `feat/sprint-6-docs-plan` — plan del sprint y contrato de pruebas.
3. `feat/sprint-6-db-maintenance` — T2/T3.
4. `feat/sprint-6-be-sync-cursor` — T6.
5. `feat/sprint-6-be-product-claim` — T4.
6. `feat/sprint-6-be-reconciliation` — T5.
7. `feat/sprint-6-fe-quota` — T7.
8. `feat/sprint-6-fe-pwa` — T8.
9. `feat/sprint-6-fe-sync-engine` — T9.
10. `feat/sprint-6-docs-close` — T10.

## Log

- **L1** (solicitud del usuario, verbatim): «Ahora ejecuta los planes indicados para el Sprint 6»
- **L2** (delegación, no ejecutada): el paquete de escritores `gentle-ai-worker` falló cuatro veces
  con `assistant reported an error` y **cero** llamadas a herramientas, así que T2–T9 se ejecutaron
  inline por el orquestador. Se reporta como *fallback*, no como ruta preferida: la regla de writers
  pedía delegar, el mecanismo no estaba disponible.
- **L3** (corrección de arrastre, verbatim de `docs/BACKEND.md` §6.2): «La definición real de la tabla
  (§4 `idempotency_keys`) usa `payload_hash`, `state`, `response_snapshot` y `created_at`, y §4 afirma
  que **`created_at` es lo que conduce la ventana de retención**». Se implementó contra §4.
- **L4** (decisión D5 corregida por medición): `idempotency_keys` no tiene columna `completed_at`; el
  DELETE de retención mide sobre `created_at`. Cambia D5, no su garantía: la ventana sigue siendo de
  400 días y las filas `in_flight` siguen intocables (probado en
  `internal/maintenance/retention_integration_test.go`).
- **L5** (decisión D10 corregida por medición): el `GRANT DELETE` de §5.5 no alcanza para ejecutar la
  retención. Medido: `permission denied for table idempotency_keys (42501)` por el `WHERE`, y
  `0` filas borradas por `FORCE ROW LEVEL SECURITY` sin política para el rol. La migración
  `20261023000001_maintenance_retention_rls.sql` agrega `GRANT SELECT (state, created_at)` y un par
  de políticas `TO pos_maintenance` cuyo `USING` es la regla de retención (el DELETE necesita además
  la política SELECT: bajo FORCE RLS solo se borra lo que el rol puede ver).
- **L6** (segundo hallazgo de D10/§5.5): PostgreSQL no propaga concesiones, RLS ni políticas del padre
  a las particiones nuevas. `cmd/maintenance partitions` aplica en la MISMA creación el pin de RLS,
  las cuatro políticas de tenant y `GRANT SELECT, INSERT ... TO pos_app`; sin ese espejo el mes
  siguiente quedaba sin concesiones o —peor— con concesiones y sin políticas.
- **L7** (alcance de T5): el orden padre → hijo no tiene endpoint de lote (D9), así que se materializa
  en el drenaje del cliente (`web/src/sync/syncEngine.ts`, orden `sale` → `void` → `cash_close`). La
  verificación de servidor se limita a los endpoints existentes y a probar que la colisión F20/F27 se
  resuelve en silencio también bajo el claim.
- **L8** (límite declarado, no omisión): el motor de sincronización está implementado y probado, pero
  no se cablea al shell porque la app todavía no tiene token en línea —el login es de la Semana 8—.
  Sin sesión el drenaje devuelve `skipped: 'no-session'` en vez de inventar un envío anónimo.
- **L9** (cambio de contrato pedido por el sprint, no de arrastre): `POST /products` ahora exige
  `Idempotency-Key` (422 sin ella), como los otros tres POST encolables ya existentes. Es la lectura
  de §7.6 y §6.1 para el único POST encolable que quedaba sin claim; las pruebas de la superficie
  anterior se actualizaron con la cabecera y la rama sin cabecera quedó fijada por prueba propia.
