# stitch-admin-prompts

Redefinir el prompt maestro de Google Stitch para implementar primero la interfaz del rol **Administrador** en desktop `1440x900px`, cubriendo las secciones administrativas planificadas en todos los artefactos de `docs/`.

## Problem

`docs/DESIGN.md` declara `[x] Cobertura Modular: 8 módulos` (`:439`) pero solo existe un prompt Admin superficial (`:424-431`): un frame dual-view sin viewport, sin tokens y sin tipografía. **15 de 24 capacidades Admin no tienen prompt.**

## Why

El DESIGN.md es la fuente única de tokens y parámetros. El prompt actual describe la estación de **cajero**, no el panel administrativo, y arrastra ticket, checkout y quick-add al diseño Admin.

## Scope

Prompts separados por pantalla (no un mega-prompt), integrando `docs/DESIGN.md` como fuente de tokens:

1. Shell + acceso Admin
2. Dashboard / auditoría
3. Cierre de caja y arqueo
4. Fiados
5. Inventario y mermas
6. Catálogo maestro y márgenes
7. *(diferido)* Ajustes de dispositivos

Fuera de alcance: migrar el POS website de Stitch a Figma; mobile/responsive.

## Constraints

- Los prompts técnicos se escriben en inglés; la prosa de `docs/` permanece en español.
- No crear `openspec/`. Artefactos SDD en Engram.
- No sobrescribir ni resetear el worktree: `ARCHITECTURE.md` modificado, `DESIGN.md` raíz borrado, `docs/` sin trackear.
- Budget de review: 400 líneas por PR.

## Tasks

- [x] **T1** SDD preflight + init guard + resolución de store (Engram)
- [x] **T2** `sdd-explore` — auditoría de cobertura Admin y drift
- [x] **T3** Decidir modelo de autorización Admin — RESUELTO 2026-10-03: **password maestra por tenant** (decisión explícita del usuario). Login en **2 pantallas** (requerimiento del usuario; ver Notas).
- [ ] **T4** `sdd-propose` para `stitch-admin-prompts`
- [ ] **T5** `sdd-spec` — contratos de datos y requisitos por pantalla
- [ ] **T6** `sdd-design` — arquitectura de prompts
- [ ] **T7** `sdd-tasks` — breakdown
- [ ] **T8** Emitir los 6-7 prompts finales

## Findings (sdd-explore)

### 1. El Admin no existe como modelo de autorización — BLOQUEANTE
Los docs exigen `Contraseña maestra` (`DESIGN.md:106`) y `PIN maestro` (`:191`), pero:
- `users` no tiene columna de rol ni display name (`DATABASE.md:191-206`)
- no hay endpoint admin (`ARCHITECTURE.md:122-135`)
- el JWT solo carga `tenant_id` + `user_id` (`ARCHITECTURE.md:23`)

No se puede diseñar una pantalla de login Admin para un modelo no definido.

### 2. Dashboard Admin sin tareas de sprint
Solo aparece en `DESIGN.md:221-231` y `FRONTEND.md` TASK 8.2. Sprints 0-7 (`SPRINTS.md:53-170`) no lo mencionan. La autoridad de scope está disputada.

### 3. Drift dentro de docs/
- `IGV/Tax` y `Descuentos` sin columna en el schema (`DATABASE.md:311`) → `IGV (18% inc.)` sin respaldo
- Diálogo de conflicto muerto por D8 (`DATABASE.md:790,806`) pero vivo en `DESIGN.md:255`; el follow-up #157 olvidó incluir DESIGN.md
- `cash_closes` sin float inicial ni egresos (`DATABASE.md:431-439`); la fórmula de diferencia (`DATABASE.md:485`) no representa emergencia (`:196`)
- Asistencia, listing de turnos y WebUSB son post-MVP (`SPRINTS.md:39,212`) pero ya aparecen en los prompts
- **No existe token de color `card`** en §2.1 (`:49-52`) aunque Tarjeta se pinta azul (`:417`)

### 4. Drift entre el prompt del usuario y docs/
- `Mariluz` vs `María Gómez` (`:126,340,364,390`)
- `400px` vs `380px` (`DESIGN.md:339,444`, `FRONTEND.md:113`)
- `S/ 24.10` no existe en ningún doc
- pill `Golosinas` no existe en docs
- `Vaciar [Esc]` pisa la semántica de `Esc` (`:309`)
- `glow /20` vs `/30` (`:48`)
- `badge-fiado` usa dos hex; docs solo `#F59E0B` (`:49`)
- El prompt nunca declara el valor del glow

### 5. Aritmética
Items: `4.80×2 + 8.50×1 + 3.00×2 = 24.10` ✓ pero la línea dice `Subtotal S/20.42`. Si IGV 18% está incluido en el total, 20.42 es la base imponible, no un subtotal — la convención de retail usual es pre-impuesto.

