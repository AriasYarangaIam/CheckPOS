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
4. Google Stitch project created with the design system tokens; screens generated and inspected. ✅ (project + design system + 5 screens; canvas has 3, Drawer/Dashboard retrievable by ID)
5. Feature document updated through the cycle; work-unit commits on feature branch with conventional messages. 🔲 T5 pending (final commit + report).

## Applicable checks
- Read-back of edited DESIGN.md sections (gate) — PASSED (337 lines).
- Cross-check each edit against ARCHITECTURE/DATABASE/SPRINTS statements (gate) — PASSED.
- `impeccable detect` / `impeccable` context directive if required by the skill (`MANUAL_DETECTOR_REQUIRED`) — pending (depend on detector availability).
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
- [ ] T5 Close — final doc update (this file), work-unit commit, report verified outcome + pending/skipped checks; present Stitch suggestions to user (checkout modal/tender cards, Escáner view, Teclado view; Arqueo modal, Libro de Fiados, Admin PIN screen).

## Progress
- T0 ✅ inline fallback.
- T1 ✅ tracking + mirror status (mirror itself PENDING).
- T2 ✅ DESIGN.md edits (commit 1b73313).
- T3 ✅ Stitch prompt alignment (commit 1b73313).
- T4 ✅ Stitch project + design system + 5 screens (canvas 3 + 2 retrievable).
- T5 🔲 close + report in progress.

## Decision log
- 2026-09-24: Palette fork identified (light zinc vs dark tactical brief). **User chose LIGHT zinc + esmeralda**. Dark brief (`.impeccable/briefs/pos-cashier.md`) documented as rejected for this deliverable.
- 2026-09-24: Design system roundness — requested ROUND_TWELVE at creation, Stitch stored ROUND_EIGHT (documented discrepancy, no rework decision yet).

## Next step
T5: commit this feature-doc update (work-unit), then final report to user: verification outcome, DESIGN.md changes, Stitch project/design system/screens summary, and the Stitch suggestions awaiting user acceptance (checkout modal, Escáner, Teclado; plus Arqueo modal, Libro de Fiados, Admin PIN).