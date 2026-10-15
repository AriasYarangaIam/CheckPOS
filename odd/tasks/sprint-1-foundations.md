# sprint-1-foundations

Ejecución del **Sprint 1 (Semana 1) — Fundaciones y congelamiento** de `SPRINTS.md`: congelar el contrato de datos, montar el andamiaje de las tres capas (DB / BE / FE) y probar que la verificación existe.

## Objective

Terminar el Sprint 1 con verificación demostrable: el catálogo de migraciones aplicado sobre una base limpia muestra las 14 tablas del contrato con su particionado, el runner `go test ./...` corre y pasa, el runner `vitest` corre y pasa, y los cuatro entregables de congelamiento quedan revisados sin contradecir `ARCHITECTURE.md`.

## Problem

El repositorio no tiene una sola línea de código: 11 archivos `.md` trackeados y cero fuentes. Sprint 1 arranca de cero absoluto en las tres capas, y el plan arrastra dos huecos:

1. **Tarea implícita no nombrada.** El Definition of Done exige "el catálogo de migraciones aplicado muestra 14 tablas", pero ninguna tarea del plan levanta la base local ni elige el runner de migraciones. Sin eso, `W1-DB-1..3` no son ejecutables ni verificables.
2. **Decisiones `(propuesta)` sin cerrar.** `docs/BACKEND.md` §2.4–2.6 dejó abiertos el layout Go, la configuración y la topología local; §7.6 deja abierta la herramienta de migración. Bloquean la primera migración.
3. **Contradicción entre documentos (cerrada).** `SPRINTS.md` dice que Sprint 1 incluye el andamiaje de las tres capas; `docs/BACKEND.md` §10.1 decía "no requiere código". Ganó `SPRINTS.md` por ser la autoridad de secuenciación; §10.1 quedó alineada con nota de alcance.

## Why

Autorización del usuario (2026-10-15): "resuelve los conflictos de runners y layout para que puedas resolver el Sprint 1". El alcance de la iteración es el Sprint 1 tal como está escrito en `SPRINTS.md`, incluida `W1-FE-4` (esquema Dexie).

## Scope

Dentro: `W1-DB-0` (nueva, base local + runner), `W1-DB-1..3`, `W1-BE-1`, `W1-FE-1..4`, `W1-BE-2/3/4` como revisión y publicación.

Fuera: cualquier tarea de Sprint 2 o posterior (JWT, endpoints de negocio, RLS aplicada a políticas); tests de negocio; tocar las autoridades `ARCHITECTURE.md` y `docs/DATABASE.md`; `git add`/`commit`/`push` sin confirmación explícita (convención observada del repo).

## Decisiones cerradas (autorizadas por el usuario)

1. **Runner de migraciones: `goose`** (`github.com/pressly/goose/v3`), SQL-first en `migrations/`, embebido con `embed.FS` y un `cmd/migrate` delgado. Fundamento: el DDL queda legible contra `docs/DATABASE.md` §4; los tests de integración aplican migraciones a una base descartable sin depender de un binario externo; soporta reversa donde el contrato la permite (`docs/BACKEND.md` §7.6).
2. **Layout Go por módulo de `ARCHITECTURE.md`**, con la regla "ningún módulo escribe las tablas de otro" hecha exigible por directorio. Siete grupos de módulos = siete paquetes.
3. **Postgres `17-alpine`** en `docker compose`, con UUIDv7 generado en la aplicación (no en la base), como fija el contrato.

## Constraints

- `ARCHITECTURE.md` es la autoridad de arquitectura; `docs/DATABASE.md` la de esquema, leída como enmienda por su §8. Nada puede contradecirlas.
- `docs/DATABASE.md` **no contiene DDL por diseño**: el SQL de las 14 tablas se deriva de §4 y se verifica contra §2 (convenciones), §3 (ERD), §6 (particionado) y §8 (enmiendas).
- Dinero `BIGINT` en centavos enteros, sin excepción. Cantidades `NUMERIC(10,3)`. JSONB exactamente en `products.attributes` y `global_products.attributes`. `tenders` y `sync_events` no son tablas.
- Registro: código e identificadores en inglés; comentarios y documentos en español neutro profesional.
- Ninguna métrica, corrida de test o número de cobertura se declara sin haber sido ejecutado.

## Tasks

