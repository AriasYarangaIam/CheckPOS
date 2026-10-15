# BACKEND: Plan Maestro de Desarrollo e Implementación Backend

## 1. Resumen Ejecutivo y Filosofía de Ingeniería Backend

Este documento es el **Plan Maestro de Implementación Backend** del Punto de Venta (POS) SaaS para bodegas y minimarkets peruanos. Es la contraparte de `docs/FRONTEND.md` ("Plan Maestro de Desarrollo e Implementación UI/UX"): donde ese documento organiza la capa cliente, este organiza el servidor.

**Qué es y qué no es.** No fija arquitectura ni esquema. La autoridad de arquitectura es `ARCHITECTURE.md`; la autoridad de esquema es `docs/DATABASE.md`, que debe leerse **como enmienda** de `ARCHITECTURE.md` por su Sección 8 (`ARCHITECTURE.md:5`; `docs/DATABASE.md` §1, §8). La autoridad de secuencia de entrega es `SPRINTS.md` (`SPRINTS.md` §1). Este documento **secuencia, planifica y contractúa el backend** contra esos tres contratos congelados, y señala explícitamente cada punto donde un contrato debe leerse junto con una enmienda.

**El trabajo del MVP, en un párrafo.** Un solo cajero, en un solo teléfono, vende y registra fiados sin perder dinero cuando la red se cae (`SPRINTS.md` §6). El servidor es la fuente de verdad del stock, del dinero y de los saldos de fiado; el teléfono es caché y cola de eventos, nunca el libro de registro (`ARCHITECTURE.md:3`). El backend existe para que esa promesa sea físicamente cierta: cada venta cerrada sobrevive a la muerte de la pestaña (evento en IndexedDB) y a la pérdida del teléfono (evento en el servidor) (`ARCHITECTURE.md:168`).

### 1.1 Principio rector: el servidor es dueño de los saldos

| Principio | Enunciado | Fuente |
| :--- | :--- | :--- |
| Saldos en el servidor | El servidor posee stock, dinero y fiado; el teléfono es caché y cola de eventos, no el libro de registro | `ARCHITECTURE.md:3` |
| Convergencia aditiva | Dinero y stock convergen por deltas firmados append-only, nunca por last-write-wins | `ARCHITECTURE.md:25`; `docs/DATABASE.md` §7.5 |
| LWW acotado | LWW aplica solo a campos mutables de catálogo y perfil; jamás a dinero ni stock | `docs/DATABASE.md` §7.4 |
| Reconciliación silenciosa | El POS nunca muestra errores de sincronización al cajero | `ARCHITECTURE.md:111`; `docs/DATABASE.md` §7.9 |
| Tenancy desde el token | El tenant sale únicamente del JWT; jamás del cuerpo ni de un encabezado | `ARCHITECTURE.md:23`; `docs/DATABASE.md` §2.11 |
| Dinero en centavos | Toda columna de dinero es `BIGINT` en centavos enteros | `docs/DATABASE.md` §2.3 |

### 1.2 Mapa de autoridad documental

Cada decisión tiene un solo dueño. Este documento no reabre ninguna de ellas.

| Documento | Es dueño de | No es dueño de | Cómo se lee |
| :--- | :--- | :--- | :--- |
| `ARCHITECTURE.md` | Contrato de arquitectura: stack, módulos y sus fronteras, superficie HTTP, resolución de conflictos, no-metas | Esquema de tablas; secuenciación | **Enmendado** por `docs/DATABASE.md` §8 (`ARCHITECTURE.md:5`) |
| `docs/DATABASE.md` | Contrato de esquema: 14 tablas, convenciones globales, RLS, particionado, sincronización y reconciliación, enmiendas vinculantes | Arquitectura; secuencia de sprints | Autoridad final sobre datos; enmienda a `ARCHITECTURE.md` (§8) |
| `SPRINTS.md` | Secuenciación, IDs de tarea, Definition of Done, plan de pruebas TDD, epics diferidos | Arquitectura y esquema | Si discrepa de `ARCHITECTURE.md`, gana `ARCHITECTURE.md` (`SPRINTS.md` §1) |
| `docs/FRONTEND.md` | Plan maestro de la capa cliente; declara los contratos que el backend debe honrar | Backend | Contraparte de este documento |
| `docs/PRODUCT.md` | Definición de producto, promesa offline-first, promesa de venta < 3 s | Decisiones técnicas | Insumo de requisitos |
| `docs/DESIGN.md` | UI/UX, 8 módulos core, Admin Prompt Suite; las operaciones que el backend debe servir | Esquema y API | Insumo de operaciones |
| `docs/BUSINESS.md` | Contenido comercial: viabilidad, planes y precios, GTM, horizonte de portafolio | Arquitectura | Restricción comercial, no técnica (`docs/BUSINESS.md:12`) |
| **`docs/BACKEND.md` (este)** | **Secuenciación, contractúa y presupuesta la implementación backend; registra riesgos, deltas diferidos y no-alcance** | Arquitectura, esquema, secuencia de sprints, producto, comercio | Este documento |

### 1.3 Stack congelado (no se discute en este documento)

PWA (React + Vite) · **Go API** · PostgreSQL con RLS · Redis como caché/cola únicamente · object storage para fotos (`SPRINTS.md` §6). Del lado servidor, las decisiones de proceso y de datos que el backend hereda congeladas:

| Decisión | Valor congelado | Fuente |
| :--- | :--- | :--- |
| API | Go, **un solo proceso**, REST, contract-first | `ARCHITECTURE.md:18` |
| Datos | PostgreSQL con `FORCE RLS` + `SET LOCAL app.current_tenant_id` por transacción; dinero en centavos `BIGINT` | `ARCHITECTURE.md:19`; `docs/DATABASE.md` §2.11 |
| Caché / cola | Redis, claves `tenant:{id}:...` — nunca fuente de verdad, nunca volcado global de SKUs | `ARCHITECTURE.md:20` |
| Fotos | Object storage; las filas de producto guardan solo URLs | `ARCHITECTURE.md:21` |
| PostgreSQL | Versión mínima 15; sin dependencia de `uuidv7()` nativo (existe desde PG18) | `docs/DATABASE.md` §2.2 |

> **Nota de alcance.** Nada de lo que sigue autoriza código nuevo fuera de estos contratos. `SPRINTS.md` §8 y §7 Sprint 1 (Semana 1) prohíben empezar a codificar hasta que el glosario y los campos de dominio del Sprint 0 coincidan con `ARCHITECTURE.md` y `docs/DATABASE.md`.

---

## 2. Arquitectura Tecnológica del Backend

### 2.1 Proceso único, REST, contract-first

El backend es **un solo proceso Go** que expone una API REST (`ARCHITECTURE.md:18`). No hay mensajería asíncrona, no hay WebSocket/WSS y no se depende de Background Sync API (`ARCHITECTURE.md:91`). El motor de sincronización vive en el cliente y hace *flush* cuando vuelve la conectividad (`ARCHITECTURE.md:91`). El servidor solo debe ser correcto ante lotes de POST idempotentes y pulls por cursor.

