-- +goose Up
-- Module: Ledger — docs/DATABASE.md §4, module group 3.
-- Tables: sales (RANGE-partitioned by month on sold_on, §6), sale_lines.
--
-- `sales` is the append-only money ledger. It is physically partitioned from day one
-- (decision 3), one partition per calendar month, named sales_YYYY_MM (§6.2).

-- `sales` — one row per sale or void.
CREATE TABLE sales (
    sold_on          DATE        NOT NULL,
    sale_id          UUID        NOT NULL,
    tenant_id        UUID        NOT NULL,
    sold_by_user_id  UUID        NOT NULL,
    kind             TEXT        NOT NULL,
    payment_method   TEXT        NOT NULL,
    total_cents      BIGINT      NOT NULL,
    original_sold_on DATE        NULL,
    original_sale_id UUID        NULL,
    receipt_type     TEXT        NULL,
    receipt_series   TEXT        NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL,
    -- The partition key (sold_on) MUST be part of the primary key; PostgreSQL rejects
    -- any unique constraint on a partitioned table that omits it (§6.1). There is
    -- therefore NO schema-level UNIQUE(sale_id) — cross-partition uniqueness of
    -- sale_id rests on the application-side UUIDv7 plus the idempotency claim (§6.1).
    CONSTRAINT sales_pkey PRIMARY KEY (sold_on, sale_id),
    CONSTRAINT sales_kind_chk CHECK (kind IN ('sale', 'void')),
    -- Six values; the sixth ('fiado') is the user-approved amendment of the spec's
    -- five-value pin (D11). `tenders` is NOT a table — it is this column (D9).
    CONSTRAINT sales_payment_method_chk CHECK (payment_method IN ('cash', 'yape', 'plin', 'card', 'other', 'fiado')),
    -- Pair-nullity for the composite self-FK (MD-9, F10).
    CONSTRAINT sales_void_self_fk_pair_nullity_chk CHECK ((original_sold_on IS NULL) = (original_sale_id IS NULL)),
    -- A void row MUST reference an original; a sale row MUST NOT (F10).
    CONSTRAINT sales_void_kind_linkage_chk CHECK ((kind = 'void') = (original_sale_id IS NOT NULL)),
    CONSTRAINT sales_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT,
    CONSTRAINT sales_sold_by_user_id_fkey FOREIGN KEY (sold_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    -- Void self-reference; both columns are NULL when kind = 'sale'.
    CONSTRAINT sales_original_sold_on_original_sale_id_fkey FOREIGN KEY (original_sold_on, original_sale_id) REFERENCES sales (sold_on, sale_id) ON DELETE RESTRICT
) PARTITION BY RANGE (sold_on);

-- Indexes are declared on the partitioned parent; PostgreSQL creates the matching
-- index on every partition, including future ones created by the maintenance job.
CREATE INDEX sales_tenant_id_sold_on_idx ON sales (tenant_id, sold_on);
-- Cash-close projection: the per-method SUM that feeds cash_close_counts.expected_cents (F8).
CREATE INDEX sales_tenant_id_sold_on_payment_method_idx ON sales (tenant_id, sold_on, payment_method);
CREATE INDEX sales_tenant_id_sold_by_user_id_idx ON sales (tenant_id, sold_by_user_id);
CREATE INDEX sales_tenant_id_original_sold_on_original_sale_id_idx ON sales (tenant_id, original_sold_on, original_sale_id);

-- Monthly partitions: the current month plus the next six (§6.3 — the rolling
-- pre-creation window is six calendar months ahead). Pre-creation is ordinarily a
-- scheduled, idempotent maintenance job; this static window is what Sprint 1 ships,
-- and extending it later is a new migration with more CREATE TABLE ... PARTITION OF
-- statements.
CREATE TABLE sales_2026_10 PARTITION OF sales FOR VALUES FROM ('2026-10-01') TO ('2026-11-01');
CREATE TABLE sales_2026_11 PARTITION OF sales FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
CREATE TABLE sales_2026_12 PARTITION OF sales FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');
CREATE TABLE sales_2027_01 PARTITION OF sales FOR VALUES FROM ('2027-01-01') TO ('2027-02-01');
CREATE TABLE sales_2027_02 PARTITION OF sales FOR VALUES FROM ('2027-02-01') TO ('2027-03-01');
CREATE TABLE sales_2027_03 PARTITION OF sales FOR VALUES FROM ('2027-03-01') TO ('2027-04-01');
CREATE TABLE sales_2027_04 PARTITION OF sales FOR VALUES FROM ('2027-04-01') TO ('2027-05-01');

-- Safety net, not a destination: rows landing here signal clock skew, a missed
-- maintenance run, or an out-of-range migration (§6.4). Remediation is the
-- detach/re-attach pattern (§6.5).
CREATE TABLE sales_default PARTITION OF sales DEFAULT;

-- `sale_lines` — one line per product in a sale; a frozen snapshot of the product at
-- close time. Child of `sales`: shares the parent's transaction and carries no
-- received_at of its own (MD-6). No surrogate id — the composite PK is pinned.
CREATE TABLE sale_lines (
    sale_id          UUID          NOT NULL,
    line_no          INT           NOT NULL,
    tenant_id        UUID          NOT NULL,
    sold_on          DATE          NOT NULL,
    product_id       UUID          NOT NULL,
    name             TEXT          NOT NULL,
    price_cents      BIGINT        NOT NULL,
    uom              TEXT          NOT NULL,
    qty              NUMERIC(10,3) NOT NULL,
    line_total_cents BIGINT        NOT NULL,
    created_at       TIMESTAMPTZ   NOT NULL,
    CONSTRAINT sale_lines_pkey PRIMARY KEY (sale_id, line_no),
    CONSTRAINT sale_lines_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT,
    -- The structurally forced FK to the partitioned parent, naming both PK columns
    -- (MD-9): PostgreSQL requires every FK target on a partitioned table to include
    -- the partition key, so sold_on is carried on the child.
    CONSTRAINT sale_lines_sold_on_sale_id_fkey FOREIGN KEY (sold_on, sale_id) REFERENCES sales (sold_on, sale_id) ON DELETE RESTRICT,
    -- products are soft-deleted, so this FK never blocks sale history (OI-11).
    CONSTRAINT sale_lines_product_id_fkey FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE RESTRICT
);

-- 2.6 tenant-leading rule; serves per-product sales aggregation and covers product_id.
CREATE INDEX sale_lines_tenant_id_product_id_idx ON sale_lines (tenant_id, product_id);
-- Composite-FK companion index; documented deviation from the tenant-leading rule (§2.7).
CREATE INDEX sale_lines_sold_on_sale_id_idx ON sale_lines (sold_on, sale_id);

-- Cross-module composite FK: `stock_adjustments` (Catalog, created in the previous
-- migration) references the partitioned `sales` parent, which only exists from here on.
ALTER TABLE stock_adjustments
    ADD CONSTRAINT stock_adjustments_sold_on_sale_id_fkey FOREIGN KEY (sold_on, sale_id) REFERENCES sales (sold_on, sale_id) ON DELETE RESTRICT;

-- +goose Down
-- The composite FK into the partitioned parent blocks DROP TABLE sales, so it goes first.
ALTER TABLE stock_adjustments DROP CONSTRAINT IF EXISTS stock_adjustments_sold_on_sale_id_fkey;
DROP TABLE IF EXISTS sale_lines;
-- Drops every monthly partition, sales_default included.
DROP TABLE IF EXISTS sales;
