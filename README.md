# POS SaaS (CheckPOS)

Point-of-sale SaaS for bodegas and minimarkets. The design documents live in `docs/`;
`docs/DATABASE.md` is the authoritative PostgreSQL schema contract.

This repository currently contains the **Sprint 1 database layer**: a local PostgreSQL
container, the goose migration runner, and the initial migration set that creates the
14 tables of the schema contract.

## Sprint 1 — start the database and apply the migrations

### 1. Start PostgreSQL

```powershell
docker compose up -d
docker compose ps
```

The container is ready when `docker compose ps` reports `(healthy)`.

The published host port is **5433**, not 5432: another service on the development
machine already listens on 5432, and 5433 was verified free. If 5433 is taken on your
machine, change the published port in `docker-compose.yml` and update `DATABASE_URL`.

### 2. Apply the migrations

```powershell
go run ./cmd/migrate up        # apply every pending migration
go run ./cmd/migrate status    # list applied and pending migrations
go run ./cmd/migrate ping      # connectivity smoke test: server version + table count
go run ./cmd/migrate down      # reverse the whole schema (down to version 0)
```

`make` is not required, and is not assumed to exist on Windows. The migration runner is
a plain Go program; the SQL files are compiled into it with `embed.FS`, so no external
goose binary is needed either.

The connection string is read from `DATABASE_URL`. Copy `.env.example` to `.env` to
override the default:

```
postgres://pos:pos_local_dev@localhost:5433/pos?sslmode=disable
```

Those credentials are throwaway local-development values, not secrets. `.env` is
git-ignored.

### 3. Migration layout

Migrations are goose SQL files in `migrations/`, applied in filename order, one module
per file so a reviewer can read one module group at a time:

| File | Module group (`docs/DATABASE.md` §4) | Tables |
|------|--------------------------------------|--------|
| `20261015000001_identity.sql` | Identity | `tenants`, `users`, `devices` |
| `20261015000002_global_catalog.sql` | Platform-curated global | `global_products`, `global_barcodes` |
| `20261015000003_catalog.sql` | Catalog (tenant) | `products`, `stock_adjustments` |
| `20261015000004_ledger.sql` | Ledger | `sales` (partitioned), `sale_lines` |
| `20261015000005_fiado.sql` | Fiado | `fiado_accounts`, `fiado_movements` |
| `20261015000006_cash_close.sql` | Cash close | `cash_closes`, `cash_close_counts` |
| `20261015000007_sync.sql` | Sync/API | `idempotency_keys` |

Two ordering notes, both about migration mechanics rather than the contract:

- the global catalog file is applied second because `products.global_product_id`
  references `global_products.id`;
- `stock_adjustments`' composite foreign key to `sales` is added at the end of the
  ledger file, because `sales` does not exist when the catalog file runs.

`sales` is RANGE-partitioned by month on `sold_on`. The migration pre-creates the
current month plus the next six as `sales_YYYY_MM`, plus a `sales_default` DEFAULT
partition as a safety net. Rolling pre-creation is a scheduled maintenance concern
(`docs/DATABASE.md` §6.3); extending the window today means adding a migration with
more `CREATE TABLE ... PARTITION OF` statements.

Row-level security is **not** part of Sprint 1 — the migration creates tables only.
FORCE RLS, the tenant-context GUC and the grant matrix are Sprint 2 work
(`docs/DATABASE.md` §5).

### 4. Useful SQL without a local client

There is no `psql` client installed locally; run SQL through the container:

```powershell
docker compose exec postgres psql -U pos -d pos -c "\dt"
```