Contract-first significa una cosa concreta para este documento: la forma de la petición y de la respuesta se fija **antes** de la implementación, y el contrato de datos que la sostiene ya está escrito en `docs/DATABASE.md`. Toda desviación observada es un defecto, no una extensión (Hyrum's Law aplica igual a un contrato interno).

### 2.2 Módulos backend y sus fronteras de datos

`ARCHITECTURE.md:31` fija la regla: "One job per module. No module writes another module's tables." La tabla siguiente reproduce esa frontera y le añade la propiedad de tablas del contrato de datos (`ARCHITECTURE.md:144-152`).

| Módulo (fuente: `ARCHITECTURE.md:33-43`) | Posee (tablas) | Frontera: no posee |
| :--- | :--- | :--- |
| **Identity** | `tenants`, `users`, `devices` | ventas, catálogo |
| **Catalog** | `products`, `stock_adjustments`, `global_products`, `global_barcodes` (estas dos últimas curadas por plataforma, lectura-solo para el tenant) | blobs; escrituras de tenant a tablas globales |
| **Input** | sin tablas — adaptadores `onProductIdentified(code)` en proceso | precios, stock |
| **Cart** | sin tablas — carrito en IndexedDB, del lado cliente | ventas cerradas |
| **Ledger** | `sales`, `sale_lines` | borrar historia; tabla separada de tenders |
| **Fiado** | `fiado_accounts`, `fiado_movements` | recordatorios por WhatsApp |
| **Cash close** | `cash_closes`, `cash_close_counts` | un segundo contador corriente |
| **Sync** | `idempotency_keys` | reglas de negocio |
| **Photos** | sin tablas — object storage | identidad de producto |

Suman **14 tablas** en 7 grupos de módulos (`ARCHITECTURE.md:142`; `docs/DATABASE.md` §4). Las tablas de `Input`, `Cart` y `Photos` no existen del lado servidor por diseño: el carrito y los favoritos son estado exclusivamente cliente y no tienen tabla ni columna en el contrato (`docs/DATABASE.md` §3, nota de frontera).

### 2.3 Ciclo de vida de una petición

```
   Cliente PWA (React/Vite)
        │  HTTPS + Idempotency-Key + JWT
        ▼
┌────────────────────────────────────────────────────────────────┐
│ [ Transport ]   TLS · ruteo REST · decode JSON · request-id     │
│                 límite de tamaño · validación de forma          │
└───────────────────────────────┬────────────────────────────────┘
                                ▼
┌────────────────────────────────────────────────────────────────┐
│ [ Auth / tenant ]  verifica firma y expiración del JWT           │
│                    extrae tenant_id · user_id · role (opcional) │
│                    BEGIN  →  SET LOCAL app.current_tenant_id    │
└───────────────────────────────┬────────────────────────────────┘
                                ▼
┌────────────────────────────────────────────────────────────────┐
│ [ Module handler ]  enruta a Identity · Catalog · Ledger ·       │
│                     Fiado · Cash close · Sync                   │
└───────────────────────────────┬────────────────────────────────┘
                                ▼
┌────────────────────────────────────────────────────────────────┐
│ [ Service ]  reglas de negocio: claim de idempotencia (7.1),     │
│              emparejamiento de anulaciones (F10/F39), fórmula    │
│              de dinero (F8), resoluciones silenciosas (7.7),     │
│              orden padre → hijo (7.8)                           │
└───────────────────────────────┬────────────────────────────────┘
                                ▼
┌────────────────────────────────────────────────────────────────┐
│ [ Repository ]  SQL contra las 11 tablas con FORCE RLS          │
│                 (predicado por GUC; fail-closed)                │
└───────────────────────────────┬────────────────────────────────┘
                                ▼
                    PostgreSQL (RLS, fuente de verdad)
                                │
             ┌──────────────────┴──────────────────┐
             ▼                                     ▼
   Redis  tenant:{id}:...                 Object storage
   (caché / cola — nunca SoT)             (fotos; la BD guarda URL)
```

El paso `SET LOCAL` es carga estructural, no un detalle: es lo que vuelve seguro un *connection pooler*, porque un `SET` de alcance de sesión filtraría el contexto de tenant a la siguiente petición que reutilice la conexión (`docs/DATABASE.md` §5.2; `ARCHITECTURE.md:85`).

### 2.4 Layout de repositorio Go propuesto `(propuesta)`

Los documentos fuente no fijan un layout de repositorio. El siguiente es **no vinculante** y existe solo para dar a los sprints un lugar concreto donde poner cada tarea:

```text
cmd/api/main.go                     # arranque del proceso único
internal/transport/http/            # router, middleware, DTOs request/response
internal/auth/                      # JWT issue/verify, login, device-bind, admin-login
internal/modules/identity/          # tenants, users, devices
internal/modules/catalog/           # products, stock_adjustments, adopción global
internal/modules/ledger/            # sales, sale_lines, void
internal/modules/fiado/             # fiado_accounts, fiado_movements
internal/modules/cashclose/         # cash_closes, cash_close_counts, fórmula F8
internal/modules/sync/              # claim de idempotencia, cursor pull
internal/platform/postgres/         # pool, helper de transacción (SET LOCAL), runner de migraciones
internal/platform/redis/            # claves tenant:{id}:...
internal/platform/storage/          # object storage (upload / URL)
migrations/                         # SQL versionado, rol owner/migration
```

La regla `ARCHITECTURE.md:31` se traduce aquí en que un paquete de módulo nunca importa el repositorio de otro módulo: si Ledger necesita un producto, lo lee por el servicio de Catalog, no por su SQL.

### 2.5 Configuración y secretos `(propuesta)`

Los nombres de variables de entorno **no están fijados en las fuentes**; se proponen aquí y deben confirmarse antes de Sprint 1.

| Variable `(propuesta)` | Propósito | Regla |
| :--- | :--- | :--- |
| `DATABASE_URL` | DSN de PostgreSQL | Un rol por privilegio, según la matriz de concesiones (`docs/DATABASE.md` §5.5): `pos_app`, `pos_auth`, `pos_platform`, `pos_maintenance`, `owner/migration` |
| `REDIS_URL` | Caché / cola | Claves siempre `tenant:{id}:...` (`ARCHITECTURE.md:20`; S1-4 `SPRINTS.md` S1-4) |
| `JWT_SECRET` | Firma del JWT | ≥ 32 bytes; rotación fuera del repositorio |
| `JWT_TTL` | Expiración del access token | No confundir con la ventana offline del PIN, que vive en `devices.offline_session_valid_until` (`docs/DATABASE.md` §4, `devices`) |
| `OBJECT_STORAGE_*` | Credenciales y bucket de fotos | La base de datos guarda solo la URL (`ARCHITECTURE.md:21,87`) |
| `HTTP_ADDR`, `LOG_LEVEL` | Runtime | — |

Política: los secretos se leen **solo** de entorno o de un gestor externo; nunca se versionan, nunca se imprimen en logs. PIN, fotos y payloads nunca se registran en logs (ver §5.6). El secreto del JWT y las credenciales de object storage se rotan sin tocar el esquema.

### 2.6 Topología de desarrollo local `(propuesta)`

Sin fuentes que la fijen; se propone por utilidad operativa:

```text
docker compose
  ├── postgres:15+        (rol owner/migration aplica migrations/; roles pos_* creados por migración)
  ├── redis:7+            (solo caché/cola)
  └── minio               (S3-compatible; sustituye object storage en local)
```

Los tests de integración corren contra la instancia Postgres local, con transacciones por tenant, y se saltan bajo `testing.Short()` (`SPRINTS.md` §8).

---

## 3. Modelo de Dominio y Contrato de Datos

### 3.1 Glosario de dominio (entregable S0-1)

`SPRINTS.md` S0-1 exige un glosario de una página donde cada término tenga un solo nombre y un solo significado, revisado contra `ARCHITECTURE.md`. Esta es la tabla de glosario del backend, con el módulo dueño y la fuente del contrato.

| Término | Significado en una línea | Módulo dueño | Fuente |
| :--- | :--- | :--- | :--- |
| **tenant** | Una bodega/minimarket; fila raíz del registro que ancla todo `tenant_id` del esquema | Identity | `docs/DATABASE.md` §4, `tenants`; §3 |
| **device** | Un teléfono enlazado a un cajero; porta el PIN y la expiración de sesión offline | Identity | `docs/DATABASE.md` §4, `devices`; S1-3 |
| **cashier (cajero)** | La cuenta que cierra ventas: `users` con `phone` de login e `id` que viaja como `user_id` en el JWT | Identity | `docs/DATABASE.md` §4, `users` |
| **product** | Un ítem vendible del catálogo de un tenant: identidad, precio, proyección de stock, foto opcional | Catalog | `docs/DATABASE.md` §4, `products`; S2-1 |
| **GTIN** | Código de barras en `TEXT`, nullable, con unicidad por tenant y predicados de soft-delete | Catalog | `docs/DATABASE.md` §2.1, §4 `products` |
| **sale** | Un cierre append-only del libro de dinero; particionado por mes sobre `sold_on`, PK `(sold_on, sale_id)` | Ledger | `docs/DATABASE.md` §4, `sales`; §6.1 |
| **tender** | El medio de pago único de una venta. **No es una tabla**: vive como columna `sales.payment_method` con seis valores | Ledger | `docs/DATABASE.md` §8 enmienda 1 y D9/D11 (`tenders` queda superseded) |
| **fiado** | Venta a crédito contra una cuenta de cliente; el saldo es `SUM(movements)`, nunca una columna escribible | Fiado | `docs/DATABASE.md` §4, `fiado_accounts` / `fiado_movements` |
| **sync event** | La unidad de sincronización entrante. **No es una tabla**: se fusionó en `idempotency_keys` (el conteo vinculante es 14, no 15) | Sync/API | `docs/DATABASE.md` §8 enmienda 5; §4 `idempotency_keys` |
| **cash close** | Un cierre de caja: cabecera por fecha de negocio más una fila por método de pago con esperado vs. contado | Cash close | `docs/DATABASE.md` §4, `cash_closes` / `cash_close_counts`; S4-1 |
| **merma** | Una pérdida de mercadería. **No es una tabla**: es un `stock_adjustments` con `source = 'manual'` y `reason` igual al motivo elegido | Catalog | `docs/DATABASE.md` §4, `stock_adjustments`; `docs/DESIGN.md:217` |

> **Honestidad de contrato.** "tender" y "sync event" aparecen como entidades en borradores anteriores y en el plan previo (`SPRINTS.md` S0-2), pero **no son tablas** en el contrato vinculante. Cualquier implementación que cree una tabla `tenders` o `sync_events` está contradiciendo `docs/DATABASE.md` §8.

### 3.2 Las 14 tablas, por módulo, con su invariante

Cada tabla se cita por sección de `docs/DATABASE.md`; no se duplican listas de columnas aquí.

| # | Tabla | Módulo dueño | Invariante que el backend debe sostener | Fuente |
| :--- | :--- | :--- | :--- | :--- |
| 1 | `tenants` | Identity | Registro raíz; fuera del RLS de tenant; `plan IN ('free','standard','pro')` | §4 `tenants`; §3 |
| 2 | `users` | Identity | `phone` es globalmente UNIQUE porque el login no lleva discriminador de tenant | §4 `users` |
| 3 | `devices` | Identity | Un dispositivo por cajero; `offline_session_valid_until` ≥ hora de bind + 7 días, refrescado en cada autenticación online | §4 `devices`; S1-3 |
| 4 | `products` | Catalog | `stock = SUM(stock_adjustments.qty_delta)`; las escrituras de proyección **nunca** modifican `updated_at` (MD-11) | §4 `products` |
| 5 | `stock_adjustments` | Catalog | Log append-only de deltas firmados; toda fuente de delta escribe exactamente una fila; ninguna corrección escribe stock absoluto | §4 `stock_adjustments` |
| 6 | `sales` | Ledger | Append-only; PK `(sold_on, sale_id)`; `payment_method` con seis valores; `sold_on` lo asigna el cliente | §4 `sales`; §6.1 |
| 7 | `sale_lines` | Ledger | Snapshot congelado del producto al cierre; sin id sustituto (PK compuesta `(sale_id, line_no)`) | §4 `sale_lines` |
| 8 | `fiado_accounts` | Fiado | Sin columna de saldo escribible; `name`/`phone` son los únicos campos LWW | §4 `fiado_accounts` |
| 9 | `fiado_movements` | Fiado | Append-only; **el signo de `amount_cents` ES el tipo** (cargo positivo, pago negativo) | §4 `fiado_movements` |
| 10 | `cash_closes` | Cash close | `UNIQUE (tenant_id, closed_on)` — un cierre por fecha de negocio por tenant (D10) | §4 `cash_closes` |
| 11 | `cash_close_counts` | Cash close | Una fila por método; `counted_cents = 0` cuando `payment_method = 'fiado'`, forzado por CHECK (F32) | §4 `cash_close_counts` |
| 12 | `idempotency_keys` | Sync/API | `UNIQUE (tenant_id, key)`; la fila **es** el claim; no append-only (ciclo de vida con UPDATE, retención con DELETE) | §4 `idempotency_keys`; §7.1 |
| 13 | `global_products` | Catalog (plataforma) | Fuera del RLS de tenant; curado por plataforma; lectura del tenant solo vía API | §4 `global_products`; §5.6 |
| 14 | `global_barcodes` | Catalog (plataforma) | `UNIQUE (gtin)`; mapea GTIN → `global_products`; nunca un volcado global a dispositivos | §4 `global_barcodes` |

### 3.3 Convenciones globales que la capa de datos debe hacer cumplir

`docs/DATABASE.md` §2 fija doce reglas de esquema completo. El backend no las reinterpreta; las implementa. Resumen con el punto donde cada una impacta al código:

| # | Regla | Obligación del backend | Fuente |
| :--- | :--- | :--- | :--- |
| 2.1 | Nombres y sufijos (`*_cents`, `*_at`, `*_hash`) | El mapeo Go→columna respeta los sufijos; sin renombres | §2.1 |
| 2.2 | UUIDv7 en todo identificador | Generación server-side en Go para filas server-born; se preserva el id del cliente en las device-originated | §2.2 |
| 2.3 | Dinero `BIGINT` centavos | Sin `float`, sin `NUMERIC` para dinero; el servidor **nunca** recomputa dinero | §2.3 |
| 2.4 | Cantidades `NUMERIC(10,3)` | Mapeo decimal exacto en Go; deltas firmados | §2.4 |
| 2.5 | Timestamps y ubicación | `TIMESTAMPTZ` siempre; el servidor **preserva** el `created_at`/`updated_at` del cliente en filas device-originated | §2.5 |
| 2.6 | `tenant_id` en las 11 tablas de tenant, con índice que empieza por `tenant_id` | Toda consulta lleva el predicado de RLS; los índices ya están fijados | §2.6 |
| 2.7 | FKs `ON DELETE RESTRICT`; índices de FK | Sin borrados duros; los companions compuestos a `sales` son excepción documentada | §2.7 |
| 2.8 | Conjuntos acotados con `TEXT` + CHECK | `payment_method`, `kind`, `source`, `plan` se validan además en el borde de la API | §2.8 |
| 2.9 | JSONB en exactamente dos columnas | Solo `products.attributes` y `global_products.attributes`; el snapshot de idempotencia es `TEXT` | §2.9 |
| 2.10 | Digests | argon2id para credenciales (`users.password_hash`, `devices.pin_hash`); SHA-256 hex para `payload_hash` | §2.10 |
| 2.11 | RLS | `FORCE RLS` + `SET LOCAL` por transacción en las 11 tablas; rutas pre-auth dedicadas | §2.11; §5 |
| 2.12 | Append-only | Cinco tablas append-only (`sales`, `sale_lines`, `fiado_movements`, `stock_adjustments`, `cash_close_counts`), con grants revocados y guardas de trigger; `idempotency_keys` es la excepción explícita | §2.12 |

---

## 4. Superficie HTTP / Contrato de API

### 4.1 Reglas transversales

**Un solo sobre de error.** Toda respuesta de error usa la misma forma (`ARCHITECTURE.md:120`):

```json
{ "error": { "code": "VALIDATION_ERROR", "message": "human-readable", "details": { } } }
```

`code` es legible por máquina y estable; `message` es legible por humano; `details` es opcional. No se mezclan estrategias: ningún endpoint devuelve `null` ni texto plano como error.

**Política de códigos de estado.** Los seis códigos que esta superficie usa, con su disparador exacto:

| Código | Significado en este contrato | Fuente |
| :--- | :--- | :--- |
| `200` | Éxito, incluida la **repetición** de un POST idempotente (se devuelve el cuerpo original almacenado) | `docs/DATABASE.md` §7.1 Branch A |
| `201` | Recurso creado en una operación nueva de creación | (convención REST; no fijada en las fuentes) |
| `401` | No autenticado. En `/auth/admin-login` es **genérico**, sin enumeración de usuarios | `ARCHITECTURE.md:126` |
| `404` | Recurso inexistente o fuera del tenant (el RLS devuelve cero filas ⇒ no encontrado) | `docs/DATABASE.md` §5.2, §5.7 |
| `409` | Duplicado **en vuelo**: misma clave, primer request aún sin confirmar | `ARCHITECTURE.md:107`; `docs/DATABASE.md` §7.1 Branch C |
| `422` | Cuerpo inválido o **misma clave con cuerpo distinto** | `ARCHITECTURE.md:106`; `docs/DATABASE.md` §7.1 Branch A |
| `500` | Error del servidor; nunca expone detalles internos | (convención) |

**Encabezado `Idempotency-Key`.** Obligatorio en todo POST que cambia estado (`ARCHITECTURE.md:120`). Derivado del **intento**, estable entre reintentos de un mismo cierre, distinto entre cierres distintos: se genera una vez por cierre y se reutiliza en cada reintento (`ARCHITECTURE.md:81`, `:98`). El servidor lo reclama con `UNIQUE (tenant_id, key)`, nunca con un `SELECT` seguido de un `INSERT` (`docs/DATABASE.md` §7.1). Retención: **400 días** después de la marca de finalización (`docs/DATABASE.md` §7.2).

**Paginación y contrato de cursor.** El pull descendente de catálogo es un protocolo de cursor, no una página numerada: el cliente presenta un cursor `timestamptz` y el servidor devuelve las filas mutables con `updated_at > cursor`, incluyendo tombstones. El cursor avanza solo hasta `MAX(updated_at)` realmente devuelto, con ventana de solape de **15 minutos** (`docs/DATABASE.md` §7.3). Para listados no-cursor (`GET /products`, `GET /fiado-accounts`) las fuentes no fijan parámetros de paginación; cualquier forma concreta se marca `(propuesta)` en su subsección.

**Enumeraciones exactas.**

| Enumeración | Valores exactos | Fuente |
| :--- | :--- | :--- |
| `payment_method` | `cash`, `yape`, `plin`, `card`, `other`, `fiado` — **seis** | `docs/DATABASE.md` §4 `sales` y `cash_close_counts`; D11; `docs/DESIGN.md:161` |
| Motivo de merma (`reason`) | `Vencimiento`, `Rotura en transporte`, `Daño en vitrina` | `docs/DESIGN.md:217`; `odd/tasks/dual-login-mermas-cleanup.md:30` |
| `kind` (sales) | `sale`, `void` | `docs/DATABASE.md` §4 `sales` |
| `source` (stock) | `sale`, `void`, `manual` | `docs/DATABASE.md` §4 `stock_adjustments` |
| `state` (idempotencia) | `in_flight`, `completed` | `docs/DATABASE.md` §4 `idempotency_keys` |

### 4.2 `POST /auth/login`

| Atributo | Valor |
| :--- | :--- |
| Auth | ninguna (ruta pre-auth) |
| Idempotencia | no (`ARCHITECTURE.md:124`) |
| Cola offline | no |

**Cuerpo `(propuesta)`.** Los campos anclados al contrato son `phone` como identidad de login y una contraseña verificada contra argon2id; el nombre exacto del campo de contraseña no está fijado en las fuentes.

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `phone` | string | sí | Identidad de login; globalmente UNIQUE (`docs/DATABASE.md` §4 `users`) |
| `password` `(propuesta)` | string | sí | Verificado contra `users.password_hash` (argon2id) |

**Respuesta 200 `(propuesta)`.**

```json
{ "token": "<jwt>", "user": { "id": "<uuidv7>" } }
```

**Efecto server-side.** Emite un JWT con `tenant_id` + `user_id` y **sin** claim `role` (token de empleado) (`ARCHITECTURE.md:23`). La lectura de `users` corre como el rol dedicado `pos_auth` con su política `FOR SELECT TO pos_auth USING (true)`, porque no existe todavía un JWT que dé contexto de tenant (`docs/DATABASE.md` §5.4).

**Errores.** `401` genérico ante credenciales inválidas.

> **Contrato de aislamiento.** El cuerpo de esta petición **no** lleva tenant. El tenant llega únicamente dentro del token devuelto (`ARCHITECTURE.md:124`).

### 4.3 `POST /auth/device-bind`

| Atributo | Valor |
| :--- | :--- |
| Auth | token de empleado |
| Idempotencia | **sí** (`ARCHITECTURE.md:125`) |
| Cola offline | no |

**Cuerpo `(propuesta)`.** El contrato ancla `devices.pin_hash` (argon2id) y `devices.offline_session_valid_until`.

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `pin` `(propuesta)` | string | sí | PIN de 4 dígitos; se almacena como `pin_hash` argon2id (`docs/DATABASE.md` §4 `devices`; `docs/DESIGN.md:105`) |
| `device_id` `(propuesta)` | uuid | no | Si se omite, el servidor crea la fila `devices` |

**Respuesta 200 `(propuesta)`.** `{ "device": { "id", "offline_session_valid_until" } }`

**Efecto server-side.** Enlaza el teléfono al cajero (S1-3) y fija `offline_session_valid_until = hora_de_bind + al menos 7 días`; se refresca en cada autenticación online (`docs/DATABASE.md` §4 `devices`).

### 4.4 `POST /auth/admin-login`

| Atributo | Valor |
| :--- | :--- |
| Auth | ninguna (ruta pre-auth) |
| Idempotencia | no (`ARCHITECTURE.md:126`) |
| Cola offline | no |

**Cuerpo (fijado).** `{ "phone": "<string>", "master_password": "<string>" }` (`ARCHITECTURE.md:126`).

**Respuesta 200 (fijada).** `{ "token": "<jwt>", "tenant": { "name": "<string>" } }`, con el JWT portando `role: "admin"` (`ARCHITECTURE.md:126`).

**Efecto server-side.** Resuelve `phone` → tenant propietario → verifica `tenants.master_password_hash` (argon2id). El claim `role:"admin"` es el único marcador de privilegio administrativo; un token sin el claim es un token de empleado (`ARCHITECTURE.md:23`).

**Errores.** `401` **genérico** con `message` constante, sin enumeración de usuarios ni distinción entre "teléfono inexistente" y "contraseña incorrecta" (`ARCHITECTURE.md:126`; delta D2 de `odd/tasks/stitch-admin-prompts.md:121`).

> **Dependencia de esquema.** `tenants.master_password_hash` está documentada como columna futura con contrato `TEXT NOT NULL` (argon2id) en `docs/DATABASE.md` §4 `tenants` (nota delta D1), **sin DDL en esta versión**. El endpoint no puede implementarse hasta que la columna y su backfill existan.

### 4.5 `GET /products`

| Atributo | Valor |
| :--- | :--- |
| Auth | token (empleado o admin) |
| Idempotencia | sí (`ARCHITECTURE.md:127`) — la lectura es naturalmente repetible |
| Cola offline | no (es un pull, no un POST) |

**Parámetros `(propuesta)`.** Las fuentes fijan el propósito ("tenant catalog page; no global dump") pero no los parámetros.

| Parámetro | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `q` `(propuesta)` | string | no | Búsqueda por nombre o GTIN (`docs/DESIGN.md:213`; `docs/FRONTEND.md:52`) |
| `updated_after` `(propuesta)` | timestamptz | no | Historial incremental; el pull canónico de catálogo es `GET /sync/cursor` |
| `limit` / `cursor` `(propuesta)` | — | no | Forma de paginación no fijada por las fuentes |

**Respuesta 200 `(propuesta)`.** `{ "data": [ ...productos... ] }` con las columnas de `docs/DATABASE.md` §4 `products`.

**Efecto server-side.** Ninguno; lectura. Nunca devuelve el catálogo global (S2-3; `ARCHITECTURE.md:127`, `:89`).

### 4.6 `POST /products`

| Atributo | Valor |
| :--- | :--- |
| Auth | token |
| Idempotencia | sí (`ARCHITECTURE.md:128`) |
| Cola offline | **sí** — `POST /products` está en la enumeración exhaustiva de POST encolables (`docs/DATABASE.md` §7.6) |

**Cuerpo.** Campos anclados a `products` (`docs/DATABASE.md` §4):

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `id` | uuid (UUIDv7) | sí | Generado por el cliente (device-originated, §2.2) |
| `name` | string | sí | Unicidad por tenant con predicado de soft-delete |
| `gtin` | string? | no | Nullable; `TEXT` preserva ceros a la izquierda |
| `price_cents` | int64 | sí | Centavos enteros (§2.3) |
| `uom` | string | sí | p. ej. `unit`, `kg` |
| `stock` | — | **no** | **Excluido de los payloads**; el stock converge solo por deltas (`docs/DATABASE.md` §4 `products`) |
| `photo_url` | string? | no | URL de object storage |
| `attributes` | object? | no | Home #1 de JSONB (§2.9) |
| `updated_at` | timestamptz | sí | Marca de mutación del cliente = comparador LWW (§2.5) |
| `global_product_id` | uuid? | no | Puntero de adopción; único enlace al catálogo global (F13) |

**Respuesta 201/200.** La fila resuelta. En una colisión silenciosa puede volver con `name` renombrado o `gtin` en `NULL` (ver §6.9).

**Errores.** `422` (validación) · `409` (claim en vuelo) · `401`.

### 4.7 `PATCH /products/{id}`

| Atributo | Valor |
| :--- | :--- |
| Auth | token |
| Idempotencia | sí (`ARCHITECTURE.md:129`) |
| Cola offline | **no** — la enumeración de encolables no incluye PATCH; la edición es solo online (`docs/DATABASE.md` §7.6; `docs/FRONTEND.md:53`) |

**Cuerpo.** Solo campos **aditivos**: se pueden agregar atributos nuevos, nunca quitar por PATCH (`docs/DATABASE.md` §4 `products`). `stock` está excluido del payload y del comparador LWW.

**Semántica LWW.** Gana el `updated_at` de mutación del cliente; el servidor preserva ese valor verbatim y registra `received_at` solo como auditoría, sin participar en la resolución (`docs/DATABASE.md` §7.4). Empate determinista: gana el `id` lexicográficamente mayor.

**Errores.** `404` (fuera del tenant o inexistente) · `422` (intento de quitar campo) · `409`.

### 4.8 `GET /sync/cursor`

| Atributo | Valor |
| :--- | :--- |
| Auth | token |
| Idempotencia | sí (`ARCHITECTURE.md:130`) |
| Cola offline | no |

**Parámetro `cursor` `(propuesta)`.** `timestamptz`; si se omite, el pull inicial devuelve el conjunto completo de las tablas mutables.

**Respuesta 200.** Filas de las tablas mutables device-synced (`products`, `fiado_accounts`) con `updated_at > cursor`, **incluyendo tombstones** (filas con `deleted_at` no nulo), más el cursor nuevo (`docs/DATABASE.md` §7.3). El índice `products_tenant_updated_at_idx` sin predicado parcial sirve exactamente esta consulta.

**Reglas del cursor.** Avanza solo a `MAX(updated_at)` devuelto, nunca a `now()`. Solape de 15 minutos. Un cursor más antiguo que 15 minutos devuelve una respuesta más grande, sin perder filas; un cursor futuro devuelve vacío (es seguro hacia adelante) (`docs/DATABASE.md` §7.3).

**Lo que el cursor no transporta.** El cursor **no** propaga dinero ni stock: solo ediciones de catálogo y perfil (`docs/DATABASE.md` §7.3). La propagación incremental de stock entre dispositivos está diferida (MD-11, `docs/DATABASE.md` §9).

### 4.9 `POST /sales`

| Atributo | Valor |
| :--- | :--- |
| Auth | token de empleado |
| Idempotencia | **requerida** (`ARCHITECTURE.md:131`) |
| Cola offline | **sí** (`docs/DATABASE.md` §7.6) |

**Cuerpo.** Anclado a `sales` + `sale_lines` (`docs/DATABASE.md` §4). La forma JSON es `(propuesta)`; los campos y su semántica son contrato.

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `sale_id` | uuid (UUIDv7) | sí | Generado por el cliente |
| `sold_on` | date | sí | Fecha de negocio asignada por el cliente al cerrar; el retraso de sync **nunca** mueve la venta de día, de cierre ni de partición |
| `payment_method` | enum (6) | sí | Un solo tender por venta |
| `total_cents` | int64 | sí | Congelado al cierre; la suma de los `line_total_cents` redondeados |
| `lines[]` | array | sí | Cada línea: `line_no`, `product_id`, `name`, `price_cents`, `uom`, `qty`, `line_total_cents` — todos congelados al cierre |

**Respuesta 200.** El ack de la venta, que además **refresca los saldos proyectados** (stock y fiado) que el cliente usa para re-renderizar (`ARCHITECTURE.md:100`).

**Efecto server-side.** Un solo `INSERT` transaccional en `sales` + `sale_lines`, y el delta de stock correspondiente en `stock_adjustments` (`ARCHITECTURE.md:99`). Todo dentro del claim de idempotencia, con la clave reclamada por restricción única (`docs/DATABASE.md` §7.1 Branch B).

**Errores.** `422` (misma clave, cuerpo distinto) · `409` (duplicado en vuelo) · `401`.

**Regla de dinero.** El servidor **nunca** recomputa dinero: almacena los valores congelados que envió el cliente (`docs/DATABASE.md` §2.3).

### 4.10 `POST /sales/{id}/void`

| Atributo | Valor |
| :--- | :--- |
| Auth | token de empleado |
| Idempotencia | **requerida** (`ARCHITECTURE.md:132`) |
| Cola offline | **sí** (`docs/DATABASE.md` §7.6) |

**Efecto server-side.** Añade una fila de `sales` con `kind = 'void'` que **espeja** el `payment_method` y el `total_cents` positivos del original; la negación vive en la fórmula de dinero, nunca en el total almacenado (`docs/DATABASE.md` §4 `sales`, fórmula F8). La anulación referencia el original por su clave compuesta (`original_sold_on`, `original_sale_id`). La fila original **se conserva**: nunca se borra ni se edita (`ARCHITECTURE.md:132`). El delta de stock se invierte como una fila nueva y positiva en `stock_adjustments` con `source = 'void'` (`docs/DATABASE.md` §4 `stock_adjustments`).

**Restricciones.** Las anulaciones deben apuntar solo a filas `kind = 'sale'`; las anulaciones de anulaciones se rechazan al crear la anulación (F39, `docs/DATABASE.md` §4 `sales`).

**Errores.** `404` (original fuera del tenant) · `422` (objetivo no es una venta, o es una anulación) · `409`.

### 4.11 `POST /fiado-movements`

| Atributo | Valor |
| :--- | :--- |
| Auth | token de empleado |
| Idempotencia | **requerida** (`ARCHITECTURE.md:133`) |
| Cola offline | **sí** (`docs/DATABASE.md` §7.6) |

**Cuerpo.** Anclado a `fiado_movements` (`docs/DATABASE.md` §4):

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `id` | uuid (UUIDv7) | sí | Generado por el cliente |
| `fiado_account_id` | uuid | sí | Si la cuenta no existe, se hace upsert atómico en la misma transacción (F12) |
| `amount_cents` | int64 | sí | **Firmado**: cargo positivo, pago negativo; el signo **es** el tipo |
| `note` | string? | no | Nota libre del operador |
| `sold_on` / `sale_id` | date / uuid | no | Enlace opcional a la venta; par de nulidad obligatorio |

**Efecto server-side.** Append-only en `fiado_movements`; si el `fiado_account_id` no existe, se crea `fiado_accounts` y se añade el movimiento de forma atómica dentro de la transacción del claim (`docs/DATABASE.md` §7.8, F12). La anulación de una venta a fiado produce un movimiento compensatorio con signo invertido — **nunca** un `DELETE` ni una edición (`docs/DATABASE.md` §4 `fiado_movements`).

**Errores.** `422` (par de nulidad roto, o monto no numérico) · `409` · `401`.

### 4.12 `GET /fiado-accounts`

| Atributo | Valor |
| :--- | :--- |
| Auth | token (empleado o admin) |
| Idempotencia | sí (`ARCHITECTURE.md:134`) |
| Cola offline | no |

**Respuesta 200.** Cuentas con **saldo computado** `balance = SUM(fiado_movements.amount_cents)`, más los movimientos firmados. No existe columna de saldo escribible (`ARCHITECTURE.md:134`; `docs/DATABASE.md` §4 `fiado_accounts`).

**Uso por el cliente admin.** Es uno de los dos únicos insumos del dashboard Admin en esta versión: el ledger local (IndexedDB) más `GET /fiado-accounts` (`ARCHITECTURE.md:138`).

**Errores.** `401` · `404` (cuenta fuera del tenant).

### 4.13 `POST /cash-closes`

| Atributo | Valor |
| :--- | :--- |
| Auth | token de empleado |
| Idempotencia | **requerida** (`ARCHITECTURE.md:135`) |
| Cola offline | **sí** (`docs/DATABASE.md` §7.6) |

**Cuerpo.** Anclado a `cash_closes` + `cash_close_counts` (`docs/DATABASE.md` §4):

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `id` | uuid (UUIDv7) | sí | Generado por el cliente |
| `closed_on` | date | sí | Fecha de negocio que se cierra |
| `counts[]` | array | sí | Una fila por método: `{ payment_method, counted_cents }`. El `expected_cents` lo computa el servidor |

**Efecto server-side.** Congela la proyección del ledger por método en `expected_cents` con la fórmula F8: `SUM(CASE kind WHEN 'sale' THEN +total_cents WHEN 'void' THEN -total_cents END)` sobre las ventas de esa fecha, agrupadas por `payment_method` (`docs/DATABASE.md` §4 `sales` y `cash_closes`). `counted_cents` es entrada del operador. La diferencia `counted_cents − expected_cents` es **computada, nunca almacenada** (`docs/DATABASE.md` §4 `cash_close_counts`).

**Reglas de negocio.**

- Un cierre por fecha de negocio por tenant, forzado por `UNIQUE (tenant_id, closed_on)` (D10). No hay camino de enmienda en el MVP.
- `counted_cents = 0` cuando `payment_method = 'fiado'`, forzado estructuralmente por CHECK (F32). La diferencia de fiado es informacional **por diseño**: nunca se marca como faltante (`docs/DATABASE.md` §4 `cash_closes` y `cash_close_counts`).
- Un cierre duplicado en cola se resuelve como no-op silencioso: se mantiene el cierre existente y el duplicado se confirma con la proyección existente (§6.9).

**Errores.** `422` (método inválido, o `counted_cents ≠ 0` en fiado) · `409` · `401`.

> **Nota de exactitud.** El formulario de cierre no incluye fondo inicial ni egresos: `cash_closes` no tiene esas columnas y la fórmula no tiene término de egreso en esta versión. El delta futuro está registrado como D10 diferido (§11.2).

### 4.14 `POST /stock-adjustments`

| Atributo | Valor |
| :--- | :--- |
| Auth | token de empleado |
| Idempotencia | **requerida** (`ARCHITECTURE.md:136`) |
| Cola offline | **sí** (`docs/DATABASE.md` §7.6) |

**Cuerpo.** Anclado a `stock_adjustments` (`docs/DATABASE.md` §4):

| Campo | Tipo | Requerido | Nota |
| :--- | :--- | :--- | :--- |
| `id` | uuid (UUIDv7) | sí | Generado por el cliente |
| `product_id` | uuid | sí | FK a `products`, `ON DELETE RESTRICT` |
| `qty_delta` | numeric(10,3) | sí | **Firmado**: la merma es negativa |
| `source` | enum | sí | `'manual'` para una merma reportada por el operador |
| `reason` | string | sí cuando `source = 'manual'` | **Uno de los tres motivos exactos** (`Vencimiento`, `Rotura en transporte`, `Daño en vitrina`) |
| `sold_on` / `sale_id` | — | no | Nulos en un ajuste manual; par de nulidad obligatorio |

**Efecto server-side.** Anexa una fila al log de deltas y aplica el delta **aditivamente** sobre `products.stock` sin tocar `updated_at` (MD-11). Ningún cambio de stock omite esta tabla (OI-6). El stock negativo está permitido a propósito (oversell offline aceptado, OI-10): el log registra la realidad.

**Errores.** `422` (motivo fuera de los tres valores, o `qty_delta` no numérico) · `409` · `401`.

> **Sin control de foto.** El formulario de merma no tiene captura de evidencia en esta versión; `stock_adjustments.evidence_photo_url` está diferido (D5, §11.2).

### 4.15 Cobertura y no-alcance de la superficie

Los 13 caminos anteriores agotan la superficie HTTP del MVP (`ARCHITECTURE.md:122-136`). **No existe un endpoint de reporte o agregado** en esta versión: no hay `GET /sales` ni `GET /dashboard/summary`. El dashboard Admin renderiza únicamente la proyección del ledger local (IndexedDB) más `GET /fiado-accounts` (`ARCHITECTURE.md:138`). Tampoco existe endpoint de logout: el cierre de sesión es client-side (`docs/FRONTEND.md:48`).

---

## 5. Seguridad y Aislamiento Multi-Tenant

### 5.1 Contrato de claims del JWT

| Claim | Tipo | Requerido | Semántica | Fuente |
| :--- | :--- | :--- | :--- | :--- |
| `tenant_id` | uuid | sí | **Única** fuente del tenant de la petición | `ARCHITECTURE.md:23` |
| `user_id` | uuid | sí | Cajero que cierra o consulta | `ARCHITECTURE.md:23` |
| `role` | `"admin"` | no | Presente **solo** en tokens emitidos por `POST /auth/admin-login`; su ausencia significa empleado | `ARCHITECTURE.md:23`, `:126` |
| `exp` | timestamp | sí | Expiración del access token; no es la ventana offline del PIN | (semántica JWT; ventana offline en `devices`) |

Regla dura: **el tenant se toma solo del token**. No se acepta `tenant_id` en el cuerpo ni en un encabezado, en ningún endpoint (`ARCHITECTURE.md:23`; S1-1 `SPRINTS.md` S1-1).

### 5.2 PIN, sesión y ventana offline

El PIN de 4 dígitos y la sesión offline son preocupaciones del **dispositivo**, no del usuario: viven en `devices` (`pin_hash` argon2id y `offline_session_valid_until`), no en `users` (`docs/DATABASE.md` §4 `users`, `devices`). `offline_session_valid_until` se fija en el bind a la hora de bind + **al menos 7 días** y se refresca en cada autenticación online; el cliente rechaza el desbloqueo offline una vez pasada esa marca (`docs/DATABASE.md` §4 `devices`; S1-3 `SPRINTS.md` S1-3).

La consecuencia de contrato es exacta: un **access token expirado con un PIN válido todavía desbloquea el POS offline** durante al menos 7 días. El backend no puede tratar la expiración del token como cierre de sesión del mostrador.

### 5.3 RLS obligatoria y seguridad del pooler

- `FORCE RLS` está activa en las **once** tablas de tenant, y liga también al dueño de la tabla; ningún rol de runtime tiene `BYPASSRLS` (`docs/DATABASE.md` §5.1, §5.5).
- Cada transacción comienza extrayendo `tenant_id` del JWT y emitiendo `SET LOCAL app.current_tenant_id = '<tenant_id>'`. `SET LOCAL` alcanza la transacción y se resetea al terminar: por eso es seguro frente a un pooler, mientras que un `SET` de sesión filtraría el contexto al siguiente cliente de la conexión reutilizada (`docs/DATABASE.md` §5.2).
- Las políticas leen `current_setting('app.current_tenant_id')::uuid` en `USING` (filtra filas visibles) y en `WITH CHECK` (rechaza escrituras con otro tenant). Un GUC ausente o discordante produce cero filas y cero escrituras: **fail-closed** (`docs/DATABASE.md` §5.2, §5.3).
- La forma fail-then-pass es el contrato de verificación: sin RLS aplicada, una consulta cross-tenant devuelve filas de otros tenants y el test falla por diseño; con RLS, la misma consulta devuelve cero filas y pasa (`docs/DATABASE.md` §5.7).

### 5.4 Ruta pre-auth (bootstrap)

Firmar y autenticar ocurren antes de que exista un JWT. Hay dos caminos dedicados (`docs/DATABASE.md` §5.4):

| Camino | Mecánica | Nota |
| :--- | :--- | :--- |
| **Signup** | Una transacción: `INSERT tenants` → `SET LOCAL app.current_tenant_id = <nuevo id>` → `INSERT users RETURNING`. La lectura usa `RETURNING` porque `pos_app` no tiene `SELECT` sobre `tenants` por diseño | El GUC se fija dentro de la misma transacción |
| **Login** | Rol dedicado `pos_auth` con política `FOR SELECT TO pos_auth USING (true)` sobre `users`; ejecuta **solo** la búsqueda `phone + password_hash` | El rol existe para una sola forma de consulta; ninguna otra tabla es legible |

### 5.5 Matriz de concesiones (resumen)

El backend debe operar con **cinco roles** con privilegios disjuntos; el cumplimiento lo verifica el propio conjunto de migraciones (`docs/DATABASE.md` §5.5).

| Rol | SELECT | INSERT | UPDATE | DELETE |
| :--- | :--- | :--- | :--- | :--- |
| `pos_app` | las 11 tablas de negocio; **no** `tenants` | tablas escritas por HTTP + `tenants` + `users` (signup) | `products`, `fiado_accounts`, `idempotency_keys`, `devices` únicamente | **nada** |
| `pos_auth` | `users` (solo la búsqueda de login) | — | — | — |
| `pos_platform` | `global_products`, `global_barcodes` | dichas tablas | dichas tablas | nada |
| `pos_maintenance` | — | — | — | `idempotency_keys` (retención MD-3); agenda la pre-creación de particiones |
| `owner/migration` | — | — | — | — (solo DDL) |

El hecho de que `pos_app` no tenga `DELETE` sobre ninguna tabla de negocio es lo que hace estructural el append-only, por encima de las guardas de trigger (`docs/DATABASE.md` §2.12, §5.5).

### 5.6 Tablas globales curadas por plataforma, fuera del RLS de tenant

`global_products` y `global_barcodes` viven fuera del RLS de tenant: no llevan `tenant_id`, no llevan política, y los roles de tenant (`pos_app`, `pos_auth`) tienen **cero** concesiones directas sobre ellas (`docs/DATABASE.md` §5.6). El único camino de lectura del tenant es la consulta API-mediated de cache-miss (S2-3, `SPRINTS.md` S2-3), servida por `pos_platform`. El único enlace del lado tenant es el puntero de adopción `products.global_product_id`; la adopción copia los datos globales a las columnas del tenant y a partir de ahí la fila es autopropiedad (F13, `docs/DATABASE.md` §4 `products`).

### 5.7 Apuntes de amenaza (entregable S0-4)

`SPRINTS.md` S0-4 exige apuntes de amenaza que cubran tenant-solo-por-JWT, aislamiento por RLS, teléfono perdido/robado y XSS por catálogo, y que declaren `FORCE RLS` + `SET LOCAL` por transacción y la expiración de PIN/sesión offline.

| Amenaza | Mitigación en el backend | Fuente |
| :--- | :--- | :--- |
| Suplantación de tenant por cuerpo/encabezado | El tenant sale solo del JWT; el GUC se fija desde el token, y el RLS rechaza escrituras con otro `tenant_id` | `ARCHITECTURE.md:23`; `docs/DATABASE.md` §5.2-5.3 |
| Fuga de lectura cross-tenant | `FORCE RLS` + `SET LOCAL` + `pos_app` sin `BYPASSRLS`; los tests fail-then-pass prueban el aislamiento | `docs/DATABASE.md` §5.1, §5.7; S1-2 |
| Teléfono perdido o robado | La sesión offline caduca por `offline_session_valid_until` (≥ 7 días desde el bind); el re-login restaura la historia desde el servidor | `docs/DATABASE.md` §4 `devices`; S4-4 `SPRINTS.md` S4-4 |
| XSS vía nombres de producto | Los nombres vienen de datos de tenant; el transporte escapa/serializa y el cliente renderiza como texto, sin HTML | (deber del contrato; `SPRINTS.md` S0-4) |
| Robo de credenciales en reposo | argon2id para `users.password_hash`, `devices.pin_hash` y `tenants.master_password_hash` | `docs/DATABASE.md` §2.10 |
| Fuga de datos sensibles en logs | PIN, fotos y payloads **nunca** se registran en logs; los payloads solo se persisten como `payload_hash` (SHA-256 hex) y `response_snapshot` (TEXT) | `docs/DATABASE.md` §2.9-2.10 |
| Enumeración de usuarios en el login admin | `401` genérico y constante, sin distinguir causa | `ARCHITECTURE.md:126` |

---

## 6. Idempotencia, Sincronización y Reconciliación

La sincronización es bidireccional: POST idempotente hacia arriba (ids UUIDv7 del cliente + protocolo de claim) y pull por cursor hacia abajo. La reconciliación es **silenciosa**: el POS nunca muestra errores de sincronización al cajero (`ARCHITECTURE.md:24`; `docs/DATABASE.md` §7.9). Esta sección convierte ese enunciado en obligaciones explícitas del servidor.

### 6.1 Protocolo de claim, paso a paso

Toda petición encolable lleva una clave de idempotencia: el par `(tenant_id, key)`. La fila `idempotency_keys` **es** el claim. El protocolo tiene tres ramas, todas dentro de una sola transacción (`docs/DATABASE.md` §7.1):

1. La petición llega con `Idempotency-Key` y un cuerpo serializado.
2. El servidor computa el hash SHA-256 del cuerpo y lo compara por inserción: intenta `INSERT` del claim con `ON CONFLICT` sobre `UNIQUE (tenant_id, key)`. Nunca hace `SELECT` seguido de `INSERT` — eso sería una carrera, no una guarda.
3. **Rama A — la clave ya existía (rowcount 0).** Se lee la fila existente y se compara el hash entrante con el `payload_hash` almacenado. Hashes iguales ⇒ es una repetición verdadera: se devuelve la respuesta almacenada con **HTTP 200** y el cuerpo original, sin ejecutar ninguna escritura de negocio. Hashes distintos ⇒ la misma clave se está reutilizando para otro payload: se devuelve **HTTP 422**. Ninguna de las dos ejecuta la capa de negocio.
4. **Rama B — clave nueva (rowcount 1).** El claim es fresco. La transacción ejecuta la escritura de negocio, y luego actualiza la fila de `idempotency_keys` al estado completado con el snapshot de la respuesta. El `COMMIT` es todo-o-nada: si la escritura de negocio falla, el claim se revierte con ella y la clave queda disponible para reintento.
5. **Rama C — duplicado concurrente.** Dos peticiones con la misma clave llegan al mismo tiempo. Una gana el `INSERT`; la otra bloquea en la restricción única hasta que la ganadora confirme. Si la ganadora confirma dentro del lock timeout, la perdedora relee el claim, aplica la comparación de hash de la Rama A, y devuelve el 200 almacenado o un 422. Si la ganadora **no** confirma dentro del lock timeout, la perdedora devuelve **HTTP 409** con `Retry-After`; el cliente retrocede y reintenta. El lock timeout es la perilla del esquema que acota la ventana de duplicado concurrente, no un timeout de negocio.
6. Un crash durante la transacción de claim revierte el `INSERT` con todo lo demás, así que una fila confirmada está siempre completada y con su snapshot: un snapshot en vuelo no puede servirse nunca como éxito fantasma (`docs/DATABASE.md` §4 `idempotency_keys`).

### 6.2 Comparación de `payload_hash`

| Aspecto | Contrato |
| :--- | :--- |
| Algoritmo | SHA-256 en hex (`docs/DATABASE.md` §2.10) |
| Entrada | El cuerpo serializado de la petición |
| Uso | Bifurcación repetición-vs-conflicto (Rama A) |
| Igualdad | Repetición: se reproduce la respuesta almacenada con 200 |
| Diferencia | Misma clave con otro cuerpo: 422, sin segunda escritura |

> **Inconsistencia de contrato a resolver (verificada).** `docs/DATABASE.md` §7.1 (línea 735) nombra la columna como `request_hash`, y §7.1 (línea 737) como `completed_at`, `response_status` y `response_body`; §7.2 (línea 745) describe la retención "400 días después de `completed_at`". La definición real de la tabla (§4 `idempotency_keys`) usa `payload_hash`, `state`, `response_snapshot` y `created_at`, y §4 afirma que **`created_at` es lo que conduce la ventana de retención**. La tabla (§4) es el contrato ejecutable; la prosa de §7.1-7.2 es anterior a la renombración. Recomendación: implementar contra §4 y registrar una corrección de prosa de §7.1/§7.2 en el follow-up ya registrado en Engram (`sdd/database-design-doc/follow-ups`, memoria #157, `docs/DATABASE.md` §8).

### 6.3 Tabla de decisión: 200 / 409 / 422

| Situación entrante | Respuesta | Efecto | Fuente |
| :--- | :--- | :--- | :--- |
| Clave nueva | `200` (o `201` en creación) | Se ejecuta la escritura de negocio una sola vez | §7.1 Branch B |
| Misma clave + mismo cuerpo | `200` con el cuerpo original | **Ninguna** escritura; repetición verdadera | §7.1 Branch A; `ARCHITECTURE.md:106` |
| Misma clave + cuerpo distinto | `422` | Ninguna escritura; diagnóstico | §7.1 Branch A; `ARCHITECTURE.md:106` |
| Misma clave, primer request aún en vuelo | `409` + `Retry-After` | Ninguna escritura; el cliente espera y reintenta, no abre una segunda venta | §7.1 Branch C; `ARCHITECTURE.md:107` |
| Violación única inbound (nombre, GTIN, cierre duplicado) | `200` con la fila resuelta | Resolución silenciosa del lado servidor | §7.7 |

### 6.4 Retención de claves — 400 días

Los claims se retienen **400 días** después de su finalización. La ventana excede la edad plausible de una cola de cliente (un dispositivo un año offline se desconoce, no se sincroniza). Pasados los 400 días, la clave es elegible para borrado, ejecutado por `pos_maintenance` — el único rol con `DELETE`, y solo sobre `idempotency_keys`. La retención es un **límite duro**: no hay tabla de archivo ni bandera de soft-delete. Si un cliente reenvía una petición de más de 400 días, el claim la trata como clave nueva — aceptable, porque el efecto de negocio es idempotente por sí mismo a nivel de aplicación (`docs/DATABASE.md` §7.2). A escala de piloto (10 bodegas), un escaneo secuencial con filtro de edad basta: no se necesita un índice dedicado sobre `created_at` (`docs/DATABASE.md` §4 `idempotency_keys`).

### 6.5 Tipos de POST encolables — enumeración exhaustiva

Un cliente puede emitir estos POST offline; el servidor los procesa al hacer flush, usando el protocolo de claim (`docs/DATABASE.md` §7.6). La lista es exhaustiva: **ningún otro tipo de POST es encolable**, y cualquier otro endpoint exige conectividad viva.

| POST encolable | Escribe | Fuente |
| :--- | :--- | :--- |
| `POST /sales` | `sales` + `sale_lines`; emite deltas de stock | §7.6 |
| `POST /sales/{id}/void` | anexa un delta de reversión; **no** borra la venta original | §7.6 |
| `POST /fiado-movements` | `fiado_movements`; puede hacer upsert atómico de `fiado_accounts` | §7.6; F12 |
| `POST /cash-closes` | `cash_closes` + `cash_close_counts` | §7.6 |
| `POST /products` | mutación de producto (LWW) | §7.6 |
| `POST /stock-adjustments` | delta firmado manual en el log de stock | §7.6 |

`PATCH /products/{id}` queda **fuera** de la enumeración: la edición de producto es solo online (`docs/DATABASE.md` §7.6; `docs/FRONTEND.md:53`).

### 6.6 Pull por cursor

| Regla | Valor | Fuente |
| :--- | :--- | :--- |
| Tablas arrastradas | `products`, `fiado_accounts` — las únicas mutables device-synced | §7.3 |
| Predicado | `updated_at > cursor` (estrictamente mayor) | §7.3 |
| Tombstones | Incluidos, para propagar borrados | §7.3 |
| Avance del cursor | Solo hasta `MAX(updated_at)` realmente devuelto; nunca a `now()` | §7.3 |
| Ventana de solape | 15 minutos — constante de diseño, no perilla | §7.3 |
| Cursor futuro | Devuelve vacío (seguro hacia adelante) | §7.3 |
| Dinero y stock | **No** viajan por el cursor | §7.3 |

### 6.7 LWW, limitado por daño

Cuando dos dispositivos editan la misma fila mutable offline y ambos hacen flush, gana el `updated_at` de mutación del cliente. El servidor preserva ese valor verbatim; `received_at` es auditoría y no participa (`docs/DATABASE.md` §7.4). El desempate es determinista: con el mismo `updated_at`, gana el `id` lexicográficamente mayor (UUIDv7 es ordenable por construcción).

**Frontera de daño.** LWW toca **solo** campos mutables de catálogo y perfil: nombre de producto, precio, `photo_url`, `attributes`; teléfono y nombre de la cuenta de fiado; y, cuando exista, el límite de crédito. LWW **nunca** toca dinero, cantidades de stock ni filas de ledger (`docs/DATABASE.md` §7.4). `stock` queda además excluido de los payloads de PATCH (`docs/DATABASE.md` §4 `products`).

### 6.8 Deltas firmados aditivos

Dinero y stock no usan LWW: convergen por deltas enteros firmados anexados a un log append-only. El servidor aplica los deltas en orden de llegada; la suma es el valor corriente. Como la suma es conmutativa y asociativa, el orden no altera el resultado final — solo importa el **conjunto** de deltas. Dos dispositivos que aplican el mismo conjunto en distinto orden llegan al mismo total (`docs/DATABASE.md` §7.5). Los deltas son firmados para soportar reversas: una anulación es un delta negativo contra la venta positiva original.

| Magnitud | Log append-only | Fuente |
| :--- | :--- | :--- |
| Stock | `stock_adjustments` (`qty_delta` firmado) | §7.5; §4 `stock_adjustments` |
| Fiado | `fiado_movements` (`amount_cents` firmado) | §7.5; §4 `fiado_movements` |
| Proyección congelada por método | `cash_close_counts` (`expected_cents`) | §4 `cash_close_counts` |

> **Inconsistencia de contrato a resolver (verificada).** `docs/DATABASE.md` §7.5 (línea 773) afirma que "el log append-only es la tabla `sale_lines` para stock y las tablas `fiado_movements` / `cash_close_counts` para dinero". El log de stock es `stock_adjustments` (§4, y `ARCHITECTURE.md:176`), no `sale_lines` — que no tiene columna de delta alguna. Y `cash_close_counts` no es un log de deltas de dinero, sino una proyección congelada por método. El resto de §7.5 (convergencia por suma, firmas para reversas) sí es correcto y vinculante. Recomendación: corregir la frase de §7.5 en el follow-up registrado; implementar stock sobre `stock_adjustments`.

### 6.9 Orden de flush y resoluciones silenciosas

**Orden de flush: padre → hijo.** Dentro de un lote, el servidor reordena antes de aplicar; el orden de la cola del cliente es **advisory** (`docs/DATABASE.md` §7.8). Dos jerarquías:

1. **Catálogo → ledger**: las mutaciones de producto se aplican antes que las líneas de venta que los referencian.
2. **`fiado_accounts` → `fiado_movements`**: la cuenta debe existir antes que el movimiento; `POST /fiado-movements` soporta upsert atómico (F12) dentro de la transacción del claim.

El reordenamiento es determinista y no depende del orden de envío del cliente.

**Resoluciones silenciosas ante violaciones únicas inbound.** El servidor las resuelve sin exponer nunca el error al cajero (`docs/DATABASE.md` §7.7):

| Violación | Resolución del servidor | Efecto en el cliente |
| :--- | :--- | :--- |
| `(tenant_id, name)` en `products` (F20) | La fila entrante (perdedora) se renombra de forma determinista: sufijo derivado de su propio `id`, con fallback escalonado si el sufijo vuelve a colisionar | Aprende el nombre nuevo en el siguiente pull §6.6; la fila sobrevive |
| `(tenant_id, gtin)` en `products` (F27) | El `gtin` entrante se pone a `NULL`; el servidor **nunca** fabrica un GTIN | La fila sobrevive sin código; el cajero puede re-codificarla después |
| Cierre de caja duplicado por fecha (F38) | El cierre existente prevalece; el duplicado se confirma como no-op con la proyección del cierre existente | Nunca se le muestra, nunca se reintenta, nunca se marca como error |

Las tres resoluciones devuelven **HTTP 200** con la fila resuelta (renombrada, anulada en GTIN, o no-op). El cliente marca la operación como completa; el cajero no ve error, prompt ni reintento.

**Orden de aplicación de un cierre duplicado.** Si los dos cierres coinciden en totales, son semánticamente idénticos; si discrepan, aplica primera-gana (se devuelve la proyección del cierre existente y el duplicado se descarta) (`docs/DATABASE.md` §7.7, F38).

---

## 7. Persistencia, Particionado y Operaciones

### 7.1 Particionado mensual del ledger

`sales` es la única tabla particionada. Es una decisión de desempeño y ciclo de vida, no de corrección: la forma lógica es idéntica a la de los otros ledgers y solo su almacenamiento físico se divide por fecha.

| Mecánica | Contrato | Fuente |
| :--- | :--- | :--- |
| Clave de partición | `sold_on` (date), partición RANGE | §6.1 |
| Clave primaria | `(sold_on, sale_id)` — `sold_on` debe estar en la PK | §6.1 |
| Granularidad | Un mes calendario por partición | §6.2 |
| Nomenclatura | `sales_YYYY_MM` (p. ej. `sales_2026_09`) | §6.2 |
| Pre-creación | Seis meses calendario hacia adelante, por un job agendado e idempotente | §6.3 |
| Ejecución del DDL | El scheduler corre como `pos_maintenance`, pero el DDL de partición lo ejecuta `owner/migration` (DDL split, F9) | §6.3 |
| Unicidad de `sale_id` | No es global a nivel de esquema; descansa en el UUIDv7 del cliente y en la PK compuesta | §4 `sales`; §6.1 |

La unicidad de `sale_id` sola **no** es exigible en una tabla particionada: PostgreSQL obliga a que toda restricción única incluya las columnas de partición. Cualquier migración que intente añadir `UNIQUE(sale_id)` global fallará en el parser (`docs/DATABASE.md` §6.1).

### 7.2 Partición DEFAULT y remediación

`sales_default` es una red de seguridad, no un destino: captura filas cuyo `sold_on` no coincide con ninguna partición mensual (desviación de reloj del cliente, corrida de mantenimiento omitida, migración con fechas fuera de rango). Se crea una vez, junto con la primera partición mensual, y persiste de por vida (`docs/DATABASE.md` §6.4).

La remediación (F5) es un patrón de una sola transacción: se desacopla la partición DEFAULT, se crean las particiones mensuales faltantes, la partición desacoplada se re-acopla como partición mensual regular (las filas se enrutan por los límites de partición), y se re-crea una DEFAULT vacía. O todo queda en una DEFAULT vacía, o la transacción se revierte y la alerta escala a intervención humana. El patrón detach/re-attach evita el movimiento fila por fila, que mantendría un lock largo y bloquearía escrituras (`docs/DATABASE.md` §6.5).

### 7.3 Skip-without-blocking y requisito de alerta y runbook

Las fallas de mantenimiento de particiones son **por mes** y nunca bloquean el pipeline: si el job no crea la partición del mes M, el pipeline sigue aceptando escrituras del mes corriente (cuya partición ya existe) y la DEFAULT captura cualquier desborde. La falla se registra, dispara una alerta, y la siguiente corrida reintenta. El pipeline **no** se detiene esperando la creación de particiones (`docs/DATABASE.md` §6.6). Es una degradación deliberada: el sistema prefiere aterrizar filas en DEFAULT con una alerta antes que bloquear la venta.

La condición de alerta es exacta: `SELECT count(*) FROM sales_default > 0`. La alerta va al canal de operaciones, **no** al cajero (la reconciliación silenciosa es una preocupación aparte) (`docs/DATABASE.md` §6.7). El runbook cubre tres escenarios: desviación de reloj del cliente, corrida de mantenimiento omitida, y migración con fechas fuera de rango. El procedimiento operativo vive en la wiki de operaciones; el contrato de esquema solo debe hacer inambigua la condición de alerta y las primitivas de remediación (`docs/DATABASE.md` §6.7).

### 7.4 Redis: qué puede y qué no puede vivir ahí

Redis es caché y cola únicamente, nunca fuente de verdad (`ARCHITECTURE.md:20`). Namespace obligatorio: `tenant:{id}:...` (S1-4, `SPRINTS.md` S1-4).

| Puede vivir en Redis `(propuesta)` | No puede vivir en Redis |
| :--- | :--- |
| Claves de idempotencia en caché (acelerador opcional; la autoridad es `idempotency_keys`) | Stock, dinero o saldos de fiado como valor autoritativo (`ARCHITECTURE.md:20`) |
| Resultados de lookup de catálogo global con TTL corto | Un volcado global de SKUs para los dispositivos (`ARCHITECTURE.md:20,89`) |
| Buckets de rate limit, contadores efímeros | Blobs de fotos (`ARCHITECTURE.md:87`) |
| Estado de sesión efímero no autoritativo | Cualquier clave sin el prefijo `tenant:{id}:` (S1-4) |

El requisito de revisión de S1-4 es verificable por grep: cualquier clave sin prefijo de tenant debe fallar la revisión (`SPRINTS.md` S1-4).

### 7.5 Fotos en object storage

Las fotos viven en object storage; las filas de producto guardan **solo** la URL (`products.photo_url`), y no hay blobs en PostgreSQL, Redis ni IndexedDB (`ARCHITECTURE.md:21,87`; S2-4 `SPRINTS.md` S2-4). La prueba de S2-4 es directa: la carga devuelve una URL y los bytes del blob son rechazados por la fila de producto (`SPRINTS.md` S2-4, §8).

### 7.6 Política de migraciones

`docs/DATABASE.md` es un contrato de planificación, no SQL ejecutable: no contiene DDL ni archivos de migración, y cada columna se expresa como Markdown que los implementadores traducen a migraciones versionadas (`docs/DATABASE.md` §1). El backend, por tanto, es el dueño de producir ese SQL, con estas reglas de política `(propuesta, informada por §1, §5.5 y §8)`:

1. **Versionadas** — cada migración tiene un número y un archivo; no hay cambios ad-hoc de esquema.
2. **Aditivas primero** — se agrega antes de modificar o quitar; los campos nuevos son opcionales (regla de interfaz estable; ver §2.4-2.5).
3. **Reversibles donde sea posible** — toda migración declara su reverso; cuando un reverso es imposible, se documenta la razón.
4. **Ejecutadas por `owner/migration`** — el único rol con DDL; ningún rol de runtime lo tiene (`docs/DATABASE.md` §5.5).
5. **El contrato vive en `docs/DATABASE.md`** — si la migración y el documento discrepan, el documento es la autoridad y el defecto es la migración.
6. **Las columnas futuras documentadas no se crean todavía** — `tenants.master_password_hash` (D1), `fiado_accounts.credit_limit_cents` (D4), `stock_adjustments.evidence_photo_url` (D5), `products.cost_cents` (D11) y las columnas de float/egresos de `cash_closes` (D10) son notas delta docs-only, sin DDL en esta versión.

### 7.7 Pooling de conexiones

El requisito no es el tamaño del pool, sino la **seguridad del pooler**: `SET LOCAL` por transacción es lo único que impide que el contexto de tenant se filtre entre clientes que reutilizan una conexión (`docs/DATABASE.md` §5.2; `ARCHITECTURE.md:85`). Regla operativa: todo acceso a datos pasa por un helper de transacción que fija el GUC como primera sentencia y lo deja resetear al confirmar; ninguna consulta corre fuera de ese helper. Un pooler en modo transacción es compatible; uno en modo sesión mal configurado no lo es.

### 7.8 Respaldo y recuperación a un punto en el tiempo

Las fuentes no fijan una política de respaldo; el contenido comercial solo afirma que los costos marginales por usuario son muy reducidos gracias a la arquitectura multi-tenant compartida (`docs/BUSINESS.md:34`), y que el respaldo cloud diario es una capacidad del plan Estándar (`docs/BUSINESS.md:50`). Por lo tanto, lo siguiente es `(propuesta)` y debe validarse contra el plan de infraestructura real:

| Elemento `(propuesta)` | Propuesta |
| :--- | :--- |
| Respaldo base | Snapshot diario de PostgreSQL + archivado continuo de WAL |
| PITR | Recuperación a un punto en el tiempo dentro de la ventana de WAL, alineada con la retención de 400 días de los claims (§6.4) como cota inferior de seguridad para dinero |
| Restauración probada | Un ensayo de restauración por sprint de duración completa, no solo la existencia del respaldo |
| Object storage | Versionado del bucket de fotos; la base solo guarda URLs |
| Redis | No se respalda como fuente de verdad (no lo es) |

### 7.9 Observabilidad

`(propuesta)` en su forma concreta; los requisitos se derivan de los contratos de arriba.

| Elemento `(propuesta)` | Obligación |
| :--- | :--- |
| Logs estructurados | JSON con `request_id`, ruta, estado, duración, `tenant_id` (nunca PIN, fotos ni payloads) |
| Identificadores de petición | `request_id` generado en transporte y propagado a logs; idealmente también devuelto en la respuesta para correlación |
| Salud | `GET /healthz` (proceso vivo) y `GET /readyz` (Postgres y Redis alcanzables) — endpoints de operación, fuera de la superficie de negocio del §4 |
| Métricas que importan a este MVP | Tasa de claims nuevos vs. repetidos vs. `409` vs. `422`; profundidad y edad del backlog de flush por cliente; latencia p95 de `POST /sales`; filas en `sales_default`; tamaños de respuesta del cursor; errores `401` de login admin (para detectar fuerza bruta) |
| Alerta ya fijada por contrato | `count(*) FROM sales_default > 0` (§7.3) |

---

## 8. Rendimiento y Presupuestos

Todo número de esta sección que no provenga de una fuente está marcado `(propuesta)` y debe medirse antes de comprometerse.

### 8.1 El presupuesto que manda: la venta en menos de 3 segundos

La promesa de producto es una venta de mostrador en **menos de 3 segundos** (`docs/PRODUCT.md:28`, `:35`, `:78`). Esa promesa es del flujo completo del cajero (reconocimiento → carrito → cobro → vuelto), y la parte que el backend controla es la del cierre y su confirmación. El backend **no** puede hacer que la venta espere a la red: la venta se cierra localmente y se encola, y el `POST /sales` ocurre después (`ARCHITECTURE.md:98`). Su presupuesto, por tanto, es de confirmación y de proyección, no de bloqueo.

| Tramo `(propuesta)` | Presupuesto objetivo | Fundamento |
| :--- | :--- | :--- |
| Decode + auth + `SET LOCAL` | ≤ 50 ms | Costo local del proceso |
| Claim de idempotencia + escritura de negocio (una transacción) | ≤ 200 ms p50, ≤ 500 ms p95 | Una transacción, pocas filas, índices fijos |
| Respuesta con saldos proyectados | ≤ 300 ms p95 en total para `POST /sales` | La promesa < 3 s es del flujo cliente; esto deja holgura |
| Pull de cursor (respuesta completa) | ≤ 1 s p95 en el catálogo de un tenant de 200 productos | `SPRINTS.md` Sprint 3 (Semana 3) fija el orden de magnitud del catálogo |

Todo lo anterior es `(propuesta)`: nada en las fuentes fija un SLA de servidor. Se registra para que el Sprint 9 (Semana 9) pueda medirlo con evidencia y no con expectativa (S7-2, `SPRINTS.md` S7-2).

### 8.2 Tamaño de lote y retroceso

`ARCHITECTURE.md:84` fija el mecanismo (jitter + retroceso exponencial) y `SPRINTS.md` Sprint 6 (Semana 6) fija la prueba (100 ventas en cola hacen flush sin estampida, colapsando duplicados a un ack cada una). Los parámetros concretos no están en las fuentes:

| Parámetro `(propuesta)` | Valor propuesto | Nota |
| :--- | :--- | :--- |
| Tamaño máximo de lote | 25 eventos por flush | Acota el trabajo por transacción y la ventana de lock de claims |
| Orden dentro del lote | padre → hijo, reordenado por el servidor (§6.9) | Invariante de esquema, no responsabilidad del cliente |
| Base de retroceso | 1 s | Alineado con el jitter que exige la arquitectura |
| Factor | 2 (exponencial) | — |
| Techo de retroceso | 60 s | — |
| Jitter | ±30 % del intervalo | Evita estampida sincronizada |
| Reintento tras `409` | Respecta `Retry-After`; no abre una segunda venta | `docs/DATABASE.md` §7.1 Branch C |

### 8.3 Cobertura de índices de las consultas calientes

Los índices están fijados por el contrato; el backend no los inventa, los aprovecha. Mapeo consulta → índice que la sirve:

| Consulta caliente | Índice | Fuente |
| :--- | :--- | :--- |
| Pull por cursor de catálogo (`updated_at > cursor`, con tombstones) | `products_tenant_updated_at_idx` — sin predicado parcial a propósito | §4 `products`; §7.3 |
| Pull por cursor de fiado | `fiado_accounts_tenant_id_updated_at_idx` | §4 `fiado_accounts` |
| Búsqueda de login | índice de respaldo de `users_phone_key` | §4 `users` |
| Proyección de cierre por método (F8) | `sales_tenant_id_sold_on_payment_method_idx` | §4 `sales` |
| Rango por fecha de negocio | `sales_tenant_id_sold_on_idx` (con pruning de partición) | §4 `sales` |
| Ventas por cajero | `sales_tenant_id_sold_by_user_id_idx` | §4 `sales` |
| Saldo y listado de movimientos por cuenta | `fiado_movements_tenant_id_fiado_account_id_idx` | §4 `fiado_movements` |
| Proyección de stock por producto | `stock_adjustments_tenant_id_product_id_idx` | §4 `stock_adjustments` |
| Agregación de ventas por producto | `sale_lines_tenant_id_product_id_idx` | §4 `sale_lines` |
| Búsqueda de la venta original para anular | `sales_tenant_id_original_sold_on_original_sale_id_idx` | §4 `sales` |
| Claim de idempotencia | índice de respaldo de `UNIQUE (tenant_id, key)` — nunca duplicado | §4 `idempotency_keys` |

La fórmula de proyección de stock (`SUM(qty_delta)` por producto) y la de saldo de fiado (`SUM(amount_cents)` por cuenta) son consultas de agregación servidas por índices que empiezan por `tenant_id` (§2.6). No se propone ningún índice nuevo: el contrato ya cubre los caminos de acceso del MVP.

### 8.4 Costo visible del protocolo de sincronización

| Costo | Magnitud | Fundamento |
| :--- | :--- | :--- |
| Volumen de la respuesta del cursor | Acotado por el catálogo del tenant, no por un catálogo global | `ARCHITECTURE.md:127,129`; `docs/DATABASE.md` §7.3 |
| Volcado global a dispositivos | **Cero** — nunca un dump global de SKUs a los dispositivos | `ARCHITECTURE.md:89`; OI-15 |
| Almacenamiento local | Limitado al subconjunto de catálogo + cola de eventos, con degradación por cuota visible | S5-1 `SPRINTS.md` S5-1 |
| Fotos | Fuera de la base de datos, Redis e IndexedDB: solo URL | `ARCHITECTURE.md:87`; S2-4 |
| Costo de reintento | Cero duplicación por diseño (claim por restricción única) | `docs/DATABASE.md` §7.1 |

El objetivo de costo que las fuentes sí declaran es indirecto: los costos marginales por usuario se mantienen reducidos por la arquitectura multi-tenant compartida (`docs/BUSINESS.md:34`). Cualquier presupuesto de egreso concreto sería `(propuesta)`, porque las fuentes no fijan uno.

---

## 9. Estrategia de Pruebas Backend (TDD estricto)

### 9.1 La regla

**No hay código sin un test que falle primero.** Rojo → verde → refactor por tarea; los tests se entregan en la misma unidad de trabajo que el comportamiento que verifican, y el revisor mira el test antes de la implementación (`SPRINTS.md` §8). Una tarea está Done solo cuando su criterio de aceptación es **demostrablemente** cierto, no cuando existe el código (`SPRINTS.md` §1).

### 9.2 Runners — instalar y probar es la primera tarea del Sprint 1 (Semana 1)

Los runners requeridos son `go test ./...` (API) y `vitest` (PWA). **Ninguno existe todavía en disco**; la primera tarea del Sprint 1 (Semana 1) es instalarlos y probar que corren (`SPRINTS.md` §8). Hasta que ambos pasen, la verificación no puede probarse. Este documento no reclama ninguna corrida que no pueda existir.

### 9.3 Capas

| Capa | Alcance | Mecánica | Fuente |
| :--- | :--- | :--- | :--- |
| **Unitaria** | Funciones puras: totales, aritmética de saldos, redondeo half-up de líneas | Tabla de casos | `SPRINTS.md` §8 |
| **Integración** | API → PostgreSQL con RLS; sync → API | Se saltan bajo `testing.Short()` cuando son lentas | `SPRINTS.md` §8 |
| **Cliente (PWA)** | Persistencia de carrito, flush de cola, resolución silenciosa del lado servidor | `vitest` | `SPRINTS.md` §8 |

Reglas adicionales: probar comportamiento y transiciones de estado, no trivialidades; `t.TempDir()` para filesystem; los tests de RLS corren transacciones por tenant; los de idempotencia repiten la misma clave dos veces; los offline matan la pestaña o la red y luego hacen flush (`SPRINTS.md` §8).

### 9.4 Mapa objetivo de prueba → primer test que falla (backend)

Reproducido de `SPRINTS.md` §8 (mapa objetivo de prueba → primer test que falla); se marcan los que dependen del runner Go.

| Módulo | Objetivo de prueba | Runner | Primer test que falla | Fuente |
| :--- | :--- | :--- | :--- | :--- |
| Identity | El JWT lleva `tenant_id`; el tenant del cuerpo/encabezado se ignora | `go test ./...` | `POST /sales` con `tenant_id` falsificado en el cuerpo queda acotado al tenant del JWT | `SPRINTS.md` §8 |
| Identity | Aislamiento por RLS (`FORCE RLS`, `SET LOCAL`, pooler-safe) | `go test ./...` | El token del tenant A no puede seleccionar productos del tenant B | `SPRINTS.md` §8 |
| Identity | Device bind + sesión offline por PIN (7+ días) | `go test ./...` + `vitest` | Access token expirado + PIN válido todavía desbloquea offline | `SPRINTS.md` §8 |
| Catalog | CRUD de producto por tenant, UoM, precio en centavos | `go test ./...` | Crear producto con UoM `kg` persiste el campo UoM, no JSONB | `SPRINTS.md` §8 |
| Ledger | Cierre idempotente de `POST /sales` | `go test ./...` | Misma clave + mismo cuerpo repetidos → una venta, un movimiento de stock | `SPRINTS.md` §8 |
| Ledger | Reutilización de clave con cuerpo distinto → 422; duplicado en vuelo → 409 | `go test ./...` | Misma clave + cuerpo distinto devuelve 422, sin segunda venta | `SPRINTS.md` §8 |
| Ledger | `POST /sales/{id}/void` como evento compensatorio | `go test ./...` | La anulación conserva la venta original; el stock se mueve en reversa | `SPRINTS.md` §8 |
| Cash close | `POST /cash-closes` proyección vs. contado | `go test ./...` | El cierre iguala la suma de tenders del ledger; la diferencia se muestra | `SPRINTS.md` §8 |
| Fiado | Saldo = SUM(movements), nunca escribible | `go test ./...` | La escritura directa de saldo se rechaza; el saldo computado coincide con los movimientos | `SPRINTS.md` §8 |
| Fiado | `POST /fiado-movements` cargo/pago offline | `go test ./...` + `vitest` | El cargo a fiado offline se encola y se repite de forma idempotente | `SPRINTS.md` §8 |
| Photos | Solo URL de object storage | `go test ./...` | La fila de producto guarda URL; los bytes de blob se rechazan | `SPRINTS.md` §8 |

Las filas de Sync del plan (`SPRINTS.md` §8) son mayoritariamente `vitest` y pertenecen al plan frontend; la fila "Server-wins conflict UI" ya no existe —el plan corregido fija reconciliación silenciosa— y el registro se conserva en §11.4.

### 9.5 Sin números de cobertura

**Ningún número de cobertura se reclama.** Se agregan solo después de que los runners estén instalados y medidos (`SPRINTS.md` §8). Afirmar un porcentaje ahora sería fabricar evidencia.

---

## 10. Plan de Implementación por Sprints (Backend)

Cada sprint conserva los IDs de tarea exactamente como los nombra `SPRINTS.md`. Con la política de unidad **un sprint = una semana**, los bloques pasan a ser Sprint 1 (Semana 1) … Sprint 10 (Semana 10), una semana cada uno, y coinciden uno a uno con los bloques de `SPRINTS.md` §7. El número de sprint es el nuevo; los IDs de tarea conservan su número previo (`S0-*` … `S7-*`) y resuelven por la columna `ID previo` de `SPRINTS.md` §7. Las celdas "Depende de" usan la numeración nueva de sprints. La columna Estimates de `SPRINTS.md` no se reproduce aquí: ese documento no define su unidad, y reinterpretarla equivaldría a inventar una estimación (ver §13). El orden de dependencia es Identity → Catalog → Cart/Sale → Cash close → Sync → Fiado → Pilot (`SPRINTS.md` §1).

### 10.1 Sprint 1 (Semana 1) — Fundaciones y congelamiento

Este es el sprint de congelamiento y decisión, y además el del andamiaje de las tres capas (`SPRINTS.md` Sprint 1 (Semana 1), reescrito el 2026-10-14). `SPRINTS.md` es la autoridad de secuenciación: hoy el sprint incluye la migración de las 14 tablas, la partición mensual, el runner `vitest` y el scaffold de la PWA, además de los cuatro entregables de congelamiento. Esta tabla cubre los entregables de congelamiento y el andamiaje del proceso Go (`W1-BE-1`); la migración y el frontend viven en las filas `W1-DB-*` y `W1-FE-*` de `SPRINTS.md`.

| ID | Tarea (backend) | Entregable backend | Criterio de aceptación | Fuente |
| :--- | :--- | :--- | :--- | :--- |
| S0-1 | Glosario de una página (tenant, device, cashier, product, GTIN, sale, tender, fiado, sync event) | Tabla de glosario §3.1 | Cada término tiene un nombre y un significado; revisado contra `ARCHITECTURE.md` | `SPRINTS.md` S0-1 |
| S0-2 | Lista de campos de dominio para las **14 tablas** del contrato (7 grupos de módulos); `tenders` y `sync_events` se tratan como conceptos, no como tablas (ver nota) | Tablas §3.2 referenciando `docs/DATABASE.md` §4 | Dinero en centavos enteros; sin datos de negocio en JSONB; coincide con la tabla de propiedad de datos | `SPRINTS.md` S0-2 |
| S0-3 | Secuencia del protocolo offline (cierre offline → reintento → POST duplicado → ack) | §6.1-6.3 | El diagrama cubre reutilización de clave, manejo 409/422 y regla de servidor-gana | `SPRINTS.md` S0-3 |
| S0-4 | Apuntes de amenaza | Tabla §5.7 | Declara `FORCE RLS` + `SET LOCAL` por transacción y la expiración de PIN/sesión offline | `SPRINTS.md` S0-4 |

> **Nota sobre S0-2 (drift verificado y corregido en el refactor de `SPRINTS.md`).** El plan previo pedía publicar la lista de campos también para `tenders` y `sync_events`. Esas dos no son tablas en el contrato vinculante: `tenders` queda superseded por `sales.payment_method` y `sync_events` se fusiona en `idempotency_keys` (`docs/DATABASE.md` §8, enmiendas 1 y 5; conteo vinculante 14). El plan corregido (`SPRINTS.md` S0-2, en el Sprint 1 (Semana 1)) acota la lista a las **14 tablas** del contrato y trata `tenders`/`sync_events` como columnas/conceptos, no como tablas.

**Definition of Done:** glosario + lista de campos + protocolo + apuntes revisados; términos que coinciden con `ARCHITECTURE.md`; el runner `go test ./...` existe, corre y pasa; sin código de negocio (`SPRINTS.md` Sprint 1 (Semana 1)).

> **Nota de alcance (2026-10-15).** Los cuatro entregables de congelamiento ya tienen su contenido redactado en este documento: el glosario en §3.1, la lista de campos de las 14 tablas en §3.2, la secuencia del protocolo offline en §6.1–6.3 y los apuntes de amenaza en §5.7. Sprint 1 los toma como **revisión y publicación**, no como autoría desde cero. La versión anterior de esta sección decía que el sprint "no requiere código"; quedó obsoleta cuando `SPRINTS.md` absorbió el andamiaje de las tres capas.

### 10.2 Sprint 2 (Semana 2) — Identidad y aislamiento de tenant

**Meta:** nadie vende sin tenant, y nadie lee a otro (`SPRINTS.md` Sprint 2 (Semana 2)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S1-1 | Implementar `POST /auth/login` emitiendo JWT con `tenant_id` + `user_id` | §4.2 | Test-first: una petición sin tenant en cuerpo/encabezado resuelve el tenant solo del token | Sprint 1 |
| S1-2 | Forzar RLS de PostgreSQL (`FORCE RLS`, `SET LOCAL` por transacción, pooler-safe) | §5.3; §7.7 | Test-first: SELECT/INSERT cross-tenant falla en un test automatizado | S1-1 |
| S1-3 | Implementar `POST /auth/device-bind` + sesión PIN offline válida 7+ días | §4.3; §5.2 | Test-first: access token expirado con PIN válido todavía desbloquea el POS offline | S1-2 |
| S1-4 | Namespacear todas las claves Redis `tenant:{id}:...` | §7.4 | Test-first: cualquier clave sin prefijo de tenant falla la revisión/grep | S1-2 |
| — | **Instalar y probar los runners** (`go test ./...`, `vitest`) | §9.2 | Ambos corren y pasan en un commit inicial | (primera tarea del sprint) |

**Definition of Done:** login → JWT → ruta de consulta con scope de tenant probado por tests de aislamiento que fallan y luego pasan; ningún valor de tenant aceptado desde cuerpo o encabezado (`SPRINTS.md` Sprint 2 (Semana 2)).

### 10.3 Sprint 3 (Semana 3) — Catálogo de tenant (sin universo global)

**Meta:** la bodega vende 50–200 productos propios sin catálogo maestro (`SPRINTS.md` Sprint 3 (Semana 3)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S2-1 | Implementar CRUD de producto (`GET/POST /products`, `PATCH /products/{id}`): nombre, GTIN opcional, precio en centavos, UoM, stock opcional, URL de foto | §4.5-4.7 | Test-first: pack vs. unidad se guarda como campo UoM; precio/stock como columnas, no JSONB | Sprint 2 |
| S2-3 | Añadir stub opcional de lookup de GTIN bajo demanda (solo cache-miss) | §4.6 (adopción, F13); §5.6 | Test-first: el teléfono nunca descarga una lista global de SKUs; el camino de miss verificado | S2-1 |
| S2-4 | Fotos en object storage; la fila de producto guarda solo URL | §7.5 | Test-first: la carga devuelve URL; sin bytes de blob en Postgres/Redis/IndexedDB | S2-1 |

(S2-2, la grilla de favoritos, es una tarea del cliente; el backend solo debe servir el catálogo que la grilla consume: `SPRINTS.md` Sprint 3 (Semana 3).)

**Definition of Done:** un catálogo de 200 productos por tenant se sirve a un Android de gama baja; las escrituras de catálogo quedan con scope de tenant; no existe camino de escritura al catálogo global (`SPRINTS.md` Sprint 3 (Semana 3)).

### 10.4 Sprint 4 (Semana 4) — Venta de mostrador

**Meta:** cobrar rápido sin depender de la cámara (`SPRINTS.md` Sprint 4 (Semana 4)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S3-3 | Implementar `POST /sales` con cierre append-only y `Idempotency-Key` generada por el cliente | §4.9; §6.1-6.3 | Test-first: repetir la misma clave + mismo cuerpo devuelve el mismo ack sin duplicar dinero/stock | Sprint 3 |

(S3-1, S3-2, S3-4 y S3-5 son tareas del cliente; el backend sostiene S3-3 y el contrato de tender de §4.9.)

> **Drift S3-2 (verificado y corregido en el refactor de `SPRINTS.md`).** El plan previo listaba cinco tenders (`cash`/`yape`/`plin`/`card`/`other`) y su criterio de aceptación pedía "split tender (cash + Yape) cierra con el vuelto correcto". El contrato tiene **seis** tenders (agrega `fiado`) y **prohíbe** el split tender (`docs/DATABASE.md` §4 `sales`; §8 enmienda 7; §9 "Multi-tender / split tender" fuera de alcance). El plan corregido (`SPRINTS.md` S3-2, en el Sprint 4 (Semana 4)) exige **sin split tender** y exactamente **seis** valores de `payment_method`; el backend implementa ese contrato.

**Definition of Done (backend):** grid → carrito → cobro → cierre → ticket funciona online; el test de repetición de idempotencia pasa; la cámara nunca bloquea (`SPRINTS.md` Sprint 4 (Semana 4)).

### 10.5 Sprint 5 (Semana 5) — Cierre de caja y durabilidad

**Meta:** los números de fin de día cuadran con el ledger, incluso tras un crash (`SPRINTS.md` Sprint 5 (Semana 5)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S4-1 | Implementar `POST /cash-closes`: esperado vs. contado por tender con diferencia visible | §4.13 | Test-first: el reporte es una proyección de los eventos del ledger, no un segundo contador | Sprint 4 |
| S4-2 | Implementar `POST /sales/{id}/void` como evento compensatorio | §4.10 | Test-first: la venta original se conserva; stock y fiado se mueven en reversa | Sprint 4 |
| S4-4 | Documentar sync de free tier + recuperación de teléfono perdido (re-login restaura desde el servidor) | §5.2; §7.8 | Test-first walkthrough: wipe/reinstall restaura el historial de ventas desde el servidor | S4-1 |

(S4-3, persistir la venta cerrada localmente antes del ack, es cliente; el backend garantiza que el ack lleve los saldos proyectados.)

**Definition of Done:** el cierre de caja reconcilia con las proyecciones del ledger; las anulaciones nunca borran; las ventas cerradas sobreviven la muerte de la pestaña (`SPRINTS.md` Sprint 5 (Semana 5)).

### 10.6 Sprint 6 (Semana 6) — Sincronización offline (el núcleo)

**Meta:** que las caídas de 3G no detengan las ventas ni cobren doble (`SPRINTS.md` Sprint 6 (Semana 6)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S5-2 | Soportar el worker de sync del cliente: lote, jitter, retroceso exponencial, POST idempotente | §6.1-6.5; §8.2 | Test-first: 100 ventas en cola hacen flush sin estampida; los duplicados colapsan a un ack cada uno | Sprint 5 |
| S5-3 | **Reconciliación silenciosa del lado servidor** para stock y fiado (ver §11.4) | §6.7, §6.9 | Test-first: una repetición conflictiva se resuelve server-side y el POS no recibe error | Sprint 5 |
| — | Pull por cursor (`GET /sync/cursor`) con ventana de solape de 15 minutos | §4.8; §6.6 | Test-first: una fila actualizada durante el armado de la respuesta se re-trae en el próximo pull | Sprint 5 |

(S5-1 y S5-4 son cliente; el backend debe proveer el cursor y la resolución silenciosa que el worker consume.)

**Definition of Done (backend):** el flush en lote es idempotente y la reconciliación es silenciosa de punta a punta; ninguna violación única inbound llega al cajero (`docs/DATABASE.md` §7.9).

### 10.7 Sprint 7 (Semana 7) — Fiados

**Meta:** que quién debe qué sobreviva a un teléfono robado (`SPRINTS.md` Sprint 7 (Semana 7)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S6-1 | Implementar el registro de cliente (nombre requerido, teléfono opcional) + `GET /fiado-accounts` con saldo computado | §4.12 | Test-first: el saldo iguala SUM(movements); sin columna de saldo escribible | Sprint 6 |
| S6-2 | Implementar `POST /fiado-movements` para cargo y pago parcial | §4.11 | Test-first: el cargo a fiado offline se encola y sincroniza con las mismas reglas de idempotencia que las ventas | S6-1 |
| S6-3 | Servir el historial de movimientos de fiado para restaurar tras reinstalación | §4.12 | Test-first: wipe/reinstall restaura cuentas + movimientos tras el login | S6-2 |
| S6-4 | Confirmar que no se envía ningún recordatorio de deuda por WhatsApp en el MVP | §11.3 | Revisión: ningún camino de código WABA/`wa.me` en el incremento | S6-2 |

**Definition of Done:** el cargo a fiado y el pago parcial funcionan offline y sincronizan de forma idempotente; los saldos son computados, nunca escritos (`SPRINTS.md` Sprint 7 (Semana 7)).

### 10.8 Sprint 8 (Semana 8) — Dashboard Admin y endurecimiento

**Meta:** el dueño lee su propio negocio con la proyección del ledger local (IndexedDB) más `GET /fiado-accounts`, sin agregar ningún endpoint de reporte; el borde de la API queda endurecido (`SPRINTS.md` Sprint 8 (Semana 8)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| W8-BE-1 | Implementar `POST /auth/admin-login`: resuelve teléfono → tenant, verifica `tenants.master_password_hash` (argon2id) y emite un JWT con `role:"admin"` | §4.4 | Test-first: `401` genérico y constante, sin enumeración de usuarios; un token de empleado no lleva el claim `role` | Sprint 7 |
| W8-BE-2 | Implementar `POST /stock-adjustments`: delta firmado negativo con `source = 'manual'` y uno de los tres motivos exactos | §4.14 | Test-first: la merma anexa una fila al log y mueve el stock de forma aditiva sin tocar `products.updated_at`; el stock negativo se permite | Sprint 7 |
| W8-BE-3 | Endurecer el borde: un solo sobre de error, validación de los conjuntos acotados y chequeos de salud `(propuesta)` | §4.1; §7.9 | Test-first: toda respuesta de error usa la misma forma y un `PATCH` con `stock` o que intenta quitar un campo devuelve `422` | Sprint 4 |
| W8-BE-4 | Observabilidad y alerta operativa `(propuesta)`: logs estructurados sin PIN, fotos ni payloads; alerta por filas en `sales_default` al canal de operaciones | §7.9; §7.3 | La alerta de particiones llega al canal de operaciones y nunca al cajero | Sprint 6 |
| W8-DB-1 | Migrar `tenants.master_password_hash` (`TEXT NOT NULL`, argon2id) por el rol de migración, más el backfill de alta de administrador | §4.4; §7.6 | La columna existe y `POST /auth/admin-login` puede implementarse; queda decidido qué ocurre con un tenant sin hash | Sprint 7 |
| W8-DB-2 | Revisión final del contrato de esquema contra el checklist de arquitectura (14 tablas, dos columnas JSONB, seis valores de `payment_method`, cero concesiones de tenant sobre las globales, append-only en cinco tablas) | §5.5; §2.12 | `pos_app` sin `DELETE` sobre tablas de negocio ni `BYPASSRLS`; las tablas globales sin política de tenant | Sprint 7 |

**Definition of Done:** el dueño entra con la contraseña maestra del tenant y ve sus propios números sin que exista un endpoint de reporte; la merma se registra como delta firmado con un motivo de la lista exacta; el checklist de esquema se verifica entero sobre las 14 tablas (`SPRINTS.md` Sprint 8 (Semana 8)).

### 10.9 Sprint 9 (Semana 9) — Piloto de campo (10 bodegas, no 20)

**Meta:** probar el analgésico antes de cualquier trabajo de minimarket; el bloque arranca el piloto, mide y deja la operación de siete días en marcha (`SPRINTS.md` Sprint 9 (Semana 9)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S7-2 | Instrumentar y medir p50/p95 del camino de venta y de la latencia de flush | §7.9; §8.1 | Métricas registradas; la grilla sigue siendo el default si el p95 de la cámara supera 3 s | Sprint 8 |
| S7-3 | Arrancar la operación de 7 días consecutivos: ventas + fiados + cierre de caja con sync tras cortes | §6; §7 | La operación arranca con cero duplicados silenciosos y cero fugas cross-tenant | Sprint 8 |

**Definition of Done:** la bodega se da de alta en menos de 10 minutos, los percentiles quedan registrados y la operación de siete días ha arrancado (`SPRINTS.md` Sprint 9 (Semana 9)).

### 10.10 Sprint 10 (Semana 10) — Piloto de campo, cierre y kill list

**Meta:** cerrar los siete días, revisar conflictos y duplicados jornada por jornada y publicar la lista de corte (`SPRINTS.md` Sprint 10 (Semana 10)).

| ID | Tarea (backend) | Entregable | Aceptación | Depende de |
| :--- | :--- | :--- | :--- | :--- |
| S7-4 | Publicar la lista de corte: qué cortar y qué construir después | §11.2-11.3 | Lista firmada por el Product Owner; los epics posteriores quedan sin agendar | S7-3 |

**Definition of Done:** log de piloto de 7 días completo; kill list firmada; los Later epics siguen sin agendar hasta que la evidencia diga lo contrario (`SPRINTS.md` Sprint 10 (Semana 10)).

---

## 11. Riesgos, Decisiones Diferidas y No-Alcance

### 11.1 Riesgos

| Riesgo | Impacto | Mitigación | Fuente |
| :--- | :--- | :--- | :--- |
| Duplicación de dinero o stock por reintentos | Crítico: cobro doble o stock fantasma | Claim por restricción única + hash de payload + 409/422; tabla de decisión §6.3 | §7.1; `ARCHITECTURE.md:106-107` |
| Fuga de lectura cross-tenant | Crítico: confianza y legal | `FORCE RLS` + `SET LOCAL` + grant matrix + tests fail-then-pass | §5.3; §5.7 |
| Divergencia de documentos (conflict UI vs. silencio) | Alto: el equipo puede construir la UI equivocada | Divergencia declarada y **resuelta** (§11.4 #1): `docs/DESIGN.md` §5.1 y el nuevo `SPRINTS.md` ya fijan reconciliación silenciosa (2026-10-14); enmienda #3 registrada | `docs/DATABASE.md` §8 enmienda 3; `docs/DESIGN.md` §5.1 |
| Fila que aterriza en `sales_default` por desviación de reloj | Medio: venta fuera de partición | Red de seguridad DEFAULT + alerta + runbook; skip-without-blocking | §7.2-7.3 |
| Contradicción interna de `docs/DATABASE.md` §7.1-7.2 sobre columnas de idempotencia | Medio: el implementador puede codificar contra nombres inexistentes | §6.2 fija la tabla (§4) como autoridad; corrección de prosa al follow-up | `docs/DATABASE.md` §4 vs §7.1 |
| Idealización de LWW hacia dinero o stock | Crítico si se cuela | Frontera de daño explícita; `stock` excluido de PATCH | §6.7; `docs/DATABASE.md` §7.4 |
| Runners inexistentes al empezar | Alto: no hay verificación posible | Instalar y probar `go test ./...` y `vitest` como primera tarea del Sprint 1 (Semana 1) | `SPRINTS.md` §8 |
| Derivación del reloj del cliente | Medio: `sold_on` futuro | El servidor nunca mueve una venta de día; la partición futura no existe y DEFAULT la captura | §6.6; §7.2 |
| Exposición de columnas futuras como si existieran | Medio: contratos rotos en la UI | Las cinco columnas futuras se declaran docs-only y sin DDL | §7.6; §11.2 |
| No hay endpoint de reporte y el dashboard Admin lo espera | Medio: brecha de expectativa UI/API | §11.3 lo fija como no-alcance; el dashboard usa el ledger local + `GET /fiado-accounts` | `ARCHITECTURE.md:138` |

### 11.2 Registro consolidado de deltas

El corpus usa **tres numeraciones D distintas** que NO deben confundirse. Antes de asignarle un significado a un "D-n" hay que saber de qué documento viene:

| Esquema | Rango | Qué numera | Fuente |
| :--- | :--- | :--- | :--- |
| Decisiones vinculantes de esquema | D1–D11 | Las decisiones de `docs/DATABASE.md` §8 (D1 shared DB … D11 sexto tender) | `docs/DATABASE.md` §8 |
| Deltas del cambio SDD `stitch-admin-prompts` | D1–D12 | Cambios de contrato docs-only que las pantallas Admin P1–P8 requieren | `odd/tasks/stitch-admin-prompts.md:121-127` |
| Tareas del cambio `dual-login-mermas-cleanup` | D1–D4 | IDs de **tarea** (generar pantallas, verificar, exportar), no deltas de esquema | `odd/tasks/dual-login-mermas-cleanup.md:35-39` |

**Deltas del cambio `stitch-admin-prompts` (D1–D12).** Estado y contenido verificados contra los contratos vivos:

| ID | Contenido | Estado en esta versión | Qué NO se construye | Fuente |
| :--- | :--- | :--- | :--- | :--- |
| D1 | `tenants.master_password_hash TEXT NOT NULL` (argon2id) | Adoptado en contrato, **docs-only, sin DDL** | La columna no existe aún; el backfill corre vía admin-setup | `docs/DATABASE.md` §4 `tenants` (nota delta D1) |
| D2 | Endpoint `POST /auth/admin-login` | Adoptado; fila en la tabla de superficie HTTP | — (la fila existe; el handler es trabajo de implementación) | `ARCHITECTURE.md:126` |
| D3 | Claim JWT `role:"admin"`; tokens de empleado sin el claim | Adoptado | — | `ARCHITECTURE.md:23` |
| D4 | `fiado_accounts.credit_limit_cents BIGINT NULL` | Adoptado en contrato, **docs-only, sin DDL** | La columna no existe; la UI P6 espera el contrato, no lo inventa | `docs/DATABASE.md` §4 `fiado_accounts` (nota delta D4) |
| D5 | `stock_adjustments.evidence_photo_url TEXT NULL` | **Diferido** | Sin control de foto en el formulario de merma | `docs/DATABASE.md` §4 `stock_adjustments` (nota delta D5) |
| D6 | Token de color `card` en el set de tokens `(frontend)` | Adoptado en la capa de diseño | No es un cambio de esquema | `odd/tasks/stitch-admin-prompts.md:121` |
| D7 | — | **No verificable en disco** | El contenido exacto de D7 vive en el design §12 del cambio (Engram), no en un archivo del repositorio | (n/a) |
| D8 | Relacionado con la retirada del diálogo de conflicto | **No verificable en disco como delta numerado**; el efecto (silencio) sí está fijado | El diálogo de conflicto no debe construirse | `odd/tasks/stitch-admin-prompts.md:62`; `docs/DATABASE.md` §7.9 |
| D9 | Teléfono como identificador de login (sin correo) | Adoptado | Sin campo `email` | `odd/tasks/stitch-admin-prompts.md:121` |
| D10 | `cash_closes.opening_float_cents` + manejo de egresos | **Diferido** | Sin fondo inicial ni egresos; la fórmula `difference = counted − expected` queda sin término de egreso | `docs/DATABASE.md` §4 `cash_closes` (nota delta D10) |
| D11 | `products.cost_cents` + margen comercial | **Diferido** | Sin márgenes, sin costos, sin "net profit"; `price_cents` sigue siendo la única edición de precio | `docs/DATABASE.md` §4 `products` (nota delta D11) |
| D12 | Endpoints de reporte (`GET /sales` / `GET /dashboard/summary`) + tabla de asistencia | **Diferido** | **Ningún endpoint de reporte se agrega**; sin tabla de asistencia | `ARCHITECTURE.md:138`; `SPRINTS.md` §6, §11 |

> **Honestidad de contrato.** Los IDs D7 y D8 no se pueden resolver de forma verificable contra archivos del repositorio: su texto vive en el design §12 del cambio, alojado en Engram. Se declaran no verificables en lugar de inferirlos. Además, la correspondencia entre "D-n" y su contenido no es la misma entre los tres esquemas: cualquier referencia a "D6" o "D10" debe citar el documento de origen.

### 11.3 No-metas (explícitas)

Estos caminos **no** se construyen en el MVP y no deben dibujarse en el backend (`ARCHITECTURE.md:154-164`; `docs/DATABASE.md` §9; `SPRINTS.md` §6):

| No-meta | Nota |
| :--- | :--- |
| Catálogo global crowdsourced como camino de escritura | La curación es solo de plataforma en el MVP (`docs/DATABASE.md` D6) |
| Millones de SKUs en IndexedDB o Redis | Nunca un volcado global a dispositivos (OI-15) |
| WhatsApp API / automatización `wa.me` | Sin camino WABA/`wa.me` (`SPRINTS.md` S6-4) |
| Conciliación automática de Yape/Plin | El tender se elige por el cajero |
| SUNAT / facturación electrónica | Solo campos reservados no fiscales en `sales` (`receipt_type`, `receipt_series`) |
| I/O de hardware (HID, ESC/POS, balanzas, Tauri) | — |
| Multi-cajero / turnos | Later epic (`SPRINTS.md` §11) |
| Split tender | Tender único por venta (`docs/DATABASE.md` D9) |
| Tablas de billing / planes / suscripciones | `plan` es un solo campo en `tenants` (`docs/DATABASE.md` D7) |
| Compras / proveedores / "net profit" | Later epic (`SPRINTS.md` §11) |
| WebSocket / WSS | WSS no es parte de la arquitectura (`ARCHITECTURE.md:91`) |
| WebUSB / HID / ESC-POS | Fuera de la arquitectura (`ARCHITECTURE.md:27`) |
| Endpoints de reporte agregado | D12 diferido; sin `GET /sales` ni `GET /dashboard/summary` (`ARCHITECTURE.md:138`) |
| Tabla de asistencia / marcaje | Post-MVP (`SPRINTS.md` §6, §11) |

### 11.4 Divergencias entre documentos (registro)

Estas son contradicciones verificadas entre artefactos vivos. Se declaran con su cita; este documento **no elige en silencio**. Las resueltas se conservan registradas con su evidencia y su fecha.

| # | Divergencia | Documentos en conflicto | Resolución recomendada |
| :--- | :--- | :--- | :--- |
| 1 | **Reconciliación silenciosa vs. UI de conflicto — RESUELTA (2026-10-14).** `ARCHITECTURE.md:24,111-116` y `docs/DATABASE.md` §7.9 fijan reconciliación silenciosa ("el POS nunca expone errores de sync al cajero"), y `docs/DATABASE.md` §8 enmienda 3 declara que la "Conflict UI (server X vs device Y)" **debe** reemplazarse por silencio. Antes de esta sesión, el S0-3 del plan previo, el S5-3, el Definition of Done de esa semana y el mapa de pruebas exigían UI de conflicto explícita, y `docs/DESIGN.md:256` describía el diálogo "Servidor: S/ X — Este dispositivo: S/ Y". Hoy ambos lados están corregidos en disco. | `ARCHITECTURE.md` + `docs/DATABASE.md` §7.9/§8 vs. `SPRINTS.md` + `docs/DESIGN.md` (ya alineados) | **Gana el silencio, y ya está aplicado (2026-10-14).** Evidencia: `docs/DESIGN.md` §5.1 define la reconciliación silenciosa sin diálogo, y el nuevo `SPRINTS.md` no contiene ningún requisito de UI de conflicto. Enmienda #3 de `docs/DATABASE.md` §8 registrada; el backend implementa reconciliación silenciosa desde el Sprint 6 (Semana 6). La fila se conserva en el registro aunque esté resuelta. |
| 2 | **Columnas de idempotencia nombradas de dos formas.** §7.1/§7.2 usan `request_hash`, `completed_at`, `response_status`, `response_body`; §4 `idempotency_keys` define `payload_hash`, `state`, `response_snapshot`, `created_at` | `docs/DATABASE.md` §7.1-7.2 vs. §4 | Implementar contra §4 (la tabla es el contrato ejecutable); corregir la prosa de §7 en el follow-up |
| 3 | **`sale_lines` nombrada como log de stock.** §7.5 dice que el log append-only de stock es `sale_lines`; el resto del documento y `ARCHITECTURE.md` dicen `stock_adjustments` | `docs/DATABASE.md` §7.5 vs. §4/§7.7 y `ARCHITECTURE.md:176` | Implementar stock sobre `stock_adjustments`; corregir la frase de §7.5 |
| 4 | **Split tender exigido por un criterio de aceptación — CORREGIDO en el refactor de `SPRINTS.md`.** El S3-2 del plan previo exigía que "split tender (cash + Yape) cierre con el vuelto correcto" y listaba cinco tenders; el contrato prohíbe el split tender y fija seis tenders. El plan corregido (`SPRINTS.md` S3-2, en el Sprint 4 (Semana 4)) exige **sin split tender** y exactamente **seis** valores de `payment_method`. | El plan previo (`SPRINTS.md` S3-2) vs. `docs/DATABASE.md` §4/§8 enmienda 7/§9 (gana el contrato) | Gana el contrato; el criterio de S3-2 ya quedó reescrito en el refactor. Se conserva el registro del drift. |
| 5 | **`tenders` / `sync_events` como tablas en S0-2 — CORREGIDO en el refactor de `SPRINTS.md`.** El S0-2 del plan previo pedía la lista de campos para ambas; el conteo vinculante es 14 y ambas están superseded/fusionadas. El plan corregido (`SPRINTS.md` S0-2, en el Sprint 1 (Semana 1)) publica la lista para las **14 tablas** del contrato y las trata como conceptos, no como tablas. | El plan previo (`SPRINTS.md` S0-2) vs. `docs/DATABASE.md` §8 enmiendas 1 y 5 | Publicar campos para las 14 tablas del contrato; tratar ambas como conceptos, no como tablas. Se conserva el registro del drift. |
| 6 | **Referencias de línea obsoletas hacia `docs/DATABASE.md`.** `docs/FRONTEND.md:50` cita `DATABASE.md:310/:466/:477` para el enum de seis tenders (real: `:341` y `:511`); `docs/FRONTEND.md:52` cita `DATABASE.md:270` para `stock = SUM(qty_delta)` (real: `:291` y `:253`); las propias notas delta de `docs/DATABASE.md` citan rangos corridos ~7–9 líneas (`:234-249` para `products`, `:272-283` para `stock_adjustments`) | `docs/FRONTEND.md`, `docs/DESIGN.md` y las notas delta de `docs/DATABASE.md` | Preferir referencias de sección (`docs/DATABASE.md` §4) sobre números de línea en citas cruzadas; este documento cita por sección donde el número no resuelve |
| 7 | **`tenants.master_password_hash NOT NULL` sin default vs. "tenant sin hash → 401".** La nota delta lo declara `TEXT NOT NULL` sin default, mientras afirma que un tenant sin hash produce un 401 genérico | `docs/DATABASE.md` §4 `tenants` (nota delta D1) | Decidir si la columna es nullable o si el backfill es obligatorio; si es `NOT NULL`, el caso "tenant sin hash" necesita un default o una política de backfill explícita |

---

## 12. Checklist de Aceptación Backend

Derivado del Checklist de `ARCHITECTURE.md:166-178` y del Definition of Done de cada sprint, expresado como hechos backend **demostrables** en lugar de "existe código".

**Recuperación y durabilidad**

- [ ] Una venta cerrada se recupera tras matar la pestaña (evento en IndexedDB) y tras perder el teléfono (evento en el servidor) (`ARCHITECTURE.md:168`).
- [ ] El historial de ventas y los fiados se restauran desde el servidor tras wipe/reinstall (`SPRINTS.md` S4-4, S6-3).

**Idempotencia y dinero**

- [ ] Repetir `POST /sales` con la misma clave no duplica dinero ni stock (`ARCHITECTURE.md:169`).
- [ ] La misma clave con un cuerpo distinto devuelve `422` y no crea una segunda venta (`SPRINTS.md` §8).
- [ ] Un duplicado en vuelo devuelve `409` con `Retry-After` y el cliente no abre una segunda venta (`docs/DATABASE.md` §7.1 Branch C).
- [ ] Una clave repetida devuelve `200` con el cuerpo original almacenado, sin ejecutar la capa de negocio (`docs/DATABASE.md` §7.1 Branch A).
- [ ] Los claims se retienen 400 días y el borrado corre solo bajo `pos_maintenance` (`docs/DATABASE.md` §7.2).

**Aislamiento de tenant**

- [ ] Una consulta sin el tenant del JWT no puede leer filas de otro tenant (`ARCHITECTURE.md:170`).
- [ ] El aislamiento se prueba por un test que falla antes de RLS y pasa después (`docs/DATABASE.md` §5.7).
- [ ] Ningún endpoint acepta `tenant_id` desde cuerpo o encabezado (`ARCHITECTURE.md:23`).
- [ ] Toda clave Redis empieza por `tenant:{id}:...` y la revisión de grep pasa (`SPRINTS.md` S1-4).

**Modelo de datos**

- [ ] El contador de tablas es **14** y ni `tenders` ni `sync_events` existen como tablas (`ARCHITECTURE.md:142`; `docs/DATABASE.md` §8).
- [ ] IndexedDB nunca guarda el catálogo global (`ARCHITECTURE.md:171`).
- [ ] El saldo de fiado no es una columna escribible; es `SUM(movements)` (`ARCHITECTURE.md:172`; `SPRINTS.md` §8).
- [ ] `FORCE RLS` + `SET LOCAL app.current_tenant_id` por transacción, pooler-safe (`ARCHITECTURE.md:173`).
- [ ] El dinero es `BIGINT` en centavos enteros en todas partes; sin `NUMERIC` ni `float` para dinero (`ARCHITECTURE.md:174`).
- [ ] JSONB aparece en exactamente dos columnas: `products.attributes` y `global_products.attributes` (`ARCHITECTURE.md:175`).
- [ ] El stock converge por deltas firmados aditivos en `stock_adjustments`, nunca por LWW (`ARCHITECTURE.md:176`).
- [ ] `sales` está particionada por mes sobre `sold_on` y su PK es `(sold_on, sale_id)` (`ARCHITECTURE.md:177`).
- [ ] `payment_method` tiene exactamente seis valores en `sales` y en `cash_close_counts` (`docs/DATABASE.md` D11).
- [ ] `PATCH /products/{id}` no acepta `stock` y es solo aditivo (`docs/DATABASE.md` §4 `products`).

**Sincronización y reconciliación**

- [ ] Los únicos POST encolables son los seis de la enumeración exhaustiva (`docs/DATABASE.md` §7.6).
- [ ] El flush reordena padre → hijo de forma determinista, sin depender del orden del cliente (`docs/DATABASE.md` §7.8).
- [ ] Los conflictos de stock y fiado se resuelven server-side de forma silenciosa; el POS no ve errores de sync (`ARCHITECTURE.md:178`; `docs/DATABASE.md` §7.9).
- [ ] La colisión `(tenant_id, name)` produce un renombrado determinista; la `(tenant_id, gtin)` deja el GTIN en `NULL` (`docs/DATABASE.md` §7.7).
- [ ] Un cierre de caja duplicado por fecha se confirma como no-op y el cierre existente prevalece (`docs/DATABASE.md` §7.7).
- [ ] El cursor no avanza más allá de `MAX(updated_at)` devuelto y respeta la ventana de solape de 15 minutos (`docs/DATABASE.md` §7.3).

**Fiado y cierre de caja**

- [ ] El cargo a fiado y el pago parcial funcionan offline y sincronizan idempotentemente (`SPRINTS.md` Sprint 7 (Semana 7)).
- [ ] El cierre de caja reconcilia con las proyecciones del ledger y muestra la diferencia computada (`SPRINTS.md` Sprint 5 (Semana 5)).
- [ ] `counted_cents = 0` para `fiado` es estructural (CHECK), y la diferencia de fiado nunca se marca como faltante (`docs/DATABASE.md` F32; D11).
- [ ] Las anulaciones nunca borran: la venta original permanece y el stock se mueve en reversa (`SPRINTS.md` Sprint 5 (Semana 5)).

**Operaciones y pruebas**

- [ ] Los runners `go test ./...` y `vitest` existen en disco, corren y pasan (`SPRINTS.md` §8).
- [ ] Cada comportamiento se entregó junto a su test, escrito antes de la implementación (`SPRINTS.md` §8).
- [ ] No se reclama ningún número de cobertura hasta medirlo con los runners instalados (`SPRINTS.md` §8).
- [ ] La alerta `count(*) FROM sales_default > 0` existe y va al canal de operaciones, no al cajero (`docs/DATABASE.md` §6.7).
- [ ] La pre-creación de particiones corre seis meses hacia adelante y es idempotente (`docs/DATABASE.md` §6.3).
- [ ] PIN, fotos y payloads nunca se escriben en logs (derivado del apunte de amenaza S0-4, `SPRINTS.md` S0-4; el contrato solo fija que los payloads se comparan por digest, `docs/DATABASE.md` §2.10).

---

## 13. Trazabilidad de Fuentes

Mapa de cada sección a los archivos y secciones/rangos de línea en que descansa. Cuando un número de línea cruzado no resolvía al contenido citado, se cita por sección (ver §11.4 #6).

| Sección de este documento | Fuentes |
| :--- | :--- |
| §1 Resumen y filosofía | `ARCHITECTURE.md:3`, `:5`, `:18-27`, `:142`, `:154-178`; `SPRINTS.md` §1, §6, §8; `docs/DATABASE.md` §1, §2.3, §2.11, §7.4, §7.5, §7.9, §8; `docs/PRODUCT.md:19`, `:78`; `docs/BUSINESS.md:3`, `:12` |
| §2 Arquitectura tecnológica | `ARCHITECTURE.md:18-27`, `:31-43`, `:76-91`, `:144-152`; `docs/DATABASE.md` §2.2, §5.2, §5.5, §7.6; `docs/FRONTEND.md:57-73`; `SPRINTS.md` §6 |
| §3 Modelo de dominio | `SPRINTS.md` S0-1, S0-2; `docs/DATABASE.md` §2.1-2.12, §3, §4 (14 tablas), §8; `ARCHITECTURE.md:142-152`; `docs/DESIGN.md:161`, `:217`; `odd/tasks/dual-login-mermas-cleanup.md:30` |
| §4 Superficie HTTP | `ARCHITECTURE.md:118-138`; `docs/DATABASE.md` §2.3, §2.5, §2.8-2.10, §4 (todas las tablas referenciadas), §5.2, §6.1, §7.1-7.9, §8, §9; `docs/FRONTEND.md:45-53`, `:174-180`; `docs/DESIGN.md:161`, `:213`, `:216-217`; `SPRINTS.md` Sprint 2 (Semana 2), Sprint 3 (Semana 3), Sprint 4 (Semana 4), Sprint 5 (Semana 5), Sprint 6 (Semana 6), Sprint 7 (Semana 7), Sprint 9 (Semana 9), §8 |
| §5 Seguridad y multi-tenant | `ARCHITECTURE.md:23`, `:85`, `:126`; `docs/DATABASE.md` §2.9-2.11, §4 (`tenants`, `users`, `devices`, `products`), §5.1-5.7; `SPRINTS.md` S0-4, S1-1 a S1-4, S4-4 |
| §6 Idempotencia y sync | `docs/DATABASE.md` §2.10, §4 (`idempotency_keys`, `sales`, `stock_adjustments`, `fiado_movements`, `cash_close_counts`), §7.1-7.9, §8 (D8, D9, D10, D11), §9; `ARCHITECTURE.md:24`, `:81-89`, `:98-116`; `SPRINTS.md` S3-2, Sprint 6 (Semana 6), §8 |
| §7 Persistencia y operaciones | `docs/DATABASE.md` §1, §4 (`sales`, `idempotency_keys`), §5.2, §5.5, §6.1-6.7, §7.2-7.3; `ARCHITECTURE.md:20-21`, `:85-87`; `SPRINTS.md` S1-4, S2-4, §8; `docs/BUSINESS.md:34`, `:50` |
| §8 Rendimiento y presupuestos | `docs/PRODUCT.md:28`, `:35`, `:78`; `ARCHITECTURE.md:21`, `:84`, `:87`, `:89`, `:98`, `:127`, `:129`; `docs/DATABASE.md` §2.6, §4 (índices de `sales`, `products`, `fiado_*`, `stock_adjustments`, `sale_lines`, `idempotency_keys`), §7.1, §7.3; `SPRINTS.md` Sprint 3 (Semana 3), Sprint 6 (Semana 6), S7-2; `docs/BUSINESS.md:34` |
| §9 Estrategia de pruebas | `SPRINTS.md` §1, §8 |
| §10 Plan por sprints | `SPRINTS.md` §1, §7, §11; `docs/DATABASE.md` §4, §6, §7, §8, §9; `ARCHITECTURE.md:122-138` |
| §11 Riesgos y no-alcance | `ARCHITECTURE.md:27`, `:91`, `:138`, `:154-164`; `docs/DATABASE.md` §4 (notas delta), §7.1-7.9, §8, §9; `SPRINTS.md` §6, S0-2, S0-3, S3-2, Sprint 6 (Semana 6), S6-4, §8, §11; `docs/DESIGN.md:256`; `docs/FRONTEND.md:50-53`; `odd/tasks/stitch-admin-prompts.md:62`, `:121-127`; `odd/tasks/dual-login-mermas-cleanup.md:30`, `:35-39` |
| §12 Checklist de aceptación | `ARCHITECTURE.md:166-178`; `SPRINTS.md` S1-4, S3-2, S4-4, Sprint 5 (Semana 5), S6-3, Sprint 7 (Semana 7), §8; `docs/DATABASE.md` §4, §5.7, §6.3, §6.7, §7.1-7.9, §8, §9 |

> **Nota de verificación.** Toda referencia `archivo:línea` de este documento fue comprobada contra el contenido real del archivo en el estado actual del repositorio. Donde una referencia cruzada de los documentos fuente apuntaba a un número de línea que ya no resolvía al contenido citado (documentado en §11.4 #6), este documento cita por sección en lugar de heredar el número obsoleto. Las referencias a `SPRINTS.md` se citan por **sección, sprint o ID de tarea** (`SPRINTS.md` §8, `SPRINTS.md` Sprint 6 (Semana 6), `SPRINTS.md` S5-3), nunca por número de línea, tras la re-sincronización del 2026-10-14. Las afirmaciones sobre las que no había fuente se marcan `(propuesta)` y se concentran en §2.4-2.6, §4 (formas JSON no fijadas), §7.8-7.9 y §8.
