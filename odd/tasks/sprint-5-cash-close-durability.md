# sprint-5-cash-close-durability

## Objective

Entregar el Sprint 5 de `SPRINTS.md` (Semana 5, "Cierre de caja y durabilidad"): que los números de fin de día cuadren con el ledger incluso después de un crash, y que ninguna venta cerrada se pierda. Alcance por capa: verificación de evidencia de las dos piezas de base que ya existen (cierre insert-once y proyección por índice), `POST /cash-closes` con `expected_cents` congelado por la fórmula F8, `POST /sales/{id}/void` como evento compensatorio, cierre duplicado en cola como no-op silencioso, la documentación de sync del plan gratuito y recuperación de teléfono perdido, la persistencia local de la venta ANTES del ack con marca visible de pendientes, el modal de arqueo con seis filas y el navigation sheet con el bloque administrativo tras PIN.

## Problem

Sprint 4 está cerrado en `feat/sprint-4-counter-sale` (7 commits: verificación DB, cierre de venta, claim de idempotencia y las cuatro unidades FE). Lo que Sprint 5 necesita de él ya existe: `POST /sales` idempotente, `synckey.Claim`/`Complete` reutilizable, la proyección aditiva de stock y el carrito persistente. Falta toda la capa de cierre de caja y durabilidad:

1. No existe `POST /cash-closes`: nada congela la proyección del ledger por método ni recibe el conteo del operador.
2. No existe `POST /sales/{id}/void`: una venta cerrada no tiene evento compensatorio y el stock no puede moverse en reversa por esa vía.
3. La venta se cierra localmente y se vacía el carrito, pero **no** se persiste en `idempotency_queue` antes del ack: matar la pestaña a mitad del envío pierde el evento (y la cola de IndexedDB existe desde Sprint 1 sin un solo consumidor).
4. No hay superficie de arqueo ni navigation sheet: el cajero no puede cerrar el día desde la PWA ni llegar a otra pantalla que no sea la venta.

## Why

Pedido explícito del usuario (2026-10-20): desarrollar el Sprint 5 siguiendo `SPRINTS.md` §"Sprint 5 (Semana 5)" y las decisiones registradas en Engram. Rama `feat/sprint-5-cash-close-durability` creada desde el tip de `feat/sprint-4-counter-sale` (`33ff894`), porque el cierre de caja depende de la venta idempotente que ese corte entregó. La cadena de PRs queda apilada sobre la rama del Sprint 4 hasta que el usuario decida el apilado de ambas.

## Scope

Dentro:

1. **W5-DB-1** Verificación de evidencia: `cash_closes` insert-once (`UNIQUE (tenant_id, closed_on)`, sin grant de UPDATE/DELETE) y `cash_close_counts` append-only con PK `(cash_close_id, payment_method)` y el `CHECK` de `counted_cents = 0` para `fiado`. Tarea de evidencia: escribir migración solo si la verificación falla.
2. **W5-DB-2** Verificación de evidencia: la proyección de dinero por método se sirve con `sales_tenant_id_sold_on_payment_method_idx` y pruning de partición. Escribir solo si falla.
3. **W5-BE-1** `POST /cash-closes`: congela `expected_cents` por método con F8 (`SUM(CASE kind WHEN 'sale' THEN +total_cents WHEN 'void' THEN -total_cents END)`) y recibe `counted_cents` del operador. La diferencia es computada, nunca almacenada.
4. **W5-BE-2** `POST /sales/{id}/void`: anexa una fila `kind = 'void'` que espeja `payment_method` y `total_cents` positivos del original, enlaza por la clave compuesta y revierte el stock con un delta nuevo y positivo (`source = 'void'`). La venta original se conserva íntegra.
5. **W5-BE-3** Cierre duplicado en cola como no-op silencioso: prevalece el cierre existente y el duplicado se confirma con la proyección existente (`200`), nunca con un error visible al cajero.
6. **W5-BE-4** Documentar la sincronización del plan gratuito y la recuperación de teléfono perdido (re-login restaura el historial desde el servidor).
7. **W5-FE-1** Persistir la venta cerrada en IndexedDB **antes** del ack y marcar en la interfaz las ventas todavía sin sincronizar.
8. **W5-FE-2** Modal de arqueo y cierre de caja con una fila por método (`expected` / `counted` / `difference`) sobre los seis valores y la fila de fiado informacional con contado bloqueado en cero.
9. **W5-FE-3** Navigation sheet con los destinos operativos y el bloque de administración protegido por PIN.

