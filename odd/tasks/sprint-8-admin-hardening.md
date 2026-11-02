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

### D-G · Forma de `POST /stock-adjustments` (decidida al implementar, T4)

`docs/BACKEND.md` §4.14 fija el cuerpo pero no el acuse. Forma congelada: `200` con
`{ "adjustment": { id, tenant_id, product_id, qty_delta, source, reason, created_at, received_at,
sold_on, sale_id }, "product": { "product_id", "stock" } }` — el ajuste resuelto más el nivel de
stock que la base confirmó, la misma pieza que el acuse de la venta publica para su refresco de
producto. Reglas del borde: el delta es negativo y distinto de cero, `source` solo puede ser
`manual` (los deltas de venta y anulación los escribe el ledger, así que la superficie los RECHAZA
en lugar de aceptarlos), `reason` es uno de los tres textos exactos y el ajuste manual no lleva
enlace a venta.

### D-H · `user_id` del token de administrador (resuelta al implementar, T3)

§5.1 exige `user_id` en todo token y el dueño no tiene fila propia. El token lleva el `user_id` de
la persona cuyo teléfono resolvió la identidad, y la autoridad la da el hash del tenant: el
privilegio no viene del usuario, así que conocer la contraseña de empleado no convierte a nadie en
administrador. El camino completo está en `internal/identity/admin_login.go`.

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

## Tasks

- [ ] **T1** Documento del sprint + rama `feat/sprint-8-admin-hardening` — RUTA: inline. S: S1.
- [x] **T2** W8-DB-1 · Columna `master_password_hash` + concesión por columna a `pos_auth` + backfill con hash inutilizable, más el subcomando `admin-password` — RUTA: inline (D-A=A1, D-B=B1). S: S11, S16. Commit: `af96035`.
- [x] **T3** W8-BE-1 · `POST /auth/admin-login` con `401` genérico constante — RUTA: inline (test-first). S: S7, S13. Commit: `4776cc6`.
- [x] **T4** W8-BE-2 · `POST /stock-adjustments` con los tres motivos exactos y stock aditivo — RUTA: inline (test-first). S: S8, S13. Commit: `f5951ba`.
- [x] **T5** W8-BE-3 · Pin del sobre único y de los `422` de `PATCH`, más los bordes que falten — RUTA: inline. S: S9, S13. Commit: `76de2ec`.
- [x] **T6** W8-BE-4 · Revisión de logs sin PIN, fotos ni payloads, y canal de la alerta — RUTA: inline. S: S10. Commit: `76de2ec`.
- [x] **T7** W8-DB-2 · Revisión del contrato de esquema contra el checklist (14 tablas, 2 JSONB, 6 tenders, concesiones, append-only) — RUTA: inline. S: S12. Commit: `16892aa`.
- [x] **T8** W8-FE-1 · Selector de rol y credencial Admin, primer token del cliente en `sessionStorage`; el Empleado sigue por PIN sin token — RUTA: inline (D-C=C2, D-D=D1). S: S2, S13. Commit: `cdb2f6c`.
- [x] **T9** W8-FE-2 · Shell Admin de 240px con seis destinos y cierre de sesión client-side — RUTA: inline. S: S3. Commit: `58e8337`.
- [x] **T10** W8-FE-3 · Dashboard Admin sobre la proyección local más `GET /fiado-accounts` — RUTA: inline (D-E). S: S4, S13, S15. Commit: `381d6c0`.
- [x] **T11** W8-FE-4 · Cierre de caja del rol Admin (seis filas, fiado informacional, solo lectura) — RUTA: inline (verificación: la pantalla venía del Sprint 5). S: S5. Commit: `d9b686b`.
- [x] **T12** W8-FE-5 · Inventario, mermas y catálogo del rol Admin — RUTA: inline. S: S6, S13, S15. Commits: `d0dc6e0`, `7b183c5`.
- [x] **T13** T-CLOSE · Verificación completa y criterios 1:1 — RUTA: inline. S: S13, S14. Commit: este documento.

