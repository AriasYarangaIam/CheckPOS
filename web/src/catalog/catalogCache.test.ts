// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { syncCatalogFromApi, trimCatalogToBound, type ApiProductRow } from '@/catalog/catalogCache'
import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'

/*
 * Cache local del subconjunto del catálogo (SPRINTS.md W3-FE-3;
 * docs/FRONTEND.md:96-100): `GET /products` es la única fuente y su respuesta
 * se escribe como la proyección local que la grilla consume.
 *
 * Acá se muerden tres criterios del sprint: la grilla renderiza desde el
 * subconjunto (la fila local es lo único que existe después del sync), el
 * subconjunto sobrevive a una recarga y el catálogo global nunca aparece en el
 * dispositivo — la base no tiene tabla para él y el único HTTP que sale es
 * `GET /products` del tenant (ARCHITECTURE.md:89, :127).
 */

const createdNames = new Set<string>()
let nextId = 0

function freshDatabase(): PosDatabase {
  nextId += 1
  const name = `${POS_DATABASE_NAME}-sync-test-${nextId}`
  createdNames.add(name)

  return new PosDatabase(name)
}

/**
 * Filas del contrato §4.5 tal como el servidor las serializa: `stock` es el
 * texto NUMERIC verbatim y `attributes` es el JSONB elástico — con `category`
 * como uno de sus valores proyectados hacia la columna local.
 */
const API_ROWS: readonly ApiProductRow[] = [
  {
    id: 'a1-uuid',
    name: 'Arroz Costeño 1kg',
    gtin: '7591234567890',
    price_cents: 350,
    uom: 'unit',
    stock: '24',
    photo_url: 'https://storage.example.com/arroz.png',
    attributes: { category: 'abarrotes', marca: 'Costeño' },
    created_at: '2026-10-18T12:00:00Z',
    updated_at: '2026-10-18T12:00:00Z',
  },
  {
    // Granel: sin GTIN, sin foto, sin atributos.
    id: 'a2-uuid',
    name: 'Pan francés',
    gtin: null,
    price_cents: 30,
    uom: 'unit',
    stock: '40.5',
    photo_url: null,
    attributes: null,
    created_at: '2026-10-18T12:00:00Z',
    updated_at: '2026-10-18T12:00:00Z',
  },
  {
    id: 'a3-uuid',
    name: 'Azúcar Rubia 1kg',
    gtin: null,
    price_cents: 250,
    uom: 'kg',
    stock: '18',
    photo_url: null,
    attributes: { category: 'abarrotes' },
    created_at: '2026-10-18T12:00:00Z',
    updated_at: '2026-10-18T12:00:00Z',
  },
]

/** El cuerpo del contrato: `{ data: [...] }` (§4.5). */
function apiResponse(rows: readonly (ApiProductRow | unknown)[]): {
  data: readonly (ApiProductRow | unknown)[]
} {
  return { data: rows }
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

function jsonOk(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  }) as unknown as Response
}

