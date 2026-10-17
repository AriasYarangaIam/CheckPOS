-- +goose Up
-- Módulo: identidad y aislamiento de tenant — matriz de concesiones de docs/DATABASE.md §5.5.
-- Roles: los cuatro de runtime (pos_app, pos_auth, pos_platform, pos_maintenance) y el rol
-- dueño/migración, que ya existe y no se crea aquí.
--
-- Por qué existe esta migración: hasta ahora el proceso se conectaba con el rol de
-- desarrollo, que es dueño de las tablas y superusuario. El aislamiento por tenant de la
-- migración siguiente solo vale algo si el rol que atiende las peticiones NO es el dueño y
-- NO puede saltarse las políticas. Esta migración entrega la mitad de privilegios de ese
-- contrato; la mitad de políticas es 20261016000002_tenant_rls.sql.
--
-- El quinto rol de la matriz es `owner/migration`: el rol que ya creó este esquema y con el
-- que corre `cmd/migrate`. No se crea aquí —existe por definición— y no recibe ninguna
-- concesión de datos: es DDL únicamente, y FORCE RLS liga también al dueño de la tabla
-- (§5.5). Renombrarlo no es tarea de esta migración.
--
-- Los roles son del clúster, no de la base: una base nueva los encuentra ya creados, así que
-- su creación tiene que ser idempotente o la segunda migración aborta.

-- Contraseña: el valor de desarrollo local que ya está en el repositorio
-- (internal/platform/config/config.go:27 y docker-compose.yml). Una migración no es el lugar
-- de un secreto real: producción la rota fuera de banda, y esta línea se queda como el valor
-- de conveniencia para la base local. Los roles que ya existen no se tocan, así que una base
-- migrada después de la rotación no la pisa.
-- +goose StatementBegin
DO $$
DECLARE
    role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY['pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance']::text[]
    LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name) THEN
            EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', role_name, 'pos_local_dev');
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- `USAGE ON SCHEMA public` es lo mínimo para que las políticas y las tablas sean
-- alcanzables; no concede CREATE, así que ningún rol de runtime puede agregar objetos al
-- esquema.
GRANT USAGE ON SCHEMA public TO pos_app, pos_auth, pos_platform, pos_maintenance;

-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format(
        'GRANT CONNECT ON DATABASE %I TO pos_app, pos_auth, pos_platform, pos_maintenance',
        current_database());
END
$$;
-- +goose StatementEnd

-- pos_app — SELECT sobre exactamente las once tablas de tenant de §5.1, enumeradas una por
-- una. Nunca un comodín, nunca `tenants` (F14: el alta pre-auth no necesita leer el registro,
-- y por eso `INSERT ... RETURNING` sobre `tenants` está prohibido para este rol), nunca las
-- dos tablas globales (§5.6: las lee la API a través de pos_platform).
GRANT SELECT ON TABLE
    users,
    devices,
    products,
    stock_adjustments,
    sales,
    sale_lines,
    fiado_accounts,
    fiado_movements,
    cash_closes,
    cash_close_counts,
    idempotency_keys
TO pos_app;

-- pos_app — INSERT sobre las tablas que escribe HTTP más `tenants` (el alta pre-auth). Cada
-- línea dice qué endpoint escribe la tabla (docs/BACKEND.md §4 salvo donde se indica).
-- alta pre-auth de §5.4: no tiene endpoint en la superficie de §4.15; la cubre el alta del piloto (W9-FE-1).
GRANT INSERT ON TABLE tenants TO pos_app;
-- §4.2 POST /auth/login la lee; quien la escribe es el mismo alta pre-auth de §5.4.
GRANT INSERT ON TABLE users TO pos_app;
-- §4.3 POST /auth/device-bind (bind y re-bind).
GRANT INSERT ON TABLE devices TO pos_app;
-- §4.6 POST /products.
GRANT INSERT ON TABLE products TO pos_app;
-- §4.14 POST /stock-adjustments (merma) y el delta de venta de §4.9.
GRANT INSERT ON TABLE stock_adjustments TO pos_app;
-- §4.9 POST /sales y §4.10 POST /sales/{id}/void.
GRANT INSERT ON TABLE sales TO pos_app;
-- §4.9 POST /sales (hijo de `sales`, misma transacción).
GRANT INSERT ON TABLE sale_lines TO pos_app;
-- §4.11 POST /fiado-movements: upsert atómico de la cuenta (F12).
GRANT INSERT ON TABLE fiado_accounts TO pos_app;
-- §4.11 POST /fiado-movements (cargo o pago).
GRANT INSERT ON TABLE fiado_movements TO pos_app;
-- §4.13 POST /cash-closes.
GRANT INSERT ON TABLE cash_closes TO pos_app;
-- §4.13 POST /cash-closes (hijo del cierre, misma transacción).
GRANT INSERT ON TABLE cash_close_counts TO pos_app;
-- El encabezado `Idempotency-Key` de todo POST encolable (§4.1 y §6): la fila ES el claim.
GRANT INSERT ON TABLE idempotency_keys TO pos_app;

