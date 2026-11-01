# sprint-7-fiados

Sprint 7 (Semana 7) del plan de entrega: fiados — que saber quién debe qué sobreviva a un
teléfono robado. Rama `feat/sprint-7-fiados`, anclada en `develop` (tip del Sprint 6 ya en `main`).
Fuente normativa: `SPRINTS.md` Sprint 7 (Semana 7), `docs/BACKEND.md` §4.11, §4.12, §10.7,
`docs/DATABASE.md` §4, §7.4, §7.6, §7.8 y los contratos que cada tarea cita.

## Specs

### S1 — Objetivo del sprint

> Que saber quién debe qué sobreviva a un teléfono robado.

### S2 — W7-DB-1 · Cuenta sin saldo escribible y ledger firmado append-only

> `fiado_accounts` sin columna de saldo escribible y `fiado_movements` como ledger append-only donde el **signo** de `amount_cents` es el tipo, con par-nulidad del enlace opcional a la venta

> La escritura directa de un saldo se rechaza; el saldo computado coincide con la suma de movimientos

Contrato: `docs/DATABASE.md` §4 `fiado_accounts` (sin columna de saldo; `balance = SUM(fiado_movements.amount_cents)` es invariante computado), §4 `fiado_movements` (sin columna `kind`; `fiado_movements_sold_on_sale_id_pair_nullity_chk`), §2.12 y `migrations/20261019000001_append_only_guards.sql` (guardas de fila). El esquema ya existe
(`migrations/20261015000005_fiado.sql`): esta tarea **fija** el contrato por prueba, no agrega DDL.

### S3 — W7-DB-2 · Upsert atómico de la cuenta y cursor

> Upsert atómico de la cuenta dentro de la transacción del movimiento, e índice de cursor `fiado_accounts_tenant_id_updated_at_idx`

> Un movimiento que referencia una cuenta inexistente crea la cuenta y anexa el movimiento en una sola transacción; si el claim se revierte, la cuenta también

Contrato: `docs/DATABASE.md` §7.8 (F12: el upsert es parte de la transacción del claim; si la transacción revierte, la cuenta revierte con ella), §7.4 (LWW con comparador `updated_at` de mutación del cliente, daño acotado a `name`/`phone`), §4 `fiado_accounts` (índice `fiado_accounts_tenant_id_updated_at_idx`, ya existente).

### S4 — W7-BE-1 · Registro de cliente y `GET /fiado-accounts`

> Registro de cliente (nombre obligatorio, teléfono opcional) y `GET /fiado-accounts` con el saldo computado como `SUM(fiado_movements.amount_cents)`

> El saldo es computado y no existe columna escribible que lo sostenga

Contrato: `docs/BACKEND.md` §4.12 (respuesta 200 con cuentas y saldo computado más los movimientos firmados; `401`, `404` fuera del tenant), `docs/DATABASE.md` §4 `fiado_accounts`. No se agrega un endpoint `POST /fiado-accounts`: los 13 caminos de §4 agotan la superficie HTTP del MVP y el registro del deudor viaja con el primer movimiento (F12, decisión D1 de forma).

### S5 — W7-BE-2 · `POST /fiado-movements` con claim de idempotencia

> `POST /fiado-movements` para cargo y pago parcial, encolable offline con las mismas reglas de idempotencia que las ventas

> Un cargo a fiado emitido offline se encola, sincroniza y se repite de forma idempotente

Contrato: `docs/BACKEND.md` §4.11 (cuerpo anclado a `fiado_movements`; idempotencia requerida; cola offline sí; `422` par de nulidad o monto no numérico; `409`; `401`), `docs/DATABASE.md` §7.1 (tres ramas del claim, Branch A/B/C), §7.6 (`POST /fiado-movements` es uno de los seis POST encolables).

### S6 — W7-BE-3 · Historial firmado para restaurar tras reinstalar

> Servir el historial de movimientos firmados para restaurar cuentas y saldos tras una reinstalación

> Un recorrido de borrado y reinstalación restaura cuentas y movimientos después del login

