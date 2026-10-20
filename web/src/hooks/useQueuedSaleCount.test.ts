// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'
import { useQueuedSaleCount } from '@/hooks/useQueuedSaleCount'
import { enqueueSaleClose, markSaleQueueStatus } from '@/sales/saleQueue'

/*
 * Contador en vivo de ventas pendientes (W5-FE-1). El estado verdadero es la
 * cola de IndexedDB: montar cuenta lo que quedó escrito —la prueba de que matar
 * la pestaña no pierde la venta— y cada mutación relee la tabla, sin una segunda
 * copia del contador en React.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-queued-sale-count-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const PAYLOAD = {
  sale_id: '0a300000-0000-7000-8000-000000000001',
  sold_on: '2026-10-20',
  payment_method: 'cash' as const,
  total_cents: 300,
  lines: [],
}

describe('useQueuedSaleCount', () => {
  it('reads the pending sales already persisted on mount', async () => {
    // Un cierre sin red, la pestaña muerta y una instancia nueva: el conteo
    // sale del almacén, no de la memoria del proceso que cerró la venta.
    const database = freshDatabase()
    await enqueueSaleClose(PAYLOAD, 'clave-1', database)
    database.close()

    const reopened = new PosDatabase(database.name)
    const { result, unmount } = renderHook(() => useQueuedSaleCount(reopened))

    await waitFor(() => expect(result.current).toBe(1))

    unmount()
    reopened.close()
  })

  it('follows the queue without remounting and drops a confirmed sale', async () => {
    const database = freshDatabase()
    await enqueueSaleClose(PAYLOAD, 'clave-2', database)

    const { result, unmount } = renderHook(() => useQueuedSaleCount(database))
    await waitFor(() => expect(result.current).toBe(1))

    await act(async () => {
      await markSaleQueueStatus('clave-2', 'completed', undefined, database)
    })

    await waitFor(() => expect(result.current).toBe(0))

    unmount()
    database.close()
  })
})
