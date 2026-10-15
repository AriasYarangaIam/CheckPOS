-- +goose Up
-- Module: Cash close — docs/DATABASE.md §4, module group 5.
-- Tables: cash_closes (header), cash_close_counts (per-method breakdown).

-- `cash_closes` — one close per business date per tenant (D10).
-- Insert-once: there is no amendment path in MVP, and no opening-float or expense
-- columns (those are deferred, not part of this version).
CREATE TABLE cash_closes (
    id                UUID        NOT NULL,
    tenant_id         UUID        NOT NULL,
    closed_on         DATE        NOT NULL,
    closed_by_user_id UUID        NOT NULL,
    notes             TEXT        NULL,
    created_at        TIMESTAMPTZ NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT cash_closes_pkey PRIMARY KEY (id),
    -- D10: ONE close per business date per tenant. Its backing index also realizes
    -- cash_closes_tenant_id_closed_on_idx, which is never duplicated as a separate index.
    CONSTRAINT cash_closes_tenant_closed_on_key UNIQUE (tenant_id, closed_on),
    CONSTRAINT cash_closes_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT,
    CONSTRAINT cash_closes_closed_by_user_id_fkey FOREIGN KEY (closed_by_user_id) REFERENCES users (id) ON DELETE RESTRICT
);

-- 2.6 tenant-leading rule; covers the closed_by_user_id FK column.
CREATE INDEX cash_closes_tenant_id_closed_by_user_id_idx ON cash_closes (tenant_id, closed_by_user_id);

-- `cash_close_counts` — expected vs counted per payment method per close.
-- Child of `cash_closes`; shares the parent's transaction, so no received_at (MD-6).
-- The composite natural key is the pinned shape (F1); no surrogate id exists.
CREATE TABLE cash_close_counts (
    cash_close_id  UUID        NOT NULL,
    payment_method TEXT        NOT NULL,
    tenant_id      UUID        NOT NULL,
    expected_cents BIGINT      NOT NULL,
    counted_cents  BIGINT      NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT cash_close_counts_pkey PRIMARY KEY (cash_close_id, payment_method),
    -- Pinned claim shape (OI-5). It duplicates the primary key on the same columns;
    -- both are stated in §4, so both are declared literally. See the migration report
    -- for this observation.
    CONSTRAINT cash_close_counts_cash_close_id_payment_method_key UNIQUE (cash_close_id, payment_method),
    -- Same six values as sales (D11).
    CONSTRAINT cash_close_counts_payment_method_chk CHECK (payment_method IN ('cash', 'yape', 'plin', 'card', 'other', 'fiado')),
    -- Makes D11's counted-fiado = 0 rule structural rather than conventional (F32):
    -- expected fiado is the credit extended that day; counted fiado is always zero.
    CONSTRAINT cash_close_counts_fiado_counted_chk CHECK (payment_method <> 'fiado' OR counted_cents = 0),
    CONSTRAINT cash_close_counts_cash_close_id_fkey FOREIGN KEY (cash_close_id) REFERENCES cash_closes (id) ON DELETE RESTRICT,
    CONSTRAINT cash_close_counts_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT
);

-- 2.6 tenant-leading rule; covers the cash_close_id FK column.
CREATE INDEX cash_close_counts_tenant_id_cash_close_id_idx ON cash_close_counts (tenant_id, cash_close_id);

-- +goose Down
DROP TABLE IF EXISTS cash_close_counts;
DROP TABLE IF EXISTS cash_closes;
