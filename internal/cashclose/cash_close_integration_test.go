// Pruebas de integración de POST /cash-closes (W5-BE-1/W5-BE-3): el ciclo completo sobre
// una base desechable `pos_test_`, router REAL httpx, store de POSTGRES sobre el pool de
// `pos_app` con RLS, y toda aserción leída POR el camino de producción (withTenant).
//
// Las invariantes que solo la base real puede demostrar:
//   - la proyección F8 de una fecha de negocio, con la anulación RESTANDO y los métodos sin
//     filas del ledger valiendo cero (docs/DATABASE.md §4 `sales`);
//   - el esperado congelado en las seis filas del desglose, y la diferencia que NO existe
//     como columna (se computa, nunca se almacena);
//   - el duplicado de fecha como no-op silencioso con la proyección del cierre existente
//     (F38, docs/DATABASE.md §7.7);
//   - la repetición de la misma clave sin una segunda escritura (§7.1 Branch A).
package cashclose_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AriasYarangaIam/CheckPOS/internal/cashclose"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

const (
	// businessDate es la fecha de negocio que las pruebas cierran. Está dentro de la ventana
	// de particiones pre-creadas (sales_2026_10).
	businessDate = "2026-10-19"
	// previousDate es una venta del día ANTERIOR: el cierre no puede proyectarla.
	previousDate = "2026-10-18"

	// Las filas del ledger que la proyección lee.
	cashSaleID  = "019b0000-0000-7000-8000-0000000000b1"
	yapeSaleID  = "019b0000-0000-7000-8000-0000000000b2"
	fiadoSaleID = "019b0000-0000-7000-8000-0000000000b3"
	cashVoidID  = "019b0000-0000-7000-8000-0000000000b4"
	oldSaleID   = "019b0000-0000-7000-8000-0000000000b5"
)

// closeAckBody es el acuse de §4.13 tal como lo decodifica la prueba. `DifferenceCents` es
// puntero para poder distinguir el `null` del fiado del cero de los demás métodos.
type closeAckBody struct {
	CashClose struct {
		ID             string `json:"id"`
		TenantID       string `json:"tenant_id"`
		ClosedOn       string `json:"closed_on"`
		ClosedByUserID string `json:"closed_by_user_id"`
		CreatedAt      string `json:"created_at"`
		ReceivedAt     string `json:"received_at"`
	} `json:"cash_close"`
	Counts []struct {
		PaymentMethod   string `json:"payment_method"`
		ExpectedCents   int64  `json:"expected_cents"`
		CountedCents    int64  `json:"counted_cents"`
		DifferenceCents *int64 `json:"difference_cents"`
	} `json:"counts"`
}

