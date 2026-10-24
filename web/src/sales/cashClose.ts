import {
  getPosDatabase,
  type CashCloseRow,
  type IdempotencyQueueRow,
  type PosDatabase,
} from '@/db/posDatabase'
import { formatBusinessDate } from '@/sales/saleClose'
import { TENDER_LABELS, TENDER_METHODS, type TenderMethod } from '@/sales/tenderMethods'

/**
 * Cierre de caja del cliente (W5-FE-2; docs/DESIGN.md §3.2:194-198; SPRINTS.md
 * W5-FE-2; docs/DATABASE.md §4 `cash_closes`/`cash_close_counts`).
 *
 * **El esperado es una proyección, no un segundo contador.** El MVP no tiene
 * endpoint de reporte (`docs/BACKEND.md:549`), así que el arqueo proyecta el
 * ledger LOCAL: las ventas y anulaciones congeladas de esa fecha de negocio,
 * agrupadas por método (decisión 7 del sprint). El servidor congela su propio
 * `expected_cents` al recibir el cierre y ese acuse es la verdad de lo que
 * quedó guardado; por eso el acuse REEMPLAZA la proyección local en pantalla.
 *
 * **La diferencia se computa** (`counted − expected`) y no se almacena jamás
 * como saldo. La de `fiado` viaja `null`: es informacional por diseño, nunca un
 * faltante (§4.13, F32). El arqueo tampoco tiene fondo inicial ni egresos: esas
 * columnas no existen en esta versión (§4 D10, `docs/BACKEND.md` §11.2).
 *
 * **Sin fondo inicial ni egresos, y sin enmienda.** El cierre local se escribe
 * antes del envío y la fecha de negocio es su clave: reabrir una fecha ya
 * cerrada muestra el cierre guardado, nunca un formulario editable (F37).
 */

/** Los seis métodos en el orden fijo del CHECK y del acuse (§4, D11). */
export const CASH_CLOSE_METHODS = TENDER_METHODS

/** Etiquetas visibles del arqueo: las mismas del selector de tender. */
export const CASH_CLOSE_LABELS = TENDER_LABELS

/** Esperado por método de una fecha de negocio; todo método ausente vale cero. */
export type ExpectedByMethod = Readonly<Record<TenderMethod, number>>

function emptyExpected(): Record<TenderMethod, number> {
  return { cash: 0, yape: 0, plin: 0, card: 0, other: 0, fiado: 0 }
}

/**
 * Proyección del ledger local para UNA fecha de negocio: la venta suma su total
 * congelado y la anulación lo resta, agrupado por el método que el evento
 * congeló. Nunca se recomputa una línea (`docs/DATABASE.md` §2.3) y una venta de
 * otra fecha no cuenta.
 */
export function expectedByMethod(
  events: readonly IdempotencyQueueRow[],
  closedOn: string,
): ExpectedByMethod {
  const totals = emptyExpected()

  for (const event of events) {
    if (event.business_date !== closedOn) continue
    if (event.operation !== 'sale' && event.operation !== 'void') continue
    if (event.payment_method === null) continue

    totals[event.payment_method] +=
      event.operation === 'sale' ? event.total_cents : -event.total_cents
  }

  return totals
}

/** Una fila del arqueo: esperado, contado y la diferencia computada. */
export interface CashCloseCountLine {
  readonly payment_method: TenderMethod
  readonly expected_cents: number
  readonly counted_cents: number
  /** `null` para fiado: informacional, nunca un faltante. */
  readonly difference_cents: number | null
}

/**
 * Fila del arqueo. La diferencia se computa acá y en ningún otro lado; el fiado
 * no lleva diferencia porque su cobro no es una entrada de caja.
 */
export function countLine(
  paymentMethod: TenderMethod,
  expectedCents: number,
  countedCents: number,
): CashCloseCountLine {
  return {
    payment_method: paymentMethod,
    expected_cents: expectedCents,
    counted_cents: countedCents,
    difference_cents: paymentMethod === 'fiado' ? null : countedCents - expectedCents,
  }
}

/** Resumen del arqueo: sobrante y faltante agregados, sin fiado. */
export interface CashCloseSummary {
  readonly surplusCents: number
  readonly shortageCents: number
}

