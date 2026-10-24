// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type CartRow } from '@/db/posDatabase'
import {
  enqueueSaleClose,
  markSaleQueueStatus,
  pendingSaleCount,
  readQueuedSales,
  subscribeToSaleQueue,
} from '@/sales/saleQueue'
import type { SaleClosePayload } from '@/sales/saleClose'

/*
 * Cola local de la venta cerrada (W5-FE-1; SPRINTS.md:265). La venta se escribe
 * en `idempotency_queue` ANTES del ack y el carrito se vacía en la MISMA
 * transacción: matar la pestaña entre las dos escrituras no puede perder el
 * evento (ARCHITECTURE.md:168).
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-sale-queue-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const CART_LINES: readonly CartRow[] = [
  {
    id: 'line-1',
    product_id: 'prod-pan',
    qty: '2',
    unit_price_cents: 30,
    name: 'Pan francés',
    uom: 'unit',
    voided: false,
  },
]

const PAYLOAD: SaleClosePayload = {
  sale_id: '0a300000-0000-7000-8000-000000000001',
  sold_on: '2026-10-20',
  payment_method: 'yape',
  total_cents: 60,
  lines: [
    {
      line_no: 1,
      product_id: 'prod-pan',
      name: 'Pan francés',
      price_cents: 30,
      uom: 'unit',
      qty: '2',
      line_total_cents: 60,
    },
  ],
}

describe('enqueueSaleClose', () => {
  it('writes the event as pending with the frozen projection and empties the cart', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd(CART_LINES)

    try {
      await enqueueSaleClose(PAYLOAD, 'clave-de-la-venta', database)

      const [row] = await database.idempotency_queue.toArray()

      expect(row).toEqual({
        id: expect.any(String),
        key: 'clave-de-la-venta',
        // El cuerpo congelado viaja serializado tal como se enviará.
        payload: JSON.stringify(PAYLOAD),
        status: 'pending',
        created_at: expect.any(String),
        operation: 'sale',
        business_date: '2026-10-20',
        sale_id: PAYLOAD.sale_id,
        payment_method: 'yape',
        total_cents: 60,
        last_error: null,
      })
      expect(await database.cart.count()).toBe(0)
    } finally {
      database.close()
    }
  })

  it('writes the event and clears the cart in one transaction, so a failure keeps both intact', async () => {
    /*
     * La atomicidad es el punto: si el carrito no se puede vaciar, la fila de la
     * cola tiene que abortar con él. Se fuerza el fallo en `cart.clear` y se lee
     * lo que quedó escrito: la transacción revierte la fila, así que ni el
     * evento a medias ni un carrito vaciado sin evento pueden existir.
     */
    const database = freshDatabase()
    await database.cart.bulkAdd(CART_LINES)

    const clear = vi.spyOn(database.cart, 'clear').mockRejectedValue(new Error('cuota agotada'))

    try {
      await expect(enqueueSaleClose(PAYLOAD, 'clave-fallida', database)).rejects.toThrow(
        'cuota agotada',
      )

      expect(await database.idempotency_queue.count()).toBe(0)
      expect(await database.cart.count()).toBe(1)
    } finally {
      clear.mockRestore()
      database.close()
    }
  })

  it('notifies the subscribers once the event is committed', async () => {
    const database = freshDatabase()
    const listener = vi.fn()
    const unsubscribe = subscribeToSaleQueue(listener)

    try {
      await enqueueSaleClose(PAYLOAD, 'clave-notificada', database)

      expect(listener).toHaveBeenCalled()
    } finally {
      unsubscribe()
      database.close()
    }
  })
})

describe('markSaleQueueStatus', () => {
  it('moves the event to completed without an error', async () => {
    const database = freshDatabase()
    await enqueueSaleClose(PAYLOAD, 'clave-1', database)

    try {
      await markSaleQueueStatus('clave-1', 'completed', undefined, database)

      expect((await database.idempotency_queue.toArray())[0]?.status).toBe('completed')
      expect((await database.idempotency_queue.toArray())[0]?.last_error).toBeNull()
    } finally {
      database.close()
    }
  })

  it('keeps the server code of a failed event', async () => {
    const database = freshDatabase()
    await enqueueSaleClose(PAYLOAD, 'clave-2', database)

    try {
      await markSaleQueueStatus('clave-2', 'failed', 'idempotency.payload_mismatch', database)

      const row = (await database.idempotency_queue.toArray())[0]

      expect(row?.status).toBe('failed')
      expect(row?.last_error).toBe('idempotency.payload_mismatch')
    } finally {
      database.close()
    }
  })

  it('ignores a key it does not know instead of creating a row', async () => {
    const database = freshDatabase()

    try {
      await markSaleQueueStatus('clave-ausente', 'completed', undefined, database)

      expect(await database.idempotency_queue.count()).toBe(0)
    } finally {
      database.close()
    }
  })
})

describe('readQueuedSales', () => {
  it('reads the sale ledger of the device —sales and their voids— and leaves cash closes out', async () => {
    const database = freshDatabase()
    await enqueueSaleClose(PAYLOAD, 'clave-venta', database)
    await database.idempotency_queue.bulkAdd([
      {
        id: 'void-1',
        key: 'clave-anulacion',
        payload: JSON.stringify({ sold_on: '2026-10-20' }),
        status: 'pending',
        created_at: '2026-10-20T10:00:00.000Z',
        operation: 'void',
        business_date: '2026-10-20',
        sale_id: PAYLOAD.sale_id,
        payment_method: 'yape',
        total_cents: 60,
        last_error: null,
      },
      {
        id: 'close-1',
        key: 'clave-cierre',
        payload: JSON.stringify({ closed_on: '2026-10-20' }),
        status: 'completed',
        created_at: '2026-10-20T22:00:00.000Z',
        operation: 'cash_close',
        business_date: '2026-10-20',
        sale_id: null,
        payment_method: null,
        total_cents: 0,
        last_error: null,
      },
    ])

    try {
      const rows = await readQueuedSales(database)

      expect(rows.map((row) => row.operation).sort()).toEqual(['sale', 'void'])
    } finally {
      database.close()
    }
  })

  it('counts only the sales still waiting for the server', () => {
    // El badge dice «ventas por sincronizar»: una anulación pendiente no es una
    // venta y una venta confirmada ya no espera nada.
    const rows = [
      { operation: 'sale' as const, status: 'pending' as const },
      { operation: 'sale' as const, status: 'pending' as const },
      { operation: 'sale' as const, status: 'completed' as const },
      { operation: 'sale' as const, status: 'failed' as const },
      { operation: 'void' as const, status: 'pending' as const },
      { operation: 'cash_close' as const, status: 'pending' as const },
    ]

    expect(pendingSaleCount(rows as never)).toBe(2)
  })
})
