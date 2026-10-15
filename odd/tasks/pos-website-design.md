# ODD Feature: pos-website-design

## Objective
Run an ODD cycle to execute the POS windows in a NEW Google Stitch project for the WEB version of DESIGN.md, using the recent web adaptation updates already committed (601df26), verifying that those updates actually contemplate responsive behavior for the web version, adjusting the design notes where responsive is missing, and generating the web screens via the Stitch MCP create-project flow.

## Problem
- `DESIGN.md` (440 lines) already contains the "6. Adaptación Web / Desktop (1280px–1440px)" section (multi-panel 3-column layout, desktop ergonomics, keyboard shortcuts F2–F4, and Prompt Stitch 5 Desktop 1440x900), committed in 601df26 on `feat/pos-website-design`.
- Defects found on exploration:
  1. **Duplicate section numbering** — the file has two `## 6` (original Prompts 1–2 mobile + new Web Adaptation) and two `## 7` (Prompts 1–4 mobile) blocks; the "Verificación" is now `## 8`. The web section was inserted without renumbering the rest.
  2. **Responsive is partial** — the web section fixes a desktop range (1280–1440px) and a single Desktop 1440x900 prompt, but does NOT specify: responsive breakpoints (tablet/laptop collapse), how the 3-column layout collapses (3→2→1), when the sidebar becomes a drawer on narrow widths, or adaptive behavior below 1280px. "Responsive" cannot be claimed for the web version until this is explicit.
- Prior Stitch work (`projects/1666859944796849703` "POS SaaS Bodegas & Minimarkets") is MOBILE-only (all screen instances 390x844/884/895/864/849) — the web version needs its OWN project (user request) with desktop/adaptive screens.

## Why
User explicitly requested: start a fresh ODD cycle to execute the windows in a new Stitch project for the web version of DESIGN.md, with the recent file updates for the web version (previous run was mobile-only), verifying that the new notes contemplate responsive for this web version, using the Stitch MCP to create a new project for the web version.

## Scope
- **In**: verify + fix DESIGN.md responsive coverage (renumber sections, make responsive explicit: breakpoints, column collapse, sidebar→drawer, Stitch device handling), new Stitch project for web (DESKTOP/AGNOSTIC deviceType) + design system reuse/apply, generation of the web windows (screens) from updated prompts, feature tracking, work-unit commits (no push/PR unless requested).
- **Out**: backend/code implementation, mobile project rework (1666859944796849703 stays untouched), prototype edits.

## Constraints
- Keep the LIGHT zinc + emerald palette and the 60-30-10 rule (user decision 2026-09-24); do not redesign branding.
- Frozen MVP contracts stay: grid-first default, no wa.me primary, device PIN login, six tenders, single tender per sale, on-screen ticket + native share sheet.
- The new Stitch project targets the WEB version; do not reuse the mobile project but MAY reuse the existing design system asset (assets/17818953502401587827) or create a desktop-tuned one.
- Desktop craft rules from the existing section 6.2 (Emil Kowalski) remain the baseline for web ergonomics.
- MVP comments/artifacts in English by convention; doc files stay as authored (ES) except explicit request.

## Acceptance criteria
1. DESIGN.md has no duplicate section numbers; the web adaptation section explicitly specifies responsive behavior (breakpoints, 3→2→1 collapse, sidebar→drawer threshold) and the desktop Stitch prompt(s) reflect it. ✅/❌
2. A NEW Google Stitch project exists for the web version (deviceType DESKTOP or AGNOSTIC) with the design system applied, distinct from the mobile project. ✅/❌
3. Web windows generated from the updated prompts and inspected (project + design system + screens retrievable). ✅/❌
4. Feature document updated through the cycle; work-unit commits on `feat/pos-website-design` with conventional messages. ✅/❌

## Applicable checks
- Read-back of edited DESIGN.md sections (gate).
- Cross-check each edit against ARCHITECTURE/DATABASE/SPRINTS statements (gate).
- Stitch screen inspection after generation (project list/get, screen list/get).
- Engram mirror of this doc at topic `odd/pos-website-design/tasks` (mem_save if plugin unblocked; fallback documented).

## Tasks
- [x] T1 Tracking — feature doc created on `feat/pos-website-design`; Engram mirror (mem_save or documented fallback).
- [x] T2 DESIGN.md responsive fix — renumber duplicate sections (6/7/8), make responsive explicit in the web section (breakpoints incl. <1280 and tablet, 3→2→1 collapse, sidebar→drawer threshold, adaptive Stitch device handling), align Prompt Stitch 5 (Desktop) with the responsive notes; read back edited sections.
- [ ] T3 New Stitch web project — create project via Stitch MCP for the web version (DESKTOP/AGNOSTIC), apply/reuse design system (assets/17818953502401587827 or tuned web variant), verify project + design system.
- [x] T4 Generate web windows — generate the web screens from updated prompts (desktop/adaptive), inspect each, verify project state via get_project/list_screens.
- [ ] T5 Close — update this doc, commit, final user report (list windows + URLs + responsive coverage + gaps).

