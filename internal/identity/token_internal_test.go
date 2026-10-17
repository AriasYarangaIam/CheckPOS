package identity

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TestIssueAndVerifyAcrossTheConfiguredLifetime fija el reloj para poder afirmar la
// expiración exacta y el momento en que deja de valer: `exp` es emisión + vida, y un segundo
// después de esa marca el MISMO token deja de verificar. Sin reloj fijo la aserción sería
// una tolerancia, y con tolerancia no se distingue «vida configurada» de «vida por defecto
// menos un poco».
func TestIssueAndVerifyAcrossTheConfiguredLifetime(t *testing.T) {
	const ttl = 90 * time.Minute
	issuedAt := time.Date(2026, time.October, 16, 12, 0, 0, 0, time.UTC)

	tenantID := uuid.MustParse("019a0000-0000-7000-8000-0000000000a1")
	userID := uuid.MustParse("019a0000-0000-7000-8000-0000000000a2")

	issuer, err := NewTokenIssuer("test-only-signing-key-0123456789abcdef", ttl)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	now := issuedAt
	issuer.now = func() time.Time { return now }

	token, err := issuer.Issue(Claims{TenantID: tenantID, UserID: userID})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("el token tiene %d partes, want 3", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decodificar el cuerpo del token: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("leer el cuerpo del token: %v", err)
	}

	got, ok := claims["exp"].(float64)
	if !ok {
		t.Fatalf("el claim exp = %#v, want un número", claims["exp"])
	}
	if want := issuedAt.Add(ttl).Unix(); int64(got) != want {
		t.Errorf("exp = %d, want %d (emisión %v + vida %v)", int64(got), want, issuedAt, ttl)
	}

	// Un segundo antes de la marca todavía vale.
	now = issuedAt.Add(ttl).Add(-time.Second)
	if _, err := issuer.Verify(token); err != nil {
		t.Errorf("Verify un segundo antes de la expiración: error inesperado: %v", err)
	}

	// Y en la marca ya no.
	now = issuedAt.Add(ttl)
	if _, err := issuer.Verify(token); err == nil {
		t.Fatal("Verify aceptó el token en el instante de la expiración, want un error")
	} else if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("Verify en la expiración = %v, want un error de expiración", err)
	}
}
