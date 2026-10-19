-- +goose Up
-- Módulo: Ledger / Sync — las guardas de trigger del append-only, docs/DATABASE.md §2.12 y §4.
--
-- Por qué existe: la matriz de concesiones de 20261016000001_roles_and_grants.sql (§5.5)
-- ya niega UPDATE y DELETE a los cuatro roles de runtime sobre las cinco tablas append-only,
-- y su propio comentario (líneas 113-114) declara esperada la otra mitad del contrato:
-- «el append-only de las cinco tablas del ledger es estructural por esta línea, por encima
-- de las guardas de trigger de W4-DB-1 (§2.12, §5.5)». Esta migración entrega esas guardas
-- (MD-2): un UPDATE o un DELETE sobre la historia confirmada lanza SQLSTATE 55006 sin
-- importar el rol que lo intente, incluido el dueño y el superusuario que migra.
--
-- Por qué son triggers de FILA: los triggers de fila sobre una tabla particionada se
-- clonan automáticamente a cada partición existente y a las que W6-DB-2 cree después con
-- CREATE TABLE ... PARTITION OF, así que ninguna partición futura hereda un hueco. Un
-- trigger de sentencia sobre el padre NO cubriría las escrituras dirigidas a la partición
-- y W6-DB-2 tendría que espejarlo a mano, el mismo riesgo que la nota de espejo de
-- concesiones de roles_and_grants.sql ya registró.
--
-- `idempotency_keys` NO se guarda: §2.12 la declara la excepción a propósito (el ciclo de
-- vida del claim necesita UPDATE y la retención de 400 días necesita DELETE, MD-3).

-- La función guarda: una sola sirve para las cinco tablas y nombra la tabla que rechazó.
-- Los marcadores StatementBegin/StatementEnd son los mismos que roles_and_grants.sql usa
-- para sus bloques DO $$: goose corta por «;» y sin ellos el cuerpo de la función queda
-- partido a la mitad.
-- +goose StatementBegin
CREATE FUNCTION guard_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% es append-only: la historia confirmada no se edita ni se borra (§2.12, MD-2)',
        TG_TABLE_NAME
        USING ERRCODE = '55006';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER sales_append_only_guard
    BEFORE UPDATE OR DELETE ON sales
    FOR EACH ROW EXECUTE FUNCTION guard_append_only();

CREATE TRIGGER sale_lines_append_only_guard
    BEFORE UPDATE OR DELETE ON sale_lines
    FOR EACH ROW EXECUTE FUNCTION guard_append_only();

CREATE TRIGGER fiado_movements_append_only_guard
    BEFORE UPDATE OR DELETE ON fiado_movements
    FOR EACH ROW EXECUTE FUNCTION guard_append_only();

CREATE TRIGGER stock_adjustments_append_only_guard
    BEFORE UPDATE OR DELETE ON stock_adjustments
    FOR EACH ROW EXECUTE FUNCTION guard_append_only();

CREATE TRIGGER cash_close_counts_append_only_guard
    BEFORE UPDATE OR DELETE ON cash_close_counts
    FOR EACH ROW EXECUTE FUNCTION guard_append_only();

-- +goose Down
DROP TRIGGER IF EXISTS cash_close_counts_append_only_guard ON cash_close_counts;
DROP TRIGGER IF EXISTS stock_adjustments_append_only_guard ON stock_adjustments;
DROP TRIGGER IF EXISTS fiado_movements_append_only_guard ON fiado_movements;
DROP TRIGGER IF EXISTS sale_lines_append_only_guard ON sale_lines;
DROP TRIGGER IF EXISTS sales_append_only_guard ON sales;
DROP FUNCTION IF EXISTS guard_append_only;