## Tamaño de la entrega (riesgo de revisión, a la vista)

Trece tareas, seis pantallas nuevas y tres superficies HTTP: es el bloque más grande del plan, más
grande que el Sprint 7, que ya se entregó en seis PRs con `size:exception`. La entrega se hará con el
mismo formato de cadena apilada anclada en `develop`, y el orden propuesto es **backend y esquema
primero** (T2–T7), **acceso Admin** (T8–T9) y **pantallas de datos** (T10–T12): hasta que exista un
token no hay forma de probar el dashboard contra `GET /fiado-accounts`, y hasta que exista la merma no
hay forma de probar el semáforo de inventario moviendo stock.

## Verificación del bloque T2–T7 (esquema, backend y endurecimiento)

Corrida del bloque (2026-10-31, contenedor PostgreSQL arriba):

- `go build ./... && go vet ./...` → exit 0.
- `go test ./... -count=1` → 15 paquetes `ok`, 0 fallos, exit 0.

Criterio por criterio de S13, en lo que este bloque cubre:

1. **Token de empleado sin `role`, token de Admin con `role:"admin"`.**
   `internal/identity/admin_login_test.go` (`TestAdminLoginIssuesAnAdminTokenWithTheTenantName`:
   el token emitido verifica con `IsAdmin()` verdadero y el tenant y el usuario del teléfono) y la
   prueba de integración `TestAdminLoginOverTheRealCredentials`. El camino de empleado sigue sin el
   claim, que ya estaba fijado en `internal/identity/token_test.go`.
2. **El `401` del login de administrador es genérico y constante.**
   `TestAdminLoginAnswersTheSameGeneric401ForEveryFailure` compara el cuerpo de los cuatro caminos
   —teléfono inexistente, contraseña incorrecta, tenant sin registro y hash ilegible— byte a byte, y
   la integración lo repite contra la base real incluyendo un tenant cuyo hash es el inutilizable de
   W8-DB-1.
3. **Los tres motivos de merma son exactamente los del contrato.**
   `internal/catalog/stock_adjustment_test.go` acepta los tres y rechaza `vencimiento` en minúsculas,
   `Dano en vitrina` sin tilde, `Robo`, `Vencido` y la ausencia del motivo, cada uno con su `field`.
4. **La merma no tiene captura de foto.** No hay campo en el cuerpo, ni columna en el esquema
   (delta D5, sin DDL). La prueba del cuerpo no conoce la foto porque el contrato no la tiene.
5. **Ningún rol de runtime borra ni actualiza el ledger.**
   `internal/platform/postgres/grants_contract_test.go` (matriz de concesiones, cero `DELETE` para
   roles de runtime salvo la retención de `pos_maintenance`) y
   `internal/platform/postgres/append_only_contract_test.go` (las cinco tablas rechazan `UPDATE` y
   `DELETE` con 55006). Ambos siguen verdes sin cambios: el bloque no tocó concesiones.
6. **W8-DB-1: la columna existe, el login se puede implementar y «tenant sin hash» está decidido.**
   `internal/identity/master_password_integration_test.go`: `NOT NULL` con valor por defecto
   inutilizable y aleatorio por fila, `pos_auth` leyendo exactamente tres columnas del registro
   (`created_at` da 42501), y el alta produciendo un argon2id que verifica solo con la contraseña
   elegida. La decisión A1 queda además fijada por la prueba de integración del login, que demuestra
   que un tenant recién creado no puede autenticarse.
7. **W8-DB-2: el checklist de arquitectura, entero.**
   `internal/platform/postgres/schema_contract_test.go` cubre 14 tablas, `tenders`/`sync_events`
   ausentes, particionado por rango, PK `(sold_on, sale_id)`, dinero `bigint`, sin `numeric`/`float`
   fuera de cantidades, JSONB en dos columnas, y —agregado en este bloque— los **seis** valores de
   `payment_method` idénticos en `sales` y `cash_close_counts` y las tablas globales sin
   `tenant_id`, sin RLS y sin política.
