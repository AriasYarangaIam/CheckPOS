# sprint-2-identity — Identidad y aislamiento de tenant (Sprint 2, Semana 2)

Autoridad de secuenciación: `SPRINTS.md` §7, bloque "Sprint 2 (Semana 2)". Autoridad de
esquema: `docs/DATABASE.md`. Autoridad de arquitectura: `ARCHITECTURE.md`. Autoridad de
contrato HTTP: `docs/BACKEND.md` §4, §5.

Rama tracker: `feat/sprint-2-identity`. Base: `origin/develop` (`5769cbd`).
Estado: **cerrado** — 8/8 tareas, 278 corridas de test de Go y 163 tests de vitest en verde,
cero skips.

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
Antes de este sprint cualquier consulta veía las filas de todos los tenants, y el proceso se
conectaba con el rol de desarrollo. Sin este sprint, el tenant es una convención de código y
no un invariante de la base; todo lo que sigue — catálogo, venta, cierre, fiado — heredaría
ese agujero. Medido antes: la base del sprint tenía 86 tests de vitest; hoy tiene 163.

## Alcance autorizado

Ocho tareas del bloque Sprint 2 (Semana 2), en el orden por capa que fija `SPRINTS.md` §2:
Base de Datos → Backend → Frontend. Rama de trabajo: `feat/sprint-2-identity`, con un corte
por capa.

## Fuera de alcance (explícito)

- El protocolo de claim de idempotencia (`W4-BE-2`) y `POST /sales`: llegan en el Sprint 4
  (Semana 4). `POST /auth/device-bind` es idempotente por *upsert*, no por clave (D5).
- Ningún endpoint nuevo: los 13 caminos de `docs/BACKEND.md` §4.15 agotan la superficie
  HTTP del MVP; no hay `GET /me`, ni `/auth/logout`, ni endpoints de reporte.
- Alta de tenant por HTTP: el camino de *bootstrap* de `docs/DATABASE.md` §5.4 se entrega
  como capacidad de base (concesiones + política) y se demuestra por test de integración;
  su superficie HTTP pertenece al alta del piloto (`W9-FE-1`).
- Refresco de la ventana offline por petición: no existe (D8).
- Cliente Redis real: no hay instancia ni dependencia en el `go.mod` (D7).
- La UI de acceso en línea del Empleado (selector de rol + credencial) es `W8-FE-1`.

## Convenciones

- Identificadores, rutas, códigos y mensajes de commit en inglés; comentarios y documentos
  en español neutro profesional (`odd/tasks/sprint-1-foundations.md:38`).
- Staging siempre por ruta explícita; `git add -A` está prohibido. **Corrección de un hecho
  registrado antes:** la base de este sprint **sí** ignora el tooling local. Cuando se creó
  la rama, `origin/develop` estaba en `a285fcd` (sin las entradas de `.gitignore`); el PR #9
  las mergeó durante el sprint, así que hoy `git status` sale limpio y el aviso de "cuatro
  directorios untracked" quedó obsoleto.
- TDD estricto: el test que falla primero viaja en la misma unidad de trabajo que el
  comportamiento; ningún número de cobertura se reclama.

## Decisiones tomadas (con evidencia)

**D1. Rama base.** `feat/sprint-2-identity` salió de `origin/develop` (`a285fcd`). A mitad
del sprint `origin/develop` avanzó a `5769cbd` (PR #9, el merge de
`fix/contrast-and-test-hardening`), así que la cadena se rebasó con
`git rebase --update-refs` para no revertir ese trabajo: la comparación final contra
`origin/develop` es de 63 archivos, +10.700/−56, sin borrados ajenos.

**D2. Tres DSN, tres roles.** `DATABASE_URL` conserva su significado (rol de migración)
y el runtime usa `DATABASE_APP_URL` (`pos_app`) y `DATABASE_AUTH_URL` (`pos_auth`). Los
roles son del clúster: la migración los crea de forma idempotente y el `Down` revoca en esta
base y libera el rol solo si nadie más lo usa.

**D3. `INSERT ... RETURNING` sobre `tenants` es imposible sin `SELECT` — gana F14.**
`docs/DATABASE.md` §5.4 justifica el readback del alta con `INSERT ... RETURNING` "no
necesita SELECT sobre `tenants`", pero PostgreSQL **exige `SELECT` sobre las columnas que
devuelve un `RETURNING`**: medido, `pos_app` recibe `42501 permission denied for table
tenants`. La invariante vinculante es F14 (§5.5, §5.6) y §2.2 ya manda que el UUID lo genere
la aplicación, así que el alta inserta el `tenants` con el id generado por la aplicación,
fija el GUC a ese id e inserta el `users`, sin `RETURNING`. La sonda que documenta el
rechazo vive en `grants_contract_test.go`.