- [x] **U1 `W1-DB-0`** Base local y runner de migraciones — RUTA: delegada (writer único; disparador de escritura: 2+ archivos no triviales; disparador de preparación: leer 4+ documentos antes de escribir). HECHO 2026-10-15. Evidencia: postgres 17 healthy en puerto 5433; `go run ./cmd/migrate up` aplica 7 migraciones; `down`→`up` probado.
- [x] **U2 `W1-DB-1`** Migración inicial con las 14 tablas — RUTA: delegada (mismo writer que U1). HECHO. Evidencia: spot check del padre devuelve las 14 tablas del contrato por nombre.
- [x] **U3 `W1-DB-2`** Particionado mensual de `sales` + `sales_default` — RUTA: delegada (mismo writer). HECHO. Evidencia: 7 particiones mensuales + `sales_default`; clave `RANGE (sold_on)`; PK `(sold_on, sale_id)`.
- [x] **U4 `W1-DB-3`** Restricciones vinculantes — RUTA: delegada (mismo writer). HECHO. Evidencia: CHECK de seis valores en ambas tablas, unicidad parcial, `UNIQUE (tenant_id, closed_on)`.
- [x] **U5 `W1-BE-1`** Runner `go test ./...` + proceso único Go con su layout — RUTA: delegada (writer único). HECHO 2026-10-15. Evidencia: `cmd/api` con `/healthz` y `/readyz`; layout de 7 módulos; `go test ./...` exit 0 con 66 tests; test de contrato de esquema contra base descartable.
- [x] **U6 `W1-FE-1`** Runner `vitest` + scaffold PWA — RUTA: delegada (writer único). HECHO 2026-10-15. Evidencia: `web/` con Vite + React + TS estricto + Tailwind; `vitest run` 7/7 exit 0; `tsc --noEmit` exit 0; build exit 0.
- [x] **U7 `W1-FE-2`** Tokens 60-30-10 y tipografía tabular — RUTA: delegada (writer único para U7–U9; disparador de escritura: 2+ archivos no triviales; disparador de preparación: leer `docs/DESIGN.md` §2.1-§2.3 y `docs/FRONTEND.md` antes de escribir). HECHO 2026-10-15, commit `cad4a53`. Evidencia: los 16 tokens de §2.1, las 6 escalas de §2.2 (tamaño, interlineado **y** peso) y la sombra de §2.3 coinciden uno a uno con `docs/DESIGN.md`; `Money` renderiza con `font-mono tabular-nums`; fuentes auto-hospedadas, cero peticiones a CDN.
- [x] **U8 `W1-FE-3`** Primitivas base — RUTA: delegada (mismo writer). HECHO, commit `ded7fe6`. Evidencia: `Button` con `active:scale-[0.98]` y `transition-transform`; `Card`, `Badge`, `Input` y `Dialog` existen; `modal-spring` con `cubic-bezier(0.16, 1, 0.3, 1)` presente en el CSS compilado, con rama `prefers-reduced-motion` real y efectiva; cero `transition: all` en fuentes.
- [x] **U9 `W1-FE-4`** Esquema Dexie — RUTA: delegada (mismo writer). HECHO, commit `782b004`. Evidencia: `products`, `cart` e `idempotency_queue` con todos los campos de `docs/FRONTEND.md:98-100`; persistencia probada cerrando y reabriendo con una instancia nueva, más control negativo con `Dexie.delete`.
- [x] **U10 `W1-BE-2/3/4`** Revisión y publicación de glosario, protocolo offline y apuntes de amenaza — RUTA: inline. HECHO 2026-10-15. Evidencia: los 11 términos exigidos están en `docs/BACKEND.md` §3.1 con módulo dueño y fuente; §5.7 declara `FORCE RLS` + `SET LOCAL` por transacción y la expiración de PIN/sesión offline, y cubre las cuatro amenazas nombradas; §6 recibió el diagrama de secuencia que el criterio de `W1-BE-3` exigía y que no existía (commit `docs(backend)` del 2026-10-15).

## Estrategia de ramas y commits

- `develop` creada desde la línea actual (`6d61273`), **no** desde `main`: `main` está 22 commits atrás y esos 22 commits tocan exactamente los 6 archivos que el usuario tiene modificados sin commitear, así que ramificar desde `main` habría pisado trabajo sin guardar.
- `feat/sprint-1-foundations` sale de `develop`. Base del PR: **`develop`**.
- 10 commits en la rama: 5 de la capa DB, 3 de la capa Go, 1 del scaffold web y 1 del rename de `internal/sync` → `internal/synckey`.
- Ningún commit incluye `docs/`, `odd/`, `.agent/`, `.engram/`, `.impeccable/` ni `.env`; hay una guarda en el staging que aborta si alguna aparece.
- **Push ejecutado el 2026-10-15** con autorización explícita del usuario: `develop` creada en el remoto en `6d61273` y `feat/sprint-1-foundations` actualizada en fast-forward a `dfa4985`. `main` quedó intacta en `3d0ecd9`.
- Hallazgo del push: `feat/sprint-1-foundations` **ya existía en el remoto** en `f893100` antes de esta operación, subida desde fuera de esta sesión. Consecuencia: los 22 commits de diseño ya eran alcanzables desde esa rama, así que publicar `develop` no expuso contenido nuevo.
- PR verificado contra refs remotos: **10 commits, 48 archivos, 0 rutas `docs/` u `odd/`**. `gh` no está instalado; el PR se abre por web.
- `main` sigue 22 commits atrás: los commits de diseño no tienen camino a `main` todavía.

