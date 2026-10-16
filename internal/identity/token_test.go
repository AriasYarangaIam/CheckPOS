package identity_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
)

// testSigningSecret es la clave con la que se firman los tokens de estas pruebas. Tiene 32
// bytes o más, como el contrato pide para HS256 (docs/BACKEND.md §2.5).
const testSigningSecret = "test-only-signing-key-0123456789abcdef"

var (
	testTenantID = uuid.MustParse("019a0000-0000-7000-8000-0000000000a1")
	testUserID   = uuid.MustParse("019a0000-0000-7000-8000-0000000000a2")
)

// newIssuer construye un emisor con la clave y la vida de las pruebas.
func newIssuer(t *testing.T, ttl time.Duration) *identity.TokenIssuer {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testSigningSecret, ttl)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}
	return issuer
}

// rawClaims devuelve el cuerpo del token SIN pasar por el verificador: es la única forma de
// afirmar un claim que NO está, como la ausencia de `role` del token de empleado.
func rawClaims(t *testing.T, token string) map[string]any {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("el token tiene %d partes separadas por '.', want 3 (cabecera, cuerpo, firma)", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decodificar el cuerpo del token: %v", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("leer el cuerpo del token: %v", err)
	}
	return claims
}

// TestIssueAndVerifyRoundTrip cubre el camino completo del contrato de claims de
// docs/BACKEND.md §5.1: lo que se emite vuelve igual, y el tenant verificado es el emitido.
func TestIssueAndVerifyRoundTrip(t *testing.T) {
	issuer := newIssuer(t, time.Hour)

	token, err := issuer.Issue(identity.Claims{TenantID: testTenantID, UserID: testUserID})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Errorf("el token = %q, want un JWT de tres partes", token)
	}

	claims, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.TenantID != testTenantID {
		t.Errorf("claims.TenantID = %v, want %v (el tenant del token es la única fuente del tenant de la petición)", claims.TenantID, testTenantID)
	}
	if claims.UserID != testUserID {
		t.Errorf("claims.UserID = %v, want %v", claims.UserID, testUserID)
	}
	if claims.Role != "" {
		t.Errorf("claims.Role = %q, want la cadena vacía: /auth/login emite un token de empleado", claims.Role)
	}
}

// TestEmployeeTokenCarriesNoRoleClaim es la propiedad central del §5.1: `role` está presente
// SOLO en un token de administrador; su ausencia significa empleado. La aserción se hace
// sobre el JSON firmado, no sobre la estructura ya parseada: si `role` se emitiera con valor
// vacío o con "employee", el verificador devolvería lo mismo y el contrato estaría roto.
func TestEmployeeTokenCarriesNoRoleClaim(t *testing.T) {
	issuer := newIssuer(t, time.Hour)

	t.Run("an employee token has no role claim at all", func(t *testing.T) {
		token, err := issuer.Issue(identity.Claims{TenantID: testTenantID, UserID: testUserID})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}

		claims := rawClaims(t, token)
		if role, present := claims["role"]; present {
			t.Errorf("el token de empleado lleva el claim role = %#v, want que el claim NO exista (ARCHITECTURE.md:23)", role)
		}

		for _, required := range []string{"tenant_id", "user_id", "exp"} {
			if _, present := claims[required]; !present {
				t.Errorf("el token de empleado no lleva el claim %q, want que esté (§5.1)", required)
			}
		}
	})

	t.Run("an admin token carries role admin", func(t *testing.T) {
		token, err := issuer.Issue(identity.Claims{TenantID: testTenantID, UserID: testUserID, Role: identity.RoleAdmin})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}

		if got, want := rawClaims(t, token)["role"], identity.RoleAdmin; got != want {
			t.Errorf("el token de administrador lleva role = %#v, want %q (§4.4)", got, want)
		}

		claims, err := issuer.Verify(token)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if claims.Role != identity.RoleAdmin {
			t.Errorf("claims.Role = %q, want %q", claims.Role, identity.RoleAdmin)
		}
	})
}