8. **S9: un solo sobre de error y los `422` del borde.**
   `internal/platform/httpx/envelope_audit_test.go` recorre las fuentes de producción y falla ante
   `http.Error`, un `WriteHeader` con estado de error o un literal de error armado a mano, y exige
   que `cmd/api` monte las rutas por `httpx.NewRouter`. Los `422` de `PATCH /products` por `stock` y
   por intento de quitar un campo ya estaban fijados en `internal/catalog/product_test.go`
   (Sprint 3) y siguen verdes.
9. **S10: los logs no llevan credenciales, fotos ni payloads; la alerta tiene canal.**
   `TestAdminLoginNeverLogsTheCredential`, `TestAdminPasswordNeverLogsTheCredential` (W8-DB-1) y
   `TestStockAdjustmentNeverLogsThePayload`. La alerta de particiones queda en el canal decidido
   (D-F): la línea `event=partitions.alert` con `stranded_rows` y el comando de remediación, más la
   salida no-cero, y sin ningún dato del tenant.

Pendiente de este sprint (T8–T13): las seis pantallas del rol Admin y el cierre. **No** hay endpoint
de reporte ni de agregados: el dashboard se alimenta solo de la proyección local más
`GET /fiado-accounts`, así que este bloque no agregó ninguna superficie de datos.

## Verificación del bloque T8–T9 (acceso Admin y shell)

Corrida del bloque (2026-11-01, sin base de datos: el cliente no la necesita):

- `cd web && npx tsc --noEmit` → exit 0.
- `cd web && npx vitest run` → **408 pruebas en 44 archivos**, 0 fallos.

Criterio por criterio, en lo que este bloque cubre:

1. **S2 · Las dos pantallas renderizan contra `POST /auth/admin-login` y contra `devices.pin_hash`,
   y no hay datos del tenant antes de autenticar.**
   `web/src/screens/RoleSelectorScreen.test.tsx` (dos tarjetas, cero campos de credencial, cero
   datos del local), `web/src/screens/AdminCredentialScreen.test.tsx` (el cuerpo es exactamente
   `{ phone, master_password }`, sin `Authorization`, y la sesión queda en `sessionStorage`), y
   `web/src/App.test.tsx` (el camino del empleado sigue entrando por PIN, sin token).
2. **S2 · El `401` no enumera.** La pantalla muestra una sola frase —«Teléfono o contraseña
   incorrectos»— y una prueba niega las variantes que insinuarían la causa; un fallo de red se
   dice como fallo de red, no como credencial inválida.
3. **S3 · El shell navega los seis destinos.**
   `web/src/components/AdminShell.test.tsx` verifica las seis etiquetas en el orden del diseño,
   que «Ajustes de dispositivos» no existe (delta D3), el bloque de marca con `tenant.name`, el
   badge `Administrador` y que la navegación sigue funcionando con la red caída.
4. **S3 · El cierre de sesión es del lado del cliente.**
   La misma prueba guarda una sesión, pulsa el botón y verifica dos cosas: la fila de
   `sessionStorage` desaparece y **no se llamó a `fetch`** —no hay endpoint de logout y no se
   inventa uno (`ARCHITECTURE.md:122-135`)—.
5. **D-C (C2) · La sesión vive en la pestaña.** `web/src/auth/accessSession.test.ts` comprueba que
   se escribe en `sessionStorage` y **no** en `localStorage`, que la lectura es fail-closed con
   JSON roto, fila sin token o almacenamiento bloqueado, y que el cierre la borra. `App.test.tsx`
   verifica la consecuencia práctica: una recarga con la sesión guardada abre el shell sin volver
   a pedir la contraseña maestra.
