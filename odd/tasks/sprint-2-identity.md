# sprint-2-identity — Identidad y aislamiento de tenant (Sprint 2, Semana 2)

Autoridad de secuenciación: `SPRINTS.md` §7, bloque "Sprint 2 (Semana 2)". Autoridad de
esquema: `docs/DATABASE.md`. Autoridad de arquitectura: `ARCHITECTURE.md`. Autoridad de
contrato HTTP: `docs/BACKEND.md` §4, §5.

## Objetivo

Que nadie venda sin tenant y que nadie lea los datos de otro tenant: `FORCE RLS` sobre las
once tablas de tenant, matriz de cinco roles, `POST /auth/login` que emite el JWT con
`tenant_id` + `user_id`, el helper de transacción que fija el contexto de tenant
(*pooler-safe*), `POST /auth/device-bind` con PIN argon2id y ventana offline de 7 días o
más, el namespace obligatorio de Redis, y las dos unidades de cliente (pantalla de acceso
del Empleado y estado de conectividad observable).

## Problema y por qué

El Sprint 1 dejó el esquema con las 14 tablas y ambos runners en verde, pero **sin ninguna
barrera de aislamiento**: los siete archivos de migración no crean políticas ni roles
(`migrations/20261015000001_identity.sql:10-11` declara que el RLS pertenece al Sprint 2).
Hoy cualquier consulta ve las filas de todos los tenants, y el proceso se conecta con el
rol de desarrollo (`internal/platform/postgres/postgres.go:13-14`). Sin este sprint, el
tenant es una convención de código y no un invariante de la base; todo lo que sigue —
catálogo, venta, cierre, fiado — heredaría ese agujero.

## Alcance autorizado

Ocho tareas del bloque Sprint 2 (Semana 2), en el orden por capa que fija `SPRINTS.md` §2:
Base de Datos → Backend → Frontend. Rama de trabajo: `feat/sprint-2-identity`.

| ID | Capa | Ruta de trabajo | Trigger de delegación |
| :--- | :--- | :--- | :--- |
| W2-DB-2 | DB | delegado (writer DB) | 2+ archivos no triviales (2 migraciones + 2 tests) |
| W2-DB-1 | DB | delegado (writer DB, misma sesión) | mismo contrato que W2-DB-2 |
| W2-BE-1 | BE | delegado (writer BE) | preparación de lectura + escritura en 2+ archivos |
| W2-BE-2 | BE | delegado (writer BE, misma sesión) | mismo contrato que W2-BE-1 |
| W2-BE-3 | BE | delegado (writer BE-2) | preparación de lectura + escritura en 2+ archivos |
| W2-BE-4 | BE | delegado (writer BE-2, misma sesión) | mismo contrato que W2-BE-3 |
| W2-FE-1 | FE | delegado (writer FE) | 2+ archivos no triviales (numpad, pantalla, ventana offline, tests) |
| W2-FE-2 | FE | delegado (writer FE, misma sesión) | mismo contrato que W2-FE-1 |

## Fuera de alcance (explícito)

- El protocolo de claim de idempotencia (`W4-BE-2`) y `POST /sales`: llegan en el Sprint 4
  (Semana 4). `POST /auth/device-bind` es idempotente por *upsert*, no por clave (decisión
  D5).
- Ningún endpoint nuevo: los 13 caminos de `docs/BACKEND.md` §4.15 agotan la superficie
  HTTP del MVP; no hay `GET /me`, ni `/auth/logout`, ni endpoints de reporte.
- Alta de tenant por HTTP: el camino de *bootstrap* de `docs/DATABASE.md` §5.4 se entrega
  como capacidad de base (concesiones + política) y se demuestra por test de integración;
  su superficie HTTP pertenece al alta del piloto (`W9-FE-1`).
- Refresco de la ventana offline por petición: no existe (decisión D8).
- Cliente Redis real: no hay instancia ni dependencia en el `go.mod` (decisión D7).

## Convenciones

- Identificadores, rutas, códigos y mensajes de commit en inglés; comentarios y documentos
  en español neutro profesional (`odd/tasks/sprint-1-foundations.md:38`).
