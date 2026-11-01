// Pruebas de integración de GET /fiado-accounts (W7-BE-1, W7-BE-3): la cartera con el saldo
// COMPUTADO y el historial firmado, sobre una base desechable `pos_test_` y el router REAL.
//
// Lo que solo la base real puede demostrar: el saldo servido es exactamente la suma de los
// movimientos servidos (no hay columna que lo sostenga), la respuesta restaura un dispositivo
// que borró su base local, y el RLS impide ver al deudor de otro tenant.
package fiado_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// accountsBody es la respuesta de GET /fiado-accounts (decisión 4 de forma).
type accountsBody struct {
	Accounts []struct {
		ID           string  `json:"id"`
		TenantID     string  `json:"tenant_id"`
		Name         string  `json:"name"`
		Phone        *string `json:"phone"`
		BalanceCents int64   `json:"balance_cents"`
		Movements    []struct {
			ID             string  `json:"id"`
			TenantID       string  `json:"tenant_id"`
			FiadoAccountID string  `json:"fiado_account_id"`
			AmountCents    int64   `json:"amount_cents"`
			SoldOn         *string `json:"sold_on"`
			SaleID         *string `json:"sale_id"`
			Note           *string `json:"note"`
			CreatedAt      string  `json:"created_at"`
			ReceivedAt     string  `json:"received_at"`
		} `json:"movements"`
	} `json:"accounts"`
}

// decodeAccounts decodifica la respuesta de §4.12.
func decodeAccounts(t *testing.T, recorder *httptest.ResponseRecorder) accountsBody {
	t.Helper()

	var body accountsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (%q)", err, recorder.Body.String())
	}
	return body
}

// TestAccountsServesTheComputedBalanceAndTheSignedHistory: `GET /fiado-accounts` devuelve el
// saldo computado y el historial firmado, con las cuentas ordenadas por nombre (§4.12).
func TestAccountsServesTheComputedBalanceAndTheSignedHistory(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "+51987111222")
	seedMovement(t, db, tokenTenantID, movementID, accountID, 1500, "", "")
	seedMovement(t, db, tokenTenantID, paymentID, accountID, -500, "", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})

	recorder := dispatch(t, router, http.MethodGet, "/fiado-accounts", issueToken(t, issuer), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	body := decodeAccounts(t, recorder)
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(body.Accounts))
	}
	account := body.Accounts[0]
	if account.BalanceCents != 1000 {
		t.Errorf("balance_cents = %d, want 1000 computado", account.BalanceCents)
	}
	if len(account.Movements) != 2 {
		t.Fatalf("movements = %d, want 2", len(account.Movements))
	}
	// Orden determinista: created_at, luego id. Los dos movimientos se sembraron con el mismo
	// reloj, así que el desempate es el id (…c3 antes que …c4) y el signo viaja intacto.
	if account.Movements[0].ID != movementID || account.Movements[0].AmountCents != 1500 {
		t.Errorf("primer movimiento = %+v, want el cargo %s de 1500", account.Movements[0], movementID)
	}
	if account.Movements[1].ID != paymentID || account.Movements[1].AmountCents != -500 {
		t.Errorf("segundo movimiento = %+v, want el abono %s de -500", account.Movements[1], paymentID)
	}

	// La suma de los movimientos servidos es el saldo servido: la invariante se sostiene en la
	// propia respuesta, no en una columna.
	var sum int64
	for _, movement := range account.Movements {
		sum += movement.AmountCents
	}
	if sum != account.BalanceCents {
		t.Errorf("SUM(movements) = %d y balance_cents = %d: el saldo no es la suma", sum, account.BalanceCents)
	}
}

// TestAccountsRestoresTheHistoryAfterAReinstall: el recorrido de borrado y reinstalación del
// lado del SERVIDOR — el pull devuelve cuentas, movimientos firmados y saldos idénticos, así
// que un dispositivo que borró su base local los reconstruye por completo (W7-BE-3, S12).
func TestAccountsRestoresTheHistoryAfterAReinstall(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedUser(t, db, tokenTenantID, tokenUserID)
	seedSale(t, db, tokenTenantID, tokenUserID, saleID, businessDate, 1500)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})
	token := issueToken(t, issuer)

	if recorder := doMovementRequest(t, router, token, chargeBody(), nil); recorder.Code != http.StatusOK {
		t.Fatalf("cargo: status = %d (%q)", recorder.Code, recorder.Body.String())
	}
	if recorder := doMovementRequest(t, router, token, paymentBody(), map[string]string{"Idempotency-Key": "fiado-key-0002"}); recorder.Code != http.StatusOK {
		t.Fatalf("abono: status = %d (%q)", recorder.Code, recorder.Body.String())
	}

	// El dispositivo reinstalado no trae NADA local: pide el historial completo otra vez, con
	// un pool nuevo (una conexión nueva, como la de un proceso recién arrancado).
	reinstalled := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	reinstalledRouter, _ := newFiadoIntegrationRouter(t, reinstalled, &strings.Builder{})

	recorder := dispatch(t, reinstalledRouter, http.MethodGet, "/fiado-accounts", token, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restauración: status = %d (%q)", recorder.Code, recorder.Body.String())
	}

	body := decodeAccounts(t, recorder)
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts restauradas = %d, want 1", len(body.Accounts))
	}
	account := body.Accounts[0]
	if account.Name != "Rosa Quispe" || account.ID != accountID {
		t.Errorf("cuenta restaurada = %+v, want la registrada", account)
	}
	if len(account.Movements) != 2 {
		t.Fatalf("movimientos restaurados = %d, want 2 (cargo y abono)", len(account.Movements))
	}
	if account.BalanceCents != 1000 {
		t.Errorf("balance_cents restaurado = %d, want 1000", account.BalanceCents)
	}
	if account.Movements[0].SaleID == nil || *account.Movements[0].SaleID != saleID {
		t.Errorf("el enlace a la venta no se restauró: %+v", account.Movements[0])
	}
	if account.Movements[1].AmountCents != -500 {
		t.Errorf("el abono restaurado viaja con %d, want -500", account.Movements[1].AmountCents)
	}
}

// TestAccountsIsolateTenants: cada token ve SOLO sus cuentas — el RLS es la barrera, no un
// WHERE del handler (docs/DATABASE.md §5.2).
func TestAccountsIsolateTenants(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedTenant(t, db, otherTenantID)
	seedAccount(t, db, tokenTenantID, accountID, "Rosa Quispe", "")
	seedAccount(t, db, otherTenantID, "019a0000-0000-7000-8000-0000000000e2", "Deudor ajeno", "")

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newFiadoIntegrationRouter(t, tenants, &strings.Builder{})

	recorder := dispatch(t, router, http.MethodGet, "/fiado-accounts", issueToken(t, issuer), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := decodeAccounts(t, recorder)
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1: el pull no puede ver el deudor de otro tenant", len(body.Accounts))
	}
	if body.Accounts[0].ID != accountID {
		t.Errorf("account.id = %q, want la del token %q", body.Accounts[0].ID, accountID)
	}
	if strings.Contains(recorder.Body.String(), "Deudor ajeno") {
		t.Errorf("la respuesta nombra un deudor de otro tenant: %q", recorder.Body.String())
	}
}
