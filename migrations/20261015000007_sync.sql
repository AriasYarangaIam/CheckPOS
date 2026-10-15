-- +goose Up
-- Module: Sync/API — docs/DATABASE.md §4, module group 6.
-- Table: idempotency_keys.
--
-- This table merges what earlier drafts called `sync_events`; there is no
-- `sync_events` table (OI-3, amendment 5). It is deliberately NOT append-only:
-- the claim lifecycle needs UPDATE (in_flight -> completed) and retention needs
-- DELETE through the pos_maintenance role (§2.12 exception, MD-3).

-- `idempotency_keys` — the row IS the idempotency claim for every queueable POST.
CREATE TABLE idempotency_keys (
    id                UUID        NOT NULL,
    tenant_id         UUID        NOT NULL,
    key               TEXT        NOT NULL,
    payload_hash      TEXT        NOT NULL,
    state             TEXT        NOT NULL DEFAULT 'in_flight',
    response_snapshot TEXT        NULL,
    created_at        TIMESTAMPTZ NOT NULL,
    CONSTRAINT idempotency_keys_pkey PRIMARY KEY (id),
    -- The client's Idempotency-Key header value; the ON CONFLICT target for the
    -- claim protocol, and the 2.6 tenant-leading index (never duplicated).
    CONSTRAINT idempotency_keys_tenant_id_key_key UNIQUE (tenant_id, key),
    CONSTRAINT idempotency_keys_state_chk CHECK (state IN ('in_flight', 'completed')),
    -- State-coupled snapshot CHECK (MD-4, F4): a committed row is always completed
    -- with its snapshot attached, so an in_flight row can never be served as a
    -- phantom success. response_snapshot is TEXT, never JSONB (§2.9).
    CONSTRAINT idempotency_keys_response_snapshot_chk CHECK ((state = 'completed' AND response_snapshot IS NOT NULL) OR (state = 'in_flight' AND response_snapshot IS NULL)),
    CONSTRAINT idempotency_keys_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE RESTRICT
);

-- No further indexes: §4 pins none (retention cleanup scans by age at pilot scale).

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
