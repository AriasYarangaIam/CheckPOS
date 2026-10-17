// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { pinMatches } from '@/auth/pinVerifier'
import {
  DEVICE_SESSION_ID,
  attemptOfflineUnlock,
  bindDeviceSession,
  clearDeviceSession,
  loadDeviceSession,
  saveDeviceSession,
} from '@/auth/deviceSession'
import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'

/*
 * La sesión offline del dispositivo y el verificador local del PIN
 * (docs/DESIGN.md:104-105; docs/DATABASE.md §4 `devices`; decisión D6 de
 * `odd/tasks/sprint-2-identity.md`).
 *
 * Lo que estas pruebas afirman es acotado a propósito: el PIN en claro no
 * llega al almacén, la ventana se aplica antes de derivar y el rechazo no es
 * pegajoso. No afirman que el verificador local resista una enumeración fuera
 * de línea: sobre 4 dígitos la derivación retrasa ese ataque, no lo impide, y
 * el límite real es el `pin_hash` argon2id del servidor.
 */

const createdNames = new Set<string>()
let nextId = 0

function freshDatabase(): PosDatabase {
  nextId += 1
  const name = `${POS_DATABASE_NAME}-session-test-${nextId}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  vi.unstubAllGlobals()
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const PIN = '7391'
const NOW = new Date('2026-10-16T09:15:00.000Z')
const INSIDE_WINDOW = '2026-10-23T09:15:00.000Z'

const BIND_TENANT_USER = '018f2b8e-8f9a-7c3d-9e10-1a2b3c4d5e6f'
const BIND_DEVICE_ID = '018f2b8e-8f9a-7c3d-9e10-000000000001'

/** Respuesta del contrato de `POST /auth/device-bind` (docs/BACKEND.md §4.3). */
function bindResponse(validUntil: string, id = BIND_DEVICE_ID): StubResponse {
  return stubResponse(200, { device: { id, offline_session_valid_until: validUntil } })
}

/**
 * Respuesta mínima con la forma que el cliente consume (`ok`, `status`, `json`).
 * No se usa `Response` del entorno: el cliente no depende de su implementación,
 * y así la prueba no depende de que undici esté publicado en jsdom.
 */
type StubResponse = {
  readonly ok: boolean
  readonly status: number
  readonly json: () => Promise<unknown>
}

function stubResponse(status: number, body: unknown): StubResponse {
  return { ok: status >= 200 && status < 300, status, json: async () => body }
}

/** Devuelve la respuesta del contrato y deja ver la petición que se envió. */
function fetchStub(response: StubResponse, calls: Array<{ url: string; init: RequestInit }>) {
  const impl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} })

    return response
  })

  return impl as unknown as typeof fetch
}

describe('bindDeviceSession', () => {
  it('posts the contract body to the contract route and stores the derived session', async () => {
    const database = freshDatabase()
    const calls: Array<{ url: string; init: RequestInit }> = []
    const fetchImpl = fetchStub(bindResponse(INSIDE_WINDOW), calls)

    const session = await bindDeviceSession(
      { token: 'access-token-del-empleado', userId: BIND_TENANT_USER, pin: PIN, fetchImpl },
      database,
    )

    expect(calls).toHaveLength(1)
    expect(calls[0]?.url).toBe('/auth/device-bind')
    expect(calls[0]?.init.method).toBe('POST')
    expect(calls[0]?.init.headers).toMatchObject({
      'content-type': 'application/json',
      authorization: 'Bearer access-token-del-empleado',
    })
    // El cuerpo es del contrato: el PIN y, cuando existe, el `device_id`. No hay
    // campo de tenant: ese sale del token y de ningún otro lado
    // (ARCHITECTURE.md:23).
    expect(JSON.parse(String(calls[0]?.init.body))).toEqual({ pin: PIN })

    expect(session.device_id).toBe(BIND_DEVICE_ID)
    expect(session.user_id).toBe(BIND_TENANT_USER)
    expect(session.offline_session_valid_until).toBe(INSIDE_WINDOW)

    const stored = await loadDeviceSession(database)

    expect(stored).toEqual(session)
    database.close()
  })

  it('sends the device id when the device is being re-bound', async () => {
    const database = freshDatabase()
    const calls: Array<{ url: string; init: RequestInit }> = []
    const fetchImpl = fetchStub(bindResponse(INSIDE_WINDOW, '018f2b8e-8f9a-7c3d-9e10-0000000000aa'), calls)

    await bindDeviceSession(
      { token: 'token', userId: BIND_TENANT_USER, pin: PIN, deviceId: '018f2b8e-8f9a-7c3d-9e10-0000000000aa', fetchImpl },
      database,
    )

    expect(JSON.parse(String(calls[0]?.init.body))).toEqual({
      pin: PIN,
      device_id: '018f2b8e-8f9a-7c3d-9e10-0000000000aa',
    })
    database.close()
  })

  it('stores the salt and the verifier, and never the clear PIN', async () => {
    const database = freshDatabase()
    const session = await bindDeviceSession(
      { token: 't', userId: BIND_TENANT_USER, pin: PIN, fetchImpl: fetchStub(bindResponse(INSIDE_WINDOW), []) },
      database,
    )

    // El conjunto exacto de campos: el `pin_hash` del servidor no está y el PIN
    // en claro tampoco. Un campo de más en la fila también falla acá.
    expect(Object.keys(session).sort()).toEqual([
      'device_id',
      'id',
      'offline_session_valid_until',
      'pin_salt',
      'pin_verifier',
      'user_id',
    ])
    expect(JSON.stringify(session)).not.toContain(PIN)
    expect(session.id).toBe(DEVICE_SESSION_ID)

    // El verificador guardado sí reconoce el PIN que se tipeó en el bind: la
    // fila es la que habilita el desbloqueo offline, y no una copia inerte.
    await expect(pinMatches(PIN, session.pin_salt, session.pin_verifier)).resolves.toBe(true)

    database.close()
  })

  it('stores nothing when the bind is refused', async () => {
    const database = freshDatabase()
    const refused = stubResponse(401, { error: { code: 'unauthorized' } })

    await expect(
      bindDeviceSession(
        { token: 'vencido', userId: BIND_TENANT_USER, pin: PIN, fetchImpl: fetchStub(refused, []) },
        database,
      ),
    ).rejects.toThrow()

    await expect(loadDeviceSession(database)).resolves.toBeNull()
    database.close()
  })

  it('refuses a 200 body that is not the contract shape', async () => {
    const database = freshDatabase()
    const broken = stubResponse(200, { device: { id: BIND_DEVICE_ID } })

    await expect(
      bindDeviceSession(
        { token: 't', userId: BIND_TENANT_USER, pin: PIN, fetchImpl: fetchStub(broken, []) },
        database,
      ),
    ).rejects.toThrow()

    await expect(loadDeviceSession(database)).resolves.toBeNull()
    database.close()
  })

  it('refuses an unreadable window mark instead of storing a session that cannot unlock', async () => {
    const database = freshDatabase()
    const broken = bindResponse('el martes')

    await expect(
      bindDeviceSession(
        { token: 't', userId: BIND_TENANT_USER, pin: PIN, fetchImpl: fetchStub(broken, []) },
        database,
      ),
    ).rejects.toThrow()

    await expect(loadDeviceSession(database)).resolves.toBeNull()
    database.close()
  })

  it('refuses a PIN that is not the four digits of the contract', async () => {
    const database = freshDatabase()

    await expect(
      bindDeviceSession(
        { token: 't', userId: BIND_TENANT_USER, pin: '739', fetchImpl: fetchStub(bindResponse(INSIDE_WINDOW), []) },
        database,
      ),
    ).rejects.toThrow()

    await expect(loadDeviceSession(database)).resolves.toBeNull()
    database.close()
  })
})

describe('attemptOfflineUnlock', () => {
  async function boundDatabase(validUntil = INSIDE_WINDOW): Promise<PosDatabase> {
    const database = freshDatabase()

    await saveDeviceSession(
      { deviceId: BIND_DEVICE_ID, userId: BIND_TENANT_USER, validUntil, pin: PIN },
      database,
    )

    return database
  }

  it('refuses when this device never bound a session', async () => {
    const database = freshDatabase()

    await expect(attemptOfflineUnlock(PIN, NOW, database)).resolves.toEqual({
      status: 'refused',
      reason: 'no-session',
    })

    database.close()
  })

  it('unlocks inside the window with the PIN that was bound', async () => {
    const database = await boundDatabase()
    const verdict = await attemptOfflineUnlock(PIN, NOW, database)

    expect(verdict.status).toBe('unlocked')
    expect(verdict.status === 'unlocked' && verdict.session.device_id).toBe(BIND_DEVICE_ID)

    database.close()
  })

  it('rejects a wrong PIN inside the window without touching the stored row', async () => {
    const database = await boundDatabase()
    const before = await loadDeviceSession(database)

    await expect(attemptOfflineUnlock('0001', NOW, database)).resolves.toEqual({
      status: 'rejected',
      reason: 'wrong-pin',
    })

    // Un intento fallido no borra la sesión: el cajero puede volver a intentar
    // sin perder la ventana que ya tiene.
    await expect(loadDeviceSession(database)).resolves.toEqual(before)

    database.close()
  })

  it('rejects a wrong PIN without issuing any network call', async () => {
    const database = await boundDatabase()
    const noNetwork = vi.fn()

    vi.stubGlobal('fetch', noNetwork)

    await expect(attemptOfflineUnlock('0001', NOW, database)).resolves.toEqual({
      status: 'rejected',
      reason: 'wrong-pin',
    })

    // El rechazo de un PIN incorrecto es un cómputo local: falla en la
    // derivación, antes de que exista transporte. La prueba es una guarda
    // contra la falsificación conocida (verificación W3): un `fetch`
    // inyectado en esta rama quedaría en evidencia acá.
    expect(noNetwork).not.toHaveBeenCalled()

    database.close()
  })

  it('refuses past the window before deriving, so the PIN stops mattering', async () => {
    const database = await boundDatabase()

    await expect(
      attemptOfflineUnlock(PIN, new Date(Date.parse(INSIDE_WINDOW) + 1), database),
    ).resolves.toEqual({ status: 'refused', reason: 'window-expired' })

    // La misma sesión, el mismo instante, un PIN que no es el correcto: el
    // veredicto es idéntico.
    await expect(
      attemptOfflineUnlock('0001', new Date(Date.parse(INSIDE_WINDOW) + 1), database),
    ).resolves.toEqual({ status: 'refused', reason: 'window-expired' })

    database.close()
  })

  it('refuses an unreadable mark even with the correct PIN', async () => {
    const database = await boundDatabase('el martes')

    await expect(attemptOfflineUnlock(PIN, NOW, database)).resolves.toEqual({
      status: 'refused',
      reason: 'window-unreadable',
    })

    database.close()
  })

  it('does not latch the refusal: a renewed window unlocks again without a reload', async () => {
    /*
     * Criterio de aceptación de W2-FE-1: pasada la expiración el desbloqueo
     * offline se rechaza **sin bloquear el intento en línea posterior**. La
     * renovación es lo que hace una autenticación en línea —`POST
     * /auth/device-bind` refresca `offline_session_valid_until` (decisión D8)— y
     * se prueba de la única forma que prueba algo: negándose primero y
     * volviendo a intentar sobre la misma base, sin recrear la sesión.
     */
    const database = await boundDatabase()
    const expired = new Date(Date.parse(INSIDE_WINDOW) + 60_000)

    await expect(attemptOfflineUnlock(PIN, expired, database)).resolves.toEqual({
      status: 'refused',
      reason: 'window-expired',
    })

    await saveDeviceSession(
      {
        deviceId: BIND_DEVICE_ID,
        userId: BIND_TENANT_USER,
        validUntil: new Date(expired.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString(),
        pin: PIN,
      },
      database,
    )

    const renewed = await attemptOfflineUnlock(PIN, expired, database)

    expect(renewed.status).toBe('unlocked')

    database.close()
  })

  it('keeps one row: a rebind replaces the session of this device', async () => {
    const database = await boundDatabase()

    await saveDeviceSession(
      { deviceId: '018f2b8e-8f9a-7c3d-9e10-0000000000bb', userId: 'otro', validUntil: INSIDE_WINDOW, pin: '4821' },
      database,
    )

    expect(await database.device_session.count()).toBe(1)
    expect((await loadDeviceSession(database))?.pin_salt).toBeTruthy()

    await clearDeviceSession(database)

    await expect(loadDeviceSession(database)).resolves.toBeNull()
    expect(await database.device_session.count()).toBe(0)

    database.close()
  })
})
