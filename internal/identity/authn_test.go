package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// Identificadores de las pruebas del middleware. Son distintos de los que usan las pruebas de
// login y de token para que cada caso hable de su propia identidad.
const (
	// tokenTenantID es el tenant que viaja DENTRO del token: la única fuente admisible.
	tokenTenantID = "019a0000-0000-7000-8000-0000000000f1"
	// tokenUserID es el usuario del token.
	tokenUserID = "019a0000-0000-7000-8000-0000000000f2"
	// forgedTenantID es el tenant que las peticiones falsifican en el cuerpo o en un
	// encabezado. Encabezado no es fuente de tenant (ARCHITECTURE.md:23).
	forgedTenantID = "019a0000-0000-7000-8000-0000000000f9"
)

// claimsForToken arma los claims del token de empleado de las pruebas: el tenant y el usuario
// de arriba, sin claim de rol.
func claimsForToken() identity.Claims {
	return identity.Claims{
		TenantID: uuid.MustParse(tokenTenantID),
		UserID:   uuid.MustParse(tokenUserID),
	}
}

// middlewareProbe es el handler interno de las pruebas: registra si la cadena llegó hasta él y
// con qué claims. Es el instrumento que mide «el middleware dejó pasar» y «el middleware
// bloqueó antes del handler».
type middlewareProbe struct {
	calls  int
	claims identity.Claims
	ok     bool
}

func (p *middlewareProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.calls++
	p.claims, p.ok = identity.ClaimsFromContext(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"reached": "yes"})
}

// newProbeChain monta el middleware sobre el handler interno con un emisor de una hora.
func newProbeChain(t *testing.T) (http.Handler, *identity.TokenIssuer, *middlewareProbe) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	probe := &middlewareProbe{}
	return identity.RequireAccessToken(issuer, probe), issuer, probe
}

// expiredToken firma un token de empleado con la clave de las pruebas y una expiración que ya
// pasó. Se arma a mano y no con el emisor porque `Issue` no permite emitir un token vencido.
func expiredToken(t *testing.T) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": tokenTenantID,
		"user_id":   tokenUserID,
		"exp":       jwt.NewNumericDate(time.Now().Add(-time.Hour)),
	}).SignedString([]byte(testSigningSecret))
	if err != nil {
		t.Fatalf("firmar un token vencido: %v", err)
	}
	return signed
}

// TestRequireAccessTokenRejectsAMissingOrMalformedToken cubre el disparador del 401 de la ruta
// autenticada: cualquier forma de credencial que no sea un access token verificado. En todos
// los casos la respuesta es el sobre de error único y el handler interno no corre.
func TestRequireAccessTokenRejectsAMissingOrMalformedToken(t *testing.T) {
	handler, issuer, probe := newProbeChain(t)

	valid, err := issuer.Issue(claimsForToken())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Un emisor con OTRA clave produce un token bien formado que este proceso no debe aceptar:
	// sin esta comprobación, «rechaza lo inválido» podría estar rechazando cualquier cosa.
	otherIssuer, err := identity.NewTokenIssuer("otra-clave-de-firma-de-pruebas-0123456789", time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer (otra clave): %v", err)
	}
	otherSigned, err := otherIssuer.Issue(claimsForToken())
	if err != nil {
		t.Fatalf("Issue (otra clave): %v", err)
	}

	cases := map[string]string{
		"absent header":                   "",
		"another scheme":                  "Basic dXNhcmlvOnNlY3JldG8=",
		"the scheme without a token":      "Bearer",
		"an empty bearer token":           "Bearer ",
		"a blank bearer token":            "Bearer     ",
		"a token that is not a jwt":       "Bearer not-a-jwt",
		"a jwt body without the header":   "eyJhbGciOiJIUzI1NiJ9.eyJ0ZW5hbnRfaWQiOiJ4In0",
		"a token signed with another key": "Bearer " + otherSigned,
		"an expired token":                "Bearer " + expiredToken(t),
	}

	for name, authorization := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(`{"pin":"1234"}`))
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if probe.calls != 0 {
				t.Errorf("el handler interno corrió %d vez/veces con una credencial no válida, want 0", probe.calls)
			}

			// El sobre es único (§4.1): se decodifica con el mismo tipo que usa login_test.go.
			var envelope loginEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("la respuesta no es el sobre de error único: %v (%q)", err, recorder.Body.String())
			}
			if envelope.Error.Code != httpx.CodeUnauthenticated {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeUnauthenticated)
			}
			if envelope.Error.Message == "" {
				t.Error("el 401 no trae mensaje humano")
			}
			if strings.Contains(recorder.Body.String(), "tenant_id") {
				t.Errorf("el 401 describe la forma del token: %q", recorder.Body.String())
			}
		})
	}

	// El control positivo: con ESTE token la cadena pasa. Sin él, la tabla de arriba probaría
	// que el middleware rechaza, pero no que sepa aceptar lo que el contrato define como válido.
	probe.calls = 0
	request := httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(`{"pin":"1234"}`))
	request.Header.Set("Authorization", "Bearer "+valid)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("el control positivo falló: estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if probe.calls != 1 {
		t.Errorf("el handler interno corrió %d vez/veces con el token válido, want 1", probe.calls)
	}
}

