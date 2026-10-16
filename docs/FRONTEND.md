# FRONTEND: Plan Maestro de Desarrollo e Implementación UI/UX

<!-- impeccable:frontend-master-plan 1.0 -->
<!-- Stitch:google-stitch-integration 2.0 -->

## 1. Resumen Ejecutivo y Filosofía de Diseño Engineering

Este documento es el **Plan Maestro de Implementación Frontend** para el Punto de Venta (POS) SaaS para bodegas y minimarkets peruanos.

Define la arquitectura cliente, la estandarización de las propuestas de **Google Stitch**, el sistema de tokens de diseño **Impeccable** y los principios de **Emil Kowalski design engineering**, organizados en una estructura de tareas ejecutables (*Work Breakdown Structure*).

### 1.1 Pilares de Diseño Engineering (Craft Floor)
1. **Regla de Color 60-30-10 Estricta (Light Mode):**
   - **60% Dominante:** Fondos neutros limpios (`zinc-100` / `#F4F4F5`, `white` / `#FFFFFF`), tipografía nítida (`zinc-900` / `#18181B`).
   - **30% Secundario:** Tarjetas de producto, contenedores de navegación, segmented controls (`zinc-100` / `zinc-200`).
   - **10% Acento:** Color de llamada a la acción primario, alertas de escaneo exitoso y badges activos. Por defecto **Verde Esmeralda** (`emerald-600` / `#059669` / `emerald-500` / `#10B981`).
2. **Principios de Emil Kowalski (UI Motion & Interaction):**
   - **Cero latencia/animación en acciones frecuentes:** La escritura en el buscador (F2), el disparo por lector láser (< 40ms) y el tipeo en numpad son de 0ms sin transiciones.
   - **Resorte amortiguado (Spring Physics):** Modales y diálogos emergen de `scale(0.96) -> scale(1)` con `cubic-bezier(0.16, 1, 0.3, 1)`.
   - **Respuesta hápica táctil:** `:active { transform: scale(0.98); transition: transform 120ms ease-out; }` (evitar `transition: all`).
   - **Tipografía Numérica Tabular:** `JetBrains Mono` con `tnum` activado para que los dígitos en Soles (`S/`) no produzcan parpadeos o saltos de layout (*Layout Shift = 0*).
3. **Ergonomía de Teclado Físico & Touch:**
   - Atajos globales: `F2` (Buscar), `F3` (Cantidad), `F4` / `Enter` (Cobrar), `1` a `6` (Tenders), `Esc` (Cancelar/Limpiar).
   - Teclas numéricas de **56px** de alto; botón primario `COBRAR` de **52px** de alto accesible con una sola mano.

---

## 2. Estandarización de Proyectos e Insumos de Google Stitch

El diseño se encuentra estandarizado e inspeccionado a partir de los dos proyectos generados en Google Stitch:

| Proyecto Stitch | Viewport Target | Cantidad de Pantallas | Propósito |
| :--- | :--- | :--- | :--- |
| `projects/13775636729982207136` | Desktop Landscape (`1280px` – `1440px+`) | 30 Pantallas / Vistas | Estación de Mostrador PC All-in-One, Multi-panel (Sidebar + Catálogo + Ticket), Dashboard Admin |
| `projects/1666859944796849703` | Mobile Viewport (`390px` – `430px`) | 15 Pantallas / Vistas | Operación Smartphone Single-Thumb (Tabs Productos/Escáner/Teclado, Bottom Sheet Checkout) |

> Nota: el conteo Desktop incluye las **8 pantallas declaradas de la Admin Prompt Suite** (DESIGN.md §7.1, rol Administrador, 1440x900): 22 existentes + 8 de la suite = 30. La suite se emite como work units del cambio `stitch-admin-prompts`.

