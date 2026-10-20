// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type IdempotencyQueueRow } from '@/db/posDatabase'
import {
  CASH_CLOSE_METHODS,
  countLine,
  persistCashClose,
  type CashCloseAck,
} from '@/sales/cashClose'
import { CashCloseScreen } from '@/screens/CashCloseScreen'

/*
 * Arqueo y cierre de caja (W5-FE-2; SPRINTS.md:266; docs/DESIGN.md §3.2). Seis
 * filas en el orden fijo del CHECK, con el esperado proyectado del ledger LOCAL
 * de esa fecha de negocio, el contado que tipea el cajero y la diferencia
 * computada por fila. El fiado va bloqueado en cero y su diferencia es
 * informacional. El acuse del servidor manda: reemplaza el esperado local.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

const CLOSED_ON = '2026-10-20'
/** Reloj fijo: el arqueo de una fecha de negocio concreta. */
const NOW = (): Date => new Date(2026, 9, 20, 23, 10)

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-cash-close-screen-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

/** Venta del ledger local con la proyección congelada. */
function saleRow(input: {
  readonly id: string
  readonly method: IdempotencyQueueRow['payment_method']
  readonly cents: number
  readonly date?: string
}): IdempotencyQueueRow {
  return {
    id: input.id,
    key: `clave-${input.id}`,
    payload: '{}',
    status: 'pending',
    created_at: '2026-10-20T10:00:00.000Z',
    operation: 'sale',
    business_date: input.date ?? CLOSED_ON,
    sale_id: input.id,
    payment_method: input.method,
    total_cents: input.cents,
    last_error: null,
  }
}

