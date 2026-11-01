import { getPosDatabase, type IdempotencyQueueRow, type PosDatabase } from '@/db/posDatabase'
import { notifyCartChanged } from '@/cart/cartStore'
import { isStorageQuotaError, StorageQuotaBlockedError } from '@/db/quota'
import { buildSaleQueueRow, notifySaleQueueChanged } from '@/sales/saleQueue'
import type { SaleClosePayload } from '@/sales/saleClose'

/**
 * Cola del fiado (W7-FE-2; docs/FRONTEND.md:51; docs/DATABASE.md §7.6).
 *
 * **El cargo a fiado desde el carrito son dos eventos.** La venta con
 * `payment_method = 'fiado'` es la que proyecta el «esperado fiado» del arqueo
 * (D11) y el movimiento positivo es el que mueve la deuda (§7.5). Los dos se
 * escriben en la MISMA transacción que vacía el carrito y cada uno lleva su
 * propia clave de idempotencia: repetir el drenaje no abre una segunda venta ni
 * cobra dos veces (decisión 9 del sprint 7).
 *
 * **El abono es un solo movimiento negativo.** El signo ES el tipo; la interfaz
 * pide un monto positivo en soles y este módulo lo firma al revés.
 *
 * Ningún saldo se guarda acá: lo que se escribe es el ledger local, y el saldo se
 * computa de él al leer (`accountBalanceCents`).
 */

/** Nota congelada del cargo que nace de una venta de mostrador (`docs/DESIGN.md:787`). */
export const FIADO_CHARGE_NOTE = 'Venta fiada'

/** Deudor elegido en el cobro. `isNew` es lo que habilita el alta en el servidor. */
export interface FiadoDebtor {
  readonly id: string
  readonly name: string
  readonly phone: string | null
  /**
   * `true` solo cuando el deudor se registra con este cargo. El cuerpo de
   * §4.11 no tiene un `POST /fiado-accounts`, así que el alta viaja en
   * `fiado_account` y se materializa con el upsert atómico del servidor (F12).
   * Un deudor ya conocido NO lo reenvía: el upsert no debe pisar el nombre con
   * la marca de este movimiento.
   */
  readonly isNew: boolean
}

/** Entrada del cargo a fiado: la venta ya congelada más el deudor elegido. */
export interface EnqueueFiadoChargeInput {
  readonly sale: SaleClosePayload
  /** Clave de idempotencia de la venta. */
  readonly saleKey: string
  readonly debtor: FiadoDebtor
  /** Clave de idempotencia del movimiento; en producción nace por cargo. */
  readonly movementKey?: string
  /** Id del movimiento; en producción nace por cargo. */
  readonly movementId?: string
  /** Reloj, inyectable para las pruebas. */
  readonly now?: () => Date
}

/** Entrada del abono parcial. `amountCents` es POSITIVO: la firma la pone este módulo. */
export interface EnqueueFiadoPaymentInput {
  readonly accountId: string
  readonly amountCents: number
  readonly note?: string
  readonly movementKey?: string
  readonly movementId?: string
  readonly now?: () => Date
}

/**
 * Cuerpo del movimiento tal como lo pide §4.11. Es la forma congelada por la
 * decisión 2 del sprint 7: `fiado_account` solo viaja en el alta, los opcionales
 * se omiten cuando no existen, y el enlace a la venta viaja como par.
 */
function movementPayload(input: {
  readonly id: string
  readonly accountId: string
  readonly amountCents: number
  readonly note: string | null
  readonly soldOn: string | null
  readonly saleId: string | null
  readonly account?: { readonly name: string; readonly phone: string | null }
}): Record<string, unknown> {
  const body: Record<string, unknown> = {
    id: input.id,
    fiado_account_id: input.accountId,
    amount_cents: input.amountCents,
  }

  if (input.account !== undefined) {
    body.fiado_account = { name: input.account.name, phone: input.account.phone }
  }
  if (input.note !== null) body.note = input.note
  if (input.soldOn !== null && input.saleId !== null) {
    body.sold_on = input.soldOn
    body.sale_id = input.saleId
  }

  return body
}

/**
 * Encola el cargo a fiado: la venta y el movimiento firmado, más el alta local
 * del deudor y su movimiento en el ledger del dispositivo, en UNA transacción con
 * el vaciado del carrito. Si la cuota del dispositivo está agotada, la venta se
 * bloquea con su motivo visible y ni la cola ni el carrito se tocan (W6-FE-1).
 */
