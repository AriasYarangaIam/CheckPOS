// El protocolo de claim de idempotencia (docs/DATABASE.md §7.1, docs/BACKEND.md §6.1):
// la fila de `idempotency_keys` ES el claim de cada POST encolable, y este paquete es su
// dueño (ARCHITECTURE.md:31 — ningún módulo escribe las tablas de otro). El consumidor
// (la venta hoy, fiado en Semana 7) reclama el claim dentro de SU transacción de negocio
// y lo completa como último paso de la misma: claim y escritura de negocio nacen o mueren
// juntos con el COMMIT (MD-4), así que un error revierte la clave y nada queda reclamado
// a medias.
//
// Las tres ramas de §7.1 sobre (tenant_id, key) con la UNIQUE del §4:
//
//   - Branch B (clave nueva): el INSERT inserta una fila. La transacción sigue con la
//     escritura de negocio y termina con el Complete + COMMIT todo-o-nada.
//   - Branch A (clave repetida): el perdedor de la UNIQUE es el ganador CONFIRMADO de
//     antes. Se relee la fila y se compara el hash del payload: igual ⇒ repetición
//     verdadera, la respuesta es el snapshot ALMACENADO sin ninguna escritura de
//     negocio; distinto ⇒ la misma clave se usó para otro payload (ErrPayloadMismatch).
//   - Branch C (duplicado concurrente): el perdedor bloquea en la restricción única
//     mientras el ganador está en vuelo. El lock timeout —perilla de §7.1— acota la
//     espera: 55P03 ⇒ ErrInFlight, y la superficie responde 409 con Retry-After.
//
// La fila del claim se reclama INSERTANDO, JAMÁS con un SELECT seguido de un INSERT —
// eso sería una carrera, no una guarda (aceptación del sprint, §7.1). Todo el camino
// corre por la transacción de tenant del llamador, cuyo contexto de RLS es el que
// separa las claves de un tenant de las de otro.
package synckey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Sentinelas de las ramas del protocolo: el llamador las reconoce con errors.Is para
// mapearlas a la superficie (422 idempotency.payload_mismatch, 409 + Retry-After). Son
// contratos del CLAIM, sin ninguna palabra de la venta: fiado en Semana 7 reclama con el
// MISMO par (docs/DATABASE.md §7.6).
var (
	// ErrPayloadMismatch es Branch A con hashes distintos: la clave ya habló por otro
	// payload, y una segunda escritura de negocio nunca corre.
	ErrPayloadMismatch = errors.New("the idempotency key was already used with a different payload")

	// ErrInFlight es Branch C: el ganador de la clave no confirmó su transacción dentro
	// de la ventana del lock timeout, así que todavía no existe snapshot que reenviar.
	ErrInFlight = errors.New("the idempotency key is already claimed by an in-flight operation")
)

// El INSERT del claim corre con dos herramientas de transacción puestas ANTES de él: el
// lock timeout acota la espera detrás de la fila ganadora (§7.1 Branch C — «el lock
// timeout es la perilla del esquema que acota la ventana de duplicado concurrente, no
// un timeout de negocio») y el savepoint permite el re-read del Branch A sin abortar la
// transacción del perdedor.
const (
	// claimLockTimeout es la ventana de espera de Branch C. El presupuesto que manda
	// (docs/BACKEND.md §8.1) es la venta entera en menos de 3 segundos: acá va el MISMO
	// techo, para que el perdedor nunca espere más que el negocio entero.
	claimLockTimeout = "3s"

	// claimSavepoint es el nombre del savepoint que rodea el INSERT del claim.
	claimSavepoint = "synckey_claim"
)

// setLockTimeoutSQL es la primera sentencia de la transacción del claim: SET LOCAL ligado
// a ESTA transacción, con el techo de §7.1. %s lleva el valor de claimLockTimeout.
const setLockTimeoutSQL = `SET LOCAL lock_timeout = '%s'`

// claimSavepointSQL abre el savepoint que rodea el INSERT del claim.
const claimSavepointSQL = `SAVEPOINT ` + claimSavepoint

// claimRollbackSQL devuelve la transacción limpia después del error de la UNIQUE, para
// el re-read de Branch A. Es sentencia sin argumentos: pgx corre el protocolo simple,
// y un ROLLBACK TO SAVEPOINT recorre bien dentro de la tx.
const claimRollbackSQL = `ROLLBACK TO SAVEPOINT ` + claimSavepoint