/**
 * Agrega sobrante y faltante sobre los CINCO métodos contados. El fiado queda
 * afuera por diseño: su diferencia es informacional y sumarla como faltante
 * diría que la bodega perdió un dinero que nunca entró a la caja (§4.13, F32).
 */
export function closeTotalSummary(lines: readonly CashCloseCountLine[]): CashCloseSummary {
  let surplusCents = 0
  let shortageCents = 0

  for (const line of lines) {
    if (line.payment_method === 'fiado') continue
    if (line.difference_cents === null) continue

    if (line.difference_cents > 0) surplusCents += line.difference_cents
    else shortageCents += -line.difference_cents
  }

  return { surplusCents, shortageCents }
}

/** Fila del cuerpo de `POST /cash-closes`: solo método y contado. */
export interface CashCloseRequestCount {
  readonly payment_method: TenderMethod
  readonly counted_cents: number
}

export interface CashClosePayload {
  readonly id: string
  readonly closed_on: string
  readonly counts: readonly CashCloseRequestCount[]
}

export interface BuildCashCloseInput {
  /** Id del cierre; en producción nace por cierre. */
  readonly id?: string
  /** Reloj de la fecha de negocio, inyectable para las pruebas. */
  readonly now?: () => Date
}

/**
 * Arma el cuerpo EXACTO del contrato: seis filas, una por método, en el orden
 * fijo. Las seis viajan siempre —omitir un método escondería dinero— y un
 * `fiado` contado distinto de cero se rechaza antes de pedirle al servidor un
 * 422 que el `CHECK` de la tabla ya prohíbe.
 */
export function buildCashClosePayload(
  countedByMethod: ExpectedByMethod,
  input: BuildCashCloseInput = {},
): CashClosePayload {
  const counts = CASH_CLOSE_METHODS.map((method) => {
    const countedCents = countedByMethod[method]

    // Dinero: centavos enteros y no negativos (docs/DATABASE.md §2.3).
    if (!Number.isInteger(countedCents) || countedCents < 0) {
      throw new Error('El conteo debe ser un entero de centavos no negativo')
    }

    return { payment_method: method, counted_cents: countedCents }
  })

  if ((counts.find((row) => row.payment_method === 'fiado')?.counted_cents ?? 0) !== 0) {
    throw new Error('El conteo de fiado es siempre cero: su diferencia es informacional')
  }

  return {
    id: input.id ?? crypto.randomUUID(),
    // La fecha de negocio es LOCAL al mostrador: `toISOString` la movería de día.
    closed_on: formatBusinessDate((input.now ?? (() => new Date()))()),
    counts,
  }
}

/** Fila del acuse: el esperado congelado por el servidor y su diferencia. */
export interface CashCloseAckCount {
  readonly payment_method: TenderMethod
  readonly expected_cents: number
  readonly counted_cents: number
  readonly difference_cents: number | null
}

/** Acuse `200` de `POST /cash-closes`, con la forma exacta del contrato. */
export interface CashCloseAck {
  readonly cash_close: {
    readonly id: string
    readonly tenant_id: string
    readonly closed_on: string
    readonly closed_by_user_id: string
    readonly created_at: string
    readonly received_at: string
  }
  readonly counts: readonly CashCloseAckCount[]
}

/**
 * Verdad del envío. `acknowledged` es un cierre fresco; `duplicate` es el
 * cierre que ya existía para la fecha (F38: el existente prevalece y no es un
 * error); `rejected` es el sobre de error del servidor y `unconfirmed` es la red
 * caída o el conflicto que superó los reintentos. Nunca se finge éxito.
 */
export type CashCloseOutcome =
  | { readonly status: 'acknowledged'; readonly ack: CashCloseAck }
  | { readonly status: 'duplicate'; readonly ack: CashCloseAck }
  | { readonly status: 'rejected'; readonly error: { readonly code: string; readonly message: string } }
  | { readonly status: 'unconfirmed' }

function isInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value)
}

function stringField(source: unknown, field: string): string | null {
  const value = (source as Record<string, unknown> | null | undefined)?.[field]

  return typeof value === 'string' ? value : null
}

/**
 * Lee el acuse del contrato sin creerle a ciegas: un `200` con un cuerpo que no
 * trae las seis filas legibles no puede convertirse en un cierre «exitoso» en
 * pantalla. Devuelve `null` y el llamador lo reporta como rechazo honesto.
 */
