import type { CashCloseRow, IdempotencyQueueRow } from '@/db/posDatabase'
import {
  CASH_CLOSE_METHODS,
  closeTotalSummary,
  countLine,
  expectedByMethod,
  type CashCloseCountLine,
  type ExpectedByMethod,
} from '@/sales/cashClose'

/**
 * Proyección del ledger local para el dashboard del dueño (W8-FE-3; docs/FRONTEND.md:49;
 * `ARCHITECTURE.md:138`).
 *
 * **No hay endpoint de reporte.** Todo lo que el dashboard muestra sale de dos fuentes y de
 * ninguna otra: la proyección del ledger local que el dispositivo ya tiene —la cola de eventos y
 * los cierres de caja guardados— más `GET /fiado-accounts` para la deuda viva, que es la única
 * lectura del servidor que el contrato permite.
 *
 * **Qué cuenta y qué no** (decisión D-E del sprint 8): una venta `pending` SÍ cuenta —el día del
 * dueño no desaparece cuando la red falla, y el badge de sincronización dice qué parte todavía no
 * está confirmada— y una fila `failed` NO cuenta, porque el servidor la rechazó y nunca fue una
 * venta. La anulación resta su total congelado, y no es un ticket.
 *
 * El dinero nunca se recomputa: se suman los `total_cents` congelados que el evento ya cerró
 * (`docs/DATABASE.md` §2.3).
 */

/** Estado de un cierre local frente a su conteo: cuadrado, sobrante o faltante. */
export type CloseAuditKind = 'balanced' | 'surplus' | 'shortage'

/** Un cierre local con su diferencia agregada sobre los CINCO métodos contados. */
export interface LocalCloseAudit {
  readonly closedOn: string
  readonly acknowledged: boolean
  /** Suma del esperado congelado de los cinco métodos contados (fiado afuera). */
  readonly expectedCents: number
  readonly countedCents: number
  /** `counted − expected` sobre los cinco métodos: positivo es sobrante. */
  readonly differenceCents: number
  readonly kind: CloseAuditKind
}

/** La foto del negocio para UNA fecha de negocio, tal como el dispositivo la conoce. */
export interface LedgerProjection {
  readonly businessDate: string
  /** Ventas netas del día: ventas menos anulaciones, con los pendientes incluidos. */
  readonly netSalesCents: number
  /** Cantidad de TICKETS: ventas cerradas; una anulación no es un ticket. */
  readonly ticketCount: number
  /** Ticket promedio computado; cero cuando no hubo tickets. */
  readonly averageTicketCents: number
  /** Desglose por los seis tenders del CHECK, con la anulación restando. */
  readonly byTender: ExpectedByMethod
  /** Ventas netas por hora LOCAL del dispositivo: 24 posiciones, de 0 a 23. */
  readonly byHour: readonly number[]
  /** Ventas todavía sin confirmar por el servidor. */
  readonly pendingSales: number
  /** Cierres locales registrados, del más reciente al más viejo. */
  readonly localCloses: readonly LocalCloseAudit[]
}

/** Eventos que cuentan para la foto: la cola sin las filas que el servidor rechazó. */
function countableEvents(events: readonly IdempotencyQueueRow[]): readonly IdempotencyQueueRow[] {
  return events.filter((event) => event.status !== 'failed')
}

/** Hora local de un instante ISO, sin reinterpretar la fecha de negocio. */
function localHour(createdAt: string): number {
  const parsed = new Date(createdAt)
  if (Number.isNaN(parsed.getTime())) return 0

  return parsed.getHours()
}

/** Agrega un cierre guardado a la lista de auditoría. */
function auditOf(row: CashCloseRow): LocalCloseAudit | null {
  let lines: CashCloseCountLine[]
  try {
    const parsed: unknown = JSON.parse(row.counts)
    if (!Array.isArray(parsed)) return null
    lines = parsed as CashCloseCountLine[]
  } catch {
    return null
  }

  // El fiado queda afuera de la suma por diseño: su diferencia es informacional y contarla como
  // faltante diría que la bodega perdió un dinero que nunca entró a la caja (§4.13, F32).
  const summary = closeTotalSummary(lines)
  const counted = CASH_CLOSE_METHODS.filter((method) => method !== 'fiado')
  const differenceCents = summary.surplusCents - summary.shortageCents
  const expectedCents = counted.reduce((total, method) => {
    const line = lines.find((candidate) => candidate.payment_method === method)
    return total + (line?.expected_cents ?? 0)
  }, 0)

  return {
    closedOn: row.closed_on,
    acknowledged: row.acknowledged,
    expectedCents,
    countedCents: expectedCents + differenceCents,
    differenceCents,
    kind: differenceCents === 0 ? 'balanced' : differenceCents > 0 ? 'surplus' : 'shortage',
  }
}

