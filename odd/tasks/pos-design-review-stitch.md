# ODD Feature: pos-design-review-stitch

## Objective
Verify and approve the UI/UX proposal in `DESIGN.md` (impeccable / design-schema 2 + Stitch:google-stitch-spec 1.0) against the business purpose in `sistema_pos_saas_bodegas_minimarkets.md` and the frozen technical contracts (`ARCHITECTURE.md`, `DATABASE.md`, `SPRINTS.md`). Adjust interactivity where the proposal contradicts the MVP scope or misses required states, improve the design so it can be launched in Google Stitch (project + design system + screens generated from updated prompts).

## Problem
- `DESIGN.md` is strong on craft (60-30-10, single-thumb ergonomics) but contained scope contradictions with the frozen MVP contracts (all fixed in T2):
  1. Camera/scanner was the default active tab; architecture mandates **favorites grid as happy path, camera best-effort** ("grid stays the default if camera p95 > 3s").
  2. Checkout gave WhatsApp ticket a primary role (`wa.me` path is explicitly OUT of MVP; MVP = on-screen ticket + native share sheet).
  3. Módulo 0 showed multi-cashier/shift photo picker; MVP = 1 device, 1 cashier, device-bound PIN unlock (offline 7+ days).
  4. Payment tenders: `DATABASE.md` CHECK has SIX values (cash, yape, plin, card, other, fiado); the proposal showed 4 and collapsed Yape/Plin into one tile, missing `other` and single-tender semantics.
  5. Missing required states for a credible design system / Stitch generation: scan permission denied, camera unavailable fallback, barcode not found, empty-cart disabled, pending-sync offline badge, conflict "server vs device", sale success state (prototype uses `alert()`/`prompt()`).
  6. Interactivity promised (beep, haptic) not spec'd in DESIGN.md; no motion spec (scan success flash, change count-up, success check, reduced-motion respect).