export function readCashCloseAck(body: unknown): CashCloseAck | null {
  const close = (body as { cash_close?: unknown } | null | undefined)?.cash_close
  const id = stringField(close, 'id')
  const tenantId = stringField(close, 'tenant_id')
  const closedOn = stringField(close, 'closed_on')
  const closedBy = stringField(close, 'closed_by_user_id')
  const createdAt = stringField(close, 'created_at')
  const receivedAt = stringField(close, 'received_at')

  if (
    id === null ||
    tenantId === null ||
    closedOn === null ||
    closedBy === null ||
    createdAt === null ||
    receivedAt === null
  ) {
    return null
  }

  const rawCounts = (body as { counts?: unknown }).counts

  if (!Array.isArray(rawCounts)) return null

  const known: readonly string[] = CASH_CLOSE_METHODS
  const byMethod = new Map<TenderMethod, CashCloseAckCount>()

  for (const entry of rawCounts) {
    const method = (entry as { payment_method?: unknown } | null)?.payment_method

    if (typeof method !== 'string' || !known.includes(method)) return null

    const expectedCents = (entry as { expected_cents?: unknown }).expected_cents
    const countedCents = (entry as { counted_cents?: unknown }).counted_cents
    const differenceCents = (entry as { difference_cents?: unknown }).difference_cents

    if (!isInteger(expectedCents) || !isInteger(countedCents)) return null
    if (differenceCents !== null && !isInteger(differenceCents)) return null

    byMethod.set(method as TenderMethod, {
      payment_method: method as TenderMethod,
      expected_cents: expectedCents,
      counted_cents: countedCents,
      difference_cents: differenceCents,
    })
  }

  // Un acuse sin las seis filas no es el acuse del contrato.
  if (byMethod.size !== CASH_CLOSE_METHODS.length) return null

  return {
    cash_close: {
      id,
      tenant_id: tenantId,
      closed_on: closedOn,
      closed_by_user_id: closedBy,
      created_at: createdAt,
      received_at: receivedAt,
    },
    // El orden del acuse es el fijo del contrato, no el del cuerpo recibido.
    counts: CASH_CLOSE_METHODS.map((method) => byMethod.get(method) as CashCloseAckCount),
  }
}

export interface SubmitCashCloseInput {
  readonly token: string
  readonly payload: CashClosePayload
  /** Origen de la API. Vacío es el mismo origen de la PWA. */
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Clave del claim; en producción es el id del cierre. */
  readonly key?: string
  /** Espera del backoff, inyectable para las pruebas. */
  readonly sleep?: (ms: number) => Promise<void>
  /** Techo de reintentos por conflicto en vuelo (2s por Retry-After). */
  readonly maxInFlightRetries?: number
}

const DEFAULT_IN_FLIGHT_RETRIES = 2
const DEFAULT_RETRY_AFTER_MS = 2000

function parseErrorEnvelope(body: unknown): { code: string; message: string } {
  if (
    typeof body === 'object' &&
    body !== null &&
    typeof (body as { error?: unknown }).error === 'object'
  ) {
    const envelope = (body as { error: { code?: unknown; message?: unknown } }).error

    return {
      code: typeof envelope.code === 'string' ? envelope.code : 'unknown',
      message: typeof envelope.message === 'string' ? envelope.message : '',
    }
  }

  return { code: 'unknown', message: '' }
}

/** Espera EXACTAMENTE lo que el servidor pidió en `Retry-After`. */
async function retryAfterMs(response: Response, fallback: number): Promise<number> {
  const header = response.headers.get('retry-after')

  if (header === null) return fallback

  const seconds = Number(header)

  return Number.isFinite(seconds) && seconds >= 0 ? seconds * 1000 : fallback
}

/**
 * Envía el cierre con la misma disciplina idempotente que la venta: la MISMA
 * clave y el MISMO cuerpo en cada reintento, `Retry-After` respetado y un techo
 * de conflictos en vuelo. Un `200` es acuse —fresco o del cierre existente— y
 * jamás se convierte en éxito si el cuerpo no es el del contrato.
 */