### 2.1 Estandarización de Componentes Atómicos (Stitch UI Mapping)
- **Header:** `CashierProfileHeader` (Avatar, turno, indicador online/offline en vivo).
- **Segmented Control:** `SegmentedViewTabs` (3 pestañas en mobile: *Productos* [default], *Escáner*, *Teclado*; en desktop se integran concurrentes).
- **Visor de Escaneo:** `BarcodeScannerViewfinder` (Marco con retícula esmeralda, disparo manual, linterna y fallback a grilla si no hay cámara).
- **Teclado Ergonomico:** `ErgonomicNumpad` (Matriz 3x4 con teclas de 56px, display monetario tabular en Soles `S/`).
- **Resumen de Orden:** `OrderSummaryPanel` (Líneas de carrito con incrementador `- / +`, subtotal, IGV y total).
- **Modal Checkout:** `MultimethodCheckoutModal` (6 tenders de `DATABASE.md`: Efectivo, Yape, Plin, Tarjeta, Otro, Fiado; billetes rápidos y display gigante de vuelto).
- **Admin Suite — Login-1 (selector de rol, desktop):** `RoleSelectorScreen` (AuthCard centrada con wordmark 'POS SaaS' y dos tarjetas de rol `RoleCard` — Empleado/Administrador — con íconos Lucide y hint de teclado caption-xs; ConnectivityBadge en esquina; sin datos de tenant pre-auth).
- **Admin Suite — Login-2 (credencial, desktop):** `AdminCredentialForm` (variante Admin: back affordance + 'Teléfono' + 'Contraseña maestra' + botón 'INGRESAR' 52px accent-primary; binds `POST /auth/admin-login`) y `PinCredentialPanel` (variante Empleado: display de 4 puntos + reuso del `ErgonomicNumpad` 3x4 56px existente, :41, adaptado a PIN).
- **Admin Suite — Shell (chrome Admin, desktop):** `Sidebar240` (brand block logo + tenant + versión, nav de 6 destinos ≥48px con íconos Lucide, ConnectivityBadge + contador de sync), `RoleBadge` 'Administrador', footer de sesión (teléfono + logout client-side — sin endpoint de logout, ARCHITECTURE.md:122-135).
- **Admin Suite — Dashboard (KPIs, desktop):** `KpiCardRow` (Ventas Netas, Ticket promedio, Desglose por tender, Fiados por cobrar — TASK 8.2 :149-150), `TenderSplitBar` (Efectivo vs Billeteras Digitales, enum de 6 tenders), `FiadoKpiCard` (pendiente offline, sin número), `AuditListCard` (cierres locales con sobrante/faltante — fiado informacional), `HoursChartCard` (MAY, buckets por hora local).
- **Admin Suite — Cierre de caja (arqueo, desktop):** `CashCloseCard` (6 filas tender expected/counted/difference — enum EXACTO a DATABASE.md:310/:466/:477; fila fiado informacional con counted locked 0, F32 :478; resumen sobrante/faltante), CTA `CERRAR CAJA` 52px accent-primary (POST /cash-closes; form read-only si ya existe cierre local — F37 :751).
- **Admin Suite — Fiados (cartera, desktop):** `DeudorListPane` (SearchBarF2 + filas nombre/teléfono/saldo computado — GET /fiado-accounts; warning badge-fiado sobre límite de crédito, TASK 7.1 :142), `MovementHistoryPane` (movimientos firmados — el signo ES el tipo, DATABASE.md:407), `AbonoForm` (monto + tender Efectivo/transferencia → POST /fiado-movements; queueable offline), share nativo del estado de cuenta (sin wa.me, SPRINTS.md S6-4).
- **Admin Suite — Inventario y mermas (desktop):** `StockRow` (semáforo Verde/Amarillo/Rojo — umbrales spec-fixed; stock = proyección SUM(qty_delta), DATABASE.md:270), `MermaModal` (motivo EXACTO Vencimiento/Rotura en transporte/Daño en vitrina + cantidad + confirmación destructiva → POST /stock-adjustments; sin control de foto — ADI-3.1), `SearchBarF2` por GTIN/nombre (TASK 8.1 :147-148).
- **Admin Suite — Catálogo maestro (desktop):** `SkuTable` (name, GTIN nullable, price_cents, uom, stock, photo thumb — GET /products; TASK 8.3 :151-152), `ProductFormModal` (create → POST /products queueable offline; edit → PATCH /products/{id} solo online — PATCH no es queueable, DATABASE.md:736-741; additive :263, stock excluido), `GlobalAdoptionPanel` (GTIN cache-miss S2-3 → autocomplete Name/Brand/Photo/Category → POST con global_product_id, F13 :266; miss → create manual).

---

## 3. Arquitectura Tecnológica del Frontend