/**
 * Proyecta la foto del día. El orden de los cierres es del más reciente al más viejo porque la
 * lista de auditoría se lee de arriba hacia abajo.
 */
export function projectLedger(
  events: readonly IdempotencyQueueRow[],
  closes: readonly CashCloseRow[],
  businessDate: string,
): LedgerProjection {
  const countable = countableEvents(events)
  const byTender = expectedByMethod(countable, businessDate)

  let netSalesCents = 0
  let ticketCount = 0
  let pendingSales = 0
  const byHour = Array.from({ length: 24 }, () => 0)

  for (const event of countable) {
    if (event.business_date !== businessDate) continue
    if (event.operation !== 'sale' && event.operation !== 'void') continue

    const signed = event.operation === 'sale' ? event.total_cents : -event.total_cents
    netSalesCents += signed
    byHour[localHour(event.created_at)] += signed

    if (event.operation === 'sale') {
      ticketCount += 1
      if (event.status === 'pending') pendingSales += 1
    }
  }

  const localCloses = closes
    .filter((close) => close.counts !== '')
    .map(auditOf)
    .filter((audit): audit is LocalCloseAudit => audit !== null)
    .sort((left, right) => right.closedOn.localeCompare(left.closedOn))

  return {
    businessDate,
    netSalesCents,
    ticketCount,
    averageTicketCents: ticketCount === 0 ? 0 : Math.round(netSalesCents / ticketCount),
    byTender,
    byHour,
    pendingSales,
    localCloses,
  }
}

/**
 * «Fiados por cobrar»: la suma de los saldos positivos. Un saldo negativo es un abono adelantado
 * y no es deuda, así que no se resta del total —la pantalla dice cuánta deuda hay, no el saldo
 * neto de la cartera—.
 */
export function receivableFromAccounts(
  accounts: readonly { readonly balance_cents: number }[],
): number {
  return accounts.reduce((total, account) => (account.balance_cents > 0 ? total + account.balance_cents : total), 0)
}

/** Resultado de pedir la deuda viva al servidor: un número, o la verdad de no tenerlo. */
export type Receivable =
  | { readonly status: 'live'; readonly balanceCents: number }
  | { readonly status: 'offline' }

/**
 * Lee la deuda viva de `GET /fiado-accounts`. Sin sesión en línea o sin red devuelve `offline`
 * —la tarjeta dice «pendiente» y no inventa un cero—: el diseño exige que el KPI de fiado no
 * muestre un número cuando no puede conocerlo (`docs/FRONTEND.md:49`, `FiadoKpiCard`).
 */
export async function readReceivable(input: {
  readonly token?: string
  readonly isOnline: boolean
  readonly fetchImpl?: typeof fetch
  readonly baseUrl?: string
}): Promise<Receivable> {
  const token = input.token?.trim() ?? ''
  if (token === '' || !input.isOnline) return { status: 'offline' }

  const network = input.fetchImpl ?? globalThis.fetch

  let response: Response
  try {
    response = await network(`${input.baseUrl ?? ''}/fiado-accounts`, {
      method: 'GET',
      headers: { authorization: `Bearer ${token}` },
    })
  } catch {
    return { status: 'offline' }
  }
  if (!response.ok) return { status: 'offline' }

  let body: unknown
  try {
    body = await response.json()
  } catch {
    return { status: 'offline' }
  }

  const accounts = (body as { accounts?: unknown })?.accounts
  if (!Array.isArray(accounts)) return { status: 'offline' }

  return {
    status: 'live',
    balanceCents: receivableFromAccounts(accounts as { balance_cents: number }[]),
  }
}

/** Fila del desglose por tender, con su etiqueta y su total neto. */
export function tenderRows(byTender: ExpectedByMethod): readonly {
  readonly method: (typeof CASH_CLOSE_METHODS)[number]
  readonly cents: number
}[] {
  return CASH_CLOSE_METHODS.map((method) => ({ method, cents: byTender[method] }))
}

/** Reexporta el constructor de fila del arqueo para que el dashboard no lo duplique. */
export { countLine }
