// Debe ser la primera importación: la pantalla lee la base local.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import { AdminInventoryScreen } from '@/screens/AdminInventoryScreen'

/*
 * Inventario / Stock Rápido del rol Administrador (W8-FE-5; docs/FRONTEND.md:52;
 * docs/DESIGN.md:213).
 *
 * Lo que se fija: el semáforo de tres niveles sobre la proyección local del stock, la búsqueda por
 * nombre y por GTIN, y la verdad de los estados vacíos. Los umbrales numéricos no están en ningún
 * documento —`docs/FRONTEND.md:52` los llama «spec-fixed» pero no los publica—, así que la prueba
 * fija la decisión declarada: quiebre en cero o menos, bajo hasta cinco unidades.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-inventory-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const PRODUCTS: readonly ProductRow[] = [
  {
    id: 'prod-arroz',
    gtin: '7591234567890',
    name: 'Arroz Costeño 1kg',
    price_cents: 450,
    uom: 'unit',
    stock: 24,
    photo_url: null,
    category: null,
  },
  {
    id: 'prod-leche',
    gtin: '7590000000002',
    name: 'Leche Gloria 400g',
    price_cents: 380,
    uom: 'unit',
    stock: 3,
    photo_url: null,
    category: null,
  },
  {
    id: 'prod-aceite',
    gtin: null,
    name: 'Aceite Primor 1L',
    price_cents: 950,
    uom: 'unit',
    stock: 0,
    photo_url: null,
    category: null,
  },
]

describe('AdminInventoryScreen', () => {
  it('shows the three levels of the stock semaphore', async () => {
    const database = freshDatabase()
    await database.products.bulkPut(PRODUCTS)

    try {
      render(<AdminInventoryScreen database={database} />)

      await screen.findByTestId('inventory-row-prod-arroz')

      expect(screen.getByTestId('inventory-row-prod-arroz').getAttribute('data-level')).toBe('normal')
      expect(screen.getByTestId('inventory-row-prod-leche').getAttribute('data-level')).toBe('low')
      expect(screen.getByTestId('inventory-row-prod-aceite').getAttribute('data-level')).toBe('out')

      expect(screen.getByTestId('inventory-row-prod-arroz').textContent).toContain('Normal')
      expect(screen.getByTestId('inventory-row-prod-leche').textContent).toContain('Bajo')
      expect(screen.getByTestId('inventory-row-prod-aceite').textContent).toContain('Quiebre')
    } finally {
      database.close()
    }
  })

  it('treats a negative projection as a broken stock, not as normal', async () => {
    const database = freshDatabase()
    await database.products.put({ ...PRODUCTS[0], stock: -2.5 })

    try {
      render(<AdminInventoryScreen database={database} />)

      await screen.findByTestId('inventory-row-prod-arroz')
      expect(screen.getByTestId('inventory-row-prod-arroz').getAttribute('data-level')).toBe('out')
    } finally {
      database.close()
    }
  })

  it('finds a product by name and by GTIN', async () => {
    const database = freshDatabase()
    await database.products.bulkPut(PRODUCTS)

    try {
      render(<AdminInventoryScreen database={database} />)
      await screen.findByTestId('inventory-row-prod-arroz')

      fireEvent.change(screen.getByTestId('inventory-search'), { target: { value: '7590000000002' } })
      expect(screen.getByTestId('inventory-row-prod-leche')).toBeTruthy()
      expect(screen.queryByTestId('inventory-row-prod-arroz')).toBeNull()

      fireEvent.change(screen.getByTestId('inventory-search'), { target: { value: 'aceite' } })
      expect(screen.getByTestId('inventory-row-prod-aceite')).toBeTruthy()

      fireEvent.change(screen.getByTestId('inventory-search'), { target: { value: 'zzz' } })
      expect(screen.getByTestId('inventory-no-hits')).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('says the device has no catalog yet instead of showing an empty table', async () => {
    const database = freshDatabase()

    try {
      render(<AdminInventoryScreen database={database} />)

      const empty = await screen.findByTestId('inventory-empty')
      expect(empty.textContent).toContain('catálogo')
    } finally {
      database.close()
    }
  })

  it('offers the waste action on a product row', async () => {
    const database = freshDatabase()
    await database.products.bulkPut(PRODUCTS)
    const requested: string[] = []

    try {
      render(
        <AdminInventoryScreen
          database={database}
          onWasteRequested={(productId) => requested.push(productId)}
        />,
      )

      await screen.findByTestId('inventory-row-prod-leche')
      fireEvent.click(
        within(screen.getByTestId('inventory-row-prod-leche')).getByRole('button', { name: /merma/i }),
      )

      expect(requested).toEqual(['prod-leche'])
    } finally {
      database.close()
    }
  })

  it('shows the pending count as how much stock is not confirmed', async () => {
    const database = freshDatabase()
    await database.products.bulkPut(PRODUCTS)
    await database.idempotency_queue.add({
      id: 'row-1',
      key: 'key-1',
      payload: '{}',
      status: 'pending',
      created_at: new Date().toISOString(),
      operation: 'stock_adjustment',
      business_date: '2026-10-26',
      sale_id: null,
      payment_method: null,
      total_cents: 0,
      last_error: null,
    })

    try {
      render(<AdminInventoryScreen database={database} />)

      await waitFor(() =>
        expect(screen.getByTestId('inventory-pending').textContent).toContain('1 movimiento'),
      )
    } finally {
      database.close()
    }
  })
})
