// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'
import { useFiadoWallet } from '@/hooks/useFiadoWallet'

/*
 * Cartera de deudores en el render (W7-FE-1).
 *
 * Lo que se fija acá es el borde: cuando la lectura local RECHAZA —una base
 * cerrada es el fallo real de IndexedDB— el hook conserva su estado honesto
 * inicial y la cadena no se escapa como un rechazo no controlado.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-fiado-wallet-hook-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

describe('useFiadoWallet', () => {
  it('keeps the honest empty wallet and never escapes the rejection', () => {
    const database = freshDatabase()
    database.close()

    const { result, unmount } = renderHook(() => useFiadoWallet(database))

    expect(result.current.wallet).toEqual({ accounts: [], movements: [] })
    unmount()
  })
})
