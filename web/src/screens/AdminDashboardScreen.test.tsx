// Debe ser la primera importación: el dashboard lee la base local.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type CashCloseRow, type IdempotencyQueueRow } from '@/db/posDatabase'
import { AdminDashboardScreen } from '@/screens/AdminDashboardScreen'

/*
 * Dashboard del rol Administrador (W8-FE-3; docs/FRONTEND.md:49; `ARCHITECTURE.md:138`;
 * decisión D-E del sprint 8).
 *
 * Lo que se fija: los números salen SOLO de la proyección del ledger local más
 * `GET /fiado-accounts` —no existe endpoint de reporte ni se inventa uno—, la deuda de fiado dice
 * «pendiente» cuando no puede conocerse, y el fiado del desglose es informacional.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

/** La fecha de negocio del día: el dashboard usa el reloj local del dispositivo. */
const TODAY = '2026-10-26'

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-dashboard-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
  vi.unstubAllGlobals()
})

function atLocal(hour: number, minute = 0): string {
  return new Date(2026, 9, 26, hour, minute, 0).toISOString()
}

let nextId = 0

function event(overrides: {
  operation: 'sale' | 'void'
  totalCents: number
  paymentMethod: 'cash' | 'yape' | 'plin' | 'card' | 'other' | 'fiado'
  status?: 'pending' | 'completed' | 'failed'
  createdAt?: string
  businessDate?: string
}): IdempotencyQueueRow {
  nextId += 1

  return {
    id: `row-${nextId}`,
    key: `key-${nextId}`,
    payload: '{}',
    status: overrides.status ?? 'completed',
    created_at: overrides.createdAt ?? atLocal(10),
    operation: overrides.operation,
    business_date: overrides.businessDate ?? TODAY,
    sale_id: null,
    payment_method: overrides.paymentMethod,
    total_cents: overrides.totalCents,
    last_error: null,
  }
}

async function seedLedger(database: PosDatabase): Promise<void> {
  await database.idempotency_queue.bulkAdd([
    event({ operation: 'sale', totalCents: 2000, paymentMethod: 'cash', createdAt: atLocal(9) }),
    event({ operation: 'sale', totalCents: 1000, paymentMethod: 'yape', status: 'pending', createdAt: atLocal(12) }),
    event({ operation: 'sale', totalCents: 1500, paymentMethod: 'fiado', createdAt: atLocal(18) }),
    event({ operation: 'void', totalCents: 500, paymentMethod: 'cash', createdAt: atLocal(19) }),
    // Rechazada por el servidor: no es una venta y no puede inflar el día.
    event({ operation: 'sale', totalCents: 9999, paymentMethod: 'cash', status: 'failed' }),
  ])
}

/** Cierre local sobrante de 300 con 500 de fiado informacional (que NO es faltante). */
async function seedClose(database: PosDatabase): Promise<void> {
  const counts = [
    { payment_method: 'cash', expected_cents: 2000, counted_cents: 2300, difference_cents: 300 },
    { payment_method: 'yape', expected_cents: 0, counted_cents: 0, difference_cents: 0 },
    { payment_method: 'plin', expected_cents: 0, counted_cents: 0, difference_cents: 0 },
    { payment_method: 'card', expected_cents: 0, counted_cents: 0, difference_cents: 0 },
    { payment_method: 'other', expected_cents: 0, counted_cents: 0, difference_cents: 0 },
    { payment_method: 'fiado', expected_cents: 500, counted_cents: 0, difference_cents: null },
  ]
  const row: CashCloseRow = {
    id: 'close-1',
    closed_on: '2026-10-25',
    created_at: '2026-10-25T23:00:00.000Z',
    counts: JSON.stringify(counts),
    acknowledged: false,
  }

  await database.cash_closes.put(row)
}

/** Monta el dashboard con el reloj fijo en el día de la siembra. */
function renderDashboard(database: PosDatabase, options: { token?: string; fetchImpl?: typeof fetch; isOnline?: boolean } = {}) {
  vi.stubGlobal('navigator', { ...navigator, onLine: options.isOnline ?? true })

  return render(
    <AdminDashboardScreen
      database={database}
      fetchImpl={options.fetchImpl}
      now={() => new Date(2026, 9, 26, 20, 0, 0)}
      token={options.token}
    />,
  )
}