// TestExpirationIsMandatoryAndEnforced cubre la parte de `exp` del §5.1: todo token lleva
// expiración y un token vencido se rechaza. La vida del token NO es la ventana offline del
// PIN, que vive en `devices.offline_session_valid_until` (§5.2).
func TestExpirationIsMandatoryAndEnforced(t *testing.T) {
	t.Run("an expired token is rejected", func(t *testing.T) {
		// Un token con `exp` en el pasado, firmado con la misma clave: la expiración se
		// verifica, no se declara.
		expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"tenant_id": testTenantID.String(),
			"user_id":   testUserID.String(),
			"exp":       jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		}).SignedString([]byte(testSigningSecret))
		if err != nil {
			t.Fatalf("firmar el token vencido: %v", err)
		}

		if _, err := newIssuer(t, time.Hour).Verify(expired); err == nil {
			t.Fatal("Verify aceptó un token vencido, want un error (un token sin vida útil es una sesión eterna)")
		}
	})

	t.Run("a token without exp is rejected", func(t *testing.T) {
		// Se firma a mano un token con la misma clave pero sin `exp`: `WithExpirationRequired`
		// tiene que rechazarlo, porque el §5.1 marca exp como requerido.
		unsigned, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"tenant_id": testTenantID.String(),
			"user_id":   testUserID.String(),
		}).SignedString([]byte(testSigningSecret))
		if err != nil {
			t.Fatalf("firmar el token sin exp: %v", err)
		}

		if _, err := newIssuer(t, time.Hour).Verify(unsigned); err == nil {
			t.Fatal("Verify aceptó un token sin `exp`, want un error")
		}
	})

	t.Run("the expiry is now plus the configured lifetime", func(t *testing.T) {
		const ttl = 90 * time.Minute
		issuedAt := time.Now()

		token, err := newIssuer(t, ttl).Issue(identity.Claims{TenantID: testTenantID, UserID: testUserID})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}

		expiry, ok := rawClaims(t, token)["exp"].(float64)
		if !ok {
			t.Fatal("el claim exp no es un número")
		}

		// Una tolerancia de un minuto cubre el tiempo que tarda la prueba, y sigue siendo
		// mucho menor que la diferencia entre la vida configurada y cualquier otra.
		got := time.Unix(int64(expiry), 0)
		if want := issuedAt.Add(ttl); got.Before(want.Add(-time.Minute)) || got.After(want.Add(time.Minute)) {
			t.Errorf("exp = %v, want alrededor de %v (emisión + la vida configurada)", got, want)
		}
	})
}

// TestVerifyRejectsTamperedAndForgedTokens cubre que la verificación sea real: firma alterada,
// cuerpo alterado, otra clave y el algoritmo `none`. Es el caso de alguien que edita el token
// para cambiar de tenant.
func TestVerifyRejectsTamperedAndForgedTokens(t *testing.T) {
	issuer := newIssuer(t, time.Hour)

	valid, err := issuer.Issue(identity.Claims{TenantID: testTenantID, UserID: testUserID})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(valid, ".")

	flip := func(field string) string {
		if field[0] == 'A' {
			return "B" + field[1:]
		}
		return "A" + field[1:]
	}

	// El mismo token de otro tenant, firmado con otra clave: la firma no puede validar.
	otherSecret := "another-test-signing-key-0123456789abcdef"
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": uuid.MustParse("019a0000-0000-7000-8000-0000000000b1").String(),
		"user_id":   testUserID.String(),
		"exp":       jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte(otherSecret))
	if err != nil {
		t.Fatalf("firmar el token con otra clave: %v", err)
	}

	// El algoritmo `none`: un token sin firma. La verificación fija HS256, así que este
	// camino tiene que quedar cerrado y no depender de la configuración de la biblioteca.
	unsecured, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"tenant_id": testTenantID.String(),
		"user_id":   testUserID.String(),
		"exp":       jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("firmar el token con alg=none: %v", err)
	}

	cases := []struct {
		name  string
		token string
	}{
		{name: "a tampered signature", token: parts[0] + "." + parts[1] + "." + flip(parts[2])},
		{name: "a tampered payload", token: parts[0] + "." + flip(parts[1]) + "." + parts[2]},
		{name: "a token signed with another secret", token: forged},
		{name: "an unsecured token (alg=none)", token: unsecured},
		{name: "a token that is not a token", token: "not.a.jwt"},
		{name: "the empty string", token: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := issuer.Verify(tc.token)
			if err == nil {
				t.Fatalf("Verify(%q) = (%+v, nil), want un error", tc.token, claims)
			}
			if claims.TenantID != uuid.Nil || claims.UserID != uuid.Nil {
				t.Errorf("Verify devolvió claims %+v junto con el error; un token rechazado no tiene claims", claims)
			}
		})
	}
}

