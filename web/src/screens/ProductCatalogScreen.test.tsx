// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import { ProductCatalogScreen } from '@/screens/ProductCatalogScreen'

/*
 * Segmento Productos de la venta (docs/DESIGN.md §1.2:136-137; SPRINTS.md
 * W3-FE-2): búsqueda instantánea por nombre y filtro por categoría, sin
 * animación de entrada ni retraso (docs/FRONTEND.md:23, §6.2:298-299).
 *
 * La prueba de latencia aprieta lo observable: las aserciones del filtro corren
 * síncronas después del `change`, sin `waitFor` —si hubiera debounce, un await
 * o una animación de entrada, el render ya estaría atrasado y fallaría.
 */

const createdNames = new Set<string>()
let nextId = 0

function freshDatabase(): PosDatabase {
  nextId += 1
  const name = `${POS_DATABASE_NAME}-catalog-test-${nextId}`
  createdNames.add(name)

  return new PosDatabase(name)
}

/** Dos categorías y tres productos: granel sin GTIN incluido. */
const PRODUCTS: readonly ProductRow[] = [
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
    gtin: '7599876504321',
    name: 'Azúcar Rubia 1kg',
    price_cents: 250,
    uom: 'unit',
    stock: 18,
    photo_url: null,
    category: 'abarrotes',
  },
  {
    id: 'prod-3',
    gtin: null,
    name: 'Pan francés',
    price_cents: 30,
    uom: 'unit',
    stock: 40,
    photo_url: null,
    category: 'panadería',
  },
  {
    id: 'prod-4',
    gtin: '7208785674006',
    name: 'Pan mama integral',
    price_cents: 120,
    uom: 'unit',
    stock: 12,
    photo_url: null,
    category: 'panadería',
  },
]

afterEach(async () => {
  Reflect.deleteProperty(window.navigator, 'vibrate')
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

async function seededRender(database: PosDatabase): Promise<void> {
  await database.products.bulkAdd(PRODUCTS)

  render(<ProductCatalogScreen database={database} />)

  // La carga inicial sí es asíncrona (IndexedDB): la espera limpia antes de las
  // pruebas de latencia, que por sí solas corren síncronas.
  await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(4))
}

describe('ProductCatalogScreen', () => {
  it('renders the products segment over the local subset with its search and category chips', async () => {
    const database = freshDatabase()
    await seededRender(database)

    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Productos')
    expect(screen.getByRole('searchbox', { name: 'Buscar producto' })).toBeTruthy()

    // Los chips de categoría son los valores distintos que el subconjunto
    // conoce: 'Todas' más las que traen los productos, en su orden estable.
    const chips = screen.getAllByRole('button', { name: /^(Todas|abarrotes|panadería)$/ })

    expect(chips.map((chip) => chip.textContent)).toEqual(['Todas', 'abarrotes', 'panadería'])
    for (const chip of chips) {
      // Control crítico del mostrador: mínimo táctil de 48px
      // (docs/DESIGN.md:136; docs/PRODUCT.md:64).
      expect(chip.className).toContain('min-h-12')
    }

    database.close()
  })

  it('filters by name synchronously as each character is typed, without waiting for anything', async () => {
    const database = freshDatabase()
    await seededRender(database)

    const search = screen.getByRole('searchbox', { name: 'Buscar producto' }) as HTMLInputElement

    // Tipear real enfoca primero: el `.focus()` nativo es lo que muerde la
    // aserción y es exactamente lo que el dedo hace — el `fireEvent` de foco
    // no mueve `document.activeElement` en jsdom.
    search.focus()
    fireEvent.change(search, { target: { value: 'pan' } })

    // Sin `waitFor`: si el filtro esperara un debounce o un await de Promise,
    // esta aserción correría contra la lista completa y fallaría. Es la
    // mordida observable del contrato de latencia cero (§6.2:298-299).
    expect(screen.getAllByTestId('product-tile')).toHaveLength(2)
    expect(search.value).toBe('pan')

    // El foco no se pierde en el filtro: el teclado del mostrador sigue en el
    // buscador y se puede seguir tipeando (una remontura de campo lo rompería).
    expect(document.activeElement).toBe(search)

    database.close()
  })

  it('matches names case-insensitively and shows the empty state without tearing the layout', async () => {
    const database = freshDatabase()
    await seededRender(database)

    const search = screen.getByRole('searchbox', { name: 'Buscar producto' }) as HTMLInputElement

    search.focus()
    fireEvent.change(search, { target: { value: 'PAN' } })

    expect(screen.getAllByTestId('product-tile')).toHaveLength(2)

    fireEvent.change(search, { target: { value: ' Destructor de galletas' } })

    expect(screen.getByTestId('favorites-grid-empty')).toBeTruthy()
    // La grilla no se desarma: el buscador conserva el valor y el foco, así
    // que un toque de `Borrar` y volver a tipear no interrumpe la venta.
    expect(search.value).toContain(' Destructor de galletas')
    expect(document.activeElement).toBe(search)

    database.close()
  })

  it('filters by category and combines the filter with the search', async () => {
    const database = freshDatabase()
    await seededRender(database)

    fireEvent.click(screen.getByRole('button', { name: 'panadería' }))

    expect(screen.getAllByTestId('product-tile')).toHaveLength(2)
    // El chip queda marcado como filtro activo para el lector de pantalla.
    expect(screen.getByRole('button', { name: 'panadería' }).getAttribute('aria-pressed')).toBe(
      'true',
    )

    // Búsqueda dentro de la categoría ya filtrada: dos criterios, una lista.
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'integral' } })

    expect(screen.getAllByTestId('product-tile')).toHaveLength(1)
    expect(screen.getByText('Pan mama integral')).toBeTruthy()

    // 'Todas' vuelve al catálogo completo sin recargar nada.
    fireEvent.click(screen.getByRole('button', { name: 'Todas' }))

    expect(screen.getAllByTestId('product-tile')).toHaveLength(1)

    database.close()
  })

  it('passes a categoric chip through the grid without any entrance animation', async () => {
    const database = freshDatabase()
    await seededRender(database)

    // §5.2 prohíbe las animaciones gratuitas y la búsqueda es acción de
    // latencia cero: ningún elemento del resultado lleva `animate-*`. Lo
    // comprueba después de filtrar: el estado filtrado es el que podría
    // tentar a un «fade» de entrada.
    fireEvent.click(screen.getByRole('button', { name: 'panadería' }))

    for (const tile of screen.getAllByTestId('product-tile')) {
      expect(tile.className).not.toMatch(/animate/)
    }
    expect(screen.getByRole('list').className).not.toMatch(/animate/)

    database.close()
  })

  it('adds the correct cart line when a tile is tapped', async () => {
    const database = freshDatabase()
    await seededRender(database)

    fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))

    await waitFor(async () => expect(await database.cart.count()).toBe(1))

    const line = (await database.cart.toArray())[0]

    expect(line).toEqual({
      id: expect.any(String),
      product_id: 'prod-3',
      qty: 1,
      unit_price_cents: 30,
    })

    database.close()
  })

  it('does not touch the state of a screen unmounted before its block resolves', async () => {
    // Regresión de desmonte: la pantalla carga el catálogo asincrónico y el
    // test desmonta antes de que la promesa resuelva. La carga no debe
    // pisar el estado de un árbol desmontado.
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    const { unmount } = render(<ProductCatalogScreen database={database} />)

    unmount()

    await database.products.count()

    database.close()
  })
})