-- pos_app — UPDATE solo en cuatro tablas, y cada una por una razón distinta: LWW de catálogo
-- y perfil, ciclo de vida del claim, y ciclo de vida de autenticación del dispositivo. El
-- ledger no aparece: `sales`, `sale_lines`, `fiado_movements`, `stock_adjustments` y
-- `cash_close_counts` son append-only (§2.12).
-- §4.7 PATCH /products/{id} (LWW) y la proyección de stock sobre `stock`, que no toca updated_at (MD-11).
GRANT UPDATE ON TABLE products TO pos_app;
-- §4.11 POST /fiado-movements: upsert de nombre y teléfono (LWW).
GRANT UPDATE ON TABLE fiado_accounts TO pos_app;
-- §6 protocolo de claim: `in_flight` -> `completed` con su snapshot.
GRANT UPDATE ON TABLE idempotency_keys TO pos_app;
-- §4.3 POST /auth/device-bind: re-bind, cambio de PIN y refresco de la ventana offline.
GRANT UPDATE ON TABLE devices TO pos_app;

-- pos_app — DELETE sobre nada. El append-only de las cinco tablas del ledger es estructural
-- por esta línea, por encima de las guardas de trigger de W4-DB-1 (§2.12, §5.5).

-- pos_auth — SELECT sobre `users` y sobre ninguna otra tabla. Es el rol pre-auth del login
-- (§4.2, §5.4): existe para una sola forma de consulta, `phone` + `password_hash`, y su
-- lectura la habilita la política `FOR SELECT TO pos_auth USING (true)` de la migración
-- siguiente.
GRANT SELECT ON TABLE users TO pos_auth;

-- pos_platform — lectura y escritura del catálogo global curado por plataforma: es el único
-- rol que alcanza las dos tablas, y a los roles de tenant les corresponden cero concesiones
-- sobre ellas (§5.6).
GRANT SELECT, INSERT, UPDATE ON TABLE global_products, global_barcodes TO pos_platform;
-- pos_platform — DELETE sobre nada: la matriz de §5.5 es la autoridad y dice NOTHING. La
-- prosa de §4 `global_barcodes` ("INSERT/UPDATE/DELETE por pos_platform") contradice a la
-- matriz en esa palabra; se implementa la matriz, y la divergencia queda registrada.

-- pos_maintenance — DELETE sobre `idempotency_keys` únicamente: la retención de 400 días
-- (MD-3, §7.2). La pre-creación de particiones que la matriz también le asigna es
-- programación, no un privilegio de base: el DDL de particiones corre bajo el rol de
-- migración (reparto F9) y llega en W6-DB-2.
GRANT DELETE ON TABLE idempotency_keys TO pos_maintenance;

