package fiado

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Este archivo entrega la mitad de `fiado_accounts` del pull por cursor (Sprint 6,
// W6-BE-3; docs/DATABASE.md §7.3, docs/BACKEND.md §4.8). Es la ÚNICA lectura de esta tabla
// fuera de lo que el Sprint 7 construirá: el pull necesita las cuentas editables para que
// un segundo dispositivo vea el mismo registro de deudores.
//
// Límite declarado (decisión D2 de odd/tasks/sprint-6-offline-sync.md): `fiado_accounts` no
// tiene columna `deleted_at` (migrations/20261015000005_fiado.sql) y el MVP no define
// borrado de cuentas, así que este pull no fabrica tombstones donde el esquema no los
// sostiene. El cursor de `products` sí los lleva.

// accountColumns es el paquete de columnas de §4 `fiado_accounts`. No hay saldo: el saldo
// es `SUM(fiado_movements.amount_cents)` y nunca viaja en la cuenta (§3.2).
const accountColumns = `id, tenant_id, name, phone, created_at, updated_at, received_at`

// accountsUpdatedSinceSQL es el pull por cursor de las cuentas del tenant. El parámetro
// nulo es el pull inicial completo.
const accountsUpdatedSinceSQL = `SELECT ` + accountColumns + ` FROM fiado_accounts
	WHERE ($1::timestamptz IS NULL OR updated_at > $1)
	ORDER BY updated_at, id`

// Account es la fila de `fiado_accounts` (docs/DATABASE.md §4) tal como el pull la
// devuelve. Nunca lleva saldo: el saldo se computa de los movimientos y no es un dato de
// la cuenta.
type Account struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Name       string
	Phone      *string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ReceivedAt time.Time
}

// PostgresAccountStore lee `fiado_accounts` sobre el pool de aplicación envuelto en el
// helper de transacción de tenant: el RLS decide qué cuentas existen para el tenant.
type PostgresAccountStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresAccountStore construye el store de cuentas sobre el pool de `pos_app`.
func NewPostgresAccountStore(tenants *postgres.TenantPool) *PostgresAccountStore {
	return &PostgresAccountStore{tenants: tenants}
}

// ListUpdatedSince devuelve las cuentas del tenant con `updated_at` posterior al cursor,
// ordenadas por `updated_at, id`. Un cursor nulo es el pull inicial completo; un cursor
// futuro devuelve el conjunto vacío sin error (§7.3).
func (s *PostgresAccountStore) ListUpdatedSince(ctx context.Context, tenantID uuid.UUID, after *time.Time) ([]Account, error) {
	if s == nil || s.tenants == nil {
		return nil, errors.New("the fiado account store has no tenant pool")
	}
	if tenantID == uuid.Nil {
		return nil, errors.New("the fiado account pull needs a tenant id")
	}

	var accounts []Account
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		rows, err := tx.Query(ctx, accountsUpdatedSinceSQL, after)
		if err != nil {
			return fmt.Errorf("pull the fiado accounts after the cursor: %w", err)
		}
		defer rows.Close()

		accounts = make([]Account, 0)
		for rows.Next() {
			var account Account
			if err := rows.Scan(&account.ID, &account.TenantID, &account.Name, &account.Phone,
				&account.CreatedAt, &account.UpdatedAt, &account.ReceivedAt); err != nil {
				return fmt.Errorf("read the fiado account row: %w", err)
			}
			accounts = append(accounts, account)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return accounts, nil
}
