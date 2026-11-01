package fiado

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// La cartera de deudores (docs/BACKEND.md §4.12): las cuentas del tenant con su saldo
// COMPUTADO y el historial firmado de cada una. Es uno de los dos únicos insumos del dashboard
// Admin en esta versión (ARCHITECTURE.md:138) y, a la vez, la ruta de restauración tras
// reinstalar: el dispositivo que borró su base local reconstruye cuentas y movimientos desde
// acá (W7-BE-3).
//
// El saldo no es un dato de la cuenta: `fiado_accounts` NO tiene columna de saldo y este
// archivo no la busca. `balance_cents` es `SUM(fiado_movements.amount_cents)` por cuenta, la
// misma invariante que el acuse del movimiento computa (§4.12, S11).

// Movement es una fila de `fiado_movements` tal como la cartera la sirve. El signo de
// `AmountCents` ES el tipo: cargo positivo, pago negativo, y una anulación escribe una fila
// compensatoria de signo invertido —nunca un DELETE ni una edición— (§4 `fiado_movements`).
type Movement struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	AccountID uuid.UUID
	// AmountCents es el monto firmado.
	AmountCents int64
	SoldOn      *time.Time
	SaleID      *uuid.UUID
	Note        *string
	CreatedAt   time.Time
	ReceivedAt  time.Time
}

// AccountWithMovements es una cuenta del tenant con su saldo computado y su historial firmado
// completo. El historial completo (no una página) es lo que la restauración necesita.
type AccountWithMovements struct {
	Account
	// BalanceCents es `SUM(fiado_movements.amount_cents)`, computado.
	BalanceCents int64
	Movements    []Movement
}

// AccountsStore es el único camino de lectura de la cartera (patrón de
// internal/fiado/account.go): una interfaz, para que el handler se pruebe sin base.
type AccountsStore interface {
	ListAccounts(ctx context.Context, tenantID uuid.UUID) ([]AccountWithMovements, error)
}

// accountsSQL lee las cuentas del tenant con su saldo computado en la MISMA consulta. El orden
// es determinista —`name`, luego `id`— porque la respuesta es un contrato: dos lecturas del
// mismo tenant no pueden cambiar de orden.
const accountsSQL = `SELECT a.id, a.tenant_id, a.name, a.phone, a.created_at, a.updated_at, a.received_at,
	COALESCE((SELECT SUM(m.amount_cents) FROM fiado_movements m WHERE m.fiado_account_id = a.id), 0)
	FROM fiado_accounts a
	ORDER BY a.name, a.id`

// movementsSQL lee el historial firmado del tenant. El orden es determinista por la misma
// razón: `created_at`, luego `id`. Se agrupa en Go por cuenta —una lectura, no una por cuenta—.
const movementsSQL = `SELECT id, tenant_id, fiado_account_id, amount_cents, sold_on, sale_id, note, created_at, received_at
	FROM fiado_movements
	ORDER BY created_at, id`

// PostgresAccountsStore lee la cartera sobre el pool de aplicación envuelto en el helper de
// transacción de tenant: el RLS decide qué cuentas existen para el tenant.
type PostgresAccountsStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresAccountsStore construye el store de la cartera sobre el pool de `pos_app`.
func NewPostgresAccountsStore(tenants *postgres.TenantPool) *PostgresAccountsStore {
	return &PostgresAccountsStore{tenants: tenants}
}

