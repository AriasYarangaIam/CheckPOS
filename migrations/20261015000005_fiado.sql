-- +goose Up
-- Module: Fiado — docs/DATABASE.md §4, module group 4.
-- Tables: fiado_accounts, fiado_movements.

-- `fiado_accounts` — a customer who buys on credit.
-- No writable balance column: balance = SUM(fiado_movements.amount_cents) is a
-- computed invariant, never stored.
CREATE TABLE fiado_accounts (
    id          UUID        NOT NULL,
    tenant_id   UUID        NOT NULL,
    name        TEXT        NOT NULL,
    phone       TEXT        NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT fiado_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT fiado_accounts_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT
);

-- Mutable synced table: the cursor pull watermark is updated_at (§7.3).
-- NO partial predicate: tombstones and edits must both be pullable.
CREATE INDEX fiado_accounts_tenant_id_updated_at_idx ON fiado_accounts (tenant_id, updated_at);

-- `fiado_movements` — append-only signed ledger of credit entries.
-- The sign of amount_cents IS the kind (charge positive, payment negative); there is
-- deliberately no `kind` column. A voided fiado sale writes a sign-reversed
-- compensating row, never a DELETE or an edit.
CREATE TABLE fiado_movements (
    id               UUID        NOT NULL,
    tenant_id        UUID        NOT NULL,
    fiado_account_id UUID        NOT NULL,
    amount_cents     BIGINT      NOT NULL,
    sold_on          DATE        NULL,
    sale_id          UUID        NULL,
    note             TEXT        NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL,
    CONSTRAINT fiado_movements_pkey PRIMARY KEY (id),
    -- Both sale-linkage columns present, or both absent (MD-9, F19).
    CONSTRAINT fiado_movements_sold_on_sale_id_pair_nullity_chk CHECK ((sold_on IS NULL) = (sale_id IS NULL)),
    CONSTRAINT fiado_movements_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT,
    CONSTRAINT fiado_movements_fiado_account_id_fkey FOREIGN KEY (fiado_account_id) REFERENCES fiado_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fiado_movements_sold_on_sale_id_fkey FOREIGN KEY (sold_on, sale_id) REFERENCES sales (sold_on, sale_id) ON DELETE RESTRICT
);

-- 2.6 tenant-leading rule; serves per-account movement listing and covers the FK column.
CREATE INDEX fiado_movements_tenant_id_fiado_account_id_idx ON fiado_movements (tenant_id, fiado_account_id);
-- Composite-FK companion index; documented deviation from the tenant-leading rule (§2.7).
CREATE INDEX fiado_movements_sold_on_sale_id_idx ON fiado_movements (sold_on, sale_id);

-- +goose Down
DROP TABLE IF EXISTS fiado_movements;
DROP TABLE IF EXISTS fiado_accounts;
