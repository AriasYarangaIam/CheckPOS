-- +goose Up
-- Módulo: identidad y aislamiento de tenant — FORCE RLS y las políticas de
-- docs/DATABASE.md §5.1 a §5.4.
--
-- Las once tablas de tenant quedan con RLS habilitado y FORZADO, y con una política por
-- operación de DML que comparte el mismo predicado. `tenants` (el registro) y las dos tablas
-- globales quedan afuera por diseño (§3, §5.6): el registro no tiene `tenant_id` y el
-- catálogo global lo lee la API a través de pos_platform.
--
-- Esta migración es la que vuelve el aislamiento un invariante de la base: sin GUC, una
-- consulta de pos_app no ve ninguna fila, y una escritura con el `tenant_id` de otro tenant
-- se rechaza. La matriz de concesiones que la hace efectiva —sin ella el proceso se conectaría
-- como dueño y el RLS no lo ligaría— está en 20261016000001_roles_and_grants.sql.

-- El predicado de §5.3, literal.
--
-- §5.3 lo fija como `tenant_id = current_setting('app.current_tenant_id')::uuid` y este
-- archivo lo escribe tal cual. El obstáculo medido nunca fue el texto del predicado sino el
-- PARÁMETRO SIN DEFINIR, que PostgreSQL no lee como NULL (medido contra este contenedor, en
-- una base con una política literal y un rol sin ninguna otra política que la enmascare):
--
--   | Estado de la sesión                                                 | current_setting(...)::uuid        |
--   | ------------------------------------------------------------------- | --------------------------------- |
--   | Conexión nueva, parámetro desconocido                               | ERROR 42704 unrecognized parameter |
--   | Tras un `set_config(...)` en esa misma sesión, sentencia siguiente  | ERROR 22P02 invalid input syntax for type uuid: "" |
--   | Conexión nueva, con un default definido para el parámetro           | el default; la política devuelve 0 filas, sin error |
--   | `set_config(..., true)` y sentencia después del commit, con default | el default otra vez (no ""); 0 filas, sin error |
--
-- Definir el default resuelve las cuatro filas: el literal de §5.3 cumple el criterio de
-- aceptación de W2-DB-1 ("sin `SET LOCAL` la consulta devuelve cero filas") en todo estado de
-- sesión, y con un tenant real fijado sigue filtrando igual (medido: 1 fila del tenant del
-- contexto, y un `WITH CHECK` con el `tenant_id` de otro tenant rechazado con 42501).
--
-- El default es un marcador, no una puerta: el UUID nulo no es el id de ningún tenant, la
-- comparación sigue siendo la misma igualdad contra el GUC, y una escritura que llevara ese
-- id sería rechazada por el `WITH CHECK` de cualquier tenant real. La mutación inversa queda
-- medida: quitar el default y abrir una sesión nueva devuelve 42704, es decir cero filas se
-- convierte en error, nunca en una fuga.
--
-- Por qué a nivel de base y no por rol: la política de tenant de `users` también se evalúa
-- para `pos_auth` —las políticas permisivas se combinan con OR y no hay garantía de
-- cortocircuito— y `pos_maintenance` borra filas de `idempotency_keys`, que también está bajo
-- política de tenant. Un default por rol habría que mantenerlo en sincronía en tres roles y
-- el dueño seguiría sin cobertura. Un default de base cubre toda conexión a esta base,
-- cualquiera sea su rol, incluido el dueño.
--
-- Se aplica al conectar: una sesión que ya estaba abierta cuando esta migración corrió sigue
-- con el parámetro sin definir y falla con 42704 hasta reconectar (medido). Falla cerrado,
-- nunca fuga, y en el orden normal —migrar y después arrancar la API— no ocurre.
-- +goose StatementBegin
DO $rls$
DECLARE
    placeholder constant text := '00000000-0000-0000-0000-000000000000';
    predicate constant text := $predicate$tenant_id = current_setting('app.current_tenant_id')::uuid$predicate$;
    relation text;