// TestVerifyRejectsTokensWithoutAUsableTenant cubre los claims requeridos de §5.1: sin
// `tenant_id` y sin `user_id` bien formados no hay contexto que aplicar, y aceptarlos dejaría
// una petición sin tenant.
func TestVerifyRejectsTokensWithoutAUsableTenant(t *testing.T) {
	token := func(t *testing.T, claims jwt.MapClaims) string {
		t.Helper()

		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSigningSecret))
		if err != nil {
			t.Fatalf("firmar el token de prueba: %v", err)
		}
		return signed
	}

	exp := jwt.NewNumericDate(time.Now().Add(time.Hour))

	cases := []struct {
		name   string
		claims jwt.MapClaims
	}{
		{name: "no tenant_id", claims: jwt.MapClaims{"user_id": testUserID.String(), "exp": exp}},
		{name: "no user_id", claims: jwt.MapClaims{"tenant_id": testTenantID.String(), "exp": exp}},
		{name: "a tenant_id that is not a uuid", claims: jwt.MapClaims{"tenant_id": "tenant-a", "user_id": testUserID.String(), "exp": exp}},
		{name: "a user_id that is not a uuid", claims: jwt.MapClaims{"tenant_id": testTenantID.String(), "user_id": "user-1", "exp": exp}},
		{name: "the nil uuid as tenant_id", claims: jwt.MapClaims{"tenant_id": uuid.Nil.String(), "user_id": testUserID.String(), "exp": exp}},
		{name: "an unknown role", claims: jwt.MapClaims{"tenant_id": testTenantID.String(), "user_id": testUserID.String(), "role": "employee", "exp": exp}},
		{name: "a role that is not a string", claims: jwt.MapClaims{"tenant_id": testTenantID.String(), "user_id": testUserID.String(), "role": 7, "exp": exp}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newIssuer(t, time.Hour).Verify(token(t, tc.claims)); err == nil {
				t.Fatal("Verify aceptó un token sin un tenant y un usuario usables, want un error")
			}
		})
	}
}

// TestIssueRejectsIncompleteClaims cierra el otro lado: el emisor no puede producir un token
// que el verificador tenga que rechazar. Un tenant o un usuario ausente es un defecto de
// programación del llamador, no algo que llegue a un cliente.
func TestIssueRejectsIncompleteClaims(t *testing.T) {
	issuer := newIssuer(t, time.Hour)

	cases := []struct {
		name   string
		claims identity.Claims
	}{
		{name: "no tenant", claims: identity.Claims{UserID: testUserID}},
		{name: "no user", claims: identity.Claims{TenantID: testTenantID}},
		{name: "no tenant and no user", claims: identity.Claims{}},
		{name: "an unknown role", claims: identity.Claims{TenantID: testTenantID, UserID: testUserID, Role: "employee"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if token, err := issuer.Issue(tc.claims); err == nil {
				t.Fatalf("Issue(%+v) = (%q, nil), want un error", tc.claims, token)
			}
		})
	}
}

// TestNewTokenIssuerRejectsAnUnusableConfiguration cubre que un emisor mal configurado no
// exista: una clave vacía y una vida no positiva son errores de configuración, y fallar acá
// es la última barrera antes de firmar con una clave que no firma nada.
func TestNewTokenIssuerRejectsAnUnusableConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		ttl    time.Duration
	}{
		{name: "an empty secret", secret: "", ttl: time.Hour},
		{name: "a blank secret", secret: "   ", ttl: time.Hour},
		{name: "a zero lifetime", secret: testSigningSecret, ttl: 0},
		{name: "a negative lifetime", secret: testSigningSecret, ttl: -time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if issuer, err := identity.NewTokenIssuer(tc.secret, tc.ttl); err == nil {
				t.Fatalf("NewTokenIssuer(%q, %v) = (%v, nil), want un error", tc.secret, tc.ttl, issuer)
			}
		})
	}
}

// TestClaimsAreNotInferredFromAnotherToken deja constancia de que el verificador no completa
// nada que falte: no hay valor por defecto de tenant ni de usuario, y el error es la única
// salida posible de un token incompleto.
func TestClaimsAreNotInferredFromAnotherToken(t *testing.T) {
	issuer := newIssuer(t, time.Hour)

	other, err := issuer.Issue(identity.Claims{
		TenantID: uuid.MustParse("019a0000-0000-7000-8000-0000000000b1"),
		UserID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000b2"),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := issuer.Verify(other)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.TenantID == testTenantID || claims.UserID == testUserID {
		t.Errorf("los claims verificados = %+v, want los del token emitido y no los de otra prueba", claims)
	}
	if errors.Is(err, jwt.ErrTokenExpired) {
		t.Error("el error del token vigente no debería ser de expiración")
	}
}
