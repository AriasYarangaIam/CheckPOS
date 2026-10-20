import { describe, expect, it, vi } from 'vitest'

import type { CartRow } from '@/db/posDatabase'
import { formatBusinessDate, buildSaleClosePayload, submitSaleClose } from '@/sales/saleClose'
import { TENDER_METHODS } from '@/sales/tenderMethods'

/*
 * Cierre de venta del cliente (W4-FE-2; docs/DATABASE.md §4.9, §2.3; SPRINTS.md
 * W4-FE-2). El payload congelado sale del carrito tal cual: el servidor NUNCA
 * recomputa dinero, así que el redondeo half-up por línea y la suma del total
 * son de este lado.
 *
 * El POST no está en el camino crítico del cobro (ARCHITECTURE.md:98): la venta
 * cierra en el dispositivo y el acuse se reclama con la rama del claim 200 /
 * 409 + Retry-After / 422.
 */

const ARROZ: CartRow = {
  id: 'l1',
  product_id: 'a1-uuid',
  qty: '2.750',
  unit_price_cents: 989,
  name: 'Arroz Costeño 1kg',
  uom: 'kg',
  voided: false,
}

const PAN: CartRow = {
  id: 'l2',
  product_id: 'p2-uuid',
  qty: '2',
  unit_price_cents: 30,
  name: 'Pan francés',
  uom: 'unit',
  voided: false,
}

const ANULADA: CartRow = { ...PAN, id: 'l3', voided: true }

describe('buildSaleClosePayload', () => {
  it('freezes the lines exactly as the §4.9 body asks, numbered and half-up rounded', () => {
    const payload = buildSaleClosePayload([ARROZ, PAN], {
      paymentMethod: 'cash',
      saleId: '0a300000-0000-7000-8000-000000000001',
    })

    expect(payload).toEqual({
      sale_id: '0a300000-0000-7000-8000-000000000001',
      sold_on: formatBusinessDate(new Date()),
      payment_method: 'cash',
      // Σ line_total: 2.750×989 → 2720 (half-up) + 2×30 → 60.
      total_cents: 2780,
      lines: [
        {
          line_no: 1,
          product_id: 'a1-uuid',
          name: 'Arroz Costeño 1kg',
          price_cents: 989,
          uom: 'kg',
          qty: '2.750',
          line_total_cents: 2720,
        },
        {
          line_no: 2,
          product_id: 'p2-uuid',
          name: 'Pan francés',
          price_cents: 30,
          uom: 'unit',
          qty: '2',
          line_total_cents: 60,
        },
      ],
    })
  })

  it('never carries an annulled line into the sale', () => {
    const payload = buildSaleClosePayload([ARROZ, ANULADA], {
      paymentMethod: 'cash',
      saleId: '0a300000-0000-7000-8000-000000000002',
    })

    expect(payload.lines).toHaveLength(1)
    expect(payload.total_cents).toBe(2720)
  })

  it('fixes the business date of the close, not the sync date', () => {
    // La fecha de negocio es LOCAL al mostrador: 2026-01-02 de madrugada
    // sigue siendo 2026-01-02, no el UTC del día anterior.
    expect(formatBusinessDate(new Date(2026, 0, 2, 5, 30))).toBe('2026-01-02')
  })
})

describe('tender methods', () => {
  it('is exactly the six values of the CHECK', () => {
    expect(TENDER_METHODS).toEqual(['cash', 'yape', 'plin', 'card', 'other', 'fiado'])
  })
})

