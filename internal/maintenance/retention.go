package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Este archivo implementa el job de retención de claves de idempotencia (Sprint 6, W6-DB-1;
// docs/DATABASE.md §7.2, decisión D10 de odd/tasks/sprint-6-offline-sync.md).
//
// La política es un techo duro, no un objetivo blando: una clave `completed` más vieja que
// la ventana se borra sin archivo y sin bandera de borrado lógico. La ventana de 400 días
// excede la edad plausible de cualquier cola de cliente.
//
// Tres invariantes que este job sostiene:
//
//   - corre con el rol de mantenimiento `pos_maintenance`, cuyo ÚNICO DELETE de datos es
//     este, sobre esta tabla y ninguna otra (§5.5, §7.2);
//   - NUNCA toca filas `in_flight`: una operación en vuelo no tiene snapshot y borrarla
//     dejaría al cliente sin poder reintentar (el CHECK acoplado al estado de §4 la
//     protege, y el WHERE la respeta);
//   - la edad se mide sobre `created_at`, que es la marca que la tabla real tiene y la que
//     §4 declara como la que conduce la ventana de retención. La prosa de §7.1-§7.2 nombra
//     un `completed_at` que no existe en el esquema ejecutable (ver la nota de
//     docs/BACKEND.md §6.2 y la corrección registrada en el documento del sprint).

// RetentionWindow es la ventana de retención de §7.2: 400 días.
const RetentionWindow = 400 * 24 * time.Hour

// RetentionResult informa el trabajo del job. `Deleted` es el conteo de filas que
// efectivamente desaparecieron: es la evidencia de la corrida.
type RetentionResult struct {
	// Deleted es el número de claves completadas y vencidas que el job borró.
	Deleted int64
}

// DeleteExpiredClaims corre la retención sobre la base indicada. El llamador decide con qué
// DSN se conecta: la operación de producción lo hace con `pos_maintenance` (split F9), que
// es el único rol con el DELETE de esta tabla.
func DeleteExpiredClaims(ctx context.Context, db *sql.DB) (RetentionResult, error) {
	return DeleteExpiredClaimsBefore(ctx, db, time.Now())
}

// DeleteExpiredClaimsBefore es la retención anclada a un instante explícito, para que la
// prueba pueda fijar el borde sin depender del reloj de la máquina.
func DeleteExpiredClaimsBefore(ctx context.Context, db *sql.DB, now time.Time) (RetentionResult, error) {
	if db == nil {
		return RetentionResult{}, errors.New("the retention job needs a database handle")
	}

	// `make_interval(days => n)` fija la ventana en días exactos desde el instante dado, que
	// es lo que §7.2 mide: 400 días después de la marca de la clave.
	interval := fmt.Sprintf("%d days", int(RetentionWindow/(24*time.Hour)))
	result, err := db.ExecContext(ctx,
		`DELETE FROM idempotency_keys
		 WHERE state = 'completed'
		   AND created_at < $1::timestamptz - $2::interval`,
		now.UTC(), interval)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("delete the expired idempotency keys: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("count the deleted idempotency keys: %w", err)
	}
	return RetentionResult{Deleted: deleted}, nil
}
