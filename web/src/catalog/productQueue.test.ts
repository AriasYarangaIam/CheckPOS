// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it } from 'vitest'

import { enqueueProductCreate } from '@/catalog/productQueue'
import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'

/*
 * Alta de producto encolable (W8-FE-5; docs/BACKEND.md §4.6).
 *
 * Lo que se fija: el cuerpo del contrato con la marca de mutación del cliente, la ausencia de
 * `stock` en el payload (converge solo por deltas), el puntero de adopción opcional y la fila local
 * que deja ver el producto sin conexión.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-product-queue-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

describe('enqueueProductCreate', () => {
  it('encola el cuerpo del contrato sin stock y crea la fila local', async () => {
    const database = freshDatabase()

    try {
      const row = await enqueueProductCreate(
        {
          draft: {
            id: 'prod-nuevo',
            name: '  Galleta Soda  ',
            gtin: ' 7593333333333 ',
            priceCents: 120,
            uom: 'unit',
          },
          key: 'product-key-1',
          now: () => new Date('2026-10-26T15:00:00.000Z'),
        },
        database,
      )

      expect(row.operation).toBe('product')
      expect(row.key).toBe('product-key-1')

      const body = JSON.parse(row.payload) as Record<string, unknown>
      expect(body).toEqual({
        id: 'prod-nuevo',
        name: 'Galleta Soda',
        gtin: '7593333333333',
        price_cents: 120,
        uom: 'unit',
        updated_at: '2026-10-26T15:00:00.000Z',
      })
      // El stock NUNCA viaja en el alta (§4 `products`).
      expect(body.stock).toBeUndefined()

      // La tabla del catálogo lo muestra en el acto, con el stock en cero: el alta no lo trae.
      expect(await database.products.get('prod-nuevo')).toEqual({
        id: 'prod-nuevo',
        gtin: '7593333333333',
        name: 'Galleta Soda',
        price_cents: 120,
        uom: 'unit',
        stock: 0,
        photo_url: null,
        category: null,
      })
    } finally {
      database.close()
    }
  })

  it('carries the adoption pointer when the global catalog matched', async () => {
    const database = freshDatabase()

    try {
      const row = await enqueueProductCreate(
        {
          draft: {
            id: 'prod-adoptado',
            name: 'Galleta Soda Global',
            gtin: '7593333333333',
            priceCents: 120,
            uom: 'unit',
            attributes: { brand: 'San Jorge' },
            globalProductId: 'global-1',
          },
        },
        database,
      )

      const body = JSON.parse(row.payload) as Record<string, unknown>
      expect(body.global_product_id).toBe('global-1')
      expect(body.attributes).toEqual({ brand: 'San Jorge' })
    } finally {
      database.close()
    }
  })

  it('rejects a draft without a name, a price or a unit, without writing anything', async () => {
    const database = freshDatabase()

    try {
      for (const draft of [
        { id: 'p1', name: '   ', gtin: null, priceCents: 100, uom: 'unit' },
        { id: 'p2', name: 'Producto', gtin: null, priceCents: -1, uom: 'unit' },
        { id: 'p3', name: 'Producto', gtin: null, priceCents: 100, uom: ' ' },
      ]) {
        await expect(enqueueProductCreate({ draft }, database)).rejects.toThrow()
      }

      expect(await database.idempotency_queue.count()).toBe(0)
      expect(await database.products.count()).toBe(0)
    } finally {
      database.close()
    }
  })
})