describe('syncCatalogFromApi', () => {
  it('writes only the tenant subset returned by GET /products into the local cache', async () => {
    const database = freshDatabase()
    const fetchImpl = vi.fn().mockResolvedValue(jsonOk(apiResponse(API_ROWS)))

    const projection = await syncCatalogFromApi({ token: 'the-token', database, fetchImpl })

    // El único HTTP que sale es el listado del tenant: ni un endpoint global,
    // ni una segunda petición en cascada (W3-FE-3; ARCHITECTURE.md:89).
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    expect(fetchImpl.mock.calls[0][0]).toBe('/products')
    expect(fetchImpl.mock.calls[0][1].headers.authorization).toBe('Bearer the-token')

    // La proyección local es la forma de la fila de `products`, no la del
    // contrato: stock numérico, price_cents tipado, category proyectada del
    // JSONB (§2.9) y nada que no sea la fila del tenant.
    expect(projection).toEqual([
      {
        id: 'a1-uuid',
        gtin: '7591234567890',
        name: 'Arroz Costeño 1kg',
        price_cents: 350,
        uom: 'unit',
        stock: 24,
        photo_url: 'https://storage.example.com/arroz.png',
        category: 'abarrotes',
      },
      {
        id: 'a2-uuid',
        gtin: null,
        name: 'Pan francés',
        price_cents: 30,
        uom: 'unit',
        // El NUMERIC verbatim del contrato a la columna numérica local.
        stock: 40.5,
        photo_url: null,
        category: null,
      },
      {
        id: 'a3-uuid',
        gtin: null,
        name: 'Azúcar Rubia 1kg',
        price_cents: 250,
        uom: 'kg',
        stock: 18,
        photo_url: null,
        category: 'abarrotes',
      },
    ])

    expect(await database.products.toArray()).toEqual(projection)

    // El criterio del sprint sobre la base: solo las tiendas locales.
    // No hay ninguna comunidad para el catálogo global (posDatabase.test.ts
    // ya muerde el negativo; acá se muerde el positivo del cache). W5-FE-2 sumó
    // el cierre de caja local, que no es catálogo.
    expect(database.tables.map((table) => table.name).sort()).toEqual([
      'cart',
      'cash_closes',
      'device_session',
      'idempotency_queue',
      'products',
    ])
  })

  it('keeps the subset across a reload, so the grid never needs the network', async () => {
    const database = freshDatabase()
    await syncCatalogFromApi({ token: 't', database, fetchImpl: async () => jsonOk(apiResponse(API_ROWS)) })
    const name = database.name
    database.close()

    const reopened = new PosDatabase(name)

    try {
      const rows = await reopened.products.toArray()

      expect(rows).toHaveLength(3)
      expect(rows.find((row) => row.id === 'a1-uuid')?.price_cents).toBe(350)
    } finally {
      reopened.close()
    }
  })

  it('overwrites the cached row with the newest server truth instead of duplicating it', async () => {
    const database = freshDatabase()
    const fetchImpl = vi.fn().mockResolvedValue(jsonOk(apiResponse(API_ROWS)))

    await syncCatalogFromApi({ token: 't', database, fetchImpl })

    // LWW del catálogo: el servidor manda la fila con el precio nuevo y el
    // cache la reemplaza — nunca acumula copias de la misma fila.
    const republished = [API_ROWS[0], { ...API_ROWS[0], price_cents: 420, gtin: null }]

    await syncCatalogFromApi({
      token: 't',
      database,
      fetchImpl: async () => jsonOk(apiResponse(republished)),
    })

    const rice = (await database.products.toArray()).filter((row) => row.name.startsWith('Arroz'))
    expect(rice).toEqual([
      {
        id: 'a1-uuid',
        gtin: null,
        name: 'Arroz Costeño 1kg',
        price_cents: 420,
        uom: 'unit',
        stock: 24,
        photo_url: 'https://storage.example.com/arroz.png',
        category: 'abarrotes',
      },
    ])
  })

  it('rejects a contract body without its `data` array', async () => {
    const database = freshDatabase()

    await expect(
      syncCatalogFromApi({ token: 't', database, fetchImpl: async () => jsonOk({ products: [] }) }),
    ).rejects.toThrow(/data/)

    expect(await database.products.count()).toBe(0)
  })

  it('propagates the server error without writing, and accepts an empty subset', async () => {
    const database = freshDatabase()
    const failing = vi.fn().mockResolvedValue(new Response('', { status: 500 }) as unknown as Response)

    await expect(
      syncCatalogFromApi({ token: 't', database, fetchImpl: failing }),
    ).rejects.toThrow(/500/)

    expect(await database.products.count()).toBe(0)

    // Un subset vacío es un estado legítimo (tenant nuevo): la lista se vacía,
    // que es lo que el servidor dijo.
    await syncCatalogFromApi({ token: 't', database, fetchImpl: async () => jsonOk(apiResponse([])) })
    expect(await database.products.count()).toBe(0)

    database.close()
  })

  it('skips the rows that break the row contract and keeps the valid ones', async () => {
    const database = freshDatabase()

    await syncCatalogFromApi({
      token: 't',
      database,
      fetchImpl: async () =>
        jsonOk(
          apiResponse([
            ...API_ROWS,
            // Fila rota de tres maneras: sin nombre, precio flotante, precio
            // negativo — la fila del contrato nunca tendría estos defectos y
            // en la grilla cache no pueden aparecer.
            { id: 'broken-1', price_cents: 100, uom: 'unit', stock: '1', gtin: null },
            { id: 'broken-2', name: 'Fila flotante', price_cents: 99.99, uom: 'unit', stock: '1', gtin: null },
            { id: 'broken-3', name: 'Fila negativa', price_cents: -5, uom: 'unit', stock: '1', gtin: null },
          ]),
        ),
    })

    const rows = await database.products.toArray()

    expect(rows.map((row) => row.id)).toEqual(['a1-uuid', 'a2-uuid', 'a3-uuid'])
  })
  it('trims the local subset back to its bound', async () => {
    /*
     * W6-FE-1: el subconjunto local está acotado. Con más filas que el techo, el cache
     * recorta por id y devuelve cuántas eliminó: el dispositivo no puede llenarse con el
     * catálogo.
     */
    const database = freshDatabase()
    await database.products.bulkAdd(
      Array.from({ length: 5 }, (_, index) => ({
        id: `019a0000-0000-7000-8000-00000000000${index}`,
        gtin: null,
        name: `producto ${index}`,
        price_cents: 100,
        uom: 'unit',
        stock: 0,
        photo_url: null,
        category: null,
      })),
    )

    try {
      const removed = await trimCatalogToBound(database, 3)

      expect(removed).toBe(2)
      expect(await database.products.count()).toBe(3)
    } finally {
      database.close()
    }
  })
})