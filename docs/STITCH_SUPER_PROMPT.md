# STITCH SUPER PROMPT — Peruvian POS SaaS Admin Desktop Web Interface

> **Scope:** Admin role first, desktop viewport `1440x900px` (the `>=1280px` branch).  
> **Language:** Prompt in English; UI labels, sample data, and copy in Spanish (Peru).  
> **Source of truth:** `docs/DESIGN.md` §2.1 tokens + §7.1 Admin prompts, `docs/FRONTEND.md`, `docs/DATABASE.md`, `ARCHITECTURE.md`.

---

## 0. System & Task

```text
System: Google Stitch UI Engine v1.0
Task: Generate a high-fidelity desktop web application interface for a Peruvian POS SaaS (bodegas / minimarkets). This is the ADMIN role suite, desktop 1440x900px. Generate it as 8 separate screens (P1–P8) in the order listed. Each screen must apply Block T verbatim, the global layout rules, the auth contract, and its own section constraints. Do not render mobile bottom bars, the cashier ticket panel, or deferred features listed in §7.
```

---

## 1. Project Context (read once, apply to every screen)

- **Product:** POS SaaS for micro-retail in Peru — replaces paper notebook, calculator, and heavy ERPs.
- **Users:** shop owners / administrators (this suite) and cashiers (out of scope here).
- **Currency:** Peruvian Sol, `S/` prefix, money in cents server-side, rounded to 2 decimals on screen.
- **Device:** Desktop web, landscape, primary viewport `1440x900px`. This is the `>=1280px` responsive branch.
- **Design system:** Shadcn UI primitives (Card, Button, Badge, Input, Dialog), light neutral zinc/emerald palette, strict 60-30-10 rule.
- **Motion:** Zero-latency for frequent actions; `prefers-reduced-motion` respected; no gratuitous entrance animations.
- **Offline:** Offline-first observable — connectivity badge always visible; actions queue locally when offline.

---

## 2. Block T — Token Contract (apply verbatim to every screen)

### 2.1 Color Palette

| Token | Hex | Tailwind | Role |
| :--- | :--- | :--- | :--- |
| `surface-bg` | `#F4F4F5` | `bg-zinc-100` / `bg-zinc-50` | 60% app background |
| `surface-card` | `#FFFFFF` | `bg-white` | Cards, displays, modal |
| `surface-subtle` | `#E4E4E7` | `bg-zinc-200` | Segmented controls, chips, dividers |
| `surface-border` | `#E4E4E7` / `#D4D4D8` | `border-zinc-200` | Subtle borders |
| `text-primary` | `#18181B` | `text-zinc-900` | Titles, main amounts |
| `text-secondary` | `#71717A` | `text-zinc-500` | Subtitles, labels |
| `text-muted` | `#A1A1AA` | `text-zinc-400` | Placeholders, separators |
| `accent-primary` | `#10B981` | `bg-emerald-500` | 10% accent — primary CTA |
| `accent-hover` | `#059669` | `bg-emerald-600` | Hover/active of primary |
| `accent-glow` | `rgba(16,185,129,0.3)` | `shadow-emerald-500/30` | Primary glow — **/30 NEVER /20** |
| `badge-fiado` | `#F59E0B` | `bg-amber-500` / `text-amber-700` | Fiados module — single hex only |
| `badge-yape` | `#7C3AED` | `bg-purple-600` | Yape identity |
| `badge-plin` | `#0284C7` | `bg-sky-600` | Plin identity |
| `badge-other` | `#52525B` | `bg-zinc-600` | Residual tender "Otro" |
| `badge-card` | `#2563EB` | `bg-blue-600` | Tarjeta identity (delta D6) |
| `danger-action` | `#E11D48` | `bg-rose-600` / `text-rose-500` | Delete/numpad clear, mermas, empty-cart |

### 2.2 Typography

- **Primary:** `Inter`, `-apple-system`, `BlinkMacSystemFont`, `Segoe UI`, `sans-serif`.
- **Numeric / tabular:** `JetBrains Mono`, `ui-monospace`, `monospace` with `font-feature-settings: "tnum" 1`.

