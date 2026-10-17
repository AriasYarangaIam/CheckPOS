package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Este archivo implementa el estado del dispositivo que sobrevive sin red: `devices.pin_hash`
// (argon2id) y `devices.offline_session_valid_until` (docs/DATABASE.md §4 `devices`; §5.2).
//
// El PIN y la sesión offline son del DISPOSITIVO, no del usuario: viven en `devices` y no en
// `users`. La consecuencia de contrato es exacta y es la razón de existir de este archivo: un
// access token vencido con un PIN válido todavía desbloquea el POS offline, así que la ventana
// offline NO puede estar acotada por `JWT_TTL` (docs/BACKEND.md §5.2).
//
// La escritura pasa por el helper de transacción de tenant y por ningún otro camino: `devices`
// está bajo FORCE RLS, el GUC lo fija el helper a partir del tenant del token, y una fila de
// otro tenant simplemente no existe para esta transacción (docs/DATABASE.md §5.1-§5.2).

// MinimumOfflineSessionWindow es el piso de la ventana de sesión offline: al menos 7 días desde
// el bind (docs/DATABASE.md §4 `devices`; §5.2; `SPRINTS.md` W2-BE-3).
//
// Es una constante propia y no una derivación de la vida del access token: `JWT_TTL` (horas) es
// otra cosa y no puede acotar esta ventana, porque el mostrador tiene que seguir vendiendo
// cuando el token ya venció. Subir este valor es un cambio de una línea; bajarlo por debajo de
// los 7 días es un cambio de contrato.
const MinimumOfflineSessionWindow = 7 * 24 * time.Hour

// ErrUnknownDevice es el resultado de un bind sobre un `device_id` que no existe en el tenant
// del token. Cubre las dos causas del 404 de §4.1 sin distinguirlas: el dispositivo no existe, o
// existe fuera del tenant y el RLS devolvió cero filas.
var ErrUnknownDevice = errors.New("no device with that id exists in this tenant")

// DeviceBinding es el resultado de enlazar un dispositivo: la fila que quedó y hasta cuándo
// vale su sesión offline.
type DeviceBinding struct {
	DeviceID   uuid.UUID
	ValidUntil time.Time
}

// DeviceBinder es el único camino de escritura de `devices`.
//
// Es una interfaz para que el handler del bind se pruebe sin base, y para que exista exactamente
// un productor de filas de dispositivo. `deviceID` nulo significa «crea la fila»: el contrato
// deja el campo opcional (§4.3) y el servidor genera el id (docs/DATABASE.md §2.2).
type DeviceBinder interface {
	BindDevice(ctx context.Context, tenantID, userID, deviceID uuid.UUID, pinHash string, bindTime time.Time) (DeviceBinding, error)
}

// PostgresDeviceBinder escribe `devices` sobre el pool de aplicación envuelto en el helper de
// transacción de tenant.
type PostgresDeviceBinder struct {
	tenants *postgres.TenantPool
}

// NewPostgresDeviceBinder construye el enlazador sobre el pool de `pos_app`.
func NewPostgresDeviceBinder(tenants *postgres.TenantPool) *PostgresDeviceBinder {
	return &PostgresDeviceBinder{tenants: tenants}
}

// insertDeviceSQL crea el dispositivo. `created_at` recibe el MISMO instante que la base de la
// ventana: una sola lectura del reloj del servidor, así que `offline_session_valid_until` menos
// `created_at` es exactamente la ventana y no depende de dos relojes distintos.
//
// El tenant y el usuario van como parámetros y no se leen de ningún otro lado: el llamador los
// recibe ya verificados del token (§5.1). El GUC que fija el helper es lo que hace que el
// `WITH CHECK` de la política acepte esta fila.
const insertDeviceSQL = `
	INSERT INTO devices (id, tenant_id, user_id, pin_hash, offline_session_valid_until, created_at)
	VALUES ($1, $2, $3, $4, $5, $6)`

