// Pruebas de integración de POST /fiado-movements (W7-DB-2, W7-BE-2): el ciclo completo sobre
// una base desechable `pos_test_`, router REAL httpx, store de POSTGRES sobre el pool de
// `pos_app` con RLS, y toda aserción leída POR el camino de producción (withTenant).
//
// Las invariantes que solo la base real puede demostrar:
//   - el upsert de la cuenta ocurre DENTRO de la transacción del movimiento y revierte con
//     ella (docs/DATABASE.md §7.8, F12);
//   - una cuenta de otro tenant no se puede referenciar por cuerpo aunque la FK la aprobaría
//     —la FK corre fuera del alcance del RLS—;
//   - la repetición de la misma clave devuelve el snapshot almacenado y deja UN movimiento y
//     UN claim (docs/DATABASE.md §7.1 Branch A);
//   - el abono viaja con signo negativo y el saldo computado lo refleja.
package fiado_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// movementAckBody es el acuse de POST /fiado-movements (decisión 3 de forma). Los punteros
// distinguen el `null` del contrato de un campo ausente.
type movementAckBody struct {
	Movement struct {
		ID             string  `json:"id"`
		TenantID       string  `json:"tenant_id"`
		FiadoAccountID string  `json:"fiado_account_id"`
		AmountCents    int64   `json:"amount_cents"`
		SoldOn         *string `json:"sold_on"`
		SaleID         *string `json:"sale_id"`
		Note           *string `json:"note"`
		CreatedAt      string  `json:"created_at"`
		ReceivedAt     string  `json:"received_at"`
	} `json:"movement"`
	Account struct {
		ID           string  `json:"id"`
		TenantID     string  `json:"tenant_id"`
		Name         string  `json:"name"`
		Phone        *string `json:"phone"`
		CreatedAt    string  `json:"created_at"`
		UpdatedAt    string  `json:"updated_at"`
		ReceivedAt   string  `json:"received_at"`
		BalanceCents int64   `json:"balance_cents"`
	} `json:"account"`
}

// chargeBody es el cuerpo de referencia del cargo a fiado: registra el deudor y enlaza la venta.
func chargeBody() string {
	return `{
		"id": "` + movementID + `",
		"fiado_account_id": "` + accountID + `",
		"fiado_account": {"name": "Rosa Quispe", "phone": "+51987111222"},
		"amount_cents": 1500,
		"note": "Venta fiada",
		"sold_on": "` + businessDate + `",
		"sale_id": "` + saleID + `"
	}`
}

// paymentBody es el cuerpo del abono parcial: el signo negativo ES el tipo.
func paymentBody() string {
	return `{
		"id": "` + paymentID + `",
		"fiado_account_id": "` + accountID + `",
		"amount_cents": -500,
		"note": "Abono efectivo"
	}`
}

// decodeMovementAck decodifica el acuse de §4.11.
func decodeMovementAck(t *testing.T, recorder *httptest.ResponseRecorder) movementAckBody {
	t.Helper()

	var ack movementAckBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &ack); err != nil {
		t.Fatalf("el acuse no es JSON: %v (%q)", err, recorder.Body.String())
	}
	return ack
}

// TestMovementCreatesTheAccountAndTheChargeAtomically: F12 contra la base real — la cuenta
// nace en la misma transacción del movimiento, el cargo queda enlazado a la venta, y el acuse
// trae el saldo computado (docs/DATABASE.md §7.8; decisión 3 de forma).
func TestMovementCreatesTheAccountAndTheChargeAtomically(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedUser(t, db, tokenTenantID, tokenUserID)
	seedSale(t, db, tokenTenantID, tokenUserID, saleID, businessDate, 1500)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})

	recorder := doMovementRequest(t, router, issueToken(t, issuer), chargeBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	ack := decodeMovementAck(t, recorder)
	if ack.Movement.ID != movementID {
		t.Errorf("movement.id = %q, want %q", ack.Movement.ID, movementID)
	}
	if ack.Movement.TenantID != tokenTenantID {
		t.Errorf("movement.tenant_id = %q, want el del token", ack.Movement.TenantID)
	}
	if ack.Movement.AmountCents != 1500 {
		t.Errorf("movement.amount_cents = %d, want 1500 del cuerpo congelado", ack.Movement.AmountCents)
	}
	if ack.Movement.SoldOn == nil || *ack.Movement.SoldOn != businessDate {
		t.Errorf("movement.sold_on = %v, want %q del par", ack.Movement.SoldOn, businessDate)
	}
	if ack.Movement.SaleID == nil || *ack.Movement.SaleID != saleID {
		t.Errorf("movement.sale_id = %v, want %q del par", ack.Movement.SaleID, saleID)
	}
	if ack.Account.ID != accountID || ack.Account.Name != "Rosa Quispe" {
		t.Errorf("account = %+v, want la cuenta creada por el upsert", ack.Account)
	}
	if ack.Account.Phone == nil || *ack.Account.Phone != "+51987111222" {
		t.Errorf("account.phone = %v, want el teléfono del registro", ack.Account.Phone)
	}
	if ack.Account.BalanceCents != 1500 {
		t.Errorf("account.balance_cents = %d, want 1500 computado de los movimientos", ack.Account.BalanceCents)
	}

	// Lo guardado: UNA cuenta, UN movimiento y un claim completado con el acuse exacto.
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_accounts`); got != 1 {
		t.Errorf("fiado_accounts = %d fila(s), want 1", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements WHERE amount_cents = 1500`); got != 1 {
		t.Errorf("fiado_movements = %d fila(s) con el cargo, want 1", got)
	}
	if got := queryString(t, tenants, tokenTenantID, `SELECT state FROM idempotency_keys WHERE key = $1`, defaultIdempotencyKey); got != "completed" {
		t.Errorf("el claim quedó en %q, want completed", got)
	}
	if got := queryString(t, tenants, tokenTenantID, `SELECT response_snapshot FROM idempotency_keys WHERE key = $1`, defaultIdempotencyKey); strings.TrimSpace(got) != strings.TrimSpace(recorder.Body.String()) {
		t.Errorf("el snapshot del claim no es el acuse respondido:\n got: %q\nwant: %q", got, recorder.Body.String())
	}
}

