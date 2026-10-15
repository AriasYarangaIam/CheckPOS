-- +goose Up
-- Module: Catalog (tenant-scoped) — docs/DATABASE.md §4, module group 2.
-- Tables: products, stock_adjustments.
--
-- Both are FORCE-RLS tables in Sprint 2 (§5.1); Sprint 1 only creates them.
-- `stock_adjustments`' composite FK to the partitioned `sales` parent is added in
-- 20261015000004_ledger.sql, because `sales` does not exist yet at this point in the
-- migration order. The separation is file order, not a contract deviation.

-- `products` — a sellable item in one tenant's catalog. Client-generated UUIDv7
-- (device-originated), mutable-LWW, soft-deleted by tombstone.
CREATE TABLE products (
    id                UUID          NOT NULL,
    tenant_id         UUID          NOT NULL,
    name              TEXT          NOT NULL,
    gtin              TEXT          NULL,
    price_cents       BIGINT        NOT NULL,
    uom               TEXT          NOT NULL,
    stock             NUMERIC(10,3) NOT NULL DEFAULT 0,
    photo_url         TEXT          NULL,
    attributes        JSONB         NULL,
    global_product_id UUID          NULL,
    created_at        TIMESTAMPTZ   NOT NULL,
    updated_at        TIMESTAMPTZ   NOT NULL,
    received_at       TIMESTAMPTZ   NOT NULL,
    deleted_at        TIMESTAMPTZ   NULL,
    CONSTRAINT products_pkey PRIMARY KEY (id),
    CONSTRAINT products_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT,
    -- Adoption pointer: lineage only, never a live dependency (F13).
    CONSTRAINT products_global_product_id_fkey FOREIGN KEY (global_product_id) REFERENCES global_products (id) ON DELETE RESTRICT
);

-- Tenant-scoped uniqueness, reclaimable after a tombstone. Partial unique constraints
-- cannot be declared as CONSTRAINTs in PostgreSQL, so these are partial unique indexes
-- under exactly the names §4 pins for them; they are also the backing indexes §4 refers
-- to for both keys.
CREATE UNIQUE INDEX products_tenant_gtin_key ON products (tenant_id, gtin) WHERE deleted_at IS NULL AND gtin IS NOT NULL;
CREATE UNIQUE INDEX products_tenant_name_key ON products (tenant_id, name) WHERE deleted_at IS NULL;

-- 2.6 tenant-leading rule.
CREATE INDEX products_tenant_id_idx ON products (tenant_id);
-- NO partial predicate: tombstoned rows MUST be pullable by the sync cursor (§7.3).
CREATE INDEX products_tenant_updated_at_idx ON products (tenant_id, updated_at);
CREATE INDEX products_tenant_global_product_id_idx ON products (tenant_id, global_product_id);

-- `stock_adjustments` — append-only signed-delta log driving the products.stock
-- projection. Invariant: products.stock = SUM(stock_adjustments.qty_delta).
-- No CHECK >= 0 on products.stock: offline oversell is accepted (OI-10).
CREATE TABLE stock_adjustments (
    id          UUID          NOT NULL,
    tenant_id   UUID          NOT NULL,
    product_id  UUID          NOT NULL,
    qty_delta   NUMERIC(10,3) NOT NULL,
    source      TEXT          NOT NULL,
    reason      TEXT          NULL,
    sold_on     DATE          NULL,
    sale_id     UUID          NULL,
    created_at  TIMESTAMPTZ   NOT NULL,
    received_at TIMESTAMPTZ   NOT NULL,
    CONSTRAINT stock_adjustments_pkey PRIMARY KEY (id),
    CONSTRAINT stock_adjustments_source_chk CHECK (source IN ('sale', 'void', 'manual')),
    -- Pair-nullity for the nullable composite sale linkage (MD-9, F19).
    CONSTRAINT stock_adjustments_sale_link_chk CHECK ((sold_on IS NULL) = (sale_id IS NULL)),
    CONSTRAINT stock_adjustments_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT,
    CONSTRAINT stock_adjustments_product_id_fkey FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE RESTRICT
    -- (sold_on, sale_id) -> sales(sold_on, sale_id): added in the ledger migration.
);

-- 2.6 tenant-leading rule; serves per-product delta aggregation and covers product_id.
CREATE INDEX stock_adjustments_tenant_id_product_id_idx ON stock_adjustments (tenant_id, product_id);
-- Composite-FK companion index; documented deviation from the tenant-leading rule (§2.7).
CREATE INDEX stock_adjustments_sold_on_sale_id_idx ON stock_adjustments (sold_on, sale_id);

-- +goose Down
DROP TABLE IF EXISTS stock_adjustments;
DROP TABLE IF EXISTS products;
