package cashclose

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

// El cierre de caja (docs/BACKEND.md §4.13): la proyección del ledger por método, congelada
// en `cash_close_counts.expected_cents`, más lo que el cajero contó. Todo dentro de la
// transacción de tenant del pool de `pos_app`, RLS de por medio.
//
// Dos invariantes mandan sobre este archivo:
//
//   - El cierre NO es un segundo contador. `expected_cents` se computa con la fórmula F8
//     sobre `sales` en el momento del cierre y queda congelado; `difference_cents` se
//     computa al acusar y NUNCA se almacena (docs/DATABASE.md §4 `cash_close_counts`).
//   - Un cierre duplicado en cola es un no-op SILENCIOSO: prevalece el cierre existente y
//     el duplicado se confirma con la proyección existente, HTTP 200, sin error para el
//     cajero (F38, docs/DATABASE.md §7.7).
//
// El servidor no recalcula dinero de venta: lo esperado es una proyección de
// `sales.total_cents`, jamás una suma de líneas (§2.3, MD-7).

// paymentMethodsInOrder es el orden DETERMINISTA del acuse y de las seis filas del cierre:
// el mismo orden del CHECK `cash_close_counts_payment_method_chk` (D11). El acuse tiene que
// ser reproducible byte a byte para que la repetición idempotente lo reenvíe tal cual, así
// que el orden no puede depender del orden del cuerpo ni del plan de la consulta.
var paymentMethodsInOrder = []string{"cash", "yape", "plin", "card", "other", "fiado"}

// paymentMethods es el conjunto cerrado de métodos del CHECK: la superficie rechaza lo que
// la base rechazaría, con un 422 que nombra el campo en lugar de un error de restricción.
var paymentMethods = map[string]bool{
	"cash":  true,
	"yape":  true,
	"plin":  true,
	"card":  true,
	"other": true,
	"fiado": true,
}

// fiadoMethod es el método cuya diferencia es informacional por diseño (D11): lo esperado
// es el crédito extendido del día y lo contado siempre es cero, así que la diferencia nunca
// es un faltante y viaja como null.
const fiadoMethod = "fiado"

// businessDayFormat es el formato de `closed_on`: fecha de negocio, ninguna hora.
const businessDayFormat = "2006-01-02"

// duplicateCloseConstraint es la UNIQUE que hace el cierre insert-once (D10). Nombrarla en
// la resolución del 23505 es lo que separa «este cierre ya existe» de cualquier otra
// violación de unicidad de la transacción.
const duplicateCloseConstraint = "cash_closes_tenant_closed_on_key"

// Un savepoint PROPIO, distinto del `synckey_claim` que abre el claim: la resolución del
// duplicado revierte la escritura de negocio sin tocar el claim reclamado, y un nombre
// ajeno obligaría a compartir el ciclo de vida de otro dueño (docs/DATABASE.md §7.1).
const (
	closeSavepoint       = "cashclose_insert"
	closeSavepointSQL    = `SAVEPOINT ` + closeSavepoint
	closeRollbackSQL     = `ROLLBACK TO SAVEPOINT ` + closeSavepoint
	sqlStateLockNotAvail = "55P03"
	sqlStateUniqueViol   = "23505"
)

// CountInput es UNA fila del cierre: el método y lo que el cajero contó. El
// `expected_cents` NO viaja acá — lo proyecta el servidor.
type CountInput struct {
	PaymentMethod string
	CountedCents  int64
}

// CloseInput es el cierre ya parseado del cuerpo, sin el tenant ni el usuario: esos viajan
// aparte y vienen SOLO del token (ARCHITECTURE.md:23).
type CloseInput struct {
	// ID es el id del cierre que generó el cliente (UUIDv7).
	ID uuid.UUID
	// ClosedOn es la fecha de negocio que se cierra: la que elige la porción del ledger que
	// se proyecta. El retraso de sync nunca la mueve (docs/DATABASE.md §4 `cash_closes`).
	ClosedOn time.Time
	// Counts son EXACTAMENTE seis filas, una por método, en el orden fijo del contrato.
	Counts []CountInput
}

// CashCloseStore es el único camino de datos del cierre (patrón de internal/ledger): una
// interfaz, para que el handler se pruebe sin base.
//
// Devuelve el cuerpo JSON EXACTO del acuse de §4.13: la cabecera del cierre y la proyección
// por método con su diferencia computada. El []byte es la pieza que el claim de
// idempotencia reenvía byte a byte en la repetición (docs/DATABASE.md §7.1 Branch A).
type CashCloseStore interface {
	Close(ctx context.Context, tenantID, userID uuid.UUID, close CloseInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error)
}

