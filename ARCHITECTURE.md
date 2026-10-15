# POS SaaS architecture (MVP freeze)

The system is a single-cashier, Android-first PWA that records sales and fiados without losing money when the network drops. The server owns balances. The phone is a cache and an event queue — not the book of record.

This document is the architecture contract. Sequencing lives in `SPRINTS.md`. The database schema contract lives in `docs/DATABASE.md` — read this file **as amended** by Section 8 of that document. Product definition lives in `docs/PRODUCT.md`; business and commercial content — pricing, plans, go-to-market, feasibility, portfolio horizon — is owned by `docs/BUSINESS.md`, not by this file. No other document holds architectural authority; earlier drafts that proposed an alternative stack are retired and must not be reintroduced.

## Quick path

1. Cashier opens the installed PWA, unlocks with a local PIN, and sells from the favorites grid.
2. Every closed sale is appended locally, then POSTed to the Go API with an idempotency key.
3. PostgreSQL (RLS) is source of truth for stock, money, and fiado balances. Redis is cache/queue only. Photos live in object storage.

## Decisions

| Topic | Decision |
| --- | --- |
| Client | PWA, React + Vite, Android-first |
| API | Go, one process, REST, contract-first |
| Data | PostgreSQL with `FORCE RLS` + `SET LOCAL` per transaction; money in integer cents (BIGINT) |
| Cache / queue | Redis, keys `tenant:{id}:...` — never a SoT, never a global SKU dump |
| Photos | Object storage; product rows store URLs only |
| Offline | IndexedDB = catalog subset + pending events. Server wins stock and fiado. |
| Identity | JWT carries `tenant_id` + `user_id`; admin tokens issued by `POST /auth/admin-login` additionally carry `role:"admin"` (employee tokens from `/auth/login` and `/auth/device-bind` unchanged — no claim ⇒ employee; delta D3, SDD stitch-admin-prompts). Tenant is never taken from body or header. |
| Sync | Bidirectional: idempotent POST upstream (client UUIDv7 ids + claim protocol), cursor pull downstream. Silent reconciliation — the POS never surfaces sync errors to the cashier. |
| Conflict resolution | LWW for catalog/profile fields only (client `updated_at` comparator). Money and stock converge via additive signed deltas, never LWW. |
| JSONB | Exactly two homes: `products.attributes` (tenant elastic properties) and `global_products.attributes` (platform catalog structure). All core fields are typed columns. |
| Out of this architecture | Tauri, WebUSB, scales, ESC/POS, WhatsApp Business, SUNAT, crowdsourced catalog writes, multi-cashier, split tender |

## Modules

One job per module. No module writes another module's tables.

| Module | Owns | Does not own |
| --- | --- | --- |
| **Identity** | tenants, users, devices, PIN/session, JWT | sales, catalog |
| **Catalog** | tenant products (name, optional GTIN, price cents, UoM, stock projection, photo URL, optional global_product_id), stock_adjustments (append-only delta log), global_products, global_barcodes (platform-curated, read-only for tenants) | blobs, tenant catalog writes to global tables |
| **Input** | `onProductIdentified(code)` adapters: grid (happy path), search, camera (best-effort) | pricing, stock |
| **Cart** | in-progress sale on one device; persisted on every mutation | closed sales |
| **Ledger** | append-only `sales`, `sale_lines`; voids as compensating events; `payment_method` CHECK column (single tender: cash, yape, plin, card, other, fiado) | deleting history, separate tenders table |
| **Fiado** | accounts + movements; balance = SUM(movements) | WhatsApp reminders |
| **Cash close** | `cash_closes` (one per business date per tenant), `cash_close_counts` (expected vs counted by tender); projection of ledger events | a second running total |
| **Sync** | outbound event queue, inbound catalog snapshot + cursor pull, backoff, silent server-side resolutions | business rules |
| **Photos** | object-storage upload/download | product identity |