## Authorized scope

Escritura: `SPRINTS.md` (fila `W1-DB-0`), `docs/BACKEND.md` §10.1 (alineación), este documento, espejo Engram, y el código nuevo del Sprint 1 (`docker-compose.yml`, `.env.example`, `Makefile` o scripts, `migrations/`, `cmd/`, `internal/`, `web/` o equivalente). Lectura: todo el repositorio. Prohibido: `git add`/`commit`/`push` sin confirmación; modificar `ARCHITECTURE.md` y `docs/DATABASE.md`; adelantar trabajo de Sprint 2+.

## Acceptance criteria

Re-verificados por el padre el 2026-10-15 sobre la rama `feat/sprint-1-frontend`, con la base local levantada:

- [x] `docker compose up -d` deja Postgres accesible y las migraciones se aplican contra una base limpia sin error. Contenedor `Up 14 hours (healthy)`; el test de contrato de esquema (`internal/platform/postgres`) aplica las migraciones a una base descartable y pasa.
- [x] `SELECT count(*) FROM information_schema.tables` sobre el esquema de la aplicación devuelve **14** tablas de negocio en 7 grupos. Trampa registrada: un conteo ingenuo devuelve **15** porque incluye `goose_db_version` (tabla de control del runner); las particiones `sales_*` también hay que excluirlas.
- [x] `tenders` y `sync_events` no existen como tablas. Conteo real: `0`.
- [x] `sales` está particionada por rango mensual sobre `sold_on`, con PK `(sold_on, sale_id)` y partición `sales_default`. Hijos reales de `sales`: **8** = 7 particiones mensuales (`2026_10` a `2027_04`) + `sales_default`.
- [x] Toda columna de dinero es `BIGINT`; JSONB sólo en las dos columnas declaradas. Columnas `%_cents` no-`bigint`: `0`; columnas `jsonb`: `2`.
- [x] `go test ./...` corre y pasa; `vitest` corre y pasa. `go build ./...` exit 0 · `go test ./...` exit 0 (4 paquetes con tests en verde) · `vitest run` **58/58** exit 0 · `tsc --noEmit` exit 0 · `vite build` exit 0.
- [x] Ningún archivo staged (`git diff --cached` vacío).
- [x] Sin bases descartables filtradas: `pg_database LIKE 'pos_test%'` devuelve `0`.

## Checks

- **Base de datos**: `docker compose up -d` · aplicar migraciones · consultas de conteo de tablas, particiones y tipos de columna de dinero. Evidencia = salida real de cada comando.
- **Backend**: `go build ./...` · `go test ./...`.
- **Frontend**: `npm ci` o `npm install` · `npx vitest run`.
- **Gate**: RDD está `off` (decidido globalmente), así que la verificación es funcional y objetiva, con spot check del padre re-ejecutando un comando reportado.

## Progress