// ListAccounts devuelve las cuentas del tenant con su saldo computado y su historial firmado,
// todo dentro de UNA transacción de tenant para que la foto sea coherente.
func (s *PostgresAccountsStore) ListAccounts(ctx context.Context, tenantID uuid.UUID) ([]AccountWithMovements, error) {
	if s == nil || s.tenants == nil {
		return nil, errors.New("the fiado accounts store has no tenant pool")
	}
	if tenantID == uuid.Nil {
		return nil, errors.New("the fiado accounts read needs a tenant id")
	}

	var accounts []AccountWithMovements
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		rows, err := tx.Query(ctx, accountsSQL)
		if err != nil {
			return fmt.Errorf("read the fiado accounts: %w", err)
		}
		defer rows.Close()

		accounts = make([]AccountWithMovements, 0)
		index := map[uuid.UUID]int{}
		for rows.Next() {
			var account AccountWithMovements
			if err := rows.Scan(&account.ID, &account.TenantID, &account.Name, &account.Phone,
				&account.CreatedAt, &account.UpdatedAt, &account.ReceivedAt, &account.BalanceCents); err != nil {
				return fmt.Errorf("read the fiado account row: %w", err)
			}
			account.Movements = make([]Movement, 0)
			index[account.ID] = len(accounts)
			accounts = append(accounts, account)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate the fiado accounts: %w", err)
		}

		movementRows, err := tx.Query(ctx, movementsSQL)
		if err != nil {
			return fmt.Errorf("read the fiado movements: %w", err)
		}
		defer movementRows.Close()

		for movementRows.Next() {
			var movement Movement
			if err := movementRows.Scan(&movement.ID, &movement.TenantID, &movement.AccountID,
				&movement.AmountCents, &movement.SoldOn, &movement.SaleID, &movement.Note,
				&movement.CreatedAt, &movement.ReceivedAt); err != nil {
				return fmt.Errorf("read the fiado movement row: %w", err)
			}
			// Un movimiento sin cuenta visible no tiene lugar en la respuesta: el RLS hace que
			// no pueda ocurrir, y el mapa lo deja explícito en lugar de inventar una cuenta.
			if position, ok := index[movement.AccountID]; ok {
				accounts[position].Movements = append(accounts[position].Movements, movement)
			}
		}
		return movementRows.Err()
	})
	if err != nil {
		return nil, err
	}

	return accounts, nil
}

// accountRead es la cuenta de la respuesta de §4.12: los campos de la cuenta, su saldo
// computado y el historial firmado (decisión 4 de forma).
type accountRead struct {
	accountBody
	Movements []movementBody `json:"movements"`
}

// accountsResponse es el cuerpo completo de la respuesta de §4.12.
type accountsResponse struct {
	Accounts []accountRead `json:"accounts"`
}

// FiadoAccountsHandler atiende GET /fiado-accounts sobre un store.
type FiadoAccountsHandler struct {
	accounts AccountsStore
	logger   *slog.Logger
}

// NewFiadoAccountsHandler construye el handler sobre el store de la cartera.
func NewFiadoAccountsHandler(accounts AccountsStore, logger *slog.Logger) (*FiadoAccountsHandler, error) {
	if accounts == nil {
		return nil, errors.New("the fiado accounts handler needs an accounts store")
	}
	if logger == nil {
		return nil, errors.New("the fiado accounts handler needs a logger")
	}
	return &FiadoAccountsHandler{accounts: accounts, logger: logger}, nil
}

// ServeHTTP resuelve la única forma de la ruta: lectura, nunca escritura.
func (h *FiadoAccountsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoFiadoClaims, h.logger))
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, httpx.NotFound("this route only answers GET"))
		return
	}

	accounts, err := h.accounts.ListAccounts(r.Context(), claims.TenantID)
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	body := accountsResponse{Accounts: make([]accountRead, 0, len(accounts))}
	for _, account := range accounts {
		read := accountRead{
			accountBody: accountBody{
				ID:           account.ID.String(),
				TenantID:     account.TenantID.String(),
				Name:         account.Name,
				Phone:        account.Phone,
				CreatedAt:    rfcTime(account.CreatedAt),
				UpdatedAt:    rfcTime(account.UpdatedAt),
				ReceivedAt:   rfcTime(account.ReceivedAt),
				BalanceCents: account.BalanceCents,
			},
			Movements: make([]movementBody, 0, len(account.Movements)),
		}
		for _, movement := range account.Movements {
			read.Movements = append(read.Movements, movementReadBody(movement))
		}
		body.Accounts = append(body.Accounts, read)
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

// movementReadBody proyecta un movimiento del ledger a la forma del contrato: los opcionales
// viajan como `null`, nunca ausentes.
func movementReadBody(movement Movement) movementBody {
	var soldOn, saleID *string
	if movement.SoldOn != nil {
		value := movement.SoldOn.Format(businessDayFormat)
		soldOn = &value
	}
	if movement.SaleID != nil {
		value := movement.SaleID.String()
		saleID = &value
	}

	return movementBody{
		ID:             movement.ID.String(),
		TenantID:       movement.TenantID.String(),
		FiadoAccountID: movement.AccountID.String(),
		AmountCents:    movement.AmountCents,
		SoldOn:         soldOn,
		SaleID:         saleID,
		Note:           movement.Note,
		CreatedAt:      rfcTime(movement.CreatedAt),
		ReceivedAt:     rfcTime(movement.ReceivedAt),
	}
}