### 6. Análisis del prompt maestro cajero 1440x900 (2026-10-03, verificado con refs de línea)
El prompt maestro del cajero es fiel al frame Stitch 5 de `DESIGN.md` (layout 3 columnas, enum de tenders EXACTO a `DATABASE.md:310`, viewport consistente con `DESIGN.md:334`), pero:
- **Cobertura total**: renderiza 1 de 37 pantallas declaradas (`FRONTEND.md:34-35`: 22 desktop + 15 mobile) y 1 de 6 prompt-screens de `DESIGN.md`. **0 de 7 estados críticos obligatorios** (`DESIGN.md:249-256`) están representados.
- **Flujos estructuralmente imposibles en un frame estático** (el "no hay flujo exacto"): post-COBRAR (modal + billetes + vuelto, `DESIGN.md:157-176`), fiado→buscador deudor (`:166`), PIN admin (`:104,191`), arqueo/cierre (`:193-198`), offline "X ventas por sincronizar" (`:254`), conflicto server-wins (`:255`), estados de cámara (`:250-252`), carrito vacío/COBRAR disabled (`:253`), éxito+share sheet (`:256`).
- **Drifts verificados nuevos**: `Mariluz Gómez` no existe en ningún artefacto (docs dicen `María Gómez`, `DESIGN.md:126,340,364,390`); línea `Descuentos` (`DESIGN.md:289`) omitida y reemplazada por IGV sin respaldo de schema; atajos **F3** (`:307`) y **Supr** (`:310`) faltan; `Esc` re-ligada a Vaciar pisa la semántica documentada (`:309`); nombres/precios de productos de ejemplo y deuda `S/ 640.00` sin respaldo documentado (herencia prompt-a-prompt desde `odd/tasks/pos-design-review-stitch.md:62`); `400px` dentro del rango 380-420 (`:281,318`) pero desvía del valor pineado `380px` (`:339,444`, `FRONTEND.md:113`).
- **Confirmados del T2**: glow `/20` vs `/30` (`DESIGN.md:48`); `#D97706` extra en badge-fiado (`:49`); pill `Golosinas` inventada (docs: `:343,367`); token `card` `#2563EB` no existe en §2.1 (`:37-53`); IGV sin columna en schema (`DATABASE.md:303-360`).

## Open Questions

1. **¿Cómo se modela la autorización Admin?** — **RESUELTA (2026-10-03): password maestra por tenant.** Implicación de contrato pendiente de spec: `tenants.master_password_hash` no existe en `DATABASE.md`; no hay endpoint admin (`ARCHITECTURE.md:118-135`); el JWT solo carga `tenant_id + user_id` (`:23`). La spec debe proponer el delta de schema/API.
2. ¿El dashboard Admin entra al MVP? No está en ningún sprint.
3. ¿IGV es real o se elimina? Sin columna no hay contrato.
4. ¿Se conserva `Descuentos` sin persistencia?
5. ¿Los servidores y las 5 tiendas dividen por tenant?

## Verification

- `sdd-explore`: completado, validado inline contra los cinco artefactos de `docs/`
- Store: Engram. Mirror de este documento persistido en `odd/stitch-admin-prompts/tasks` (observación 351, 2026-10-03). El write path volvió a funcionar.

## Next Step

T3 resuelto (password maestra por tenant). Login en 2 pantallas definido como requerimiento (ver Notas). Siguiente: **T4 `sdd-propose`** — Interactive, store Engram, delivery ask-on-risk.

## Notes

**Delivery (2026-10-03).** Forecast T7: chained PRs Yes, ~1.270-1.920 líneas, budget risk Medium. El usuario declinó PRs: **"No aplicaremos PRs por el momento, es solo documentación"**. La suite de prompts se emite en T8 directamente a `docs/` (y sus deltas a `DATABASE.md`/`ARCHITECTURE.md`/`FRONTEND.md`), sin PRs. Commit/push/PR siguen siendo decisiones del usuario bajo política ordinaria del repo.

**Login en 2 pantallas (requerimiento usuario, 2026-10-03).** La guía NO registra cómo debe verse el login: `DESIGN.md:101-106` define solo el contrato funcional ("Entrada unificada con login dual (Empleado vs. Administrador)"; Empleado = PIN 4 dígitos ligado a dispositivo, offline 7+ días, 1 dispositivo/1 cajero congelado; Administrador = Correo/Teléfono + contraseña maestra). `FRONTEND.md` no lista ningún componente de login entre los atómicos (`:37-43`). El usuario pidió que el login sea una sección dividida en **2 pantallas**: (1) selector de rol Empleado/Admin, (2) credencial según rol (PIN numérico empleado / correo-teléfono + password maestra admin). Es un requerimiento nuevo, compatible con el contrato; la spec debe fijar el layout porque no existe registro visual previo.