```
┌───────────────────────────────────────────────────────────────────────────┐
│                          APLICACIÓN FRONTEND PWA                          │
│                      (React 18/19 + Vite + TypeScript)                    │
├───────────────────────────────────────────────────────────────────────────┤
│  [ Capa UI ]         Tailwind CSS v3/v4 + Shadcn Primitives + Lucide    │
│  [ Interacción ]     Atajos de Teclado (F2-F4) + Web Audio API + Hápico  │
│  [ Estado React ]    Zustand (Carrito & UI) + TanStack Query (Sync)     │
│  [ Base Datos IDB ] Dexie.js (Catalog Cache + Event Queue Idempotente)    │
└─────────────────────────────────────┬─────────────────────────────────────┘
                                      │ Sync Engine (HTTPS / REST API)
                                      ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                         BACKEND GO API & POSTGRESQL                       │
└───────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Desglose de Fases y Tareas Ejecutables (WBS)

### FASE 1: Base del Proyecto, Tokens y Shadcn Design System
- [ ] **TASK 1.1: Scaffold de Vite + React + TypeScript + Tailwind CSS**
  - Configurar proyecto Vite con TypeScript estricto.
  - Instalar y configurar `tailwindcss`, `@types/node` y `lucide-react`.
  - Configurar alias `@/` apuntando a `src/`.
- [ ] **TASK 1.2: Integración del Sistema de Tokens (60-30-10)**
  - Configurar `tailwind.config.js` con los tokens de `DESIGN.md`:
    - Neutros 60%: `bg-zinc-50`, `bg-white`, `text-zinc-900`, `border-zinc-200`.
    - Secundarios 30%: `bg-zinc-100`, `bg-zinc-200`.
    - Acento 10%: `emerald-500` (`#10B981`), `emerald-600`, `shadow-glow-emerald`.
    - Tipografía: Inter para UI chrome, JetBrains Mono con `tnum` para importes `S/`.
- [ ] **TASK 1.3: Componentes Base de UI (Shadcn Primitives)**
  - Implementar `Button` (con estado `:active { transform: scale(0.98); }`).
  - Implementar `Card`, `Badge`, `Input`, `Dialog` / `Modal` (animación resorte `modal-spring`).

### FASE 2: Motor Offline-First e Almacenamiento IndexedDB (Dexie.js)
- [ ] **TASK 2.1: Esquema de Base de Datos Local con Dexie.js**
  - Crear `src/db/posDatabase.ts` con tablas locales:
    - `products` (id, gtin, name, price_cents, uom, stock, photo_url, category).
    - `cart` (id, product_id, qty, unit_price_cents).
    - `idempotency_queue` (id, key, payload, status, created_at).
- [ ] **TASK 2.2: Hook de Sincronización y Estado de Conexión (`useNetworkStatus`)**
  - Listener de `window.addEventListener('online' | 'offline')`.
  - Exponer estado `isOnline` y badge visual con resiliencia observable.
- [ ] **TASK 2.3: Store de Carrito en Zustand (`useCartStore`)**
  - Acciones: `addProduct`, `updateQty`, `removeItem`, `emptyCart`, `calculateTotalCents`.
  - Redondeo client-side *half-up* a centavos enteros (regla 2.3 de `DATABASE.md`).

### FASE 3: Componentes Táctiles Atómicos y Hooks Sensoriales
- [ ] **TASK 3.1: Hook de Feedback Sensorial (`useSensoryFeedback`)**
  - Audio: Sintetizador de Beep ligero mediante `Web Audio API` (sin latencia).
  - Háptico: Disparo de `navigator.vibrate([60])` en confirmaciones.
- [ ] **TASK 3.2: Listener de Atajos de Teclado Físico (`useKeyboardShortcuts`)**
  - Mapeo global: `F2` (Buscador), `F3` (Cantidad), `F4` / `Enter` (Cobrar), `Esc` (Cerrar/Limpiar), `1-6` (Tenders).
- [ ] **TASK 3.3: Componente Visor de Escáner (`BarcodeScanner`)**
  - Implementación con `BarcodeDetector API` acelerada en GPU + fallback.
  - Manejo de estados requeridos: permiso denegado, linterna (`Torch API`) y disparo manual.
- [ ] **TASK 3.4: Componente Teclado Numérico Ergonómico (`ErgonomicNumpad`)**
  - Matriz 3x4 con teclas de 56px de alto.
  - Display digital tabular en Soles (`S/ 0.00`).

### FASE 4: Módulo 1 — Vista de Venta Mostrador (Responsive Multi-pane)
- [ ] **TASK 4.1: Layout de Tres Columnas Desktop (>= 1280px)**
  - Sidebar fija de 240px + Catálogo flex-1 + Panel de Ticket persistente de 380px.