6. **D-D (D1) · Solo el Administrador tiene sesión en línea.** El camino del empleado no cambia:
   PIN contra `devices.pin_hash`, sin token, y las pruebas existentes de la venta de mostrador y
   del arqueo siguen verdes después de mover la entrada al selector de rol.

Pendiente de este sprint (T10–T12): el dashboard, el arqueo del rol Admin y el inventario/mermas/
catálogo. Los cuatro destinos que todavía no tienen módulo renderizan un panel honesto que nombra
el módulo y dice que aún no está disponible; ninguno se inventó ni se dejó en blanco.

## Verificación del bloque T10–T12 (pantallas de datos del rol Admin)

Corrida del bloque (2026-11-01):

- `cd web && npx tsc --noEmit` → exit 0.
- `cd web && npx vitest run` → **464 pruebas en 51 archivos**, 0 fallos.
- `go build ./... && go vet ./...` → exit 0 (el bloque no tocó Go).
- `go test ./... -count=1` → **15 paquetes `ok`, 0 fallos** (corrida del cierre, con PostgreSQL arriba).

Criterio por criterio:

1. **S4 · El dashboard renderiza solo la proyección del ledger local más `GET /fiado-accounts`.**
   `web/src/dashboard/ledgerProjection.test.ts` (13 pruebas de la proyección) y
   `web/src/screens/AdminDashboardScreen.test.tsx` (8 pruebas de la pantalla), incluida la que
   enumera las lecturas de red de la pantalla y exige que sean exactamente `['/fiado-accounts']`:
   ningún endpoint de reporte existe ni se llama. `docs/BACKEND.md` §4.15 sigue siendo cierto.
2. **S4 · La deuda de fiado no inventa un número.** Sin sesión o sin red la tarjeta dice «pendiente»
   y no muestra un importe: cero afirmaría que nadie debe nada, que es distinto de no saberlo.
3. **S5 · El arqueo del rol Admin son las seis filas con el fiado bloqueado en cero y de solo
   lectura cuando el cierre existe.** La pantalla venía del Sprint 5 con esas reglas; el bloque
   agrega la variante Admin en el shell: seis filas con la sesión del dueño, sin el aviso de «sin
   sesión en línea», y el formulario abierto en solo lectura cuando el día ya tiene cierre, sin
   botón de envío (F37).
4. **S6 · La merma encola `POST /stock-adjustments` con un motivo de la lista exacta y sin foto.**
   `web/src/stock/wasteQueue.test.ts` (cuerpo `source: manual`, `qty_delta` negativo en texto,
   rechazo de cantidad y motivo inválidos) y `web/src/screens/AdminWasteScreen.test.tsx` (tres
   motivos exactos, confirmación destructiva antes de escribir, ausencia de todo control de foto,
   y cero llamadas a `fetch`: funciona sin red).
5. **S6 · La edición de producto usa `PATCH` y es solo en línea.**
   `web/src/screens/AdminCatalogScreen.test.tsx`: sin red el guardado está deshabilitado con su
   motivo visible y no se encola nada; con sesión manda `PATCH /products/{id}` con el token y un
   cuerpo aditivo que nunca lleva `stock`.
6. **S6 · La tabla de SKUs y la adopción desde el catálogo global.**
   La misma prueba cubre las columnas de la tabla y el puntero `global_product_id` cuando la
   mediación encuentra el GTIN; `web/src/catalog/productQueue.test.ts` fija que el alta no lleva
   `stock`, que crea la fila local con stock cero y que la adopción viaja como puntero.

Pendiente de este bloque: nada del sprint. T13 cierra el sprint completo.

## Cierre del sprint (T13)

Los trece objetivos del sprint quedan entregados. **La corrida final completa** se registra en la
sección siguiente; los criterios de aceptación del plan (S13), uno por uno, sobre todo el sprint:

1. **El dashboard Admin renderiza solo la proyección del ledger local más `GET /fiado-accounts`;
   no se agrega ningún endpoint de reporte ni de agregados.**
   Evidencia: la prueba de la pantalla enumera las lecturas de red y solo encuentra
   `/fiado-accounts`; `cmd/api/main.go` no ganó ninguna ruta de reporte en el sprint.