**D4. `set_config(..., true)` como forma parametrizable de `SET LOCAL`.** `SET LOCAL` no
acepta marcadores de posición; el tercer argumento liga el valor a la transacción, que es lo
que hace la seguridad frente al pooler. Las políticas siguen leyendo
`current_setting('app.current_tenant_id')::uuid`, literal de §5.3. La superficie se cierra en
el tipo (el único camino de datos exportado de `TenantPool` es `WithTenant`, con la excepción
pre-auth de §5.4 nombrada en el comentario).

**D5. `device-bind` idempotente por *upsert*, no por clave.** `ARCHITECTURE.md:125` marca la
ruta como idempotente con `Idempotency-Key`, pero el protocolo de claim es `W4-BE-2`. Se
implementó la idempotencia natural: reenviar el mismo `device_id` actualiza `pin_hash` y
extiende la ventana en una sola fila; un `device_id` de otro tenant no toca nada y devuelve
`404`. Divergencia registrada para el Sprint 4.

**D6. El cliente nunca recibe `pin_hash`.** `devices.pin_hash` es argon2id y solo del
servidor; el cliente deriva en el momento del bind un verificador local (PBKDF2-SHA256 vía
WebCrypto sobre un *salt* por dispositivo) y persiste sal + verificador + la ventana. El PIN
en claro no llega al DOM ni a ningún almacén, y el código declara el límite honesto: 10⁴
candidatos significan que el verificador local retrasa pero no impide una conjetura offline;
los límites reales son el argon2id del servidor y la ventana de 7 días.

**D7. W2-BE-4 se entregó como helper de namespace con revisión verificable.** No hay cliente
Redis ni instancia. El entregable es el constructor único de claves `tenant:{id}:...` más un
escáner de fuentes que falla ante una clave sin prefijo, probado contra el repositorio real y
contra fixtures, con la lista de puntos ciegos declarada en el propio escáner.

**D8. Refresco de la ventana offline en el bind.** "Se refresca en cada autenticación en
línea" (`docs/DATABASE.md` §4 `devices`) se cumple en la única autenticación en línea que
conoce el dispositivo en el MVP: `POST /auth/device-bind`.

**D9. Lenguaje de comentarios.** El código nuevo usa comentarios en español neutro. Las siete
migraciones del Sprint 1 quedan en inglés: deuda de normalización preexistente, no la
resuelve este sprint.

**D10. El proceso se niega a arrancar con un rol privilegiado.** El rol dueño de este
contenedor (`pos`) es `rolsuper` y `rolbypassrls`, y **`FORCE RLS` no liga a un superusuario**:
medido, `pos` lee las filas de todos los tenants sin GUC y sin error, mientras `pos_app` lee
cero. Como ninguna validación de configuración ni test lo detectaba, `cmd/api` ahora afirma
`rolsuper`/`rolbypassrls` en falso sobre los dos pools y se niega a arrancar nombrando la
variable. Consecuencia aceptada: sin base no se puede probar el rol, así que el arranque pasa
a depender de que la base responda (antes arrancaba y `/readyz` reportaba 503).

## Cortes de entrega (feature-branch-chain)

| Corte | Rama hija | PR apunta a | Commits | Líneas |
| :--- | :--- | :--- | :--- | :--- |
| 1 | `feat/sprint-2-db` | `feat/sprint-2-identity` | `63efad4`, `f2a3bda` | +1.572 / −8 |
| 2 | `feat/sprint-2-be-auth` | `feat/sprint-2-db` | `0f51a12`, `ba2f367` | +3.412 / −25 |
| 3 | `feat/sprint-2-be-sync` | `feat/sprint-2-be-auth` | `2c08d06`, `a6ccb04` | +2.465 |
| 4 | `feat/sprint-2-fe` | `feat/sprint-2-be-sync` | `768affa`, `67250e6` | +2.506 / −15 |
| 5 | `feat/sprint-2-hardening` | `feat/sprint-2-fe` | `1829202`, `3d62a24`, `15001a8`, `864205a` | +513 / −20 |

La cadena es lineal: cada corte sale del tip del anterior, así que el merge final sobre el
tracker es un *fast-forward*. Push, PR y merge siguen siendo decisión del usuario.