Contrato: `docs/BACKEND.md` §4.12 (la respuesta de `GET /fiado-accounts` incluye los movimientos firmados), `docs/DATABASE.md` §4 `fiado_movements` (el signo es el tipo: cargo positivo, pago negativo), `ARCHITECTURE.md:168` (la pérdida del teléfono no pierde el evento del servidor).

### S7 — W7-BE-4 · Sin recordatorio por WhatsApp

> Confirmar que no se envía ningún recordatorio de deuda por WhatsApp en el MVP

> No existe ningún camino WABA ni `wa.me` en el incremento

Contrato: `ARCHITECTURE.md:155`, `ARCHITECTURE.md:158`, `docs/DESIGN.md:207` (WhatsApp queda declarado posterior; en MVP el envío usa el share sheet nativo).

### S8 — W7-FE-1 · Cartera de deudores

> Cartera de deudores con búsqueda por nombre o teléfono y saldo computado (`docs/FRONTEND.md:51`)

> La búsqueda encuentra por nombre y por teléfono y muestra el saldo computado

Contrato: `docs/FRONTEND.md:51` (`DeudorListPane`: búsqueda por nombre/teléfono, filas nombre/teléfono/saldo computado desde `GET /fiado-accounts`), `docs/DESIGN.md:787` (master-detail; saldo en mono tabular; cargo positivo, pago negativo).

### S9 — W7-FE-2 · Cargo a fiado desde el carrito y abono parcial

> Cargo a fiado desde el carrito y registro del deudor en 2 clics, más abono parcial, sin salir del flujo de venta (`docs/PRODUCT.md:27`; `docs/FRONTEND.md:51`)

> Un cargo a fiado se completa sin salir del flujo de venta y el abono amortiza la deuda

Contrato: `docs/PRODUCT.md:27` (registro de deudor en 2 clics, historial de abonos parciales), `docs/FRONTEND.md:51` (`AbonoForm`: monto + tender Efectivo/transferencia → `POST /fiado-movements`, encolable offline), `docs/DATABASE.md` §7.6 (el movimiento es encolable y reclama clave).

### S10 — W7-FE-3 · Historial y restauración tras reinstalar

> Historial de movimientos firmados y restauración desde el servidor tras reinstalar

> Tras reinstalar, las cuentas y los movimientos vuelven desde el servidor

Contrato: `docs/BACKEND.md` §4.12, `docs/DESIGN.md:787` (historial con saldo corriente computado, nunca un saldo escribible).

### S11 — Criterios de aceptación (verbatim del plan)

> - El saldo iguala `SUM(fiado_movements.amount_cents)` y la escritura directa de un saldo se rechaza (`ARCHITECTURE.md:134`, `:172`; `docs/DATABASE.md` §4 `fiado_accounts`).
> - El signo del monto es el tipo: cargo positivo, pago negativo; no existe una columna `kind` adicional (`docs/DATABASE.md` §4 `fiado_movements`).
> - La anulación de una venta a fiado produce un movimiento compensatorio con signo invertido, nunca un `DELETE` ni una edición (`docs/DATABASE.md` §4 `fiado_movements`).
> - El cargo a fiado funciona offline y sincroniza de forma idempotente (`docs/BACKEND.md` §10.7).
> - El registro del deudor se hace en dos toques y la deuda sobrevive a la reinstalación (`docs/PRODUCT.md:27`).
> - No existe ningún camino de recordatorio por WhatsApp (`ARCHITECTURE.md:158`; `docs/DESIGN.md:207` lo declara posterior).

### S12 — Definition of Done (verbatim del plan)

> El cargo a fiado y el pago parcial funcionan offline y sincronizan de forma idempotente; los saldos son computados y nunca escritos; ninguna cuenta ni movimiento se pierde tras reinstalar.

### S13 — Advertencia de alcance del plan (vinculante)

