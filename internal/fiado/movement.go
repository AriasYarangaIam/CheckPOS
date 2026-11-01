package fiado

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// El movimiento de fiado (docs/BACKEND.md §4.11): un cargo positivo o un pago negativo que
// se anexa al ledger firmado, con el upsert atómico de la cuenta cuando el cuerpo la registra
// (docs/DATABASE.md §7.8, F12). Todo dentro de la transacción de tenant del pool de `pos_app`,
// RLS de por medio.
//
// Dos invariantes mandan sobre este archivo:
//
//   - El saldo NO se escribe: es `SUM(fiado_movements.amount_cents)` y el acuse lo computa
//     dentro de la MISMA transacción, para que el número que ve el cajero sea el de la base
//     que acaba de confirmar (§4.12).
//   - El upsert es parte de la transacción del claim: si la escritura falla, la cuenta
//     recién creada revierte con ella y el claim no queda reclamado (MD-4, §7.8).

// Sentinelas de la referencia por cuerpo: la superficie las mapea a un 422 que nombra el
// campo, jamás el id ajeno ni la existencia de otro tenant (§4.1).
var (
	// ErrUnknownAccount es una cuenta que el RLS no deja ver: puede no existir, o pertenecer a
	// otro tenant, y el 422 no distingue las dos causas. La FK de `fiado_movements` corre FUERA
	// del alcance del RLS —aprobaría la cuenta ajena como aprueba la propia—, así que la
	// barrera es la relectura dentro del contexto de tenant.
	ErrUnknownAccount = errors.New("the fiado movement references an account that is not in the tenant")

	// ErrUnknownSale es el enlace opcional a una venta que el tenant no tiene.
	ErrUnknownSale = errors.New("the fiado movement references a sale that is not in the tenant")
)

// Nombres de las restricciones que la resolución de errores necesita nombrar: son la única
// forma de separar «la venta enlazada no existe» de cualquier otra violación de la transacción.
const (
	saleLinkConstraint    = "fiado_movements_sold_on_sale_id_fkey"
	accountLinkConstraint = "fiado_movements_fiado_account_id_fkey"
)

// AccountInput es el registro del deudor que viaja con el primer movimiento (decisión D1 de
// forma). `updated_at` es la marca de mutación del CLIENTE —el comparador LWW de §7.4— y su
// ausencia vale el mismo reloj que la creación.
type AccountInput struct {
	Name      string
	Phone     *string
	UpdatedAt *time.Time
}

// MovementInput es el movimiento ya parseado del cuerpo de §4.11, sin el tenant ni el usuario:
// esos viajan aparte y vienen SOLO del token (ARCHITECTURE.md:23).
type MovementInput struct {
	// ID es el id que el cliente generó (UUIDv7): el movimiento nace offline.
	ID uuid.UUID
	// AccountID es la cuenta de fiado del deudor.
	AccountID uuid.UUID
	// Account es el registro opcional del deudor: si viaja, la cuenta se crea o se actualiza
	// dentro de la misma transacción (F12).
	Account *AccountInput
	// AmountCents es el monto FIRMADO: cargo positivo, pago negativo. El signo ES el tipo.
	AmountCents int64
	// Note es la nota libre del operador.
	Note *string
	// SoldOn y SaleID son el enlace opcional a la venta: viajan juntos o no viajan.
	SoldOn *time.Time
	SaleID *uuid.UUID
}

// MovementStore es el único camino de datos del movimiento de fiado (patrón de
// internal/catalog/product.go): una interfaz, para que el handler se pruebe sin base.
//
// Devuelve el cuerpo JSON EXACTO del acuse de la decisión 3 de forma: el movimiento resuelto y
// la cuenta con su saldo computado. El []byte es la pieza que el claim almacena y que la
// repetición reenvía byte a byte (docs/DATABASE.md §7.1 Branch A).
type MovementStore interface {
	AppendMovement(ctx context.Context, tenantID uuid.UUID, movement MovementInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error)
}

