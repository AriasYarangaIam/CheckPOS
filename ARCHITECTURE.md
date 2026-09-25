# POS SaaS architecture (MVP freeze)

The system is a single-cashier, Android-first PWA that records sales and fiados without losing money when the network drops. The server owns balances. The phone is a cache and an event queue — not the book of record.

This document is the architecture contract. It supersedes the original pitch in `sistema_pos_saas_bodegas_minimarkets.md` wherever they disagree. Sequencing lives in `SPRINTS.md`.

## Quick path

1. Cashier opens the installed PWA, unlocks with a local PIN, and sells from the favorites grid.
2. Every closed sale is appended locally, then POSTed to the Go API with an idempotency key.
3. PostgreSQL (RLS) is source of truth for stock, money, and fiado balances. Redis is cache/queue only. Photos live in object storage.

## Decisions

| Topic | Decision |
| --- | --- |
| Client | PWA, React + Vite, Android-first |
| API | Go, one process, REST, contract-first |
| Data | PostgreSQL with `FORCE RLS` + `SET LOCAL` per transaction; money in integer cents |
| Cache / queue | Redis, keys `tenant:{id}:...` |
| Photos | Object storage; product rows store URLs only |
| Offline | IndexedDB = catalog subset + pending events. Server wins stock and fiado. |
| Identity | JWT carries `tenant_id` + `user_id`. Tenant is never taken from body or header. |
| Out of this architecture | Tauri, WebUSB, scales, ESC/POS, WhatsApp Business, SUNAT, crowdsourced catalog writes, multi-cashier |

## Modules

One job per module. No module writes another module’s tables.

| Module | Owns | Does not own |
| --- | --- | --- |
| **Identity** | tenants, users, devices, PIN/session, JWT | sales, catalog |
| **Catalog** | tenant products (name, optional GTIN, price cents, UoM, stock snapshot, photo URL) | global SKU universe, blobs |
| **Input** | `onProductIdentified(code)` adapters: grid (happy path), search, camera (best-effort) | pricing, stock |
| **Cart** | in-progress sale on one device; persisted on every mutation | closed sales |
| **Ledger** | append-only `sales`, `sale_lines`, `tenders`; voids/returns as compensating events | deleting history |
| **Fiado** | accounts + movements; balance = SUM(movements) | WhatsApp reminders |
| **Cash close** | projection of ledger events vs counted cash | a second running total |
| **Sync** | outbound event queue, inbound catalog snapshot, backoff, conflict UI | business rules |
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
| Cart → Fiado | local event, then HTTP | `POST /fiado-movements` | same idempotency rules as sales |
| Sync → API | HTTPS | batch of pending events | jitter + exponential backoff; no WebSocket in MVP |
| API → Postgres | one transaction | `SET LOCAL app.current_tenant_id` from JWT | `FORCE RLS`; pooler must not leak GUC across clients |
| API → Redis | key/value, list, or stream | `tenant:{id}:...` | never a SoT; never a global SKU dump |
| Catalog → Photos | HTTPS URL | object storage | no blobs in Postgres, Redis, or IndexedDB |
| API → PWA | HTTPS | catalog snapshot + acks + conflict payload | server stock/fiado wins; UI shows “server X vs device Y” |

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

**Conflict path**

Stock and fiado balances are not merged with last-write-wins. The server value is canonical. The device shows the mismatch and does not silently overwrite money.

## HTTP surface (MVP)

Validate at the API boundary. One error shape: `{ error: { code, message, details? } }`. State-changing POSTs honour `Idempotency-Key`.

| Method | Path | Idempotent | Notes |
| --- | --- | --- | --- |
| POST | `/auth/login` | no | issues JWT; tenant only inside the token |
| POST | `/auth/device-bind` | yes | binds this phone to the cashier |
| GET | `/products` | yes | tenant catalog page; no global dump |
| POST | `/products` | yes | create tenant product |
| PATCH | `/products/{id}` | no | additive fields only |
| POST | `/sales` | **required** | append-only close |
| POST | `/sales/{id}/void` | **required** | compensating event |
| POST | `/fiado-movements` | **required** | charge or payment |
| GET | `/fiado-accounts` | yes | balance is computed, not stored as a writable field |
| POST | `/cash-closes` | **required** | projection vs counted tenders |
| GET | `/sync/cursor` | yes | watermark for catalog pull |

## Data ownership

| Table | Owner module | Notes |
| --- | --- | --- |
| `tenants`, `users`, `devices` | Identity | |
| `products` | Catalog | price/stock columns, not JSONB |
| `sales`, `sale_lines`, `tenders` | Ledger | PK compatible with date partitioning later: `(sold_on, sale_id)` |
| `fiado_accounts`, `fiado_movements` | Fiado | balance = SUM(movements) |
| `sync_events` | Sync | pending/acked; payload hash for idempotency |
| `idempotency_keys` | API | unique `(tenant_id, key)`; retain longer than the retry/DLQ window |

## Non-goals (do not draw these boxes)

- Crowdsourced global catalog as a write path
- Millions of SKUs in IndexedDB or Redis
- WhatsApp API / `wa.me` automation
- Automatic Yape/Plin reconciliation
- SUNAT / electronic invoicing (reserve receipt type/series fields only)
- Hardware I/O (HID, ESC/POS, scales, Tauri)

## Checklist

- [ ] A closed sale can be recovered after killing the tab (event in IDB) and after losing the phone (event on server).
- [ ] Replaying `POST /sales` with the same key does not duplicate money or stock.
- [ ] A query without the JWT tenant cannot read another tenant’s rows.
- [ ] IndexedDB never holds the global catalog.
- [ ] Fiado balance is not a writable column.

## Next step

Implement in sprint order (`SPRINTS.md`). Do not start code until Sprint 0 glossary + domain fields match this document.