export async function submitCashClose(input: SubmitCashCloseInput): Promise<CashCloseOutcome> {
  const fetchImpl = input.fetchImpl ?? globalThis.fetch
  const sleep = input.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)))
  const key = input.key ?? input.payload.id
  const maxRetries = input.maxInFlightRetries ?? DEFAULT_IN_FLIGHT_RETRIES
  const body = JSON.stringify(input.payload)
  const headers: Record<string, string> = {
    authorization: `Bearer ${input.token}`,
    'content-type': 'application/json',
    'idempotency-key': key,
  }

  let inFlightRetries = 0

  while (true) {
    let response: Response

    try {
      response = await fetchImpl(`${input.baseUrl ?? ''}/cash-closes`, {
        method: 'POST',
        headers,
        body,
      })
    } catch {
      // El cierre ya quedó escrito localmente: sin acuse es `unconfirmed`,
      // reintentable, nunca un éxito fingido.
      return { status: 'unconfirmed' }
    }

    if (response.ok) {
      const ack = readCashCloseAck(await response.json().catch(() => undefined))

      if (ack === null) {
        return {
          status: 'rejected',
          error: { code: 'invalid_response', message: 'El acuse no trae un cierre legible' },
        }
      }

      // El id del acuse es el único indicio de F38: si difiere del enviado, la
      // fecha ya tenía cierre y el que existe es el que vale.
      return ack.cash_close.id === input.payload.id
        ? { status: 'acknowledged', ack }
        : { status: 'duplicate', ack }
    }

    if (response.status === 409) {
      if (inFlightRetries >= maxRetries) return { status: 'unconfirmed' }

      inFlightRetries += 1
      await sleep(await retryAfterMs(response, DEFAULT_RETRY_AFTER_MS))

      continue
    }

    return { status: 'rejected', error: parseErrorEnvelope(await response.json().catch(() => undefined)) }
  }
}

/** El cierre local guardado para una fecha de negocio, o `null`. */
export async function readCashClose(
  closedOn: string,
  database: PosDatabase = getPosDatabase(),
): Promise<CashCloseRow | null> {
  const row = await database.cash_closes.get(closedOn)

  return row ?? null
}

/**
 * Escribe el cierre local con la proyección del dispositivo ANTES del envío. La
 * clave es la fecha de negocio, así que reabrir el arqueo de una fecha ya
 * cerrada no crea un segundo cierre ni devuelve un formulario editable (F37).
 */
export async function persistCashClose(
  payload: CashClosePayload,
  lines: readonly CashCloseCountLine[],
  database: PosDatabase = getPosDatabase(),
): Promise<CashCloseRow> {
  const row: CashCloseRow = {
    id: payload.id,
    closed_on: payload.closed_on,
    created_at: new Date().toISOString(),
    counts: JSON.stringify(lines),
    acknowledged: false,
  }

  await database.cash_closes.put({ ...row })

  return row
}

/**
 * Reemplaza el cierre local con el acuse: los esperados congelados por el
 * servidor y su diferencia computada pasan a ser la verdad guardada, y el
 * cierre queda marcado como confirmado. Un duplicado entra por acá igual: el
 * cierre existente es el que prevalece (F38).
 */
export async function acknowledgeCashClose(
  ack: CashCloseAck,
  database: PosDatabase = getPosDatabase(),
): Promise<CashCloseRow> {
  const row: CashCloseRow = {
    id: ack.cash_close.id,
    closed_on: ack.cash_close.closed_on,
    created_at: ack.cash_close.created_at,
    counts: JSON.stringify(ack.counts),
    acknowledged: true,
  }

  await database.cash_closes.put({ ...row })

  return row
}

/** Las filas del cierre guardado; una fila ilegible no rompe la pantalla. */
export function cashCloseLinesOf(row: CashCloseRow): readonly CashCloseCountLine[] {
  let parsed: unknown

  try {
    parsed = JSON.parse(row.counts)
  } catch {
    return []
  }

  if (!Array.isArray(parsed)) return []

  const known: readonly string[] = CASH_CLOSE_METHODS

  return parsed.filter((line): line is CashCloseCountLine => {
    const entry = line as Partial<CashCloseCountLine> | null

    return (
      entry !== null &&
      typeof entry.payment_method === 'string' &&
      known.includes(entry.payment_method) &&
      isInteger(entry.expected_cents) &&
      isInteger(entry.counted_cents) &&
      (entry.difference_cents === null || isInteger(entry.difference_cents))
    )
  })
}