- Staging siempre por ruta explícita; `git add -A` está prohibido. La rama base es
  `origin/develop`, cuyo `.gitignore` **no** ignora el tooling local (`.codegraph/`,
  `.agent/`, `.engram/`, `.impeccable/`): esos directorios aparecen como *untracked* y no
  deben entrar en ningún commit.
- TDD estricto: el test que falla primero viaja en la misma unidad de trabajo que el
  comportamiento, y ningún número de cobertura se reclama.

## Decisiones tomadas (con evidencia)

**D1. Rama base.** `feat/sprint-2-identity` sale de `origin/develop` (`a285fcd`). La rama
`fix/contrast-and-test-hardening` tiene 7 commits propios (`72c0a65`…`ee0208d`) que **no**
están en `develop`: el PR #7 se mergeó sobre `8ecfa6a`, el estado anterior. Esos commits
quedan intactos y su merge es decisión del usuario; no se arrastran a este sprint.

**D2. Tres DSN, tres roles.** `DATABASE_URL` conserva su significado actual (rol de
migración/dueño: `cmd/migrate`, tests de integración). Se agregan `DATABASE_APP_URL`
(`pos_app`) y `DATABASE_AUTH_URL` (`pos_auth`) para el runtime de `cmd/api`, con el mismo
puerto y la contraseña de desarrollo ya presente en el repositorio
(`internal/platform/config/config.go:27`). Los roles son del clúster: la migración los crea
de forma idempotente (`IF NOT EXISTS`) y el `Down` revoca en esta base y libera el rol solo
si nadie más lo usa, porque un rol con concesiones en otra base no se puede eliminar.