// TestCashCloseFreezesThePerMethodProjectionOfTheLedger: el cierre contra la base real —
// F8 por método con la anulación restando, el esperado congelado en las seis filas, y la
// diferencia computada por la superficie sin una columna que la guarde.
func TestCashCloseFreezesThePerMethodProjectionOfTheLedger(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedCloseUser(t, db, tokenTenantID, tokenUserID)

	// El ledger del día: una venta por método, una anulación de la venta en efectivo, y una
	// venta del día anterior que la fecha de negocio del cierre NO toca.
	seedLedgerRow(t, db, ledgerRow{saleID: cashSaleID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: businessDate, kind: "sale", paymentMethod: "cash", totalCents: 5000})
	seedLedgerRow(t, db, ledgerRow{saleID: yapeSaleID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: businessDate, kind: "sale", paymentMethod: "yape", totalCents: 3000})
	seedLedgerRow(t, db, ledgerRow{saleID: fiadoSaleID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: businessDate, kind: "sale", paymentMethod: "fiado", totalCents: 2500})
	seedLedgerRow(t, db, ledgerRow{saleID: cashVoidID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: businessDate, kind: "void", paymentMethod: "cash", totalCents: 1000, originalSaleID: cashSaleID})
	seedLedgerRow(t, db, ledgerRow{saleID: oldSaleID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: previousDate, kind: "sale", paymentMethod: "cash", totalCents: 999})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newCashCloseIntegrationRouter(t, tenants, &strings.Builder{})

	recorder := doCloseRequest(t, router, closeToken(t, issuer), countedCloseBody(), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	ack := decodeCloseAck(t, recorder)
	if ack.CashClose.ID != closeID || ack.CashClose.ClosedOn != businessDate ||
		ack.CashClose.TenantID != tokenTenantID || ack.CashClose.ClosedByUserID != tokenUserID {
		t.Errorf("cabecera del acuse = %+v, want el cierre del token con su fecha de negocio", ack.CashClose)
	}
	if ack.CashClose.CreatedAt == "" || ack.CashClose.ReceivedAt == "" {
		t.Errorf("marcas del acuse = (%q, %q), want las del servidor", ack.CashClose.CreatedAt, ack.CashClose.ReceivedAt)
	}

	// La proyección F8, método por método: cash = 5000 − 1000 (la anulación resta), yape =
	// 3000, fiado = 2500 (crédito extendido del día), y los tres métodos sin filas del
	// ledger valen CERO — no están ausentes.
	wantExpected := map[string]int64{"cash": 4000, "yape": 3000, "plin": 0, "card": 0, "other": 0, "fiado": 2500}
	wantCounted := map[string]int64{"cash": 4500, "yape": 3000, "plin": 0, "card": 0, "other": 0, "fiado": 0}
	wantOrder := []string{"cash", "yape", "plin", "card", "other", "fiado"}

	if len(ack.Counts) != len(wantOrder) {
		t.Fatalf("counts = %d fila(s), want 6 (una por método)", len(ack.Counts))
	}
	for index, method := range wantOrder {
		count := ack.Counts[index]
		if count.PaymentMethod != method {
			t.Errorf("counts[%d].payment_method = %q, want %q (orden fijo del acuse)", index, count.PaymentMethod, method)
		}
		if count.ExpectedCents != wantExpected[method] {
			t.Errorf("%s.expected_cents = %d, want %d (proyección F8 de la fecha)", method, count.ExpectedCents, wantExpected[method])
		}
		if count.CountedCents != wantCounted[method] {
			t.Errorf("%s.counted_cents = %d, want %d del operador", method, count.CountedCents, wantCounted[method])
		}

		// La diferencia es comptuada en la superficie y el fiado viaja null: su esperado es
		// informacional y nunca un faltante (D11).
		if method == "fiado" {
			if count.DifferenceCents != nil {
				t.Errorf("fiado.difference_cents = %d, want null: la diferencia del fiado es informacional", *count.DifferenceCents)
			}
			continue
		}
		if count.DifferenceCents == nil {
			t.Fatalf("%s.difference_cents = null, want un entero computado", method)
		}
		if want := wantCounted[method] - wantExpected[method]; *count.DifferenceCents != want {
			t.Errorf("%s.difference_cents = %d, want %d (counted − expected)", method, *count.DifferenceCents, want)
		}
	}

	// La forma del cable: el fiado lleva `null` literal, no la ausencia del campo.
	if !strings.Contains(recorder.Body.String(), `"difference_cents":null`) {
		t.Errorf("el acuse no lleva difference_cents null: %q", recorder.Body.String())
	}

	// Lo guardado: UNA cabecera con la fecha y el cajero del token, seis filas con el
	// esperado CONGELADO, y ninguna columna de diferencia — se computa, nunca se almacena.
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_closes WHERE closed_on = $1`, businessDate); got != 1 {
		t.Errorf("cash_closes del día = %d fila(s), want 1", got)
	}
	if got := queryString(t, tenants, tokenTenantID, `SELECT closed_by_user_id::text FROM cash_closes WHERE id = $1`, mustParseUUID(t, closeID)); got != tokenUserID {
		t.Errorf("closed_by_user_id = %q, want el usuario del token", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_close_counts WHERE cash_close_id = $1`, mustParseUUID(t, closeID)); got != 6 {
		t.Errorf("cash_close_counts = %d fila(s), want 6", got)
	}
	for method, expected := range wantExpected {
		got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_close_counts
			WHERE cash_close_id = $1 AND payment_method = $2 AND expected_cents = $3 AND counted_cents = $4`,
			mustParseUUID(t, closeID), method, expected, wantCounted[method])
		if got != 1 {
			t.Errorf("%s: fila con expected %d y counted %d = %d, want 1 (el esperado queda congelado)", method, expected, wantCounted[method], got)
		}
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'cash_close_counts' AND column_name = 'difference_cents'`); got != 0 {
		t.Errorf("difference_cents existe como columna: la diferencia se computa, nunca se almacena (S4-1)")
	}

	// Y el claim quedó completado con el acuse EXACTO que se respondió.
	assertClaimSnapshot(t, tenants, defaultIdempotencyKey, recorder.Body.String())
}