```
[Favorites grid] [Search] [Camera best-effort]
            \        |         /
             v       v        v
            [ Input adapter ]
                     |
                     v
              [ Cart (IDB) ]
                     |
          close sale / charge to fiado
                     |
                     v
        [ Event queue (IDB) ] ----HTTPS + Idempotency-Key + JWT----\
                     |                                               \
                     v                                                v
        [ Sync engine ]                              [ Go API : tenant from JWT ]
                                                            |
                         +----------------------------------+----------------------------------+
                         |                                  |                                  |
                         v                                  v                                  v
                  [ Identity ]                       [ Ledger ]                          [ Catalog ]
                         |                                  |                                  |
                         |                                  v                                  v
                         |                            [ Fiado ]  [ Cash close ]         [ Photos / URL ]
                         |                                  |                                  |
                         +----------------------------------+----------------------------------+
                                                            |
                                                            v
                                    PostgreSQL (RLS)     Redis (cache/queue)     Object storage
```

## Connections

| From | To | Transport | Contract |
| --- | --- | --- | --- |
| Input → Cart | in-process | `onProductIdentified(code)` | code ≠ product identity; UoM and qty live on the line |
| Cart → Ledger | local event, then HTTP | `POST /sales` | client generates idempotency key once per close; retries reuse it |
| Cart → Fiado | local event, then HTTP | `POST /fiado-movements` | same idempotency rules as sales; atomic account upsert if account doesn't exist |
| Cart → Catalog | local event, then HTTP | `POST /products`, `POST /stock-adjustments` | stock adjustments are signed deltas; LWW for product edits |
| Sync → API | HTTPS | batch of pending events | jitter + exponential backoff; no WebSocket in MVP |
| API → Postgres | one transaction | `SET LOCAL app.current_tenant_id` from JWT | `FORCE RLS`; pooler must not leak GUC across clients |
| API → Redis | key/value, list, or stream | `tenant:{id}:...` | never a SoT; never a global SKU dump |
| Catalog → Photos | HTTPS URL | object storage | no blobs in Postgres, Redis, or IndexedDB |
| API → PWA | HTTPS | catalog snapshot + acks + resolved conflicts | server stock/fiado wins; silent reconciliation for unique violations |
| API → Global catalog | HTTPS (API-mediated) | S2-3 cache-miss lookup | never a global dump to devices; read-only for tenants |

WSS is not part of this architecture. Background Sync API is not relied on (Chrome-only, unstable). The sync engine runs in the client and flushes when connectivity returns.

## Module communication (runtime)

**Happy path (online)**

1. Grid tap → cart line → cashier chooses tender → close.
2. Client writes the event to IndexedDB, then `POST /sales` with `Idempotency-Key`.
3. API claims the key with a unique constraint, appends ledger rows, projects stock/fiado, returns ack.
4. Client marks the event acked and refreshes projected balances from the response.

**Offline path**

1. Same close, but POST fails or is skipped.
2. Sale stays in the queue; cashier keeps selling.
3. On reconnect, Sync POSTs in batches. Same key + same body → replayed response. Same key + different body → `422`.
4. In-flight duplicate → `409`. Client waits and retries; it does not open a second sale.

**Conflict path (silent reconciliation)**

Stock and fiado balances are not merged with last-write-wins. The server value is canonical. Unique violations are resolved server-side silently — the POS never surfaces sync errors to the cashier:
- `(tenant_id, name)` collision → deterministic rename suffix on the inbound row.
- `(tenant_id, gtin)` collision → `gtin` set to NULL on the inbound row.
- Duplicate-date cash close → existing close stands; duplicate acked as no-op.

LWW applies only to mutable catalog/profile fields (product name, price, fiado account phone). Money and stock converge exclusively via append-only events and signed deltas.

## HTTP surface (MVP)

Validate at the API boundary. One error shape: `{ error: { code, message, details? } }`. State-changing POSTs honour `Idempotency-Key`.

