// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'
import { accountBalanceCents } from '@/fiado/fiadoStore'
import { FiadoAccountsScreen } from '@/screens/FiadoAccountsScreen'

/*
 * Cartera de deudores (W7-FE-1/2/3; docs/FRONTEND.md:51). Lo que estas pruebas
 * fijan: el saldo es COMPUTADO del ledger local, la búsqueda encuentra por
 * nombre y por teléfono, el abono encola un movimiento NEGATIVO —el signo es el
 * tipo—, la restauración reconstruye la cartera desde el servidor y el estado de
 * cuenta se comparte con el share sheet nativo, sin ningún camino wa.me.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-fiado-screen-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

async function seedWallet(database: PosDatabase): Promise<void> {
  await database.fiado_accounts.bulkAdd([
    {
      id: 'acct-rosa',
      name: 'Rosa Quispe',
      phone: '+51987111222',
      created_at: '2026-10-26T12:00:00.000Z',
      updated_at: '2026-10-26T12:00:00.000Z',
    },
    {
      id: 'acct-jose',
      name: 'José Núñez',
      phone: null,
      created_at: '2026-10-26T12:00:00.000Z',
      updated_at: '2026-10-26T12:00:00.000Z',
    },
  ])
  await database.fiado_movements.bulkAdd([
    {
      id: 'mov-1',
      fiado_account_id: 'acct-rosa',
      amount_cents: 1500,
      note: 'Venta fiada',
      sold_on: '2026-10-26',
      sale_id: 'sale-1',
      created_at: '2026-10-26T12:00:00.000Z',
    },
    {
      id: 'mov-2',
      fiado_account_id: 'acct-rosa',
      amount_cents: -500,
      note: 'Abono efectivo',
      sold_on: null,
      sale_id: null,
      created_at: '2026-10-26T13:00:00.000Z',
    },
  ])
}

/** Digita un monto en el numpad: cada dígito es un toque en centavos. */
function typeAmount(digits: string): void {
  for (const digit of digits) {
    fireEvent.click(screen.getByRole('button', { name: digit }))
  }
}