> La interfaz **no** renderiza el aviso de límite de crédito que describe `docs/DESIGN.md:205`. La columna `fiado_accounts.credit_limit_cents` está adoptada en el contrato pero es una nota docs-only sin DDL: la UI espera el contrato, no lo inventa (`docs/DATABASE.md` §4 `fiado_accounts`, nota delta D4; `docs/BACKEND.md` §11.2).

Corolario: no se agrega la columna, no se agrega el badge de límite y no se inventa el umbral.

## Decisiones de forma (`(propueta)`/`(propuesta)` donde el contrato no fija la forma)

Contrato congelado para que las dos capas implementen la misma forma; ninguna de estas decisiones
agrega requisitos.

1. **Dónde vive el registro del deudor.** `docs/BACKEND.md` §4.11 fija el cuerpo del movimiento sin
   campos de la cuenta, y §4 declara que los 13 caminos agotan la superficie HTTP: **no se crea
   `POST /fiado-accounts`**. El registro viaja con el primer movimiento, dentro del mismo cuerpo,
   como objeto opcional `fiado_account`, y se materializa con el upsert atómico de F12 (§7.8).

2. **Cuerpo de `POST /fiado-movements`.** `201`/`200` con el movimiento resuelto más la cuenta con
   su saldo computado. El `sold_on`/`sale_id` es un par: los dos o ninguno (422 si no).

   ```json
   {
     "id": "019b0000-0000-7000-8000-000000000101",
     "fiado_account_id": "019b0000-0000-7000-8000-000000000201",
     "fiado_account": { "name": "Rosa Quispe", "phone": "+51987111222", "updated_at": "2026-10-26T12:00:00Z" },
     "amount_cents": 1500,
     "note": "Venta fiada",
     "sold_on": "2026-10-26",
     "sale_id": "019b0000-0000-7000-8000-000000000301"
   }
   ```

   `fiado_account` es opcional y solo se usa para crear o actualizar la cuenta; `name` es
   obligatorio cuando el objeto viaja (teléfono opcional, `updated_at` es la marca de mutación del
   cliente para el comparador LWW de §7.4 y su ausencia vale el mismo reloj que `created_at`).
   `amount_cents` es distinto de cero: el signo ES el tipo y un cero no tiene tipo.

3. **Acuse de `POST /fiado-movements`.** El movimiento persistido con sus valores congelados más la
   cuenta con `balance_cents` computado. Es la misma pieza que el claim almacena como snapshot, así
   que la repetición devuelve byte a byte lo mismo (§7.1 Branch A).

   ```json
   {
     "movement": {
       "id": "...", "tenant_id": "...", "fiado_account_id": "...",
       "amount_cents": -500, "sold_on": null, "sale_id": null,
       "note": "Abono efectivo", "created_at": "...", "received_at": "..."
     },
     "account": {
       "id": "...", "tenant_id": "...", "name": "Rosa Quispe", "phone": null,
       "created_at": "...", "updated_at": "...", "received_at": "...",
       "balance_cents": 1000
     }
   }
   ```

4. **Respuesta de `GET /fiado-accounts`.** Sin parámetros de paginación (el contrato no los fija y
   §4.12 no los pide): las cuentas del tenant, cada una con su historial firmado completo, que es
   lo que la restauración necesita (§4.12, W7-BE-3). Orden determinista: cuentas por `name`, luego
   `id`; movimientos por `created_at`, luego `id`.

   ```json
   {
     "accounts": [
       { "id": "...", "tenant_id": "...", "name": "...", "phone": null,
         "created_at": "...", "updated_at": "...", "received_at": "...",
         "balance_cents": 1000,
         "movements": [
           { "id": "...", "tenant_id": "...", "fiado_account_id": "...",
             "amount_cents": 1500, "sold_on": "2026-10-26", "sale_id": "...",
             "note": "Venta fiada", "created_at": "...", "received_at": "..." }
         ] }
     ]
   }
   ```

   `balance_cents` es `COALESCE(SUM(fiado_movements.amount_cents), 0)` por cuenta: se computa, nunca
   se guarda.