describe('submitSaleClose', () => {
  const payload = buildSaleClosePayload([PAN], {
    paymentMethod: 'yape',
    saleId: '0a300000-0000-7000-8000-000000000003',
  })

  function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json', ...headers },
    })
  }

  it('issues the POST with the Idempotency-Key header and the frozen body', async () => {
    const fetchImpl = vi.fn(async () => jsonResponse(200, { sale: { sale_id: payload.sale_id } }))

    const outcome = await submitSaleClose({
      token: 'token-de-la-sesion',
      payload,
      fetchImpl: fetchImpl as unknown as typeof fetch,
    })

    expect(outcome.status).toBe('acknowledged')
    expect(fetchImpl).toHaveBeenCalledTimes(1)

    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]

    expect(url).toBe('/sales')
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>)['authorization']).toBe(
      'Bearer token-de-la-sesion',
    )
    // La clave nace por venta: uuid del cliente (la reclama el servidor con la
    // UNIQUE de idempotency_keys).
    expect((init.headers as Record<string, string>)['idempotency-key']).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    )
    expect(JSON.parse(init.body as string)).toEqual(payload)
    expect(JSON.parse(init.body as string).payment_method).toBe('yape')
  })

  it('accepts the replay answer as the acknowledgement, byte for byte', async () => {
    // Branch A: la misma clave repetida devuelve el snapshot almacenado con
    // 200. Para el cliente es un acuse normal; nada se volvió a cerrar.
    const snapshot = { sale: { sale_id: payload.sale_id, total_cents: 60 } }
    const fetchImpl = vi.fn(async () => jsonResponse(200, snapshot))

    const outcome = await submitSaleClose({
      token: 'token',
      payload,
      fetchImpl: fetchImpl as unknown as typeof fetch,
      key: 'clave-repetida',
    })

    expect(outcome.status).toBe('acknowledged')
    if (outcome.status === 'acknowledged') {
      expect(outcome.ack).toEqual(snapshot)
    }
  })

  it('surfaces the 422 payload mismatch as an honest failure, not a success', async () => {
    const fetchImpl = vi.fn(async () =>
      jsonResponse(422, {
        error: {
          code: 'idempotency.payload_mismatch',
          message: 'the same idempotency key was already used with a different payload',
          details: { field: 'Idempotency-Key' },
        },
      }),
    )

    const outcome = await submitSaleClose({
      token: 'token',
      payload,
      fetchImpl: fetchImpl as unknown as typeof fetch,
    })

    expect(outcome.status).toBe('mismatch')
    if (outcome.status === 'mismatch') {
      expect(outcome.error.code).toBe('idempotency.payload_mismatch')
    }
  })

  it('surfaces another 422 as a rejected body with the code of the envelope', async () => {
    const fetchImpl = vi.fn(async () =>
      jsonResponse(422, {
        error: { code: 'sale.invalid_body', message: 'payment_method must be one of ...' },
      }),
    )

    const outcome = await submitSaleClose({
      token: 'token',
      payload: { ...payload, payment_method: 'cash' },
      fetchImpl: fetchImpl as unknown as typeof fetch,
    })

    expect(outcome.status).toBe('rejected')
    if (outcome.status === 'rejected') {
      expect(outcome.error.code).toBe('sale.invalid_body')
    }
  })

  it('waits the Retry-After of the 409 and retries the same claim', async () => {
    // Branch C: el ganador sigue en vuelo — esperar y SOLO reintentar con la
    // misma clave y el mismo cuerpo, jamás abrir una segunda venta.
    const responses: Array<() => Response> = [
      () => jsonResponse(409, { error: { code: 'CONFLICT', message: 'still in flight' } }, { 'retry-after': '2' }),
      () => jsonResponse(200, { sale: { sale_id: payload.sale_id } }),
    ]
    let attempt = 0
    const fetchImpl = vi.fn(async () => responses[attempt++]?.() ?? jsonResponse(409, {})) as unknown as typeof fetch

    const sleep = vi.fn(async (_ms: number) => undefined)

    const outcome = await submitSaleClose({
      token: 'token',
      payload,
      fetchImpl,
      sleep,
      key: 'clave-en-vuelo',
    })

    expect(outcome.status).toBe('acknowledged')
    expect(sleep).toHaveBeenCalledWith(2000)
    // Dos intentos, mismos encabezados y cuerpo: un solo claim.
    expect(fetchImpl).toHaveBeenCalledTimes(2)
  })

  it('reports an unconfirmed delivery when the conflict outlasts the retries', async () => {
    const fetchImpl = vi.fn(async () =>
      jsonResponse(409, { error: { code: 'CONFLICT', message: 'still in flight' } }, { 'retry-after': '2' }),
    ) as unknown as typeof fetch

    const outcome = await submitSaleClose({
      token: 'token',
      payload,
      fetchImpl,
      sleep: async () => undefined,
    })

    expect(outcome.status).toBe('unconfirmed')
  })

  it('reports an unconfirmed delivery when the network is down, without faking success', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    }) as unknown as typeof fetch

    const outcome = await submitSaleClose({
      token: 'token',
      payload,
      fetchImpl,
    })

    expect(outcome.status).toBe('unconfirmed')
  })
})