**Presupuesto de revisión: no se cumplió.** El corte más chico ya son 513 líneas y el más
grande 3.412, contra el presupuesto de ~400 por PR. El pronóstico al planificar (1.400–1.900
para todo el sprint) se quedó corto por un factor de cinco, y la causa es estructural: con TDD
estricto los tests viajan en la misma unidad de trabajo, y este sprint es casi todo barrera
de seguridad (matriz de concesiones, políticas, pruebas de aislamiento fail-then-pass, tests
de mutación). Las opciones quedan abiertas para el usuario al abrir los PR: revisar los cinco
cortes como están, recortarlos por tarea (algunos siguen por encima de 400), o aceptar
`size:exception`.

## Tareas

### Base de datos

- [x] **W2-DB-2** — Cinco roles y la matriz de concesiones de §5.5, con los dos caminos de
  bootstrap pre-auth. `63efad4`.
- [x] **W2-DB-1** — `FORCE RLS` y las políticas `USING` / `WITH CHECK` de §5.3 sobre las once
  tablas de tenant. `f2a3bda`.

### Backend

- [x] **W2-BE-1** — `POST /auth/login` con JWT de `tenant_id` + `user_id` y sin claim `role`.
  `0f51a12`.
- [x] **W2-BE-2** — Helper de transacción con el contexto de tenant como primera sentencia y
  la superficie cerrada en el tipo. `ba2f367`.
- [x] **W2-BE-3** — `POST /auth/device-bind` con `pin_hash` argon2id y ventana offline de
  7 días o más. `2c08d06`.
- [x] **W2-BE-4** — Namespacing obligatorio de las claves de Redis. `a6ccb04`.

### Frontend

- [x] **W2-FE-1** — Pantalla de acceso del Empleado con el numpad de 56px y la ventana
  offline. `768affa`.
- [x] **W2-FE-2** — `useNetworkStatus` y badge de conectividad observable. `67250e6`.

### Cierre posterior a la verificación

- [x] Rol privilegiado rechazado al arrancar (D10). `1829202`.
- [x] Toda partición de `sales` con su política de tenant, verificado por CI. `3d62a24`.
- [x] Escáner de claves Redis ampliado a la concatenación por partes. `15001a8`.

## Criterios de aceptación — resultado medido

1. **Ningún endpoint acepta `tenant_id` del cuerpo ni de un encabezado.** *Demostrado.*
   Barrido exhaustivo: las únicas cabeceras leídas en todo el árbol son `Authorization` y
   `X-Request-Id`; no hay `URL.Query` ni `FormValue` ni una etiqueta `json:"…tenant…"`. Contra
   el binario real, un `POST /auth/device-bind` con `tenant_id` y `X-Tenant-Id` falsificados
   devuelve 200 y escribe la fila en el tenant del token.
2. **Aislamiento fail-then-pass.** *Demostrado*, incluso corriendo el `Down` de la migración
   de RLS con el runner del repositorio: con la migración revertida la misma consulta devuelve
   las filas de los dos tenants (2), y con ella aplicada devuelve una (1).
3. **Token expirado con PIN válido todavía desbloquea offline ≥ 7 días.** *Demostrado*: la
   ventana es exactamente 7 días desde el bind con `JWT_TTL=1h`, y el test corre el emisor con
   un TTL de un segundo y comprueba que la ventana sigue abierta y el `pin_hash` sigue
   verificando.
4. **PIN y sesión offline son estado del dispositivo.** *Demostrado*: `users` no tiene
   columna de PIN; `devices` lleva `pin_hash` y `offline_session_valid_until`.
5. **Ninguna clave de Redis sin el prefijo `tenant:{id}:`.** *Parcial, por construcción*: no
   existe cliente ni instancia de Redis, así que la población de claves infractoras es vacía.
   Lo que sí está demostrado es la puerta: el escáner informa archivo y línea contra el
   repositorio real cuando se inyecta una clave sin prefijo.
6. **`pos_app` sin `SELECT` sobre `tenants`, sin `DELETE`, ningún rol de runtime con
   `BYPASSRLS`.** *Demostrado* contra `information_schema.role_table_grants` y `pg_roles`,
   columna por columna.

**Definition of Done.** *Demostrado*: login → JWT → consulta con scope de tenant probado por
tests de aislamiento que fallan antes de RLS y pasan después; ninguna petición acepta un
tenant del cuerpo o de un encabezado.

## Verificación

```
docker compose up -d
go build ./...                                   # sin salida
go test ./... -count=1                           # 6 paquetes ok, 278 corridas, 0 skips
cd web; npm run typecheck; npm run build; npx vitest run   # 14 archivos, 163 tests
```

Verificación independiente adversarial (un agente que no escribió nada de esto) sobre el
diff completo: reprodujo el fail-then-pass con el runner real, corrió ocho mutaciones con el
fallo esperado, y encontró cuatro cosas. Tres se cerraron en el corte 5; la cuarta es una
deuda registrada abajo.