5. **Append-only del ledger no cambia.** La anulación de una venta a fiado escribe una fila de
   `fiado_movements` con el signo invertido y `amount_cents` negativo o positivo según corresponda;
   nunca edita ni borra (S11). Esta semana **no** construye la anulación de venta a fiado en la
   interfaz: el criterio se sostiene con la guarda de base y con el movimiento compensatorio que ya
   permite el contrato.

6. **El tender del abono no se persiste.** `fiado_movements` no tiene columna de medio de pago
   (`docs/DATABASE.md` §4) y el cuerpo de §4.11 no la transporta: el selector Efectivo/transferencia
   del `AbonoForm` es de interfaz y NO viaja al servidor. Se documenta como límite; no se inventa una
   columna ni se dobla el medio dentro de `note`.

7. **El saldo local también se computa.** La base local del dispositivo guarda cuentas y movimientos,
   nunca un saldo: la cartera suma los movimientos locales (`SUM`) igual que el servidor. El acuse y
   el pull refrescan filas, no contadores.

8. **Orden de drenaje.** Un movimiento de fiado enlazado a una venta referencia `sales(sold_on, sale_id)`:
   la venta tiene que existir antes. El orden padre → hijo de la cola queda
   `sale` → `void` → `fiado_movement` → `cash_close` (decisión D9 del Sprint 6 extendida con el
   quinto POST encolable).

9. **Cargo a fiado desde el carrito = dos eventos.** La venta con `payment_method = 'fiado'` es la
   que proyecta el "esperado fiado" del arqueo (D11), y el movimiento positivo es el que mueve la
   deuda (§7.5). Los dos se encolan en la MISMA transacción de Dexie que vacía el carrito, con claves
   de idempotencia distintas.

10. **Restauración tras reinstalar.** La restauración es una lectura de `GET /fiado-accounts` que
    reemplaza por completo las dos tiendas locales de fiado dentro de una transacción y NO toca la
    cola de eventos pendientes ni el catálogo: lo que el dispositivo no confirmó no se pierde.

## Tasks

- [ ] **T1** Documento del sprint + rama `feat/sprint-7-fiados` — RUTA: inline. S: S1.
- [ ] **T2** W7-DB-1 · Contrato de esquema fijado por prueba: sin saldo escribible, ledger firmado append-only, par-nulidad — RUTA: writer backend. S: S2, S11.
- [ ] **T3** W7-DB-2 · Upsert atómico de la cuenta en la transacción del movimiento, con rollback total — RUTA: writer backend. S: S3.
- [ ] **T4** W7-BE-1 · Registro de cliente (nombre obligatorio, teléfono opcional) y `GET /fiado-accounts` con saldo computado — RUTA: writer backend. S: S4, S11.
- [ ] **T5** W7-BE-2 · `POST /fiado-movements` con claim de idempotencia (A/B/C) — RUTA: writer backend. S: S5, S11.
- [ ] **T6** W7-BE-3 · Historial firmado servido para restaurar tras reinstalar — RUTA: writer backend. S: S6.
- [ ] **T7** W7-BE-4 · Sin camino WhatsApp/WABA/`wa.me`, fijado por prueba — RUTA: writer frontend. S: S7, S11.
- [ ] **T8** W7-FE-1 · Cartera de deudores con búsqueda por nombre o teléfono y saldo computado — RUTA: writer frontend. S: S8, S13.
- [ ] **T9** W7-FE-2 · Cargo a fiado desde el carrito con registro en 2 clics, y abono parcial — RUTA: writer frontend. S: S9, S11, S12.
- [ ] **T10** W7-FE-3 · Historial de movimientos firmados y restauración desde el servidor — RUTA: writer frontend. S: S10, S12.
- [ ] **T11** T-CLOSE · Verificación completa y criterios 1:1 — RUTA: inline. S: S11, S12, S13.

## Log

- **L1** (solicitud del usuario, verbatim): «Necesito que prosigas con el plan actual del proyecto (el siguiente Sprint que corresponda), luego de verificar que los anteriores cambios al Sprint que se avanzo se encuentren dentro de la rama "main".»