- 2026-10-15 · Arranque. Contradicción `SPRINTS.md` vs `docs/BACKEND.md` §10.1 cerrada; fila `W1-DB-0` agregada al plan; decisiones de runner y layout cerradas; Docker daemon levantado; RDD verificado `off`. U1–U4 delegadas.
- 2026-10-15 · U1–U4 cerradas (capa DB completa, 5 commits). Verificado por el padre: 14 tablas, 7 particiones + default, 0 columnas de dinero no-`bigint`, 2 columnas `jsonb`, 0 tablas fantasma.
- 2026-10-15 · Ramas creadas (`develop`, `feat/sprint-1-foundations`). U5 y U6 cerradas (capa Go y scaffold web, 4 commits). Verificado por el padre: `go test ./...` exit 0 con 66 tests y 0 bases descartables filtradas; `vitest run` 7/7 y `tsc --noEmit` exit 0.
- 2026-10-15 · PR #1 (`feat/sprint-1-foundations` → `develop`) y PR #2 (`develop` → `main`) mergeados por el usuario; PR #3 (`chore/repo-cleanup`) publicó los documentos del proyecto. Los runners quedaron verdes en `develop`.
- 2026-10-15 · U7–U9 cerradas en `feat/sprint-1-frontend` (3 commits de writer + 1 de corrección posterior). Verificado por el padre: tokens de §2.1-§2.3 cotejados uno a uno contra `docs/DESIGN.md`; `vitest run` 58/58; `tsc --noEmit` exit 0; `vite build` exit 0.
- 2026-10-15 · Verificación independiente adversarial sobre los tres commits: `gentle-ai review assess` devolvió `risk: high` con motivo `unassessable` (los directorios de tooling sin versionar), así que el gate exigía verificador separado. Cero blockers; dos defectos reales corregidos en `719e442`; nueve hallazgos nuevos registrados abajo.
- 2026-10-15 · U10 cerrada (inline). Hallazgo accionable: el criterio de `W1-BE-3` exige un diagrama y la §6 de `docs/BACKEND.md` no tenía ninguno; se agregó la secuencia del ciclo offline.
- 2026-10-15 · Definition of Done re-verificada completa sobre esta rama: 14 tablas de negocio, 8 hijos de `sales`, 0 columnas de dinero no-`bigint`, 2 `jsonb`, `go build`/`go test` exit 0, `vitest` 58/58, `tsc` exit 0, 0 bases filtradas, 0 archivos staged.

## Hallazgos registrados

1. **`internal/sync` declara `package sync` y ensombrece la librería estándar.** Cualquier archivo que necesite ambas debe aliasar. Es barato de renombrar ahora y caro cuando Sprint 6 dependa de él.
2. **`NOT_READY` (503) es un séptimo código de error** que no está en la tabla de seis de `docs/BACKEND.md` §4.1. El writer lo puso en el sobre porque §4.1 prohíbe errores en texto plano, pero es una decisión que conviene confirmar.
3. **El test de contrato de esquema tiene fecha de vencimiento.** Afirma que existe la partición del mes actual, y la ventana creada es `2026-10 … 2027-04`. Desde mayo de 2027 falla por diseño hasta que exista el job mensual de `docs/DATABASE.md` §6.3.
4. **Apagado ordenado implementado pero no verificado en runtime.** PowerShell en Windows no puede enviar `SIGINT`/`SIGTERM` a un hijo de `Start-Process`, así que el camino `ctx.Done() → Shutdown` compila pero ninguna corrida prueba que drene peticiones en vuelo.
5. **`formatCentsToSoles` deja indefinidos dos casos por contrato:** el separador de miles y el render de montos negativos. Los negativos aparecen en Sprint 7 con `fiado_movements`; el separador, cuando el catálogo muestre precios de cuatro dígitos.
6. **Tailwind v4 es CSS-first y no usa `tailwind.config.js`**, pero `docs/FRONTEND.md:86` nombra ese archivo para `W1-FE-2`. El puente es `@config`.
7. **El `package-lock.json` entra al repo (2595 líneas generadas).** Correcto para reproducibilidad, pero cuenta para cualquier presupuesto de revisión que no excluya lockfiles.

### Hallazgos de U7–U9 (2026-10-15)