## Divergencias y deudas abiertas (las decide el usuario)

1. **`docs/DATABASE.md` §5.4 no se puede ejecutar como está escrito** (D3). El documento es
   autoridad y está protegido: la corrección de esa frase queda pendiente.
2. **§5.2/§5.7 implican que un GUC ausente "produce cero filas"**; medido, con el predicado
   literal de §5.3 PostgreSQL **falla** en vez de filtrar (`42704` si el parámetro nunca se
   definió, `22P02` si la sesión lo dejó en `''`). Cero filas se logra porque la migración
   define el marcador con un default de UUID nil a nivel de base. Es un hecho del contrato
   que ninguno de los dos documentos dice y que conviene que digan.
3. **`docs/DATABASE.md:667` ("`FORCE RLS` liga también al dueño")** es cierto para un dueño
   normal y falso para un superusuario. El rol dueño del contenedor local es superusuario: en
   producción, el rol `owner/migration` no debe serlo (D10).
4. **Las particiones de `sales` no heredan concesiones ni políticas.** El bucle `DO` de las
   migraciones cubre las que existen; una partición nueva con `SELECT` y sin política filtra
   mal si se la consulta directamente. La consecuencia para `W6-DB-2` está escrita y ahora
   además afirmada por CI (`3d62a24`), pero el mecanismo queda en manos de ese sprint.
5. **`ARCHITECTURE.md:125` pide `Idempotency-Key` en device-bind** y se implementó por
   *upsert* (D5); el protocolo de claim es `W4-BE-2`.
6. **`pos_platform` DELETE**: la prosa de §4 `global_barcodes` lo permite, la matriz de §5.5
   dice "nada". Se implementó la matriz y la divergencia quedó comentada en la migración.
7. **Layout**: `docs/BACKEND.md` §2.4 `(propuesta)` propone `internal/auth` y
   `internal/modules/identity`; se mantuvo el `internal/identity` plano que ya existía.
8. **Cuarto almacén local**: el cliente persiste la sesión del dispositivo en un store
   `device_session` que `docs/FRONTEND.md:96-100` no nombra (autorizado por W2-FE-1).
9. **La UI de acceso en línea del Empleado no existe** hasta el Sprint 8, así que
   `bindDeviceSession` es código probado sin consumidor de producción; el criterio "no
   bloquea un intento en línea posterior" se demuestra como propiedad de la pantalla.
10. **`JWT_TTL` (12 h) y los parámetros de argon2id** no los fija ninguna autoridad: se
    eligieron y se documentaron en el código.
11. **Escáner de Redis**: la concatenación que cruza variables o llamadas sigue siendo
    incatchable y está declarada como punto ciego medido por un test propio.
12. **Deuda de idioma en migraciones**: las siete del Sprint 1 están en inglés y las dos
    nuevas en español.
13. **`JWT_SECRET` es obligatorio y sin default**: cualquier entorno existente tiene que
    definirlo (documentado en `.env.example`).
14. **El arranque depende de la base** (D10), a cambio de no servir con un rol que puede
    saltar el RLS.

## Progreso y evidencia

- [x] Exploración: contrato leído (`docs/DATABASE.md` §2, §4 identidad, §5, §7.4;
  `docs/BACKEND.md` §4.1-4.4, §5, §7.4, §7.6, §10.2; `SPRINTS.md` §7 Sprint 2 y §8),
  código leído vía CodeGraph, memoria de Engram recuperada.
- [x] Línea base: `go test ./...` verde con el contenedor arriba; `vitest` 86 tests en 7
  archivos (**corrección**: los 94 tests que se midieron al planificar pertenecían a la rama
  `fix/contrast-and-test-hardening`, que todavía no estaba mergeada; el PR #9 la mergeó
  durante el sprint).
- [x] Cuatro cortes ejecutados en orden DB → BE → FE, con una unidad de trabajo por tarea.
- [x] Rebasa sobre `origin/develop` (`5769cbd`) cuando el PR #9 aterrizó, con los dos suites
  re-corridos después: Go verde y `vitest` 163.
- [x] Verificación independiente adversarial, con sus hallazgos y su cierre en el corte 5.
- [x] Bases de sondeo locales (`pos_probe_undef`, `pos_probe_w2`) eliminadas: el chequeo de
      fugas del propio plan solo miraba `pos_test%` y no las habría visto.

## Siguiente paso

Abrir los PR de los cinco cortes (o recortarlos) y decidir las divergencias 1–4, que son
frases de contrato que la realidad de PostgreSQL contradice.
