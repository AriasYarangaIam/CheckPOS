// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type IdempotencyQueueRow } from '@/db/posDatabase'
import {
  CASH_CLOSE_LABELS,
  CASH_CLOSE_METHODS,
  acknowledgeCashClose,
  buildCashClosePayload,
  cashCloseLinesOf,
  closeTotalSummary,
  countLine,
  expectedByMethod,
  persistCashClose,
  readCashClose,
  submitCashClose,
  type CashCloseAck,
} from '@/sales/cashClose'
import { formatBusinessDate } from '@/sales/saleClose'

/*
 * Cierre de caja del cliente (W5-FE-2; docs/DATABASE.md §4 `cash_close_counts`;
 * SPRINTS.md:266). El esperado del arqueo es una PROYECCIÓN del ledger local
 * —las ventas y anulaciones congeladas de esa fecha de negocio, agrupadas por
 * método—, nunca un recálculo de líneas (decisión 7 del sprint). La diferencia
 * se computa, y la de fiado es informacional por diseño (§4.13, F32).
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-cash-close-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

/** Evento del ledger local con la proyección congelada de la fila. */
function ledgerEvent(input: {
  readonly id: string
  readonly operation: IdempotencyQueueRow['operation']
  readonly businessDate: string
  readonly paymentMethod: IdempotencyQueueRow['payment_method']
  readonly totalCents: number
}): IdempotencyQueueRow {
  return {
    id: input.id,
    key: `clave-${input.id}`,
    payload: '{}',
    status: 'pending',
    created_at: '2026-10-20T10:00:00.000Z',
    operation: input.operation,
    business_date: input.businessDate,
    sale_id: input.operation === 'cash_close' ? null : input.id,
    payment_method: input.paymentMethod,
    total_cents: input.totalCents,
    last_error: null,
  }
}

/** Acuse del contrato, con los seis métodos en el orden fijo del servidor. */
function ackBody(input?: {
  readonly id?: string
  readonly closedOn?: string
  readonly expected?: Partial<Record<(typeof CASH_CLOSE_METHODS)[number], number>>
  readonly counted?: Partial<Record<(typeof CASH_CLOSE_METHODS)[number], number>>
}): CashCloseAck {
  return {
    cash_close: {
      id: input?.id ?? '0a300000-0000-7000-8000-0000000000aa',
      tenant_id: '0a300000-0000-7000-8000-0000000000bb',
      closed_on: input?.closedOn ?? '2026-10-20',
      closed_by_user_id: '0a300000-0000-7000-8000-0000000000cc',
      created_at: '2026-10-20T22:00:00.000Z',
      received_at: '2026-10-20T22:00:00.000Z',
    },
    counts: CASH_CLOSE_METHODS.map((method) => {
      const expected = input?.expected?.[method] ?? 0
      const counted = input?.counted?.[method] ?? 0

      return {
        payment_method: method,
        expected_cents: expected,
        counted_cents: counted,
        // El fiado nunca lleva diferencia: es informacional (§4.13).
        difference_cents: method === 'fiado' ? null : counted - expected,
      }
    }),
  }
}

describe('CASH_CLOSE_METHODS', () => {
  it('is the six methods in the fixed order of the CHECK, with Spanish labels', () => {
    expect(CASH_CLOSE_METHODS).toEqual(['cash', 'yape', 'plin', 'card', 'other', 'fiado'])
    expect(CASH_CLOSE_METHODS.map((method) => CASH_CLOSE_LABELS[method])).toEqual([
      'Efectivo',
      'Yape',
      'Plin',
      'Tarjeta',
      'Otro',
      'Fiado',
    ])
  })
})

describe('expectedByMethod', () => {
  it('adds sales and subtracts voids of that business date, grouped by method', () => {
    const events = [
      ledgerEvent({ id: 's1', operation: 'sale', businessDate: '2026-10-20', paymentMethod: 'cash', totalCents: 1250 }),
      ledgerEvent({ id: 's2', operation: 'sale', businessDate: '2026-10-20', paymentMethod: 'cash', totalCents: 750 }),
      ledgerEvent({ id: 's3', operation: 'sale', businessDate: '2026-10-20', paymentMethod: 'yape', totalCents: 3000 }),
      // La anulación espeja el total positivo y el esperado la resta.
      ledgerEvent({ id: 'v1', operation: 'void', businessDate: '2026-10-20', paymentMethod: 'cash', totalCents: 500 }),
    ]

    expect(expectedByMethod(events, '2026-10-20')).toEqual({
      cash: 1500,
      yape: 3000,
      plin: 0,
      card: 0,
      other: 0,
      fiado: 0,
    })
  })

  it('never counts another business date, a cash close, or a row without method', () => {
    const events = [
      ledgerEvent({ id: 's1', operation: 'sale', businessDate: '2026-10-19', paymentMethod: 'cash', totalCents: 999 }),
      ledgerEvent({ id: 'c1', operation: 'cash_close', businessDate: '2026-10-20', paymentMethod: null, totalCents: 0 }),
      ledgerEvent({ id: 's2', operation: 'sale', businessDate: '2026-10-20', paymentMethod: 'plin', totalCents: 400 }),
    ]

    expect(expectedByMethod(events, '2026-10-20')).toEqual({
      cash: 0,
      yape: 0,
      plin: 400,
      card: 0,
      other: 0,
      fiado: 0,
    })
  })

  it('fills every missing method with zero', () => {
    expect(Object.values(expectedByMethod([], '2026-10-20'))).toEqual([0, 0, 0, 0, 0, 0])
  })
})