Fuera: la reconciliación silenciosa de violaciones de unicidad inbound y el motor de sync del cliente (Semana 6); fiados, cartera y movimientos compensatorios de fiado (Semana 7); RLS/sync de cursor; fondo inicial y egresos del arqueo (D10 diferido, `docs/BACKEND.md` §11.2); `PATCH`/`GET /sales`.

## Constraints

- `TDD estricto` (convención activa del proyecto): test que falla primero, mismo commit que el comportamiento; RED→GREEN→refactor por tarea.
- Runners: `go test ./...` (HTTP in-process con `httptest.NewRecorder` + router; Postgres por `docker compose up -d`; bases `pos_test_` desechables); `vitest --run` en `web/` y `tsc --noEmit`.
- Contratos: `docs/DATABASE.md` §4 (`sales`, `cash_closes`, `cash_close_counts`, `stock_adjustments`), §7.1 (protocolo de claim, ramas A/B/C), §7.7 (cierre duplicado, F38), §8 D9/D10/D11; `ARCHITECTURE.md:41` (cierre como proyección), `:132` (anulación no borra), `:135` (idempotencia del cierre), `:168` (durabilidad); `docs/BACKEND.md` §4.10, §4.13, §6.9.
- El servidor nunca recomputa dinero de venta: `expected_cents` es una proyección de `sales.total_cents`, no un recálculo de líneas.
- `expected_cents`/`counted_cents` son `BIGINT` centavos (§2.3); `qty` viaja como texto.
- Ni `UPDATE` ni `DELETE` sobre una venta, una línea o un conteo de cierre: el compensatorio es una fila nueva (§2.12, MD-2).
- Sobre de error único `{ error: { code, message, details? } }` en toda respuesta de error (§4.1).
- Commits de unidad de trabajo con Conventional Commits, sin `Co-Authored-By`; comentarios en el estilo del repo (español), mensajes de commit en inglés.
- Push/PR: requiere confirmación explícita del usuario (decisión de Engram, patrón Sprints 3 y 4).

## Decisiones de forma registradas (`(propuesta)` donde el contrato no fija el JSON)

1. **Cuerpo de `POST /sales/{id}/void`: `{ "sold_on": "YYYY-MM-DD" }` obligatorio.** La PK de `sales` es `(sold_on, sale_id)` y la partición se elige por `sold_on`: sin la fecha de negocio del original, la búsqueda por `sale_id` recorre todas las particiones. El cliente la conoce (viaja en el payload congelado de la venta). Un `sold_on` que no contiene la venta responde `404`, la misma respuesta que «fuera del tenant».
2. **Acuse de la anulación: mismo sobre que §4.9** (`{ sale, products }`) con `kind = "void"` y `original_sale_id`/`original_sold_on`, para que el claim almacene un snapshot con la misma forma y el cliente re-renderice stock igual que en el cierre.
3. **Cuerpo de `POST /cash-closes`: `{ id, closed_on, counts: [{ payment_method, counted_cents }] }` con exactamente las seis filas**, una por método. Un cierre es la proyección por método: omitir un método escondería dinero. `counted_cents != 0` para `fiado` responde `422` (además del `CHECK`).
4. **Acuse del cierre: `{ cash_close: {...}, counts: [{ payment_method, expected_cents, counted_cents, difference_cents }] }`.** `difference_cents` se computa (`counted − expected`) y **no se almacena**; para `fiado` viaja `null` porque su diferencia es informacional por diseño y nunca un faltante (§4.13, F32).
5. **Sin movimiento compensatorio de fiado en la anulación.** `fiado_movements` no tiene aún ningún camino de escritura (Semana 7); la anulación de una venta a fiado mueve solo el stock. Diferencia declarada contra `docs/BACKEND.md:1049` ("stock y fiado se mueven en reversa") que se cierra en W7-BE-2.
6. **Destinos del navigation sheet sin superficie en esta versión** (Fiados, Inventario, Mermas, Asistencia) se listan como no disponibles en lugar de navegar a una pantalla inexistente; Punto de Venta y Caja sí navegan. Ajuste declarado del criterio de `SPRINTS.md` W5-FE-3: no se inventan pantallas que son de Sprints 7 y 8.
7. **La proyección esperada del arqueo en el cliente sale del ledger local** (filas de `idempotency_queue` con la venta/anulación congelada de esa fecha de negocio, agrupadas por método), porque el MVP no tiene endpoint de reporte (`docs/BACKEND.md:549`). El servidor congela su propio `expected_cents` y el acuse es la verdad de lo que quedó guardado.

## Tasks

