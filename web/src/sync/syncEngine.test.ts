// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type IdempotencyQueueRow, type QueueOperation } from '@/db/posDatabase'
import {
  backoffDelay,
  drainQueue,
  endpointFor,
  MAX_BATCH_SIZE,
  MAX_BACKOFF_MS,
  nextBatch,
  retryAfterMs,
} from '@/sync/syncEngine'

/**
 * Reglas del motor de sincronización (W6-FE-3; docs/BACKEND.md §8.2): lotes de 25, retroceso
 * con jitter y techo, `Retry-After` respetado, orden padre → hijo, un solo drenaje activo y
 * CERO errores visibles para el cajero.
 */

let sequence = 0

function freshDatabase(): PosDatabase {
  sequence += 1
  const database = new PosDatabase(`${POS_DATABASE_NAME}-sync-${sequence}`)
  return database
}

function queueRow(overrides: Partial<IdempotencyQueueRow> = {}): IdempotencyQueueRow {
  return {
    id: crypto.randomUUID(),
    key: crypto.randomUUID(),
    payload: '{"sale_id":"019a"}',
    status: 'pending',
    created_at: new Date(1_700_000_000_000 + sequence).toISOString(),
    operation: 'sale',
    business_date: '2026-10-23',
    sale_id: '019a0000-0000-7000-8000-000000000001',
    payment_method: 'cash',
    total_cents: 1250,
    last_error: null,
    ...overrides,
  }
}

function response(status: number, headers: Record<string, string> = {}): Response {
  return new Response(status === 200 ? '{}' : JSON.stringify({ error: { code: 'sale.invalid' } }), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  })
}

afterEach(async () => {
  await Dexie.delete(`${POS_DATABASE_NAME}-sync-${sequence}`)
})

describe('sync engine backoff', () => {
  it('starts at one second and doubles, inside the thirty percent jitter', () => {
    const even = () => 0.5

    expect(backoffDelay(0, even)).toBe(1_000)
    expect(backoffDelay(1, even)).toBe(2_000)
    expect(backoffDelay(3, even)).toBe(8_000)

    for (const attempt of [0, 1, 2, 4]) {
      const low = backoffDelay(attempt, () => 0)
      const high = backoffDelay(attempt, () => 1)
      const base = Math.min(1_000 * 2 ** attempt, MAX_BACKOFF_MS)

      expect(low).toBeGreaterThanOrEqual(Math.round(base * 0.7) - 1)
      expect(high).toBeLessThanOrEqual(Math.round(base * 1.3) + 1)
    }
  })

  it('never waits more than the sixty second ceiling', () => {
    for (const attempt of [6, 10, 40]) {
      expect(backoffDelay(attempt, () => 1)).toBeLessThanOrEqual(MAX_BACKOFF_MS)
      expect(backoffDelay(attempt, () => 0)).toBeLessThanOrEqual(MAX_BACKOFF_MS)
    }
  })

  it('reads the retry advice of a 409 and ignores an unreadable one', () => {
    expect(retryAfterMs('2')).toBe(2_000)
    expect(retryAfterMs('0')).toBe(0)
    expect(retryAfterMs(null)).toBeNull()
    expect(retryAfterMs('más tarde')).toBeNull()
  })
})

describe('sync engine batching', () => {
  it('drains at most twenty five events per batch, parents before children', () => {
    const rows = [
      ...Array.from({ length: 30 }, () => queueRow({ operation: 'sale' as QueueOperation })),
      queueRow({ operation: 'void' }),
      queueRow({ operation: 'cash_close' }),
    ]

    const batch = nextBatch(rows)

    expect(batch).toHaveLength(MAX_BATCH_SIZE)
    expect(batch.every((row) => row.operation === 'sale')).toBe(true)
  })

  it('orders a sale before its void and the cash close last', () => {
    const cashClose = queueRow({ operation: 'cash_close', created_at: '2026-10-23T09:00:00.000Z' })
    const voidRow = queueRow({ operation: 'void', created_at: '2026-10-23T08:00:00.000Z' })
    const saleRow = queueRow({ operation: 'sale', created_at: '2026-10-23T10:00:00.000Z' })

    expect(nextBatch([cashClose, voidRow, saleRow]).map((row) => row.operation)).toEqual([
      'sale',
      'void',
      'cash_close',
    ])
  })

  it('posts each queued event to its contract endpoint with its own idempotency key', () => {
    expect(endpointFor(queueRow({ operation: 'sale' }))).toBe('/sales')
    expect(endpointFor(queueRow({ operation: 'void' }))).toBe('/sales/019a0000-0000-7000-8000-000000000001/void')
    expect(endpointFor(queueRow({ operation: 'cash_close' }))).toBe('/cash-closes')
  })
})

