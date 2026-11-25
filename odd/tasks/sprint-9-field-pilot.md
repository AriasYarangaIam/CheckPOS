# sprint-9-field-pilot

Sprint 9 (Semana 9) del plan de entrega: piloto de campo. Alcance técnico autorizado de este
bloque: **W9-DB-1** (respaldo y recuperación) y **W9-BE-1** (instrumentación de percentiles).
Rama `feat/sprint-9-field-pilot`, anclada en `develop` (Sprint 8 cerrado por el PR #63).
Fuente normativa: `SPRINTS.md` Sprint 9, `docs/BACKEND.md` §7.9 y §8.1, `docs/RECOVERY.md` §1.3.

## Specs

### S1 — W9-DB-1, la fila del plan (verbatim)

> | W9-DB-1 | DB | Operación de respaldo y recuperación `(propuesta)`: instantánea diaria más archivado continuo, recuperación a un punto en el tiempo alineada con la retención de los claims, y un ensayo de restauración probado | Un ensayo de restauración ejecutado y registrado dentro de la ventana del piloto, no solo la existencia del respaldo | — |

### S2 — W9-DB-1, el entregable (verbatim)

> Un ensayo de restauración ejecutado y registrado dentro de la ventana del piloto, no solo la existencia del respaldo

### S3 — W9-BE-1, la fila del plan (verbatim)

> | W9-BE-1 | BE | Instrumentar y medir los percentiles 50 y 95 del cierre de venta y de la latencia de flush, y comparar el percentil 95 del escaneo contra el umbral de 3 segundos (`docs/BACKEND.md` §8.1) | Métricas registradas por dispositivo; la grilla sigue siendo el camino por defecto si el percentil 95 de la cámara supera 3 segundos | S7-2 |

### S4 — W9-BE-1, el entregable (verbatim)

> Métricas registradas por dispositivo; la grilla sigue siendo el camino por defecto si el percentil 95 de la cámara supera 3 segundos

### S5 — Criterio de aceptación del Sprint 9 aplicable (verbatim)

> Los percentiles quedan **medidos y registrados**; ninguna latencia se declara como lograda sin medición previa (`docs/BACKEND.md` §8.1). La grilla permanece como camino por defecto si el percentil 95 de la cámara supera 3 segundos.

### S6 — Honestidad de medición (verbatim)

> Nada en este bloque se afirma como ya medido: los percentiles, los tiempos de alta y las métricas de flush son objetivos a registrar durante el piloto, no resultados. Ninguna corrida de pruebas ni número de cobertura se declara sin haber sido ejecutado.

### S7 — Decisión D-A del usuario (2026-11-25, verbatim)

> W9-DB-1 pide «instantánea diaria más archivado continuo, recuperación a un punto en el tiempo, y un ensayo de restauración probado». ¿Qué alcance implementamos en el repo? — A · Respaldo verificado + runbook PITR

Con su preview aceptado:

> `maintenance backup --dir` → dump `-Fc` + manifiesto (timestamp, tamaño, sha256, retención de claims). `maintenance restore-verify --file --scratch` → crea `pos_restore_*`, restaura, verifica esquema y conteos, borra. Test de integración del ciclo completo (Docker). Runbook PITR en `docs/RECOVERY.md` §6 con el límite declarado.

> **No incluye**: `archive_mode` en compose ni recuperación a un instante real.

### S8 — Decisión D-B del usuario (2026-11-25, verbatim)

> ¿Qué superficie de métricas construimos para medir p50/p95 del cierre de venta, del flush y del escaneo? — A · Servidor /metrics + tarjeta cliente

Con su preview aceptado:

> `internal/platform/metrics`: histograma por ruta, p50/p95, `GET /metrics` en texto Prometheus. Middleware montado solo en rutas de negocio. `web/src/metrics/`: muestras `scan` (detección → carrito) y `flush` (inicio → ack) por `device_id`. Tarjeta «Desempeño del dispositivo» con p50/p95 y el umbral de 3 s.

## Tasks

- [ ] **T1** Documento del sprint + rama `feat/sprint-9-field-pilot` — RUTA: inline. S: S1–S8.
- [x] **T2** W9-DB-1 · `maintenance backup --dir`: dump `-Fc` vía los binarios del contenedor con manifiesto (timestamp UTC, bytes, sha256, huella del esquema, conteos de las catorce tablas) — RUTA: worker no disponible, ejecutada inline. S: S1, S2, S7.
- [x] **T3** W9-DB-1 · `maintenance restore-verify --file --scratch`: base `pos_restore_*` desechable, `pg_restore`, verificación de esquema y conteos contra el manifiesto, limpieza incluso en fallo, exit no-cero si no coincide — RUTA: worker no disponible, ejecutada inline. S: S1, S2, S7.
- [x] **T4** W9-DB-1 · Runbook §6 de `docs/RECOVERY.md`: instantánea diaria programada, retención alineada a los 400 días de claims, PITR como capacidad de infraestructura con su límite declarado, y el registro del ensayo — RUTA: inline. S: S1, S2, S7.
- [x] **T5** W9-BE-1 · `internal/platform/metrics` con histograma por ruta (buckets fijos, p50/p95, memoria acotada), middleware solo en rutas de negocio y `GET /metrics` en texto Prometheus — RUTA: worker no disponible, ejecutada inline. S: S3, S4, S5, S8.
- [x] **T6** W9-BE-1 · Cliente: muestras `scan` y `flush` por `device_id` en IndexedDB, cálculo p50/p95, tarjeta «Desempeño del dispositivo» en el Dashboard Admin con el umbral de 3 s y sin inventar cifras sin muestras — RUTA: worker no disponible, ejecutada inline. S: S3, S4, S5, S8.
- [ ] **T7** T-CLOSE · Verificación completa contra S1–S8, suites verdes y evidencia registrada — RUTA: inline. S: S5, S6.

## Decisiones de implementación (tomadas al escribir el documento)

- **D1** — `pg_dump`/`pg_restore` no están en el PATH del host (verificado: `pg_dump: command not found`). Los comandos ejecutan los binarios **dentro del contenedor** (`docker compose exec -T postgres …`), con `BACKUP_DOCKER_SERVICE` (default `postgres`) y `PG_DUMP`/`PG_RESTORE`/`PSQL` como override para un host con cliente instalado. El repo no asume un cliente PostgreSQL local.
- **D6** — El manifiesto no lleva «versión de migración» porque el runner aplica archivos idempotentes y no mantiene una tabla de versiones aplicadas. En su lugar lleva `schema_fingerprint`: sha256 de `information_schema.columns` (tabla, columna, tipo y nulabilidad) ordenado. Describe el esquema REAL y el ensayo lo compara contra el restaurado.
- **D2** — El dump usa el DSN de migración (`DATABASE_URL`), que es el rol dueño: un respaldo lógico necesita leer todas las tablas, y `pos_app`/`pos_auth` no pueden. El manifiesto deja escrito qué rol y qué base se respaldaron.
- **D3** — `GET /metrics` es **ruta de operación**, fuera de la superficie de negocio de `docs/BACKEND.md` §4, igual que `/healthz` y `/readyz`. No se documenta como endpoint de negocio ni se añade al contrato §4.
- **D4** — El servidor no conoce el `device_id` (el token solo lleva `tenant_id`, `user_id` y `role`; `internal/identity/token.go`). Por eso el percentil **por dispositivo** se mide en el cliente, que sí lo conoce desde `device-bind` (`web/src/auth/deviceSession.ts`), y el servidor mide p50/p95 **por ruta y estado**.
- **D5** — El límite de PITR es declarado, no simulado: este bloque entrega respaldo lógico verificado y ensayo real de restauración; el archivado continuo de WAL y la recuperación a un instante quedan como runbook de infraestructura, tal como el usuario aprobó en S7.

## Log

- **L1** (solicitud del usuario, verbatim): «Cual es el estado del proyecto, que cambios o implementaciones faltan hacer?»
- **L2** (elección del usuario, verbatim): «Sprint 9 técnico (W9-DB-1 + W9-BE-1)» — con la descripción «Documento ODD del Sprint 9 y las dos filas con código: respaldo/PITR con ensayo de restauración e instrumentación de percentiles p50/p95.»
- **L3** (decisiones de diseño, 2026-11-25): **D-A = A** (respaldo verificado + runbook PITR) y **D-B = A** (`GET /metrics` + tarjeta cliente), registradas verbatim en S7 y S8.
- **L4** (medición de partida contra `develop`, 2026-11-25): `go build ./...` exit 0; `npm test` 51 archivos / 464 pruebas verdes con 14 errores no controlados de Dexie; `go test ./...` con 73 pruebas fallidas, **todas** por `dial tcp 127.0.0.1:5433` con Docker apagado — 7 paquetes OK y 8 con fallo de conexión. No hay defecto de código medido.
- **L5** (los tres escritores delegados fallaron con error del harness sin escribir un solo archivo; `git status` solo mostraba este documento). La unidad se ejecutó inline, como manda la política de escritores: no se reintenta el lanzamiento. Evidencia de la unidad: `go test ./cmd/maintenance/` verde con 15 pruebas, y el ensayo real se salta mientras Docker esté apagado, declarándolo en lugar de fingirlo.
- **L6** (límite de verificación, declarado): el host tiene CGO deshabilitado, así que `go test -race` no corre (`-race requires cgo`). El registro de métricas lleva su prueba concurrente de 8 goroutines × 100 observaciones y pasa, pero **no** se ejecutó el detector de carreras; ninguna afirmación de este documento dice que esté libre de carreras con esa evidencia. La protección real es el mutex único del registro.
- **L8** (esquema local): la tabla `performance_samples` entra como versión 5 de Dexie (`++id, kind, recorded_at`). Los dos tests que fijan el contrato del esquema —`posDatabase.test.ts` y `catalogCache.test.ts`— se actualizaron a ocho tiendas y versión 5: el esquema cambió a propósito y el test tenía que reflejarlo, no silenciarse.
- **L9** (evidencia de la unidad de cliente): `npx vitest run` 53 archivos / 479 pruebas verdes (eran 464 antes de la unidad) y `tsc --noEmit` limpio. El fallo intermitente de la tarjeta se debía a leer `textContent` antes de que resolviera el efecto de IndexedDB; se corrigió con `waitFor`, no relajando la aserción.
- **L7** (segundo lanzamiento de escritores, T5 y T6, también falló con error del harness sin dejar archivos). Ambas unidades pasan a inline. El monitor de envelope de `internal/platform/httpx/envelope_audit_test.go` se actualizó para aceptar `NewRouterWithMetrics`: la auditoría busca que `cmd/api` no construya su propio `ServeMux`, no que el constructor tenga un nombre fijo.
