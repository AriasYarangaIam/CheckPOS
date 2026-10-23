import { getPosDatabase, type IdempotencyQueueRow, type PosDatabase, type QueueOperation } from '@/db/posDatabase'
import { markSaleQueueStatus } from '@/sales/saleQueue'

/**
 * Motor de sincronización del cliente (W6-FE-3; SPRINTS.md Semana 6; ARCHITECTURE.md:91;
 * docs/BACKEND.md §8.2).
 *
 * El motor corre en la PESTAÑA: no hay WebSocket, no hay Background Sync API y no hay
 * servicio de fondo. Cuando vuelve la conectividad se drena la cola local por lotes, con
 * retroceso exponencial y jitter, respetando el `Retry-After` de un 409.
 *
 * La regla que manda sobre todas es de producto, no de ingeniería: **el cajero nunca ve un
 * error de sincronización** (docs/DATABASE.md §7.9, decisión S12). Un fallo deja la fila en
 * la cola y se reintenta después; no hay diálogo, no hay botón de reintento, no hay
 * conflicto en pantalla.
 *
 * Los parámetros están congelados por el contrato (docs/BACKEND.md §8.2, tabla de
 * `(propuesta)`): lote máximo 25, base 1 s, factor 2, techo 60 s, jitter ±30 %.
 */

/** Tamaño máximo de lote por drenaje: acota el trabajo y la ventana de locks de claim. */
export const MAX_BATCH_SIZE = 25

/** Base del retroceso exponencial, en milisegundos. */
export const BASE_BACKOFF_MS = 1_000

/** Factor del retroceso exponencial. */
export const BACKOFF_FACTOR = 2

/** Techo del retroceso, en milisegundos. */
export const MAX_BACKOFF_MS = 60_000

/** Jitter aplicado al intervalo: ±30 %. */
export const JITTER_RATIO = 0.3

/**
 * Orden padre → hijo del drenaje (decisión D9): el contrato enumera los POST encolables uno
 * por uno y no define un endpoint de lote, así que el orden se materializa acá. Una venta
 * antes que su anulación, y el cierre de caja al final, sobre el ledger ya confirmado.
 */
const OPERATION_ORDER: Readonly<Record<QueueOperation, number>> = {
  sale: 0,
  void: 1,
  cash_close: 2,
}

/**
 * Retroceso del intento `attempt` (base 1 s, factor 2) con jitter ±30 % y techo de 60 s. El
 * techo se aplica DESPUÉS del jitter, así que ningún reintento espera más de un minuto.
 */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const exponent = Math.max(0, Math.trunc(attempt))
  const base = Math.min(BASE_BACKOFF_MS * BACKOFF_FACTOR ** exponent, MAX_BACKOFF_MS)
  const jitter = 1 + (random() * 2 - 1) * JITTER_RATIO

  return Math.round(Math.min(base * jitter, MAX_BACKOFF_MS))
}

/**
 * Siguiente lote a drenar: solo lo pendiente, padre → hijo y, dentro del mismo tipo, en el
 * orden en que se encoló. El lote se acota al techo del contrato.
 */
export function nextBatch(
  rows: readonly IdempotencyQueueRow[],
  limit: number = MAX_BATCH_SIZE,
): readonly IdempotencyQueueRow[] {
  return rows
    .filter((row) => row.status === 'pending')
    .sort((left, right) => {
      const byOperation = OPERATION_ORDER[left.operation] - OPERATION_ORDER[right.operation]
      if (byOperation !== 0) return byOperation

      return left.created_at.localeCompare(right.created_at)
    })
    .slice(0, Math.max(0, limit))
}

/**
 * URL del POST de un evento encolado. La anulación referencia la venta ORIGINAL en la ruta
 * (docs/BACKEND.md §4.10), y esa venta es el `sale_id` congelado del cuerpo.
 */
export function endpointFor(row: IdempotencyQueueRow): string {
  switch (row.operation) {
    case 'sale':
      return '/sales'
    case 'void':
      return `/sales/${row.sale_id ?? ''}/void`
    case 'cash_close':
      return '/cash-closes'
  }
}

