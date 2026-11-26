// Prueba de integración de `POST /auth/admin-login` (W8-BE-1) contra una base migrada de
// verdad: el rol pre-auth real (`pos_auth`), la concesión por columna sobre `tenants`, y el
// tenant recién creado con el hash inutilizable de W8-DB-1.
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package identity_test

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

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// adminFixtureIntegration siembra un tenant con su usuario y su contraseña maestra real.
func adminFixtureIntegration(t *testing.T, db *testDatabase) loginFixture {
	t.Helper()

	fixture := loginFixture{
		label:       "admin",
		tenantID:    "019c0000-0000-7000-8000-0000000000d1",
		userID:      "019c0000-0000-7000-8000-0000000000d2",
		phone:       "+51988000333",
		password:    "contraseña-del-empleado",
		productID:   "019c0000-0000-7000-8000-0000000000d3",
		productName: "producto del tenant",
	}
	fixture.seed(t, db)

	if err := identity.SetMasterPassword(context.Background(), db.owner,
		mustUUID(t, fixture.tenantID), adminPassword); err != nil {
		t.Fatalf("SetMasterPassword del fixture: %v", err)
	}
	return fixture
}

// TestAdminLoginOverTheRealCredentials: el ciclo completo —teléfono → usuario → tenant, hash
// maestro verificado, token con `role:"admin"` y nombre del tenant en la respuesta— contra la
// base real.
func TestAdminLoginOverTheRealCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("integración: necesita PostgreSQL")
	}

	db := newThrowawayDatabase(t)
	fixture := adminFixtureIntegration(t, db)

	authPool := db.rolePool(t, config.AuthDatabaseURL(), "pos_auth")
	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := identity.NewAdminLoginHandler(
		identity.NewPostgresUserDirectory(authPool),
		identity.NewPostgresTenantDirectory(authPool),
		issuer,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAdminLoginHandler: %v", err)
	}

	router := httpx.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), alwaysReady{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /auth/admin-login", handler)
		}))

	request := httptest.NewRequest(http.MethodPost, "/auth/admin-login",
		strings.NewReader(adminLoginBody(fixture.phone, adminPassword)))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%q)", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Token  string `json:"token"`
		Tenant struct {
			Name string `json:"name"`
		} `json:"tenant"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (%q)", err, recorder.Body.String())
	}
	if body.Tenant.Name != "tenant admin" {
		t.Errorf("tenant.name = %q, want el nombre sembrado", body.Tenant.Name)
	}

	claims, err := issuer.Verify(body.Token)
	if err != nil {
		t.Fatalf("el token no verifica: %v", err)
	}
	if !claims.IsAdmin() {
		t.Errorf("role = %q, want admin", claims.Role)
	}
	if claims.TenantID.String() != fixture.tenantID || claims.UserID.String() != fixture.userID {
		t.Errorf("claims = %s/%s, want %s/%s", claims.TenantID, claims.UserID, fixture.tenantID, fixture.userID)
	}
}

// TestAdminLoginRefusesATenantWhosePasswordWasNeverSet: el tenant recién creado tiene el hash
// inutilizable de W8-DB-1, así que el login falla con el mismo 401 genérico que una contraseña
// incorrecta —las dos piezas del sprint se prueban juntas—.
func TestAdminLoginRefusesATenantWhosePasswordWasNeverSet(t *testing.T) {
	if testing.Short() {
		t.Skip("integración: necesita PostgreSQL")
	}

	db := newThrowawayDatabase(t)

	fixture := loginFixture{
		label:       "sin-clave",
		tenantID:    "019c0000-0000-7000-8000-0000000000e1",
		userID:      "019c0000-0000-7000-8000-0000000000e2",
		phone:       "+51988000444",
		password:    "contraseña-del-empleado",
		productID:   "019c0000-0000-7000-8000-0000000000e3",
		productName: "producto",
	}
	fixture.seed(t, db)

	authPool := db.rolePool(t, config.AuthDatabaseURL(), "pos_auth")
	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := identity.NewAdminLoginHandler(
		identity.NewPostgresUserDirectory(authPool),
		identity.NewPostgresTenantDirectory(authPool),
		issuer,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAdminLoginHandler: %v", err)
	}

	// La contraseña del empleado y una adivinanza: ninguna entra por el camino del dueño. La
	// credencial vacía no se prueba acá porque es un 422 de validación, no un intento de login.
	bodies := []string{}
	for _, attempt := range []string{fixture.password, "admin"} {
		request := httptest.NewRequest(http.MethodPost, "/auth/admin-login",
			strings.NewReader(adminLoginBody(fixture.phone, attempt)))
		request.Header.Set("Content-Type", "application/json")

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("intento %q: status = %d, want 401 (%q)", attempt, recorder.Code, recorder.Body.String())
		}
		bodies = append(bodies, recorder.Body.String())
	}

	// Y el 401 del tenant sin contraseña es el MISMO que el de un teléfono inexistente.
	request := httptest.NewRequest(http.MethodPost, "/auth/admin-login",
		strings.NewReader(adminLoginBody("+51900000000", "lo-que-sea")))
	request.Header.Set("Content-Type", "application/json")
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, request)

	if unknown.Body.String() != bodies[0] {
		t.Errorf("el 401 de un teléfono inexistente no es constante:\n got: %q\nwant: %q",
			unknown.Body.String(), bodies[0])
	}
}
