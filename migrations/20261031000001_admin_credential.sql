-- +goose Up
-- Módulo: Identidad — la credencial maestra del tenant (docs/BACKEND.md §4.4 y §11.4 #7;
-- docs/DATABASE.md §4 `tenants` nota delta D1; W8-DB-1).
--
-- Decisiones de forma D-A = A1 y D-B = B1 de odd/tasks/sprint-8-admin-hardening.md:
--
--   - la columna es `TEXT NOT NULL` como la documenta el contrato, y el hueco que la
--     divergencia §11.4 #7 señalaba —«NOT NULL sin default» contra «tenant sin hash → 401»—
--     se cierra por el otro lado: NO existe un tenant sin hash. Toda fila nace con un
--     argon2id bien formado cuya preimagen nadie conoce, así que un tenant sin contraseña
--     configurada simplemente no puede autenticarse;
--   - la contraseña real la escribe SOLO `cmd/maintenance admin-password`, con el rol de
--     migración. No se agrega ninguna ruta HTTP: los 13 caminos de §4 la agotan.

-- La columna nace NULL para que el relleno de las filas existentes sea ALEATORIO por fila:
-- un `ADD COLUMN ... NOT NULL DEFAULT <volátil>` haría que todas las filas viejas compartieran
-- un mismo valor, y el default solo se evalúa para las inserciones nuevas.
ALTER TABLE tenants ADD COLUMN master_password_hash TEXT;

-- Relleno: la forma exacta que produce internal/identity.HashCredential
-- (`$argon2id$v=19$m=65536,t=3,p=2$<sal>$<hash>`, base64 sin relleno). Cada campo es UN solo
-- valor de entropía: 32 bytes de md5 codificados como 43 caracteres base64 canónicos. Pegar dos
-- trozos recortados desplaza los bits y el decodificador estricto de Go lo rechaza como cadena
-- inválida —medido al escribir esta migración—. Lo único que se necesita es que NADIE conozca la
-- preimagen.
UPDATE tenants
   SET master_password_hash =
           '$argon2id$v=19$m=65536,t=3,p=2$'
           || rtrim(encode(convert_to(md5(random()::text || clock_timestamp()::text), 'UTF8'), 'base64'), '=')
           || '$'
           || rtrim(encode(convert_to(md5(random()::text || clock_timestamp()::text), 'UTF8'), 'base64'), '=');

-- Y las filas nuevas tampoco nacen sin hash: el DEFAULT sortea el mismo valor inutilizable en
-- cada INSERT. Es volátil a propósito, para que dos tenants no compartan el valor.
ALTER TABLE tenants
    ALTER COLUMN master_password_hash SET DEFAULT (
        '$argon2id$v=19$m=65536,t=3,p=2$'
        || rtrim(encode(convert_to(md5(random()::text || clock_timestamp()::text), 'UTF8'), 'base64'), '=')
        || '$'
        || rtrim(encode(convert_to(md5(random()::text || clock_timestamp()::text), 'UTF8'), 'base64'), '='));

ALTER TABLE tenants ALTER COLUMN master_password_hash SET NOT NULL;

-- El login de administrador corre ANTES de que exista un token, así que su lectura la hace el
-- rol pre-auth `pos_auth` (docs/DATABASE.md §5.4, §5.6). La concesión es POR COLUMNA: el rol
-- que atiende el login no puede leer nada más del registro de tenants — ni `plan` ni
-- `created_at` —, y `tenants` no tiene RLS por diseño, así que esta concesión es la barrera
-- completa.
GRANT SELECT (id, name, master_password_hash) ON TABLE tenants TO pos_auth;

-- +goose Down
REVOKE SELECT (id, name, master_password_hash) ON TABLE tenants FROM pos_auth;
ALTER TABLE tenants ALTER COLUMN master_password_hash DROP DEFAULT;
ALTER TABLE tenants DROP COLUMN IF EXISTS master_password_hash;
