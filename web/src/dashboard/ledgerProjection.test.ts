import { describe, expect, it } from 'vitest'

import type { CashCloseRow, IdempotencyQueueRow, QueueStatus } from '@/db/posDatabase'
import {
  projectLedger,
  receivableFromAccounts,
  readReceivable,
  tenderRows,
} from '@/dashboard/ledgerProjection'
import type { TenderMethod } from '@/sales/tenderMethods'

/*
 * Proyección del ledger local del dashboard (W8-FE-3; decisión D-E del sprint 8).
 *
 * Lo que se fija: el día del dueño incluye las ventas todavía sin sincronizar y excluye las que el
 * servidor rechazó, la anulación resta sin ser un ticket, el desglose cubre los seis tenders, las
 * horas son las del reloj LOCAL, el fiado nunca se cuenta como faltante, y la deuda viva es la
 * suma de los saldos positivos.
 */

const DATE = '2026-10-26'
const OTHER_DATE = '2026-10-25'

/** Instante ISO a una hora LOCAL fija: la zona del runner no altera la aserción. */
function atLocal(hour: number, minute = 0): string {
  return new Date(2026, 9, 26, hour, minute, 0).toISOString()
}

let nextId = 0

function event(overrides: {
  operation: 'sale' | 'void' | 'cash_close' | 'fiado_movement'
  totalCents: number
  paymentMethod?: TenderMethod | null
  status?: QueueStatus
  businessDate?: string
  createdAt?: string
}): IdempotencyQueueRow {
  nextId += 1

  return {
    id: `row-${nextId}`,
    key: `key-${nextId}`,
    payload: '{}',
    status: overrides.status ?? 'completed',
    created_at: overrides.createdAt ?? atLocal(10),
    operation: overrides.operation,
    business_date: overrides.businessDate ?? DATE,
    sale_id: null,
    payment_method: overrides.paymentMethod ?? null,
    total_cents: overrides.totalCents,
    last_error: null,
  }
}

function close(overrides: {
  closedOn: string
  expected: Partial<Record<TenderMethod, number>>
  counted: Partial<Record<TenderMethod, number>>
}): CashCloseRow {
  const methods: TenderMethod[] = ['cash', 'yape', 'plin', 'card', 'other', 'fiado']

  return {
    id: `close-${overrides.closedOn}`,
    closed_on: overrides.closedOn,
    created_at: '2026-10-26T23:00:00.000Z',
    acknowledged: false,
    counts: JSON.stringify(
      methods.map((method) => ({
        payment_method: method,
        expected_cents: overrides.expected[method] ?? 0,
        counted_cents: overrides.counted[method] ?? 0,
        difference_cents: method === 'fiado' ? null : (overrides.counted[method] ?? 0) - (overrides.expected[method] ?? 0),
      })),
    ),
  }
}

