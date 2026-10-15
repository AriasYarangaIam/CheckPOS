# sprints-week-plan

Refactor de `SPRINTS.md` a bloques de **semanas** con orden explícito por capa (Base de Datos → Backend → Frontend), y aplicación de la decisión del usuario sobre **reconciliación silenciosa** en `docs/DESIGN.md` y en toda línea que aún la contradiga.

## Objective

Dejar el plan de entrega del MVP expresado en semanas de trabajo con alcance por capa (DB / BE / FE), ordenado por dependencia real, con criterios de aceptación sustentados en `ARCHITECTURE.md`, `docs/DATABASE.md`, `docs/BACKEND.md`, `docs/FRONTEND.md` y `docs/DESIGN.md` — y sin ninguna expectativa de UI de conflicto en el sistema.

## Problem

1. `SPRINTS.md` secuencia en sprints con duraciones sueltas y sin decir **de qué lado** arranca cada módulo. El orden real de dependencia (contrato de datos → API que lo hace cumplir → cliente que lo consume) no está declarado.
2. Cinco líneas de `SPRINTS.md` (:61, :137, :140, :180, :198) y tres tokens de estado en `docs/DESIGN.md` (:256, y las listas de estados ausentes en :501, :560, :619) y el checklist :933 siguen exigiendo un **diálogo de conflicto servidor vs dispositivo**. La política vinculante es la contraria: reconciliación silenciosa (D8; `docs/DATABASE.md` §7.9 y §8 enmienda 3; `ARCHITECTURE.md:24,:111-116,:178`).
3. `SPRINTS.md` arrastra tres criterios de aceptación que contradicen el contrato: S0-2 pide campos para `tenders` y `sync_events` (no son tablas, el conteo es 14), y S3-2 exige **split tender** ("cash + Yape") con cinco tenders (el contrato lo prohíbe y fija seis).

## Why

Decisión explícita del usuario (2026-10-14): (a) se conserva el silencio y se corrige `docs/DESIGN.md` más toda línea que lo contradiga; (b) se refactoriza la redacción de `SPRINTS.md` en bloques de semanas, con el orden de módulos y de capas definido por criterio técnico del proyecto. Pedido: `ARCHITECTURE.md` y `docs/DATABASE.md` **no** se tocan; todo queda **untracked** (sin `git add`, sin commit).

## Scope

Dentro:
1. Refactor completo de `SPRINTS.md` a bloques de semana (Semana 1–10) con alcance por capa, criterios corregidos contra el contrato, tabla de trazabilidad de IDs previos y bloque de decisión de capas con su fundamento.
2. Retiro del diálogo de conflicto en `docs/DESIGN.md`: línea :256 redefinida como reconciliación silenciosa sin diálogo, token en las listas de estados ausentes (:501, :560, :619) y checklist §8 (:933).
3. Verificación de lectura de los dos artefactos.

Fuera: `odd/tasks/*.md` históricos salvo este documento (registros de lo hecho, no se reescribe la historia); `docs/STITCH_SUPER_PROMPT.md` (mención negativa en un prompt histórico, no contradice); cualquier `git add`/`commit`/`push`.

Autorizado después (2026-10-14, pedido explícito del usuario): re-sincronizar todos los punteros `SPRINTS.md:<línea>` del set de documentos a referencias por sección/sprint/ID, y alinear el registro de la divergencia #1. Entra `docs/BACKEND.md`, `docs/DATABASE.md`, `docs/DESIGN.md`, `docs/FRONTEND.md` y el único puntero vivo en `ARCHITECTURE.md:138`. Un sprint equivale a una semana en esta planificación (aclaración del usuario).

## Constraints

