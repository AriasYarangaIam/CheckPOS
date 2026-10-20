import { getPosDatabase, type CartRow, type PosDatabase } from '@/db/posDatabase'
import { payableCartLines, saleLineTotalCents } from '@/cart/cartStore'
import type { TenderMethod } from '@/sales/tenderMethods'

/**
 * Cierre de venta del cliente (W4-FE-2; docs/DATABASE.md §4.9; SPRINTS.md
 * W4-BE-1/W4-FE-2). El payload congelado nace del carrito tal cual: el
 * redondeo half-up por línea ya lo hizo el carrito (`saleLineTotalCents`) y
 * este módulo solo lo NUMERA y lo SERIAL — el servidor nunca recomputa dinero.
 *
 * El enviado NO está en el camino crítico del cobro (ARCHITECTURE.md:98): la
 * venta cierra en el dispositivo y `submitSaleClose` reclama el acuse con la
 * rama del claim del servidor — 200 replay/fresco, 409 + Retry-After con
 * reenvío de la MISMA clave y cuerpo, y 422 `idempotency.payload_mismatch`
 * u otro error del sobre devuelto como estado honesto, nunca como éxito.
 */

interface SaleCloseInput {
  readonly paymentMethod: TenderMethod
  readonly saleId?: string
  /** Reloj de la fecha de negocio, inyectable para las pruebas. */
  readonly now?: () => Date
}

export interface SaleClosePayload {
  readonly sale_id: string
  readonly sold_on: string
  readonly payment_method: TenderMethod
  readonly total_cents: number
  readonly lines: readonly {
    readonly line_no: number
    readonly product_id: string
    readonly name: string
    readonly price_cents: number
    readonly uom: string
    readonly qty: string
    readonly line_total_cents: number
  }[]
}

/**
 * La fecha de negocio es LOCAL al mostrador: `toISOString` la movería de día,
 * que el sync nunca debe hacer (docs/DATABASE.md §4 `sales` sobre `sold_on`).
 */
export function formatBusinessDate(date: Date): string {
  const pad = (value: number): string => String(value).padStart(2, '0')

  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

export function buildSaleClosePayload(
  lines: readonly CartRow[],
  input: SaleCloseInput,
): SaleClosePayload {
  const payable = payableCartLines(lines)

  if (payable.length === 0) {
    throw new Error('Una venta cierra con al menos una línea no anulada')
  }

  const payloadLines = payable.map((line, index) => ({
    line_no: index + 1,
    product_id: line.product_id,
    name: line.name,
    price_cents: line.unit_price_cents,
    uom: line.uom,
    qty: line.qty,
    line_total_cents: saleLineTotalCents(line),
  }))

  return {
    sale_id: input.saleId ?? crypto.randomUUID(),
    sold_on: formatBusinessDate((input.now ?? (() => new Date()))()),
    payment_method: input.paymentMethod,
    total_cents: payloadLines.reduce((sum, line) => sum + line.line_total_cents, 0),
    lines: payloadLines,
  }
}

/** Verdad del claim: el par clave/cuerpo es el que el servidor reclama. */
export type SaleCloseOutcome =
  | { readonly status: 'acknowledged'; readonly ack: unknown }
  | { readonly status: 'mismatch'; readonly error: { readonly code: string; readonly message: string } }
  | { readonly status: 'rejected'; readonly error: { readonly code: string; readonly message: string } }
  // Sin acuse: red caída, conflicto que superó los reintentos o envío sin
  // sesión en línea. La venta YA está cerrada localmente; esto es lo que el
  // ticket dice, sin fingir éxito ni error.
  | { readonly status: 'unconfirmed' }

export interface SubmitSaleCloseInput {
  readonly token: string
  readonly payload: SaleClosePayload
  /** Origen de la API. Vacío es el mismo origen de la PWA. */
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Clave del claim; en producción nace por venta. */
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

/**
 * Detrás del 409: esperar EXACTAMENTE lo que el servidor pidió en
 * `Retry-After` y reintentar la MISMA petición (misma clave, mismo cuerpo) —
 * abrir una segunda venta sería el mal que el reclamo idempotente prohíbe.
 */
async function retryAfterMs(response: Response, fallback: number): Promise<number> {
  const header = response.headers.get('retry-after')

  if (header === null) return fallback

  const seconds = Number(header)

  return Number.isFinite(seconds) && seconds >= 0 ? seconds * 1000 : fallback
}

export async function submitSaleClose(input: SubmitSaleCloseInput): Promise<SaleCloseOutcome> {
  const fetchImpl = input.fetchImpl ?? globalThis.fetch
  const sleep = input.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)))
  const key = input.key ?? crypto.randomUUID()
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
      response = await fetchImpl(`${input.baseUrl ?? ''}/sales`, {
        method: 'POST',
        headers,
        body,
      })
    } catch {
      // La red no está en el camino crítico: la venta ya cerró localmente y el
      // envío queda sin confirmar — reintentable, nunca un éxito fingido.
      return { status: 'unconfirmed' }
    }

    if (response.ok) {
      // 200 es acuse FRESCO o REPLAY: las dos ramas sirven el mismo snapshot.
      return { status: 'acknowledged', ack: await response.json() }
    }

    if (response.status === 409) {
      if (inFlightRetries >= maxRetries) return { status: 'unconfirmed' }

      inFlightRetries += 1
      await sleep(await retryAfterMs(response, DEFAULT_RETRY_AFTER_MS))

      continue
    }

    const envelope = parseErrorEnvelope(await response.json().catch(() => undefined))

    // La misma clave con otro cuerpo: 422 honesto del servidor, nunca éxito.
    if (envelope.code === 'idempotency.payload_mismatch') {
      return { status: 'mismatch', error: envelope }
    }

    return { status: 'rejected', error: envelope }
  }
}

/**
 * Lectura del carrito que el checkout recibió: en producción este es el único
 * llamado que el cierre hace a la base, tras la confirmación de la pantalla
 * (el carrito se vacía DESPUÉS de armar el payload congelado).
 */
export async function payableLinesOfCart(
  database: PosDatabase = getPosDatabase(),
): Promise<readonly CartRow[]> {
  return payableCartLines(await database.cart.toArray())
}
