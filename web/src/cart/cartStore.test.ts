// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import {
  addIdentifiedLine,
  resetIdentifiedTapMemory,
  resolveIdentifiedProduct,
} from '@/cart/identifiedLine'
import {
  annulCartLine,
  cartTotalCents,
  changeCartLineQtyBy,
  emptyCart,
  payableCartLines,
  removeCartLine,
  saleLineTotalCents,
  setCartLineQty,
  setCartLineUnitPrice,
} from '@/cart/cartStore'
import { POS_DATABASE_NAME, PosDatabase, type CartRow, type ProductRow } from '@/db/posDatabase'

/*
 * Carrito persistente de W4-FE-1 (docs/FRONTEND.md:104-106; SPRINTS.md W4-FE-1):
 * agregar, cantidad, quitar, override de precio y anular línea, con
 * persistencia en IndexedDB EN CADA mutación y el debounce anti doble toque
 * que la grilla deliberadamente no trajo en W3-FE-1.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

const PAN: ProductRow = {
  id: 'prod-pan',
  gtin: '7591234567890',
  name: 'Pan francés',
  price_cents: 30,
  uom: 'unit',
  stock: 40,
  photo_url: null,
  category: 'panadería',
}

const ARROZ: ProductRow = {
  id: 'prod-arroz',
  gtin: null,
  name: 'Arroz Costeño 1kg',
  price_cents: 350,
  uom: 'kg',
  stock: 24,
  photo_url: null,
  category: 'abarrotes',
}

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-cart-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

async function databaseWithCatalog(): Promise<PosDatabase> {
  const database = freshDatabase()
  await database.products.bulkAdd([PAN, ARROZ])

  return database
}

beforeEach(() => {
  // El debounce es estado del módulo (un device = un escritorio). Cada prueba
  // empieza sin memoria de toques anteriores.
  resetIdentifiedTapMemory()
})

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

describe('addIdentifiedLine (carrito, no la grilla)', () => {
  it('adds one frozen line with the contract qty as exact text', async () => {
    const database = await databaseWithCatalog()

    const line = await addIdentifiedLine('7591234567890', database)

    expect(line).toEqual({
      id: expect.any(String),
      product_id: 'prod-pan',
      qty: '1',
      unit_price_cents: 30,
      name: 'Pan francés',
      uom: 'unit',
      voided: false,
    })
    expect(await database.cart.count()).toBe(1)

    database.close()
  })

  it('resolves a granel product by its local id when it has no GTIN', async () => {
    const database = await databaseWithCatalog()

    expect((await resolveIdentifiedProduct('prod-arroz', database))?.name).toBe(
      'Arroz Costeño 1kg',
    )
    await addIdentifiedLine('prod-arroz', database)

    expect(await database.cart.count()).toBe(1)

    database.close()
  })

  it('never invents a line for an unknown code', async () => {
    const database = await databaseWithCatalog()

    expect(await addIdentifiedLine('desconocido', database)).toBeNull()
    expect(await database.cart.count()).toBe(0)

    database.close()
  })

  it('coalesces a double tap into a single line with a single unit', async () => {
    /*
     * La mordida de W4-FE-1: dos toques rápidos —un gesto físico mal
     * disparado dos veces— producen UNA línea con cantidad UNO (SPRINTS.md
     * W4-FE-1: «un doble toque produce una sola línea»).
     */
    const database = await databaseWithCatalog()
    let now = 1000

    await addIdentifiedLine('7591234567890', database, { now: () => now })
    now += 60
    await addIdentifiedLine('7591234567890', database, { now: () => now })

    const rows = await database.cart.toArray()

    expect(rows).toHaveLength(1)
    expect(rows[0]?.qty).toBe('1')

    database.close()
  })

  it('still adds fast: a deliberate second tap outside the window bumps the same line to qty 2', async () => {
    const database = await databaseWithCatalog()
    let now = 1000

    await addIdentifiedLine('7591234567890', database, { now: () => now })
    now += 500
    await addIdentifiedLine('7591234567890', database, { now: () => now })

    const rows = await database.cart.toArray()

    expect(rows).toHaveLength(1)
    expect(rows[0]?.qty).toBe('2')

    database.close()
  })
})