// updateDeviceSQL refresca el dispositivo existente: el PIN nuevo, el cajero enlazado y la
// ventana extendida. Son las tres columnas mutables del ciclo de vida de autenticación
// (docs/DATABASE.md §4 `devices`); `created_at` es la hora del bind original y no se toca.
//
// El WHERE nombra solo el id: el aislamiento lo impone la política de RLS del tenant que el
// helper ya fijó, y repetir el `tenant_id` acá taparía un GUC mal fijado —la sentencia seguiría
// acotada— en lugar de dejarlo ver como cero filas, que es la forma fail-closed del contrato
// (docs/DATABASE.md §5.2, §5.3).
const updateDeviceSQL = `
	UPDATE devices
	SET user_id = $2, pin_hash = $3, offline_session_valid_until = $4
	WHERE id = $1`

// BindDevice enlaza el dispositivo al cajero del token: crea la fila cuando no hay `device_id` y
// actualiza —en una sola fila— cuando lo hay (docs/BACKEND.md §4.3).
//
// La idempotencia es natural, por upsert (decisión D5): reenviar el mismo `device_id` refresca
// la ventana y cambia el PIN, y no crea un segundo dispositivo. El protocolo de claim con
// `Idempotency-Key` es W4-BE-2 y no forma parte de esta ruta.
//
// Los dos caminos son excluyentes y el contrato los fija: con `device_id` se ACTUALIZA una fila
// existente —un id que no existe en el tenant devuelve ErrUnknownDevice, que el handler traduce
// a 404— y sin `device_id` se CREA una, con el id que genera el servidor (docs/DATABASE.md
// §2.2). Un id que el cliente invente no crea nada: no es «enlazar este teléfono», es pedir una
// fila que no existe.
//
// `bindTime` lo entrega el llamador y no se lee acá: es el instante del bind, y la ventana se
// calcula sobre él. El UUID nulo de tenant o de usuario se rechaza antes de abrir la
// transacción, porque una fila con cualquiera de los dos sería inutilizable y el RLS la
// escondería en lugar de señalarla.
func (b *PostgresDeviceBinder) BindDevice(ctx context.Context, tenantID, userID, deviceID uuid.UUID, pinHash string, bindTime time.Time) (DeviceBinding, error) {
	if b == nil || b.tenants == nil {
		return DeviceBinding{}, errors.New("the device binder has no tenant pool")
	}
	if tenantID == uuid.Nil {
		return DeviceBinding{}, errors.New("the device bind needs a tenant id")
	}
	if userID == uuid.Nil {
		return DeviceBinding{}, errors.New("the device bind needs the user id of the cashier")
	}
	if strings.TrimSpace(pinHash) == "" {
		return DeviceBinding{}, errors.New("the device bind needs the pin hash")
	}
	if bindTime.IsZero() {
		return DeviceBinding{}, errors.New("the device bind needs the bind instant")
	}

	validUntil := bindTime.Add(MinimumOfflineSessionWindow)

	// El id lo genera la aplicación cuando el cuerpo no lo trae (docs/DATABASE.md §2.2), y con
	// un id propio el camino es el de creación: el contrato no deja que un id del cliente cree
	// una fila.
	boundID := deviceID
	creating := boundID == uuid.Nil
	if creating {
		generated, err := uuid.NewV7()
		if err != nil {
			return DeviceBinding{}, fmt.Errorf("generate the device id: %w", err)
		}
		boundID = generated
	}

	err := b.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		if creating {
			if _, err := tx.Exec(ctx, insertDeviceSQL, boundID, tenantID, userID, pinHash, validUntil, bindTime); err != nil {
				return fmt.Errorf("insert the device: %w", err)
			}
			return nil
		}

		// Un `device_id` que existe en otro tenant no es visible para el `USING` de la política
		// de UPDATE, así que esta sentencia afecta cero filas: es el mismo 404 que un id
		// inexistente, sin filtrar si el dispositivo ajeno existe (docs/BACKEND.md §4.1).
		tag, err := tx.Exec(ctx, updateDeviceSQL, boundID, userID, pinHash, validUntil)
		if err != nil {
			return fmt.Errorf("bind the existing device: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrUnknownDevice
		}
		return nil
	})
	if err != nil {
		return DeviceBinding{}, err
	}

	return DeviceBinding{DeviceID: boundID, ValidUntil: validUntil}, nil
}
