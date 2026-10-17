package identity

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// Este archivo implementa `POST /auth/device-bind` (docs/BACKEND.md §4.3): enlaza el teléfono al
// cajero del token, guarda el PIN como `pin_hash` argon2id y fija la ventana de sesión offline.
//
// La ruta va SIEMPRE detrás del middleware de token: el tenant y el usuario salen de los claims
// verificados y de ningún otro lado. El cuerpo no tiene campo de tenant, y un cuerpo que lo
// traiga lo trae de más: los campos desconocidos se ignoran, nunca se leen (§5.1).
//
// El efecto es el que hace posible el mostrador sin red: `offline_session_valid_until` queda a
// la hora del bind más al menos 7 días (`identity.MinimumOfflineSessionWindow`), y el cliente
// rechaza el desbloqueo offline después de esa marca. El access token vence mucho antes —su
// vida es `JWT_TTL`, en horas— y su vencimiento NO cierra la sesión del mostrador (§5.2).

// pinPattern es la forma del PIN del contrato: exactamente cuatro dígitos ASCII
// (docs/DESIGN.md:105). Un PIN con espacios, con letras, con signos o con dígitos de ancho
// completo no es el PIN que el teclado numérico del cliente produce.
var pinPattern = regexp.MustCompile(`^[0-9]{4}$`)

// errNoClaims es la causa que se registra cuando la ruta se alcanza sin haber pasado por el
// middleware de token: es un defecto de cableado, no una petición anónima.
var errNoClaims = errors.New("the route ran without the access token middleware: there is no tenant in the request context")

// deviceBindRequest es el cuerpo del contrato (§4.3). No tiene campo de tenant ni ningún otro
// discriminador que pudiera confundirse con uno.
type deviceBindRequest struct {
	PIN string `json:"pin"`
	// DeviceID es opcional: presente, identifica la fila que se refresca; ausente —o nulo, que es
	// como un cliente expresa «no tengo ninguno»— significa que el servidor crea el dispositivo.
	//
	// Es un puntero para distinguir «no vino» de «vino vacío»: un `device_id` presente pero
	// inutilizable es una falla del cliente, y tratarla como ausencia crearía un dispositivo
	// nuevo en silencio en el caso que justamente quiere evitar duplicados (decisión D5).
	DeviceID *string `json:"device_id"`
}

// deviceBindResponse es la respuesta 200 del contrato: el id del dispositivo y hasta cuándo vale
// su sesión offline. El `pin_hash` no sale nunca de la base.
type deviceBindResponse struct {
	Device boundDevice `json:"device"`
}

type boundDevice struct {
	ID         string `json:"id"`
	ValidUntil string `json:"offline_session_valid_until"`
}

// DeviceBindHandler atiende `POST /auth/device-bind`.
type DeviceBindHandler struct {
	devices DeviceBinder
	logger  *slog.Logger
}

// NewDeviceBindHandler construye el handler sobre el enlazador de dispositivos.
func NewDeviceBindHandler(devices DeviceBinder, logger *slog.Logger) (*DeviceBindHandler, error) {
	if devices == nil {
		return nil, errors.New("the device bind handler needs a binder")
	}
	if logger == nil {
		return nil, errors.New("the device bind handler needs a logger")
	}
	return &DeviceBindHandler{devices: devices, logger: logger}, nil
}

// ServeHTTP implementa el contrato completo del endpoint.
func (h *DeviceBindHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// El tenant y el usuario son los del token, y solo existen si el middleware corrió. Sin
	// claims no hay tenant del que escribir la fila: la respuesta es un 500 y nada se escribe.
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoClaims, h.logger))
		return
	}

	var request deviceBindRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		httpx.WriteError(w, httpx.Validation("the body must be a JSON object with a pin", nil))
		return
	}

	// El detalle nombra el CAMPO que falló y nunca el valor recibido: el PIN no vuelve al
	// cliente ni como eco de la validación (§7.9).
	if !pinPattern.MatchString(request.PIN) {
		httpx.WriteError(w, httpx.Validation("pin must be exactly 4 digits", map[string]string{"field": "pin"}))
		return
	}

	deviceID, err := parseOptionalDeviceID(request.DeviceID)
	if err != nil {
		httpx.WriteError(w, httpx.Validation(err.Error(), map[string]string{"field": "device_id"}))
		return
	}

	// El hash se calcula con el primitivo de credenciales del proceso —el mismo de
	// `users.password_hash`—: no hay un segundo hasher y el PIN en claro no se guarda en ningún
	// lado (docs/DATABASE.md §2.10).
	pinHash, err := HashCredential(request.PIN)
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	binding, err := h.devices.BindDevice(r.Context(), claims.TenantID, claims.UserID, deviceID, pinHash, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, ErrUnknownDevice):
		// §4.1: el recurso no existe o está fuera del tenant, y las dos causas devuelven la MISMA
		// respuesta. El mensaje no nombra siquiera el aislamiento: decir «fuera de este tenant»
		// sería describir una causa que el RLS no permite distinguir sin filtrar datos ajenos.
		httpx.WriteError(w, httpx.NotFound("the device does not exist"))
		return
	default:
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	// Una línea por bind, con el dispositivo, el tenant y el cajero. Nunca el PIN ni su hash
	// (§7.9).
	h.logger.Info("device bound to the offline session window",
		"device_id", binding.DeviceID.String(),
		"tenant_id", claims.TenantID.String(),
		"user_id", claims.UserID.String(),
	)

	httpx.WriteJSON(w, http.StatusOK, deviceBindResponse{Device: boundDevice{
		ID: binding.DeviceID.String(),
		// RFC3339 en UTC: el formato que fija el contrato (§4.3).
		ValidUntil: binding.ValidUntil.UTC().Format(time.RFC3339),
	}})
}

// parseOptionalDeviceID lee el `device_id` opcional.
//
// Ausente o nulo es «créala vos»: el servidor genera el id. Presente es un id que tiene que ser
// utilizable —un UUID que no sea el nulo—, porque un valor vacío o ilegible es un cuerpo
// inválido y no un id que se pueda ignorar en silencio.
func parseOptionalDeviceID(raw *string) (uuid.UUID, error) {
	if raw == nil {
		return uuid.Nil, nil
	}

	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return uuid.Nil, errors.New("device_id must not be empty")
	}

	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return uuid.Nil, errors.New("device_id must be a uuid")
	}
	// El UUID nulo no es el id de ninguna fila: aceptarlo sería pedir un dispositivo que no
	// puede existir y confundirlo con el «sin device_id» de arriba.
	if parsed == uuid.Nil {
		return uuid.Nil, errors.New("device_id must not be the nil uuid")
	}
	return parsed, nil
}
