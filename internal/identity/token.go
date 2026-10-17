package identity

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Este archivo implementa el contrato de claims del access token (docs/BACKEND.md §5.1):
//
//	tenant_id  uuid        requerido  única fuente del tenant de la petición
//	user_id    uuid        requerido  cajero que cierra o consulta
//	role       "admin"     opcional   presente SOLO en tokens de administrador
//	exp        timestamp   requerido  vida del access token
//
// Dos propiedades son load-bearing y se cumplen por construcción, no por convención del
// llamador:
//
//  1. `role` está AUSENTE en un token de empleado. No se emite vacío ni con "employee":
//     la ausencia del claim ES la marca de empleado (ARCHITECTURE.md:23). El emisor arma el
//     mapa de claims y solo agrega `role` cuando el llamador pide un administrador.
//  2. El algoritmo está fijado en HS256 del lado de la verificación. Un token con `alg` de la
//     familia `none` no se acepta aunque la biblioteca lo permita.

// RoleAdmin es el único valor privilegiado del claim `role`: el marcador administrativo que
// emite `POST /auth/admin-login` (§4.4). Un token sin el claim es un token de empleado.
const RoleAdmin = "admin"

// Claves de los claims. Son las del contrato §5.1, en snake_case.
const (
	claimTenantID = "tenant_id"
	claimUserID   = "user_id"
	claimRole     = "role"
)

// Claims es el conjunto de claims del contrato ya verificado. Role vacío significa empleado;
// no existe el valor "employee".
type Claims struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Role     string
}

// IsAdmin informa si el token verificado es un token de administrador.
func (c Claims) IsAdmin() bool { return c.Role == RoleAdmin }

// TokenIssuer firma y verifica el access token. La firma es simétrica (HS256): el mismo
// proceso emite y verifica, y la clave viene de la configuración del proceso
// (docs/BACKEND.md §2.5), nunca del repositorio.
type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
	// now se guarda para que las pruebas puedan fijar el reloj. En producción es time.Now.
	now func() time.Time
}

// NewTokenIssuer construye el emisor. Una clave vacía o una vida no positiva es un error de
// configuración: firmar con una clave que no firma nada, o emitir un token sin vida, son
// fallas silenciosas que conviene detener acá y no en el primer login.
func NewTokenIssuer(secret string, ttl time.Duration) (*TokenIssuer, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("the JWT signing secret is empty")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("the access token lifetime %v is not positive", ttl)
	}

	return &TokenIssuer{secret: []byte(secret), ttl: ttl, now: time.Now}, nil
}

// Issue firma un access token con los claims del contrato y la vida configurada.
//
// El llamador no elige la expiración y no puede emitir un token sin tenant ni sin usuario:
// los dos son la identidad de la petición, y un token al que le falte uno deja una petición
// sin contexto de tenant o sin autor conocido.
func (i *TokenIssuer) Issue(claims Claims) (string, error) {
	if claims.TenantID == uuid.Nil {
		return "", errors.New("the access token needs a tenant id")
	}
	if claims.UserID == uuid.Nil {
		return "", errors.New("the access token needs a user id")
	}
	if claims.Role != "" && claims.Role != RoleAdmin {
		return "", fmt.Errorf("unknown role %q: the contract only defines %q, and an employee is a token without the claim", claims.Role, RoleAdmin)
	}

	payload := jwt.MapClaims{
		claimTenantID: claims.TenantID.String(),
		claimUserID:   claims.UserID.String(),
		"exp":         jwt.NewNumericDate(i.now().Add(i.ttl)),
	}
	// Acá está la propiedad 1: el claim `role` se OMITE para un empleado. No se emite con la
	// cadena vacía ni con un valor que signifique «empleado».
	if claims.Role != "" {
		payload[claimRole] = claims.Role
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, payload).SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("sign the access token: %w", err)
	}
	return signed, nil
}

// Verify valida la firma, la expiración y la forma de los claims, y devuelve el tenant, el
// usuario y el rol.
//
// Un token que no se puede validar no devuelve claims parciales: o vuelven los tres con la
// firma verificada, o vuelve el error. La mitad de un token verificado es exactamente lo que
// dejaría una petición sin tenant.
func (i *TokenIssuer) Verify(token string) (Claims, error) {
	// El mapa tiene que estar creado: `jwt.MapClaims` es un mapa, y decodificar sobre un mapa
	// nil deja el conjunto de claims vacío y haría fallar la validación de `exp` por falta de
	// claim en lugar de por un token inválido.
	payload := jwt.MapClaims{}

	parsed, err := jwt.ParseWithClaims(
		token,
		payload,
		func(*jwt.Token) (any, error) { return i.secret, nil },
		// El algoritmo se fija del lado de la verificación: un token que declare otro `alg`
		// se rechaza antes de mirar el cuerpo (el ataque clásico es `alg: none`).
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// `exp` es requerido por el §5.1: un token sin expiración sería una sesión eterna.
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("verify the access token: %w", err)
	}

	verified, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, errors.New("verify the access token: unexpected claim set")
	}

	tenantID, err := claimUUID(verified, claimTenantID)
	if err != nil {
		return Claims{}, err
	}
	userID, err := claimUUID(verified, claimUserID)
	if err != nil {
		return Claims{}, err
	}

	role, err := claimRoleOf(verified)
	if err != nil {
		return Claims{}, err
	}

	return Claims{TenantID: tenantID, UserID: userID, Role: role}, nil
}

// claimUUID lee un claim que tiene que ser un UUID no nulo. El del tenant es además la única
// fuente del tenant de la petición, así que un valor ilegible no se puede ignorar: es un
// token inutilizable, no un token de un tenant desconocido.
func claimUUID(claims jwt.MapClaims, name string) (uuid.UUID, error) {
	raw, present := claims[name]
	if !present {
		return uuid.Nil, fmt.Errorf("the access token has no %q claim", name)
	}

	text, ok := raw.(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("the access token %q claim is not a string", name)
	}

	parsed, err := uuid.Parse(text)
	if err != nil {
		return uuid.Nil, fmt.Errorf("the access token %q claim is not a uuid: %w", name, err)
	}
	if parsed == uuid.Nil {
		return uuid.Nil, fmt.Errorf("the access token %q claim is the nil uuid", name)
	}
	return parsed, nil
}

// claimRoleOf lee el claim `role` con su ausencia como significado: sin claim, empleado. Un
// valor distinto de "admin" es un token que este contrato no define y no puede circular.
func claimRoleOf(claims jwt.MapClaims) (string, error) {
	raw, present := claims[claimRole]
	if !present || raw == nil {
		return "", nil
	}

	role, ok := raw.(string)
	if !ok {
		return "", errors.New("the access token role claim is not a string")
	}
	if role != RoleAdmin {
		return "", fmt.Errorf("the access token carries an unknown role %q", role)
	}
	return role, nil
}
