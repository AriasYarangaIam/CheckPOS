package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Este archivo implementa la mitad de runtime del contrato de RLS: el helper de transacción
// que fija el contexto de tenant como PRIMERA sentencia y lo deja resetear al confirmar
// (docs/BACKEND.md §5.3 y §7.7; docs/DATABASE.md §5.2; decisión D4).
//
// El requisito no es el tamaño del pool sino la seguridad del pooler: `SET LOCAL` por
// transacción es lo único que impide que el contexto de tenant se filtre entre clientes que
// reutilizan una conexión. Un pooler en modo transacción es compatible; un `SET` de sesión
// habría dejado el tenant de un cliente pegado a la conexión que sirve al siguiente.
//
// La regla operativa «ninguna consulta corre fuera del helper» se cierra acá en el TIPO:
// TenantPool no expone ninguna forma de consultar. No hay `Acquire`, ni `Query`, ni `Exec`, ni
// `Begin`, así que un módulo que reciba este envoltorio no puede saltearse el contexto ni por
// descuido ni por comodidad. Un sondeo de salud no es una consulta de tenant y vive sobre el
// pool crudo (ver cmd/api), que es el único lugar donde eso se hace y está documentado.

// setTenantContextSQL es la forma parametrizable de `SET LOCAL app.current_tenant_id = ...`.
//
// La equivalencia, que es exactamente lo que hace segura la reutilización de conexiones:
//
//	SET LOCAL app.current_tenant_id = '<tenant_id>'          -- no acepta marcadores de posición
//	SELECT set_config('app.current_tenant_id', $1, true)     -- el tercer argumento ES el LOCAL
//
// `set_config` con `is_local = true` liga el valor a la transacción actual y PostgreSQL lo
// revierte al confirmar o al abortar, igual que `SET LOCAL`. La forma parametrizable es la
// única que permite pasar el tenant como argumento en lugar de interpolar texto en SQL. Las
// políticas siguen leyendo `current_setting('app.current_tenant_id')::uuid`, sin cambios.
const setTenantContextSQL = `SELECT set_config('app.current_tenant_id', $1, true)`

// TenantTx es lo que el llamador puede hacer dentro de una transacción de tenant: consultar.
//
// `pgx.Tx` lo satisface tal cual, y la interfaz recorta a propósito todo lo que decida el final
// de la transacción —`Commit`, `Rollback`— y todo lo que abra otra conexión. El cierre lo
// decide el helper: un callback que confirmara por su cuenta cerraría una transacción con un
// contexto de tenant que ya no le corresponde.
type TenantTx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TenantPool envuelve el pool de datos de la aplicación. Su única ruta de datos es
// WithTenant: el tipo no ofrece ninguna forma de consultar sin contexto de tenant.
type TenantPool struct {
	pool *pgxpool.Pool
}

// NewTenantPool envuelve el pool de datos.
func NewTenantPool(pool *pgxpool.Pool) *TenantPool {
	return &TenantPool{pool: pool}
}

// WithTenant abre una transacción, fija el contexto de tenant como primera sentencia, corre fn
// y confirma si fn no devolvió error.
//
// El final es de acá y no del callback: un error de fn revierte todo y se devuelve TAL CUAL
// —sin envolver, para que el llamador pueda reconocerlo con errors.Is— y un panic también
// revierte, porque el rollback diferido corre durante el desenrollado. Un `Commit` exitoso
// deja que el rollback diferido devuelva `pgx.ErrTxClosed`, que se descarta: no hay que
// ramificar para eso.
//
// El UUID nulo se rechaza antes de abrir la transacción, y no es una formalidad: la base tiene
// un default de marcador igual a ese valor (lo dejó la migración de RLS), así que una
// transacción «con» tenant nulo se comportaría igual que una sin contexto —cero filas, cero
// escrituras— y se leería como un tenant vacío en lugar de como el defecto de programación que
// es.
func (p *TenantPool) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(context.Context, TenantTx) error) error {
	if p == nil || p.pool == nil {
		return errors.New("the tenant pool has no connection pool")
	}
	if tenantID == uuid.Nil {
		return errors.New("the tenant id is the nil uuid: without a real tenant every query would read zero rows and look like an empty tenant")
	}
	if fn == nil {
		return errors.New("the tenant transaction needs a callback")
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin the tenant transaction: %w", err)
	}
	// El rollback diferido cubre los tres finales: la primera sentencia que falla, el error que
	// devuelve fn y el desenrollado de un panic.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, setTenantContextSQL, tenantID.String()); err != nil {
		return fmt.Errorf("set the tenant context: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit the tenant transaction: %w", err)
	}
	return nil
}
