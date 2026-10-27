-- +goose Up
-- Module: Sprint 6, W6-DB-1 — the RLS maintenance path for the 400-day retention.
--
-- docs/DATABASE.md §5.5 grants `pos_maintenance` DELETE on `idempotency_keys` "via the RLS
-- maintenance path", but the grant alone does not make the job runnable: measured against
-- this container,
--
--   DELETE FROM idempotency_keys WHERE state = 'completed' AND created_at < ...
--
-- fails for `pos_maintenance` with 42501 (the WHERE reads columns, so PostgreSQL also asks
-- for SELECT on them), and even with the privilege it would delete zero rows because the
-- tenant DELETE policy of migration 20261016000002 filters every row out: the maintenance
-- role has no tenant context, and FORCE ROW LEVEL SECURITY binds it like any other role.
--
-- This migration supplies exactly the two pieces the contract describes, and nothing more:
--
--   1. the column-level SELECT the WHERE needs (no table-wide read: the job has no business
--      reading key payloads or snapshots);
--   2. a permissive SELECT + DELETE pair scoped TO pos_maintenance whose USING clause is the
--      retention rule itself. Both are needed, and this is measured behaviour: under
--      FORCE ROW LEVEL SECURITY a DELETE can only remove rows that the role can also see
--      through the SELECT policies. PostgreSQL ORs permissive policies, so the tenant policy
--      of `pos_app` keeps filtering that role exactly as before, while the maintenance role
--      sees only the rows the retention rule admits. The window is enforced by the database,
--      not by the caller's SQL: a wrong statement cannot delete a fresh or an in-flight key
--      (§7.2: the policy is a hard bound), and the role cannot read any other column.

GRANT SELECT (state, created_at) ON TABLE idempotency_keys TO pos_maintenance;

CREATE POLICY idempotency_keys_pos_maintenance_select ON idempotency_keys
    FOR SELECT TO pos_maintenance
    USING (state = 'completed' AND created_at < now() - interval '400 days');

CREATE POLICY idempotency_keys_pos_maintenance_delete ON idempotency_keys
    FOR DELETE TO pos_maintenance
    USING (state = 'completed' AND created_at < now() - interval '400 days');

-- +goose Down
DROP POLICY IF EXISTS idempotency_keys_pos_maintenance_delete ON idempotency_keys;
DROP POLICY IF EXISTS idempotency_keys_pos_maintenance_select ON idempotency_keys;
REVOKE SELECT (state, created_at) ON TABLE idempotency_keys FROM pos_maintenance;