// TestRequireAccessTokenPutsTheClaimsInTheContext es la mitad positiva del middleware: con un
// token válido el handler interno corre y lee el tenant y el usuario del TOKEN. La petición
// lleva el tenant falsificado en el encabezado y el middleware no lo mira.
func TestRequireAccessTokenPutsTheClaimsInTheContext(t *testing.T) {
	handler, issuer, probe := newProbeChain(t)

	token, err := issuer.Issue(claimsForToken())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(`{"pin":"1234"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	// Los encabezados de tenant que el contrato prohíbe: ninguno es fuente del tenant.
	request.Header.Set("X-Tenant-Id", forgedTenantID)
	request.Header.Set("X-Tenant", forgedTenantID)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if probe.calls != 1 {
		t.Fatalf("el handler interno corrió %d vez/veces, want 1", probe.calls)
	}
	if !probe.ok {
		t.Fatal("el handler interno no encontró los claims en el contexto")
	}
	if probe.claims != claimsForToken() {
		t.Errorf("claims = %+v, want %+v", probe.claims, claimsForToken())
	}
	if probe.claims.TenantID.String() == forgedTenantID {
		t.Errorf("el middleware tomó el tenant del encabezado: %v", probe.claims.TenantID)
	}
}

// TestRequireAccessTokenAcceptsTheBearerSchemeInAnyCase: el esquema de RFC 7235 se compara sin
// distinguir mayúsculas, y un token válido no depende de cómo lo escribió el cliente.
func TestRequireAccessTokenAcceptsTheBearerSchemeInAnyCase(t *testing.T) {
	handler, issuer, probe := newProbeChain(t)

	token, err := issuer.Issue(claimsForToken())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		probe.calls = 0

		request := httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(`{"pin":"1234"}`))
		request.Header.Set("Authorization", scheme+" "+token)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Errorf("[%s] estado = %d, want 200 (cuerpo %q)", scheme, recorder.Code, recorder.Body.String())
		}
		if probe.calls != 1 {
			t.Errorf("[%s] el handler interno corrió %d vez/veces, want 1", scheme, probe.calls)
		}
	}
}

// TestClaimsFromContextWithoutTheMiddleware documenta la ausencia como ausencia: un contexto
// sin claims no devuelve un tenant cero que se pueda confundir con un tenant real.
func TestClaimsFromContextWithoutTheMiddleware(t *testing.T) {
	claims, ok := identity.ClaimsFromContext(context.Background())
	if ok {
		t.Errorf("ClaimsFromContext devolvió ok con un contexto vacío: %+v", claims)
	}
	if claims.TenantID != uuid.Nil || claims.UserID != uuid.Nil || claims.Role != "" {
		t.Errorf("claims = %+v, want los tres campos en cero", claims)
	}
}
