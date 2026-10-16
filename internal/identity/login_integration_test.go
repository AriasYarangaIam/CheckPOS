// Prueba de integración de `POST /auth/login` (W2-BE-1) contra una base migrada de verdad,
// con el rol pre-auth real (`pos_auth`) y el rol de aplicación real (`pos_app`).
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package identity_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// alwaysReady es el sondeo de preparación del router de estas pruebas: la base ya está
// verificada por el arnés, y /readyz no es lo que se está probando.
type alwaysReady struct{}

func (alwaysReady) Ping(context.Context) error { return nil }

// setTenantSettingSQL fija el contexto de tenant de la transacción actual. `SET LOCAL` no
// acepta marcadores de posición, así que la forma parametrizable es `set_config(..., true)`,
// con el tercer argumento ligado a la transacción (decisión D4; docs/DATABASE.md §5.2, §5.3).
const setTenantSettingSQL = `SELECT set_config('app.current_tenant_id', $1, true)`

// loginFixture es un tenant con su usuario y su producto: lo mínimo para que el login tenga
// algo que resolver y para que exista una lectura que consuma el tenant.
type loginFixture struct {
	label       string
	tenantID    string
	userID      string
	phone       string
	password    string
	productID   string
	productName string
}

// seed inserta la siembra completa como DUEÑO, con el contexto de tenant fijado en la misma
// transacción: `users` y `products` están bajo FORCE RLS, así que la escritura pasa por el
// mismo camino que la aplicación (docs/DATABASE.md §5.2).
func (f loginFixture) seed(t *testing.T, db *testDatabase) {
	t.Helper()

	passwordHash, err := identity.HashCredential(f.password)
	if err != nil {
		t.Fatalf("HashCredential del fixture %s: %v", f.label, err)
	}

	ctx := context.Background()
	tx, err := db.owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("abrir la transacción de siembra del tenant %s: %v", f.label, err)
	}

	execOn(t, tx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, now())`, f.tenantID, "tenant "+f.label)

	var setting string
	if err := tx.QueryRowContext(ctx, setTenantSettingSQL, f.tenantID).Scan(&setting); err != nil {
		t.Fatalf("fijar el contexto de tenant del fixture %s: %v", f.label, err)
	}

	execOn(t, tx, `
		INSERT INTO users (id, tenant_id, phone, password_hash, created_at)
		VALUES ($1, $2, $3, $4, now())`, f.userID, f.tenantID, f.phone, passwordHash)

	execOn(t, tx, `
		INSERT INTO products (id, tenant_id, name, gtin, price_cents, uom, created_at, updated_at, received_at)
		VALUES ($1, $2, $3, NULL, 1000, 'unit', now(), now(), now())`, f.productID, f.tenantID, f.productName)

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la siembra del tenant %s: %v", f.label, err)
	}
}

// productNamesWithTenantContext lee el catálogo con el contexto de tenant fijado y devuelve
// los nombres visibles.
//
// Es el mecanismo que CONSUME un tenant: el GUC como primera sentencia de la transacción y las
// políticas de RLS filtrando por `current_setting('app.current_tenant_id')::uuid`. Sin este
// camino, «el tenant sale solo del token» no sería comprobable: lo que se prueba no es que el
// handler ignore un campo, sino que el tenant que termina aplicándose es el del token.
//
// El envoltorio del pool con este mismo contrato es el helper de transacción de tenant
// (`postgres.WithTenant`, W2-BE-2).
func productNamesWithTenantContext(t *testing.T, pool *pgxpool.Pool, tenantID string) []string {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir la transacción de tenant %s: %v", tenantID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, setTenantSettingSQL, tenantID); err != nil {
		t.Fatalf("fijar el contexto de tenant %s: %v", tenantID, err)
	}

	rows, err := tx.Query(ctx, `SELECT name FROM products ORDER BY name`)
	if err != nil {
		t.Fatalf("leer products con el contexto de tenant %s: %v", tenantID, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("leer el nombre de un producto: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer products: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar la transacción de tenant %s: %v", tenantID, err)
	}
	return names
}

// productNamesWithoutTenantContext lee el catálogo con el pool crudo, sin fijar ningún
// contexto: es la forma fail-closed del contrato, y el punto de partida que hace falsable
// cualquier lectura posterior (docs/DATABASE.md §5.7).
func productNamesWithoutTenantContext(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(context.Background(), `SELECT name FROM products ORDER BY name`)
	if err != nil {
		t.Fatalf("leer products sin contexto de tenant: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("leer el nombre de un producto: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recorrer products: %v", err)
	}
	return names
}

// newLoginServer arma el router real del proceso —los sondeos, el registro de peticiones y la
// ruta montada por opción— sobre un servidor HTTP de verdad, y devuelve su URL.
func newLoginServer(t *testing.T, authPool *pgxpool.Pool, logBuf *strings.Builder) *httptest.Server {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := identity.NewLoginHandler(
		identity.NewPostgresUserDirectory(authPool),
		issuer,
		slog.New(slog.NewTextHandler(logBuf, nil)),
	)
	if err != nil {
		t.Fatalf("NewLoginHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /auth/login", handler)
		}),
	)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

// postLoginTo envía un cuerpo al servidor y devuelve la respuesta.
func postLoginTo(t *testing.T, server *httptest.Server, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/auth/login", strings.NewReader(body))
	if err != nil {
		t.Fatalf("armar la petición: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("enviar la petición: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("leer la respuesta: %v", err)
	}
	return response, string(raw)
}

// execOn ejecuta una sentencia sobre una transacción de database/sql.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func execOn(t *testing.T, q querier, query string, args ...any) {
	t.Helper()

	if _, err := q.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("ejecutar %q: %v", strings.Join(strings.Fields(query), " "), err)
	}
}

// TestLoginScopesTheRequestToTheTokensTenant es el criterio de aceptación de W2-BE-1: una
// petición con un tenant falsificado en el cuerpo Y en el encabezado `X-Tenant-Id` queda
// acotada al tenant del token que devuelve.
//
// La prueba no es una tautología porque la lectura se hace con el mecanismo que consume un
// tenant —el contexto de RLS sobre `pos_app`— y porque hay un CONTROL que demuestra que esa
// lectura discrimina: con el contexto del tenant B la misma consulta devuelve el producto de
// B. Si el endpoint hubiera usado el tenant falsificado, la lectura habría mostrado el
// catálogo de B, y eso se ve.
func TestLoginScopesTheRequestToTheTokensTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	tenantA := loginFixture{
		label:       "A",
		tenantID:    "019a0000-0000-7000-8000-0000000000a1",
		userID:      "019a0000-0000-7000-8000-0000000000a2",
		phone:       "+51900000101",
		password:    "secreto-del-tenant-a",
		productID:   "019a0000-0000-7000-8000-0000000000a3",
		productName: "producto de A",
	}
	tenantB := loginFixture{
		label:       "B",
		tenantID:    "019a0000-0000-7000-8000-0000000000b1",
		userID:      "019a0000-0000-7000-8000-0000000000b2",
		phone:       "+51900000102",
		password:    "secreto-del-tenant-b",
		productID:   "019a0000-0000-7000-8000-0000000000b3",
		productName: "producto de B",
	}

	tenantA.seed(t, db)
	tenantB.seed(t, db)

	appPool := db.rolePool(t, config.AppDatabaseURL(), "pos_app")
	authPool := db.rolePool(t, config.AuthDatabaseURL(), "pos_auth")

	// El estado de partida, medido: la lectura de catálogo discrimina. Sin contexto no hay
	// ninguna fila, y cada contexto ve exactamente la suya. Sin estas dos mediciones, la
	// aserción de abajo no probaría que la lectura depende del tenant.
	if got := productNamesWithoutTenantContext(t, appPool); len(got) != 0 {
		t.Fatalf("products sin contexto de tenant = %v, want ninguna fila (fail-closed)", got)
	}
	if got := productNamesWithTenantContext(t, appPool, tenantB.tenantID); len(got) != 1 || got[0] != tenantB.productName {
		t.Fatalf("products con el contexto de B = %v, want [%q]: la lectura no discrimina y la prueba no probaría nada", got, tenantB.productName)
	}

	var logBuf strings.Builder
	server := newLoginServer(t, authPool, &logBuf)

	// La petición lleva el tenant de B en el cuerpo y en el encabezado, y las credenciales de
	// A. El contrato dice que el tenant sale SOLO del token.
	body := `{"phone":"` + tenantA.phone + `","password":"` + tenantA.password + `",` +
		`"tenant_id":"` + tenantB.tenantID + `","tenantId":"` + tenantB.tenantID + `"}`
	response, raw := postLoginTo(t, server, body, map[string]string{"X-Tenant-Id": tenantB.tenantID})

	if response.StatusCode != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", response.StatusCode, raw)
	}

	var payload struct {
		Token string `json:"token"`
		User  struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("la respuesta no es el JSON del contrato §4.2: %v (%q)", err, raw)
	}
	if payload.Token == "" {
		t.Fatalf("la respuesta no trae token: %q", raw)
	}
	if payload.User.ID != tenantA.userID {
		t.Errorf("user.id = %q, want %q (el usuario que resolvió el teléfono)", payload.User.ID, tenantA.userID)
	}
	// El tenant no viaja en el cuerpo de la respuesta: viaja dentro del token (§4.2).
	if strings.Contains(raw, tenantB.tenantID) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", raw)
	}
	if strings.Contains(raw, `"tenant`) {
		t.Errorf("la respuesta expone un tenant en el cuerpo, want solo token y user.id: %q", raw)
	}

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}
	claims, err := issuer.Verify(payload.Token)
	if err != nil {
		t.Fatalf("el token emitido no verifica: %v", err)
	}
	if claims.TenantID.String() != tenantA.tenantID {
		t.Fatalf("claims.TenantID = %v, want el del usuario resuelto %s (y NO el falsificado %s)",
			claims.TenantID, tenantA.tenantID, tenantB.tenantID)
	}
	if claims.UserID.String() != tenantA.userID {
		t.Errorf("claims.UserID = %v, want %s", claims.UserID, tenantA.userID)
	}
	if claims.Role != "" {
		t.Errorf("claims.Role = %q, want vacío: el login emite un token de empleado", claims.Role)
	}

	// La prueba del criterio: el tenant que hay que aplicar es el del token, y con ese contexto
	// la lectura ve el catálogo de A y no el de B.
	visible := productNamesWithTenantContext(t, appPool, claims.TenantID.String())
	if len(visible) != 1 || visible[0] != tenantA.productName {
		t.Errorf("products con el tenant del token = %v, want [%q]", visible, tenantA.productName)
	}
	for _, name := range visible {
		if name == tenantB.productName {
			t.Errorf("la lectura con el tenant del token devolvió %q, que es del tenant B", name)
		}
	}

	// El control cerró el círculo: con el contexto de B la misma lectura devuelve el otro
	// producto, así que haber aplicado el tenant falsificado se habría visto.
	if got := productNamesWithTenantContext(t, appPool, tenantB.tenantID); len(got) != 1 || got[0] != tenantB.productName {
		t.Fatalf("el control cambió: products con el contexto de B = %v, want [%q]", got, tenantB.productName)
	}

	// §7.9: el teléfono, la contraseña, el hash y el token no aparecen en el log del proceso.
	for _, forbidden := range []string{tenantA.phone, tenantA.password, payload.Token} {
		if strings.Contains(logBuf.String(), forbidden) {
			t.Errorf("el log del login contiene %q: %q", forbidden, logBuf.String())
		}
	}
}