/**
 * Milisegundos que el servidor pidió esperar en un 409. `Retry-After` puede venir en
 * segundos —lo que este contrato usa (`Retry-After: 2`)— o como fecha HTTP. Un valor
 * ilegible no inventa una espera: devuelve `null` y manda el retroceso exponencial.
 */
export function retryAfterMs(header: string | null): number | null {
  if (header === null) return null

  const seconds = Number(header.trim())
  if (Number.isFinite(seconds) && seconds >= 0) return Math.round(seconds * 1000)

  const date = Date.parse(header)
  if (!Number.isNaN(date)) return Math.max(0, date - Date.now())

  return null
}

/** Resumen de un drenaje: evidencia para las pruebas y para el estado de la cabecera. */
export interface DrainSummary {
  /** Eventos acusados en este drenaje. */
  readonly acknowledged: number
  /** Eventos rechazados con un 4xx definitivo (quedan `failed`, sin UI). */
  readonly rejected: number
  /** El drenaje se salteó porque ya había uno activo, por falta de sesión o de red. */
  readonly skipped?: 'already-draining' | 'no-session' | 'offline'
  /** Espera pedida por el servidor en un 409, en milisegundos. */
  readonly retryAfterMs?: number
  /** Si conviene reintentar más tarde (fallo transitorio). */
  readonly shouldRetry: boolean
}

/** Dependencias inyectables del drenaje: la base, la red, la sesión y el reloj. */
export interface DrainDeps {
  readonly database?: PosDatabase
  readonly fetchImpl?: typeof fetch
  readonly token?: string
  readonly baseUrl?: string
  readonly isOnline?: () => boolean
}

/**
 * Un solo drenaje activo por pestaña. Es una bandera en memoria —no un lock del navegador—
 * porque la coordinación entre pestañas es del `startSyncEngine`; acá se protege el caso
 * obvio: dos llamadas simultáneas dentro de la misma pestaña no pueden abrir la misma venta.
 */
let draining = false

/**
 * Drena la cola por lotes. Nunca lanza un error de sincronización hacia arriba: devuelve el
 * resumen y deja las filas en el estado que la verdad dictó.
 */
export async function drainQueue(deps: DrainDeps = {}): Promise<DrainSummary> {
  const database = deps.database ?? getPosDatabase()
  const fetchImpl = deps.fetchImpl ?? globalThis.fetch
  const online = deps.isOnline ?? defaultIsOnline

  if (!online()) return { acknowledged: 0, rejected: 0, skipped: 'offline', shouldRetry: true }
  if (deps.token === undefined || deps.token === '') {
    // Sin sesión en línea no hay a quién entregar: la cola espera. El login llega en la
    // Semana 8; hasta entonces el motor simplemente no drena.
    return { acknowledged: 0, rejected: 0, skipped: 'no-session', shouldRetry: false }
  }
  if (draining) return { acknowledged: 0, rejected: 0, skipped: 'already-draining', shouldRetry: false }

  draining = true
  try {
    const rows = await database.idempotency_queue.toArray()
    const batch = nextBatch(rows)

    let acknowledged = 0
    let rejected = 0

    for (const row of batch) {
      const outcome = await deliver(row, { fetchImpl, token: deps.token, baseUrl: deps.baseUrl ?? '' })

      switch (outcome.kind) {
        case 'acknowledged':
          await markSaleQueueStatus(row.key, 'completed', undefined, database)
          acknowledged += 1
          break

        case 'rejected':
          // Un 4xx definitivo (payload distinto, cuerpo inválido) no se reintenta y tampoco
          // se le muestra al cajero: la fila queda `failed` y el badge deja de contarla.
          await markSaleQueueStatus(row.key, 'failed', outcome.code, database)
          rejected += 1
          break

        case 'in-flight':
          // Branch C: el servidor pidió esperar. Se corta el drenaje entero —no tiene
          // sentido seguir con el lote— y la fila sigue `pending`.
          return {
            acknowledged,
            rejected,
            retryAfterMs: outcome.retryAfterMs ?? undefined,
            shouldRetry: true,
          }

        case 'unconfirmed':
          // Red caída o 5xx: nada se pierde y no hay nada que mostrar.
          return { acknowledged, rejected, shouldRetry: true }
      }
    }

    return { acknowledged, rejected, shouldRetry: false }
  } finally {
    draining = false
  }
}