describe('sync engine drain', () => {
  it('acknowledges every duplicate only once, each with its own key', async () => {
    const database = freshDatabase()
    const first = queueRow()
    const second = queueRow()
    await database.idempotency_queue.bulkAdd([first, second])
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      void input
      void init
      return response(200)
    })

    try {
      const summary = await drainQueue({ database, fetchImpl, token: 'token', isOnline: () => true })

      expect(summary).toMatchObject({ acknowledged: 2, rejected: 0, shouldRetry: false })
      expect(fetchImpl).toHaveBeenCalledTimes(2)
      const keys = fetchImpl.mock.calls.map((call) => (call[1]?.headers ?? {}) as Record<string, string>)
      expect(keys.map((headers) => headers['Idempotency-Key']).sort()).toEqual([first.key, second.key].sort())
      expect(await database.idempotency_queue.where('status').equals('completed').count()).toBe(2)
    } finally {
      database.close()
    }
  })

  it('honours the Retry-After of a 409 and leaves the event pending', async () => {
    const database = freshDatabase()
    const row = queueRow()
    await database.idempotency_queue.add(row)
    const fetchImpl = vi.fn(async () => response(409, { 'Retry-After': '3' }))

    try {
      const summary = await drainQueue({ database, fetchImpl, token: 'token', isOnline: () => true })

      expect(summary.shouldRetry).toBe(true)
      expect(summary.retryAfterMs).toBe(3_000)
      expect(summary.acknowledged).toBe(0)
      expect((await database.idempotency_queue.get(row.id))?.status).toBe('pending')
    } finally {
      database.close()
    }
  })

  it('records a definitive rejection without surfacing an error to the cashier', async () => {
    const database = freshDatabase()
    const row = queueRow()
    await database.idempotency_queue.add(row)
    const fetchImpl = vi.fn(async () => response(422))

    try {
      const summary = await drainQueue({ database, fetchImpl, token: 'token', isOnline: () => true })

      expect(summary).toMatchObject({ acknowledged: 0, rejected: 1, shouldRetry: false })
      const stored = await database.idempotency_queue.get(row.id)
      expect(stored?.status).toBe('failed')
      expect(stored?.last_error).toBe('sale.invalid')
    } finally {
      database.close()
    }
  })

  it('waits without draining when there is no session, no connectivity or another drain', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.add(queueRow())
    const fetchImpl = vi.fn(async () => response(200))

    try {
      expect(await drainQueue({ database, fetchImpl, token: 'token', isOnline: () => false })).toMatchObject({
        skipped: 'offline',
      })
      expect(await drainQueue({ database, fetchImpl, isOnline: () => true })).toMatchObject({
        skipped: 'no-session',
      })
      expect(fetchImpl).not.toHaveBeenCalled()

      const pending = drainQueue({ database, fetchImpl, token: 'token', isOnline: () => true })
      const overlapping = await drainQueue({ database, fetchImpl, token: 'token', isOnline: () => true })
      expect(overlapping).toMatchObject({ skipped: 'already-draining' })
      await pending
    } finally {
      database.close()
    }
  })

  it('keeps the event pending when the network fails mid-drain', async () => {
    const database = freshDatabase()
    const row = queueRow()
    await database.idempotency_queue.add(row)
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    })

    try {
      const summary = await drainQueue({ database, fetchImpl, token: 'token', isOnline: () => true })

      expect(summary).toMatchObject({ acknowledged: 0, rejected: 0, shouldRetry: true })
      expect((await database.idempotency_queue.get(row.id))?.status).toBe('pending')
    } finally {
      database.close()
    }
  })
})