describe('countLine', () => {
  it('computes the difference as counted minus expected for the five counted methods', () => {
    expect(countLine('cash', 1000, 1250).difference_cents).toBe(250)
    expect(countLine('yape', 1000, 900).difference_cents).toBe(-100)
  })

  it('leaves the fiado difference without a value: it is informational', () => {
    // Aunque el contado de fiado sea cero, su diferencia no es un faltante.
    expect(countLine('fiado', 5000, 0).difference_cents).toBeNull()
  })
})

describe('closeTotalSummary', () => {
  it('aggregates sobrante and faltante over the five counted methods only', () => {
    const summary = closeTotalSummary([
      countLine('cash', 1000, 1250),
      countLine('yape', 3000, 2800),
      countLine('plin', 500, 500),
      countLine('card', 0, 0),
      countLine('other', 0, 0),
      // El fiado jamás entra al resumen: su diferencia es informacional.
      countLine('fiado', 5000, 0),
    ])

    expect(summary).toEqual({ surplusCents: 250, shortageCents: 200 })
  })

  it('reports zero on a perfect count', () => {
    expect(closeTotalSummary([countLine('cash', 1234, 1234)])).toEqual({
      surplusCents: 0,
      shortageCents: 0,
    })
  })
})

describe('buildCashClosePayload', () => {
  const counts = { cash: 1500, yape: 0, plin: 300, card: 0, other: 0, fiado: 0 }

  it('builds the exact body of the contract: six rows, id, closed_on and counted_cents', () => {
    const payload = buildCashClosePayload(counts, {
      id: '0a300000-0000-7000-8000-00000000000d',
      now: () => new Date(2026, 9, 20, 23, 10),
    })

    // La fecha de negocio es LOCAL al mostrador, nunca el UTC del día siguiente.
    expect(payload).toEqual({
      id: '0a300000-0000-7000-8000-00000000000d',
      closed_on: '2026-10-20',
      counts: [
        { payment_method: 'cash', counted_cents: 1500 },
        { payment_method: 'yape', counted_cents: 0 },
        { payment_method: 'plin', counted_cents: 300 },
        { payment_method: 'card', counted_cents: 0 },
        { payment_method: 'other', counted_cents: 0 },
        { payment_method: 'fiado', counted_cents: 0 },
      ],
    })
    expect(payload.closed_on).toBe(formatBusinessDate(new Date(2026, 9, 20, 23, 10)))
  })

  it('refuses a fiado count other than zero before asking the server', () => {
    // El CHECK de `cash_close_counts` y la validación de superficie rechazan un
    // fiado contado: el cliente no manda un cuerpo que va a volver 422.
    expect(() => buildCashClosePayload({ ...counts, fiado: 100 })).toThrow('fiado')
  })

  it('refuses a fractional or negative count: money is integer cents', () => {
    expect(() => buildCashClosePayload({ ...counts, cash: 10.5 })).toThrow('centavos')
    expect(() => buildCashClosePayload({ ...counts, cash: -1 })).toThrow('centavos')
  })
})

