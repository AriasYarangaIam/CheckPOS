// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type FiadoAccountRow, type FiadoMovementRow } from '@/db/posDatabase'
import { accountBalanceCents, searchFiadoAccounts, seedFiadoAccount } from '@/fiado/fiadoStore'

/*
 * Registro local de deudores (W7-FE-1; docs/FRONTEND.md:51). La invariante que
 * estas pruebas fijan es la del contrato: NO existe un saldo guardado —el saldo
 * es `SUM(amount_cents)` de los movimientos firmados y se computa al leer— y la
 * búsqueda encuentra por nombre y por teléfono.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-fiado-store-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const ACCOUNT: FiadoAccountRow = {
  id: 'acct-rosa',
  name: 'Rosa Quispe',
  phone: '+51987111222',
  created_at: '2026-10-26T12:00:00.000Z',
  updated_at: '2026-10-26T12:00:00.000Z',
}

function movement(overrides: Partial<FiadoMovementRow> & Pick<FiadoMovementRow, 'id' | 'amount_cents'>): FiadoMovementRow {
  return {
    fiado_account_id: ACCOUNT.id,
    note: null,
    sold_on: null,
    sale_id: null,
    created_at: '2026-10-26T12:00:00.000Z',
    ...overrides,
  }
}

describe('el saldo local de fiado', () => {
  it('es la suma firmada de los movimientos y no una columna de la cuenta', async () => {
    const database = freshDatabase()

    try {
      await database.fiado_accounts.add(ACCOUNT)
      await database.fiado_movements.bulkAdd([
        movement({ id: 'mov-1', amount_cents: 1500, sold_on: '2026-10-26', sale_id: 'sale-1' }),
        movement({ id: 'mov-2', amount_cents: -500, note: 'Abono efectivo' }),
      ])

      const account = await database.fiado_accounts.get(ACCOUNT.id)
      const movements = await database.fiado_movements.where('fiado_account_id').equals(ACCOUNT.id).toArray()

      // La fila de la cuenta no tiene saldo: lo que hay es lo que el contrato
      // guarda (nombre y teléfono), y el saldo sale de los movimientos.
      expect(Object.keys(account ?? {}).sort()).toEqual([
        'created_at',
        'id',
        'name',
        'phone',
        'updated_at',
      ])
      expect(accountBalanceCents(movements)).toBe(1000)
    } finally {
      database.close()
    }
  })

  it('vale cero para una cuenta sin movimientos', () => {
    expect(accountBalanceCents([])).toBe(0)
  })
})

describe('la búsqueda de deudores', () => {
  const accounts: readonly FiadoAccountRow[] = [
    ACCOUNT,
    {
      id: 'acct-jose',
      name: 'José Núñez',
      phone: null,
      created_at: '2026-10-26T12:00:00.000Z',
      updated_at: '2026-10-26T12:00:00.000Z',
    },
  ]

  it('encuentra por nombre sin distinguir mayúsculas ni acentos', () => {
    expect(searchFiadoAccounts(accounts, 'rosa').map((account) => account.id)).toEqual(['acct-rosa'])
    expect(searchFiadoAccounts(accounts, 'JOSE').map((account) => account.id)).toEqual(['acct-jose'])
    expect(searchFiadoAccounts(accounts, 'núñez').map((account) => account.id)).toEqual(['acct-jose'])
  })

  it('encuentra por teléfono, con o sin el prefijo y con espacios', () => {
    expect(searchFiadoAccounts(accounts, '987111222').map((account) => account.id)).toEqual(['acct-rosa'])
    expect(searchFiadoAccounts(accounts, '+51 987 111 222').map((account) => account.id)).toEqual([
      'acct-rosa',
    ])
  })

  it('devuelve la cartera completa con una consulta vacía', () => {
    expect(searchFiadoAccounts(accounts, '   ')).toHaveLength(2)
  })

  it('no inventa coincidencias', () => {
    expect(searchFiadoAccounts(accounts, 'zzz')).toEqual([])
  })
})

describe('el alta local del deudor', () => {
  it('siembra la cuenta una sola vez y no pisa el nombre ya registrado', async () => {
    const database = freshDatabase()

    try {
      await seedFiadoAccount(
        { id: ACCOUNT.id, name: ACCOUNT.name, phone: ACCOUNT.phone },
        '2026-10-26T12:00:00.000Z',
        database,
      )
      await seedFiadoAccount(
        { id: ACCOUNT.id, name: 'Otro nombre', phone: null },
        '2026-10-27T12:00:00.000Z',
        database,
      )

      const accounts = await database.fiado_accounts.toArray()
      expect(accounts).toHaveLength(1)
      expect(accounts[0].name).toBe(ACCOUNT.name)
    } finally {
      database.close()
    }
  })
})
