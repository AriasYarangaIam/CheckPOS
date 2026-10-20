// Pruebas unitarias del protocolo de claim de idempotencia (W4-BE-2): la forma del hash
// del payload y el camino de datos del claim sobre un TenantTx stub, sin base de datos.
//
// Lo no negociable de docs/DATABASE.md §7.1 y docs/BACKEND.md §6.1:
//   - el claim se GANA con el INSERT sobre la UNIQUE (tenant_id, key): una clave fresca
//     no corre NINGUNA consulta antes del INSERT — nada de SELECT-then-INSERT (la
//     aceptación del sprint) ni después de él (el re-read de Branch A/C solo puede pasar
//     por el re-read con savepoint, probado de integración en el paquete del ledger);
//   - el lock timeout es la PRIMERA sentencia de la transacción: es la perilla del §7.1
//     Branch C que acota la ventana de duplicado concurrente;
//   - el claim se COMPLETA con un UPDATE al estado y su snapshot, en la MISMA
//     transacción que escribe el negocio.
package synckey_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AriasYarangaIam/CheckPOS/internal/synckey"
)

// claimTenant es el tenant de las pruebas del claim.
const claimTenant = "019a0000-0000-7000-8000-0000000000f1"

// claimTime es la marca del claim de las pruebas: fija para que el INSERT sea determinista.
var claimTime = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// stubClaimTx es el TenantTx de las pruebas: registra cada Exec y a veces falla bajo
// pedido. Claim estrena la transacción, así que en estas pruebas no hay Query: el stub
// las corta como una vía de escape oculta que anularía el INSERT como método de claim.
type stubClaimTx struct {
	execs []stubExec

	// failIndex falla EN EL EXEC i-ésimo (0-based) con failErr; -1 no falla nunca.
	failIndex int
	failErr   error
}

type stubExec struct {
	sql  string
	args []any
}

func (s *stubClaimTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	index := len(s.execs)
	if s.failErr != nil && s.failIndex == index {
		s.execs = append(s.execs, stubExec{sql: sql, args: args})
		return pgconn.CommandTag{}, s.failErr
	}
	s.execs = append(s.execs, stubExec{sql: sql, args: args})
	// Un tag con una fila afectada: es lo que devuelve PostgreSQL para el UPDATE del
	// Complete sobre el claim recién insertado.
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (s *stubClaimTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("el claim no debe leer con Query: el re-read de las ramas A/C corre solo tras un error real de la base y se prueba de integración")
}

func (s *stubClaimTx) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("el claim no debe leer con QueryRow: el re-read de las ramas A/C corre solo tras un error real de la base y se prueba de integración")
}

// execSQLFor es un atajo de aserción: la SQL del exec i-ésimo.
func execSQLFor(t *testing.T, tx *stubClaimTx, index int) string {
	t.Helper()

	if index >= len(tx.execs) {
		t.Fatalf("el claim corrió %d sentencia(s), se esperaban al menos %d", len(tx.execs), index+1)
	}
	return tx.execs[index].sql
}

// TestPayloadHashCoversTheBodyAndTheKey fija la forma exacta del hash del payload: SHA-256
// en hex sobre la clave y el cuerpo serializado, en el orden —cuerpo y clave son la MISMA
// pareja (tenant_id, key) del claim, así que el hash hereda la unicidad del par (§2.10,
// docs/BACKEND.md §6.2)—.
func TestPayloadHashCoversTheBodyAndTheKey(t *testing.T) {
	body := []byte(`{"sale_id":"019b"}`)

	digest := sha256.Sum256(append(append([]byte("k-1"), 0x00), body...))
	want := hex.EncodeToString(digest[:])
	if got := synckey.PayloadHash("k-1", body); got != want {
		t.Fatalf("PayloadHash = %q, want %q: SHA-256 hex sobre (clave, NUL, cuerpo)", got, want)
	}
	if len(want) != 64 {
		t.Fatalf("el hash mide %d caracteres, want 64 de un SHA-256 hex", len(want))
	}

	// El par que manda: cambiar el cuerpo con la MISMA clave o la clave con el MISMO cuerpo
	// son dos payloads distintos (§7.1 Branch A).
	if synckey.PayloadHash("k-1", body) == synckey.PayloadHash("k-1", []byte(`{"sale_id":"otra"}`)) {
		t.Errorf("cuerpos distintos con la misma clave compartieron hash")
	}
	if synckey.PayloadHash("k-1", body) == synckey.PayloadHash("k-2", body) {
		t.Errorf("la misma clave con claves de claim distintas compartió hash")
	}
}

