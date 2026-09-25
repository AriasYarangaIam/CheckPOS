# ODD Feature: pos-design-review-stitch

## Objective
Verify and approve the UI/UX proposal in `DESIGN.md` (impeccable / design-schema 2 + Stitch:google-stitch-spec 1.0) against the business purpose in `sistema_pos_saas_bodegas_minimarkets.md` and the frozen technical contracts (`ARCHITECTURE.md`, `DATABASE.md`, `SPRINTS.md`). Adjust interactivity where the proposal contradicts the MVP scope or misses required states, improve the design so it can be launched in Google Stitch (project + design system + screens generated from updated prompts).

## Problem
- `DESIGN.md` is strong on craft (60-30-10, single-thumb ergonomics) but contains scope contradictions with the frozen MVP contracts:
  1. Camera/scanner is the default active tab; architecture mandates **favorites grid as happy path, camera best-effort** ("grid stays the default if camera p95 > 3s").
  2. Checkout gives WhatsApp ticket a primary role (`wa.me` path is explicitly OUT of MVP; MVP = on-screen ticket + native share sheet).
  3. Módulo 0 shows multi-cashier/shift photo picker; MVP = 1 device, 1 cashier, device-bound PIN unlock (offline 7+ days).
  4. Payment tenders: `DATABASE.md` CHECK has SIX values (cash, yape, plin, card, other, fiado); `DESIGN.md` shows 4 and collapses Yape/Plin into one tile, missing `other` and single-tender semantics.
  5. Missing required states for a credible design system / Stitch generation: scan permission denied, camera unavailable fallback, barcode not found, empty-cart disabled, pending-sync offline badge, quota degraded, conflict "server vs device", sale success state (prototype uses `alert()`/`prompt()`).
  6. Interactivity promised (beep, haptic) not spec'd in DESIGN.md; no motion spec (scan success flash, change count-up, success check, reduced-motion respect).
- Palette conflict to resolve with user: `.impeccable/briefs/pos-cashier.md` OWN-WORLD is DARK tactical (Slate #0F172A, cyan scanner #06B6D4); the actual proposal + prototype + PRODUCT.md brand commitments are LIGHT zinc + emerald.

## Why
User explicitly asked: verify/approve the impeccable proposal, adjust interactivity so it complies with the established purpose, run an ODD cycle for better scope, and improve it for a Google Stitch launch.

## Scope
- **In**: `DESIGN.md` edits (evolution pass, not redesign), updated Stitch prompts inside it, new Google Stitch project + design system + screens generated from updated prompts, feature tracking, work-unit commits (no push/PR unless requested).
- **Out**: `prototype_pos.html` changes (optional follow-up), all backend/code implementation, multi-cashier features (post-MVP, documented only).

## Constraints
- ARCHITECTURE.md supersedes the original pitch wherever they disagree. DATABASE.md amends it (six tenders, integer cents). SPRINTS.md is the scope freeze.
- Money: integer cents backend, half-up rounding client; display "S/" with two decimals.
- Single tender per sale (no split tender in MVP).
- Palette: pending user decision (light zinc keep vs dark tactical brief).
- MVP comments/artifacts in English by convention; doc files stay as authored (ES) except explicit request.

## Acceptance criteria
1. DESIGN.md modules 0-2 and the tickets/checkout flow are consistent with the frozen contracts (grid-first default, no wa.me primary, PIN device login, six tenders).
2. New "States & Feedback / Motion" section covers the missing states and interaction specs above.
3. Stitch prompts in DESIGN.md are coherent with the edited modules.
4. Google Stitch project created with the design system tokens; screens generated and inspected (thumbnails/snapshot review).
5. Feature document updated through the cycle; work-unit commits on feature branch with conventional messages.

## Applicable checks
- Read-back of edited DESIGN.md sections (gate).
- Cross-check each edit against ARCHITECTURE/DATABASE/SPRINTS statements (gate).
- `impeccable detect` / `impeccable` context directive if required by the skill (`MANUAL_DETECTOR_REQUIRED`).
- Stitch screen inspection after generation.

## Tasks
- [ ] T0 Exploration — IN PROGRESS: mapped all docs + prototype. Delegation attempted twice (`explore`), failed with transport error `OpenCode's free tier can only be used from within OpenCode` (task_ids ses_f2946df3affezTaO44bzCsBEyt, ses_f2946b332ffeofuo55e9T0NjTr); inline fallback used (documented route: inline).
- [x] T1 Tracking — feature doc created on branch `feat/pos-design-review-stitch` (commit b7b005e). Engram mirror `odd/pos-design-review-stitch/tasks` PENDING — mem_save rejected twice ("could not confirm Engram session registration"); local file is source of truth until mirror succeeds. Resync on next available write.
- [ ] T2 DESIGN.md edits — tender model (6 values, single tender), grid-first happy path + feature toggle, on-screen ticket + share sheet (WhatsApp optional post-MVP), MVP-scoped PIN login, States & Feedback + motion/interactivity spec, palette per user answer. Route: inline (1 decision-heavy file).
- [ ] T3 Stitch prompt update — align prompts with edited modules (grid default, tenders, success ticket, states).
- [ ] T4 Google Stitch launch — create project, design system (or reuse), generate screens from updated prompts, inspect.
- [ ] T5 Close — update doc, work-unit commit(s), report verified outcome + pending/skipped checks.

## Progress
- T0 complete (inline fallback, evidence above).
- T1 in progress.
- T2-T5 pending.

## Decision log
- 2026-09-24: Palette fork identified (light zinc vs dark tactical brief); asking user once before T2. (Pending)

## Next step
Present verification report + one focused question (palette + any scope confirm), then start T2 after the answer.