// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { addIdentifiedLine } from '@/cart/identifiedLine'
import { ProductFavoritesGrid } from '@/components/ProductFavoritesGrid'
import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'

/*
 * Grilla de favoritos (docs/DESIGN.md:136; SPRINTS.md W3-FE-1).
 *
 * Lo que se prueba acá es el criterio de aceptación completo: tiles táctiles
 * de mínimo 48px con foto, nombre y precio, y el agregado con UN solo toque
 * vía `onProductIdentified(code)` —el código que la grilla emite es el que el
 * camino Input → Cart resuelve, de modo que el toque agrega la línea correcta
 * al carrito (ARCHITECTURE.md:37, :80).
 */

const createdNames = new Set<string>()
let nextId = 0

function freshDatabase(): PosDatabase {
  nextId += 1
  const name = `${POS_DATABASE_NAME}-grid-test-${nextId}`
  createdNames.add(name)

  return new PosDatabase(name)
}

/**
 * Favorito con GTIN y granel sin GTIN: los dos extremos que la grilla tiene
 * que saber identificar (§1.2:136 nombra pan, huevos y hielo, que no llevan
 * código de barras).
 */
const TILES: readonly ProductRow[] = [
  {
    id: 'prod-1',
    gtin: '7591234567890',
    name: 'Arroz Costeño 1kg',
    price_cents: 350,
    uom: 'unit',
    stock: 24,
    photo_url: 'https://storage.example.com/arroz.png',
    category: 'abarrotes',
  },
  {
    id: 'prod-2',
    gtin: null,
    name: 'Pan francés',
    price_cents: 30,
    uom: 'unit',
    stock: 40,
    photo_url: null,
    category: 'panadería',
  },
]

