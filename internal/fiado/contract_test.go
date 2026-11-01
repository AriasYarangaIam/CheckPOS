// Constantes y ayudantes compartidos por las pruebas del módulo Fiado.
//
// Las constantes son las del arnés de internal/cashclose: un tenant, un usuario y los ids
// que los cuerpos de referencia envían. Los ids son UUID con la forma de los del contrato
// (prefijo temporal fijo y cola secuencial) para que un fallo se lea de un vistazo.
package fiado_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// testSigningSecret es la clave de firma de las pruebas: 32 bytes, el piso del contrato.
	testSigningSecret = "test-only-signing-key-0123456789abcdef"

	// tokenTenantID es el tenant DENTRO del token: la única fuente admisible.
	tokenTenantID = "019a0000-0000-7000-8000-0000000000b1"
	// tokenUserID es el usuario del token: el cajero que registra el movimiento.
	tokenUserID = "019a0000-0000-7000-8000-0000000000b2"
	// otherTenantID es un tenant ajeno: su cuenta no puede ser referenciada por cuerpo.
	otherTenantID = "019a0000-0000-7000-8000-0000000000b3"

	// businessDate es la fecha de negocio de la venta enlazada.
	businessDate = "2026-10-26"
	// saleID es la venta a fiado a la que un movimiento puede enlazarse.
	saleID = "019a0000-0000-7000-8000-0000000000c1"
	// accountID es la cuenta de fiado del deudor de referencia.
	accountID = "019a0000-0000-7000-8000-0000000000c2"
	// movementID es el movimiento de cargo de referencia.
	movementID = "019a0000-0000-7000-8000-0000000000c3"
	// paymentID es el movimiento de abono (negativo) de referencia.
	paymentID = "019a0000-0000-7000-8000-0000000000c4"

	// defaultIdempotencyKey es la clave que el helper inyecta cuando la prueba no pide otra.
	defaultIdempotencyKey = "fiado-key-0001"
	// payloadMismatchCode es el código de superficie de Branch A con hashes distintos.
	payloadMismatchCode = "idempotency.payload_mismatch"
)

// Códigos de SQLSTATE que las pruebas nombran: son parte del contrato que fijan.
const (
	sqlStateUndefinedColumn = "42703"
	sqlStateCheckViolation  = "23514"
	sqlStateForeignKey      = "23503"
	sqlStateAppendOnly      = "55006"
)

// alwaysReady es el sondeo de preparación del router de estas pruebas.
type alwaysReady struct{}

// Ping informa que la dependencia responde: el router no la vuelve a consultar.
func (alwaysReady) Ping(context.Context) error { return nil }

// assertSQLState exige que el error sea de PostgreSQL con el SQLSTATE esperado.
func assertSQLState(t *testing.T, err error, want, what string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s no falló: want SQLSTATE %s", what, want)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s falló con %v, want un error de PostgreSQL con SQLSTATE %s", what, err, want)
	}
	if pgErr.Code != want {
		t.Errorf("%s devolvió SQLSTATE %s (%s), want %s", what, pgErr.Code, pgErr.Message, want)
	}
}