// TestLoginAgainstTheRealPosAuthRole cubre el camino pre-auth del §5.4 sobre la base migrada:
// el teléfono se resuelve con el rol `pos_auth`, que lee `users` y nada más, y una contraseña
// incorrecta y un teléfono desconocido devuelven respuestas idénticas.
func TestLoginAgainstTheRealPosAuthRole(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	fixture := loginFixture{
		label:       "A",
		tenantID:    "019a0000-0000-7000-8000-0000000000a1",
		userID:      "019a0000-0000-7000-8000-0000000000a2",
		phone:       "+51900000101",
		password:    "el-secreto-correcto",
		productID:   "019a0000-0000-7000-8000-0000000000a3",
		productName: "producto de A",
	}
	fixture.seed(t, db)

	authPool := db.rolePool(t, config.AuthDatabaseURL(), "pos_auth")
	server := newLoginServer(t, authPool, &strings.Builder{})

	t.Run("valid credentials issue an employee token", func(t *testing.T) {
		response, raw := postLoginTo(t, server, `{"phone":"`+fixture.phone+`","password":"`+fixture.password+`"}`, nil)

		if response.StatusCode != http.StatusOK {
			t.Fatalf("estado = %d, want 200 (cuerpo %q)", response.StatusCode, raw)
		}
	})

	cases := map[string]string{
		"wrong password": `{"phone":"` + fixture.phone + `","password":"el-equivocado"}`,
		"unknown phone":  `{"phone":"+51900009999","password":"` + fixture.password + `"}`,
	}

	var bodies []string
	for name, body := range cases {
		response, raw := postLoginTo(t, server, body, nil)

		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("[%s] estado = %d, want 401 (cuerpo %q)", name, response.StatusCode, raw)
		}

		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			t.Fatalf("[%s] la respuesta no es el sobre de error único: %v (%q)", name, err, raw)
		}
		if envelope.Error.Code != httpx.CodeUnauthenticated {
			t.Errorf("[%s] error.code = %q, want %q", name, envelope.Error.Code, httpx.CodeUnauthenticated)
		}
		if strings.Contains(raw, fixture.phone) || strings.Contains(raw, "el-equivocado") {
			t.Errorf("[%s] la respuesta repite datos de la credencial: %q", name, raw)
		}

		bodies = append(bodies, raw)
	}

	if bodies[0] != bodies[1] {
		t.Errorf("las respuestas de «contraseña incorrecta» y «teléfono desconocido» difieren y enumeran usuarios:\n  %q\n  %q", bodies[0], bodies[1])
	}
}

