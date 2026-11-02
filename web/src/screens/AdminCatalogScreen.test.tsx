// Debe ser la primera importación: la pantalla lee y escribe la base local.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { lookupGlobalProduct, priceToCents } from '@/catalog/productQueue'
import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import { AdminCatalogScreen } from '@/screens/AdminCatalogScreen'

/*
 * Catálogo Maestro del rol Administrador (W8-FE-5; docs/FRONTEND.md:53; docs/BACKEND.md §4.6-4.7).
 *
 * Lo que se fija: la tabla de SKUs con sus columnas, el alta encolable con su clave y SIN stock en
 * el cuerpo, la adopción desde el catálogo global como puntero, y la edición por PATCH como camino
 * SOLO en línea —que no se encola— con una razón visible cuando no hay red.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-catalog-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
  vi.unstubAllGlobals()
})

const PRODUCT: ProductRow = {
  id: 'prod-cafe',
  gtin: '7592222222222',
  name: 'Café Altomayo 100g',
  price_cents: 1250,
  uom: 'unit',
  stock: 12,
  photo_url: null,
  category: null,
}

describe('el precio en soles', () => {
  it('se convierte a centavos enteros sin pasar por float', () => {
    expect(priceToCents('4.50')).toBe(450)
    expect(priceToCents('4.5')).toBe(450)
    expect(priceToCents('10')).toBe(1000)
    expect(priceToCents(' 12.05 ')).toBe(1205)
  })

  it('rechaza lo que no es un precio', () => {
    for (const invalid of ['', '-1', '4.555', 'dos', '1,5']) {
      expect(priceToCents(invalid), invalid).toBeNull()
    }
  })
})

describe('AdminCatalogScreen', () => {
  it('renders the SKU table with its columns', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      render(<AdminCatalogScreen database={database} />)

      const row = await screen.findByTestId(`catalog-row-${PRODUCT.id}`)
      expect(row.textContent).toContain(PRODUCT.name)
      expect(row.textContent).toContain(PRODUCT.gtin ?? '')
      expect(row.textContent).toContain('S/ 12.50')
      expect(row.textContent).toContain('unit')
      expect(row.textContent).toContain('12')
    } finally {
      database.close()
    }
  })

  it('enqueues the creation with its key and without stock in the body', async () => {
    const database = freshDatabase()

    try {
      render(<AdminCatalogScreen database={database} />)

      fireEvent.click(await screen.findByTestId('catalog-new'))
      fireEvent.change(screen.getByTestId('catalog-name'), { target: { value: 'Galleta Soda' } })
      fireEvent.change(screen.getByTestId('catalog-price'), { target: { value: '1.20' } })
      fireEvent.change(screen.getByTestId('catalog-uom'), { target: { value: 'unit' } })
      fireEvent.click(screen.getByTestId('catalog-create-submit'))

      await waitFor(async () => expect(await database.idempotency_queue.count()).toBe(1))

      const row = (await database.idempotency_queue.toArray())[0]
      expect(row.operation).toBe('product')
      expect(row.key).not.toBe('')

      const body = JSON.parse(row.payload) as Record<string, unknown>
      expect(body.name).toBe('Galleta Soda')
      expect(body.price_cents).toBe(120)
      expect(body.uom).toBe('unit')
      expect(body.updated_at).toEqual(expect.any(String))
      // El stock NUNCA viaja en el alta: converge solo por deltas (§4 `products`).
      expect(body.stock).toBeUndefined()

      // Y el producto aparece en la tabla sin conexión.
      expect(await screen.findByText('Galleta Soda')).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('adopts a global product through the lookup pointer', async () => {
    const database = freshDatabase()
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) =>
      new Response(
        JSON.stringify({ product: { id: 'global-1', name: 'Galleta Soda Global', attributes: { brand: 'San Jorge' } } }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
    )

    try {
      render(
        <AdminCatalogScreen database={database} fetchImpl={fetchSpy as unknown as typeof fetch} token="jwt" />,
      )

      fireEvent.click(await screen.findByTestId('catalog-new'))
      fireEvent.change(screen.getByTestId('catalog-gtin'), { target: { value: '7593333333333' } })
      fireEvent.click(screen.getByTestId('catalog-adopt'))

      // La mediación trae el nombre y la marca: el operador solo pone el precio y confirma.
      await waitFor(() =>
        expect((screen.getByTestId('catalog-name') as HTMLInputElement).value).toBe('Galleta Soda Global'),
      )
      expect(screen.getByTestId('catalog-adopted').textContent).toContain('catálogo global')

      fireEvent.change(screen.getByTestId('catalog-price'), { target: { value: '1.20' } })
      fireEvent.change(screen.getByTestId('catalog-uom'), { target: { value: 'unit' } })
      fireEvent.click(screen.getByTestId('catalog-create-submit'))

      await waitFor(async () => expect(await database.idempotency_queue.count()).toBe(1))
      const body = JSON.parse((await database.idempotency_queue.toArray())[0].payload) as Record<string, unknown>
      expect(body.global_product_id).toBe('global-1')
      expect(body.gtin).toBe('7593333333333')

      const [url] = fetchSpy.mock.calls[0] as [string]
      expect(url).toBe('/gtin-lookup?gtin=7593333333333')
    } finally {
      database.close()
    }
  })

  it('says the GTIN is not in the global catalog and allows the manual creation', async () => {
    const database = freshDatabase()
    const fetchSpy = vi.fn(async () => new Response('{}', { status: 404 }))

    try {
      render(
        <AdminCatalogScreen database={database} fetchImpl={fetchSpy as unknown as typeof fetch} token="jwt" />,
      )

      fireEvent.click(await screen.findByTestId('catalog-new'))
      fireEvent.change(screen.getByTestId('catalog-gtin'), { target: { value: '0000' } })
      fireEvent.click(screen.getByTestId('catalog-adopt'))

      expect((await screen.findByTestId('catalog-adopt-miss')).textContent).toContain('no está')
      // El alta manual sigue disponible.
      expect(screen.getByTestId('catalog-create-submit')).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('keeps the edition online-only and says why when there is no network', async () => {
    const database = freshDatabase()
    await database.products.put(PRODUCT)

    try {
      const offline = render(<AdminCatalogScreen database={database} />)

      fireEvent.click(await screen.findByTestId(`catalog-edit-${PRODUCT.id}`))
      // PATCH no es encolable (§7.6): sin red no hay edición, y la pantalla lo dice.
      expect((screen.getByTestId('catalog-edit-save') as HTMLButtonElement).disabled).toBe(true)
      expect(screen.getByTestId('catalog-edit-offline').textContent).toContain('solo en línea')
      expect(await database.idempotency_queue.count()).toBe(0)
      offline.unmount()

      // Con red, el guardado manda el PATCH con el token y el cuerpo aditivo.
      const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) =>
        new Response(JSON.stringify({ data: [{ id: PRODUCT.id }] }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      )
      render(
        <AdminCatalogScreen
          database={database}
          fetchImpl={fetchSpy as unknown as typeof fetch}
          token="jwt"
        />,
      )

      fireEvent.click(await screen.findByTestId(`catalog-edit-${PRODUCT.id}`))
      fireEvent.change(screen.getByTestId('catalog-edit-name'), { target: { value: 'Café Altomayo 90g' } })
      fireEvent.click(screen.getByTestId('catalog-edit-save'))

      await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toBe(`/products/${PRODUCT.id}`)
      expect(init.method).toBe('PATCH')
      expect((init.headers as Record<string, string>).authorization).toBe('Bearer jwt')
      const body = JSON.parse(String(init.body)) as Record<string, unknown>
      expect(body.name).toBe('Café Altomayo 90g')
      expect(body.stock).toBeUndefined()
      // El PATCH no se encola: no hay una fila nueva esperando acuse.
      expect(await database.idempotency_queue.count()).toBe(0)
    } finally {
      database.close()
    }
  })
})

describe('lookupGlobalProduct', () => {
  it('returns null without a session or a gtin, without calling the server', async () => {
    const fetchSpy = vi.fn()

    expect(await lookupGlobalProduct('759', { fetchImpl: fetchSpy as unknown as typeof fetch })).toBeNull()
    expect(await lookupGlobalProduct('', { token: 'jwt', fetchImpl: fetchSpy as unknown as typeof fetch })).toBeNull()
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})
