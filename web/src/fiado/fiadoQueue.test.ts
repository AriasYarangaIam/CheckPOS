// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { afterEach, describe, expect, it } from 'vitest'

import { POS_DATABASE_NAME, PosDatabase, type CartRow } from '@/db/posDatabase'
import { enqueueFiadoCharge, enqueueFiadoPayment } from '@/fiado/fiadoQueue'
import { accountBalanceCents } from '@/fiado/fiadoStore'
import type { SaleClosePayload } from '@/sales/saleClose'

/*
 * Cola del fiado (W7-FE-2; docs/FRONTEND.md:51; decisiones 8 y 9 del sprint 7).
 *
 * El cargo a fiado desde el carrito es DOS eventos con claves distintas —la
 * venta, que proyecta el esperado del arqueo por D11, y el movimiento positivo,
 * que mueve la deuda— escritos en la MISMA transacción que vacía el carrito. El
 * abono es un solo movimiento NEGATIVO. Ningún saldo se guarda: el saldo local
 * es la suma del ledger local.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-fiado-queue-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

const CART_LINES: readonly CartRow[] = [
  {
    id: 'line-1',
    product_id: 'prod-pan',
    qty: '2',
    unit_price_cents: 750,
    name: 'Pan francés',
    uom: 'unit',
    voided: false,
  },
]

const SALE: SaleClosePayload = {
  sale_id: '0a700000-0000-7000-8000-000000000001',
  sold_on: '2026-10-26',
  payment_method: 'fiado',
  total_cents: 1500,
  lines: [
    {
      line_no: 1,
      product_id: 'prod-pan',
      name: 'Pan francés',
      price_cents: 750,
      uom: 'unit',
      qty: '2',
      line_total_cents: 1500,
    },
  ],
}

const NEW_DEBTOR = {
  id: '0a700000-0000-7000-8000-000000000002',
  name: 'Rosa Quispe',
  phone: '+51987111222',
  isNew: true,
} as const

const KNOWN_DEBTOR = {
  id: '0a700000-0000-7000-8000-000000000002',
  name: 'Rosa Quispe',
  phone: '+51987111222',
  isNew: false,
} as const

describe('el cargo a fiado desde el carrito', () => {
  it('encola la venta y el movimiento firmado en la misma transacción que vacía el carrito', async () => {
    const database = freshDatabase()

    try {
      await database.cart.bulkAdd(CART_LINES)

      await enqueueFiadoCharge(
        {
          sale: SALE,
          saleKey: 'sale-key-1',
          debtor: NEW_DEBTOR,
          movementKey: 'movement-key-1',
          now: () => new Date('2026-10-26T12:00:00.000Z'),
        },
        database,
      )

      const queue = await database.idempotency_queue.toArray()
      expect(queue.map((row) => row.operation).sort()).toEqual(['fiado_movement', 'sale'])

      // El carrito se vació en la misma transacción: no hay un estado intermedio
      // donde la venta exista sin evento o el evento sin carrito vacío.
      expect(await database.cart.count()).toBe(0)

      // El saldo local del deudor sube por el cargo, computado del ledger local.
      const movements = await database.fiado_movements.toArray()
      expect(movements.map((movement) => movement.amount_cents)).toEqual([1500])
      expect(accountBalanceCents(movements)).toBe(1500)
      expect(movements[0].sale_id).toBe(SALE.sale_id)
      expect(movements[0].sold_on).toBe(SALE.sold_on)
    } finally {
      database.close()
    }
  })

  it('registra al deudor nuevo y lo envía en el cuerpo, porque no hay POST /fiado-accounts', async () => {
    const database = freshDatabase()

    try {
      await enqueueFiadoCharge(
        { sale: SALE, saleKey: 'sale-key-1', debtor: NEW_DEBTOR, movementKey: 'movement-key-1' },
        database,
      )

      const account = await database.fiado_accounts.get(NEW_DEBTOR.id)
      expect(account?.name).toBe('Rosa Quispe')
      expect(account?.phone).toBe('+51987111222')

      const queued = await database.idempotency_queue.where('key').equals('movement-key-1').first()
      const body = JSON.parse(queued?.payload ?? '{}') as {
        fiado_account_id?: string
        fiado_account?: { name?: string; phone?: string }
        amount_cents?: number
        sold_on?: string
        sale_id?: string
      }

      expect(body.fiado_account_id).toBe(NEW_DEBTOR.id)
      expect(body.fiado_account).toEqual({ name: 'Rosa Quispe', phone: '+51987111222' })
      expect(body.amount_cents).toBe(1500)
      expect(body.sold_on).toBe('2026-10-26')
      expect(body.sale_id).toBe(SALE.sale_id)
    } finally {
      database.close()
    }
  })

  it('no reenvía el registro de un deudor ya conocido: el upsert no debe pisar el nombre', async () => {
    const database = freshDatabase()

    try {
      await enqueueFiadoCharge(
        { sale: SALE, saleKey: 'sale-key-1', debtor: KNOWN_DEBTOR, movementKey: 'movement-key-1' },
        database,
      )

      const queued = await database.idempotency_queue.where('key').equals('movement-key-1').first()
      const body = JSON.parse(queued?.payload ?? '{}') as { fiado_account?: unknown }

      expect(body.fiado_account).toBeUndefined()
    } finally {
      database.close()
    }
  })

  it('congela la proyección de la venta y la del movimiento con claves distintas', async () => {
    const database = freshDatabase()

    try {
      await enqueueFiadoCharge(
        {
          sale: SALE,
          saleKey: 'sale-key-1',
          debtor: NEW_DEBTOR,
          movementKey: 'movement-key-1',
          now: () => new Date('2026-10-26T12:00:00.000Z'),
        },
        database,
      )

      const queue = await database.idempotency_queue.toArray()
      const keys = queue.map((row) => row.key).sort()
      expect(keys).toEqual(['movement-key-1', 'sale-key-1'])

      const sale = queue.find((row) => row.operation === 'sale')
      const movement = queue.find((row) => row.operation === 'fiado_movement')
      expect(sale?.payment_method).toBe('fiado')
      expect(sale?.total_cents).toBe(1500)
      expect(movement?.total_cents).toBe(1500)
      expect(movement?.sale_id).toBe(SALE.sale_id)
    } finally {
      database.close()
    }
  })
})

describe('el abono parcial', () => {
  it('encola un movimiento NEGATIVO: el signo es el tipo', async () => {
    const database = freshDatabase()

    try {
      await enqueueFiadoPayment(
        { accountId: NEW_DEBTOR.id, amountCents: 500, movementKey: 'payment-key-1' },
        database,
      )

      const movements = await database.fiado_movements.toArray()
      expect(movements.map((movement) => movement.amount_cents)).toEqual([-500])
      expect(accountBalanceCents(movements)).toBe(-500)

      const queued = await database.idempotency_queue.where('key').equals('payment-key-1').first()
      expect(queued?.operation).toBe('fiado_movement')
      const body = JSON.parse(queued?.payload ?? '{}') as {
        amount_cents?: number
        fiado_account?: unknown
      }
      expect(body.amount_cents).toBe(-500)
      // El abono no trae registro de deudor: la cuenta ya existe.
      expect(body.fiado_account).toBeUndefined()
    } finally {
      database.close()
    }
  })

  it('amortiza la deuda del cargo previo', async () => {
    const database = freshDatabase()

    try {
      await enqueueFiadoCharge(
        { sale: SALE, saleKey: 'sale-key-1', debtor: NEW_DEBTOR, movementKey: 'movement-key-1' },
        database,
      )
      await enqueueFiadoPayment(
        { accountId: NEW_DEBTOR.id, amountCents: 500, movementKey: 'payment-key-1' },
        database,
      )

      const movements = await database.fiado_movements.toArray()
      expect(accountBalanceCents(movements)).toBe(1000)
    } finally {
      database.close()
    }
  })

  it('rechaza un monto no positivo sin escribir nada', async () => {
    const database = freshDatabase()

    try {
      await expect(
        enqueueFiadoPayment(
          { accountId: NEW_DEBTOR.id, amountCents: 0, movementKey: 'payment-key-0' },
          database,
        ),
      ).rejects.toThrow()

      expect(await database.idempotency_queue.count()).toBe(0)
      expect(await database.fiado_movements.count()).toBe(0)
    } finally {
      database.close()
    }
  })
})
