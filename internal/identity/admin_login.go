package identity

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// El login de administrador (W8-BE-1; docs/BACKEND.md §4.4; ARCHITECTURE.md:126): la SEGUNDA
// credencial del sistema. El teléfono resuelve la identidad y el tenant —la misma lectura
// pre-auth del login de empleado—, pero lo que se verifica es la contraseña MAESTRA del tenant,
// no la del usuario. El token resultante lleva `role: "admin"`, el único marcador de privilegio
// administrativo del contrato.
//
// Dos propiedades mandan sobre este archivo:
//
//   - **El 401 es genérico y constante.** Teléfono inexistente, contraseña incorrecta, tenant
//     sin registro y hash ilegible producen el MISMO cuerpo, y la verificación criptográfica
//     corre igual en los cuatro caminos: sin eso, el tiempo de respuesta distingue «este
//     teléfono existe» de «no existe», que es exactamente la enumeración que §4.4 prohíbe.
//   - **El privilegio viene de la contraseña maestra, no del usuario.** El `user_id` del token
//     es el de la persona cuyo teléfono se usó —el contrato lo exige en todo token (§5.1)— pero
//     la autoridad administrativa la da el hash del tenant: un empleado que conozca su propia
//     contraseña no se convierte en administrador por este camino.
//
// Nada de acá registra el teléfono, la contraseña, el hash ni el token (§7.9).

// adminLoginRequest es el cuerpo FIJADO por §4.4: teléfono y contraseña maestra.
type adminLoginRequest struct {
	Phone          string `json:"phone"`
	MasterPassword string `json:"master_password"`
}

// adminLoginTenant es el objeto `tenant` de la respuesta: el contrato publica solo el nombre
// (§4.4). Ni el plan, ni el id, ni ningún dato más del registro.
type adminLoginTenant struct {
	Name string `json:"name"`
}

// adminLoginResponse es la respuesta 200 fijada por §4.4.
type adminLoginResponse struct {
	Token  string           `json:"token"`
	Tenant adminLoginTenant `json:"tenant"`
}

// AdminLoginHandler atiende POST /auth/admin-login.
type AdminLoginHandler struct {
	users   UserDirectory
	tenants TenantDirectory
	issuer  *TokenIssuer
	logger  *slog.Logger

	// dummyHash es el mismo señuelo del login de empleado: es un hash argon2id real contra el
	// que se verifica cuando el teléfono no existe o el tenant no tiene registro, para que el
	// trabajo criptográfico de los cuatro caminos del 401 sea el mismo.
	dummyHash string
}

// NewAdminLoginHandler construye el handler completo o falla: un handler a medias atendería
// logins sin poder verificar.
func NewAdminLoginHandler(users UserDirectory, tenants TenantDirectory, issuer *TokenIssuer, logger *slog.Logger) (*AdminLoginHandler, error) {
	if users == nil {
		return nil, errors.New("the admin login needs the user directory")
	}
	if tenants == nil {
		return nil, errors.New("the admin login needs the tenant directory")
	}
	if issuer == nil {
		return nil, errors.New("the admin login needs a token issuer")
	}
	if logger == nil {
		return nil, errors.New("the admin login needs a logger")
	}

	dummyHash, err := HashCredential(dummyCredential)
	if err != nil {
		return nil, err
	}

	return &AdminLoginHandler{users: users, tenants: tenants, issuer: issuer, logger: logger, dummyHash: dummyHash}, nil
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *AdminLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}

	var request adminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with phone and master_password", nil))
		return
	}

	phone := strings.TrimSpace(request.Phone)
	if phone == "" || request.MasterPassword == "" {
		httpx.WriteError(w, httpx.Validation("phone and master_password are required", nil))
		return
	}

	// La identidad primero: el teléfono es globalmente UNIQUE y resuelve el tenant.
	var credentials Credentials
	identityKnown := false

	storedHash := h.dummyHash
	tenantCredential := TenantCredential{}

	found, err := h.users.CredentialsByPhone(r.Context(), phone)
	switch {
	case err == nil:
		credentials = found
		identityKnown = true
	case errors.Is(err, ErrUnknownPhone):
		// Se sigue hasta la verificación con el señuelo: el camino no se acorta.
	default:
		// Un fallo de la base no es «credenciales inválidas»: es un 500 con el sobre genérico.
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	if identityKnown {
		credential, err := h.tenants.MasterCredential(r.Context(), credentials.TenantID)
		switch {
		case err == nil:
			tenantCredential = credential
			storedHash = credential.MasterPasswordHash
		case errors.Is(err, ErrUnknownTenant):
			// El usuario apunta a un tenant que no está en el registro: el cuerpo no es
			// autenticable, y el 401 es el mismo de siempre.
		default:
			httpx.WriteError(w, httpx.Internal(err, h.logger))
			return
		}
	}

	matches, err := VerifyCredential(storedHash, request.MasterPassword)
	if err != nil {
		// El hash guardado no es verificable: no se puede autorizar a nadie. El cliente recibe
		// el mismo 401 —no tiene por qué enterarse de un problema de datos— y el operador una
		// línea sin ningún dato de la credencial.
		h.logger.Error("the stored master credential hash is not verifiable")
		httpx.WriteError(w, httpx.Unauthenticated(invalidCredentialsMessage))
		return
	}
	if !identityKnown || !matches {
		httpx.WriteError(w, httpx.Unauthenticated(invalidCredentialsMessage))
		return
	}

	// Token de ADMINISTRADOR: el claim `role` es lo único que lo distingue de un token de
	// empleado, y el tenant sale del usuario que resolvió el teléfono (§5.1).
	token, err := h.issuer.Issue(Claims{
		TenantID: credentials.TenantID,
		UserID:   credentials.UserID,
		Role:     RoleAdmin,
	})
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	// Una línea por login, con los dos identificadores que ya viajan en el token.
	h.logger.Info("admin login issued an access token",
		"tenant_id", credentials.TenantID.String(),
		"user_id", credentials.UserID.String(),
	)

	httpx.WriteJSON(w, http.StatusOK, adminLoginResponse{
		Token:  token,
		Tenant: adminLoginTenant{Name: tenantCredential.Name},
	})
}