2. **Un token de empleado no lleva el claim `role` y uno de Admin sí; la ausencia significa
   empleado.** Evidencia: `internal/identity/admin_login_test.go` (el token emitido tiene
   `IsAdmin()` verdadero) y `internal/identity/token_test.go` (el camino de empleado sigue sin el
   claim, y un valor distinto de `admin` no puede circular).
3. **El `401` del login de administrador es genérico y constante.** Evidencia:
   `TestAdminLoginAnswersTheSameGeneric401ForEveryFailure` compara el cuerpo de los cuatro caminos
   byte a byte, y la prueba de integración repite la comparación contra la base real.
4. **Los tres motivos de merma son exactamente `Vencimiento`, `Rotura en transporte` y
   `Daño en vitrina`.** Evidencia: `internal/catalog/stock_adjustment_test.go` (el borde los acepta
   y rechaza las variantes) y `web/src/screens/AdminWasteScreen.test.tsx` (la pantalla ofrece
   exactamente esos tres).
5. **La merma no tiene captura de foto en esta versión.** Evidencia: ningún campo en el cuerpo del
   servidor ni del cliente, y una prueba que exige la ausencia de todo control de foto.
6. **Ningún rol de runtime puede borrar ni actualizar una fila del ledger.** Evidencia:
   `internal/platform/postgres/grants_contract_test.go` (matriz de concesiones) y
   `append_only_contract_test.go` (las cinco tablas rechazan `UPDATE` y `DELETE` con 55006); el
   sprint no cambió concesiones, y la suite sigue verde.

Definition of Done del plan: el dueño entra con la contraseña maestra del tenant y ve sus propios
números sin endpoint de reporte; el registro de merma escribe un delta firmado con un motivo de la
lista exacta; el checklist de esquema se verificó entero sobre las 14 tablas (T7).

## Log

- **L4** (hueco de contrato resuelto en el login, no inventado): §5.1 exige `user_id` en TODO token, y el dueño no tiene fila propia. La resolución es la que el propio contrato sugiere: el teléfono resuelve primero un `users` (identidad y tenant), y la autoridad administrativa la da la contraseña maestra del tenant. El `user_id` del token es el de la persona cuyo teléfono se usó, así que conocer la contraseña de empleado no convierte a nadie en administrador.
- **L5** (corrección medida al escribir la migración): los campos base64 del PHC tienen que ser UN solo cuanto canónico cada uno. Pegar dos trozos recortados de relleno desplaza la alineación de bits y el decodificador estricto de Go rechaza la cadena como inválida; la sal y la clave son entonces una sola tirada de 32 bytes por campo.
- **L6** (corrección medida en la revisión de esquema): filtrar `pg_constraint` por nombre devuelve una fila por partición de `sales`, porque cada partición clona los CHECK del padre. La consulta del contrato une con `pg_class` para quedarse con las dos tablas reales.
- **L7** (límite declarado, no omisión): `uom` NO es un conjunto acotado en el contrato —`docs/DATABASE.md` §4 da `'unit'` y `'kg'` como ejemplo, no como lista cerrada, y no hay CHECK en el esquema—. No se inventa una lista para "validar conjuntos acotados": los conjuntos que el contrato SÍ fija son `payment_method` (seis), `source` (tres), los tres motivos de merma y `plan` (sin endpoint de escritura). Todos los que reciben entrada del cliente están validados en el borde.

