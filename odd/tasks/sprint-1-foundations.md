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
- [ ] **U7 `W1-FE-2`** Tokens 60-30-10 y tipografía tabular — RUTA: delegada (mismo writer que U6).
- [ ] **U8 `W1-FE-3`** Primitivas base — RUTA: delegada (mismo writer que U6).
- [ ] **U9 `W1-FE-4`** Esquema Dexie — RUTA: delegada (mismo writer que U6).
- [ ] **U10 `W1-BE-2/3/4`** Revisión y publicación de glosario, protocolo offline y apuntes de amenaza — RUTA: inline.

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

- [ ] `docker compose up -d` deja Postgres accesible y las migraciones se aplican contra una base limpia sin error.
- [ ] `SELECT count(*) FROM information_schema.tables` sobre el esquema de la aplicación devuelve **14** tablas de negocio en 7 grupos.
- [ ] `tenders` y `sync_events` no existen como tablas.
- [ ] `sales` está particionada por rango mensual sobre `sold_on`, con PK `(sold_on, sale_id)` y partición `sales_default`; una fila fuera de rango aterriza en `sales_default`.
- [ ] Toda columna de dinero es `BIGINT`; JSONB sólo en las dos columnas declaradas.
- [ ] `go test ./...` corre y pasa; `vitest` corre y pasa.
- [ ] Ningún archivo staged (`git diff --cached` vacío).

## Checks

- **Base de datos**: `docker compose up -d` · aplicar migraciones · consultas de conteo de tablas, particiones y tipos de columna de dinero. Evidencia = salida real de cada comando.
- **Backend**: `go build ./...` · `go test ./...`.
- **Frontend**: `npm ci` o `npm install` · `npx vitest run`.
- **Gate**: RDD está `off` (decidido globalmente), así que la verificación es funcional y objetiva, con spot check del padre re-ejecutando un comando reportado.

## Progress

- 2026-10-15 · Arranque. Contradicción `SPRINTS.md` vs `docs/BACKEND.md` §10.1 cerrada; fila `W1-DB-0` agregada al plan; decisiones de runner y layout cerradas; Docker daemon levantado; RDD verificado `off`. U1–U4 delegadas.
- 2026-10-15 · U1–U4 cerradas (capa DB completa, 5 commits). Verificado por el padre: 14 tablas, 7 particiones + default, 0 columnas de dinero no-`bigint`, 2 columnas `jsonb`, 0 tablas fantasma.
- 2026-10-15 · Ramas creadas (`develop`, `feat/sprint-1-foundations`). U5 y U6 cerradas (capa Go y scaffold web, 4 commits). Verificado por el padre: `go test ./...` exit 0 con 66 tests y 0 bases descartables filtradas; `vitest run` 7/7 y `tsc --noEmit` exit 0.

## Hallazgos abiertos que salieron de U5 y U6

1. **`internal/sync` declara `package sync` y ensombrece la librería estándar.** Cualquier archivo que necesite ambas debe aliasar. Es barato de renombrar ahora y caro cuando Sprint 6 dependa de él.
2. **`NOT_READY` (503) es un séptimo código de error** que no está en la tabla de seis de `docs/BACKEND.md` §4.1. El writer lo puso en el sobre porque §4.1 prohíbe errores en texto plano, pero es una decisión que conviene confirmar.
3. **El test de contrato de esquema tiene fecha de vencimiento.** Afirma que existe la partición del mes actual, y la ventana creada es `2026-10 … 2027-04`. Desde mayo de 2027 falla por diseño hasta que exista el job mensual de `docs/DATABASE.md` §6.3.
4. **Apagado ordenado implementado pero no verificado en runtime.** PowerShell en Windows no puede enviar `SIGINT`/`SIGTERM` a un hijo de `Start-Process`, así que el camino `ctx.Done() → Shutdown` compila pero ninguna corrida prueba que drene peticiones en vuelo.
5. **`formatCentsToSoles` deja indefinidos dos casos por contrato:** el separador de miles y el render de montos negativos. Los negativos aparecen en Sprint 7 con `fiado_movements`; el separador, cuando el catálogo muestre precios de cuatro dígitos.
6. **Tailwind v4 es CSS-first y no usa `tailwind.config.js`**, pero `docs/FRONTEND.md:86` nombra ese archivo para `W1-FE-2`. El puente es `@config`.
7. **El `package-lock.json` entra al repo (2595 líneas generadas).** Correcto para reproducibilidad, pero cuenta para cualquier presupuesto de revisión que no excluya lockfiles.

## Next step

U7 (tokens) → U8 (primitivas) → U9 (Dexie) → U10 (revisión de los tres documentales de congelamiento). Con eso Sprint 1 cierra su Definition of Done: ambos runners corriendo y pasando, 14 tablas aplicadas y los tres documentos revisados.

Estado de entrega al 2026-10-15: **PR #1 (`feat/sprint-1-foundations` → `develop`) y PR #2 (`develop` → `main`) mergeados por el usuario.** La capa DB, la capa Go y el scaffold web ya están en `main`. La rama del sprint conserva un commit huérfano y superado (`6782611`, primer intento de README) que no debe mergearse; el README definitivo viaja en la rama de limpieza.

Rama de limpieza `chore/repo-cleanup` (base `develop`, mismo flujo hacia `main`): resuelve la duplicación de `DESIGN.md`, versiona `odd/tasks` completo y deja los documentos del proyecto en el remoto. Contiene 10 commits propios más un merge de puesta al día con `develop`.

Sobre el presupuesto de revisión: el PR de limpieza son ~2.700 líneas de documentación. El presupuesto de 400 líneas por PR está pensado para carga de revisión de código; documentación de contrato se revisa por sección, no por conteo. El PR del sprint, en cambio, fueron ~2.900 líneas autoradas de código y se mergeó como una sola pieza.

Push remoto: ejecutado con autorización explícita del usuario (`develop`, `feat/sprint-1-foundations`, `chore/repo-cleanup`).
