// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { syncCatalogFromApi } from '@/catalog/catalogCache'
import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import { CounterSaleScreen } from '@/screens/CounterSaleScreen'

/*
 * Pantalla de venta de mostrador (W4-FE-1): la grilla entrega el código y el
 * panel del carrito vive en la misma pantalla. El resto del flujo (checkout,
 * ticket) son unidades posteriores de este slice.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

const PRODUCTS: readonly ProductRow[] = [
  {
    id: 'prod-pan',
    gtin: '7591234567890',
    name: 'Pan francés',
    price_cents: 30,
    uom: 'unit',
    stock: 40,
    photo_url: null,
    category: 'panadería',
  },
]

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-counter-sale-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

describe('CounterSaleScreen', () => {
  it('hosts the products grid and the cart on the same sale surface', async () => {
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    render(<CounterSaleScreen database={database} />)

    expect(screen.getByText('Punto de venta')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Productos' })).toBeTruthy()
    await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))
    expect(screen.getByTestId('cart-panel-empty')).toBeTruthy()

    database.close()
  })

  it('adds the line to the cart when a tile is tapped, persisted before the panel refreshes', async () => {
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    render(<CounterSaleScreen database={database} />)
    await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

    fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))

    await waitFor(async () => expect(await database.cart.count()).toBe(1))
    const line = (await database.cart.toArray())[0]

    expect(line).toEqual({
      id: expect.any(String),
      product_id: 'prod-pan',
      qty: '1',
      unit_price_cents: 30,
      name: 'Pan francés',
      uom: 'unit',
      voided: false,
    })
    await waitFor(() => expect(screen.getByTestId('cart-total').textContent).toContain('S/ 0.30'))

    database.close()
  })

  it('renders the cached subset without touching the network', async () => {
    const database = freshDatabase()
    await syncCatalogFromApi({
      token: 'token-de-la-sesion',
      database,
      fetchImpl: async () =>
        new Response(
          JSON.stringify({
            data: [
              {
                id: 'a1-uuid',
                name: 'Pan francés',
                gtin: '7591234567890',
                price_cents: 30,
                uom: 'unit',
                stock: '40',
                photo_url: null,
                attributes: { category: 'panadería' },
              },
            ],
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        ) as unknown as Response,
    })

    // El sync de W3-FE-3 ya pobló la base: el render no pide nada.
    const fetchSpy = vi.spyOn(globalThis, 'fetch')

    try {
      render(<CounterSaleScreen database={database} />)
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      expect(fetchSpy).not.toHaveBeenCalled()
    } finally {
      vi.restoreAllMocks()

      database.close()
    }
  })
})
