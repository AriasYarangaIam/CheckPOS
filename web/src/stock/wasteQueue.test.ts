// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import { enqueueWaste, isWasteReason, qtyDeltaOf, WASTE_REASONS } from '@/stock/wasteQueue'

/*
 * Cola de la merma (W8-FE-5; docs/BACKEND.md §4.14). Lo que se fija: los tres motivos exactos, el
 * delta NEGATIVO con tres decimales en TEXTO, la clave de idempotencia, el efecto local sobre la
 * proyección del stock y el rechazo de una cantidad que no es una merma.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-waste-queue-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const PRODUCT: ProductRow = {
  id: 'prod-arroz',
  gtin: '7591234567890',
  name: 'Arroz Costeño 1kg',
  price_cents: 450,
  uom: 'unit',
  stock: 10,
  photo_url: null,
  category: 'abarroz',
}

describe('el delta de la merma', () => {
  it('firma la cantidad del operador en negativo y la lleva a tres decimales', () => {
    expect(qtyDeltaOf('2')).toBe('-2.000')
    expect(qtyDeltaOf('2.5')).toBe('-2.500')
    expect(qtyDeltaOf('0.125')).toBe('-0.125')
    expect(qtyDeltaOf(' 12.75 ')).toBe('-12.750')
  })

  it('rechaza lo que no es una merma', () => {
    for (const invalid of ['', '0', '0.000', '-1', 'dos', '1.2345', '1,5', '.5']) {
      expect(qtyDeltaOf(invalid), invalid).toBeNull()
    }
  })
})

describe('los motivos de la merma', () => {
  it('son exactamente los tres del contrato', () => {
    expect(WASTE_REASONS).toEqual(['Vencimiento', 'Rotura en transporte', 'Daño en vitrina'])
    expect(isWasteReason('Vencimiento')).toBe(true)
    expect(isWasteReason('Rotura en transporte')).toBe(true)
    expect(isWasteReason('Daño en vitrina')).toBe(true)
  })

  it('no acepta variantes de escritura', () => {
    for (const invalid of ['vencimiento', 'Dano en vitrina', 'Rotura de transporte', 'Robo']) {
      expect(isWasteReason(invalid), invalid).toBe(false)
    }
  })
})

describe('enqueueWaste', () => {
  it('encola el cuerpo del contrato con su clave y el delta negativo', async () => {
    const database = freshDatabase()

    try {
      await database.products.put(PRODUCT)

      const row = await enqueueWaste(
        { productId: PRODUCT.id, quantity: '2.5', reason: 'Vencimiento', key: 'waste-key-1', adjustmentId: 'adj-1' },
        database,
      )

      expect(row.operation).toBe('stock_adjustment')
      expect(row.key).toBe('waste-key-1')
      expect(row.status).toBe('pending')
      expect(row.total_cents).toBe(0)

      const body = JSON.parse(row.payload) as Record<string, unknown>
      expect(body).toEqual({
        id: 'adj-1',
        product_id: PRODUCT.id,
        qty_delta: '-2.500',
        source: 'manual',
        reason: 'Vencimiento',
      })
      // Sin control de foto: el campo no existe en el cuerpo (delta D5).
      expect(body.evidence_photo_url).toBeUndefined()
    } finally {
      database.close()
    }
  })

  it('mueve la proyección local del stock, sin arrastrar el error del punto flotante', async () => {
    const database = freshDatabase()

    try {
      await database.products.put({ ...PRODUCT, stock: 10 })

      await enqueueWaste({ productId: PRODUCT.id, quantity: '0.1', reason: 'Daño en vitrina' }, database)
      await enqueueWaste({ productId: PRODUCT.id, quantity: '0.2', reason: 'Daño en vitrina' }, database)

      const product = await database.products.get(PRODUCT.id)
      expect(product?.stock).toBe(9.7)
    } finally {
      database.close()
    }
  })

  it('rechaza una cantidad inválida sin escribir nada', async () => {
    const database = freshDatabase()

    try {
      await database.products.put(PRODUCT)

      await expect(
        enqueueWaste({ productId: PRODUCT.id, quantity: '0', reason: 'Vencimiento' }, database),
      ).rejects.toThrow()

      expect(await database.idempotency_queue.count()).toBe(0)
      expect((await database.products.get(PRODUCT.id))?.stock).toBe(10)
    } finally {
      database.close()
    }
  })

  it('rechaza un motivo fuera de la lista exacta sin escribir nada', async () => {
    const database = freshDatabase()

    try {
      await database.products.put(PRODUCT)

      await expect(
        enqueueWaste({ productId: PRODUCT.id, quantity: '1', reason: 'Robo' as never }, database),
      ).rejects.toThrow()

      expect(await database.idempotency_queue.count()).toBe(0)
    } finally {
      database.close()
    }
  })
})
