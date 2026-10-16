package identity

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// Este archivo implementa `POST /auth/login` (docs/BACKEND.md §4.2): ruta pre-auth, sin
// idempotencia y sin cola offline. Resuelve el teléfono a un usuario, verifica la contraseña
// contra argon2id y devuelve un access token de EMPLEADO —sin claim `role`— con el
// `tenant_id` y el `user_id` del usuario.
//
// El cuerpo de esta petición NO lleva tenant: no hay campo para él y no se lee ningún
// encabezado de tenant. El tenant sale del usuario que resolvió el teléfono, viaja dentro del
// token y es lo único que el resto de la API puede consumir (ARCHITECTURE.md:23, §4.2).

// invalidCredentialsMessage es el mensaje ÚNICO del 401, compartido por «teléfono
// desconocido» y «contraseña incorrecta»: un mensaje distinto por causa enumeraría usuarios
// (docs/BACKEND.md §4.2, §5.7).
const invalidCredentialsMessage = "invalid credentials"

// dummyCredential es la credencial contra la que se verifica cuando el teléfono no existe, y
// dummyHashSalt entropía del mismo hash. El valor no importa: importa que el camino
// «teléfono desconocido» haga exactamente el mismo trabajo criptográfico que el camino
// «contraseña incorrecta». Sin eso, medir el tiempo de respuesta distingue un teléfono
// registrado de uno que no lo está.
const dummyCredential = "timing-parity-credential"

// loginRequest es el cuerpo del contrato (§4.2). No tiene campo `tenant_id` ni ningún otro
// discriminador de tenant: un cuerpo que lo traiga, lo trae de más y se ignora.
type loginRequest struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// loginResponse es la respuesta 200 del contrato: el token y el id del usuario. El tenant no
// viaja acá: viaja dentro del token (§4.2).
type loginResponse struct {
	Token string    `json:"token"`
	User  loginUser `json:"user"`
}

type loginUser struct {
	ID string `json:"id"`
}

// LoginHandler atiende `POST /auth/login`.
type LoginHandler struct {
	users  UserDirectory
	issuer *TokenIssuer
	logger *slog.Logger

	// dummyHash se calcula una vez, al construir el handler, con los parámetros vigentes. Es
	// un hash argon2id real y verifica contra dummyCredential.
	dummyHash string
}

// NewLoginHandler construye el handler. El único error posible es no poder generar el hash
// señuelo, y ese error se devuelve acá en lugar de convertirse en una petición que responde
// más rápido cuando el teléfono no existe.
func NewLoginHandler(users UserDirectory, issuer *TokenIssuer, logger *slog.Logger) (*LoginHandler, error) {
	if users == nil {
		return nil, errors.New("the login handler needs a user directory")
	}
	if issuer == nil {
		return nil, errors.New("the login handler needs a token issuer")
	}
	if logger == nil {
		return nil, errors.New("the login handler needs a logger")
	}

	dummyHash, err := HashCredential(dummyCredential)
	if err != nil {
		return nil, err
	}

	return &LoginHandler{users: users, issuer: issuer, logger: logger, dummyHash: dummyHash}, nil
}

// ServeHTTP implementa el contrato completo del endpoint.
func (h *LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with phone and password", nil))
		return
	}

	phone := strings.TrimSpace(request.Phone)
	if phone == "" || request.Password == "" {
		httpx.WriteError(w, httpx.Validation("phone and password are required", nil))
		return
	}

	// El hash contra el que se verifica: el del usuario cuando existe, y el señuelo cuando no.
	// La verificación corre SIEMPRE, y el resultado del señuelo no autoriza a nadie: existe
	// para igualar el trabajo criptográfico de los dos caminos, no como puerta.
	storedHash := h.dummyHash
	known := false

	credentials, err := h.users.CredentialsByPhone(r.Context(), phone)
	switch {
	case err == nil:
		storedHash = credentials.PasswordHash
		known = true
	case errors.Is(err, ErrUnknownPhone):
		// Nada que hacer acá: se sigue hasta la verificación con el señuelo.
	default:
		// Un fallo de la base no es «credenciales inválidas»: es un 500 con el sobre genérico,
		// y la causa queda en el log del operador.
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	matches, err := VerifyCredential(storedHash, request.Password)
	if err != nil {
		// La fila existe pero su hash no es argon2id: no se puede verificar nada, así que la
		// respuesta es el mismo 401 genérico —el cliente no tiene por qué enterarse— y el
		// operador recibe una línea sin ningún dato de la credencial.
		h.logger.Error("the stored credential hash is not verifiable")
		httpx.WriteError(w, httpx.Unauthenticated(invalidCredentialsMessage))
		return
	}
	// Las dos condiciones importan: la credencial tiene que coincidir Y el usuario tiene que
	// existir. Un teléfono desconocido devuelve 401 aunque su contraseña coincida con el hash
	// señuelo.
	if !known || !matches {
		httpx.WriteError(w, httpx.Unauthenticated(invalidCredentialsMessage))
		return
	}

	// Token de EMPLEADO: sin claim `role` (docs/BACKEND.md §4.2). El tenant es el del usuario
	// que resolvió el teléfono, y es la única fuente posible.
	token, err := h.issuer.Issue(Claims{TenantID: credentials.TenantID, UserID: credentials.UserID})
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	// Una línea por login, con los dos identificadores que ya viajan en el token. Nunca el
	// teléfono, la contraseña, el hash ni el token mismo (§7.9).
	h.logger.Info("login issued an access token",
		"tenant_id", credentials.TenantID.String(),
		"user_id", credentials.UserID.String(),
	)

	httpx.WriteJSON(w, http.StatusOK, loginResponse{
		Token: token,
		User:  loginUser{ID: credentials.UserID.String()},
	})
}
