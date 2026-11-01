# sprint-8-admin-hardening

Sprint 8 (Semana 8) del plan de entrega: Dashboard Admin y endurecimiento. Rama
`feat/sprint-8-admin-hardening`, anclada en `develop` (Sprint 7 ya en `main` por los PRs #47–#55).
Fuente normativa: `SPRINTS.md` Sprint 8 (Semana 8), `docs/BACKEND.md` §4.4, §4.14, §4.15, §11.2–§11.4,
`docs/DATABASE.md` §4 `tenants`/`stock_adjustments`, `docs/FRONTEND.md:46-53`, `docs/DESIGN.md:436-449`.

**Estado: LISTO PARA EJECUTAR.** Las cuatro decisiones de diseño que bloqueaban el sprint —D-A a
D-D— quedaron resueltas por el usuario el 2026-10-31 y están registradas abajo con su consecuencia.
Este documento fija además la medición de partida contra el código de `develop`.

## Specs

### S1 — Objetivo del sprint

> Dar al dueño la lectura de su propio negocio y cerrar los bordes del contrato, sin agregar una sola superficie de datos nueva.

### S2 — W8-FE-1 · Acceso Admin en dos pantallas

> Acceso Admin en dos pantallas: selector de rol (Empleado / Administrador) y credencial (teléfono más contraseña maestra para Admin; PIN para Empleado) (`docs/DESIGN.md:442-443`; `docs/FRONTEND.md:46-47`)

> Las dos pantallas renderizan contra `POST /auth/admin-login` y contra `devices.pin_hash`; no hay datos de tenant antes de autenticar

### S3 — W8-FE-2 · Shell Admin

> Shell Admin: sidebar de 240px con 6 destinos, badge de rol y footer de sesión con cierre de sesión del lado cliente (`docs/FRONTEND.md:48`)

> El shell navega los 6 destinos; no existe endpoint de cierre de sesión y no se inventa uno

### S4 — W8-FE-3 · Dashboard Admin

> Dashboard Admin: ventas netas, ticket promedio, desglose por tender (los seis), fiados por cobrar, cierres locales con sobrante o faltante y distribución por hora (`docs/FRONTEND.md:49`)

> Los números salen **únicamente** de la proyección del ledger local en IndexedDB más `GET /fiado-accounts`, sin ningún endpoint de reporte

### S5 — W8-FE-4 · Cierre de caja del rol Admin

> Cierre de caja del rol Admin: seis filas con `expected` / `counted` / `difference`, fila de fiado informacional con contado bloqueado en cero, y formulario de solo lectura cuando el cierre del día ya existe (`docs/FRONTEND.md:50`)

> El arqueo renderiza las seis filas y liga a `POST /cash-closes`; un cierre ya existente no se puede volver a enviar

### S6 — W8-FE-5 · Inventario, mermas y catálogo del rol Admin

> Inventario, mermas y catálogo del rol Admin: semáforo de stock, registro de merma con los tres motivos exactos, tabla de SKUs y adopción desde el catálogo global (`docs/FRONTEND.md:52-53`)

> El registro de merma encola `POST /stock-adjustments` con un motivo de la lista exacta y **sin** control de foto; la edición de producto usa `PATCH` y es solo en línea

### S7 — W8-BE-1 · `POST /auth/admin-login`

> `POST /auth/admin-login`: resuelve teléfono → tenant, verifica `tenants.master_password_hash` con argon2id y emite un JWT con `role:"admin"`

> La respuesta devuelve token y nombre del tenant, y un `401` constante y genérico sin enumeración de usuarios

### S8 — W8-BE-2 · `POST /stock-adjustments`

> `POST /stock-adjustments`: delta firmado negativo para una merma, con `source = 'manual'` y uno de los tres motivos exactos

> La merma anexa una fila al log y mueve el stock de forma aditiva sin tocar `products.updated_at`; el stock negativo se permite

### S9 — W8-BE-3 · Endurecimiento del borde

> Endurecimiento del borde: un solo sobre de error `{ error: { code, message, details? } }`, validación de los conjuntos acotados en el borde y chequeos de salud `(propuesta)`

> Un `PATCH` que envía `stock` o que intenta quitar un campo devuelve `422`; toda respuesta de error usa la misma forma

### S10 — W8-BE-4 · Observabilidad y alerta operativa `(propuesta)`

> Observabilidad y alerta operativa `(propuesta)`: logs estructurados sin PIN, fotos ni payloads, y la alerta por filas en `sales_default` dirigida al canal de operaciones

> La alerta de particiones llega al canal de operaciones y en ningún caso al cajero

### S11 — W8-DB-1 · Migración de `tenants.master_password_hash`

> Migración de `tenants.master_password_hash` como `TEXT NOT NULL` con argon2id, ejecutada por el rol de migración, más el backfill de alta de administrador

> La columna existe, `POST /auth/admin-login` puede implementarse y queda decidido qué ocurre con un tenant sin hash

### S12 — W8-DB-2 · Revisión final del contrato de esquema

> Revisión final del contrato de esquema contra el checklist de arquitectura: 14 tablas, dos columnas JSONB, seis valores de `payment_method`, cero concesiones de tenant sobre las tablas globales y append-only en las cinco tablas

> `pos_app` no tiene `DELETE` sobre ninguna tabla de negocio ni `BYPASSRLS`, y las tablas globales no tienen política de tenant

### S13 — Criterios de aceptación (verbatim del plan)

> - El dashboard Admin renderiza **solo** la proyección del ledger local más `GET /fiado-accounts`: no se agrega ningún endpoint de reporte ni de agregados (`ARCHITECTURE.md:138`; `docs/BACKEND.md` §4.15).
> - Un token de empleado no lleva claim `role` y un token de Admin sí lleva `role: "admin"`; la ausencia del claim significa empleado (`ARCHITECTURE.md:23`, `:126`).
> - El `401` del login de administrador es genérico y constante, sin distinguir entre teléfono inexistente y contraseña incorrecta (`ARCHITECTURE.md:126`).
> - Los tres motivos de merma son exactamente `Vencimiento`, `Rotura en transporte` y `Daño en vitrina` (`docs/DESIGN.md:217`).
> - La merma no tiene captura de foto en esta versión (`docs/DATABASE.md` §4 `stock_adjustments`, nota delta D5).
> - Ningún rol de runtime puede borrar ni actualizar una fila del ledger (matriz de concesiones de `docs/DATABASE.md` §5.5).

### S14 — Definition of Done (verbatim del plan)

> El dueño entra con la contraseña maestra del tenant y ve sus propios números sin que exista un endpoint de reporte; el registro de merma escribe un delta firmado con un motivo de la lista exacta; el checklist de esquema de arquitectura se verifica entero sobre las 14 tablas.

### S15 — Alcance del dashboard (delta D12, diferido)

> El delta registrado que proponía `GET /sales` o `GET /dashboard/summary` más una tabla de asistencia **sigue diferido**: no se agrega ningún endpoint de reporte en esta versión y el marcaje de asistencia y el listado de turnos quedan fuera (`ARCHITECTURE.md:138`; `docs/DESIGN.md:231`; `docs/BACKEND.md` §11.2 y §11.3). Tampoco existen columnas de impuestos ni de descuentos en el esquema (`docs/DATABASE.md` §4 `sales`), ni `products.cost_cents` (nota delta D11): la pantalla de catálogo no renderiza márgenes ni alertas de costo.

### S16 — Advertencia de forma del login (bloqueante declarado)

> La contraseña maestra vive como columna futura documentada, y su declaración `TEXT NOT NULL` sin valor por defecto convive mal con la regla "tenant sin hash → 401 genérico". La decisión queda registrada como divergencia abierta en `docs/BACKEND.md` §11.4 #7 y debe resolverse antes de esta semana.

## Medición de partida (verificada en el código de `develop`)

Lo que el sprint describe como trabajo y **ya existe** — se convierte en PIN, no en construcción:

1. **El sobre de error único ya existe** (`internal/platform/httpx/errors.go`): `{ error: { code, message, details? } }` y `500` que nunca filtra la causa. W8-BE-3 en esa mitad es verificación.
2. **`PATCH /products` ya rechaza `stock` y el intento de quitar un campo** con `422`
   (`internal/catalog/products_handler.go:370` y `:385`, con la lista `patchFields` como definición de
   «quitar»). La otra mitad de W8-BE-3 también es PIN.
3. **Los dos chequeos de salud ya existen** (`GET /healthz` y `GET /readyz` con verificación de los
   pools), incluida la aserción de rol de arranque.
4. **La alerta de particiones ya existe** como salida no-cero más línea estructurada
   (`internal/maintenance` + `cmd/maintenance`): lo que W8-BE-4 agrega es el destino operativo y la
   revisión de que ningún log lleve PIN, fotos ni payloads.
5. **La proyección local del arqueo ya existe** (`web/src/sales/cashClose.ts`: `expectedByMethod`,
   `countLine`, `closeTotalSummary`), y el arqueo ya es de solo lectura cuando el cierre del día
   existe (`CashCloseScreen`). W8-FE-4 reutiliza la proyección; lo nuevo es la variante Admin.
6. **El claim `role` ya está implementado y validado** en `internal/identity/token.go`: un valor
   distinto de `"admin"` no puede circular y la ausencia del claim significa empleado.

Lo que **falta** y es trabajo real del sprint:

1. **`tenants.master_password_hash` no existe** (`migrations/20261015000001_identity.sql`: `tenants`
   tiene `id`, `name`, `plan`, `created_at`). W8-DB-1 entrega el DDL.
2. **`pos_auth` no puede leer `tenants`.** La matriz le da `SELECT` solo sobre `users`
   (`roles_and_grants.sql:120`) y `tenants` queda fuera del RLS por diseño (§5.6). El login de
   administrador no se puede implementar con la concesión actual: W8-DB-1 tiene que agregar la
   concesión **por columna** (`id`, `name`, `master_password_hash`) al rol pre-auth. Hallazgo: la
   tarea es más grande que «agregar una columna».
3. **`POST /auth/admin-login` no existe** (no hay admin-login en `internal/identity`).
4. **`POST /stock-adjustments` no existe.** La tabla sí, con `source_chk` (`'sale','void','manual'`)
   y **sin** CHECK sobre `reason`: los tres motivos exactos se validan en el borde.
5. **El cliente no tiene sesión en línea en ninguna parte.** `web/src/auth/deviceSession.ts:181` lo
   dice literalmente («el camino de UI que consigue el token todavía no existe»), el motor de
   sincronización devuelve `skipped: 'no-session'` y `restoreFiadoAccounts` se saltea. W8-FE-1 es el
   primer camino que emite un token en producción, y con él se encienden el drenaje de la cola y la
   restauración del fiado.
6. **No existe ninguna pantalla Admin**: ni selector de rol, ni credencial, ni shell de 240px, ni
   dashboard, ni inventario/mermas/catálogo del rol. El menú actual tiene seis destinos con tres
   «Próximamente», y el bloque de administración está detrás del PIN del dispositivo.

## Decisiones abiertas (bloquean la implementación)

### D-A · Forma de `tenants.master_password_hash` (divergencia §11.4 #7, S16)

`TEXT NOT NULL` sin default no convive con «tenant sin hash → 401 genérico»: o la columna admite el
estado «sin contraseña», o el backfill hace que ese estado no exista.

- **(A1) `TEXT NOT NULL` + backfill con hash argon2id aleatorio inutilizable.** El contrato
  documentado se respeta al pie de la letra; un tenant sin contraseña configurada simplemente no puede
  iniciar sesión, y «sin hash» deja de ser un estado posible. La alta fija la contraseña real.
- **(A2) `TEXT NULL`; `NULL` significa «sin contraseña» y responde el `401` genérico.** Representa el
  estado real, pero **cambia el contrato documentado** (`NOT NULL` → nullable) y esa enmienda debe
  registrarse en `docs/DATABASE.md`.
- **(A3) `TEXT NOT NULL` con backfill desde una variable de entorno en la migración.** Todos los
  tenants existentes arrancan con la misma contraseña conocida. Se descarta por seguridad salvo
  instrucción explícita.

**Resuelto (2026-10-31): A1 — `TEXT NOT NULL`.** El contrato documentado no se enmienda: la migración
agrega la columna `NOT NULL` y el backfill escribe un hash argon2id aleatorio que nadie conoce, así que
un tenant sin contraseña configurada no puede iniciar sesión y «sin hash» deja de ser un estado
posible. La contraseña real se fija por D-B. Divergencia §11.4 #7: **resuelta** por esta decisión.

### D-B · Dónde se fija la contraseña maestra (no hay camino en los 13 endpoints)

`docs/BACKEND.md` §4.15 declara que los 13 caminos agotan la superficie HTTP, así que el «backfill de
alta de administrador» que pide W8-DB-1 no tiene endpoint. Sin decidir esto, la contraseña solo podría
fijarse por SQL a mano.

- **(B1) Subcomando de `cmd/maintenance`** (`admin-password`): operativo, auditable, sin superficie
  HTTP nueva, reutiliza el rol de migración y el patrón de los jobs ya existentes.
- **(B2) Procedimiento SQL documentado** en `docs/` para que lo corra el operador: cero código nuevo,
  cero tests, y el error humano queda sin red.
- **(B3) Endpoint nuevo `POST /auth/admin-setup`**: contradice «los 13 caminos agotan la superficie» y
  abre una ruta de escritura pre-auth; requeriría autorización explícita como enmienda.

**Resuelto (2026-10-31): B1 — subcomando `cmd/maintenance admin-password`.** La contraseña se lee por
entrada estándar, nunca por argumento —el argumento queda en el historial del shell y en la tabla de
procesos—, se deriva con argon2id y se actualiza con el rol de migración. No se agrega ninguna ruta
HTTP: §4.15 sigue siendo cierto.

### D-C · Dónde vive el token en el cliente y cuánto dura

El POS necesita el token para drenar la cola y restaurar el fiado, y el contrato dice que el cierre de
sesión es del lado del cliente (sin endpoint).

- **(C1) Solo en memoria:** la máxima seguridad y el peor costo operativo —cada recarga de la pestaña
  exige volver a escribir la contraseña maestra, y la cola no drena hasta entonces.
- **(C2) `sessionStorage`:** sobrevive recargas de la pestaña y muere al cerrarla; el logout lo borra.
  Expone el token a XSS dentro de la misma pestaña, que es el mismo supuesto que ya tiene la app.
- **(C3) `localStorage`:** el POS abre listo cada mañana sin volver a autenticar; el token sobrevive a
  todo hasta el logout o su expiración (`JWT_TTL`, hoy 12 h).

**Resuelto (2026-10-31): C2 — `sessionStorage`.** El token del dueño sobrevive las recargas de la
pestaña y muere al cerrarla; el cierre de sesión lo borra. Recargar la PWA durante el turno no obliga a
reescribir la contraseña maestra, y el token no queda escrito en el disco de un dispositivo perdido.

### D-D · Quién tiene sesión en línea: ¿solo el Administrador?

La fila W8-FE-1 dice «PIN para Empleado» y «teléfono más contraseña maestra para Admin», y su
entregable nombra `POST /auth/admin-login` y `devices.pin_hash`. Nada en el sprint agrega una UI de
`POST /auth/login` (empleado) ni de `POST /auth/device-bind`, que hoy el cliente tampoco tiene.

- **(D1) Solo Admin, como dice el plan:** el dueño inicia sesión con su contraseña maestra y la cola
  drena bajo su token; el turno del empleado funciona offline por PIN y sincroniza cuando el dueño
  entra. Consecuencia a declarar: sin sesión de dueño, la cola no sale del dispositivo.
- **(D2) Empleado también con login en línea** (`POST /auth/login`, teléfono y contraseña) y bind del
  dispositivo: el plan no lo pide esta semana, pero es lo que hace que el turno del empleado sincronice
  por sí solo. Agrega una pantalla y un camino de token no listados en el sprint.
- **(D3) D1 ahora y D2 declarado como deuda** para el Sprint 9 (piloto), registrado en el log y en el
  documento de entrega.

**Resuelto (2026-10-31): D1 — solo el Administrador tiene sesión en línea.** El Empleado entra con su PIN
contra `devices.pin_hash` y opera offline; el token que drena la cola y que restaura el fiado es el del
dueño. **Consecuencia declarada y aceptada:** sin una sesión de dueño, la cola del dispositivo no sale
hacia el servidor. No se agrega la UI de `POST /auth/login` ni la de `POST /auth/device-bind`, y el
cierre de la entrega lo registra como límite conocido, no como olvido.

### Decisiones aplicadas por recomendación (sin objeción en la ronda de decisiones)

- **D-E · Qué cuenta el dashboard como «ventas netas».** Recomendación: la proyección del ledger
  local incluye los eventos pendientes además de los acusados, porque el dashboard es del dueño y su
  día no desaparece cuando la red falla; el badge de «N ventas por sincronizar» que ya existe en la
  cabecera dice qué parte no está confirmada. La alternativa —contar solo `completed`— muestra S/ 0 en
  un día sin red.
- **D-F · Canal de la alerta de particiones.** Recomendación: la línea estructurada más la salida
  no-cero que el job ya produce son el canal de operaciones, documentado como tal. Un webhook externo
  agregaría una dependencia y un secreto que el sprint no tiene presupuestados; si lo quieres, es un
  addendum explícito.

## Tasks (provisionales: dependen de D-A a D-D)

- [ ] **T1** Documento del sprint + rama `feat/sprint-8-admin-hardening` — RUTA: inline. S: S1.
- [ ] **T2** W8-DB-1 · Columna `master_password_hash` + concesión por columna a `pos_auth` + backfill con hash inutilizable, más el subcomando `admin-password` — RUTA: inline (D-A=A1, D-B=B1). S: S11, S16.
- [ ] **T3** W8-BE-1 · `POST /auth/admin-login` con `401` genérico constante — RUTA: inline (test-first). S: S7, S13.
- [ ] **T4** W8-BE-2 · `POST /stock-adjustments` con los tres motivos exactos y stock aditivo — RUTA: inline (test-first). S: S8, S13.
- [ ] **T5** W8-BE-3 · Pin del sobre único y de los `422` de `PATCH`, más los bordes que falten — RUTA: inline. S: S9, S13.
- [ ] **T6** W8-BE-4 · Revisión de logs sin PIN, fotos ni payloads, y canal de la alerta — RUTA: inline. S: S10.
- [ ] **T7** W8-DB-2 · Revisión del contrato de esquema contra el checklist (14 tablas, 2 JSONB, 6 tenders, concesiones, append-only) — RUTA: inline. S: S12.
- [ ] **T8** W8-FE-1 · Selector de rol y credencial Admin, primer token del cliente en `sessionStorage`; el Empleado sigue por PIN sin token — RUTA: inline (D-C=C2, D-D=D1). S: S2, S13.
- [ ] **T9** W8-FE-2 · Shell Admin de 240px con seis destinos y cierre de sesión client-side — RUTA: inline. S: S3.
- [ ] **T10** W8-FE-3 · Dashboard Admin sobre la proyección local más `GET /fiado-accounts` — RUTA: inline (depende de D-E). S: S4, S13, S15.
- [ ] **T11** W8-FE-4 · Cierre de caja del rol Admin (seis filas, fiado informacional, solo lectura) — RUTA: inline. S: S5.
- [ ] **T12** W8-FE-5 · Inventario, mermas y catálogo del rol Admin — RUTA: inline. S: S6, S13, S15.
- [ ] **T13** T-CLOSE · Verificación completa y criterios 1:1 — RUTA: inline. S: S13, S14.

## Tamaño de la entrega (riesgo de revisión, a la vista)

Trece tareas, seis pantallas nuevas y tres superficies HTTP: es el bloque más grande del plan, más
grande que el Sprint 7, que ya se entregó en seis PRs con `size:exception`. La entrega se hará con el
mismo formato de cadena apilada anclada en `develop`, y el orden propuesto es **backend y esquema
primero** (T2–T7), **acceso Admin** (T8–T9) y **pantallas de datos** (T10–T12): hasta que exista un
token no hay forma de probar el dashboard contra `GET /fiado-accounts`, y hasta que exista la merma no
hay forma de probar el semáforo de inventario moviendo stock.

## Log

- **L1** (solicitud del usuario, verbatim): «Me indicas de que trata, ve preparando todo para continuar con el siguiente Sprint y me dejas en claro las dudas abiertas respecto al diseño del software en este sprint.»
- **L2** (decisiones de diseño, respondidas por el usuario el 2026-10-31): **D-A = A1** (`master_password_hash TEXT NOT NULL` con backfill de hash argon2id aleatorio inutilizable; «tenant sin hash» deja de existir y la divergencia §11.4 #7 queda resuelta sin enmendar el contrato). **D-B = B1** (`cmd/maintenance admin-password`, contraseña por entrada estándar, sin ruta HTTP nueva). **D-C = C2** (token del dueño en `sessionStorage`: sobrevive recargas de la pestaña y muere al cerrarla). **D-D = D1** (solo el Administrador tiene sesión en línea; el Empleado opera offline por PIN, con la consecuencia declarada de que sin sesión de dueño la cola no drena).
- **L3** (decisiones por recomendación, sin objeción): **D-E** el dashboard cuenta la proyección del ledger local incluyendo los eventos pendientes, con el badge de sincronización existente diciendo qué parte no está confirmada; **D-F** la línea estructurada más la salida no-cero del job de particiones son el canal de operaciones, sin webhook externo.
