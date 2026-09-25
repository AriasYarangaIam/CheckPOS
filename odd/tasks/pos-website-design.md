# ODD Feature: pos-website-design

## Objective
Adapt the mobile-first point-of-sale and SaaS platform design into a full-featured, responsive **Website Desktop / Web App (1280px–1440px viewport)** and marketing/landing portal, strictly applying the design craft of **Impeccable** and the **Emil Kowalski design engineering principles** (unseen details compound, 60-30-10 palette, spatial consistency, zero-delay keyboard interaction, and tactile feedback).

## Problem
The previous design work focused exclusively on mobile smartphones (390x844px single-thumb). In real retail environments (minimarkets, larger bodegas, and SaaS administration), cashiers and owners use:
1. All-in-one desktop counter stations or laptops with landscape displays (1280x800 to 1920x1080).
2. Keyboard-first operations (function keys F1–F12, Enter, barcode scanner bursts < 40ms, direct numpad).
3. Two-pane or three-pane persistent split layout (Side Navigation + Live Product Catalog Grid / Scanner + Persistent Cart / Order Summary Panel).
4. Desktop admin dashboard, multi-column reports, and marketing/onboarding portal.
Without desktop website adaptation, the system cannot function on standard counter PC hardware or deliver the primary website experience.

## Why
User explicitly requested:
> "necesito que apliques correctamente las skills por default que tienes tanto en impeccable y las obtenidas por emil kowalski para poder crear el diseño website de la aplicacion... Te corresponde adaptarlo a website, que era la idea principal del producto. Por ello, usando ODD, genera las secciones correspondientes en un nuevo proyecto, tomando en cuenta los documentos actuales que se tienen junto a DESIGN.md"

## Scope
- **In**:
  - `DESIGN.md` expansion: New dedicated section **Adaptación Web / Desktop (1280px–1440px)** detailing layout, keyboard shortcuts, multi-pane architecture, and desktop Stitch prompts.
  - Interactive Desktop Web Prototype `prototype_pos_web.html`: Single-page responsive desktop web application with:
    - Left persistent sidebar (Nav & Cashier status).
    - Center catalog grid with quick categories, search, and bulk/granel selector.
    - Right sticky order panel with live cart line items, tax breakdown, and quick payment tender buttons.
    - Modal checkout dialog with instant change calculation, keyboard shortcuts, and print preview.
    - Polished motion & interaction engineering (Emil Kowalski rules: no animation on rapid keystrokes, active scale 0.98 on clicks, clean focus rings, tab navigation).
  - Work-unit conventional commits on feature branch `feat/pos-website-design`.
- **Out**: Backend Go API implementation, native hardware drivers (WebUSB/Serial).

## Constraints
- Palette: 60-30-10 Light Zinc + Emerald (`bg-zinc-50`, `bg-white`, `border-zinc-200`, `emerald-600` accent `#10B981`).
- Money: BigInt integer cents backend, rounded half-up, displayed in Peruvian Soles (`S/`).
- Typography: Inter for UI chrome, JetBrains Mono with tabular figures (`tnum`) for monetary values.
- Emil Kowalski principles:
  - Do NOT animate frequent keystroke-driven actions (typing in search, hitting Enter).
  - Use `ease-out` for dropdowns and dialogs.
  - Scale down `:active` state to `0.98` for tactile button response.
  - Explicit transition properties (never `transition: all`).

## Tasks
- [ ] **T1: Plan & Design Document Expansion** — Update `DESIGN.md` with Section 6: "Adaptación Web Desktop (1280px–1440px): Arquitectura Multi-panel, Navegación de Teclado y Prompts Stitch".
- [ ] **T2: Interactive Desktop Web Application Prototype** — Create `prototype_pos_web.html` showcasing the full 3-column desktop layout (Sidebar, Catalog/Scanner center, Persistent Cart right) with keyboard shortcut support (F2 Buscar, F4 Cobrar, Esc Cancelar) and Emil Kowalski interaction physics.
- [ ] **T3: Stitch Desktop Prompts & Verification** — Verify prompts for Google Stitch desktop screens and ensure design system alignment.
- [ ] **T4: ODD Verification & Close** — Run checks, ensure commit traceability, and report outcome.

## Delivery Strategy
- Strategy: `single-pr`
- Forecast: ~350 authored lines.
- Feature Branch: `feat/pos-website-design`