/** Acuse del contrato con las seis filas en orden. */
function ackFor(
  id: string,
  expected: Partial<Record<(typeof CASH_CLOSE_METHODS)[number], number>> = {},
  counted: Partial<Record<(typeof CASH_CLOSE_METHODS)[number], number>> = {},
): CashCloseAck {
  return {
    cash_close: {
      id,
      tenant_id: 'tenant-1',
      closed_on: CLOSED_ON,
      closed_by_user_id: 'user-1',
      created_at: '2026-10-20T23:11:00.000Z',
      received_at: '2026-10-20T23:11:00.000Z',
    },
    counts: CASH_CLOSE_METHODS.map((method) => {
      const expectedCents = expected[method] ?? 0
      const countedCents = counted[method] ?? 0

      return {
        payment_method: method,
        expected_cents: expectedCents,
        counted_cents: countedCents,
        difference_cents: method === 'fiado' ? null : countedCents - expectedCents,
      }
    }),
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

/** Tipea dígitos de centavos en el numpad compartido. */
function typeCents(digits: string): void {
  for (const digit of digits) {
    fireEvent.click(screen.getByRole('button', { name: digit }))
  }
}

describe('CashCloseScreen', () => {
  it('shows the six rows in the fixed order of the CHECK with their Spanish labels', async () => {
    const database = freshDatabase()

    render(<CashCloseScreen database={database} now={NOW} token="token" />)

    const rows = await screen.findAllByTestId(/^cash-close-row-/)

    expect(rows.map((row) => row.dataset.method)).toEqual([
      'cash',
      'yape',
      'plin',
      'card',
      'other',
      'fiado',
    ])
    expect(rows.map((row) => row.dataset.label)).toEqual([
      'Efectivo',
      'Yape',
      'Plin',
      'Tarjeta',
      'Otro',
      'Fiado',
    ])

    database.close()
  })

  it('locks the fiado row at zero and renders its difference as informational', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      saleRow({ id: 'fiado-1', method: 'fiado', cents: 4500 }),
    ])

    render(<CashCloseScreen database={database} now={NOW} token="token" />)

    const row = await screen.findByTestId('cash-close-row-fiado')

    // El fiado no se edita: no hay botón que lo seleccione y la fila lo declara.
    expect(row.getAttribute('aria-disabled')).toBe('true')
    expect(within(row).queryByRole('button')).toBeNull()

    // Su esperado sí se proyecta —lo fiado se vendió—, pero el contado queda
    // bloqueado en cero y la diferencia no es un faltante.
    expect(screen.getByTestId('cash-close-expected-fiado').textContent).toBe('S/ 45.00')
    expect(screen.getByTestId('cash-close-counted-fiado').textContent).toBe('S/ 0.00')
    expect(screen.getByTestId('cash-close-difference-fiado').textContent).toBe('Informativo')

    // Y no entra al resumen de sobrante/faltante.
    expect(screen.getByTestId('cash-close-shortage').textContent).toBe('S/ 0.00')

    database.close()
  })

  it('computes each difference as counted minus expected and summarizes them', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      saleRow({ id: 'efectivo-1', method: 'cash', cents: 1000 }),
      saleRow({ id: 'yape-1', method: 'yape', cents: 3000 }),
    ])

    render(<CashCloseScreen database={database} now={NOW} token="token" />)
    await waitFor(() =>
      expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 10.00'),
    )

    // Efectivo: se cuentan S/ 15.00 contra S/ 10.00 de esperado.
    typeCents('1500')

    expect(screen.getByTestId('cash-close-counted-cash').textContent).toBe('S/ 15.00')
    expect(screen.getByTestId('cash-close-difference-cash').textContent).toBe('+S/ 5.00')

    // Yape: se cuentan S/ 28.00 contra S/ 30.00 de esperado.
    fireEvent.click(
      within(screen.getByTestId('cash-close-row-yape')).getByRole('button', { name: /Yape/ }),
    )
    typeCents('2800')

    expect(screen.getByTestId('cash-close-difference-yape').textContent).toBe('-S/ 2.00')
    expect(screen.getByTestId('cash-close-surplus').textContent).toBe('S/ 5.00')
    expect(screen.getByTestId('cash-close-shortage').textContent).toBe('S/ 2.00')

    database.close()
  })

  it('projects the expectation from the sales of that business date only', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      // La venta de ayer NO entra al arqueo de hoy.
      saleRow({ id: 'ayer', method: 'cash', cents: 9999, date: '2026-10-19' }),
      saleRow({ id: 'hoy', method: 'plin', cents: 2000 }),
    ])

    render(<CashCloseScreen database={database} now={NOW} token="token" />)

    await waitFor(() =>
      expect(screen.getByTestId('cash-close-expected-plin').textContent).toBe('S/ 20.00'),
    )
    expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 0.00')

    database.close()
  })

  it('sends the exact contract body with the six counts and the idempotency key', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      saleRow({ id: 'efectivo-1', method: 'cash', cents: 1000 }),
    ])
    const requests: { url: string; init: RequestInit }[] = []
    const fetchImpl = vi.fn(async (url: string, init: RequestInit) => {
      requests.push({ url, init })

      return jsonResponse(200, ackFor(JSON.parse(String(init.body)).id))
    })

    render(
      <CashCloseScreen
        database={database}
        now={NOW}
        token="token-de-la-sesion"
        fetchImpl={fetchImpl as unknown as typeof fetch}
      />,
    )
    await waitFor(() =>
      expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 10.00'),
    )

    typeCents('1500')
    fireEvent.click(screen.getByTestId('cash-close-submit'))

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(1))

    const [request] = requests
    const body = JSON.parse(String(request?.init.body))

    expect(request?.url).toBe('/cash-closes')
    expect((request?.init.headers as Record<string, string>)['idempotency-key']).toBe(body.id)
    expect(body).toEqual({
      id: expect.any(String),
      closed_on: CLOSED_ON,
      counts: [
        { payment_method: 'cash', counted_cents: 1500 },
        { payment_method: 'yape', counted_cents: 0 },
        { payment_method: 'plin', counted_cents: 0 },
        { payment_method: 'card', counted_cents: 0 },
        { payment_method: 'other', counted_cents: 0 },
        { payment_method: 'fiado', counted_cents: 0 },
      ],
    })

    database.close()
  })

  it('replaces the local expectation with the frozen server ack', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      saleRow({ id: 'efectivo-1', method: 'cash', cents: 1000 }),
    ])
    const fetchImpl = vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body))

      // El servidor congela su propio esperado: S/ 99.99, distinto del local.
      return jsonResponse(200, ackFor(body.id, { cash: 9999 }, { cash: 1500 }))
    })

    render(
      <CashCloseScreen
        database={database}
        now={NOW}
        token="token"
        fetchImpl={fetchImpl as unknown as typeof fetch}
      />,
    )
    await waitFor(() =>
      expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 10.00'),
    )

    typeCents('1500')
    fireEvent.click(screen.getByTestId('cash-close-submit'))

    await waitFor(() =>
      expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 99.99'),
    )
    expect(screen.getByTestId('cash-close-counted-cash').textContent).toBe('S/ 15.00')
    expect(screen.getByTestId('cash-close-difference-cash').textContent).toBe('-S/ 84.99')
    expect(screen.getByTestId('cash-close-delivery').textContent).toContain('confirmó el cierre')

    database.close()
  })

  it('renders the existing close of the date with a non-modal notice on a duplicate', async () => {
    const database = freshDatabase()
    await database.idempotency_queue.bulkAdd([
      saleRow({ id: 'efectivo-1', method: 'cash', cents: 1000 }),
    ])
    const fetchImpl = vi.fn(async () =>
      jsonResponse(
        200,
        // Otra fila: la fecha ya tenía cierre y ese cierre prevalece (F38).
        ackFor('cierre-que-ya-existia', { cash: 1200 }, { cash: 1200 }),
      ),
    )

    render(
      <CashCloseScreen
        database={database}
        now={NOW}
        token="token"
        fetchImpl={fetchImpl as unknown as typeof fetch}
      />,
    )
    await waitFor(() =>
      expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 10.00'),
    )

    typeCents('1100')
    fireEvent.click(screen.getByTestId('cash-close-submit'))

    const notice = await screen.findByTestId('cash-close-duplicate-notice')

    expect(notice.getAttribute('role')).toBe('status')
    expect(notice.textContent).toContain('ya estaba registrado')
    // Nunca un error: ni alerta ni mensaje de rechazo.
    expect(screen.queryByRole('alert')).toBeNull()
    // Se muestra el cierre guardado, no lo tipeado.
    expect(screen.getByTestId('cash-close-counted-cash').textContent).toBe('S/ 12.00')
    expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 12.00')

    database.close()
  })

  it('shows a stored close as read-only instead of an editable form (F37)', async () => {
    const database = freshDatabase()

    await persistCashClose(
      { id: 'cierre-guardado', closed_on: CLOSED_ON, counts: [] },
      [countLine('cash', 1000, 1250), countLine('fiado', 0, 0)],
      database,
    )

    render(<CashCloseScreen database={database} now={NOW} token="token" />)

    expect(await screen.findByTestId('cash-close-readonly')).toBeTruthy()
    expect(screen.queryByTestId('cash-close-submit')).toBeNull()
    expect(screen.queryByTestId('numpad-grid')).toBeNull()
    expect(screen.getByTestId('cash-close-expected-cash').textContent).toBe('S/ 10.00')
    expect(screen.getByTestId('cash-close-counted-cash').textContent).toBe('S/ 12.50')
    expect(screen.getByTestId('cash-close-difference-cash').textContent).toBe('+S/ 2.50')

    database.close()
  })

  it('keeps the local close and says it was not confirmed when the network is down', async () => {
    const database = freshDatabase()
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('red caída')
    })

    render(
      <CashCloseScreen
        database={database}
        now={NOW}
        token="token"
        fetchImpl={fetchImpl as unknown as typeof fetch}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('cash-close-submit')).toBeTruthy())

    fireEvent.click(screen.getByTestId('cash-close-submit'))

    // El texto de entrega existe desde el primer render; se espera su verdad.
    await waitFor(() =>
      expect(screen.getByTestId('cash-close-delivery').textContent).toContain('no se pudo confirmar'),
    )
    // El cierre existe en el dispositivo y lo dice; sin fingir acuse.
    const stored = await database.cash_closes.get(CLOSED_ON)

    expect(stored?.acknowledged).toBe(false)
    expect(screen.getByTestId('cash-close-readonly')).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()

    database.close()
  })

  it('keeps the CTA disabled without an online session', async () => {
    const database = freshDatabase()

    render(<CashCloseScreen database={database} now={NOW} />)

    const cta = await screen.findByTestId('cash-close-submit')

    expect((cta as HTMLButtonElement).disabled).toBe(true)
    expect(cta.textContent).toBe('CERRAR CAJA')
    expect(screen.getByTestId('cash-close-no-session').textContent).toContain(
      'Sin sesión en línea',
    )

    database.close()
  })
})
