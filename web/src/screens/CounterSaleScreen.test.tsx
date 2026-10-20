// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { syncCatalogFromApi } from '@/catalog/catalogCache'
import { resetIdentifiedTapMemory } from '@/cart/identifiedLine'
import { POS_DATABASE_NAME, PosDatabase, type ProductRow } from '@/db/posDatabase'
import type { BarcodeDetectorLike } from '@/scanner/ProductScanner'
import { CounterSaleScreen } from '@/screens/CounterSaleScreen'

/*
 * Pantalla de venta de mostrador (W4-FE-1..3): la grilla entrega el código y el
 * panel del carrito vive en la misma pantalla; el escáner es un segmento
 * best-effort que comparte el mismo camino hacia el carrito. El ticket real
 * llega en W4-FE-4.
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

beforeEach(() => {
  // La memoria del doble toque es estado del módulo (un device = uno): cada
  // prueba empieza sin gesto previo, igual que un device recién cargado.
  resetIdentifiedTapMemory()
})

function fakeStream(): { stream: MediaStream; stop: ReturnType<typeof vi.fn> } {
  const stop = vi.fn()

  const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream

  return { stream, stop }
}

function asGetUserMedia(
  stub: ReturnType<typeof vi.fn>,
): typeof navigator.mediaDevices.getUserMedia {
  return stub as unknown as typeof navigator.mediaDevices.getUserMedia
}

function asShareInScreen(
  stub: ReturnType<typeof vi.fn>,
): (data?: ShareData) => Promise<void> {
  return stub as unknown as (data?: ShareData) => Promise<void>
}

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

  it('walks grid → cart → checkout → local close, with the POST after the close', async () => {
    /*
     * El cobro NO espera la red (ARCHITECTURE.md:98): `Finalizar venta`
     * cierra localmente y muestra la venta cerrada antes de que el POST
     * responda. El POST va con su clave de idempotencia y el método elegido
     * es el que viaja a `sales.payment_method`.
     */
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    const saleResponses: Request[] = []
    const fetchSpy = vi.fn(async (_url: string | URL, init?: RequestInit) => {
      saleResponses.push(new Request('/sales', init))

      return new Response(
        JSON.stringify({ sale: { sale_id: '00000000-0000-0000-0000-000000000009' } }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )
    })

    try {
      render(<CounterSaleScreen database={database} token="token-de-la-sesion" fetchImpl={fetchSpy as unknown as typeof fetch} />)

      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      // Grilla → línea del carrito: el panel muestra los controles de línea
      // cuando la mutación persistió (el sincronizador es la UI, no el reloj).
      fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))
      expect(
        await screen.findByRole('button', { name: 'Agregar una unidad de Pan francés' }),
      ).toBeTruthy()

      // Barra inferior → checkout.
      fireEvent.click(screen.getByTestId('cobrar-button'))

      expect(screen.getByText('Cobrar')).toBeTruthy()

      // Tender único: el método elegido es el que fija el payload.
      fireEvent.click(screen.getByTestId('tender-method-yape'))
      fireEvent.click(screen.getByRole('button', { name: 'Finalizar venta' }))

      // La venta ya cerró localmente: la superficie exacta del ticket llega
      // en W4-FE-4; acá se dice la verdad sin fingir una remotamente.
      await screen.findByTestId('sale-closed-summary')
      // El POST sale DESPUÉS del cierre local: llega al servidor cuando llega.
      await waitFor(() => expect(fetchSpy).toHaveBeenCalled())

      const [url, requestInit] = (fetchSpy.mock.calls[0] ?? []) as [
        string,
        (RequestInit & { body: string }) | undefined,
      ]

      expect(url).toBe('/sales')
      expect((requestInit?.headers as Record<string, string>)['idempotency-key']).toBeTruthy()
      expect(JSON.parse(requestInit!.body).payment_method).toBe('yape')
      expect(JSON.parse(requestInit!.body).total_cents).toBe(30)

      // El carrito quedó vacío tras el cierre local.
      expect(await database.cart.count()).toBe(0)
    } finally {
      vi.restoreAllMocks()

      database.close()
    }
  })

  it('starts on the products segment and mounts the camera scanner on demand', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())

    const { stream, stop } = fakeStream()
    const getUserMedia = vi.fn(async () => stream)
    const detector: BarcodeDetectorLike = { detect: async () => [] }
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    try {
      render(
        <CounterSaleScreen
          database={database}
          getUserMedia={asGetUserMedia(getUserMedia)}
          createBarcodeDetector={async () => detector}
        />,
      )
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      // §1.2:134 — Productos (grilla) es la vista por defecto; la cámara es
      // best-effort y se enciende solo cuando el cajero la pide.
      expect(
        screen.getByRole('button', { name: 'Productos' }).getAttribute('aria-pressed'),
      ).toBe('true')
      expect(screen.queryByTestId('scanner-viewport')).toBeNull()

      fireEvent.click(screen.getByRole('button', { name: 'Escáner' }))

      expect(await screen.findByTestId('scanner-viewport')).toBeTruthy()
      expect(getUserMedia).toHaveBeenCalledWith({ video: true, audio: false })

      // Volver al segmento Productos apaga la cámara (el cleanup detiene el stream).
      fireEvent.click(screen.getByRole('button', { name: 'Productos' }))
      await waitFor(() => expect(screen.queryByTestId('scanner-viewport')).toBeNull())
      expect(stop).toHaveBeenCalled()
    } finally {
      database.close()
    }
  })

  it('falls back to the grid with a non-modal notice when the camera permission is denied', async () => {
    /*
     * Aceptación de W4-FE-3 (SPRINTS.md:229; DESIGN.md:251): con el permiso
     * denegado, la venta se completa igual desde la grilla — el aviso es no
     * modal y el segmento regresa solo a Productos.
     */
    const getUserMedia = vi.fn(async () => {
      throw new DOMException('NotAllowedError', 'NotAllowedError')
    })
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    try {
      render(
        <CounterSaleScreen database={database} getUserMedia={asGetUserMedia(getUserMedia)} />,
      )
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      fireEvent.click(screen.getByRole('button', { name: 'Escáner' }))

      const notice = await screen.findByTestId('scanner-notice')

      expect(notice.getAttribute('role')).toBe('status')
      expect(notice.textContent).toContain('Permiso de cámara denegado')
      expect(screen.queryByTestId('scanner-viewport')).toBeNull()
      // La grilla quedó a la vista y la venta se completa igual.
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))

      expect(
        await screen.findByRole('button', { name: 'Agregar una unidad de Pan francés' }),
      ).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('falls back to the grid with a non-modal notice when the camera is unavailable', async () => {
    const getUserMedia = vi.fn(async () => {
      throw new DOMException('NotFoundError', 'NotFoundError')
    })
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    try {
      render(
        <CounterSaleScreen database={database} getUserMedia={asGetUserMedia(getUserMedia)} />,
      )
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      fireEvent.click(screen.getByRole('button', { name: 'Escáner' }))

      const notice = await screen.findByTestId('scanner-notice')

      expect(notice.textContent).toContain('La cámara no está disponible')
      expect(screen.queryByTestId('scanner-viewport')).toBeNull()
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))
    } finally {
      database.close()
    }
  })

  it('adds the decoded camera code to the cart through the same identified path', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())

    const { stream } = fakeStream()
    const getUserMedia = vi.fn(async () => stream)

    let frames = 0
    const detector: BarcodeDetectorLike = {
      detect: async () => {
        frames += 1

        return frames === 1 ? [{ rawValue: '7591234567890' }] : []
      },
    }
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    try {
      render(
        <CounterSaleScreen
          database={database}
          getUserMedia={asGetUserMedia(getUserMedia)}
          createBarcodeDetector={async () => detector}
        />,
      )
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      fireEvent.click(screen.getByRole('button', { name: 'Escáner' }))

      // El código escaneado entra por el MISMO camino que la grilla: una línea
      // del carrito con el congelado de W4-FE-1, no una vía paralela.
      expect(
        await screen.findByRole('button', { name: 'Agregar una unidad de Pan francés' }),
      ).toBeTruthy()
      expect(await database.cart.count()).toBe(1)
    } finally {
      database.close()
    }
  })

  it('shows the on-screen ticket with its operation number after the local close', async () => {
    /*
     * W4-FE-4 (SPRINTS.md:230; DESIGN.md:176): la venta termina EN UN TICKET
     * en pantalla con su número de operación. En un escritorio sin share sheet
     * el botón Compartir NO existe: la degradación es honesta, no un botón
     * que no hace nada.
     */
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    try {
      render(<CounterSaleScreen database={database} />)
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))
      expect(
        await screen.findByRole('button', { name: 'Agregar una unidad de Pan francés' }),
      ).toBeTruthy()
      fireEvent.click(screen.getByTestId('cobrar-button'))
      fireEvent.click(screen.getByTestId('tender-method-yape'))
      fireEvent.click(screen.getByRole('button', { name: 'Finalizar venta' }))

      const ticket = await screen.findByTestId('sale-closed-summary')

      expect(ticket.textContent).toContain('S/ 0.30')
      expect(screen.queryByTestId('sale-delivery-status')?.textContent).toContain(
        'el envío queda pendiente',
      )
      // El número de operación es el sale_id del payload congelado.
      expect(screen.getByText(/N.º de operación/)).toBeTruthy()
      expect(screen.queryByTestId('share-ticket')).toBeNull()
      expect(screen.getByRole('button', { name: 'Nueva venta' })).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('shares the finished sale through the system share sheet with no wa.me path', async () => {
    const openSpy = vi.spyOn(window, 'open')
    const share = vi.fn(async (_data?: ShareData): Promise<void> => {})
    const database = freshDatabase()
    await database.products.bulkAdd(PRODUCTS)

    try {
      render(<CounterSaleScreen database={database} share={asShareInScreen(share)} />)
      await waitFor(() => expect(screen.getAllByTestId('product-tile')).toHaveLength(1))

      fireEvent.click(screen.getByRole('button', { name: 'Pan francés, S/ 0.30' }))
      expect(
        await screen.findByRole('button', { name: 'Agregar una unidad de Pan francés' }),
      ).toBeTruthy()
      fireEvent.click(screen.getByTestId('cobrar-button'))
      fireEvent.click(screen.getByTestId('tender-method-yape'))
      fireEvent.click(screen.getByRole('button', { name: 'Finalizar venta' }))

      fireEvent.click(await screen.findByTestId('share-ticket'))

      await waitFor(() => expect(share).toHaveBeenCalledTimes(1))

      const shareData = share.mock.calls[0]?.[0]

      expect(shareData?.title).toBe('Venta de mostrador')
      expect(String(shareData?.text)).toContain('S/ 0.30')
      // Aceptación: la venta comparte por el share sheet del sistema, sin
      // NINGÚN camino wa.me — ni texto, ni ventana, ni URL.
      expect(String(shareData?.text)).not.toContain('wa.me')
      expect(openSpy).not.toHaveBeenCalled()
      expect(await screen.findByText('Compartido con el sistema.')).toBeTruthy()
    } finally {
      vi.restoreAllMocks()

      database.close()
    }
  })
})
