-- +goose Up
-- Module: Platform-curated global — docs/DATABASE.md §4, module group 7.
-- Tables: global_products, global_barcodes.
--
-- File-order note: §4 lists this module last, after Sync/API. It is created here,
-- second, because `products.global_product_id` (§4 `products`) references
-- `global_products.id` and that reference is declared inline on `products`. Creation
-- order is a migration concern only — the module grouping of §4 is unchanged, and no
-- column, constraint or index differs from the contract.
--
-- Both tables sit outside tenant RLS: no tenant_id, no policy (§3, §5.6, OI-13).
-- Tenant access is API-mediated read-only through the pos_platform role.

-- `global_products` — platform-managed canonical catalog.
-- `attributes` is JSONB home #2 of exactly two (§2.9, decision 2a).
CREATE TABLE global_products (
    id         UUID        NOT NULL,
    name       TEXT        NOT NULL,
    attributes JSONB       NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT global_products_pkey PRIMARY KEY (id)
);

-- `global_barcodes` — platform-managed GTIN -> global_products mapping.
-- TEXT preserves leading zeros across GTIN-8/12/13/14.
CREATE TABLE global_barcodes (
    id                UUID        NOT NULL,
    gtin              TEXT        NOT NULL,
    global_product_id UUID        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL,
    CONSTRAINT global_barcodes_pkey PRIMARY KEY (id),
    CONSTRAINT global_barcodes_gtin_key UNIQUE (gtin),
    CONSTRAINT global_barcodes_global_product_id_fkey FOREIGN KEY (global_product_id) REFERENCES global_products (id) ON DELETE RESTRICT
);

-- FK coverage (§2.7, F2) and the reverse lookup from a global product to its barcodes.
-- The UNIQUE (gtin) backing index satisfies the gtin lookup and is never duplicated.
CREATE INDEX global_barcodes_global_product_id_idx ON global_barcodes (global_product_id);

-- +goose Down
DROP TABLE IF EXISTS global_barcodes;
DROP TABLE IF EXISTS global_products;