8. **Contraste WCAG del botón primario — decisión del usuario.** `docs/DESIGN.md:153` fija texto blanco sobre `accent-primary` (`#10B981`), que mide **2.54:1**. Falla AA incluso para texto grande (umbral 3:1) y el CTA es `heading-md` (20px, 700). El arreglo es de una palabra: pintar el botón sobre `accent-hover` (`#059669`, **3.77:1**, cumple el umbral de texto grande). El badge de Plin (`#0284C7` + blanco) queda en 4.09:1 y también queda bajo el umbral de 4.5:1 para texto normal. **No se cambió el contrato de diseño**: la decisión es del usuario.
9. **Divergencia `scale(0.95)` vs `scale(0.96)` entre autoridades.** `docs/FRONTEND.md:19` fija la entrada del modal en `scale(0.96)`; `docs/DESIGN.md` §6.2:304 la fija en `scale(0.95)`. Ambas hablan de lo mismo (diálogos y modales de cobro). La implementación siguió `docs/FRONTEND.md:19`; la cita del código atribuía 0.96 a los dos documentos y se corrigió en `719e442`.
10. **`docs/FRONTEND.md:89` confunde el nombre del token de acento.** Dice `emerald-600` (`#10B981`), pero `docs/DESIGN.md` §2.1 mapea `#10B981 → emerald-500` y `#059669 → emerald-600`. La implementación siguió el **hex**, que es la autoridad.
11. **Ruido en la columna `Tailwind Class` de `docs/DESIGN.md` §2.1.** `badge-yape` `#7C3AED` figura como `bg-purple-600` cuando es `violet-600`; y §1.1:14 llama `zinc-50` a `#F4F4F5`, que es `zinc-100`. La implementación siguió los hex.
12. **El literal de la prohibición inyectaba en el bundle lo prohibido (corregido).** Tailwind escanea también los archivos de test: la aserción `not.toContain('transition-all')` hacía que el CSS publicado incluyera una regla `.transition-all` muerta. Corregido en `719e442` armando el literal por partes — tras el arreglo, el CSS compilado bajó de 24.11 kB a 23.91 kB y `transition-property: all` desapareció. Nota metodológica: el primer intento de arreglo repuso el literal dentro de su propio comentario explicativo y no movió el CSS; se detectó comparando el hash del bundle.
13. **`index.css.test.ts` es un detector de cambios, no una verificación de conformidad.** Sus valores esperados son literales dentro del test y nunca lee `docs/DESIGN.md`: si un token se cambiara contradiciendo §2.1 y el literal del test se actualizara en el mismo commit, la suite sigue verde. Además es `toContain` sobre texto crudo, así que un valor dentro de un comentario también la satisface. Y cubre **15** de los 16 tokens de §2.1: falta `accent-glow`.
14. **"No produce salto de layout" no es verificable, solo su mecanismo.** jsdom no tiene motor de layout, así que ninguna aserción puede medir el salto. Lo verificado es el mecanismo que `docs/FRONTEND.md:21` declara como causa (`font-mono` + `tabular-nums` en el nodo del importe, `"tnum" 1` en el CSS compilado). El criterio se cumple en los términos del propio contrato, no por medición.
15. **Dos aserciones de U9 son tautológicas.** La de "campos requeridos" inserta un objeto con 8 claves y afirma que tiene esas 8 claves (la garantía real es la interfaz de TypeScript, verificada por `tsc`); y la de "ninguna tabla guarda el catálogo global" es una heurística por nombre (`/global|catalog/i`) que una tabla llamada `master_skus` esquivaría. El hecho se sostiene, la aserción no lo sostiene.
16. **`shadow-keypad` (`docs/DESIGN.md` §2.3:76) no está declarado.** Queda fuera del alcance de `W1-FE-2`, que `SPRINTS.md` limita a §2.1 y §2.2, pero el teclado ergonómico de FASE 3 lo necesita. Se declara cuando exista el teclado.

## Next step

Sprint 1 **cerrado** el 2026-10-15: U1–U10 hechas y Definition of Done re-verificada completa (ambos runners en verde, 14 tablas aplicadas, los tres entregables de congelamiento revisados).

Entrega pendiente: abrir el PR de `feat/sprint-1-frontend` → `develop` con los 4 commits de la rama (U7 `cad4a53`, U8 `ded7fe6`, U9 `782b004`, corrección de verificación `719e442`). La rama incluye además el diagrama de la §6 de `docs/BACKEND.md` y este documento: U10 es una tarea del Sprint 1 y su entregable es de documentación, así que viaja con el cierre de la semana. Si el usuario prefiere separarlo, se mueve a una rama de documentación sin tocar el código.

Antes de abrir el PR queda una decisión del usuario: el contraste del botón primario (hallazgo 8).

Estado de entrega al 2026-10-15: **PR #1 (`feat/sprint-1-foundations` → `develop`) y PR #2 (`develop` → `main`) mergeados por el usuario.** La capa DB, la capa Go y el scaffold web ya están en `main`. La rama del sprint conserva un commit huérfano y superado (`6782611`, primer intento de README) que no debe mergearse; el README definitivo viaja en la rama de limpieza.

Rama de limpieza `chore/repo-cleanup` (base `develop`, mismo flujo hacia `main`): resuelve la duplicación de `DESIGN.md`, versiona `odd/tasks` completo y deja los documentos del proyecto en el remoto. Contiene 10 commits propios más un merge de puesta al día con `develop`.

Sobre el presupuesto de revisión: el PR de limpieza son ~2.700 líneas de documentación. El presupuesto de 400 líneas por PR está pensado para carga de revisión de código; documentación de contrato se revisa por sección, no por conteo. El PR del sprint, en cambio, fueron ~2.900 líneas autoradas de código y se mergeó como una sola pieza.

Push remoto: ejecutado con autorización explícita del usuario (`develop`, `feat/sprint-1-foundations`, `chore/repo-cleanup`).