describe('submitCashClose', () => {
  const payload = buildCashClosePayload(
    { cash: 1500, yape: 0, plin: 300, card: 0, other: 0, fiado: 0 },
    { id: '0a300000-0000-7000-8000-00000000000e', now: () => new Date(2026, 9, 20) },
  )

  function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json', ...headers },
    })
  }

  it('issues the POST with the idempotency key, the frozen body and the bearer token', async () => {
    const fetchImpl = vi.fn(async () => jsonResponse(200, ackBody({ id: payload.id })))

    const outcome = await submitCashClose({
      token: 'token-de-la-sesion',
      payload,
      fetchImpl: fetchImpl as unknown as typeof fetch,
    })

    expect(fetchImpl).toHaveBeenCalledTimes(1)

    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]

    expect(url).toBe('/cash-closes')
    expect((init.headers as Record<string, string>).authorization).toBe('Bearer token-de-la-sesion')
    expect((init.headers as Record<string, string>)['idempotency-key']).toBe(payload.id)
    expect(JSON.parse(String(init.body))).toEqual(payload)
    expect(outcome.status).toBe('acknowledged')
  })

  it('exposes the frozen ack of a fresh close', async () => {
    const ack = ackBody({ id: payload.id, expected: { cash: 1200 }, counted: { cash: 1500 } })
    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: (async () => jsonResponse(200, ack)) as unknown as typeof fetch,
    })

    expect(outcome).toEqual({ status: 'acknowledged', ack })
  })

  it('treats the existing close of the date as a duplicate, not an error', async () => {
    // F38: el cierre que ya existía prevalece. El `id` del acuse difiere del
    // que se envió —es otra fila— y su contado puede diferir de lo tipeado.
    const existing = ackBody({ id: 'cierre-que-ya-existia', counted: { cash: 1200 } })
    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: (async () => jsonResponse(200, existing)) as unknown as typeof fetch,
    })

    expect(outcome).toEqual({ status: 'duplicate', ack: existing })
  })

  it('retries the same key and body after a 409, honoring Retry-After', async () => {
    const sleeps: number[] = []
    let calls = 0
    const fetchImpl = vi.fn(async () => {
      calls += 1

      return calls === 1
        ? jsonResponse(409, { error: { code: 'in_flight', message: 'En vuelo' } }, { 'retry-after': '2' })
        : jsonResponse(200, ackBody({ id: payload.id }))
    })

    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: fetchImpl as unknown as typeof fetch,
      sleep: async (ms) => {
        sleeps.push(ms)
      },
    })

    expect(outcome.status).toBe('acknowledged')
    expect(sleeps).toEqual([2000])
    expect(fetchImpl.mock.calls[1]).toEqual(fetchImpl.mock.calls[0])
  })

  it('reports an exhausted conflict as unconfirmed, never as success', async () => {
    const fetchImpl = vi.fn(async () =>
      jsonResponse(409, { error: { code: 'in_flight', message: 'En vuelo' } }, { 'retry-after': '0' }),
    )

    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: fetchImpl as unknown as typeof fetch,
      sleep: async () => {},
      maxInFlightRetries: 2,
    })

    expect(outcome).toEqual({ status: 'unconfirmed' })
    expect(fetchImpl).toHaveBeenCalledTimes(3)
  })

  it('returns the server envelope of a rejected body', async () => {
    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: (async () =>
        jsonResponse(422, {
          error: { code: 'validation_failed', message: 'El cuerpo no es válido' },
        })) as unknown as typeof fetch,
    })

    expect(outcome).toEqual({
      status: 'rejected',
      error: { code: 'validation_failed', message: 'El cuerpo no es válido' },
    })
  })

  it('reports a dead network as unconfirmed', async () => {
    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: (async () => {
        throw new TypeError('red caída')
      }) as unknown as typeof fetch,
    })

    expect(outcome).toEqual({ status: 'unconfirmed' })
  })

  it('never fakes success on a 200 whose body is not the contract', async () => {
    const outcome = await submitCashClose({
      token: 'token',
      payload,
      fetchImpl: (async () => jsonResponse(200, { close: 'otra cosa' })) as unknown as typeof fetch,
    })

    expect(outcome.status).toBe('rejected')
  })
})

describe('the local cash close store', () => {
  it('keeps one close per business date and reads it back', async () => {
    const database = freshDatabase()

    try {
      const first = await persistCashClose(
        { id: 'cierre-1', closed_on: '2026-10-20', counts: [] },
        [countLine('cash', 1000, 1000)],
        database,
      )

      expect(await readCashClose('2026-10-20', database)).toEqual(first)
      expect(first.acknowledged).toBe(false)

      // Reabrir el arqueo de la misma fecha no crea un segundo cierre local: la
      // fecha de negocio es la clave, igual que la UNIQUE del servidor.
      await persistCashClose({ id: 'cierre-2', closed_on: '2026-10-20', counts: [] }, [], database)

      expect(await database.cash_closes.count()).toBe(1)
    } finally {
      database.close()
    }
  })

  it('replaces the local expectation with the frozen server ack once acknowledged', async () => {
    const database = freshDatabase()
    const ack = ackBody({ id: 'cierre-acusado', expected: { cash: 1200 }, counted: { cash: 1200 } })

    try {
      await persistCashClose(
        { id: 'cierre-local', closed_on: '2026-10-20', counts: [] },
        [countLine('cash', 1000, 1200)],
        database,
      )
      await acknowledgeCashClose(ack, database)

      const stored = await readCashClose('2026-10-20', database)

      expect(stored?.acknowledged).toBe(true)
      expect(stored?.id).toBe('cierre-acusado')
      expect(cashCloseLinesOf(stored!).find((line) => line.payment_method === 'cash')).toEqual({
        payment_method: 'cash',
        expected_cents: 1200,
        counted_cents: 1200,
        difference_cents: 0,
      })
    } finally {
      database.close()
    }
  })
})