- `ARCHITECTURE.md` gana sobre `SPRINTS.md`; `docs/DATABASE.md` se lee **como enmienda** de `ARCHITECTURE.md` por su §8. Ningún criterio nuevo puede contradecir los tres.
- Registro: español neutro profesional, sin voseo, sin emojis. Convención observada: los **contratos** técnicos están en inglés (`ARCHITECTURE.md`, `docs/DATABASE.md`) y los **planes** en español (`PRODUCT.md`, `BUSINESS.md`, `DESIGN.md`, `FRONTEND.md`, `BACKEND.md`). `SPRINTS.md` es un plan → pasa a español.
- Se preservan los IDs de tarea (`S0-1`, `S1-2`, … `S7-4`) como columna de trazabilidad: `docs/BACKEND.md` §10, §11.4 y §12 los citan, y el historial de commits/Engram los usa.
- Todo lo propuesto fuera de las fuentes se marca `(propuesta)`.
- Cero escrituras fuera de los archivos del Scope. Sin commits.

## Tasks

- [x] **T1** Refactor de `SPRINTS.md` a bloques semanales con orden por capa — RUTA: delegada (writer único; dispara el disparador de escritura: 1 archivo no trivial de ~300 líneas + el disparador de preparación: leer 4+ documentos antes de escribir). HECHO 2026-10-14. Evidencia: `SPRINTS.md` reescrito; ver T3.
- [x] **T2** Retiro del diálogo de conflicto en `docs/DESIGN.md` (:256, :501, :560, :619, :933) — RUTA: inline (ediciones mecánicas, entendidas, 1 archivo, 5 líneas; conservan el conteo de líneas para no romper las referencias cruzadas internas de DESIGN.md). HECHO 2026-10-14.
- [x] **T3** Verificación de lectura de ambos artefactos + reporte de consecuencia — RUTA: inline (3 archivos leídos para decidir/verificar). HECHO 2026-10-14.
- [x] **T4** Re-sincronización de punteros `SPRINTS.md:<línea>` y alineación del registro de divergencias — RUTA: delegada (writer único; disparador de escritura: 5 archivos no triviales + disparador de preparación: leer 6 documentos antes de escribir). HECHO 2026-10-14. Evidencia: `Select-String 'SPRINTS\.md:\d'` repo-wide devuelve **1** coincidencia, `ARCHITECTURE.md:138`, cerrada después inline; cero en los cinco archivos del set.
- [x] **T5** Criterios separados de alta en el piloto — RUTA: inline (ediciones mecánicas sobre 1 archivo, 4 líneas, ya entendidas). HECHO 2026-10-14. Evidencia: Sprint 9 de `SPRINTS.md` con dos criterios cronometrados (autoservicio <10 min; asistido 5 min, `docs/BUSINESS.md:67`) en la fila `W9-FE-1`, en los criterios de aceptación, en el Definition of Done y en la nota de decisión.

## Resultado de T4

- `SPRINTS.md`: diez bloques `Sprint N (Semana N)` 1:1, uno por semana (el bloque fusionado 9–10 se partió). 571 líneas.
- `docs/BACKEND.md`: 98 citas (100 ocurrencias) re-apuntadas a sección/sprint/ID; §10 renumerado a `Sprint 1 (Semana 1)`…`Sprint 10 (Semana 10)` conservando todos los IDs de tarea; §11.4 #1 pasa a **RESUELTA (2026-10-14)** con evidencia, y las divergencias #4 y #5 quedan registradas como corregidas en el refactor. 1236 líneas.
- `docs/DESIGN.md` (7), `docs/DATABASE.md` (3) y `docs/FRONTEND.md` (1): sólo punteros. Cero cambios de sustancia de contrato.
- `ARCHITECTURE.md:138`: único puntero vivo que quedaba; cerrado inline a `SPRINTS.md` §6 y §11.
- `docs/DATABASE.md` §8: se agregó una nota de estado del follow-up (mitad `SPRINTS.md` aplicada; mitad `ARCHITECTURE.md` sin tocar). Sin cambios en las enmiendas.

## Authorized scope

Escritura: `SPRINTS.md`, `docs/DESIGN.md`, `docs/BACKEND.md`, `docs/DATABASE.md`, `docs/FRONTEND.md`, el puntero de `ARCHITECTURE.md:138`, este documento, espejo Engram. Lectura: todo el repositorio. Prohibido: `git add`/`commit`/`push` (pedido explícito del usuario: todo queda untracked); reescribir sustancia de contrato, arquitectura o esquema; editar `odd/tasks/*.md` históricos.