describe('projectLedger', () => {
  it('counts a pending sale and ignores a rejected one', () => {
    // D-E: el día incluye lo que todavía no sincronizó; lo rechazado por el servidor nunca fue
    // una venta.
    const projection = projectLedger(
      [
        event({ operation: 'sale', totalCents: 1000, paymentMethod: 'cash', status: 'completed' }),
        event({ operation: 'sale', totalCents: 500, paymentMethod: 'yape', status: 'pending' }),
        event({ operation: 'sale', totalCents: 9000, paymentMethod: 'cash', status: 'failed' }),
      ],
      [],
      DATE,
    )

    expect(projection.netSalesCents).toBe(1500)
    expect(projection.ticketCount).toBe(2)
    expect(projection.pendingSales).toBe(1)
    expect(projection.byTender.cash).toBe(1000)
    expect(projection.byTender.yape).toBe(500)
  })

  it('subtracts a void without counting it as a ticket', () => {
    const projection = projectLedger(
      [
        event({ operation: 'sale', totalCents: 2000, paymentMethod: 'cash' }),
        event({ operation: 'void', totalCents: 500, paymentMethod: 'cash' }),
      ],
      [],
      DATE,
    )

    expect(projection.netSalesCents).toBe(1500)
    expect(projection.ticketCount).toBe(1)
    expect(projection.byTender.cash).toBe(1500)
  })

  it('computes the average ticket over the closed tickets, rounded', () => {
    const projection = projectLedger(
      [
        event({ operation: 'sale', totalCents: 1000, paymentMethod: 'cash' }),
        event({ operation: 'sale', totalCents: 333, paymentMethod: 'plin' }),
        event({ operation: 'sale', totalCents: 334, paymentMethod: 'card' }),
      ],
      [],
      DATE,
    )

    expect(projection.ticketCount).toBe(3)
    expect(projection.averageTicketCents).toBe(Math.round(1667 / 3))
  })

  it('answers zero average without tickets and ignores another business date', () => {
    const projection = projectLedger(
      [event({ operation: 'sale', totalCents: 800, paymentMethod: 'cash', businessDate: OTHER_DATE })],
      [],
      DATE,
    )

    expect(projection.netSalesCents).toBe(0)
    expect(projection.ticketCount).toBe(0)
    expect(projection.averageTicketCents).toBe(0)
  })

  it('breaks the day down over the six tenders and the local hour', () => {
    const projection = projectLedger(
      [
        event({ operation: 'sale', totalCents: 100, paymentMethod: 'cash', createdAt: atLocal(9) }),
        event({ operation: 'sale', totalCents: 200, paymentMethod: 'fiado', createdAt: atLocal(9, 30) }),
        event({ operation: 'sale', totalCents: 300, paymentMethod: 'other', createdAt: atLocal(18) }),
        event({ operation: 'void', totalCents: 50, paymentMethod: 'cash', createdAt: atLocal(18, 15) }),
      ],
      [],
      DATE,
    )

    expect(tenderRows(projection.byTender).map((row) => row.method)).toEqual([
      'cash',
      'yape',
      'plin',
      'card',
      'other',
      'fiado',
    ])
    expect(projection.byTender).toEqual({ cash: 50, yape: 0, plin: 0, card: 0, other: 300, fiado: 200 })
    expect(projection.byHour).toHaveLength(24)
    expect(projection.byHour[9]).toBe(300)
    expect(projection.byHour[18]).toBe(250)
    expect(projection.byHour[0]).toBe(0)
  })

  it('audits the local closes with their over or short, leaving fiado out', () => {
    const projection = projectLedger(
      [],
      [
        close({
          closedOn: OTHER_DATE,
          expected: { cash: 1000, fiado: 500 },
          counted: { cash: 1200, fiado: 0 },
        }),
        close({ closedOn: DATE, expected: { cash: 1000 }, counted: { cash: 900 } }),
      ],
      DATE,
    )

    // El orden es del más reciente al más viejo.
    expect(projection.localCloses.map((audit) => audit.closedOn)).toEqual([DATE, OTHER_DATE])

    const [today, yesterday] = projection.localCloses
    expect(today.kind).toBe('shortage')
    expect(today.differenceCents).toBe(-100)
    expect(today.expectedCents).toBe(1000)
    expect(today.countedCents).toBe(900)

    // El fiado queda afuera de la diferencia: con 500 esperados y 0 contados, el cierre sigue
    // siendo sobrante de 200 y no un faltante de 300 (§4.13, F32).
    expect(yesterday.kind).toBe('surplus')
    expect(yesterday.differenceCents).toBe(200)
  })

  it('marks a close without difference as balanced', () => {
    const projection = projectLedger([], [close({ closedOn: DATE, expected: { yape: 500 }, counted: { yape: 500 } })], DATE)

    expect(projection.localCloses[0].kind).toBe('balanced')
    expect(projection.localCloses[0].differenceCents).toBe(0)
  })

  it('skips a close whose counts cannot be read instead of inventing one', () => {
    const broken: CashCloseRow = {
      id: 'broken',
      closed_on: DATE,
      created_at: '2026-10-26T23:00:00.000Z',
      counts: 'no-es-json',
      acknowledged: false,
    }

    expect(projectLedger([], [broken], DATE).localCloses).toEqual([])
  })
})

describe('la deuda viva', () => {
  it('sums only the positive balances: an advance is not a debt', () => {
    expect(receivableFromAccounts([{ balance_cents: 1500 }, { balance_cents: -500 }, { balance_cents: 0 }])).toBe(1500)
    expect(receivableFromAccounts([])).toBe(0)
  })

  it('says pending instead of zero when there is no session', async () => {
    expect(await readReceivable({ isOnline: true })).toEqual({ status: 'offline' })
  })

  it('says pending when there is no network, without calling the server', async () => {
    let called = false
    const fetchImpl = (async () => {
      called = true
      return new Response('{}', { status: 200 })
    }) as unknown as typeof fetch

    expect(await readReceivable({ token: 'jwt', isOnline: false, fetchImpl })).toEqual({ status: 'offline' })
    expect(called).toBe(false)
  })

  it('reads the live balance from GET /fiado-accounts with the session token', async () => {
    const seen: string[] = []
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      seen.push(`${String(url)} ${String((init?.headers as Record<string, string>).authorization)}`)
      return new Response(
        JSON.stringify({ accounts: [{ balance_cents: 1500 }, { balance_cents: -200 }] }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )
    }) as unknown as typeof fetch

    expect(await readReceivable({ token: 'jwt', isOnline: true, fetchImpl })).toEqual({
      status: 'live',
      balanceCents: 1500,
    })
    expect(seen[0]).toBe('/fiado-accounts Bearer jwt')
  })

  it('says pending when the server refuses or the network fails', async () => {
    const refused = (async () => new Response('{}', { status: 500 })) as unknown as typeof fetch
    const broken = (async () => {
      throw new Error('network down')
    }) as unknown as typeof fetch

    expect(await readReceivable({ token: 'jwt', isOnline: true, fetchImpl: refused })).toEqual({ status: 'offline' })
    expect(await readReceivable({ token: 'jwt', isOnline: true, fetchImpl: broken })).toEqual({ status: 'offline' })
  })
})