**D3. `INSERT ... RETURNING` sobre `tenants` es imposible sin `SELECT` — gana F14.**
`docs/DATABASE.md` §5.4 justifica el readback del alta con `INSERT ... RETURNING` "no
necesita SELECT sobre `tenants`", pero PostgreSQL **exige privilegio `SELECT` sobre las
columnas que devuelve un `RETURNING`**. Con `pos_app` sin `SELECT` sobre `tenants` (§5.5,
F14) la sentencia de §5.4 falla. La invariante vinculante es F14 ("`pos_app` SELECT es
exactamente las once tablas; sin `tenants`"), y §2.2 ya manda que el UUID lo genere la
aplicación: el alta inserta el `tenants` con el id generado por la aplicación, fija el GUC
a ese id e inserta el `users`, **sin `RETURNING`**. Se prueba con una sonda que documenta
el rechazo (el test que falla por diseño), y se registra como divergencia abierta de
`docs/DATABASE.md` §5.4.

**D4. `set_config(..., true)` como forma parametrizable de `SET LOCAL`.** La primera
sentencia de cada transacción de tenant es `select set_config('app.current_tenant_id', $1,
true)`: `SET LOCAL` no acepta marcadores de posición, y el tercer argumento liga el valor a
la transacción, que es lo que hace la seguridad frente al pooler. Las políticas siguen
leyendo `current_setting('app.current_tenant_id')::uuid`, sin cambios. La superficie se
cierra en el tipo, no en una convención: el pool se envuelve y su único camino de datos es
el helper, así que "ninguna consulta corre fuera del helper" es estructural (W2-BE-2).

**D5. `device-bind` idempotente por *upsert*, no por clave.** `ARCHITECTURE.md:125` marca
la ruta como idempotente con `Idempotency-Key`, pero el protocolo de claim es `W4-BE-2`
(`SPRINTS.md` §7). Se implementa la idempotencia natural: reenviar el mismo `device_id`
actualiza `pin_hash` y extiende `offline_session_valid_until` en una sola fila — repetir el
bind no crea un segundo dispositivo ni duplica nada. Divergencia registrada para el
Sprint 4.

**D6. El cliente nunca recibe `pin_hash` (propuesta).** `devices.pin_hash` es argon2id y
solo del servidor. Para que el desbloqueo offline funcione dentro de la ventana, el cliente
deriva en el momento del bind un verificador local (PBKDF2-SHA256 vía WebCrypto sobre un
*salt* por dispositivo) y lo persiste junto a `offline_session_valid_until`; el PIN en claro
nunca se guarda. Es una decisión de implementación que el contrato no fija, y se marca
`(propuesta)` en el código.

**D7. W2-BE-4 se entrega como helper de namespace con revisión verificable.** No hay
cliente Redis en `go.mod` ni instancia en `docker-compose.yml`. El entregable es el
constructor único de claves `tenant:{id}:...` (con tenant vacío rechazado) más un test que
recorre fuentes y falla ante una clave sin prefijo, probado por mutación sobre un
directorio temporal. Se registra que hoy no existe ninguna clave de Redis fuera del helper.

**D8. Refresco de la ventana offline en el bind.** "Se refresca en cada autenticación en
línea" (`docs/DATABASE.md` §4 `devices`) se cumple en la única autenticación en línea que
conoce el dispositivo en el MVP: `POST /auth/device-bind`. Un refresco por petición sería
una escritura por request sin contrato que lo pida.

**D9. Lenguaje de comentarios.** El código nuevo usa comentarios en español neutro, según la
convención registrada. Las siete migraciones del Sprint 1 quedan en inglés: es deuda de
normalización preexistente, no la resuelve este sprint.

## Tareas

### Base de datos

- [ ] **W2-DB-2** — Cinco roles (`pos_app`, `pos_auth`, `pos_platform`, `pos_maintenance`,
  `owner/migration`) y la matriz de concesiones de §5.5, con los dos caminos de bootstrap
  pre-auth: alta de tenant + usuario, y la lectura de `users` por `pos_auth` con su política
  `FOR SELECT TO pos_auth USING (true)`.
  - Entregable: `pos_app` no tiene `SELECT` sobre `tenants` ni `DELETE` sobre ninguna tabla
    de negocio; ningún rol de runtime tiene `BYPASSRLS`; la matriz se verifica columna por
    columna contra `information_schema.role_table_grants`.
- [ ] **W2-DB-1** — `FORCE RLS` y las políticas `USING` / `WITH CHECK` sobre las once tablas
  de tenant, con el predicado `tenant_id = current_setting('app.current_tenant_id')::uuid`.
  - Entregable: sin `SET LOCAL` la consulta devuelve cero filas y toda escritura con otro
    `tenant_id` se rechaza (fail-closed), probado *fail-then-pass*.

### Backend

- [ ] **W2-BE-1** — `POST /auth/login` que emite un JWT con `tenant_id` y `user_id` y
  **sin** claim `role`, leyendo `users` por el rol dedicado `pos_auth`.
  - Entregable: una petición sin tenant en el cuerpo o en un encabezado resuelve el tenant
    únicamente desde el token; credenciales inválidas devuelven `401` genérico.
- [ ] **W2-BE-2** — Helper de transacción que fija el contexto de tenant como primera
  sentencia y lo deja resetear al confirmar, con la superficie cerrada en el tipo.
  - Entregable: dos transacciones consecutivas sobre la **misma conexión del pool** no
    filtran el contexto de tenant entre clientes (pooler *transaction mode*).
- [ ] **W2-BE-3** — `POST /auth/device-bind` con `devices.pin_hash` en argon2id y
  `offline_session_valid_until` fijado a la hora de bind más **7 días** como mínimo.
  - Entregable: un access token expirado con un PIN válido todavía desbloquea el POS
    offline dentro de la ventana; un `tenant_id` en el cuerpo se ignora.
- [ ] **W2-BE-4** — Namespacing obligatorio de todas las claves de Redis como
  `tenant:{id}:...`.
  - Entregable: cualquier clave sin prefijo de tenant falla la revisión por grep.

### Frontend

- [ ] **W2-FE-1** — Pantalla de acceso del Empleado: display de PIN y reuso del numpad
  ergonómico de 56px (`docs/DESIGN.md:143-145`).
  - Entregable: el desbloqueo offline funciona dentro de la ventana y se rechaza, **sin**
    bloquear el intento en línea posterior, una vez pasada la expiración.
- [ ] **W2-FE-2** — Estado de conectividad observable: `useNetworkStatus` y badge de
  cabecera.
  - Entregable: el badge alterna entre en línea y modo offline sin interrumpir ni bloquear
    la operación.

## Criterios de aceptación del sprint (`SPRINTS.md` §7, Sprint 2)

1. Ningún endpoint acepta `tenant_id` desde el cuerpo ni desde un encabezado; el tenant
   sale solo del JWT (`ARCHITECTURE.md:23`, `:124`).
2. El aislamiento se prueba *fail-then-pass*: antes de aplicar RLS la consulta cross-tenant
   devuelve filas y el test falla por diseño; después devuelve cero filas y pasa
   (`docs/DATABASE.md` §5.7).
3. Un access token expirado con un PIN válido todavía desbloquea el POS offline durante al
   menos 7 días (`docs/DATABASE.md` §4 `devices`).
4. El PIN y la sesión offline son estado del dispositivo, no del usuario.
5. Ninguna clave de Redis carece del prefijo `tenant:{id}:` (`ARCHITECTURE.md:20`, `:86`).
6. `pos_app` no tiene `SELECT` sobre `tenants` ni `DELETE` sobre ninguna tabla de negocio;
   ningún rol de runtime tiene `BYPASSRLS` (`docs/DATABASE.md` §5.5).

## Verificación (comandos exactos)

```
docker compose up -d
go build ./...
gofmt -l .            # sin salida
go test ./...         # incluye los tests de integración contra PostgreSQL
cd web; npm run typecheck; npx vitest run
```

## Presupuesto de entrega

Tareas: 8. Cambio autorado previsto: **1.400–1.900 líneas** (migraciones + Go + tests de
integración + cliente). Supera el presupuesto de revisión de ~400 líneas por PR, así que la
estrategia de entrega se decidió con el usuario antes del primer commit (estrategia por
defecto: `ask-on-risk`).

**Decisión del usuario (2026-10-16): `feature-branch-chain`.** El tracker es
`feat/sprint-2-identity` y cada corte es una rama hija apilada sobre el corte anterior; el
PR de cada corte apunta a la rama del corte anterior, así el diff de revisión queda
enfocado, y solo el tracker se mergea (a `develop`).

| Corte | Rama hija | PR apunta a | Commits que contiene | Tareas |
| :--- | :--- | :--- | :--- | :--- |
| 1 | `feat/sprint-2-db` | `feat/sprint-2-identity` | roles y concesiones; RLS y políticas | W2-DB-2, W2-DB-1 |
| 2 | `feat/sprint-2-be-auth` | `feat/sprint-2-db` | login y JWT; helper de transacción | W2-BE-1, W2-BE-2 |
| 3 | `feat/sprint-2-be-sync` | `feat/sprint-2-be-auth` | device-bind; namespace de Redis | W2-BE-3, W2-BE-4 |
| 4 | `feat/sprint-2-fe` | `feat/sprint-2-be-sync` | acceso del Empleado; conectividad | W2-FE-1, W2-FE-2 |

La cadena es lineal: cada rama hija sale del tip de la anterior, así que el merge final del
último corte sobre el tracker es un *fast-forward*. Push, PR y merge siguen siendo decisión
del usuario.

## Progreso y evidencia

- [x] Exploración: contrato leído (`docs/DATABASE.md` §2, §4 identidad, §5; `docs/BACKEND.md`
  §4.1-4.4, §5, §7.4, §10.2; `SPRINTS.md` §7 Sprint 2 y §8), código actual leído vía
  CodeGraph, memoria de Engram recuperada.
- [x] Línea base verde medida antes de escribir: `go test ./...` → `ok` en los 4 paquetes con
  tests (contenedor PostgreSQL arriba, 39 h); `npx vitest run` → 7 archivos, **94 tests**
  en verde.
- [ ] Implementación (pendiente del corte de entrega).

## Siguiente paso

Confirmar la estrategia de entrega (cortes de PR) y ejecutar las cuatro capas en orden
DB → BE → FE con una unidad de trabajo por tarea.