export async function enqueueFiadoCharge(
  input: EnqueueFiadoChargeInput,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  const createdAt = (input.now?.() ?? new Date()).toISOString()
  const movementKey = input.movementKey ?? crypto.randomUUID()
  const movementId = input.movementId ?? crypto.randomUUID()
  const amountCents = input.sale.total_cents

  const saleRow = buildSaleQueueRow(input.sale, input.saleKey, createdAt)
  const movementRow: IdempotencyQueueRow = {
    id: crypto.randomUUID(),
    key: movementKey,
    // El alta del deudor viaja SOLO cuando es nueva (decisión 2 del sprint 7): un
    // deudor ya conocido no reenvía el nombre, así el upsert no lo pisa con la
    // marca de este movimiento.
    payload: JSON.stringify(
      movementPayload({
        id: movementId,
        accountId: input.debtor.id,
        amountCents,
        note: FIADO_CHARGE_NOTE,
        soldOn: input.sale.sold_on,
        saleId: input.sale.sale_id,
        account: input.debtor.isNew
          ? { name: input.debtor.name, phone: input.debtor.phone }
          : undefined,
      }),
    ),
    status: 'pending',
    created_at: createdAt,
    operation: 'fiado_movement',
    business_date: input.sale.sold_on,
    sale_id: input.sale.sale_id,
    payment_method: null,
    total_cents: amountCents,
    last_error: null,
  }

  try {
    await database.transaction(
      'rw',
      [database.idempotency_queue, database.cart, database.fiado_accounts, database.fiado_movements],
      async () => {
        await database.idempotency_queue.bulkAdd([saleRow, movementRow])

        if (input.debtor.isNew) {
          const existing = await database.fiado_accounts.get(input.debtor.id)
          if (existing === undefined) {
            await database.fiado_accounts.add({
              id: input.debtor.id,
              name: input.debtor.name,
              phone: input.debtor.phone,
              created_at: createdAt,
              updated_at: createdAt,
            })
          }
        }

        await database.fiado_movements.put({
          id: movementId,
          fiado_account_id: input.debtor.id,
          amount_cents: amountCents,
          note: FIADO_CHARGE_NOTE,
          sold_on: input.sale.sold_on,
          sale_id: input.sale.sale_id,
          created_at: createdAt,
        })

        await database.cart.clear()
      },
    )
  } catch (error) {
    if (isStorageQuotaError(error)) throw new StorageQuotaBlockedError()

    throw error
  }

  notifySaleQueueChanged()
  notifyCartChanged()
}

/**
 * Encola un abono parcial contra una cuenta existente. El movimiento viaja
 * NEGATIVO porque el signo es el tipo, y NO lleva `fiado_account`: la cuenta ya
 * está registrada y el upsert no tiene nada que crear.
 *
 * El tender del formulario (Efectivo / transferencia) es solo de interfaz: el
 * ledger no tiene columna de medio de pago y el contrato no la transporta
 * (decisión 6 del sprint 7). No se inventa una columna ni se dobla el medio
 * dentro de la nota.
 */
export async function enqueueFiadoPayment(
  input: EnqueueFiadoPaymentInput,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  if (!Number.isInteger(input.amountCents) || input.amountCents <= 0) {
    throw new Error('Un abono necesita un monto positivo en centavos')
  }

  const createdAt = (input.now?.() ?? new Date()).toISOString()
  const movementKey = input.movementKey ?? crypto.randomUUID()
  const movementId = input.movementId ?? crypto.randomUUID()
  const amountCents = -input.amountCents
  const note = input.note?.trim() === '' ? null : (input.note?.trim() ?? null)

  const row: IdempotencyQueueRow = {
    id: crypto.randomUUID(),
    key: movementKey,
    payload: JSON.stringify(
      movementPayload({
        id: movementId,
        accountId: input.accountId,
        amountCents,
        note,
        soldOn: null,
        saleId: null,
      }),
    ),
    status: 'pending',
    created_at: createdAt,
    operation: 'fiado_movement',
    business_date: createdAt.slice(0, 10),
    sale_id: null,
    payment_method: null,
    total_cents: input.amountCents,
    last_error: null,
  }

  try {
    await database.transaction(
      'rw',
      [database.idempotency_queue, database.fiado_movements],
      async () => {
        await database.idempotency_queue.add(row)
        await database.fiado_movements.put({
          id: movementId,
          fiado_account_id: input.accountId,
          amount_cents: amountCents,
          note,
          sold_on: null,
          sale_id: null,
          created_at: createdAt,
        })
      },
    )
  } catch (error) {
    if (isStorageQuotaError(error)) throw new StorageQuotaBlockedError()

    throw error
  }

  notifySaleQueueChanged()
}