- [ ] **TASK 4.2: Layout Mobile con Tabs & Sticky Bottom Bar (< 768px)**
  - Segmented control (Productos [default] | Escáner | Teclado).
  - Sticky bottom bar de 52px en verde esmeralda `COBRAR (S/ 0.00)`.
- [ ] **TASK 4.3: Grilla Fluid de Productos y Filtros de Categoría**
  - Tarjetas de producto táctiles con imagen, precio destacado y stock disponible.
  - Buscador global instantáneo (F2) sin latencia de animación.

### FASE 5: Módulo 2 — Checkout, Selector de Tender & Motor de Vuelto
- [ ] **TASK 5.1: Modal de Checkout y Selector de Tender Único**
  - Selector táctil con los 6 métodos de `DATABASE.md`: Efectivo, Yape, Plin, Tarjeta, Otro, Fiado.
- [ ] **TASK 5.2: Billetes Rápidos y Calculadora de Vuelto**
  - Chips de billetes (`S/ 10`, `S/ 20`, `S/ 50`, `S/ 100`, `Exacto`).
  - Tarjeta highlight de vuelto gigante (`VUELTO: S/ 0.00`).
- [ ] **TASK 5.3: Cierre de Transacción & Ticket en Pantalla**
  - Registro de venta en ledger local / IndexedDB.
  - Pantalla de éxito con ticket y botón secundario de `Compartir` (share sheet nativo).

### FASE 6: Módulo 3 — Navigation Drawer, Cierre de Caja & Arqueo
- [ ] **TASK 6.1: Navigation Drawer Operativo (Navigation Sheet)**
  - Menú lateral deslizante con enlaces a módulos (POS, Caja, Fiados, Inventario, Mermas).
  - Bloque protegido de Administración con acceso por PIN.
- [ ] **TASK 6.2: Modal de Arqueo y Cierre de Turno**
  - Formulario de conteo de efectivo.
  - Comparativa automática entre ventas proyectadas del sistema vs. contado real.

### FASE 7: Módulo 4 — Cuentas Corrientes & Libro de Fiados
- [ ] **TASK 7.1: Cartera de Clientes y Buscador de Deudores**
  - Búsqueda por nombre o teléfono.
  - Indicador de semáforo sobre límite de crédito fijado.
- [ ] **TASK 7.2: Registro de Abonos Parciales**
  - Amortización de deuda con cálculo automático de saldo pendiente.

### FASE 8: Módulo 5, 6 y 7 — Mermas, Dashboard & Catálogo Global
- [ ] **TASK 8.1: Registro de Mermas y Productos Dañados**
  - Formulario de ajuste de stock negativo por rotura o vencimiento.
- [ ] **TASK 8.2: Dashboard de Administrador (KPIs)**
  - Resumen diario de ventas netas, ticket promedio y gráficos de distribución por tender.
- [ ] **TASK 8.3: Integración de SKU Maestro Global**
  - Autocompletado de producto al escanear código no existente.

### FASE 9: Automatización de Pruebas E2E con Playwright
- [ ] **TASK 9.1: Configuración de Playwright Test Suite**
  - Instalar `@playwright/test` y configurar `playwright.config.ts` (Viewports: Mobile 390x844, Desktop 1440x900).
- [ ] **TASK 9.2: Test E2E de Flujo Completo de Venta (Desktop & Mobile)**
  - Verificar atajos F2, F4, agregado de productos, cálculo de vuelto y respuesta del modal de checkout.
- [ ] **TASK 9.3: Test de Regresión Visual & Responsive**
  - Capturas comparativas de UI en Playwright asegurando cumplimiento de la regla 60-30-10.

---

## 5. Checklist de Criterios de Aceptación (Definition of Done)

- [ ] **Fidelidad de Diseño:** 60-30-10 verificado, tipografía Inter + JetBrains Mono `tnum`, ergonomía 56px/52px.
- [ ] **Desempeño de Interacción:** 0ms de latencia en búsqueda y numpad; resorte resuelto en modales.
- [ ] **Resiliencia Offline:** La aplicación opera 100% desconectada guardando ventas en IndexedDB.
- [ ] **Contratos Respetados:** 6 métodos de pago de `DATABASE.md`, montos en centavos enteros backend, tender único por venta.
- [ ] **Verificación Automática:** Suite de Playwright ejecutada con 100% de tests de flujo aprobados.