## Progress
- T0 ✅ Exploration: read DESIGN.md (440 lines) + Stitch list_projects (mobile project 1666859944796849703 has 12 mobile screens; older barber projects exist). Branch `feat/pos-website-design` present with 601df26 commit.
- T1 ✅ Tracking: feature doc created; Engram mirror saved as observation #334 via HTTP POST fallback (`mem_save` still blocked by plugin gate post-restart: `gentle-engram could not confirm Engram session registration`), topic `odd/pos-website-design/tasks`, session `plugin-probe-20260925`.
- T2 ✅ DESIGN.md responsive fix (in progress → done, uncommitted): renumbered sections — removed stale duplicate mobile Prompts 1–2 block (superseded by section 7 Prompts 1–4); "6. Adaptación Web / Desktop" now `responsive`; added §6.3 Breakpoints table (≥1280 3-col; 1024–1279 sidebar→icons/ticket 340–360; 768–1023 sidebar→drawer + ticket sheet; <768 falls to §§1–5 mobile) + 5 mandatory collapse rules; renamed old 6.3→6.4 Prompt 5 (Desktop 1440x900, added responsive branch note); added §6.5 Prompt Stitch 6 (Tablet vertical 768x1024, 2-panel + drawer + floating COBRAR); section 8 Verificación updated with explicit Responsive Web criterio. About 83 diff lines. **Committed `cbca5d5`** on `feat/pos-website-design`. Native RDD: off (status reports off; global mode file has a parse warning) — no review started, ordinary checks (diff readback + commit) passed; recorded assessment `off` per task.
- T3 ✅ New Stitch web project — **created** `projects/13775636729982207136` "POS SaaS Bodegas Web (Desktop Responsive)" (PRIVATE, PROJECT_DESIGN, deviceType DESKTOP pending screens). Design system tuned for web created on-project: `assets/7832418823636642244` "POS SaaS Bodegas Web (zinc + emerald)" (LIGHT, INTER, #10B981, ROUND_EIGHT, NEUTRAL, designMd includes breakpoints + desktop ergonomics + collapse rules). apply_design_system on the empty project failed (needs existing screens) → created DS directly instead. Verified via get_project (designTheme set).
- T4 ✅ Generate web windows — 4 screens generated & inspected (each verified via get_screen + first one spot-checked structurally from HTML: sidebar, COBRAR, Ticket, #10B981, F2/F4 present; flexbox layout). Screens in `projects/13775636729982207136`:
  1. `3f872264d717477994013ef6bb5628e4` — "POS Mostrador Caja - Bodega San Pedro" (Desktop 3-col cashier, ≥1280 branch; Prompt 5).
  2. `30d88f21b37f4da8a54273e2715dd15e` — "POS Mostrador Tablet (768x1024)" (tablet branch 6.3: top bar, full-width catalog 3-col, drawer overlay, bottom-sheet peek + floating COBRAR; Prompt 6).
  3. `b3d6c175ffd74af8834f76d42f2e92d7` — "POS Tablet (768x1024) - Bottom Sheet Ticket Expandido" (expanded ticket sheet: IGV breakdown, 6 tenders single-select with [1]-[6], COBRAR [F4]).
  4. `259a0436bd3349cea7e82613a31e52b8` — "POS Mostrador - Modal de Cobro & Vuelto Rápido" (desktop centered dialog ~520px: 6 tenders, fast cash chips S/10-100 + Exacto, Recibido S/ 50.00, VUELTO S/ 23.50 emerald box, FINALIZAR VENTA [Enter]).
  CAVEAT: all 4 screens report `deviceType: DESKTOP` at the API level even for the tablet requested ones (project-level deviceType inheritance); content follows the 768x1024 tablet layout per prompt. `list_screens` returned only the first screen; each subsequent screen is retrievable individually via get_screen (verified).
- T5 🔲 close (update doc + commit + report) — in progress.
- T5 🔲 pending (close + report).

## Decision log
- 2026-09-25: New web-dedicated Stitch project required; mobile project (1666859944796849703) untouched.
- 2026-09-25: Responsive must be explicit in DESIGN.md before generating web screens (verification requested by user).
- 2026-09-25: Stale duplicate mobile prompts (old "6" block) removed — not lost; the newer Prompts 1–4 live in section 7 and are the operative mobile prompts.

## Next step
T5 close: commit feature doc, deliver final user report (windows list + accessible URLs + responsive coverage + honest caveats).