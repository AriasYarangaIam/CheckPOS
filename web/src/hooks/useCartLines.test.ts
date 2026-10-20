// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { annulCartLine } from '@/cart/cartStore'
import { POS_DATABASE_NAME, PosDatabase, type CartRow } from '@/db/posDatabase'
import { useCartLines } from '@/hooks/useCartLines'

/*
 * Lectura en vivo del carrito (W4-FE-1): el hook consume la tabla `cart` y
 * sigue sus mutaciones por el emisor del store, sin segunda copia del estado.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

const LINE: CartRow = {
  id: 'linea-1',
  product_id: 'prod-pan',
  qty: '2',
  unit_price_cents: 30,
  name: 'Pan francés',
  uom: 'unit',
  voided: false,
}

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-cart-hook-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

describe('useCartLines', () => {
  it('reads the persisted cart on mount, so a reload restores the cart', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd([LINE])

    const { result, unmount } = renderHook(() => useCartLines(database))

    await waitFor(() => expect(result.current).toEqual([LINE]))

    unmount()
    database.close()
  })

  it('follows a mutation without remounting', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd([LINE])

    const { result, unmount } = renderHook(() => useCartLines(database))
    await waitFor(() => expect(result.current).toEqual([LINE]))

    await act(async () => {
      await annulCartLine(LINE.id, database)
    })

    await waitFor(() => expect(result.current[0]?.voided).toBe(true))

    unmount()
    database.close()
  })
})
