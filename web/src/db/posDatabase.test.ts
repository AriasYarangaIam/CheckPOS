// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
// global, y este runner no trae IndexedDB.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it } from 'vitest'

import {
  POS_DATABASE_NAME,
  PosDatabase,
  getPosDatabase,
  type CartRow,
  type IdempotencyQueueRow,
} from '@/db/posDatabase'

/*
 * Cada test usa su propia base: `fake-indexeddb` es un almacén por proceso y
 * compartir nombre haría que un test leyera las filas de otro.
 */
const createdNames = new Set<string>()
let nextId = 0

function freshDatabase(): PosDatabase {
  nextId += 1
  const name = `${POS_DATABASE_NAME}-test-${nextId}`
  createdNames.add(name)

  return new PosDatabase(name)
}

/** Cierra la conexión y vuelve a abrir el mismo nombre: una recarga en la PWA. */
function reopen(database: PosDatabase): PosDatabase {
  const name = database.name
  database.close()

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

/*
 * Las fixtures están tipadas con el tipo de la fila, no solo escritas con su
 * forma: `tsc` rechaza una fila a la que le falte un campo o le sobre uno, así
 * que la garantía de forma de `CartRow` / `IdempotencyQueueRow` es de tiempo de
 * compilación. Es una línea de defensa legítima y distinta: Dexie guarda los
 * campos que no son índice tal como llegan, sin validarlos al escribir, de modo
 * que no existe una aserción de tiempo de ejecución equivalente.
 */
const CART_LINES: readonly CartRow[] = [
  { id: 'line-1', product_id: 'prod-1', qty: 2, unit_price_cents: 350 },
  { id: 'line-2', product_id: 'prod-2', qty: 1, unit_price_cents: 1250 },
]

const QUEUED_EVENTS: readonly IdempotencyQueueRow[] = [
  {
    id: 'event-1',
    key: 'sale-0001',
    payload: JSON.stringify({ total_cents: 1950, payment_method: 'efectivo' }),
    status: 'pending',
    created_at: '2026-10-15T09:15:00.000Z',
  },
  {
    id: 'event-2',
    key: 'sale-0002',
    payload: JSON.stringify({ total_cents: 480, payment_method: 'yape' }),
    status: 'completed',
    created_at: '2026-10-15T09:21:30.000Z',
  },
]

/*
 * Especificación de almacenes: la clave primaria y los índices con los que Dexie
 * abre cada tabla (docs/FRONTEND.md:96-100). Es lo que el esquema impone de
 * verdad, a diferencia de la forma de la fila, que no valida nada.
 */
const DECLARED_STORES: readonly {
  readonly table: string
  readonly primaryKey: string
  readonly indexes: readonly string[]
}[] = [
  { table: 'products', primaryKey: 'id', indexes: ['gtin', 'name', 'category'] },
  { table: 'cart', primaryKey: 'id', indexes: ['product_id'] },
  { table: 'idempotency_queue', primaryKey: 'id', indexes: ['key', 'status', 'created_at'] },
]

/** Clave primaria y caminos de índice declarados, tabla por tabla. */
function declaredPaths(database: PosDatabase): { name: string; paths: string[] }[] {
  return database.tables.map((table) => ({
    name: table.name,
    paths: [
      String(table.schema.primKey.keyPath),
      ...table.schema.indexes.map((index) => String(index.keyPath)),
    ],
  }))
}

describe('PosDatabase schema', () => {
  it('declares the primary key and the index paths each store opens with', async () => {
    const database = freshDatabase()
    await database.open()

    try {
      expect(database.tables.map((table) => table.name).sort()).toEqual(
        DECLARED_STORES.map((store) => store.table).sort(),
      )

      for (const store of DECLARED_STORES) {
        const schema = database.table(store.table).schema

        expect(String(schema.primKey.keyPath), `${store.table} primKey`).toBe(store.primaryKey)
        expect(
          schema.indexes.map((index) => String(index.keyPath)).sort(),
          `${store.table} índices`,
        ).toEqual([...store.indexes].sort())
      }
    } finally {
      database.close()
    }
  })

  it('exposes only the local tables, never one holding the global catalog', async () => {
    const database = freshDatabase()
    await database.open()

    const declared = declaredPaths(database)
    database.close()

    // Criterio de W1-FE-4: lo local es el catálogo del tenant, el carrito y la
    // cola. Un volcado del catálogo global no tiene tabla acá (DATABASE.md:160).
    expect(declared.map((entry) => entry.name).sort()).toEqual([
      'cart',
      'idempotency_queue',
      'products',
    ])

    // El conjunto exacto de arriba ya descarta una tabla con cualquier otro
    // nombre —una heurística sobre nombres se evade con un nombre como
    // `master_skus`—, así que lo que se afirma acá es que tampoco haya un camino
    // declarado (clave primaria o índice) apuntando al catálogo global. Los
    // campos que no son índice quedan fuera de alcance: los impone el tipo de la
    // fila en tiempo de compilación, no el esquema.
    for (const entry of declared) {
      for (const path of [entry.name, ...entry.paths]) {
        expect(path).not.toMatch(/global|catalog/i)
      }
    }
  })

  it('keeps the local catalog projection and not the global row', async () => {
    const database = freshDatabase()
    await database.open()

    await database.products.add({
      id: 'prod-1',
      gtin: '7591234567890',
      name: 'Arroz Costeño 1kg',
      price_cents: 350,
      uom: 'unit',
      stock: 24,
      photo_url: 'https://storage.example.com/arroz.png',
      category: 'abarrotes',
    })

    const stored = await database.products.get('prod-1')
    database.close()

    // El dueño de la fila es el tenant: trae su precio y su stock, que el
    // catálogo global no tiene. La forma de la fila no se afirma acá —Dexie
    // guarda los campos que no son índice sin validarlos, así que comparar las
    // claves guardadas contra las que se escribieron sería una tautología—: esa
    // garantía es de compilación, vía el tipo `ProductRow` que `tsc` impone.
    // Lo que corresponde afirmar en ejecución es el viaje de ida y vuelta.
    expect(stored?.price_cents).toBe(350)
    expect(stored?.stock).toBe(24)
  })

  it('finds a local product by its barcode', async () => {
    const database = freshDatabase()
    await database.open()

    await database.products.bulkAdd([
      {
        id: 'prod-1',
        gtin: '7591234567890',
        name: 'Arroz Costeño 1kg',
        price_cents: 350,
        uom: 'unit',
        stock: 24,
        photo_url: null,
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
    ])

    const found = await database.products.where('gtin').equals('7591234567890').toArray()
    database.close()

    expect(found).toHaveLength(1)
    expect(found[0]?.id).toBe('prod-1')
  })
})

describe('PosDatabase persistence across a reload', () => {
  it('keeps the cart', async () => {
    const database = freshDatabase()
    await database.open()
    await database.cart.bulkAdd(CART_LINES)

    const reopened = reopen(database)

    try {
      expect(await reopened.cart.orderBy('id').toArray()).toEqual(CART_LINES)
    } finally {
      reopened.close()
    }
  })

  it('keeps the idempotency queue with the status of each event', async () => {
    const database = freshDatabase()
    await database.open()
    await database.idempotency_queue.bulkAdd(QUEUED_EVENTS)

    const reopened = reopen(database)

    try {
      expect(await reopened.idempotency_queue.count()).toBe(2)
      expect(await reopened.idempotency_queue.orderBy('id').toArray()).toEqual(QUEUED_EVENTS)

      // El estado sobrevive a la recarga: un evento ya confirmado no se
      // reenvía al reconectar.
      expect(await reopened.idempotency_queue.where('status').equals('pending').count()).toBe(1)
    } finally {
      reopened.close()
    }
  })

  it('keeps the changes written after a reload', async () => {
    const database = freshDatabase()
    await database.open()
    await database.cart.add(CART_LINES[0])

    const reopened = reopen(database)
    await reopened.cart.update('line-1', { qty: 5 })

    const second = reopen(reopened)

    try {
      expect((await second.cart.get('line-1'))?.qty).toBe(5)
    } finally {
      second.close()
    }
  })

  it('reads the store by name, so the reopen above proves persistence', async () => {
    // Control negativo del criterio: si el dato viviera en una caché del
    // proceso en lugar del almacén, estas dos comprobaciones fallarían.
    const database = freshDatabase()
    await database.open()
    await database.cart.add(CART_LINES[0])
    const name = database.name
    database.close()

    const other = new PosDatabase(`${name}-otra`)
    createdNames.add(other.name)
    expect(await other.cart.count()).toBe(0)
    other.close()

    await Dexie.delete(name)
    const afterDelete = new PosDatabase(name)

    try {
      expect(await afterDelete.cart.count()).toBe(0)
    } finally {
      afterDelete.close()
    }
  })
})

describe('getPosDatabase', () => {
  it('opens no connection until the first operation', async () => {
    const database = getPosDatabase()
    createdNames.add(database.name)

    try {
      expect(database.name).toBe(POS_DATABASE_NAME)
      // La pereza es el punto del accesor: importar el módulo y pedir la
      // instancia no abre IndexedDB, así que el arranque de la PWA no depende de
      // la base; la conexión nace con la primera operación real.
      expect(database.isOpen()).toBe(false)

      await database.cart.count()

      expect(database.isOpen()).toBe(true)
    } finally {
      database.close()
    }
  })
})