describe('AdminDashboardScreen', () => {
  it('renders the KPI row from the local ledger alone', async () => {
    const database = freshDatabase()
    await seedLedger(database)

    try {
      renderDashboard(database)

      // Ventas netas: 2000 + 1000 (pendiente, D-E) + 1500 de fiado − 500 de anulación.
      await waitFor(() =>
        expect(screen.getByTestId('dashboard-net-sales').textContent).toContain('S/ 40.00'),
      )
      // Tres tickets: la anulación no es uno. Promedio = 4000 / 3 ≈ 1333.
      expect(screen.getByTestId('dashboard-tickets').textContent).toContain('3')
      expect(screen.getByTestId('dashboard-average-ticket').textContent).toContain('S/ 13.33')
    } finally {
      database.close()
    }
  })

  it('shows the pending count as the unconfirmed part of the day', async () => {
    const database = freshDatabase()
    await seedLedger(database)

    try {
      renderDashboard(database)

      await waitFor(() =>
        expect(screen.getByTestId('dashboard-pending').textContent).toContain('1 venta por sincronizar'),
      )
    } finally {
      database.close()
    }
  })

  it('breaks the day down over the six tenders, marking fiado as informational', async () => {
    const database = freshDatabase()
    await seedLedger(database)

    try {
      renderDashboard(database)

      const rows = await screen.findAllByTestId(/^dashboard-tender-/)
      await waitFor(() =>
        expect(screen.getByTestId('dashboard-tender-cash').textContent).toContain('S/ 15.00'),
      )
      expect(rows.map((row) => row.getAttribute('data-method'))).toEqual([
        'cash',
        'yape',
        'plin',
        'card',
        'other',
        'fiado',
      ])
      expect(screen.getByTestId('dashboard-tender-yape').textContent).toContain('S/ 10.00')
      expect(screen.getByTestId('dashboard-tender-fiado').textContent).toContain('S/ 15.00')
      expect(screen.getByTestId('dashboard-tender-fiado').getAttribute('data-informational')).toBe('true')
    } finally {
      database.close()
    }
  })

  it('charts the day over the 24 local hours', async () => {
    const database = freshDatabase()
    await seedLedger(database)

    try {
      renderDashboard(database)

      const bars = await screen.findAllByTestId(/^dashboard-hour-/)
      expect(bars).toHaveLength(24)

      // 9h: 2000; 12h: 1000 (pendiente); 18h: 1500; 19h: −500.
      await waitFor(() =>
        expect(screen.getByTestId('dashboard-hour-9').getAttribute('aria-label')).toContain('S/ 20.00'),
      )
      expect(screen.getByTestId('dashboard-hour-19').getAttribute('aria-label')).toContain('-S/ 5.00')
      expect(screen.getByTestId('dashboard-hour-3').getAttribute('aria-label')).toContain('S/ 0.00')
    } finally {
      database.close()
    }
  })

  it('audits the local closes with their over or short, and fiado informational', async () => {
    const database = freshDatabase()
    await seedClose(database)

    try {
      renderDashboard(database)

      const audit = await screen.findByTestId('dashboard-close-2026-10-25')
      expect(audit.textContent).toContain('Sobrante')
      expect(audit.textContent).toContain('S/ 3.00')
      // El fiado del cierre no se suma como faltante.
      expect(audit.textContent).not.toContain('-S/')
    } finally {
      database.close()
    }
  })

  it('reads the live fiado debt from GET /fiado-accounts and nothing else', async () => {
    const database = freshDatabase()
    await seedLedger(database)
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) =>
      new Response(
        JSON.stringify({ accounts: [{ balance_cents: 1500 }, { balance_cents: -200 }] }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
    )

    try {
      renderDashboard(database, { token: 'jwt-del-dueno', fetchImpl: fetchSpy as unknown as typeof fetch })

      await waitFor(() =>
        expect(screen.getByTestId('dashboard-receivable').textContent).toContain('S/ 15.00'),
      )

      // Ninguna otra lectura: no existe endpoint de reporte ni de agregados (`ARCHITECTURE.md:138`).
      const urls = fetchSpy.mock.calls.map((call) => String(call[0]))
      expect(urls).toEqual(['/fiado-accounts'])
    } finally {
      database.close()
    }
  })

  it('says pendiente for the fiado KPI when there is no session, without a number', async () => {
    const database = freshDatabase()
    await seedLedger(database)

    try {
      renderDashboard(database, { isOnline: true })

      const card = await screen.findByTestId('dashboard-receivable')
      expect(card.textContent).toContain('pendiente')
      expect(card.textContent).not.toContain('S/')
      expect(within(card).queryByText(/^S\//)).toBeNull()
    } finally {
      database.close()
    }
  })

  it('reports an empty day without inventing numbers', async () => {
    const database = freshDatabase()

    try {
      renderDashboard(database)

      await waitFor(() => expect(screen.getByTestId('dashboard-net-sales').textContent).toContain('S/ 0.00'))
      expect(screen.getByTestId('dashboard-tickets').textContent).toContain('0')
      await waitFor(() =>
        expect(screen.getByTestId('dashboard-closes-empty').textContent).toContain('Todavía no hay cierres'),
      )
    } finally {
      database.close()
    }
  })
})