**Gap de schema para password maestra por tenant.** `tenants` no tiene `master_password_hash`; `users` no tiene rol (`DATABASE.md:191-206`); no hay endpoint de login admin (`ARCHITECTURE.md:118-135`); el JWT no lleva role (`:23`). La spec/design deben proponer el delta y la pantalla no debe inventar un contrato que el backend no sostiene.

**Engram write path: restaurado (2026-10-03, verificado).** El bloqueo original (`plugins/engram.ts:698` rechazando `mem_save`, `mem_save_prompt`, `mem_session_summary`, `mem_capture_passive`) ya no se reproduce: `mem_save` aceptó este documento. Causa raíz sigue siendo estado en memoria del plugin (`disposed` / `closedSessions` / `subAgentSessions`, issue upstream #116), o sea que el fallo puede volver en cualquier sesión. **No depender del write path como única copia**: este archivo es la fuente de verdad y el mirror Engram es recuperación secundaria. Si vuelve a fallar, escribir el reporte acá y seguir. Fix preventivo: upgrade a v2.2.1 + `engram setup opencode`. Ver `~/.engram/backup-all-20261003.json` y topic `tooling/engram-plugin-write-block`.

## T8 — Apply (emisión de prompts)

**Delivery:** sin PRs (decisión usuario 2026-10-03: "es solo documentación"). Work-unit commits por prompt en `feat/stitch-admin-prompts`, conventional commits, sin push/PR. Verificación por work unit = anti-drift estático (footer diff + grep de tokens prohibidos + resolución de refs de línea); runners `go test ./...` / `vitest` no existen aún en disco (SPRINTS.md:178), no se reclaman corridas que no pueden existir.

| WU | Prompt / delta | Commit | Verificación anti-drift |
|----|----------------|--------|--------------------------|
| 1 | P1 Login-1 + scaffold §7.1 + FRONTEND mapping (conteo 30 + Login-1) | `76ec23d` | Banned-token scan limpio en bloque P1 (hits solo en contenido pre-existente :53/:149/:289/:348); refs del footer resuelven contra docs vivos |
| 2 | P2 Login-2 + badge-card §2.1 (D6) + DATABASE tenants `master_password_hash` (D1) + ARCHITECTURE `/auth/admin-login` + role claim (D2/D3) + FRONTEND Login-2 | `0a51866` | badge-card en §2.1 + Block T; 'Correo' ausente como label de campo (phone pinned D9); nota D1 verbatim §12; fila endpoint en ARCHITECTURE.md:126; refs del footer resuelven |
| 3 | P3 Shell + acceso Admin + FRONTEND chrome Admin | `fb37894` | 'Ajustes de dispositivos' solo como negación (ADS-2.2); Mariluz/400px cero; Bodega San Pedro/María Gómez doc-backed; phone/versión spec-fixed flaggeados |
| 4 | P4 Dashboard + nota ARCHITECTURE D12 (deferred) + FRONTEND KPIs | `3c6530a` | Attendance solo como ausencia declarada (ADD-4.2); IGV/Descuentos/400px/Mariluz cero en bloque; enum 6 tenders exacto; SPRINTS.md:39/:214 (nunca :212); sample consistente (Σ=280, 280/14=20, 43+32+25=100, fiado 25.00=40−15) |
| 5 | P5 Cierre de caja + nota DATABASE D10 (deferred) + FRONTEND arqueo | `38783b4` | apertura/egresos solo como negación (ADC-3.1); enum 6 filas exacto; sample consistente con P4 (115 vs 120 = −5.00); solo POST /cash-closes; nota D10 en DATABASE.md:470 |
| 6 | P6 Fiados + delta DATABASE `credit_limit_cents` ADOPTED (D4) + FRONTEND fiados | `21bb25e` | Sample consistente (25.00 = 40.00 − 15.00, calza con P4); WhatsApp/wa.me solo como negación (ADF-4.1); delta verbatim §12 (BIGINT NULL, sin índice, sin CHECK) en DATABASE.md:412 |
| 7 | P7 Inventario y mermas + nota DATABASE D5 (deferred) + FRONTEND inventario | `915e15f` | camera/photo/evidence solo como negación (ADI-3.1); motivos exactos 3; umbrales semáforo flaggeados; sample doc-backed (pan/huevos/hielo) con 20→18; solo GET /products + POST /stock-adjustments; nota D5 en DATABASE.md:309-312 |
| 8 | P8 Catálogo maestro + nota DATABASE D11 (deferred) + FRONTEND catálogo | (pendiente) | (pendiente) |