// TestMovementReplaysTheStoredSnapshot: Branch A — la misma clave con el mismo payload no
// escribe nada y devuelve el snapshot almacenado byte a byte (§7.1).
func TestMovementReplaysTheStoredSnapshot(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})
	token := issueToken(t, issuer)

	first := doMovementRequest(t, router, token, paymentBody(), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("primer envío: status = %d (%q)", first.Code, first.Body.String())
	}

	second := doMovementRequest(t, router, token, paymentBody(), nil)
	if second.Code != http.StatusOK {
		t.Fatalf("repetición: status = %d, want 200 (%q)", second.Code, second.Body.String())
	}
	if strings.TrimSpace(first.Body.String()) != strings.TrimSpace(second.Body.String()) {
		t.Errorf("la repetición no devolvió el snapshot almacenado:\nfirst: %q\nsecond: %q", first.Body.String(), second.Body.String())
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements`); got != 1 {
		t.Errorf("fiado_movements = %d fila(s) tras la repetición, want 1", got)
	}

	// El abono negativo bajó el saldo computado.
	if balance := decodeMovementAck(t, second).Account.BalanceCents; balance != -500 {
		t.Errorf("balance_cents = %d tras el abono, want -500", balance)
	}
}

// TestMovementWithAnotherPayloadIsTheMismatchCode: la misma clave con otro payload es el 422
// del contrato y NINGUNA segunda escritura de negocio (§7.1 Branch A).
func TestMovementWithAnotherPayloadIsTheMismatchCode(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})
	token := issueToken(t, issuer)

	if recorder := doMovementRequest(t, router, token, paymentBody(), nil); recorder.Code != http.StatusOK {
		t.Fatalf("primer envío: status = %d (%q)", recorder.Code, recorder.Body.String())
	}

	otherPayload := `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "amount_cents": -900}`
	recorder := doMovementRequest(t, router, token, otherPayload, nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
	}
	if code := decodeEnvelope(t, recorder).Error.Code; code != payloadMismatchCode {
		t.Errorf("error.code = %q, want %q", code, payloadMismatchCode)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements`); got != 1 {
		t.Errorf("fiado_movements = %d fila(s), want 1: el payload distinto no escribe", got)
	}
}