describe('FiadoAccountsScreen', () => {
  it('lists the debtors with the balance computed from the signed ledger', async () => {
    const database = freshDatabase()
    await seedWallet(database)

    try {
      render(<FiadoAccountsScreen database={database} />)

      const balance = await screen.findByTestId('fiado-balance-acct-rosa')
      // 1500 del cargo menos 500 del abono: la suma, no una columna.
      expect(balance.textContent).toContain('S/ 10.00')
      expect(screen.getByTestId('fiado-balance-acct-jose').textContent).toContain('S/ 0.00')
    } finally {
      database.close()
    }
  })

  it('finds a debtor by name and by phone', async () => {
    const database = freshDatabase()
    await seedWallet(database)

    try {
      render(<FiadoAccountsScreen database={database} />)
      await screen.findByTestId('fiado-account-acct-rosa')

      fireEvent.change(screen.getByTestId('fiado-search'), { target: { value: '987111222' } })
      expect(screen.getByTestId('fiado-account-acct-rosa')).toBeTruthy()
      expect(screen.queryByTestId('fiado-account-acct-jose')).toBeNull()

      fireEvent.change(screen.getByTestId('fiado-search'), { target: { value: 'jose' } })
      expect(screen.getByTestId('fiado-account-acct-jose')).toBeTruthy()

      fireEvent.change(screen.getByTestId('fiado-search'), { target: { value: 'zzz' } })
      expect(screen.getByTestId('fiado-no-hits')).toBeTruthy()
    } finally {
      database.close()
    }
  })

  it('shows the signed history: the charge in text colour and the payment in emerald', async () => {
    const database = freshDatabase()
    await seedWallet(database)

    try {
      render(<FiadoAccountsScreen database={database} />)
      // El detalle arranca en la primera cuenta de la cartera (ordenada por nombre),
      // así que la prueba elige a la deudora con historial.
      fireEvent.click(await screen.findByTestId('fiado-account-acct-rosa'))
      await screen.findByTestId('fiado-movement-mov-1')

      const charge = screen.getByTestId('fiado-movement-mov-1')
      const payment = screen.getByTestId('fiado-movement-mov-2')

      expect(charge.textContent).toContain('S/ 15.00')
      expect(payment.textContent).toContain('-S/ 5.00')
      expect(payment.querySelector('.text-emerald-600')).toBeTruthy()
      expect(charge.querySelector('.text-emerald-600')).toBeNull()
    } finally {
      database.close()
    }
  })

  it('enqueues a partial payment as a negative movement and refreshes the computed balance', async () => {
    const database = freshDatabase()
    await seedWallet(database)

    try {
      render(<FiadoAccountsScreen database={database} />)
      await screen.findByTestId('fiado-account-acct-rosa')

      typeAmount('200')
      fireEvent.click(screen.getByTestId('fiado-tender-transfer'))
      fireEvent.click(screen.getByTestId('fiado-abono-submit'))

      await waitFor(() => expect(screen.getByTestId('fiado-notice')).toBeTruthy())

      const movements = await database.fiado_movements.toArray()
      expect(accountBalanceCents(movements)).toBe(800)

      const queued = await database.idempotency_queue.toArray()
      expect(queued).toHaveLength(1)
      expect(queued[0].operation).toBe('fiado_movement')
      expect(JSON.parse(queued[0].payload).amount_cents).toBe(-200)
      // El tender es de interfaz y NO viaja: el ledger no tiene esa columna.
      expect(JSON.parse(queued[0].payload).tender).toBeUndefined()
    } finally {
      database.close()
    }
  })

  it('restores the wallet from the server after a reinstall', async () => {
    const database = freshDatabase()
    const fetchSpy = vi.fn(async () =>
      new Response(
        JSON.stringify({
          accounts: [
            {
              id: 'acct-restored',
              tenant_id: 'tenant-1',
              name: 'Marta Luján',
              phone: null,
              created_at: '2026-10-27T12:00:00Z',
              updated_at: '2026-10-27T12:00:00Z',
              received_at: '2026-10-27T12:00:00Z',
              balance_cents: 2500,
              movements: [
                {
                  id: 'mov-restored',
                  tenant_id: 'tenant-1',
                  fiado_account_id: 'acct-restored',
                  amount_cents: 2500,
                  sold_on: '2026-10-27',
                  sale_id: 'sale-9',
                  note: 'Venta fiada',
                  created_at: '2026-10-27T12:00:00Z',
                  received_at: '2026-10-27T12:00:00Z',
                },
              ],
            },
          ],
        }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
    )

    try {
      render(
        <FiadoAccountsScreen
          database={database}
          fetchImpl={fetchSpy as unknown as typeof fetch}
          token="token-de-la-sesion"
        />,
      )

      await screen.findByTestId('fiado-empty')
      fireEvent.click(screen.getByTestId('fiado-restore'))

      const restored = await screen.findByTestId('fiado-account-acct-restored')
      expect(restored.textContent).toContain('Marta Luján')
      expect(screen.getByTestId('fiado-balance-acct-restored').textContent).toContain('S/ 25.00')
      expect(screen.getByTestId('fiado-notice').textContent).toContain('Cartera restaurada')
    } finally {
      vi.restoreAllMocks()
      database.close()
    }
  })

  it('skips the restore without an online session, without inventing an anonymous pull', async () => {
    const database = freshDatabase()

    try {
      render(<FiadoAccountsScreen database={database} />)

      await screen.findByTestId('fiado-empty')
      fireEvent.click(screen.getByTestId('fiado-restore'))

      await waitFor(() =>
        expect(screen.getByTestId('fiado-notice').textContent).toContain('Sin sesión en línea'),
      )
    } finally {
      database.close()
    }
  })

  it('shares the statement through the native sheet, with no WhatsApp path', async () => {
    const database = freshDatabase()
    await seedWallet(database)
    const share = vi.fn(async () => undefined)

    try {
      render(<FiadoAccountsScreen database={database} share={share} />)
      fireEvent.click(await screen.findByTestId('fiado-account-acct-rosa'))
      await screen.findByTestId('fiado-movement-mov-1')

      fireEvent.click(screen.getByTestId('fiado-share'))
      await waitFor(() => expect(share).toHaveBeenCalled())

      const [data] = share.mock.calls[0] as unknown as [ShareData]
      expect(data.title).toContain('Rosa Quispe')
      expect(data.text).toContain('Saldo: 10.00')
      expect(String(data.text)).not.toContain('wa.me')
    } finally {
      database.close()
    }
  })

  it('shows the empty state that points at the sale flow', async () => {
    const database = freshDatabase()

    try {
      render(<FiadoAccountsScreen database={database} />)

      const empty = await screen.findByTestId('fiado-empty')
      expect(empty.textContent).toContain('venta a fiado')
    } finally {
      database.close()
    }
  })
})