## Acceptance criteria

- [ ] `SPRINTS.md` está en bloques de Semana 1–10, cada bloque con alcance declarado por capa (DB / BE / FE), entregable demostrable y criterio de aceptación.
- [ ] Existe un bloque que declara el orden de arranque por capa y lo fundamenta con citas.
- [ ] Ningún criterio de aceptación exige UI de conflicto; ninguno exige split tender; S0-2 pide los campos de las 14 tablas del contrato.
- [ ] `docs/DESIGN.md` no contiene el diálogo "Servidor: S/ X — Este dispositivo: S/ Y" ni lo exige en ninguna lista de estados.
- [ ] La tabla de trazabilidad mapea cada ID previo (`S0-1`…`S7-4`) al bloque de semana nuevo.
- [ ] `git status` no muestra ningún archivo staged.

## Checks

- Docs-only: no hay runners (`go test ./...`, `vitest`) instalados y nada en este cambio los requiere. Verificación = lectura del artefacto + `Select-String` sobre patrones de contradicción (`conflict`, `split tender`, `tenders`, `sync_events`, `Servidor: S/`) + `git status`.

## Progress

- 2026-10-14 · T1, T2, T3 cerradas. `SPRINTS.md` reescrito a Semanas 1–10 con alcance por capa y IDs previos preservados; `docs/DESIGN.md` con el diálogo de conflicto retirado en las 5 líneas.
- 2026-10-14 · T4, T5 cerradas. Punteros `SPRINTS.md:<línea>` re-sincronizados en los cinco documentos (writer delegado) y criterios separados de alta en el piloto (inline). Estado final: `SPRINTS.md` 572 líneas con diez bloques `Sprint N (Semana N)`; `Select-String 'SPRINTS\.md:\d'` devuelve **cero** en el set vivo; nada staged.

## Consecuencia declarada — resuelta

`docs/BACKEND.md` citaba `SPRINTS.md` por número de línea en 98 lugares. Re-sincronizado bajo autorización explícita del usuario (T4): ya no queda ningún puntero por línea en el set de documentos. Se conservan a propósito las referencias por línea en `odd/tasks/stitch-admin-prompts.md` (5 líneas) por ser registro histórico de lo hecho.

## Next step

Pendientes declarados, ninguno bloqueante para el arranque de Sprint 1:

1. ~~Unificar el criterio de onboarding del piloto~~ — **RESUELTO 2026-10-14 (T5)**: decisión del Product Owner, dos criterios separados y ambos exigibles (autoservicio <10 min; asistido 5 min).
2. Confirmar los `(propuesta)` de `docs/BACKEND.md` §2.4–2.6 antes de escribir la primera migración (layout Go, nombres de variables de entorno, presupuestos).
3. Divergencias preexistentes de `docs/DATABASE.md` §7.1–§7.2 vs §4 (nombres de columnas de idempotencia) y §7.5 (`sale_lines` como log de stock): el plan implementa contra §4 y ambas quedan registradas en `docs/BACKEND.md` §11.4 #2 y #3.

## Checks ejecutados

- `Select-String 'SPRINTS\.md:\d'` sobre el repo: 1 coincidencia (`ARCHITECTURE.md:138`, cerrada después) y 5 líneas históricas en `odd/tasks/stitch-admin-prompts.md`, dejadas a propósito.
- `SPRINTS.md`: exactamente 10 bloques `Sprint N (Semana N)`; los 33 IDs legacy aparecen una sola vez.
- `docs/BACKEND.md` §10: `Sprint 1 (Semana 1)`…`Sprint 10 (Semana 10)`.
- `docs/BACKEND.md` §11.4 #1: RESUELTA con evidencia.
- `git diff --cached`: vacío. `git status`: sólo archivos modificados sin stage y untracked; ningún archivo nuevo creado por T4.