// insertClaimSQL es el INSERT del claim: la forma de ganar la clave sobre la UNIQUE
// (tenant_id, key) del §4. Nace en_flight, con su payload_hash congelado y la marca del
// claim (docs/DATABASE.md §4 `idempotency_keys`).
const insertClaimSQL = `
INSERT INTO idempotency_keys (id, tenant_id, key, payload_hash, state, created_at)
VALUES ($1, $2, $3, $4, 'in_flight', $5)`

// reReadClaimSQL es la lectura de Branch A: la fila del ganador CONFIRMADO, con el hash
// que ya firmó y el snapshot de su respuesta. El WHERE es solo la clave: el RLS de
// tenant acota la búsqueda al CLAIM del token.
const reReadClaimSQL = `SELECT payload_hash, state, response_snapshot FROM idempotency_keys WHERE key = $1`

// completeClaimSQL marca el claim completado con el snapshot de la respuesta, por id —
// la fila que el propio Claim insertó, sin correr la UNIQUE otra vez. El §2.9 manda
// response_snapshot como TEXT: el snapshot viaja como string, jamás como bytea.
const completeClaimSQL = `UPDATE idempotency_keys SET state = 'completed', response_snapshot = $2 WHERE id = $1`

// ClaimResult es el desenlace del claim para el llamador.
type ClaimResult struct {
	// Fresh es la Branch B: la clave se ganó con el INSERT y el negocio corre ahora.
	Fresh bool
	// ID es el id de la fila recién insertada: es lo que Complete completa. Nil uuid en
	// Branch A.
	ID uuid.UUID
	// Replay es el snapshot ALMACENADO de la repetición verdadera (Branch A con hashes
	// iguales): la respuesta EXACTA que el ganador confirmó, byte a byte. Nil en Branch B.
	Replay []byte
}

// ClaimInput es la pareja que identifica el claim de una petición: la clave del
// encabezado Idempotency-Key y el hash del payload RAW. No sabe de qué negocio viene —
// fiado en Semana 7 reclama con el mismo par —.
type ClaimInput struct {
	// Key es el valor del encabezado Idempotency-Key del cliente.
	Key string
	// PayloadHash es el hash del payload, el que arma PayloadHash.
	PayloadHash string
}

// PayloadHash es el hash de comparación del claim (docs/BACKEND.md §6.2): SHA-256 en hex
// (docs/DATABASE.md §2.10) sobre la clave de idempotencia y el cuerpo serializado de la
// petición, separados por un byte NUL para que ninguna concatenación ambigua pueda
// fabricar un par (clave, cuerpo) falso.
//
// Divergencia registrada con el §6.2 (que pide solo el cuerpo): el (tenant_id, key) de la
// fila ya es único, de modo que incluir la clave es redundante para el ENCUENTRO de la
// fila; el hash con la clave adentro es redundante para la COMPARACIÓN de Branch A y más
// estricto. Queda fijado así por decisión del writer BE (2026-10-19) y vale como el
// algoritmo congelado del claim.
func PayloadHash(key string, body []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(key))
	_, _ = digest.Write([]byte{0x00})
	_, _ = digest.Write(body)
	return hex.EncodeToString(digest.Sum(nil))
}