// acknowledgedCount es una fila del acuse: lo esperado congelado, lo contado y la
// diferencia computada. `DifferenceCents` es puntero para que el fiado viaje como `null`
// —su diferencia es informacional y nunca un faltante (D11)—.
type acknowledgedCount struct {
	PaymentMethod   string `json:"payment_method"`
	ExpectedCents   int64  `json:"expected_cents"`
	CountedCents    int64  `json:"counted_cents"`
	DifferenceCents *int64 `json:"difference_cents"`
}

// acknowledgedClose es la cabecera congelada del cierre.
type acknowledgedClose struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	ClosedOn       string `json:"closed_on"`
	ClosedByUserID string `json:"closed_by_user_id"`
	CreatedAt      string `json:"created_at"`
	ReceivedAt     string `json:"received_at"`
}

// closeAck es el cuerpo completo de la respuesta de §4.13. La forma es la de la decisión 4
// del documento del sprint.
type closeAck struct {
	CashClose acknowledgedClose   `json:"cash_close"`
	Counts    []acknowledgedCount `json:"counts"`
}

// closeSnapshot es la cabecera leída de la base —o la recién escrita—, ya en la forma del
// acuse. Un solo tipo para el camino fresco y el duplicado: la respuesta del no-op es la
// del cierre que existe, no una versión recalculada.
type closeSnapshot struct {
	id             string
	closedOn       string
	closedByUserID string
	createdAt      time.Time
	receivedAt     time.Time
}

// insertCloseSQL escribe la cabecera del cierre. created_at y received_at reciben el MISMO
// instante: el cuerpo de §4.13 no trae una marca del cliente, así que la pone el servidor y
// una sola lectura del reloj deja el par coherente (docs/DATABASE.md §2.5).
const insertCloseSQL = `
INSERT INTO cash_closes (id, tenant_id, closed_on, closed_by_user_id, created_at, received_at)
VALUES ($1, $2, $3, $4, $5, $5)`

// insertCountSQL escribe una fila del desglose: hija de `cash_closes`, comparte su
// transacción y no lleva received_at propio (MD-6).
const insertCountSQL = `
INSERT INTO cash_close_counts (cash_close_id, payment_method, tenant_id, expected_cents, counted_cents, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`

// expectedByMethodSQL es la fórmula F8 del contrato: la suma FIRMADA del ledger por método
// para una fecha de negocio. La anulación resta porque su fila espeja el total POSITIVO del
// original y la negación vive acá, nunca en el total almacenado (docs/DATABASE.md §4
// `sales`, F8). El CAST a bigint es el tipo del contrato (BIGINT en centavos, §2.3): SUM
// sobre BIGINT devuelve NUMERIC y lo que se congela es un entero.
const expectedByMethodSQL = `
SELECT payment_method,
       COALESCE(SUM(CASE kind WHEN 'sale' THEN total_cents WHEN 'void' THEN -total_cents END), 0)::bigint
FROM sales
WHERE tenant_id = $1 AND sold_on = $2
GROUP BY payment_method`

// existingCloseSQL lee el cierre que ganó el duplicado. El WHERE es la UNIQUE de D10 —el
// mismo par que rechazó el INSERT— y el RLS de tenant acota la búsqueda al cierre del token.
const existingCloseSQL = `
SELECT id::text, closed_on::text, closed_by_user_id::text, created_at, received_at
FROM cash_closes
WHERE tenant_id = $1 AND closed_on = $2`

// existingCountsSQL lee el desglose del cierre existente; el orden lo fija Go, no la base.
const existingCountsSQL = `
SELECT payment_method, expected_cents, counted_cents
FROM cash_close_counts
WHERE cash_close_id = $1`

// PostgresCashCloseStore cierra cajas sobre el pool de aplicación envuelto en el helper de
// transacción de tenant.
type PostgresCashCloseStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresCashCloseStore construye el store sobre el pool de `pos_app`.
func NewPostgresCashCloseStore(tenants *postgres.TenantPool) *PostgresCashCloseStore {
	return &PostgresCashCloseStore{tenants: tenants}
}