-- Las particiones de `sales` no heredan las concesiones de su tabla padre: PostgreSQL las
-- guarda por relación, así que un `SELECT` directo sobre `sales_2026_10` falla con 42501
-- aunque `pos_app` tenga `SELECT` sobre `sales`. Se probó contra este mismo contenedor. Para
-- que el acceso directo a una partición se comporte igual que el acceso por el padre, cada
-- partición recibe las mismas concesiones que `sales`. Las particiones no son tablas del
-- contrato: son el almacenamiento físico de `sales`, y el conteo de 14 tablas no cambia
-- (schema_contract_test.go las excluye con `relispartition`).
-- Consecuencia para W6-DB-2: la pre-creación mensual tiene que repetir este espejo en cada
-- partición nueva, o el mes siguiente queda sin concesiones (falla cerrado, pero rompe el
-- acceso directo).
-- +goose StatementBegin
DO $$
DECLARE
    partition_name text;
BEGIN
    FOR partition_name IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'public.sales'::regclass
        ORDER BY c.relname
    LOOP
        EXECUTE format('GRANT SELECT, INSERT ON TABLE public.%I TO pos_app', partition_name);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Reverso exacto del Up: primero se revocan las concesiones —tabla por tabla, con el mismo
-- espejo de particiones— y recién después se intenta liberar los roles.
REVOKE SELECT ON TABLE
    users,
    devices,
    products,
    stock_adjustments,
    sales,
    sale_lines,
    fiado_accounts,
    fiado_movements,
    cash_closes,
    cash_close_counts,
    idempotency_keys
FROM pos_app;

REVOKE INSERT ON TABLE users, devices, products, stock_adjustments, sales, sale_lines,
    fiado_accounts, fiado_movements, cash_closes, cash_close_counts, idempotency_keys,
    tenants
FROM pos_app;

REVOKE UPDATE ON TABLE products, fiado_accounts, idempotency_keys, devices FROM pos_app;

REVOKE SELECT ON TABLE users FROM pos_auth;

REVOKE SELECT, INSERT, UPDATE ON TABLE global_products, global_barcodes FROM pos_platform;

REVOKE DELETE ON TABLE idempotency_keys FROM pos_maintenance;

-- +goose StatementBegin
DO $$
DECLARE
    partition_name text;
BEGIN
    FOR partition_name IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'public.sales'::regclass
        ORDER BY c.relname
    LOOP
        EXECUTE format('REVOKE SELECT, INSERT ON TABLE public.%I FROM pos_app', partition_name);
    END LOOP;
END
$$;
-- +goose StatementEnd

REVOKE USAGE ON SCHEMA public FROM pos_app, pos_auth, pos_platform, pos_maintenance;

-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format(
        'REVOKE CONNECT ON DATABASE %I FROM pos_app, pos_auth, pos_platform, pos_maintenance',
        current_database());
END
$$;
-- +goose StatementEnd

-- La liberación de roles es tolerante a propósito. Un rol no se puede eliminar mientras el
-- clúster conserve privilegios otorgados a él: con más de una base migrada —el caso normal,
-- porque los tests de integración crean y eliminan bases desechables y la base de desarrollo
-- sigue ahí— `DROP ROLE` falla con 2BP01 dependiendo de en qué base corra este Down. Un Down
-- que falla deja la migración irrepetible, así que el fallo se registra y se sigue. Un rol
-- huérfano sin concesiones no concede nada: el Up de esta migración lo vuelve a usar tal cual
-- (la creación es idempotente) y la política de `pos_auth` de la migración siguiente sigue
-- resolviendo.
-- Los roles son del clúster: eliminar una concesión en OTRA base está fuera del alcance de
-- este archivo, y por eso el intento es best-effort y no una promesa.
-- +goose StatementBegin
DO $$
DECLARE
    role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY['pos_app', 'pos_auth', 'pos_platform', 'pos_maintenance']::text[]
    LOOP
        BEGIN
            EXECUTE format('DROP ROLE IF EXISTS %I', role_name);
        EXCEPTION
            WHEN dependent_objects_still_exist THEN
                RAISE NOTICE 'rol % no liberado: conserva dependencias en el clúster (concesiones en otra base). SQLSTATE %', role_name, SQLSTATE;
            WHEN OTHERS THEN
                RAISE NOTICE 'rol % no liberado: %. SQLSTATE %', role_name, SQLERRM, SQLSTATE;
        END;
    END LOOP;
END
$$;
-- +goose StatementEnd
