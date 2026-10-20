// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type CartRow } from '@/db/posDatabase'
import { CartPanel } from '@/components/CartPanel'

/*
 * Panel del carrito de W4-FE-1 (docs/FRONTEND.md:104-106): líneas con
 * cantidad, override de precio, anulación y quitar, y el total half-up por
 * línea. Las mutaciones viajan por el store; el panel nunca guarda estado.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

const PAN: CartRow = {
  id: 'linea-pan',
  product_id: 'prod-pan',
  qty: '2',
  unit_price_cents: 30,
  name: 'Pan francés',
  uom: 'unit',
  voided: false,
}

const ARROZ: CartRow = {
  id: 'linea-arroz',
  product_id: 'prod-arroz',
  qty: '2.750',
  unit_price_cents: 989,
  name: 'Arroz Costeño 1kg',
  uom: 'kg',
  voided: false,
}

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-cart-panel-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

describe('CartPanel', () => {
  it('renders the persisted lines with their totals after a reload', async () => {
    // Recargar la aplicación restaura el carrito: el panel nace de la tabla.
    const database = freshDatabase()
    await database.cart.bulkAdd([PAN, ARROZ])

    render(<CartPanel database={database} />)

    expect(await screen.findByText('Pan francés')).toBeTruthy()
    expect(screen.getByText('Arroz Costeño 1kg')).toBeTruthy()
    // 2 × 30 + 2.750 × 989 (2720 half-up) = 2780.
    expect(screen.getByTestId('cart-total').textContent).toContain('S/ 27.80')

    database.close()
  })

  it('shows the honest empty state of a fresh device', async () => {
    const database = freshDatabase()

    render(<CartPanel database={database} />)

    expect(screen.getByTestId('cart-panel-empty').textContent).toBe('El carrito está vacío.')

    database.close()
  })

  it('adds and takes one unit through the quantity controls', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd([PAN])

    render(<CartPanel database={database} />)
    await screen.findByText('Pan francés')

    fireEvent.click(screen.getByRole('button', { name: 'Agregar una unidad de Pan francés' }))

    await waitFor(async () => expect((await database.cart.toArray())[0]?.qty).toBe('3'))

    fireEvent.click(screen.getByRole('button', { name: 'Quitar una unidad de Pan francés' }))
    fireEvent.click(screen.getByRole('button', { name: 'Quitar una unidad de Pan francés' }))

    // Quedar debajo de la cantidad mínima quita la línea del todo.
    await waitFor(async () => expect(await database.cart.count()).toBe(0))

    database.close()
  })

  it('overrides the unit price in soles and applies it in integer cents', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd([PAN])

    render(<CartPanel database={database} />)
    await screen.findByText('Pan francés')

    const entry = screen.getByLabelText('Precio de Pan francés') as HTMLInputElement

    fireEvent.change(entry, { target: { value: '4' } })
    fireEvent.click(screen.getByTestId('cart-line-price-apply'))

    await waitFor(
      async () => expect((await database.cart.toArray())[0]?.unit_price_cents).toBe(400),
    )

    database.close()
  })

  it('annuls a line visibly without paying for it any more', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd([PAN])

    render(<CartPanel database={database} />)
    await screen.findByText('Pan francés')

    // 2 × 30 = 60 antes de anular.
    expect(screen.getByTestId('cart-total').textContent).toContain('S/ 0.60')

    fireEvent.click(screen.getByRole('button', { name: 'Anular Pan francés' }))

    await waitFor(() => expect(screen.getByTestId('cart-line-voided')).toBeTruthy())

    // La línea queda (evidencia) y deja de importar.
    expect(await database.cart.count()).toBe(1)
    expect((await database.cart.toArray())[0]?.voided).toBe(true)
    expect(screen.getByTestId('cart-total').textContent).toContain('S/ 0.00')

    database.close()
  })

  it('removes a line for good', async () => {
    const database = freshDatabase()
    await database.cart.bulkAdd([PAN])

    render(<CartPanel database={database} />)
    await screen.findByText('Pan francés')

    fireEvent.click(screen.getByRole('button', { name: 'Quitar Pan francés del carrito' }))

    await waitFor(() => expect(screen.queryByTestId('cart-line')).toBeNull())

    database.close()
  })
})