// TestLoginFailsClosedWhenTheStoreIsBroken cubre que un fallo de la base no se confunda con
// credenciales inválidas: la respuesta es 500 con el sobre genérico y la causa no llega al
// cliente.
func TestLoginFailsClosedWhenTheStoreIsBroken(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)
	fixture := loginFixture{
		label:       "A",
		tenantID:    "019a0000-0000-7000-8000-0000000000a1",
		userID:      "019a0000-0000-7000-8000-0000000000a2",
		phone:       "+51900000101",
		password:    "el-secreto-correcto",
		productID:   "019a0000-0000-7000-8000-0000000000a3",
		productName: "producto de A",
	}
	fixture.seed(t, db)

	authPool := db.rolePool(t, config.AuthDatabaseURL(), "pos_auth")
	authPool.Close()

	var logBuf strings.Builder
	server := newLoginServer(t, authPool, &logBuf)

	response, raw := postLoginTo(t, server, `{"phone":"`+fixture.phone+`","password":"`+fixture.password+`"}`, nil)

	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500 con el pool cerrado (cuerpo %q)", response.StatusCode, raw)
	}

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error único: %v (%q)", err, raw)
	}
	if envelope.Error.Code != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeInternalError)
	}
	if strings.Contains(raw, "pool") || strings.Contains(raw, fixture.phone) {
		t.Errorf("el cuerpo del 500 filtró la causa o el teléfono: %q", raw)
	}
	if !strings.Contains(logBuf.String(), "pool") {
		t.Errorf("la causa no llegó al log del operador: %q", logBuf.String())
	}
}