BEGIN
    -- El default del marcador, en la base en la que corre esta migración. `ALTER DATABASE`
    -- admite estar dentro de una transacción —este bloque corre en la transacción que goose
    -- abre por migración— y se probó explícitamente en este contenedor.
    EXECUTE format('ALTER DATABASE %I SET app.current_tenant_id = %L', current_database(), placeholder);

    FOR relation IN
        -- Las once tablas de tenant de §5.1.
        SELECT unnest(ARRAY[
            'users',
            'devices',
            'products',
            'stock_adjustments',
            'sales',
            'sale_lines',
            'fiado_accounts',
            'fiado_movements',
            'cash_closes',
            'cash_close_counts',
            'idempotency_keys'
        ]::text[]) AS relname
        UNION ALL
        -- Y las particiones de `sales`, incluidas las mensuales y `sales_default`.
        --
        -- PostgreSQL NO propaga el RLS ni las políticas de una tabla particionada a sus
        -- particiones, y tampoco propaga las concesiones. Medido contra este contenedor: con
        -- el pin y la política sólo en `sales`, la partición tiene relrowsecurity en falso y
        -- un `SELECT` directo sobre ella (con concesión) devuelve las filas de los dos
        -- tenants; sin concesión falla con 42501. Para que el acceso directo a una partición
        -- se comporte igual que el acceso por el padre, cada partición recibe las mismas
        -- políticas y el mismo pin. El nombre de política se repite en la partición porque
        -- los nombres son por relación, y así el espejo es evidente.
        --
        -- Consecuencia para W6-DB-2 (pre-creación mensual): cada partición nueva necesita
        -- este mismo tratamiento —pin, las cuatro políticas y las concesiones— o el mes
        -- siguiente queda sin aislamiento. Lo que falla cerrado es la concesión ausente; lo
        -- que fuga es la política ausente con concesión presente.
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'public.sales'::regclass
        ORDER BY 1
    LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', relation);
        EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY', relation);

        -- El fragmento USING filtra las filas visibles; el WITH CHECK rechaza escrituras con
        -- otro tenant. UPDATE lleva los dos: no alcanza con filtrar lo que se puede leer, la
        -- fila resultante también tiene que pertenecer al tenant del contexto.
        EXECUTE format('CREATE POLICY %I ON public.%I FOR SELECT USING (%s)',
            relation || '_tenant_select', relation, predicate);
        EXECUTE format('CREATE POLICY %I ON public.%I FOR INSERT WITH CHECK (%s)',
            relation || '_tenant_insert', relation, predicate);
        EXECUTE format('CREATE POLICY %I ON public.%I FOR UPDATE USING (%s) WITH CHECK (%s)',
            relation || '_tenant_update', relation, predicate, predicate);
        EXECUTE format('CREATE POLICY %I ON public.%I FOR DELETE USING (%s)',
            relation || '_tenant_delete', relation, predicate);
    END LOOP;
END
$rls$;
-- +goose StatementEnd

-- Bootstrap pre-auth del login (§5.4): `pos_auth` es el rol que resuelve `phone` +
-- `password_hash` antes de que exista un JWT, así que corre sin contexto de tenant. Su única
-- tabla legible es `users` —así lo fija la matriz de §5.5— y esta política permisiva es la que
-- le da la lectura completa: las políticas permisivas se combinan con OR, de modo que para
-- `pos_auth` alcanza con `true`, mientras el resto de los roles sigue viendo sólo su tenant.
-- Es el único camino de la base que lee por encima de un tenant, y existe para una sola forma
-- de consulta.
CREATE POLICY users_pos_auth_select ON users FOR SELECT TO pos_auth USING (true);

-- +goose Down
-- El reverso tiene que dejar el esquema como estaba antes de esta migración —sin políticas,
-- sin RLS y sin el marcador— para que el Down de la migración anterior pueda liberar los
-- roles.
DROP POLICY IF EXISTS users_pos_auth_select ON users;

-- +goose StatementBegin
DO $rls$
DECLARE
    relation text;
BEGIN
    -- El marcador se revierte primero: el ida y vuelta no deja nada atrás.
    EXECUTE format('ALTER DATABASE %I RESET app.current_tenant_id', current_database());

    FOR relation IN
        SELECT unnest(ARRAY[
            'users',
            'devices',
            'products',
            'stock_adjustments',
            'sales',
            'sale_lines',
            'fiado_accounts',
            'fiado_movements',
            'cash_closes',
            'cash_close_counts',
            'idempotency_keys'
        ]::text[]) AS relname
        UNION ALL
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'public.sales'::regclass
        ORDER BY 1
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', relation || '_tenant_select', relation);
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', relation || '_tenant_insert', relation);
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', relation || '_tenant_update', relation);
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', relation || '_tenant_delete', relation);
        EXECUTE format('ALTER TABLE public.%I NO FORCE ROW LEVEL SECURITY', relation);
        EXECUTE format('ALTER TABLE public.%I DISABLE ROW LEVEL SECURITY', relation);
    END LOOP;
END
$rls$;
-- +goose StatementEnd