describe('cart mutations persist at every step', () => {
  it('sets an exact quantity for granel lines', async () => {
    const database = await databaseWithCatalog()
    const line = await addIdentifiedLine('prod-arroz', database)

    await setCartLineQty((line as CartRow).id, '2.750', database)

    expect((await database.cart.toArray())[0]?.qty).toBe('2.750')

    database.close()
  })

  it('refuses an unusable quantity without touching the stored row', async () => {
    const database = await databaseWithCatalog()
    const line = (await addIdentifiedLine('prod-arroz', database)) as CartRow

    await expect(setCartLineQty(line.id, '0', database)).rejects.toThrow()
    await expect(setCartLineQty(line.id, '2.7500', database)).rejects.toThrow()

    expect((await database.cart.toArray())[0]).toEqual(line)

    database.close()
  })

  it('moves the quantity by exactly one unit and removes the line at zero', async () => {
    const database = await databaseWithCatalog()
    const line = (await addIdentifiedLine('7591234567890', database)) as CartRow

    await changeCartLineQtyBy(line.id, 1, database)

    expect((await database.cart.toArray())[0]?.qty).toBe('2')

    await changeCartLineQtyBy(line.id, -1, database)
    await changeCartLineQtyBy(line.id, -1, database)

    expect(await database.cart.count()).toBe(0)

    database.close()
  })

  it('overrides the frozen unit price', async () => {
    const database = await databaseWithCatalog()
    const line = (await addIdentifiedLine('prod-arroz', database)) as CartRow

    await setCartLineUnitPrice(line.id, 400, database)

    expect((await database.cart.toArray())[0]?.unit_price_cents).toBe(400)

    await expect(setCartLineUnitPrice(line.id, -1, database)).rejects.toThrow()

    database.close()
  })

  it('annuls a line visibly: the row stays, voided is set, and it leaves the totals', async () => {
    const database = await databaseWithCatalog()
    const line = (await addIdentifiedLine('7591234567890', database)) as CartRow

    await annulCartLine(line.id, database)

    const rows = await database.cart.toArray()

    expect(rows).toHaveLength(1)
    expect(rows[0]?.voided).toBe(true)
    expect(payableCartLines(rows)).toHaveLength(0)
    expect(cartTotalCents(rows)).toBe(0)

    database.close()
  })

  it('removes a line for good', async () => {
    const database = await databaseWithCatalog()
    const line = (await addIdentifiedLine('7591234567890', database)) as CartRow

    await removeCartLine(line.id, database)

    expect(await database.cart.count()).toBe(0)

    database.close()
  })

  it('empties the whole cart', async () => {
    const database = await databaseWithCatalog()
    await addIdentifiedLine('7591234567890', database)
    await addIdentifiedLine('prod-arroz', database)

    await emptyCart(database)

    expect(await database.cart.count()).toBe(0)

    database.close()
  })
})

describe('money at the border', () => {
  it('rounds each line half-up and excludes the annulled ones', async () => {
    // 2.750 kg × 989 ¢ = 2719.75 → 2720.
    const granel: CartRow = {
      id: 'l1',
      product_id: 'prod-arroz',
      qty: '2.750',
      unit_price_cents: 989,
      name: 'Arroz Costeño 1kg',
      uom: 'kg',
      voided: false,
    }
    const unitaria: CartRow = {
      id: 'l2',
      product_id: 'prod-pan',
      qty: '3',
      unit_price_cents: 30,
      name: 'Pan francés',
      uom: 'unit',
      voided: false,
    }
    const anulada: CartRow = { ...unitaria, id: 'l3', voided: true }

    expect(saleLineTotalCents(granel)).toBe(2720)
    expect(saleLineTotalCents(unitaria)).toBe(90)
    expect(cartTotalCents([granel, unitaria, anulada])).toBe(2810)
    expect(payableCartLines([granel, unitaria, anulada]).map((line) => line.id)).toEqual([
      'l1',
      'l2',
    ])
  })
})