// Claim reclama la clave (tenant_id, key) DENTRO de la transacción de tenant del
// llamador: el lock timeout de Branch C, el savepoint, y el INSERT que GANA la UNIQUE
// — nunca un SELECT seguido de un INSERT (§7.1). Devuelve la rama del protocolo:
//
//   - ClaimResult.Fresh (Branch B): el negocio corre ahora y termina con Complete sobre
//     el id devuelto, dentro de la MISMA transacción.
//   - ClaimResult.Replay (Branch A igual): la respuesta almacenada byte a byte, sin negocio.
//   - ErrPayloadMismatch (Branch A distinto): la clave fue de otro payload.
//   - ErrInFlight (Branch C): el ganador no confirmó dentro del lock timeout.
//
// createdAt es la marca del claim: el llamador pasa su única lectura de reloj del
// cierre, así que la fila del claim y el negocio nacen con la MISMA marca (§2.5).
func Claim(ctx context.Context, claims postgres.TenantTx, tenantID uuid.UUID, key, hash string, createdAt time.Time) (ClaimResult, error) {
	if claims == nil {
		return ClaimResult{}, errors.New("the idempotency claim needs the caller's tenant transaction")
	}
	switch {
	case tenantID == uuid.Nil:
		return ClaimResult{}, errors.New("the idempotency claim needs the tenant id of the token")
	case key == "":
		return ClaimResult{}, errors.New("the idempotency claim needs the client key")
	case hash == "":
		return ClaimResult{}, errors.New("the idempotency claim needs the payload hash")
	}

	// Primera sentencia: el techo de espera de Branch C (§7.1). Es SET LOCAL: solo esta
	// transacción lleva el techo, y el COMMIT lo revierte.
	if _, err := claims.Exec(ctx, fmt.Sprintf(setLockTimeoutSQL, claimLockTimeout)); err != nil {
		return ClaimResult{}, fmt.Errorf("bound the claim wait: %w", err)
	}
	if _, err := claims.Exec(ctx, claimSavepointSQL); err != nil {
		return ClaimResult{}, fmt.Errorf("open the claim savepoint: %w", err)
	}

	// El INSERT es el método de ganancia: la fila ES el claim (§7.1). Nada se leyó antes
	// — el SELECT-then-INSERT es la carrera que el contrato prohibe.
	claimID, err := uuid.NewV7()
	if err != nil {
		return ClaimResult{}, fmt.Errorf("generate the claim id: %w", err)
	}
	if _, err := claims.Exec(ctx, insertClaimSQL, claimID, tenantID, key, hash, createdAt); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			return ClaimResult{}, fmt.Errorf("claim the key %s: %w", key, err)
		}

		switch pgErr.Code {
		case "55P03":
			// Branch C perdedor: el ganador no confirmó dentro de la ventana del lock
			// timeout. La transacción del perdedor queda abortada y el rollback del
			// llamador la cierra — nada más escribe a partir de acá.
			return ClaimResult{}, ErrInFlight
		case "23505":
			// Branch A: la ganadora confirmó antes que nuestra espera terminara. El
			// error de la UNIQUE abortó la transacción — el rollback al savepoint la
			// deja limpia para el re-read.
			if _, err := claims.Exec(ctx, claimRollbackSQL); err != nil {
				return ClaimResult{}, fmt.Errorf("return to the claim savepoint: %w", err)
			}
			return replayOrMismatch(ctx, claims, key, hash)
		default:
			return ClaimResult{}, fmt.Errorf("claim the key %s: %w", key, err)
		}
	}

	return ClaimResult{ID: claimID, Fresh: true}, nil
}

// replayOrMismatch es la mitad que decide Branch A después del error de la UNIQUE: la
// fila del ganador confirmado separa la repetición verdadera del conflicto de payload.
func replayOrMismatch(ctx context.Context, claims postgres.TenantTx, key, hash string) (ClaimResult, error) {
	var (
		storedHash   string
		state        string
		snapshotText *string
	)
	if err := claims.QueryRow(ctx, reReadClaimSQL, key).Scan(&storedHash, &state, &snapshotText); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// El ganador confirmó hace un instante (por eso nuestro INSERT chocó) y la
			// fila desapareció antes de nuestra lectura: no debería existir un camino
			// con retención de 400 días y lo tratamos como un fallo de la base.
			return ClaimResult{}, fmt.Errorf("re-read the claim of %s: the winning row is gone: %w", key, err)
		}
		return ClaimResult{}, fmt.Errorf("re-read the claim of %s: %w", key, err)
	}

	// La CHECK acoplada al estado del §4 garantiza una fila confirmada SIEMPRE completed
	// y con snapshot (F4). Una in_flight vista acá es un ganador que confirmó DESPUÉS de
	// nuestra ventana de lectura: no hay snapshot que servir — Branch C puro.
	switch {
	case state == "in_flight":
		return ClaimResult{}, ErrInFlight
	case state == "completed" && storedHash == hash && snapshotText != nil:
		return ClaimResult{Replay: []byte(*snapshotText)}, nil
	case storedHash != hash:
		return ClaimResult{}, ErrPayloadMismatch
	default:
		return ClaimResult{}, fmt.Errorf("re-read the claim of %s: state %q carries no snapshot to serve", key, state)
	}
}

// Complete marca el claim nuevo como completado con el snapshot de la respuesta: es el
// ÚLTIMO paso de la transacción de negocio (docs/DATABASE.md §7.1 Branch B), de modo que
// el COMMIT es todo-o-nada — si el negocio falla, el claim se revierte con él. Un UPDATE
// de cero filas es un defecto del llamador, no una corrección silenciosa.
func Complete(ctx context.Context, claims postgres.TenantTx, id uuid.UUID, snapshot []byte) error {
	if claims == nil {
		return errors.New("the idempotency claim completion needs the caller's tenant transaction")
	}
	if id == uuid.Nil {
		return errors.New("the idempotency claim completion needs the claim id")
	}

	tag, err := claims.Exec(ctx, completeClaimSQL, id, string(snapshot))
	if err != nil {
		return fmt.Errorf("complete the claim %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("complete the claim %s: the row is not in this transaction", id)
	}
	return nil
}