// TestCashCloseDuplicateIsASilentNoOp: F38 contra la base real — el segundo cierre de la
// misma fecha, con OTRA clave y OTROS conteos, responde 200 con el cierre existente: ni un
// error para el cajero, ni una segunda fila, ni un segundo desglose (docs/DATABASE.md §7.7).
func TestCashCloseDuplicateIsASilentNoOp(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedCloseUser(t, db, tokenTenantID, tokenUserID)
	seedLedgerRow(t, db, ledgerRow{saleID: cashSaleID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: businessDate, kind: "sale", paymentMethod: "cash", totalCents: 5000})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newCashCloseIntegrationRouter(t, tenants, &strings.Builder{})

	first := doCloseRequest(t, router, closeToken(t, issuer), countedCloseBody(), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("estado del primer cierre = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	// El duplicado: otro id de cierre, otra clave y un conteo distinto — el cierre existente
	// es el que prevalece.
	duplicateBody := strings.Replace(countedCloseBody(), `"id": "`+closeID+`"`, `"id": "`+otherCloseID+`"`, 1)
	duplicateBody = strings.Replace(duplicateBody, `"payment_method": "cash", "counted_cents": 4500`,
		`"payment_method": "cash", "counted_cents": 1111`, 1)
	duplicate := doCloseRequest(t, router, closeToken(t, issuer), duplicateBody,
		map[string]string{"Idempotency-Key": "close-key-duplicado"})

	if duplicate.Code != http.StatusOK {
		t.Fatalf("estado del duplicado = %d, want 200: el duplicado nunca es un error visible (F38) (cuerpo %q)", duplicate.Code, duplicate.Body.String())
	}
	// La respuesta del duplicado ES la del cierre que existe: mismos bytes, porque el acuse
	// es determinista y el snapshot se arma con la fila guardada.
	if duplicate.Body.String() != first.Body.String() {
		t.Errorf("cuerpo del duplicado = %q, want la proyección EXISTENTE byte a byte %q", duplicate.Body.String(), first.Body.String())
	}

	ack := decodeCloseAck(t, duplicate)
	if ack.CashClose.ID != closeID {
		t.Errorf("id del acuse = %q, want el cierre EXISTENTE %q (primera-gana)", ack.CashClose.ID, closeID)
	}
	if ack.Counts[0].CountedCents != 4500 {
		t.Errorf("cash.counted_cents = %d, want 4500 del cierre existente, no el 1111 del duplicado", ack.Counts[0].CountedCents)
	}

	// Ni una segunda fila: una cabecera, seis conteos, y la clave del duplicado completada
	// con la proyección existente para que el cliente marque la operación como terminada.
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_closes`); got != 1 {
		t.Errorf("cash_closes = %d fila(s), want 1: el duplicado no inserta nada", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_close_counts`); got != 6 {
		t.Errorf("cash_close_counts = %d fila(s), want 6: el duplicado no duplica el desglose", got)
	}
	assertClaimSnapshot(t, tenants, "close-key-duplicado", duplicate.Body.String())
}

// TestCashCloseReplaysTheSameKeyWithoutASecondClose: Branch A del §7.1 contra la base real —
// la MISMA clave con el MISMO cuerpo devuelve el snapshot ALMACENADO byte a byte y no deja
// una segunda escritura.
func TestCashCloseReplaysTheSameKeyWithoutASecondClose(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedCloseUser(t, db, tokenTenantID, tokenUserID)
	seedLedgerRow(t, db, ledgerRow{saleID: cashSaleID, tenantID: tokenTenantID, userID: tokenUserID, soldOn: businessDate, kind: "sale", paymentMethod: "cash", totalCents: 5000})

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	router, issuer := newCashCloseIntegrationRouter(t, tenants, &strings.Builder{})

	first := doCloseRequest(t, router, closeToken(t, issuer), countedCloseBody(), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("estado del primer cierre = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	replay := doCloseRequest(t, router, closeToken(t, issuer), countedCloseBody(), nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("estado de la repetición = %d, want 200 (cuerpo %q)", replay.Code, replay.Body.String())
	}
	if replay.Body.String() != first.Body.String() {
		t.Errorf("cuerpo repetido = %q, want el snapshot ALMACENADO byte a byte %q", replay.Body.String(), first.Body.String())
	}

	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_closes`); got != 1 {
		t.Errorf("cash_closes = %d fila(s), want 1: el replay no cierra dos veces", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM cash_close_counts`); got != 6 {
		t.Errorf("cash_close_counts = %d fila(s), want 6: el replay no duplica el desglose", got)
	}
	if got := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM idempotency_keys WHERE key = $1 AND state = 'completed'`,
		defaultIdempotencyKey); got != 1 {
		t.Errorf("claims completados de la clave = %d, want 1", got)
	}
}

// newCashCloseIntegrationRouter arma el router REAL con el store de POSTGRES sobre el pool
// de `pos_app` de la base desechable, igual que cmd/api lo monte.
func newCashCloseIntegrationRouter(t *testing.T, tenants *postgres.TenantPool, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	handler, err := cashclose.NewCashCloseHandler(cashclose.NewPostgresCashCloseStore(tenants), slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewCashCloseHandler: %v", err)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	return httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /cash-closes", identity.RequireAccessToken(issuer, handler))
		}),
	), issuer
}

// countedCloseBody es el cuerpo de referencia del cierre: seis filas con el conteo del
// operador. El esperado nunca viaja en el cuerpo.
func countedCloseBody() string {
	return `{
		"id": "` + closeID + `",
		"closed_on": "` + businessDate + `",
		"counts": [
			{"payment_method": "cash", "counted_cents": 4500},
			{"payment_method": "yape", "counted_cents": 3000},
			{"payment_method": "plin", "counted_cents": 0},
			{"payment_method": "card", "counted_cents": 0},
			{"payment_method": "other", "counted_cents": 0},
			{"payment_method": "fiado", "counted_cents": 0}
		]
	}`
}

// decodeCloseAck decodifica el acuse de §4.13.
func decodeCloseAck(t *testing.T, recorder *httptest.ResponseRecorder) closeAckBody {
	t.Helper()

	var ack closeAckBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &ack); err != nil {
		t.Fatalf("el acuse no es JSON: %v (%q)", err, recorder.Body.String())
	}
	return ack
}

// assertClaimSnapshot exige que la clave haya quedado completada con el acuse EXACTO que la
// respuesta transportó (salvo el newline de transporte del encoder).
func assertClaimSnapshot(t *testing.T, tenants *postgres.TenantPool, key, body string) {
	t.Helper()

	withTenant(t, tenants, tokenTenantID, func(tx postgres.TenantTx) {
		var (
			snapshot string
			state    string
		)
		if err := tx.QueryRow(context.Background(), `SELECT COALESCE(response_snapshot, ''), state FROM idempotency_keys WHERE key = $1`, key).
			Scan(&snapshot, &state); err != nil {
			t.Fatalf("leer el claim %q: %v", key, err)
		}
		if state != "completed" {
			t.Errorf("estado del claim = %q, want completed", state)
		}
		if snapshot != strings.TrimSuffix(body, "\n") {
			t.Errorf("snapshot almacenado = %q, want el acuse EXACTO de la respuesta", snapshot)
		}
	})
}