// Close cierra la caja: UNA TRANSACCIÓN. El orden interno es el que §4.13 y §7.1 exigen —
// reclamar la clave de idempotencia, insertar la cabecera del cierre, congelar la
// proyección F8 por método en las seis filas del desglose, y completar el claim con el
// acuse como ÚLTIMO paso —. Cualquier paso que falla revierte todo, claim incluido: la
// transacción es todo-o-nada (MD-4).
//
// El duplicado de fecha (F38) se resuelve DENTRO de la misma transacción: la UNIQUE de D10
// rechaza el segundo INSERT, el savepoint propio devuelve la transacción limpia, y la
// respuesta es el cierre que ya existe —nunca un error visible—.
func (s *PostgresCashCloseStore) Close(ctx context.Context, tenantID, userID uuid.UUID, close CloseInput, receivedAt time.Time, claim synckey.ClaimInput) ([]byte, error) {
	if err := guardCloseInputs(tenantID, userID, close, receivedAt, claim); err != nil {
		return nil, err
	}

	var ack []byte
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		// Branch B ANTES de la escritura: la fila de `idempotency_keys` ES el claim y se
		// gana INSERTANDO sobre la UNIQUE (tenant_id, key) — jamás SELECT-then-INSERT (§7.1).
		claimed, err := synckey.Claim(ctx, tx, tenantID, claim.Key, claim.PayloadHash, receivedAt)
		if err != nil {
			return err
		}
		if claimed.Replay != nil {
			// Branch A, repetición verdadera: la respuesta es el snapshot ALMACENADO tal
			// cual, sin correr NINGÚN camino de negocio (§7.1).
			ack = claimed.Replay
			return nil
		}

		// El savepoint propio de esta escritura: el INSERT de la cabecera puede chocar con
		// la UNIQUE de D10, y volver al savepoint deja la transacción limpia para leer el
		// ganador sin arrastrar un claim abortado.
		if _, err := tx.Exec(ctx, closeSavepointSQL); err != nil {
			return fmt.Errorf("open the cash close savepoint: %w", err)
		}

		snapshot, counts, err := writeClose(ctx, tx, tenantID, userID, close, receivedAt)
		if err != nil {
			return err
		}

		body, err := acknowledgeJSON(tenantID, snapshot, counts)
		if err != nil {
			return err
		}
		// Complete es el ÚLTIMO paso de la transacción (§7.1 Branch B): el snapshot viaja
		// con el estado 'completed' y el COMMIT los consolida juntos — un crash antes
		// revierte el claim y el cierre a la vez.
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

// writeClose resuelve la escritura de negocio y devuelve el cierre que quedó vigente: el
// recién escrito, o el que ganó si esta petición era el duplicado de fecha.
func writeClose(ctx context.Context, tx postgres.TenantTx, tenantID, userID uuid.UUID, close CloseInput, receivedAt time.Time) (closeSnapshot, []acknowledgedCount, error) {
	_, insertErr := tx.Exec(ctx, insertCloseSQL, close.ID, tenantID, close.ClosedOn, userID, receivedAt)
	if insertErr != nil {
		var pgErr *pgconn.PgError
		if !errors.As(insertErr, &pgErr) {
			return closeSnapshot{}, nil, fmt.Errorf("insert the cash close %s: %w", close.ID, insertErr)
		}

		switch {
		case pgErr.Code == sqlStateLockNotAvail:
			// El cierre ganador de la MISMA fecha todavía no confirmó: la espera la acota
			// el lock timeout que el claim fijó, y la superficie responde 409 + Retry-After.
			return closeSnapshot{}, nil, synckey.ErrInFlight
		case pgErr.Code == sqlStateUniqueViol && pgErr.ConstraintName == duplicateCloseConstraint:
			return existingClose(ctx, tx, tenantID, close.ClosedOn)
		default:
			return closeSnapshot{}, nil, fmt.Errorf("insert the cash close %s: %w", close.ID, insertErr)
		}
	}

	// El cierre es NUEVO: se congela la proyección del ledger de esa fecha de negocio.
	expected, err := expectedByMethod(ctx, tx, tenantID, close.ClosedOn)
	if err != nil {
		return closeSnapshot{}, nil, err
	}

	counts := make([]acknowledgedCount, 0, len(paymentMethodsInOrder))
	for _, method := range paymentMethodsInOrder {
		expectedCents := expected[method]
		countedCents, ok := countedByMethod(close.Counts, method)
		if !ok {
			return closeSnapshot{}, nil, fmt.Errorf("the cash close has no count for the payment method %q", method)
		}

		if _, err := tx.Exec(ctx, insertCountSQL, close.ID, method, tenantID, expectedCents, countedCents, receivedAt); err != nil {
			return closeSnapshot{}, nil, fmt.Errorf("insert the cash close count of %q: %w", method, err)
		}
		counts = append(counts, acknowledgedCount{
			PaymentMethod:   method,
			ExpectedCents:   expectedCents,
			CountedCents:    countedCents,
			DifferenceCents: differenceCents(method, expectedCents, countedCents),
		})
	}

	return closeSnapshot{
		id:             close.ID.String(),
		closedOn:       close.ClosedOn.Format(businessDayFormat),
		closedByUserID: userID.String(),
		createdAt:      receivedAt,
		receivedAt:     receivedAt,
	}, counts, nil
}

// existingClose resuelve F38: la fecha ya tiene cierre, así que el que existe prevalece y
// esta petición se confirma con SU proyección. Primero se revierte la escritura fallida al
// savepoint propio, y después se lee el ganador por el mismo camino RLS.
func existingClose(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, closedOn time.Time) (closeSnapshot, []acknowledgedCount, error) {
	if _, err := tx.Exec(ctx, closeRollbackSQL); err != nil {
		return closeSnapshot{}, nil, fmt.Errorf("return to the cash close savepoint: %w", err)
	}

	var snapshot closeSnapshot
	var id uuid.UUID
	if err := tx.QueryRow(ctx, existingCloseSQL, tenantID, closedOn).
		Scan(&id, &snapshot.closedOn, &snapshot.closedByUserID, &snapshot.createdAt, &snapshot.receivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// La fila que chocó con la UNIQUE desapareció antes de la lectura: con retención
			// de 400 días no hay camino para eso, así que es un fallo de la base.
			return closeSnapshot{}, nil, fmt.Errorf("read the existing cash close of %s: the row is gone: %w", closedOn.Format(businessDayFormat), err)
		}
		return closeSnapshot{}, nil, fmt.Errorf("read the existing cash close of %s: %w", closedOn.Format(businessDayFormat), err)
	}
	snapshot.id = id.String()

	stored, err := existingCounts(ctx, tx, id)
	if err != nil {
		return closeSnapshot{}, nil, err
	}

	// El acuse del duplicado es el del cierre que existe, con la MISMA forma y el MISMO
	// orden determinista: el cliente no puede distinguirlo de un cierre fresco.
	counts := make([]acknowledgedCount, 0, len(paymentMethodsInOrder))
	for _, method := range paymentMethodsInOrder {
		count, ok := stored[method]
		if !ok {
			return closeSnapshot{}, nil, fmt.Errorf("the existing cash close %s carries no count for %q", id, method)
		}
		counts = append(counts, count)
	}
	return snapshot, counts, nil
}