// TestMovementRefusesAnAccountFromAnotherTenant: la FK corre FUERA del alcance del RLS y
// aprobaría la cuenta ajena, así que la barrera es la relectura por RLS (misma regla que la
// venta aplica a `products`). El 422 nombra el campo, jamás el tenant (§4.1).
func TestMovementRefusesAnAccountFromAnotherTenant(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedTenant(t, db, otherTenantID)
	seedAccount(t, db, otherTenantID, accountID, "Deudor ajeno", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})

	// El cuerpo NO trae `fiado_account`: sin nombre no hay upsert posible, así que la única
	// salida es rechazar la referencia.
	body := `{"id": "` + movementID + `", "fiado_account_id": "` + accountID + `", "amount_cents": 1500}`
	recorder := doMovementRequest(t, router, issueToken(t, issuer), body, nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
	}
	if field := decodeEnvelope(t, recorder).Error.Details["field"]; field != "fiado_account_id" {
		t.Errorf("details.field = %v, want fiado_account_id", field)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements`); got != 0 {
		t.Errorf("fiado_movements del tenant = %d, want 0", got)
	}
}

// TestMovementRollsBackTheAccountWhenTheMovementFails: si la escritura de negocio falla
// DESPUÉS del upsert, la cuenta revierte con la transacción y el claim no queda reclamado —
// todo-o-nada (docs/DATABASE.md §7.8, MD-4).
func TestMovementRollsBackTheAccountWhenTheMovementFails(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})

	// La venta enlazada NO existe en la base: el INSERT del movimiento choca con la FK
	// compuesta después de que la cuenta ya se escribió en la transacción.
	orphanSaleID := "019a0000-0000-7000-8000-0000000000e1"
	body := `{
		"id": "` + movementID + `",
		"fiado_account_id": "` + accountID + `",
		"fiado_account": {"name": "Rosa Quispe"},
		"amount_cents": 1500,
		"sold_on": "` + businessDate + `",
		"sale_id": "` + orphanSaleID + `"
	}`
	recorder := doMovementRequest(t, router, issueToken(t, issuer), body, nil)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%q)", recorder.Code, recorder.Body.String())
	}
	if field := decodeEnvelope(t, recorder).Error.Details["field"]; field != "sale_id" {
		t.Errorf("details.field = %v, want sale_id", field)
	}

	// La cuenta creada por el upsert NO sobrevivió al rollback, y el claim tampoco.
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_accounts`); got != 0 {
		t.Errorf("fiado_accounts = %d fila(s) tras el rollback, want 0", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM idempotency_keys`); got != 0 {
		t.Errorf("idempotency_keys = %d fila(s) tras el rollback, want 0", got)
	}
}

// TestMovementIsIdempotentUnderRetries: el cargo emitido sin conexión colapsa a un solo ack
// aunque el cliente lo reintente varias veces con la misma clave (S11; §7.1).
func TestMovementIsIdempotentUnderRetries(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedUser(t, db, tokenTenantID, tokenUserID)
	seedSale(t, db, tokenTenantID, tokenUserID, saleID, businessDate, 1500)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})
	token := issueToken(t, issuer)

	var bodies []string
	for attempt := 0; attempt < 3; attempt++ {
		recorder := doMovementRequest(t, router, token, chargeBody(), nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("intento %d: status = %d (%q)", attempt+1, recorder.Code, recorder.Body.String())
		}
		bodies = append(bodies, strings.TrimSpace(recorder.Body.String()))
	}

	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Errorf("los reintentos no devolvieron el mismo acuse:\n%q\n%q\n%q", bodies[0], bodies[1], bodies[2])
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements`); got != 1 {
		t.Errorf("fiado_movements = %d fila(s) tras tres intentos, want 1", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM idempotency_keys`); got != 1 {
		t.Errorf("idempotency_keys = %d fila(s) tras tres intentos, want 1", got)
	}
}

// TestMovementRequiresTheIdempotencyKeyOverTheRealRouter: el 422 del encabezado ausente
// también sobre el router completo, sin escribir nada.
func TestMovementRequiresTheIdempotencyKeyOverTheRealRouter(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})

	recorder := doMovementRequest(t, router, issueToken(t, issuer), paymentBody(), map[string]string{"Idempotency-Key": ""})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 sin Idempotency-Key", recorder.Code)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM fiado_movements`); got != 0 {
		t.Errorf("fiado_movements = %d fila(s) sin claim, want 0", got)
	}
}

// TestMovementUpdatesTheAccountUnderLWLBound: `updated_at` es el comparador LWW y el daño
// está acotado a nombre y teléfono (docs/DATABASE.md §7.4): una mutación MÁS VIEJA no pisa el
// nombre, una más nueva sí, y el ledger anexa las dos.
func TestMovementUpdatesTheAccountUnderLWLBound(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})
	token := issueToken(t, issuer)

	// Una mutación de nombre MÁS NUEVA que la cuenta sembrada (sembrada con now()).
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	newer := `{
		"id": "` + movementID + `",
		"fiado_account_id": "` + accountID + `",
		"fiado_account": {"name": "Rosa Quispe Vargas", "updated_at": "` + future + `"},
		"amount_cents": 1500
	}`
	newerRecorder := doMovementRequest(t, router, token, newer, nil)
	if newerRecorder.Code != http.StatusOK {
		t.Fatalf("mutación nueva: status = %d (%q)", newerRecorder.Code, newerRecorder.Body.String())
	}
	if got := decodeMovementAck(t, newerRecorder).Account.Name; got != "Rosa Quispe Vargas" {
		t.Errorf("account.name = %q, want el nombre de la mutación más nueva", got)
	}

	// Una mutación MÁS VIEJA con otra clave: no pisa el nombre ya confirmado, pero el abono SÍ
	// se anexa — el LWW acota el daño a nombre y teléfono, nunca al ledger (§7.4).
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	older := `{
		"id": "` + paymentID + `",
		"fiado_account_id": "` + accountID + `",
		"fiado_account": {"name": "Rosa Q.", "updated_at": "` + past + `"},
		"amount_cents": -500
	}`
	recorder := doMovementRequest(t, router, token, older, map[string]string{"Idempotency-Key": "fiado-key-0002"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("mutación vieja: status = %d (%q)", recorder.Code, recorder.Body.String())
	}
	ack := decodeMovementAck(t, recorder)
	if ack.Account.Name != "Rosa Quispe Vargas" {
		t.Errorf("account.name = %q, want el nombre más nuevo: LWW no aplica la mutación vieja", ack.Account.Name)
	}
	if ack.Account.BalanceCents != 1000 {
		t.Errorf("balance_cents = %d, want 1000 (1500 del cargo menos 500 del abono)", ack.Account.BalanceCents)
	}
}
