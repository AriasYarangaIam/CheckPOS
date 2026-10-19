# sprint-3-tenant-catalog

## Objective

Entregar el catálogo del tenant (Semana 3 de `SPRINTS.md`, líneas 181-211): venta entre 50 y 200 productos propios, sin catálogo maestro y sin descargar jamás un universo global, con la grilla de favoritos, la búsqueda instantánea y el subconjunto cacheado en IndexedDB.

## Problem

Sprint 2 (identidad + aislamiento por tenant) está cerrado y fusionado en `origin/develop` (PRs #11-#15). `W3-DB-1` ya quedó cubierto por el esquema en bloque de Sprint 1 (`migrations/20261015000003_catalog.sql`): unicidad parcial con predicado de soft-delete, índice de cursor sin predicado parcial y `stock_adjustments` como log de deltas firmados. La capa Go de catálogo (`internal/catalog`, `internal/photos`) son solo stubs con `doc.go`: falta todo el backend y todo el frontend del sprint.

## Why

Pedido explícito del usuario (2026-10-18): arrancar con las tareas planificadas para el Sprint 3. El sprint es la dependencia de entrada de la venta de mostrador (Semana 4): `stock` tiene que converger por deltas antes de que la venta escriba su delta negativo.

## Scope

Dentro:
1. Linea base + rama `feat/sprint-3-tenant-catalog` desde `origin/develop`.
2. `W3-DB-1` — verificación de la migración existente contra el contrato (tarea de evidencia, ver Resultado de la exploración).
3. `W3-BE-1` — `GET /products`, `POST /products`, `PATCH /products/{id}` (nombre, GTIN opcional, precio en centavos, UoM, URL de foto, `attributes`; `stock` excluido del payload; `422` si el PATCH intenta mutar campos fuera del contrato).
4. `W3-BE-2` — stub de búsqueda de GTIN bajo demanda, solo en el camino de fallo de caché, mediado por API.
5. `W3-BE-3` — fotos en object storage; la fila guarda solo la URL; ningún byte de blob entra en PostgreSQL, Redis ni IndexedDB.
6. `W3-FE-1` — grilla de favoritos con tiles táctiles de 48px y agregado en un toque vía `onProductIdentified`.
7. `W3-FE-2` — búsqueda instantánea por nombre + filtro por categoría, sin animación ni salto de layout.
8. `W3-FE-3` — cache del subconjunto de catálogo en IndexedDB poblado por `GET /products`.

Fuera: catálogo maestro colaborativo (`docs/DESIGN.md:238` no es alcance); UI de conflicto; split tender; migraciones nuevas de catálogo si la existente cumple el contrato; push/PR (decisión del usuario).

## Constraints

- `TDD estricto` (convención activa del proyecto, heredada de Sprint 2, `odd/tasks/sprint-2-identity.md:58`): el test que falla primero viaja en la misma unidad de trabajo que el cambio.
- Runners: `go test ./...` (HTTP in-process, Postgres vía `docker compose up -d`, bases `pos_test_` desechables); `vitest` en `web/` (86 tests en Sprint 2).
- Contratos: `docs/DATABASE.md` §2.9/§4/§7.7, `ARCHITECTURE.md:89/127/176`, `docs/DESIGN.md:135-137`, `docs/FRONTEND.md:23`, `docs/BACKEND.md` §10.3.
- `stock` NUNCA viaja en payloads POST/PATCH; converge solo por `SUM(stock_adjustments.qty_delta)`; sin CHECK `>= 0` (oversell offline aceptado).
- Colisión `(tenant_id, name)`: renombrado determinista; colisión `(tenant_id, gtin)`: GTIN en NULL, ambas silenciosas (`docs/DATABASE.md` §7.7).
- El teléfono nunca descarga un volcado global de SKUs.
- Commits de unidad de trabajo con Conventional Commits, sin Co-Authored-By.

## Tasks

- [x] **T0** Linea base y rama: `feat/sprint-3-tenant-catalog` desde `origin/develop`; `go test ./...` verde con contenedor arriba; `vitest` verde en `web/` — RUTA: inline. HECHO 2026-10-18. Evidencia: rama creada y tracking a origin/develop; `go test ./...` ok (identity + platform, cacheada) con Postgres de Docker arriba; `vitest` 166/166 en 14 archivos.
- [x] **W3-DB-1** Verificación de evidencia de la migración existente contra `docs/DATABASE.md` §4/§7.3 — RUTA: inline (verificación por lectura; sin escritura). HECHO 2026-10-18. Evidencia: `migrations/20261015000003_catalog.sql` — índices únicos parciales `(tenant_id, name)` y `(tenant_id, gtin)` con predicado `deleted_at IS NULL` (:37-38), `products_tenant_updated_at_idx` sin predicado parcial con comentario explícito del porqué (:43), `stock_adjustments` log de deltas firmados sin CHECK `>= 0` (:19, :49-67); FORCE RLS sobre `products` y `stock_adjustments` ya impuesto por Sprint 2 (`20261016000002_tenant_rls.sql:67-68`, `:145-146`). La migración cumple; hipótesis confirmada, cero escrituras.
- [ ] **W3-BE-1** CRUD de productos con scope de tenant — RUTA: delegada (writer único; contrato de §10.3 + helpers de identity como patrón; TDD estricto RED→GREEN). Entregable: UoM persistido como campo, `422` ante mutación prohibida.
- [ ] **W3-BE-2** Stub de búsqueda de GTIN bajo demanda — RUTA: delegada (misma unidad de implementación BE que W3-BE-1/W3-BE-3, corte por tarea).
- [ ] **W3-BE-3** Fotos en object storage, la fila con solo la URL — RUTA: delegada (mismo corte BE).
- [ ] **W3-FE-1** Grilla de favoritos — RUTA: delegada (writer único FE; corte FE separado; TDD estricto con vitest).
- [ ] **W3-FE-2** Búsqueda instantánea + filtro por categoría — RUTA: delegada (mismo corte FE).
- [ ] **W3-FE-3** Cache IndexedDB del subconjunto — RUTA: delegada (mismo corte FE).
- [ ] **T-CLOSE** Verificación completa: ambos suites verde, criterios de aceptación revisados uno a uno, espejo Engram actualizado.

## Authorized scope

Escritura: código en `internal/catalog/`, `internal/photos/`, `web/src/`, `cmd/api/main.go` (montaje de rutas), pruebas de todo lo anterior, este documento, espejo Engram. Escrituras de migración solo si la verificación de W3-DB-1 falla. Lectura: todo el repositorio y sus contratos.

## Acceptance criteria

- [ ] `stock` no viaja en POST ni PATCH; el PATCH que intenta mutar campos fuera del contrato devuelve `422`.
- [ ] Un producto con UoM `kg` persiste el campo UoM y no un JSONB.
- [ ] Colisión de nombre: renombrado determinista; colisión de GTIN: queda NULL, ambas silenciosas.
- [ ] El camino de fallo de caché de GTIN queda verificado y ninguno descarga una lista global de SKUs.
- [ ] La carga de foto devuelve una URL; cero bytes de blob en PostgreSQL, Redis e IndexedDB.
- [ ] Un toque en la grilla agrega la línea correcta al carrito vía `onProductIdentified`.
- [ ] La escritura en el buscador no produce latencia ni salto de layout.
- [ ] La grilla renderiza desde el subconjunto local (IndexedDB); el catálogo global nunca aparece en el dispositivo.
- [ ] Ambos suites verdes (`go test ./...`, `vitest`) y comunidades de evidencia registradas por tarea.

## Checks

- `go test ./...` (PostgreSQL de prueba con Docker); `vitest watch:run` en `web/` (fake-indexeddb).
- Verificación de lectura contra `docs/BACKEND.md` §10.3 y `docs/DATABASE.md` §4/§7.7 en cada corte.
- `Review workload forecast` ~ 900-1400 líneas totales (Sprint 2 subestimó por factor 5 con TDD): por encima de ~400 → estrategia de entrega según decisión del usuario, resuelta antes del primer commit.

## Delivery decision (2026-10-18, usuario)

Estrategia: **PRs encadenados** · cadena: **stacked a develop** (corte BE y corte FE; cortes bebas dependientes de couch). Reutiliza el precedente de Sprint 2 (PRs #11-#14). Slice 1 = DB verificación + W3-BE-1..3; slice 2 = W3-FE-1..3.

## Progress

- 2026-10-18 · Documento creado en el arranque del sprint; exploración cerrada: migración de catálogo ya en repo, `internal/catalog` y `internal/photos` son stubs, Sprint 2 cerrado y fusionado en `origin/develop` (PR #15). Estrategia de entrega resuelta: PRs encadenados stacked a develop.
- 2026-10-18 · T0 en ejecución: rama `feat/sprint-3-tenant-catalog` desde `origin/develop`.
- 2026-10-18 · T0 y W3-DB-1 cerradas. Línea base verde (go test ok, vitest 166/166); migración de catálogo verificada contra el contrato: cumple por evidencia, hipótesis confirmada, cero escrituras DB.
