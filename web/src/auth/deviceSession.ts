import { offlineSessionVerdict, type OfflineWindowVerdict } from '@/auth/offlineSession'
import { createPinSalt, derivePinVerifier, pinMatches, toBase64 } from '@/auth/pinVerifier'
import { getPosDatabase, type DeviceSessionRow, type PosDatabase } from '@/db/posDatabase'

/**
 * Sesión offline del dispositivo: el bind, la persistencia y el veredicto del
 * desbloqueo (docs/DESIGN.md:104-105; docs/DATABASE.md §4 `devices`; docs/BACKEND.md
 * §4.3; decisión D6 de `odd/tasks/sprint-2-identity.md`).
 *
 * Todo lo que el dispositivo guarda de la identidad vive acá: el id del
 * dispositivo, el id del usuario, la marca de la ventana, la sal y el
 * verificador derivado. El PIN en claro no se persiste —existe mientras se
 * tipea y se convierte en verificador— y el `pin_hash` argon2id del servidor no
 * llega nunca al cliente.
 */

/** Clave de la única fila de la tienda: 1 dispositivo, 1 cajero (docs/DESIGN.md:106). */
export const DEVICE_SESSION_ID = 'device-session'

/** Forma del PIN del contrato: cuatro dígitos ASCII (docs/DESIGN.md:105). */
const PIN_SHAPE = /^[0-9]{4}$/

/** Datos que el dispositivo ya tiene cuando puede derivar el verificador. */
export interface DeviceSessionDraft {
  readonly deviceId: string
  readonly userId: string
  readonly validUntil: string
  /** El PIN que el cajero acaba de tipear. No se persiste. */
  readonly pin: string
}

/**
 * Veredicto de un intento de desbloqueo offline. `unlocked` es el único estado
 * que habilita el mostrador; `rejected` es un PIN incorrecto dentro de la
 * ventana y `refused` es la ventana —la razón por la que el criterio de
 * aceptación de W2-FE-1 exige un motivo visible y no una pantalla bloqueada.
 */
export type UnlockVerdict =
  | { readonly status: 'unlocked'; readonly session: DeviceSessionRow }
  | {
      readonly status: 'refused'
      readonly reason: 'no-session' | 'window-expired' | 'window-unreadable'
    }
  | { readonly status: 'rejected'; readonly reason: 'wrong-pin' }

/**
 * Persiste la sesión del dispositivo derivando el verificador del PIN. Es el
 * único camino de escritura de la tienda: no existe un `add` suelto desde una
 * pantalla, así que el PIN en claro no tiene por dónde llegar al almacén.
 *
 * `put` sobre la clave constante reemplaza la fila anterior: un re-bind del
 * mismo teléfono mantiene una sola sesión, que es lo que el alcance congelado
 * pide (1 dispositivo).
 */
export async function saveDeviceSession(
  draft: DeviceSessionDraft,
  database: PosDatabase = getPosDatabase(),
): Promise<DeviceSessionRow> {
  if (!PIN_SHAPE.test(draft.pin)) {
    throw new Error('El PIN del contrato son cuatro dígitos')
  }

  const salt = createPinSalt()
  const row: DeviceSessionRow = {
    id: DEVICE_SESSION_ID,
    device_id: draft.deviceId,
    user_id: draft.userId,
    offline_session_valid_until: draft.validUntil,
    pin_salt: toBase64(salt),
    pin_verifier: await derivePinVerifier(draft.pin, salt),
  }

  await database.device_session.put({ ...row })

  return row
}

/** La sesión del dispositivo, o `null` si este teléfono todavía no se vinculó. */
export async function loadDeviceSession(
  database: PosDatabase = getPosDatabase(),
): Promise<DeviceSessionRow | null> {
  const row = await database.device_session.get(DEVICE_SESSION_ID)

  return row ?? null
}

/** Borra la sesión del dispositivo: el teléfono volvería a pedir el bind en línea. */
export async function clearDeviceSession(database: PosDatabase = getPosDatabase()): Promise<void> {
  await database.device_session.delete(DEVICE_SESSION_ID)
}

/**
 * Intenta el desbloqueo offline. El orden importa y es parte del contrato: la
 * ventana se evalúa **antes** de derivar, así que pasada la marca el PIN deja de
 * ser relevante —el intento se rechaza igual, con el mismo motivo— y el
 * rechazo no se pega: cada intento vuelve a leer la sesión y a evaluar la marca,
 * de modo que una renovación en línea posterior habilita el desbloqueo sin
 * recargar nada.
 *
 * `pinMatches` nunca lanza y devuelve `false` ante cualquier valor ilegible, así
 * que una fila corrupta rechaza el intento en lugar de romper la pantalla.
 */