afterEach(async () => {
  Reflect.deleteProperty(window.navigator, 'vibrate')
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

function installVibrate(): ReturnType<typeof vi.fn> {
  const vibrate = vi.fn()

  Object.defineProperty(window.navigator, 'vibrate', { configurable: true, value: vibrate })

  return vibrate
}

describe('ProductFavoritesGrid', () => {
  it('renders each product as a tactile tile with photo, name and price', () => {
    const { container } = render(
      <ProductFavoritesGrid products={TILES} onProductIdentified={() => {}} />,
    )

    const tiles = screen.getAllByTestId('product-tile')

    expect(tiles).toHaveLength(2)

    // Mínimo táctil de 48px (docs/DESIGN.md:136; docs/PRODUCT.md:64): la
    // garantía vive en la clase del tile, que es lo que el CSS compila a
    // `min-height: 3rem`. `h-12` sería ella sola cuando el tile no llena la
    // celda, pero el contenido (foto, nombre, precio) la supera.
    for (const tile of tiles) {
      expect(tile.className).toContain('min-h-12')
    }

    // La foto es la URL del contrato: la única imagen de la fila. El `alt`
    // vacío es correcto —el nombre legible ya está en el tile y la foto es
    // decorativa para el lector de pantalla—.
    const image = container.querySelector('img') as HTMLImageElement

    expect(image.src).toBe('https://storage.example.com/arroz.png')
    expect(image.alt).toBe('')
    // El granel no tiene foto: el hueco no se rompe, se reserva.
    expect(container.querySelectorAll('img')).toHaveLength(1)

    expect(screen.getByText('Arroz Costeño 1kg')).toBeTruthy()
    expect(screen.getByText('Pan francés')).toBeTruthy()

    // El precio viaja por el rol numérico-tabular de Money, con el formato
    // del contrato de dinero (dos decimales siempre visibles).
    expect(screen.getByText('S/ 3.50')).toBeTruthy()
    expect(screen.getByText('S/ 0.30')).toBeTruthy()
  })

  it('emits the identified code of the tapped product via onProductIdentified', () => {
    const onProductIdentified = vi.fn()

    render(<ProductFavoritesGrid products={TILES} onProductIdentified={onProductIdentified} />)

    // Favorito con GTIN: el código que viaja es el del escáner, de modo que el
    // carrito lo resuelve igual que una lectura de cámara.
    fireEvent.click(screen.getByRole('button', { name: 'Arroz Costeño 1kg, S/ 3.50' }))

    expect(onProductIdentified).toHaveBeenCalledTimes(1)
    expect(onProductIdentified).toHaveBeenCalledWith('7591234567890')

    // Granel sin GTIN: el código es el id local del producto, la única
    // identidad que la fila tiene y con la que la línea puede resolverse.
    fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))

    expect(onProductIdentified).toHaveBeenCalledTimes(2)
    expect(onProductIdentified).toHaveBeenLastCalledWith('prod-2')
  })

  it('gives the one-touch add its haptic feedback ([60]) where there is a motor', () => {
    const vibrate = installVibrate()

    render(<ProductFavoritesGrid products={TILES} onProductIdentified={() => {}} />)

    fireEvent.click(screen.getByRole('button', { name: 'Arroz Costeño 1kg, S/ 3.50' }))

    expect(vibrate).toHaveBeenCalledTimes(1)
    expect(vibrate).toHaveBeenCalledWith([60])
  })

  it('taps without a vibration motor still emit the identified code', () => {
    // El mostrador puede ser un equipo sin motor de vibración —y jsdom no lo
    // implementa—: la ausencia no rompe el agregado (el patrón ya probado en
    // la pantalla de acceso).
    expect(typeof window.navigator.vibrate).toBe('undefined')

    const onProductIdentified = vi.fn()

    render(<ProductFavoritesGrid products={TILES} onProductIdentified={onProductIdentified} />)

    fireEvent.click(screen.getByRole('button', { name: 'Arroz Costeño 1kg, S/ 3.50' }))

    expect(onProductIdentified).toHaveBeenCalledWith('7591234567890')
  })

  it('adds the correct line to the cart with one touch (Input → Cart)', async () => {
    const database = await freshDatabase()
    await database.products.bulkAdd(TILES)

    /*
     * Acá está el criterio de W3-FE-1 entero: la grilla no conoce precios ni
     * carrito —solo emite el código (Input, ARCHITECTURE.md:37)— y es la
     * resolución del camino el que mete la línea correcta al carrito
     * (ARCHITECTURE.md:80): producto correcto, qty 1, precio congelado.
     */
    render(
      <ProductFavoritesGrid
        products={TILES}
        onProductIdentified={(code) => {
          void addIdentifiedLine(code, database)
        }}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Arroz Costeño 1kg, S/ 3.50' }))

    await waitFor(async () => expect(await database.cart.count()).toBe(1))

    const line = (await database.cart.toArray())[0]

    expect(line).toEqual({
      id: expect.any(String),
      product_id: 'prod-1',
      qty: '1',
      // El precio, el nombre y la unidad se congelan al agregar, tal como los
      // fija la fila del carrito de W4-FE-1.
      unit_price_cents: 350,
      name: 'Arroz Costeño 1kg',
      uom: 'unit',
      voided: false,
    })

    // El granel también enclosure su línea, con el código que sí resuelve:
    // id local de la fila del catálogo.
    fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))

    await waitFor(async () => expect(await database.cart.count()).toBe(2))

    const lines = await database.cart.toArray()

    expect(
      lines.find((candidate) => candidate.product_id === 'prod-2'),
    ).toEqual({
      id: expect.any(String),
      product_id: 'prod-2',
      qty: '1',
      unit_price_cents: 30,
      name: 'Pan francés',
      uom: 'unit',
      voided: false,
    })

    database.close()
  })

  it('leaves the cart untouched when the tapped code resolves to nothing', async () => {
    const database = await freshDatabase()

    render(<ProductFavoritesGrid products={[]} onProductIdentified={(code) => { void addIdentifiedLine(code, database) }} />)

    // Sin catálogo sincronizado no hay tiles que tocar: la grilla se lo dice,
    // y el carrito queda exactamente como estaba.
    expect(screen.getByRole('status').textContent).toContain('todavía no tiene')

    expect(await database.cart.count()).toBe(0)

    // Y un código que no resuelve contra el subconjunto tampoco agrega nada.
    expect(await addIdentifiedLine('desconocido', database)).toBeNull()
    expect(await database.cart.count()).toBe(0)

    database.close()
  })
})