| Method | Path | Idempotent | Notes |
| --- | --- | --- | --- |
| POST | `/auth/login` | no | issues JWT; tenant only inside the token |
| POST | `/auth/device-bind` | yes | binds this phone to the cashier |
| POST | `/auth/admin-login` | no | admin login (delta D2, SDD stitch-admin-prompts): req `{ phone, master_password }`; resolves phone → owning tenant → verify `tenants.master_password_hash` (argon2id); 200 `{ token, tenant: { name } }` with JWT `role:"admin"`; generic 401, no user enumeration (IDA-2.2); tenant only inside the token |
| GET | `/products` | yes | tenant catalog page; no global dump |
| POST | `/products` | yes | create tenant product |
| PATCH | `/products/{id}` | yes | additive fields only; LWW-resolved |
| GET | `/sync/cursor` | yes | watermark for catalog pull; returns rows with `updated_at > cursor` |
| POST | `/sales` | **required** | append-only close; `payment_method` CHECK (cash, yape, plin, card, other, fiado) |
| POST | `/sales/{id}/void` | **required** | compensating event; original sale retained |
| POST | `/fiado-movements` | **required** | charge or payment; atomic account upsert if needed |
| GET | `/fiado-accounts` | yes | balance is computed (SUM), not stored as a writable field |
| POST | `/cash-closes` | **required** | projection vs counted tenders; one per business date per tenant |
| POST | `/stock-adjustments` | **required** | manual stock correction; signed delta |

**Delta note — SDD stitch-admin-prompts (D12 DEFERRED).** El dashboard Admin (P4) renderiza SOLO el ledger local (IndexedDB, proyección F8) + `GET /fiado-accounts` (ADD-4): no se agrega ningún endpoint de reporte en esta versión. Delta futuro registrado: `GET /sales` o `GET /dashboard/summary` + tabla de asistencia (post-MVP, SPRINTS.md:39/:214). Sin endpoint agregado en este cambio.

## Data ownership

The database schema comprises 14 tables across 7 module groups. Full column, constraint, and index definitions live in `docs/DATABASE.md`.

| Table | Owner module | Notes |
| --- | --- | --- |
| `tenants`, `users`, `devices` | Identity | registry root; no RLS on tenants |
| `products`, `stock_adjustments` | Catalog | price/stock columns, not JSONB; stock = SUM(deltas) |
| `sales`, `sale_lines` | Ledger | PK `(sold_on, sale_id)` — composite, partition-compatible |
| `fiado_accounts`, `fiado_movements` | Fiado | balance = SUM(movements); sign IS the kind |
| `cash_closes`, `cash_close_counts` | Cash close | UNIQUE (tenant_id, closed_on); one close per date |
| `idempotency_keys` | Sync/API | claim protocol; 400-day retention; NOT append-only |
| `global_products`, `global_barcodes` | Catalog (platform) | outside tenant RLS; API-mediated read-only |

## Non-goals (do not draw these boxes)

- Crowdsourced global catalog as a write path (platform-curated only in MVP)
- Millions of SKUs in IndexedDB or Redis
- WhatsApp API / `wa.me` automation
- Automatic Yape/Plin reconciliation
- SUNAT / electronic invoicing (reserve receipt type/series fields only)
- Hardware I/O (HID, ESC/POS, scales, Tauri)
- Multi-cashier / shifts
- Split tender (single tender per sale)
- Billing / plans / subscriptions tables

## Checklist

- [ ] A closed sale can be recovered after killing the tab (event in IDB) and after losing the phone (event on server).
- [ ] Replaying `POST /sales` with the same key does not duplicate money or stock.
- [ ] A query without the JWT tenant cannot read another tenant's rows.
- [ ] IndexedDB never holds the global catalog.
- [ ] Fiado balance is not a writable column.
- [ ] `FORCE RLS` + `SET LOCAL app.current_tenant_id` per transaction; pooler-safe.
- [ ] Money is BIGINT integer cents everywhere; no NUMERIC or float money.
- [ ] JSONB appears in exactly two columns: `products.attributes` and `global_products.attributes`.
- [ ] Stock converges via additive signed deltas (`stock_adjustments`), never LWW.
- [ ] `sales` is RANGE-partitioned by month on `sold_on`; PK is `(sold_on, sale_id)`.
- [ ] Silent reconciliation: unique violations resolved server-side; POS never shows sync errors.

## Next step

Implement in sprint order (`SPRINTS.md`). Do not start code until Sprint 0 glossary + domain fields match this document and `docs/DATABASE.md`.