// TestClaimFreshWinsByInsertion: una clave nueva se reclama con la UNIQUE — el lock timeout
// primero, savepoint, y el INSERT del claim; NINGUNA consulta de lectura en el camino
// fresco (§7.1 Branch B, la aceptación del sprint).
func TestClaimFreshWinsByInsertion(t *testing.T) {
	tx := &stubClaimTx{failIndex: -1}

	result, err := synckey.Claim(context.Background(), tx, uuid.MustParse(claimTenant), "k-fresh", "hash-de-payload", claimTime)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if !result.Fresh {
		t.Errorf("result.Fresh = false, want la rama B: clave nueva")
	}
	if result.ID == uuid.Nil {
		t.Errorf("result.ID = nil uuid, want el id del claim recién insertado, para el Complete posterior")
	}
	if len(result.Replay) != 0 {
		t.Errorf("result.Replay = %q, want vacío: una clave fresca no reenvía nada", result.Replay)
	}
	if !strings.Contains(execSQLFor(t, tx, 0), "lock_timeout") {
		t.Errorf("la primera sentencia = %q, want el lock timeout del Branch C primero", execSQLFor(t, tx, 0))
	}
	if !strings.Contains(execSQLFor(t, tx, 1), "SAVEPOINT") {
		t.Errorf("la segunda sentencia = %q, want el savepoint que permite el re-read de Branch A/C", execSQLFor(t, tx, 1))
	}
	inserted := execSQLFor(t, tx, 2)
	if !strings.Contains(inserted, "INSERT INTO idempotency_keys") || !strings.Contains(inserted, "'in_flight'") {
		t.Errorf("tercera sentencia = %q, want el INSERT del claim en in_flight — el claim se GANA insertando", inserted)
	}
	if strings.Contains(inserted, "SELECT") {
		t.Errorf("el INSERT = %q, want sin SELECT: el claim jamás es SELECT-then-INSERT", inserted)
	}
	for _, exec := range tx.execs[3:] {
		if strings.Contains(exec.sql, "SELECT") {
			t.Errorf("el camino fresco leyó con %q: la clave nueva no relee nada", exec.sql)
		}
	}
	// El INSERT lleva el par completo: tenant, key, hash del payload y la marca del claim.
	args := tx.execs[2].args
	if len(args) != 5 {
		t.Fatalf("args del INSERT = %d, want 5 (id, tenant, key, hash, marca)", len(args))
	}
	if args[0].(uuid.UUID) == uuid.Nil {
		t.Errorf("args[0] = uuid nil, want el id del claim")
	}
	if args[1] != uuid.MustParse(claimTenant) {
		t.Errorf("args[1] = %v, want el tenant del token", args[1])
	}
	if args[2] != "k-fresh" {
		t.Errorf("args[2] = %v, want la clave del encabezado", args[2])
	}
	if args[3] != "hash-de-payload" {
		t.Errorf("args[3] = %v, want el hash del payload", args[3])
	}
	if args[4] != claimTime {
		t.Errorf("args[4] = %v, want la marca del cierre", args[4])
	}
}

// TestClaimFeedsAnUnexpectedFailure: un error del INSERT que no es de la UNIQUE ni del
// lock timeout sube tal cual — codificarlo acá sería esconder una falla de la base.
func TestClaimFeedsAnUnexpectedFailure(t *testing.T) {
	tx := &stubClaimTx{failIndex: 2, failErr: context.DeadlineExceeded}

	_, err := synckey.Claim(context.Background(), tx, uuid.MustParse(claimTenant), "k", "hash", claimTime)
	if err == nil {
		t.Fatalf("Claim = nil error, want el fallo de la base")
	}
	if errors.Is(err, synckey.ErrPayloadMismatch) || errors.Is(err, synckey.ErrInFlight) {
		t.Errorf("err = %v, want un error externo, NO un sentinela de rama", err)
	}
}

// TestCompleteMarksTheClaimCompletedWithTheSnapshot: el Complete es UN UPDATE a completed
// con el snapshot de la respuesta, por id — la fila que el claim insertó (§7.1 Branch B).
func TestCompleteMarksTheClaimCompletedWithTheSnapshot(t *testing.T) {
	claimID := uuid.MustParse("019b0000-0000-7000-8000-0000000000d1")
	tx := &stubClaimTx{failIndex: -1}

	snapshot := []byte(`{"sale":{"sale_id":"019b"}}`)
	if err := synckey.Complete(context.Background(), tx, claimID, snapshot); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(tx.execs) != 1 {
		t.Fatalf("Complete corrió %d sentencia(s), want 1", len(tx.execs))
	}
	sql := tx.execs[0].sql
	if !strings.Contains(sql, "UPDATE idempotency_keys") || !strings.Contains(sql, "'completed'") {
		t.Errorf("Complete = %q, want el UPDATE al estado completed", sql)
	}
	if strings.Contains(sql, "SELECT") {
		t.Errorf("Complete = %q, want sin SELECT: la fila de retornar ya es nuestra", sql)
	}
	args := tx.execs[0].args
	if args[0] != claimID {
		t.Errorf("args[0] = %v, want el id del claim", args[0])
	}
	if args[1] != string(snapshot) {
		t.Errorf("args[1] = %v, want el snapshot de la respuesta como TEXTO del contrato (§2.9)", args[1])
	}
}