- Palette conflict resolved with user: `.impeccable/briefs/pos-cashier.md` OWN-WORLD is DARK tactical (Slate #0F172A, cyan scanner #06B6D4); the actual proposal + prototype + PRODUCT.md brand commitments are LIGHT zinc + emerald. **User chose LIGHT zinc + esmeralda (recommended)**.

## Why
User explicitly asked: verify/approve the impeccable proposal, adjust interactivity so it complies with the established purpose, run an ODD cycle for better scope, and improve it for a Google Stitch launch.

## Scope
- **In**: `DESIGN.md` edits (evolution pass, not redesign), updated Stitch prompts inside it, new Google Stitch project + design system + screens generated from updated prompts, feature tracking, work-unit commits (no push/PR unless requested).
- **Out**: `prototype_pos.html` changes (optional follow-up), all backend/code implementation, multi-cashier features (post-MVP, documented only).

## Constraints
- ARCHITECTURE.md supersedes the original pitch wherever they disagree. DATABASE.md amends it (six tenders, integer cents). SPRINTS.md is the scope freeze.
- Money: integer cents backend, half-up rounding client; display "S/" with two decimals.
- Single tender per sale (no split tender in MVP).
- Palette: **LIGHT zinc + esmeralda** (user decision 2026-09-24).
- MVP comments/artifacts in English by convention; doc files stay as authored (ES) except explicit request.

## Acceptance criteria
1. DESIGN.md modules 0-2 and the tickets/checkout flow are consistent with the frozen contracts (grid-first default, no wa.me primary, PIN device login, six tenders). ✅
2. New "States & Feedback / Motion" section covers the missing states and interaction specs above. ✅ (Section 5 Estados y Motion Spec, 7 states + inclusive motion)
3. Stitch prompts in DESIGN.md are coherent with the edited modules. ✅ (Prompts 1 & 3 updated, sections renumbered 5/6/7)
4. Google Stitch project created with the design system tokens; screens generated and inspected. ✅ (project + design system + 14 screens; canvas 10 screens + design-system instance verified; last 4 T6 retrievable by ID — canvas attach is async, do not treat as failure)
5. Feature document updated through the cycle; work-unit commits on feature branch with conventional messages. ✅ (T0-T6 closed, commits on feat/pos-design-review-stitch)

## Applicable checks
- Read-back of edited DESIGN.md sections (gate) — PASSED (337 lines).
- Cross-check each edit against ARCHITECTURE/DATABASE/SPRINTS statements (gate) — PASSED.
- `impeccable detect` / `impeccable` context directive if required by the skill (`MANUAL_DETECTOR_REQUIRED`) — RUN 2026-09-24 via `impeccable.cmd detect DESIGN.md` (exit 0, no findings; binary lives at `C:\Users\user\.config\opencode\skills\impeccable\scripts\`).
- Stitch screen inspection after generation — PASSED (canvas verified, screens exist with download URLs).

## Tasks
- [x] T0 Exploration — mapped all docs + prototype. Delegation attempted twice (`explore`), failed with transport error `OpenCode's free tier can only be used from within OpenCode` (task_ids ses_f2946df3affezTaO44bzCsBEyt, ses_f2946b332ffeofuo55e9T0NjTr); inline fallback used (documented route: inline).
- [x] T1 Tracking — feature doc created on branch `feat/pos-design-review-stitch` (commit b7b005e); mirror status update committed (5e16f58). Engram mirror `odd/pos-design-review-stitch/tasks` PENDING — mem_save rejected twice ("could not confirm Engram session registration"); local file is source of truth until mirror succeeds. Resync on next available write.
- [x] T2 DESIGN.md edits — applied and read back (337 lines): six-tender single-tender selector (matches DATABASE.md CHECK), PRODUCTS-grid-first segmented control (3 tabs) per architect happy path, scanner best-effort + non-blocking fallback, on-screen ticket + native share sheet (no wa.me in MVP), MVP device-bound PIN login (multi-cashier post-MVP), new section 5 Estados y Motion Spec (7 states + inclusive motion), token `badge-other` added. Palette decision: LIGHT zinc + esmeralda. Route: inline (1 decision-heavy file). Committed in 1b73313.
- [x] T3 Stitch prompt update — Prompt 1 (Products Grid default + 3 tabs) and Prompt 3 (6 tenders + success/share-sheet) updated; sections renumbered (5 Estados, 6 Prompts, 7 Verificación). Committed in 1b73313.
- [x] T4 Google Stitch launch — project created `projects/1666859944796849703` "POS SaaS Bodegas & Minimarkets" (PRIVATE, PROJECT_DESIGN); design system "POS SaaS Bodegas Light (zinc + emerald)" `assets/17818953502401587827` (LIGHT, NEUTRAL, #10B981, Inter, ROUND_TWELVE→stored ROUND_EIGHT, designMd covers 60-30-10 + 7 states + ergonomics); applied via update_design_system (session 15913165927038896025). 5 screens generated from updated prompts (all MOBILE 780px):
  - a753c28ac87c4dc39feb1c9efce87785 "Punto de Venta - Vista Cajero" (session 6523886583708399399) — canvas
  - f050000aaf324c0c83a96e302da814b3 "Ingreso de Monto Manual" (session 8099062398677723340) — canvas
  - 6872700f8cc64fb39de557e56a9f141a "Cobro y Cálculo de Vuelto" (session 8000886133231484877) — canvas
  - 4cf6a6b48b574333863259db163a402c "Dashboard Administrativo de KPIs" (generated in dual-view, retrievable) — NOT on canvas; regenerated individual: cd2c4312f18d4a79b91da8b181cbe52d (session 10304821578548291342)
  - 77db6b9a2e464b64a669a27528aa7c5d "Menú de Navegación - Bodega San Pedro" (generated in dual-view, retrievable) — NOT on canvas; regenerated individual: 2be1633275a04f57b16d96ddd1379afe "Menú de Navegación Lateral (Drawer) - POS" (session 348551149814137080)
  Screenshot download URLs available in stitch_get_screen responses (see session log). Canvas final state: 3 screens + design-system instance verified via stitch_get_project/stitch_list_screens.
- [x] T5 Close — feature doc updated (this file) + committed (c5be227). Engram mirror remains PENDING (3rd mem_save failure: "could not confirm Engram session registration"; local file is source of truth). Detector: impeccable detect clean. Final report to user sent (Stitch suggestions relayed).
- [x] T6 Second generation batch (user-confirmed) — 7 screens generated sequentially with single-screen prompts (designSystem assets/17818953502401587827, project 1666859944796849703, MOBILE; all exist with screenshot+HTML download URLs):
  - d84b9ae8a1cb45408574b0ed5cda169d "Módulo 0: Acceso y Login PIN - Bodega San Pedro" (session 1888620363808845314) — LOGIN declared in DESIGN.md Módulo 0 §0.1: empleado/cajero PIN 4 dígitos device-bound, online offline, sin dependencia de red; admin = contraseña maestra; selector cajero/turno post-MVP. **On canvas.**
  - ff9e25776ea44762920e995eecee0e8d "Selector de Métodos de Pago - Tender Único" (session 2040399981529963019) — 6 tenders (EFECTIVO más usado, YAPE, PLIN, TARJETA, OTRO, FIADO con note feature-toggle), single tender per sale, CTA CONTINUAR CON {tender}. **On canvas.**
  - 4067fff0148141f6867c96c43725bb3d "Punto de Venta - Escáner de Códigos" (session 18168936452132305655) — segmented Productos|Escáner|Teclado, camera viewport with emerald brackets + laser, permission-denied non-blocking toast, product result chip. **On canvas.**
  - ed8b5c578b4d4ca684265e002a5d2789 "Punto de Venta - Teclado Numérico" (session 5558573949147872446) — montos manuales, display zinc-900 JetBrains Mono, keypad 3x4 (00-0-Borrar), + Agregar al Ticket, sticky COBRAR S/ 12.60. **Retrievable by ID.**
  - 57cb88dc640d487e845e185915095fc4 "Cierre de Turno y Arqueo de Caja" (session 11552034057508568832) — conteo efectivo + quick chips S/ 10/20/50/100, banner cuadrado/faltante/sobrante, resumen ventas por tender, fiado informativo (no cuenta en caja), observaciones, CONFIRMAR CIERRE. **Retrievable by ID.**
  - fb362f925e77472dabd6d28f20436dcb "Libro de Fiados - Bodega San Pedro" (session 5819424721861842786) — cartera S/ 640.00, chips Todos/Con deuda/Sobre límite, cards cliente con límite + barra, badge Sobre límite rose, abonos, Registrar Nuevo Deudor, note share-sheet (no wa.me). **Retrievable by ID.**
  - f8ac3da956874ec986da3bd2a45bc89a "Acceso Administrador - PIN Maestro" (session 368692600529412875) — full-screen overlay, PIN 4 dots JetBrains Mono, 3x4 keypad, error inline 'PIN incorrecto — Intento 1 de 3', caminho empleado PIN, footer onboarding Módulo 0.2. **Retrievable by ID.**
  Canvas verified via stitch_get_project: 10 screens + design-system instance; last 4 T6 screens retrievable by ID (canvas attach async — recheck later if needed). Note: earlier assumption 'canvas keeps only first 3' was wrong — 4cf6a6b4/77db6b9a/2be16332/cd2c4312 all attached eventually (async cascade).

## Progress
- T0 ✅ inline fallback.
- T1 ✅ tracking + mirror status (mirror itself PENDING).
- T2 ✅ DESIGN.md edits (commit 1b73313).
- T3 ✅ Stitch prompt alignment (commit 1b73313).
- T4 ✅ Stitch project + design system + 5 screens (canvas 3 + 2 retrievable).
- T5 ✅ close + report (this doc, commit c5be227; detector clean; suggestions presented).
- T6 ✅ second batch: 7 screens generated (Login, Tender, Escáner, Teclado, Arqueo, Fiados, Admin PIN) — 3 on canvas, 4 retrievable by ID.

## Decision log
- 2026-09-24: Palette fork identified (light zinc vs dark tactical brief). **User chose LIGHT zinc + esmeralda**. Dark brief (`.impeccable/briefs/pos-cashier.md`) documented as rejected for this deliverable.
- 2026-09-24: Design system roundness — requested ROUND_TWELVE at creation, Stitch stored ROUND_EIGHT (documented discrepancy, no rework decision yet).
- 2026-09-25: Canvas attach is ASYNC — screens generated in the second batch attached progressively; verify via stitch_get_project at the end, never mid-batch.

## Next step
T6 done. Final user report: (1) LOGIN IS declared in DESIGN.md Módulo 0 §0.1; (2) 7 new screens listed; (3) gap analysis — remaining screens/sections for a complete flow are DESIGN-doc-level, not Stitch-blockers: Módulo 0.2 onboarding (K-Means brand extractor), inventarios/stock, mermas, asistencia/turnos (all post-MVP in SPRINTS), per-client fiado state, admin dashboard detail; only confirmed next action is the final commit.