// Pruebas unitarias de la cartera de deudores (W7-BE-1): el handler de GET /fiado-accounts
// contra un store de mentira, sin base de datos. Lo que se fija acá es la FORMA de §4.12 —el
// saldo computado y el historial firmado— y no el SQL, que vive en la prueba de integración.
package fiado_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/fiado"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
)

// stubAccountsStore devuelve las cuentas que la prueba fijó.
type stubAccountsStore struct {
	accounts []fiado.AccountWithMovements
	err      error
}

// ListAccounts devuelve las cuentas fijadas.
func (s *stubAccountsStore) ListAccounts(context.Context, uuid.UUID) ([]fiado.AccountWithMovements, error) {
	return s.accounts, s.err
}

// newAccountsRouter monta el router REAL con el handler de cuentas sobre el store de mentira.
func newAccountsRouter(t *testing.T, store fiado.AccountsStore) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	handler, err := fiado.NewFiadoAccountsHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewFiadoAccountsHandler: %v", err)
	}

	tokenIssuer := newTestIssuer(t)

	return newRouter(t, func(mux *http.ServeMux) {
		mux.Handle("GET /fiado-accounts", identity.RequireAccessToken(tokenIssuer, handler))
	}, tokenIssuer)
}

// TestAccountsHandlerServesTheComputedBalances: `GET /fiado-accounts` devuelve las cuentas con
// el saldo computado y el historial firmado completo (§4.12, decisiones 4 y 7 de forma).
func TestAccountsHandlerServesTheComputedBalances(t *testing.T) {
	phone := "+51987111222"
	store := &stubAccountsStore{accounts: []fiado.AccountWithMovements{
		{
			Account:      fiado.Account{ID: mustParseUUID(t, accountID), TenantID: mustParseUUID(t, tokenTenantID), Name: "Rosa Quispe", Phone: &phone},
			BalanceCents: 1000,
			Movements: []fiado.Movement{
				{ID: mustParseUUID(t, movementID), AccountID: mustParseUUID(t, accountID), AmountCents: 1500},
				{ID: mustParseUUID(t, paymentID), AccountID: mustParseUUID(t, accountID), AmountCents: -500},
			},
		},
	}}
	router, issuer := newAccountsRouter(t, store)

	recorder := dispatch(t, router, http.MethodGet, "/fiado-accounts", issueToken(t, issuer), "", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Accounts []struct {
			ID           string  `json:"id"`
			Name         string  `json:"name"`
			Phone        *string `json:"phone"`
			BalanceCents int64   `json:"balance_cents"`
			Movements    []struct {
				ID          string `json:"id"`
				AmountCents int64  `json:"amount_cents"`
			} `json:"movements"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (%q)", err, recorder.Body.String())
	}
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(body.Accounts))
	}
	account := body.Accounts[0]
	if account.BalanceCents != 1000 {
		t.Errorf("balance_cents = %d, want 1000 computado", account.BalanceCents)
	}
	if len(account.Movements) != 2 {
		t.Fatalf("movements = %d, want 2 (el historial firmado completo)", len(account.Movements))
	}
	if account.Movements[1].AmountCents != -500 {
		t.Errorf("el abono viaja como %d, want -500: el signo ES el tipo", account.Movements[1].AmountCents)
	}
}

// TestAccountsHandlerOnlyAnswersGET: la ruta es de lectura y nada más.
func TestAccountsHandlerOnlyAnswersGET(t *testing.T) {
	router, issuer := newAccountsRouter(t, &stubAccountsStore{})

	recorder := dispatch(t, router, http.MethodPost, "/fiado-accounts", issueToken(t, issuer), "{}", nil)

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: la ruta solo responde GET", recorder.Code)
	}
}

// TestAccountsHandlerFailsClosedWithoutTheStore: un handler sin store no se construye — el
// contrato de tipos no deja un camino donde el handler corra sin dueño de datos.
func TestAccountsHandlerFailsClosedWithoutTheStore(t *testing.T) {
	if _, err := fiado.NewFiadoAccountsHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Error("NewFiadoAccountsHandler(nil) no falló")
	}
}
