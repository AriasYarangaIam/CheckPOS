// Debe ser la primera importación: la pantalla lee la base local.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import { AdminWasteScreen } from '@/screens/AdminWasteScreen'

/*
 * Mermas y Daños del rol Administrador (W8-FE-5; docs/FRONTEND.md:52; docs/DESIGN.md:214-218).
 *
 * Lo que se fija: los TRES motivos exactos, la cantidad positiva con hasta tres decimales, la
 * confirmación destructiva ANTES de escribir, el encolado con `source: manual` y la ausencia de
 * cualquier control de foto. La merma funciona sin red: no llama a `fetch`.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-waste-screen-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
  vi.unstubAllGlobals()
})

const PRODUCT: ProductRow = {
  id: 'prod-yogur',
  gtin: '7591111111111',
  name: 'Yogur Gloria 1L',
  price_cents: 650,
  uom: 'unit',
  stock: 8,
  photo_url: null,
  category: null,
}

/** Elige el producto, escribe la cantidad y marca el motivo. */
function fillWaste(quantity = '2', reason = 'vencimiento'): void {
  fireEvent.click(screen.getByTestId(`merma-select-${PRODUCT.id}`))
  fireEvent.change(screen.getByTestId('merma-quantity'), { target: { value: quantity } })
  fireEvent.click(screen.getByTestId(`merma-reason-${reason}`))
}

describe('AdminWasteScreen', () => {
  it('offers exactly the three reasons of the contract', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} />)

      fireEvent.click(await screen.findByTestId(`merma-select-${PRODUCT.id}`))

      expect(screen.getByTestId('merma-reason-vencimiento').textContent).toBe('Vencimiento')
      expect(screen.getByTestId('merma-reason-transporte').textContent).toBe('Rotura en transporte')
      expect(screen.getByTestId('merma-reason-vitrina').textContent).toBe('Daño en vitrina')
      expect(screen.queryAllByTestId(/^merma-reason-/)).toHaveLength(3)
    } finally {
      database.close()
    }
  })

  it('has no photo control anywhere (delta D5)', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} />)
      fireEvent.click(await screen.findByTestId(`merma-select-${PRODUCT.id}`))

      expect(screen.queryByLabelText(/foto/i)).toBeNull()
      expect(screen.queryByTestId(/photo/i)).toBeNull()
      expect(screen.queryByRole('button', { name: /foto|cámara/i })).toBeNull()
    } finally {
      database.close()
    }
  })

  it('validates the form when the operator asks, naming what is missing', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} />)
      fireEvent.click(await screen.findByTestId(`merma-select-${PRODUCT.id}`))

      // Sin cantidad, el aviso nombra la cantidad y NO abre la confirmación.
      fireEvent.click(screen.getByTestId('merma-submit'))
      expect(screen.getByTestId('merma-error').textContent).toContain('cantidad')
      expect(screen.queryByTestId('merma-confirm')).toBeNull()

      // Con cantidad y sin motivo, el aviso nombra el motivo.
      fireEvent.change(screen.getByTestId('merma-quantity'), { target: { value: '2' } })
      fireEvent.click(screen.getByTestId('merma-submit'))
      expect(screen.getByTestId('merma-error').textContent).toContain('motivo')
      expect(screen.queryByTestId('merma-confirm')).toBeNull()

      // Con los dos, la confirmación aparece.
      fireEvent.click(screen.getByTestId('merma-reason-vitrina'))
      fireEvent.click(screen.getByTestId('merma-submit'))
      expect(screen.getByTestId('merma-confirm')).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('confirms before writing and enqueues the contract body on confirmation', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} />)
      await screen.findByTestId(`merma-select-${PRODUCT.id}`)
      fillWaste('2.5', 'transporte')

      fireEvent.click(screen.getByTestId('merma-submit'))

      // La confirmación es destructiva: todavía no se escribió nada.
      expect(screen.getByTestId('merma-confirm').textContent).toContain('Rotura en transporte')
      expect(await database.idempotency_queue.count()).toBe(0)
      expect((await database.products.get(PRODUCT.id))?.stock).toBe(8)

      fireEvent.click(screen.getByTestId('merma-confirm-submit'))

      await waitFor(async () => expect(await database.idempotency_queue.count()).toBe(1))

      const row = (await database.idempotency_queue.toArray())[0]
      expect(row.operation).toBe('stock_adjustment')
      const body = JSON.parse(row.payload) as Record<string, unknown>
      expect(body.qty_delta).toBe('-2.500')
      expect(body.source).toBe('manual')
      expect(body.reason).toBe('Rotura en transporte')

      // El stock del dispositivo baja en el acto: el semáforo no puede seguir diciendo que hay
      // mercadería que ya se dio de baja.
      expect(await screen.findByTestId('merma-notice')).toBeTruthy()
      expect((await database.products.get(PRODUCT.id))?.stock).toBe(5.5)
    } finally {
      database.close()
    }
  })

  it('cancels the confirmation without writing', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} />)
      await screen.findByTestId(`merma-select-${PRODUCT.id}`)
      fillWaste()

      fireEvent.click(screen.getByTestId('merma-submit'))
      fireEvent.click(screen.getByTestId('merma-cancel'))

      expect(screen.queryByTestId('merma-confirm')).toBeNull()
      expect(await database.idempotency_queue.count()).toBe(0)
    } finally {
      database.close()
    }
  })

  it('rejects a quantity that is not a loss and says why', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} />)
      await screen.findByTestId(`merma-select-${PRODUCT.id}`)
      fillWaste('0.0000', 'vencimiento')

      fireEvent.click(screen.getByTestId('merma-submit'))

      expect((await screen.findByTestId('merma-error')).textContent).toContain('cantidad')
      expect(await database.idempotency_queue.count()).toBe(0)
    } finally {
      database.close()
    }
  })

  it('works without network: it never calls the server', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    try {
      render(<AdminWasteScreen database={database} />)
      await screen.findByTestId(`merma-select-${PRODUCT.id}`)
      fillWaste('1', 'vencimiento')

      fireEvent.click(screen.getByTestId('merma-submit'))
      fireEvent.click(screen.getByTestId('merma-confirm-submit'))

      await waitFor(async () => expect(await database.idempotency_queue.count()).toBe(1))
      expect(fetchSpy).not.toHaveBeenCalled()
    } finally {
      database.close()
    }
  })

  it('opens with the product the inventory asked for', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminWasteScreen database={database} initialProductId={PRODUCT.id} />)

      // El producto llega elegido desde la fila del inventario; el operador solo pone cantidad y
      // motivo.
      expect(await screen.findByTestId('merma-selected')).toBeTruthy()
      expect(screen.getByTestId('merma-selected').textContent).toContain(PRODUCT.name)
    } finally {
      database.close()
    }
  })

  it('says the device has no catalog instead of an empty picker', async () => {
    const database = freshDatabase()

    try {
      render(<AdminWasteScreen database={database} />)

      expect((await screen.findByTestId('merma-empty')).textContent).toContain('catálogo')
    } finally {
      database.close()
    }
  })
})
