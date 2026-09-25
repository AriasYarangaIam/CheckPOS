# ODD Feature: pos-remaining-screens

## Objective
Generate the remaining DESIGN.md screens (apartados restantes) in their CORRESPONDING Stitch project: mobile screens in the mobile project (`projects/1666859944796849703`), web/desktop screens in the web project (`projects/13775636729982207136`). Covers the documented gap: Módulo 0.2 onboarding (K-Means), Módulo 3.2 gestión de caja (apertura/arqueo/egresos), Módulo 5 (inventario + mermas), Módulo 7 (catálogo maestro), and the critical states of §5.1 lacking their own screen (carrito vacío, éxito de venta, offline sync pendiente, conflicto servidor/device, cámara permiso denegado, código no encontrado).

## Problem
- DESIGN.md specifies 8 core modules + 7 mandatory critical states (§5.1), but the two Stitch projects cover only part of them.
- Mobile project (1666859944796849703) has 14 screens: login PIN, admin PIN, vista cajero, escáner, teclado, monto manual, cobro/vuelto, tender único, drawer, menú navegación, cierre de turno/arqueo, libro de fiados, dashboard KPIs (x2).
- Web project (13775636729982207136) has 4 screens: mostrador desktop 3-col, tablet 768, ticket sheet expandido, modal cobro/vuelto.
- Missing on BOTH projects: módulos operativos restantes (fiados/inventario/mermas/caja/reportes) y estados críticos.

## Why
User explicitly requested: "Genera de forma correspondiente los apartados restantes del proyecto, tanto para el proyecto web como el mobile, en su proyecto correspondiente (ids de proyecto distintos)" — complete the remaining DESIGN.md screens, each in its own project.

## Scope
- **In**: new screens in mobile project (1666859944796849703) + new screens in web project (13775636729982207136), feature tracking, work-unit commit.
- **Out**: DESIGN.md edits (not requested), mobile project rework, prototype/backend, push/PR (not requested).

## Constraints
- LIGHT zinc + emerald 60-30-10 (user decision 2026-09-24); frozen MVP contracts (grid-first default, no wa.me primary, device PIN, six tenders single, on-screen ticket + native share).
- Mobile screens → MOBILE device in project 1666859944796849703 using designSystem `assets/17818953502401587827`.
- Web screens → project 13775636729982207136 using designSystem `assets/7832418823636642244` (deviceType may report DESKTOP by project inheritance even for tablet prompts — documented caveat).
- Generated screens are DESIGN VIEWS; they do not change the MVP freeze status of any post-MVP module in SPRINTS.md.
- Artifacts/comments in English; docs stay as authored (ES).

## Acceptance criteria
1. Mobile project gains screens for: onboarding (M0.2), gestión de caja (M3.2 apertura/arqueo/egresos), inventario+stock (M5), mermas/daños (M5), catálogo maestro (M7), and critical states (carrito vacío, éxito venta, offline sync, conflicto server/device, cámara denegada, código no encontrado). ✅/❌
2. Web project gains the corresponding desktop/adaptive views for the operative modules and states. ✅/❌
3. Every screen verified via stitch_get_screen (exists, title, deviceType, download URLs). ✅/❌
4. Feature doc updated; work-unit commit with conventional message. ✅/❌

## Applicable checks
- stitch_get_screen per generated screen (existence + title + HTML/screenshot URLs).
- stitch_get_project at end (count screens per project).
- Engram mirror at topic `odd/pos-remaining-screens/tasks` (HTTP fallback if mem_save still gated).

## Tasks
- [ ] T1 Tracking — feature doc created.
- [ ] T2 Mobile batch — generate remaining mobile screens (sequential, single-screen prompts, designSystem assets/17818953502401587827) + inspect each.
- [ ] T3 Web batch — generate corresponding web screens (designSystem assets/7832418823636642244) + inspect each.
- [ ] T4 Verify — stitch_get_project both projects; count screens.
- [ ] T5 Close — update doc, commit, final user report.

## Progress
- T0 ✅ Mapping: read DESIGN.md (445 lines, all modules + states), both previous feature docs (pos-design-review-stitch T0-T6, pos-website-design T0-T5), list_screens mobile (14 screens), web project state (4 screens from last cycle).
- T1 🔲 in progress: this doc.

## Decision log
- 2026-09-25: Fewer, higher-value screens per module instead of every micro-state: one screen per missing module + one screen per missing critical state (§5.1), in the project that matches the device. Web gets desktop/adaptive variants of the same modules.