/** ¿Hay una sesión de drenaje activa en esta pestaña? Lo usa el motor para no encimarse. */
export function isDraining(): boolean {
  return draining
}

/** Resultado de entregar un evento. */
type DeliveryOutcome =
  | { kind: 'acknowledged' }
  | { kind: 'rejected'; code: string }
  | { kind: 'in-flight'; retryAfterMs: number | null }
  | { kind: 'unconfirmed' }

/**
 * Entrega UN evento. La clave de idempotencia es la misma que la fila guardó, así que un
 * reintento jamás abre una segunda venta: el servidor lo reconoce como repetición y
 * devuelve el snapshot almacenado (docs/DATABASE.md §7.1).
 */
async function deliver(
  row: IdempotencyQueueRow,
  deps: { fetchImpl: typeof fetch; token: string; baseUrl: string },
): Promise<DeliveryOutcome> {
  let response: Response
  try {
    response = await deps.fetchImpl(`${deps.baseUrl}${endpointFor(row)}`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        authorization: `Bearer ${deps.token}`,
        'Idempotency-Key': row.key,
      },
      body: row.payload,
    })
  } catch {
    return { kind: 'unconfirmed' }
  }

  if (response.status === 200) return { kind: 'acknowledged' }
  if (response.status === 409) {
    return { kind: 'in-flight', retryAfterMs: retryAfterMs(response.headers.get('Retry-After')) }
  }
  if (response.status >= 400 && response.status < 500) {
    return { kind: 'rejected', code: await rejectionCode(response) }
  }

  return { kind: 'unconfirmed' }
}

/** Código del sobre de error del contrato, sin dejar que un cuerpo raro rompa el drenaje. */
async function rejectionCode(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: { code?: string } }
    return body.error?.code ?? `http_${response.status}`
  } catch {
    return `http_${response.status}`
  }
}

/** Estado de red del navegador, con el `navigator.onLine` real como fuente. */
function defaultIsOnline(): boolean {
  if (typeof navigator === 'undefined' || typeof navigator.onLine !== 'boolean') return true
  return navigator.onLine
}

/** Dependencias del motor en marcha. */
export interface EngineDeps extends DrainDeps {
  /** Intentos ya consumidos por retroceso: se reinicia con cada acuse. */
  readonly onSummary?: (summary: DrainSummary) => void
}

/**
 * Arranca el motor: drena al recuperar conectividad y se reprograma con retroceso si el
 * servidor pidió esperar o si la red falló. Devuelve la función que lo detiene.
 *
 * No hay WebSocket ni Background Sync API: los dos disparadores son el evento `online` y el
 * temporizador de reintento (ARCHITECTURE.md:91).
 */
export function startSyncEngine(deps: EngineDeps = {}): () => void {
  let attempt = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false

  const schedule = (delayMs: number) => {
    if (stopped) return
    if (timer !== undefined) clearTimeout(timer)
    timer = setTimeout(() => {
      void run()
    }, delayMs)
  }

  const run = async (): Promise<void> => {
    if (stopped) return

    const summary = await drainQueue(deps)
    deps.onSummary?.(summary)

    if (summary.skipped === 'already-draining') return
    if (summary.skipped === 'no-session') return

    if (summary.acknowledged > 0) attempt = 0

    if (summary.shouldRetry) {
      const delay = summary.retryAfterMs ?? backoffDelay(attempt)
      attempt += 1
      schedule(delay)
    }
  }

  const onOnline = () => {
    attempt = 0
    void run()
  }

  if (typeof globalThis.addEventListener === 'function') {
    globalThis.addEventListener('online', onOnline)
  }

  // Un intento al arrancar: la pestaña puede abrirse ya con la cola llena y la red arriba.
  void run()

  return () => {
    stopped = true
    if (timer !== undefined) clearTimeout(timer)
    if (typeof globalThis.removeEventListener === 'function') {
      globalThis.removeEventListener('online', onOnline)
    }
  }
}
