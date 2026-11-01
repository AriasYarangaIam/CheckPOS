// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'
import { enqueueFiadoPayment } from '@/fiado/fiadoQueue'
import { accountBalanceCents } from '@/fiado/fiadoStore'
import { restoreFiadoAccounts } from '@/fiado/restoreFiado'

/*
 * Restauración del fiado tras reinstalar (W7-FE-3; docs/BACKEND.md §4.12;
 * decisión 10 del sprint 7). La restauración es UNA lectura de
 * `GET /fiado-accounts` que reemplaza las dos tiendas de fiado en una sola
 * transacción y NO toca la cola de eventos ni el catálogo: lo que el
 * dispositivo todavía no confirmó no se pierde por reinstalar la vista.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-fiado-restore-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

/** La respuesta de §4.12 con una cuenta, un cargo y un abono (decisión 4 de forma). */
const ACCOUNTS_BODY = {
  accounts: [
    {
      id: 'acct-rosa',
      tenant_id: 'tenant-1',
      name: 'Rosa Quispe',
      phone: '+51987111222',
      created_at: '2026-10-26T12:00:00Z',
      updated_at: '2026-10-26T12:00:00Z',
      received_at: '2026-10-26T12:00:00Z',
      balance_cents: 1000,
      movements: [
        {
          id: 'mov-1',
          tenant_id: 'tenant-1',
          fiado_account_id: 'acct-rosa',
          amount_cents: 1500,
          sold_on: '2026-10-26',
          sale_id: 'sale-1',
          note: 'Venta fiada',
          created_at: '2026-10-26T12:00:00Z',
          received_at: '2026-10-26T12:00:00Z',
        },
        {
          id: 'mov-2',
          tenant_id: 'tenant-1',
          fiado_account_id: 'acct-rosa',
          amount_cents: -500,
          sold_on: null,
          sale_id: null,
          note: 'Abono efectivo',
          created_at: '2026-10-26T13:00:00Z',
          received_at: '2026-10-26T13:00:00Z',
        },
      ],
    },
  ],
}

function stubFetch(body: unknown, status = 200): typeof fetch {
  return (async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json' },
    })) as unknown as typeof fetch
}

describe('la restauración de la cartera', () => {
  it('reconstruye cuentas, movimientos y saldos computados en un dispositivo vacío', async () => {
    const database = freshDatabase()

    try {
      const outcome = await restoreFiadoAccounts({
        token: 'access-token',
        fetchImpl: stubFetch(ACCOUNTS_BODY),
        database,
      })

      expect(outcome).toEqual({ status: 'restored', accounts: 1, movements: 2 })

      const account = await database.fiado_accounts.get('acct-rosa')
      expect(account?.name).toBe('Rosa Quispe')

      const movements = await database.fiado_movements.toArray()
      expect(accountBalanceCents(movements)).toBe(1000)
      expect(movements.map((movement) => movement.amount_cents).sort()).toEqual([-500, 1500])
    } finally {
      database.close()
    }
  })

  it('pide la cartera con el token en la cabecera', async () => {
    const database = freshDatabase()
    const seen: string[] = []

    try {
      const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
        seen.push(`${String(input)} ${init?.headers ? JSON.stringify(init.headers) : ''}`)
        return new Response(JSON.stringify(ACCOUNTS_BODY), { status: 200 })
      }) as unknown as typeof fetch

      await restoreFiadoAccounts({ token: 'access-token', fetchImpl, baseUrl: 'https://api.example', database })

      expect(seen).toHaveLength(1)
      expect(seen[0]).toContain('https://api.example/fiado-accounts')
      expect(seen[0]).toContain('Bearer access-token')
    } finally {
      database.close()
    }
  })

  it('no toca la cola de eventos ni el catálogo pendientes de sincronizar', async () => {
    const database = freshDatabase()

    try {
      await enqueueFiadoPayment(
        { accountId: 'acct-rosa', amountCents: 200, movementKey: 'payment-key-1' },
        database,
      )
      await database.products.add({
        id: 'prod-1',
        gtin: null,
        name: 'Arroz',
        price_cents: 500,
        uom: 'unit',
        stock: 3,
        photo_url: null,
        category: null,
      })

      await restoreFiadoAccounts({
        token: 'access-token',
        fetchImpl: stubFetch(ACCOUNTS_BODY),
        database,
      })

      expect(await database.idempotency_queue.count()).toBe(1)
      expect(await database.products.count()).toBe(1)
    } finally {
      database.close()
    }
  })

  it('reemplaza la vista anterior en lugar de duplicarla', async () => {
    const database = freshDatabase()

    try {
      await database.fiado_accounts.add({
        id: 'acct-rosa',
        name: 'Rosa Q.',
        phone: null,
        created_at: '2026-10-20T12:00:00.000Z',
        updated_at: '2026-10-20T12:00:00.000Z',
      })

      await restoreFiadoAccounts({
        token: 'access-token',
        fetchImpl: stubFetch(ACCOUNTS_BODY),
        database,
      })

      const accounts = await database.fiado_accounts.toArray()
      expect(accounts).toHaveLength(1)
      expect(accounts[0].name).toBe('Rosa Quispe')
      expect(await database.fiado_movements.count()).toBe(2)
    } finally {
      database.close()
    }
  })

  it('se saltea sin sesión en línea, sin inventar un pull anónimo', async () => {
    const database = freshDatabase()

    try {
      const outcome = await restoreFiadoAccounts({ database })

      expect(outcome).toEqual({ status: 'skipped', reason: 'no-session' })
      expect(await database.fiado_accounts.count()).toBe(0)
    } finally {
      database.close()
    }
  })

  it('no lanza un error de sincronización cuando la red falla', async () => {
    const database = freshDatabase()

    try {
      const fetchImpl = (async () => {
        throw new Error('network down')
      }) as unknown as typeof fetch

      const outcome = await restoreFiadoAccounts({ token: 'access-token', fetchImpl, database })

      expect(outcome).toEqual({ status: 'unconfirmed' })
      expect(await database.fiado_accounts.count()).toBe(0)
    } finally {
      database.close()
    }
  })
})
