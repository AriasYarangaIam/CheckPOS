// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { PendingSyncBadge, pendingSyncText } from '@/components/PendingSyncBadge'
import { POS_DATABASE_NAME, PosDatabase, type IdempotencyQueueRow } from '@/db/posDatabase'
import { markSaleQueueStatus } from '@/sales/saleQueue'

/*
 * Badge de ventas por sincronizar (W5-FE-1; docs/DESIGN.md §5.1:255). La copia
 * es contrato —`1 venta por sincronizar` / `N ventas por sincronizar`—, el tono
 * es el ámbar de `badge-fiado` y con cero pendientes no se renderiza nada.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-pending-badge-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

/** Evento de venta con la forma de la fila; lo único que varía es el estado. */
function saleRow(index: number, status: IdempotencyQueueRow['status']): IdempotencyQueueRow {
  return {
    id: `venta-${index}`,
    key: `clave-${index}`,
    payload: JSON.stringify({ total_cents: 300, payment_method: 'cash' }),
    status,
    created_at: '2026-10-20T10:00:00.000Z',
    operation: 'sale',
    business_date: '2026-10-20',
    sale_id: `0a300000-0000-7000-8000-00000000000${index}`,
    payment_method: 'cash',
    total_cents: 300,
    last_error: null,
  }
}

describe('pendingSyncText', () => {
  it('singular for one and plural for the rest, with the exact document copy', () => {
    expect(pendingSyncText(1)).toBe('1 venta por sincronizar')
    expect(pendingSyncText(3)).toBe('3 ventas por sincronizar')
  })
})

describe('PendingSyncBadge', () => {
  it('renders nothing while there is nothing to synchronize', () => {
    const database = freshDatabase()

    render(<PendingSyncBadge database={database} />)

    expect(screen.queryByTestId('pending-sync-badge')).toBeNull()
    database.close()
  })

  it('announces one pending sale in the amber tone of the offline state', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([saleRow(1, 'pending')])

    render(<PendingSyncBadge database={database} />)

    const badge = await screen.findByTestId('pending-sync-badge')

    expect(badge.textContent).toBe('1 venta por sincronizar')
    // Ámbar `badge-fiado` (§2.1:49): el mismo tono del badge de conectividad
    // cuando la PWA trabaja contra IndexedDB.
    expect(badge.className).toContain('bg-badge-fiado')
    expect(badge.getAttribute('role')).toBe('status')
    // Estado, no control: no aparece como botón ni abre nada.
    expect(screen.queryByRole('button')).toBeNull()
    database.close()
  })

  it('counts every pending sale, not the confirmed or failed ones', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      saleRow(1, 'pending'),
      saleRow(2, 'pending'),
      saleRow(3, 'completed'),
      saleRow(4, 'failed'),
    ])

    render(<PendingSyncBadge database={database} />)

    expect((await screen.findByTestId('pending-sync-badge')).textContent).toBe(
      '2 ventas por sincronizar',
    )
    database.close()
  })

  it('disappears as soon as the last sale is acknowledged', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([saleRow(1, 'pending')])

    render(<PendingSyncBadge database={database} />)
    await screen.findByTestId('pending-sync-badge')

    await act(async () => {
      await markSaleQueueStatus('clave-1', 'completed', undefined, database)
    })

    await waitFor(() => expect(screen.queryByTestId('pending-sync-badge')).toBeNull())
    database.close()
  })
})