// movementBody es el movimiento del acuse, con los valores congelados que la fila guardó. Los
// campos opcionales viajan como `null` —nunca ausentes— porque la forma es la del contrato.
type movementBody struct {
	ID             string  `json:"id"`
	TenantID       string  `json:"tenant_id"`
	FiadoAccountID string  `json:"fiado_account_id"`
	AmountCents    int64   `json:"amount_cents"`
	SoldOn         *string `json:"sold_on"`
	SaleID         *string `json:"sale_id"`
	Note           *string `json:"note"`
	CreatedAt      string  `json:"created_at"`
	ReceivedAt     string  `json:"received_at"`
}

// accountBody es la cuenta con su saldo COMPUTADO: no hay columna de saldo, así que el número
// sale de la suma de los movimientos de la misma transacción (§4.12).
type accountBody struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	Name         string  `json:"name"`
	Phone        *string `json:"phone"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	ReceivedAt   string  `json:"received_at"`
	BalanceCents int64   `json:"balance_cents"`
}

// movementAck es el cuerpo completo de la respuesta de §4.11 (decisión 3 de forma).
type movementAck struct {
	Movement movementBody `json:"movement"`
	Account  accountBody  `json:"account"`
}

// upsertAccountSQL registra el deudor y, si la cuenta ya existe, aplica el LWW de §7.4: el
// comparador es `updated_at` de mutación del cliente y el daño está acotado a nombre y
// teléfono. Una mutación más vieja no pisa el nombre confirmado.
const upsertAccountSQL = `
INSERT INTO fiado_accounts (id, tenant_id, name, phone, created_at, updated_at, received_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE
   SET name = EXCLUDED.name,
       phone = EXCLUDED.phone,
       updated_at = EXCLUDED.updated_at
 WHERE EXCLUDED.updated_at > fiado_accounts.updated_at`

// visibleAccountSQL es la barrera de aislamiento REAL de la referencia por cuerpo: el RLS deja
// caer toda fila que no es del token, así que lo que vuelve ES la cuenta del tenant (§5.2).
const visibleAccountSQL = `SELECT ` + accountColumns + ` FROM fiado_accounts WHERE id = $1`

// insertMovementSQL anexa el movimiento firmado. `created_at` y `received_at` comparten el
// mismo instante: el cuerpo de §4.11 no trae created_at y una sola lectura del reloj deja el
// par negocio/auditoría coherente.
const insertMovementSQL = `
INSERT INTO fiado_movements (id, tenant_id, fiado_account_id, amount_cents, sold_on, sale_id, note, created_at, received_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`

// balanceSQL computa el saldo del deudor: la suma firmada del ledger, jamás una columna.
const balanceSQL = `SELECT COALESCE(SUM(amount_cents), 0) FROM fiado_movements WHERE fiado_account_id = $1`

// PostgresMovementStore anexa movimientos sobre el pool de aplicación envuelto en el helper de
// transacción de tenant.
type PostgresMovementStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresMovementStore construye el store sobre el pool de `pos_app`.
func NewPostgresMovementStore(tenants *postgres.TenantPool) *PostgresMovementStore {
	return &PostgresMovementStore{tenants: tenants}
}

// AppendMovement anexa el movimiento: UNA TRANSACCIÓN. El orden interno es el que §7.1 y §7.8
// exigen — reclamar la clave de idempotencia, registrar al deudor si el cuerpo lo trae,
// releer la cuenta por RLS, anexar el movimiento firmado, computar el saldo y completar el
// claim con el acuse como ÚLTIMO paso —. Cualquier paso que falla revierte todo, cuenta y
// claim incluidos: la transacción es todo-o-nada (MD-4).
func (s *PostgresMovementStore) AppendMovement(ctx context.Context, tenantID uuid.UUID, movement MovementInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	if err := guardMovementInputs(tenantID, movement, receivedAt, claim); err != nil {
		return nil, err
	}

	var ack []byte
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		// Branch B ANTES de la escritura: la fila de `idempotency_keys` ES el claim y se gana
		// INSERTANDO sobre la UNIQUE (tenant_id, key) — jamás SELECT-then-INSERT (§7.1).
		claimed, err := synckey.Claim(ctx, tx, tenantID, claim.Key, claim.PayloadHash, receivedAt)
		if err != nil {
			return err
		}
		if claimed.Replay != nil {
			// Branch A, repetición verdadera: la respuesta es el snapshot ALMACENADO tal cual,
			// sin correr NINGÚN camino de negocio (§7.1).
			ack = claimed.Replay
			return nil
		}

		// El registro del deudor viaja con el primer movimiento: el upsert es parte de ESTA
		// transacción, así que un fallo posterior lo revierte con todo lo demás (§7.8).
		accountCreatedAt := receivedAt
		if movement.Account != nil {
			accountUpdatedAt := receivedAt
			if movement.Account.UpdatedAt != nil {
				accountUpdatedAt = *movement.Account.UpdatedAt
				accountCreatedAt = accountUpdatedAt
			}
			if _, err := tx.Exec(ctx, upsertAccountSQL, movement.AccountID, tenantID, movement.Account.Name,
				movement.Account.Phone, accountCreatedAt, accountUpdatedAt, receivedAt); err != nil {
				return fmt.Errorf("upsert the fiado account %s: %w", movement.AccountID, err)
			}
		}

		// La cuenta tiene que ser visible por RLS: la FK de `fiado_movements` corre fuera del
		// alcance del RLS y aprobaría la cuenta de otro tenant. Esta lectura es la barrera de
		// aislamiento REAL de la referencia por cuerpo (§4.1).
		account, err := visibleAccount(ctx, tx, movement.AccountID)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, insertMovementSQL, movement.ID, tenantID, movement.AccountID,
			movement.AmountCents, movement.SoldOn, movement.SaleID, movement.Note, receivedAt); err != nil {
			return classifyMovementError(err, movement)
		}

		balance, err := accountBalance(ctx, tx, movement.AccountID)
		if err != nil {
			return err
		}

		// Complete es el ÚLTIMO paso de la transacción (§7.1 Branch B): el snapshot viaja con
		// el estado 'completed' y el COMMIT los consolida juntos.
		body, err := movementAcknowledgeJSON(tenantID, movement, receivedAt, account, balance)
		if err != nil {
			return err
		}
		if err := synckey.Complete(ctx, tx, claimed.ID, body); err != nil {
			return err
		}
		ack = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ack, nil
}

// visibleAccount lee la cuenta por el CAMINO DE PRODUCCIÓN: dentro del RLS. Un `ErrNoRows` es
// una cuenta que el tenant no tiene, y la sentencia es la misma que nombra el contrato.
func visibleAccount(ctx context.Context, tx postgres.TenantTx, accountID uuid.UUID) (Account, error) {
	var account Account
	err := tx.QueryRow(ctx, visibleAccountSQL, accountID).Scan(&account.ID, &account.TenantID, &account.Name,
		&account.Phone, &account.CreatedAt, &account.UpdatedAt, &account.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrUnknownAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("read the fiado account %s: %w", accountID, err)
	}
	return account, nil
}

// accountBalance computa el saldo del deudor sobre las filas que la transacción ya escribió.
func accountBalance(ctx context.Context, tx postgres.TenantTx, accountID uuid.UUID) (int64, error) {
	var balance int64
	if err := tx.QueryRow(ctx, balanceSQL, accountID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("compute the balance of the fiado account %s: %w", accountID, err)
	}
	return balance, nil
}

// classifyMovementError separa las dos referencias por cuerpo que el contrato nombra de
// cualquier otra violación de la transacción. La venta enlazada ausente y la cuenta que el RLS
// no ve son 422 con su campo; el resto sube tal cual.
func classifyMovementError(err error, movement MovementInput) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != sqlStateForeignKeyViolation {
		return fmt.Errorf("insert the fiado movement %s: %w", movement.ID, err)
	}

	switch pgErr.ConstraintName {
	case saleLinkConstraint:
		return ErrUnknownSale
	case accountLinkConstraint:
		return ErrUnknownAccount
	default:
		return fmt.Errorf("insert the fiado movement %s: %w", movement.ID, err)
	}
}

// sqlStateForeignKeyViolation es la violación de clave ajena de PostgreSQL (23503).
const sqlStateForeignKeyViolation = "23503"

// guardMovementInputs valida las precondiciones de escritura ANTES de abrir la transacción:
// son defectos de programación del llamador, no peticiones erróneas (patrón de
// catalog.guardWrite). El handler ya validó el cuerpo y el encabezado del claim.
func guardMovementInputs(tenantID uuid.UUID, movement MovementInput, receivedAt time.Time, claim synckey.ClaimInput) error {
	switch {
	case tenantID == uuid.Nil:
		return errors.New("the fiado movement store needs the tenant id of the token")
	case movement.ID == uuid.Nil:
		return errors.New("the fiado movement needs the client movement id")
	case movement.AccountID == uuid.Nil:
		return errors.New("the fiado movement needs the fiado account id")
	case movement.AmountCents == 0:
		return errors.New("the fiado movement needs a non-zero amount: the sign is the kind")
	case (movement.SoldOn == nil) != (movement.SaleID == nil):
		return errors.New("the fiado movement sale linkage travels as a pair of nullity")
	case receivedAt.IsZero():
		return errors.New("the fiado movement needs the server receive time")
	case claim.Key == "":
		return errors.New("the fiado movement needs the idempotency key of the client")
	case claim.PayloadHash == "":
		return errors.New("the fiado movement needs the payload hash of the claim")
	case movement.Account != nil && movement.Account.Name == "":
		return errors.New("the fiado account registration needs a name")
	}
	return nil
}

// movementAcknowledgeJSON arma el acuse de la decisión 3 de forma: la forma exacta que el claim
// almacena en response_snapshot para que la repetición devuelva esta MISMA respuesta (§7.1
// Branch A). Los opcionales viajan como `null`, nunca ausentes.
func movementAcknowledgeJSON(tenantID uuid.UUID, movement MovementInput, receivedAt time.Time, account Account, balance int64) ([]byte, error) {
	var soldOn, saleID *string
	if movement.SoldOn != nil {
		value := movement.SoldOn.Format(businessDayFormat)
		soldOn = &value
	}
	if movement.SaleID != nil {
		value := movement.SaleID.String()
		saleID = &value
	}

	ack := movementAck{
		Movement: movementBody{
			ID:             movement.ID.String(),
			TenantID:       tenantID.String(),
			FiadoAccountID: movement.AccountID.String(),
			AmountCents:    movement.AmountCents,
			SoldOn:         soldOn,
			SaleID:         saleID,
			Note:           movement.Note,
			CreatedAt:      rfcTime(receivedAt),
			ReceivedAt:     rfcTime(receivedAt),
		},
		Account: accountBody{
			ID:           account.ID.String(),
			TenantID:     account.TenantID.String(),
			Name:         account.Name,
			Phone:        account.Phone,
			CreatedAt:    rfcTime(account.CreatedAt),
			UpdatedAt:    rfcTime(account.UpdatedAt),
			ReceivedAt:   rfcTime(account.ReceivedAt),
			BalanceCents: balance,
		},
	}

	body, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("marshal the fiado movement acknowledgement: %w", err)
	}
	return body, nil
}

// businessDayFormat es el formato de `sold_on`: fecha de negocio, ninguna hora.
const businessDayFormat = "2006-01-02"

// rfcTime es el formato RFC3339 en UTC del contrato para todo instante de superficie
// (docs/BACKEND.md §4.3, igual que internal/catalog y internal/ledger).
func rfcTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}