- [ ] **T1** Documento del sprint + rama `feat/sprint-5-cash-close-durability` desde `33ff894` — RUTA: inline. Evidencia: este archivo.
- [ ] **W5-DB-1** Verificación de evidencia del cierre insert-once y del conteo append-only — RUTA: inline (lectura; migración solo si falla).
- [ ] **W5-DB-2** Verificación de evidencia del índice de proyección por método — RUTA: inline (lectura; migración solo si falla).
- [ ] **W5-BE-1** `POST /cash-closes` con proyección congelada por método — RUTA: delegada (writer BE; disparadores: 4+ archivos nuevos no triviales).
- [ ] **W5-BE-2** `POST /sales/{id}/void` como evento compensatorio con reversa de stock — RUTA: delegada (mismo writer BE).
- [ ] **W5-BE-3** Cierre duplicado como no-op silencioso (`200` con la proyección existente) — RUTA: delegada (mismo writer BE).
- [ ] **W5-BE-4** Documentación de sync del plan gratuito y recuperación de teléfono perdido — RUTA: delegada (mismo writer BE; 1 documento nuevo).
- [ ] **W5-FE-1** Venta persistida antes del ack + marca de pendientes en la interfaz — RUTA: delegada (writer FE; disparadores: 3+ archivos no triviales).
- [ ] **W5-FE-2** Modal de arqueo y cierre con seis filas — RUTA: delegada (mismo writer FE).
- [ ] **W5-FE-3** Navigation sheet con bloque administrativo tras PIN — RUTA: delegada (mismo writer FE).
- [ ] **T-CLOSE** Verificación completa: `go test ./...`, `vitest --run`, `tsc --noEmit` y criterios 1:1 — RUTA: inline.

## Authorized scope

Escritura: `internal/cashclose/` (nuevo paquete del módulo Cash close), `internal/ledger/` (anulación y su handler), `internal/catalog/` (reversa de stock), `cmd/api/main.go` (montaje de rutas), pruebas de todo lo anterior, `docs/RECOVERY.md` (nuevo, W5-BE-4), `migrations/` solo si W5-DB-1/2 fallan, `web/src/`, este documento y su espejo Engram. Lectura: todo el repositorio y sus contratos. Prohibido: `git push`, PR, cambios de sustancia en `ARCHITECTURE.md`/`docs/DATABASE.md`, y tocar `feat/sprint-4-counter-sale`.

## Acceptance criteria

- [ ] El cierre es una proyección de los eventos del ledger, nunca un segundo contador (`expected_cents` congelado por F8; ninguna columna de saldo corriente).
- [ ] Existe exactamente un cierre por fecha de negocio por tenant y no hay camino de enmienda en el MVP.
- [ ] `counted_cents = 0` para `fiado` está forzado por `CHECK` y por validación de superficie; la diferencia de fiado es informacional y nunca un faltante.
- [ ] Una anulación conserva la venta original y mueve el stock en reversa; una anulación de anulación se rechaza.
- [ ] Una venta cerrada se recupera tras matar la pestaña (evento en IndexedDB) y tras perder el teléfono (evento en el servidor).
- [ ] El arqueo muestra seis filas con la diferencia por método y sin fondo inicial ni egresos.
- [ ] Matar la pestaña a mitad de la cola no pierde ninguna venta cerrada después de reiniciar.

## Checks

- `go test ./...` con el Postgres de Docker (`docker compose up -d`); `vitest --run` y `tsc --noEmit` en `web/`.
- Verificación de lectura contra `docs/DATABASE.md` §4/§7.7 y `docs/BACKEND.md` §4.10/§4.13 en cada corte.
- Review workload forecast: BE ~500-700 líneas y FE ~600-800 (TDD las infla). Acumulado por encima de ~400 → cadena heredada de Sprint 3/4 (PRs apilados a `develop`, cortes BE + FE).

## Delivery decision (heredada de Sprints 3 y 4)

Estrategia: **PRs encadenados** · cadena: **stacked**. Slice 1 = W5-DB-1/2 + W5-BE-1..4; slice 2 = W5-FE-1..3. El corte 1 se apila sobre `feat/sprint-4-counter-sale` (Sprint 4 aún sin PR); el apilado de ambas ramas a `develop` es decisión del usuario.

## Progress

- 2026-10-20 · Documento creado en el arranque del sprint; rama `feat/sprint-5-cash-close-durability` desde `33ff894` (tip del Sprint 4). Estrategia de entrega heredada: PRs apilados.

## Next step

Ejecutar el slice BE (W5-DB-1/2 verificación + W5-BE-1..4) con TDD estricto y luego el slice FE (W5-FE-1..3). Push/PR quedan a la espera de confirmación explícita del usuario.