export async function attemptOfflineUnlock(
  pin: string,
  now: Date,
  database: PosDatabase = getPosDatabase(),
): Promise<UnlockVerdict> {
  const session = await loadDeviceSession(database)
  const verdict: OfflineWindowVerdict = offlineSessionVerdict(session, now)

  if (verdict === 'unreadable') {
    return { status: 'refused', reason: session === null ? 'no-session' : 'window-unreadable' }
  }

  if (verdict === 'expired') {
    return { status: 'refused', reason: 'window-expired' }
  }

  // `verdict === 'allowed'` implica que hay sesión con marca legible; el
  // guardia es para el tipo, no para el flujo.
  if (session === null) {
    return { status: 'refused', reason: 'no-session' }
  }

  return (await pinMatches(pin, session.pin_salt, session.pin_verifier))
    ? { status: 'unlocked', session }
    : { status: 'rejected', reason: 'wrong-pin' }
}

/** Respuesta 200 de `POST /auth/device-bind`, tal como la fija el contrato (§4.3). */
export interface DeviceBindResponse {
  readonly device: {
    readonly id: string
    readonly offline_session_valid_until: string
  }
}

export interface BindDeviceInput {
  /** Access token del empleado: `POST /auth/login` lo emite y el bind lo exige. */
  readonly token: string
  /** `user.id` de la respuesta del login: el usuario del token. */
  readonly userId: string
  /** El PIN que el cajero tipeó en el momento del bind. */
  readonly pin: string
  /** Presente en un re-bind del mismo teléfono; ausente deja que el servidor cree la fila. */
  readonly deviceId?: string
  /** Origen de la API. Vacío es el mismo origen de la PWA. */
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
}

/**
 * Valida la respuesta del contrato antes de escribir nada. El cuerpo del bind no
 * trae el `pin_hash` y tampoco el `user_id`: el usuario sale del login, así que
 * un cuerpo sin `device` o con una marca ilegible se rechaza entero en lugar de
 * crear una sesión que después no podría desbloquear nada.
 */
function readBoundDevice(body: unknown): DeviceBindResponse['device'] {
  const device = (body as { device?: unknown } | null | undefined)?.device
  const id = (device as { id?: unknown } | undefined)?.id
  const validUntil = (device as { offline_session_valid_until?: unknown } | undefined)
    ?.offline_session_valid_until

  if (typeof id !== 'string' || id === '') {
    throw new Error('La respuesta del bind no trae device.id')
  }
  if (typeof validUntil !== 'string' || Number.isNaN(Date.parse(validUntil))) {
    throw new Error('La respuesta del bind no trae una marca de ventana legible')
  }

  return { id, offline_session_valid_until: validUntil }
}

/**
 * Ejecuta `POST /auth/device-bind` y persiste la sesión derivada. El cuerpo es
 * del contrato y solo del contrato: `pin` y, cuando existe, `device_id`. No hay
 * campo de tenant —ese sale del token y de ningún otro lado
 * (`ARCHITECTURE.md:23`)— y el PIN no viaja a ningún otro destino.
 *
 * El camino de UI que consigue el token todavía no existe —el selector de rol y
 * la credencial del Empleado son de la semana 8— así que esta función se ejerce
 * por sus pruebas y queda lista para esa pantalla.
 */
export async function bindDeviceSession(
  input: BindDeviceInput,
  database: PosDatabase = getPosDatabase(),
): Promise<DeviceSessionRow> {
  const fetchImpl = input.fetchImpl ?? globalThis.fetch

  if (typeof fetchImpl !== 'function') {
    throw new Error('No hay `fetch` para llamar al bind del dispositivo')
  }

  const response = await fetchImpl(`${input.baseUrl ?? ''}/auth/device-bind`, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      authorization: `Bearer ${input.token}`,
    },
    body: JSON.stringify(
      input.deviceId === undefined
        ? { pin: input.pin }
        : { pin: input.pin, device_id: input.deviceId },
    ),
  })

  if (!response.ok) {
    throw new Error(`POST /auth/device-bind respondió ${response.status}`)
  }

  const device = readBoundDevice(await response.json())

  return saveDeviceSession(
    { deviceId: device.id, userId: input.userId, validUntil: device.offline_session_valid_until, pin: input.pin },
    database,
  )
}