- **L1** (solicitud del usuario, verbatim): «Me indicas de que trata, ve preparando todo para continuar con el siguiente Sprint y me dejas en claro las dudas abiertas respecto al diseño del software en este sprint.»
- **L2** (decisiones de diseño, respondidas por el usuario el 2026-10-31): **D-A = A1** (`master_password_hash TEXT NOT NULL` con backfill de hash argon2id aleatorio inutilizable; «tenant sin hash» deja de existir y la divergencia §11.4 #7 queda resuelta sin enmendar el contrato). **D-B = B1** (`cmd/maintenance admin-password`, contraseña por entrada estándar, sin ruta HTTP nueva). **D-C = C2** (token del dueño en `sessionStorage`: sobrevive recargas de la pestaña y muere al cerrarla). **D-D = D1** (solo el Administrador tiene sesión en línea; el Empleado opera offline por PIN, con la consecuencia declarada de que sin sesión de dueño la cola no drena).
- **L3** (decisiones por recomendación, sin objeción): **D-E** el dashboard cuenta la proyección del ledger local incluyendo los eventos pendientes, con el badge de sincronización existente diciendo qué parte no está confirmada; **D-F** la línea estructurada más la salida no-cero del job de particiones son el canal de operaciones, sin webhook externo.
- **L8** (límite declarado del shell): por debajo del breakpoint de escritorio la sidebar se apila y se pliega tras un botón, en lugar de convertirse en el drawer de Radix de `docs/DESIGN.md` §6.3. La suite Admin es de escritorio 1440x900 (§7.1) y el shell móvil sigue siendo el del empleado: ninguna pantalla de esta versión necesita ese drawer. Se declara acá en lugar de omitirlo.
- **L9** (una sola `h1` por pantalla): el wordmark y el badge de conexión viven en la cabecera del shell, así que el selector de rol lleva su título en `h2` y el shell del administrador usa el nombre del tenant como `h1` de su bloque de marca. Dos `h1` con el mismo texto habrían roto la jerarquía que las pruebas de accesibilidad del shell ya fijan.
- **L10** (alcance de T9, declarado): el shell monta los seis destinos, pero cuatro de ellos —Dashboard, Inventario, Mermas y Catálogo— todavía no tienen módulo en esta unidad. Renderizan un panel que nombra el módulo y dice que aún no está disponible; no se inventó una superficie de datos ni se dejó una pantalla en blanco. Cada uno se sustituye por su módulo en T10–T12.
- **L11** (efecto de la sesión): al pasar el token al destino `Cuentas y Fiados`, la restauración del fiado deja de saltearse por falta de sesión. `restoreFiadoAccounts` ya existía; lo que faltaba era el token, y una prueba del shell verifica que el `GET /fiado-accounts` sale con `Bearer` de la sesión.
- **L12** (umbrales del semáforo, decisión D-I): `docs/FRONTEND.md:52` llama «spec-fixed» a los umbrales del semáforo de stock, pero **ningún documento publica el número** —`docs/DESIGN.md:213` da solo la regla cualitativa (normal, bajo, quiebre)—. Se congela la propuesta mínima y se declara: quiebre en cero o menos, bajo hasta cinco unidades, normal por encima; un stock negativo es quiebre. Queda en el código y en la prueba, no en la prosa.
- **L13** (la merma mueve la proyección local): al encolarse, la merma descuenta `products.stock` del dispositivo con redondeo a tres decimales. Un semáforo que sigue mostrando mercadería ya dada de baja es peor que no tener semáforo; el acuse y el pull del servidor siguen siendo la verdad final. La venta no hacía esto porque su acuse trae el nivel del servidor en la misma respuesta.
- **L14** (orden de drenaje del alta): `product` entra a la cola y drena **primero** (§7.8, catálogo antes que ledger): un producto creado en el mismo drenaje tiene que existir antes que la venta que lo referencia. El orden previo —`sale` → `void` → `fiado_movement` → `cash_close` → `stock_adjustment`— se conserva relativo.
- **L15** (validación del formulario de merma): el botón no se deshabilita con el formulario incompleto; se valida al pulsar y el aviso nombra lo que falta —cantidad o motivo—. Un botón deshabilitado sin explicación deja al operador sin saber qué le pide la pantalla, que es exactamente el tipo de callejón sin salida que el contrato de estados quiere evitar.