// existingCounts lee el desglose del cierre existente y arma el acuse por método. La
// diferencia se computa otra vez —nunca se almacenó—, así que el duplicado devuelve
// exactamente lo que devolvió el ganador.
func existingCounts(ctx context.Context, tx postgres.TenantTx, closeID uuid.UUID) (map[string]acknowledgedCount, error) {
	rows, err := tx.Query(ctx, existingCountsSQL, closeID)
	if err != nil {
		return nil, fmt.Errorf("read the existing cash close counts: %w", err)
	}
	defer rows.Close()

	stored := map[string]acknowledgedCount{}
	for rows.Next() {
		var (
			method   string
			expected int64
			counted  int64
		)
		if err := rows.Scan(&method, &expected, &counted); err != nil {
			return nil, fmt.Errorf("read the existing cash close counts: %w", err)
		}
		stored[method] = acknowledgedCount{
			PaymentMethod:   method,
			ExpectedCents:   expected,
			CountedCents:    counted,
			DifferenceCents: differenceCents(method, expected, counted),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate the existing cash close counts: %w", err)
	}
	return stored, nil
}

// expectedByMethod proyecta la fórmula F8 por método para la fecha de negocio. Los métodos
// sin filas del ledger NO aparecen en el resultado y valen cero: un método sin ventas es
// una fila del despiece con esperado cero, no una fila ausente (las seis filas son el
// contrato).
func expectedByMethod(ctx context.Context, tx postgres.TenantTx, tenantID uuid.UUID, closedOn time.Time) (map[string]int64, error) {
	rows, err := tx.Query(ctx, expectedByMethodSQL, tenantID, closedOn)
	if err != nil {
		return nil, fmt.Errorf("project the cash close expected amounts: %w", err)
	}
	defer rows.Close()

	expected := map[string]int64{}
	for rows.Next() {
		var (
			method string
			cents  int64
		)
		if err := rows.Scan(&method, &cents); err != nil {
			return nil, fmt.Errorf("project the cash close expected amounts: %w", err)
		}
		expected[method] = cents
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate the cash close expected amounts: %w", err)
	}
	return expected, nil
}

// countedByMethod busca el conteo de un método en el cuerpo ya validado.
func countedByMethod(counts []CountInput, method string) (int64, bool) {
	for _, count := range counts {
		if count.PaymentMethod == method {
			return count.CountedCents, true
		}
	}
	return 0, false
}

// differenceCents computa la diferencia del contrato (counted − expected) y NUNCA se
// almacena (docs/DATABASE.md §4 `cash_close_counts`). En el fiado viaja `null`: su
// diferencia es informacional por diseño y no es un faltante (D11).
func differenceCents(method string, expected, counted int64) *int64 {
	if method == fiadoMethod {
		return nil
	}
	difference := counted - expected
	return &difference
}

// acknowledgeJSON arma el acuse de §4.13: la forma exacta que el claim almacenará en
// response_snapshot para que la repetición devuelva esta MISMA respuesta (§7.1 Branch A).
func acknowledgeJSON(tenantID uuid.UUID, snapshot closeSnapshot, counts []acknowledgedCount) ([]byte, error) {
	body, err := json.Marshal(closeAck{
		CashClose: acknowledgedClose{
			ID:             snapshot.id,
			TenantID:       tenantID.String(),
			ClosedOn:       snapshot.closedOn,
			ClosedByUserID: snapshot.closedByUserID,
			CreatedAt:      rfcTime(snapshot.createdAt),
			ReceivedAt:     rfcTime(snapshot.receivedAt),
		},
		Counts: counts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal the cash close acknowledgement: %w", err)
	}
	return body, nil
}

// guardCloseInputs valida las precondiciones de escritura ANTES de abrir la transacción:
// son defectos de programación del llamador, no peticiones erróneas. El handler ya validó
// el cuerpo y el encabezado del claim; esto cierra el tipo.
func guardCloseInputs(tenantID, userID uuid.UUID, close CloseInput, receivedAt time.Time, claim synckey.ClaimInput) error {
	switch {
	case tenantID == uuid.Nil:
		return errors.New("the cash close store needs the tenant id of the token")
	case userID == uuid.Nil:
		return errors.New("the cash close store needs the user id of the token")
	case close.ID == uuid.Nil:
		return errors.New("the cash close needs the client close id")
	case close.ClosedOn.IsZero():
		return errors.New("the cash close needs the client business date")
	case len(close.Counts) != len(paymentMethodsInOrder):
		return fmt.Errorf("the cash close needs exactly %d counts, one per payment method", len(paymentMethodsInOrder))
	case receivedAt.IsZero():
		return errors.New("the cash close needs the server receive time")
	case claim.Key == "":
		return errors.New("the cash close needs the idempotency key of the client")
	case claim.PayloadHash == "":
		return errors.New("the cash close needs the payload hash of the claim")
	}

	seen := map[string]bool{}
	for _, count := range close.Counts {
		if !paymentMethods[count.PaymentMethod] {
			return fmt.Errorf("the cash close carries the unknown payment method %q", count.PaymentMethod)
		}
		if seen[count.PaymentMethod] {
			return fmt.Errorf("the cash close carries the payment method %q twice", count.PaymentMethod)
		}
		seen[count.PaymentMethod] = true
		if count.CountedCents < 0 {
			return fmt.Errorf("the cash close carries a negative counted amount for %q", count.PaymentMethod)
		}
		if count.PaymentMethod == fiadoMethod && count.CountedCents != 0 {
			return errors.New("the cash close carries a non-zero counted amount for fiado")
		}
	}
	return nil
}

// rfcTime es el formato RFC3339 en UTC del contrato para todo instante de superficie
// (docs/BACKEND.md §4.3, igual que internal/ledger).
func rfcTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}