| Scale | Size | Line-height | Weight | Use |
| :--- | :--- | :--- | :--- | :--- |
| `display-xl` | 36px | 40px | 900 | Giant change display (not used in admin) |
| `display-lg` | 30px | 36px | 800 | PIN display, numpad display |
| `heading-md` | 20px | 28px | 700 | Totals, modal titles |
| `body-md` | 14px | 20px | 600 | Buttons, product names |
| `body-sm` | 12px | 16px | 500 | Badges, connectivity, table cells |
| `caption-xs` | 10px | 12px | 700 | Uppercase labels |

### 2.3 Elevation & Radii

- `rounded-xl` = 12px (secondary buttons, keys).
- `rounded-2xl` = 16px (cards, displays, modal).
- `rounded-3xl` = 24px (bottom sheets — **not used on admin desktop**).
- `shadow-xs`: `0 1px 2px 0 rgba(0, 0, 0, 0.05)`.
- `shadow-keypad`: `0 2px 4px rgba(0,0,0,0.04), 0 1px 2px rgba(0,0,0,0.06)`.
- `shadow-emerald-glow`: `0 8px 24px -4px rgba(16, 185, 129, 0.35)`.

---

## 3. Global Layout Rules (apply to every admin screen)

- **Full viewport:** 60% `surface-bg` (#F4F4F5).
- **Admin shell:** fixed 240px left sidebar (`Sidebar240`) + flex-1 content pane with independent scroll.
- **NO ticket panel** on any admin screen.
- **NO mobile bottom bars**, **NO segmented controls** in this desktop variant.
- **Connectivity badge** always present: online = emerald "En línea"; offline = amber badge-fiado "Modo Offline - IndexedDB".
- **Pending-sync counter** when offline: "X ventas por sincronizar".
- **Interaction defaults:** `:active scale(0.98) 120ms ease-out`; hover `bg-zinc-50 / border-zinc-300`; primary CTA 52px min height.

---

## 4. Auth Contract (P1 + P2)

- **Login flow = 2 screens:**
  1. P1 — Role selector (Empleado / Administrador).
  2. P2 — Credential screen (variant Admin primary, variant Empleado PIN secondary).
- **Administrador:** phone + master password per tenant. Submit binds `POST /auth/admin-login`. JWT carries `tenant_id`, `user_id`, `role:"admin"`.
- **Empleado:** 4-digit PIN bound to `devices.pin_hash` + `offline_session_valid_until` (7+ days offline).
- **Identifier pinned to phone:** `users` has no email column; phone is the only identity.
- Admin submit is disabled when offline.

---

## 5. Admin Navigation (P3 shell)

Six sidebar destinations, each `>=48px`, Lucide icons, active state marked:

1. Dashboard / Auditoría
2. Gestión de Caja y Arqueo
3. Cuentas y Fiados
4. Inventario / Stock Rápido
5. Mermas y Daños
6. Catálogo Maestro

NO "Ajustes de dispositivos" in this version (deferred).

---

## 6. Banned Tokens & Anti-Drift Anchors (verify on every screen)

Do NOT use:

- `shadow-emerald-500/20` — glow is always `/30`.
- A second invented deudor name — the only documented person is **María Gómez**.
- `400px` auth card — P1 max-width ~520px, P2 ~420px.
- Ticket panel on admin screens.
- WhatsApp / wa.me buttons — native share sheet only.
- `cost_cents`, margin columns, margin alerts — deferred.
- Opening-float input or emergency-egress form on cash-close — deferred.
- Photo/evidence capture on merma modal — deferred.
- Attendance section on dashboard — post-MVP.
- Report endpoint for dashboard — deferred.

Cross-screen consistency anchors:

- Tender enum (6 values): Efectivo, Yape, Plin, Tarjeta, Otro, Fiado.
- Fiado balance of María Gómez: charge +S/ 40.00, abono −S/ 15.00 → **S/ 25.00**.
- P4 expected totals: Efectivo 120, Yape 60, Plin 30, Tarjeta 25, Otro 5, Fiado 40 → Ventas Netas **S/ 280.00**.
- P7 stock: Pan 20 (verde), Huevos 3 (amarillo), Hielo 0 (rojo).

---

## 7. Screen Prompts (P1–P8)

For each screen, generate one high-fidelity desktop interface. Each section below is a complete mini-prompt.

---

### P1 — Login-1: Role Selector

```text
Task: Generate screen 1 of 2 of the admin login: the pre-auth role selector (Empleado / Administrador). No tenant data and no credential fields.

Layout contract:
- Centered auth card on surface-bg. Card: surface-card, rounded-2xl 16px, shadow-xs, max-width ~520px.
- Product wordmark "POS SaaS" only — NO tenant name pre-auth.
- Two role cards side-by-side, min-height 160px:
  - Empleado (Lucide user icon): subline "PIN de 4 dígitos, sin depender de red".
  - Administrador (Lucide shield icon): subline "Teléfono + contraseña maestra, acceso a métricas".
- Selected role = accent-primary ring; hover bg-zinc-50 / border-zinc-300; :active scale(0.98).
- Caption-xs hint: "Usa ← → para elegir · Enter para continuar".
- ConnectivityBadge in card corner.

Keyboard map:
- ArrowLeft / ArrowRight = move selection.
- Enter = confirm role.
- Esc = no-op.
- F3, F4, 1-6 tenders, Supr = INACTIVE (station-scoped).

States:
- Online / offline connectivity badge only. Both roles selectable offline; Administrador proceeds to P2 where submit is gated.
- No camera, no code-not-found, no empty-cart, no sync-pending, no conflict, no sale-success states.

Sample data: none — wordmark only.
```

---

### P2 — Login-2: Credential Screen

```text
Task: Generate screen 2 of 2 of the admin login: the credential screen. PRIMARY render = Administrador variant (phone + master password). SECONDARY render = Empleado variant (4-digit PIN keypad), same screen, second frame.

Layout contract:
- Centered auth card ~420px, surface-card, rounded-2xl 16px, shadow-xs.
- Back affordance (icon-button, Lucide arrow-left, aria-label "Volver al selector de rol"); Esc = back to P1.
- Administrador variant:
  - Field "Teléfono" (type=tel, inputmode=tel, autocomplete="tel").
  - Field "Contraseña maestra" (type=password, autocomplete="current-password").
  - 52px accent-primary full-width button "INGRESAR".
  - Submit binds POST /auth/admin-login; disabled when offline (opacity 40%, no glow).
- Empleado variant:
  - 4-digit PIN dot display (caption-xs "PIN", display-lg 30px dots).
  - ErgonomicNumpad 3x4, 56px keys: rows 1-2-3 / 4-5-6 / 7-8-9 / 0-Borrar.
  - Borrar key = danger-action.
  - Unlock binds devices.pin_hash + offline_session_valid_until.

Keyboard map:
- Esc = back to P1.
- Enter = submit (online only for Admin).
- Tab = move fields.
- Empleado: physical digits 0-9 + Backspace + Enter.
- F2, F3, F4, 1-6 tenders, Supr = INACTIVE.

States:
- Admin offline: "INGRESAR" disabled, connectivity hint.
- Admin error 401: inline danger-action message "Credenciales incorrectas. Verifica tu teléfono y contraseña maestra." + next step, no user enumeration.
- Empleado PIN error: danger-action + vibration [30,60,30].
- Expired offline session: client refuses offline unlock, requests connectivity.

Sample data: empty fields. Error copy as specified above.
```

---

### P3 — Admin Shell + Welcome Empty State

```text
Task: Generate the post-login admin chrome: fixed sidebar navigation, content pane, and session footer. Module content belongs to P4-P8; this screen renders the shell with a welcome empty state.

Layout contract:
- Fixed 240px sidebar + flex-1 content pane.
- Sidebar:
  - Brand block: logo + tenant display name "Bodega San Pedro" + app version "v1.0".
  - ConnectivityBadge + pending-sync counter.
  - Six nav destinations >=48px (Dashboard/Auditoría, Caja, Fiados, Inventario, Mermas, Catálogo), Lucide icons, active state marked.
  - NO "Ajustes de dispositivos".
- Session footer:
  - Admin identity "María Gómez" + phone "+51 987 654 321".
  - RoleBadge "Administrador" (surface-subtle + text-primary, NOT badge-other).
  - Logout — client-side token clear → P1 (no server logout endpoint).
- Content pane heading "Dashboard / Auditoría" + welcome empty state:
  - "Selecciona un módulo para comenzar".

Keyboard map: NONE active on this screen. F2-F4, 1-6, Supr = INACTIVE.

States:
- Offline: amber badge + pending-sync counter; navigation not blocked.
- Access gate: employee token without role claim → shell does not render → P1 (behavior, not rendered).
- Logout → P1.

Sample data:
- Brand: "Bodega San Pedro".
- Admin: "María Gómez", "+51 987 654 321", version "v1.0".
- Active nav: Dashboard / Auditoría.
```

---

### P4 — Dashboard / Auditoría

```text
Task: Generate the admin KPI dashboard: net sales, tender split, outstanding fiado, shift audit list, and peak-hours chart. Data sources: local IndexedDB sales ledger + live GET /fiado-accounts + local cash-close history. NO report endpoint exists.

Layout contract:
- Sidebar 240px + content flex-1.
- Content grid:
  - KPI row = 4 columns.
  - Second row = fiado card + hours chart (2 columns).
  - Audit list card full-width.
- KPI cards:
  1. "S/ Ventas Netas" — value S/ 280.00.
  2. "Ticket promedio" S/ 20.00 + "14 transacciones".
  3. "Desglose por método de pago" — split bar Efectivo (emerald) vs Billeteras Digitales (Yape purple + Plin sky); remainder neutral (Tarjeta/Otro/Fiado).
  4. "Deuda acumulada en fiados por cobrar" — live S/ 25.00.
- Audit list: one row per close — closed_on, expected/counted per tender, difference = counted − expected, sobrante (emerald) / faltante (danger-action). Fiado difference INFORMATIONAL only.
- Hours-pico chart bucketed by hour.
- NO attendance section.
- Money everywhere in JetBrains Mono tnum.

Keyboard map: NONE active. F2 no-op; F3, F4, 1-6, Supr = INACTIVE.

States:
- Zero-sales day: KPIs S/ 0.00, chart empty "Sin ventas registradas hoy".
- Offline fiado card: pending state, no stale number.
- No closes: "Sin cierres registrados" + Cierre de caja entry point.

Sample data:
- Brand: "Bodega San Pedro"; cashier "María Gómez".
- Tenders EXACT: Efectivo 120.00, Yape 60.00, Plin 30.00, Tarjeta 25.00, Otro 5.00, Fiado 40.00 → Net 280.00.
- Ticket avg 20.00 over 14 transactions.
- Split: Efectivo 43% / Billeteras Digitales 32% / remainder 25%.
- Fiado outstanding: S/ 25.00.
- Audit rows: one sobrante +S/ 5.00, one faltante −S/ 10.00, fiado row informational.
- Hours chart: 14 sales bucketed by hour.
```

---

### P5 — Cierre de Caja y Arqueo

```text
Task: Generate the cash-close screen: expected vs counted per tender, sobrante/faltante summary, and close action. NO opening-float input and NO emergency-egress form (deferred).

Layout contract:
- Sidebar 240px + content flex-1.
- Dense admin close card:
  - Header: business date "2026-10-03".
  - SIX tender rows: Efectivo, Yape, Plin, Tarjeta, Otro, Fiado.
  - For each cash-like tender: expected (read-only) + counted (operator input, S/ prefix) + difference (sobrante emerald / faltante danger-action).
  - Fiado row: expected only, counted LOCKED to 0, informational note with badge-fiado — NEVER a shortage flag.
  - Summary line: total sobrante / faltante across cash-like tenders.
  - 52px accent-primary button "CERRAR CAJA" → POST /cash-closes (idempotency key required).
- If a close exists locally → form renders READ-ONLY with recorded counts; duplicate-date close = silent no-op.
- Tender identity badges: Yape purple, Plin sky, Tarjeta blue (#2563EB), Otro zinc, Fiado amber.

Keyboard map:
- Digits enter counted amounts.
- Enter = submit.
- Esc = clear focused counted input (no modal on this screen).
- F2 no-op; F3, F4, 1-6, Supr = INACTIVE.

States:
- Empty day: every row expected 0, "Sin ventas registradas", close action still available.
- Read-only close-exists: recorded counts shown, no edits.
- Offline queues: close persists locally and queues; pending counter.
- Mismatch sobrante/faltante.
- Fiado informational only.

Sample data:
- Business date "2026-10-03".
- Expected EXACT to P4: Efectivo 120.00, Yape 60.00, Plin 30.00, Tarjeta 25.00, Otro 5.00, Fiado 40.00.
- Happy path: counted = expected, differences 0.
- Mismatch alternate: Efectivo counted 115.00 → faltante −S/ 5.00.
```

---

### P6 — Fiados

```text
Task: Generate the fiados portfolio screen: deudor search, credit-limit warning, movement history, and abono registration with native share.

Layout contract:
- Sidebar 240px + content flex-1.
- Master-detail, two panes:
  - LEFT: SearchBarF2 (hint "[F2]") + deudor list (~min 320px).
  - RIGHT: account detail (flex-1).
- Left list: name, optional phone, computed balance (JetBrains Mono tnum). Warning badge-fiado when balance > credit_limit_cents.
- Right detail:
  - Movement history: signed amount_cents (charge positive, payment negative), note, sale linkage, running balance.
  - Abono form: amount + tender (Efectivo / transferencia) → POST /fiado-movements (negative amount, queueable, atomic account upsert).
  - Share action: native share sheet with composed estado de cuenta. NO wa.me/WABA.
- Money in JetBrains Mono tnum.

Keyboard map:
- F2 = focus search.
- Esc = clear search.
- Enter = open first match / submit abono.
- Arrow keys navigate list.
- F3, F4, 1-6, Supr = INACTIVE.

States:
- No deudores: "Sin deudores registrados".
- No-hits after search.
- Warning above limit: badge-fiado on row.
- No-limit / within limit.
- Offline abono queues.
- Share native.

Sample data:
- "María Gómez" (the only documented person):
  - charge +S/ 40.00 (note "Venta fiada")
  - abono −S/ 15.00 (note "Abono efectivo")
  - balance S/ 25.00
- Primary render: credit limit S/ 50.00 → no warning.
- Alternate render: limit S/ 20.00 → balance 25 > 20 → badge-fiado warning.
```

---

### P7 — Inventario y Mermas

```text
Task: Generate the inventory screen with stock semáforo and merma/damage registration. NO photo/evidence-capture control on the merma form (deferred).

Layout contract:
- Sidebar 240px + content flex-1.
- SearchBarF2 top (hint "[F2]") — search by GTIN/barcode or name.
- Dense product rows: name + stock + semáforo.
- Semáforo thresholds (SPEC-FIXED):
  - Verde (emerald): stock > 5
  - Amarillo (badge-fiado #F59E0B): 0 < stock ≤ 5
  - Rojo (danger-action): stock ≤ 0
- Merma flow per row (danger-action button) → AppModal:
  - Motivo selector EXACT three values: "Vencimiento", "Rotura en transporte", "Daño en vitrina".
  - Quantity field (integer units for sample; contract NUMERIC(10,3)).
  - Explicit confirmation before queueing.
  - Submit → POST /stock-adjustments: qty_delta negative, source "manual", reason = motivo.
- Modal opens scale(0.95→1) with cubic-bezier(0.16,1,0.3,1).
- NO photo control.

Keyboard map:
- F2 = focus search.
- Esc = clear search / close modal.
- Enter = submit merma.
- Arrow keys navigate rows.
- F3, F4, 1-6, Supr = INACTIVE.

States:
- No-hits: search stays active, non-blocking.
- Empty catalog.
- Offline queues for merma.
- Semáforo three states.
- Merma confirmation (destructive).
- Camera states NOT rendered.

Sample data:
- "Pan" stock 20 (Verde)
- "Huevos" stock 3 (Amarillo)
- "Hielo" stock 0 (Rojo)
- Merma alternate: modal on "Pan", qty 2, motivo "Daño en vitrina" → stock projects 18.
```

---

### P8 — Catálogo Maestro y Márgenes

```text
Task: Generate the catalog screen: tenant SKU list, product create/edit, and global-catalog adoption on first scan. NO margin column and NO margin alerts (deferred).

Layout contract:
- Sidebar 240px + content flex-1.
- SearchBarF2 top (hint "[F2]") — search by name or GTIN.
- Dense SKU table: name, GTIN (nullable), price (JetBrains Mono), uom, stock projection, photo thumb.
- Create → POST /products (queueable).
- Edit → PATCH /products/{id} (additive-only, LWW on name/price/gtin/uom/photo_url/attributes). PATCH is NOT queueable → edit form disabled offline.
- Stock NEVER edited here; converges via stock_adjustments.
- Global-catalog adoption: scan GTIN not in tenant catalog → cache-miss lookup (global_barcodes → global_products) → autocomplete Name/Brand/Photo/Category → operator enters Precio de Venta + Stock Inicial → POST /products with global_product_id. Miss → manual tenant create.
- Create/edit/adopt forms in AppModal (scale(0.95→1), cubic-bezier(0.16,1,0.3,1)).
- NO margin column/alerts.

Keyboard map:
- F2 = focus search.
- Esc = clear search / close modal.
- Enter = submit.
- Arrow keys navigate rows.
- F3, F4, 1-6, Supr = INACTIVE.

States:
- Empty catalog: create + adopt entry points.
- No-hits.
- Offline create queues.
- Offline edit disabled.
- Adoption hit → autocomplete renders.
- Miss → manual create.
- No margins.

Sample data:
- "Pan" S/ 4.80 stock 20, GTIN "7750101001011"
- "Huevos" S/ 9.50 stock 3, GTIN NULL
- "Hielo" S/ 3.00 stock 0, GTIN NULL
- Adoption: scanned GTIN maps to global "Huevos" → autocomplete → Precio de Venta + Stock Inicial 20 → POST /products with global_product_id.
- Miss: GTIN with no hit → manual tenant create "Hielo".
```

---

## 8. Delivery Rules for Stitch

1. Generate **one screen per prompt section** (P1–P8). Do not merge screens.
2. Use the **desktop 1440x900px** viewport; never render mobile bottom bars or the cashier ticket panel.
3. Apply **Block T colors, typography, and radii verbatim** on every screen.
4. All monetary values use `S/` prefix and `JetBrains Mono` tabular nums.
5. UI copy and sample data in Spanish (Peru); the prompt itself is in English.
6. Respect the deferred list: no photos, no margins, no opening float, no report endpoint, no attendance, no WhatsApp automation.
7. Verify banned-token checks before finishing each screen.

---

## 9. Source Refs (anti-drift footer)

- `docs/DESIGN.md` §2.1 tokens (:37-53), §7.1 Admin prompts P1-P8 (:451-920).
- `docs/FRONTEND.md` admin component mapping, sidebar 240px, keyboard-first rules.
- `docs/DATABASE.md` payment_method six-value enum (:310), cash closes cluster (:427-487), fiado_accounts / fiado_movements (:378-415), products (:234-249), stock_adjustments (:272-279), queueable sets (:736-741).
- `ARCHITECTURE.md` auth surface (:122-135), `/auth/admin-login`, `/fiado-accounts`, `/fiado-movements`, `/products`, `/stock-adjustments`, `/cash-closes`, JWT role claim.
- `SPRINTS.md` MVP scope, single-cashier frozen scope, no wa.me in MVP.
