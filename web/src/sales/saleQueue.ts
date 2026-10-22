import { getPosDatabase, type IdempotencyQueueRow, type PosDatabase, type QueueStatus } from '@/db/posDatabase'
import { notifyCartChanged } from '@/cart/cartStore'
import type { SaleClosePayload } from '@/sales/saleClose'

/**
 * Cola local de la venta cerrada (W5-FE-1; SPRINTS.md:265; ARCHITECTURE.md:168).
 *
 * **La venta se escribe antes del ack.** Hasta el Sprint 4 el cierre vaciaba el
 * carrito y recién después disparaba el POST: matar la pestaña en ese hueco
 * perdía el evento, porque la cola de IndexedDB no tenía un solo consumidor. Acá
 * la fila `pending` y el carrito vacío se escriben en UNA transacción de Dexie,
 * así que el crash no puede dejar ni un evento a medias ni un carrito vaciado
 * sin evento.
 *
 * El estado de la fila es la verdad de la entrega: `completed` solo cuando el
 * servidor acusó, `failed` cuando el sobre trajo un código (`last_error`), y
 * `pending` mientras la red, el conflicto en vuelo o la falta de sesión dejen el
 * envío sin confirmar. El envío NUNCA finge éxito (W4-FE-2 conserva su forma).
 */

type SaleQueueListener = () => void

const listeners = new Set<SaleQueueListener>()

/** Suscripción al cambio de la cola, con la forma de `subscribeToCart`. */
export function subscribeToSaleQueue(listener: SaleQueueListener): () => void {
  listeners.add(listener)

  return () => {
    listeners.delete(listener)
  }
}

/** Notifica el cambio a quien relea la cola (badge de pendientes, hook). */
function notifySaleQueueChanged(): void {
  for (const listener of listeners) listener()
}

/**
 * Escribe la venta cerrada en la cola y vacía el carrito en la MISMA
 * transacción. Las dos tiendas se declaran explícitas: la transacción cubre
 * `idempotency_queue` y `cart`, y una escritura que falle revierte la otra.
 *
 * Las notificaciones salen DESPUÉS del commit, nunca dentro de la transacción:
 * un suscriptor que relea la tabla dentro de la transacción vería el estado sin
 * confirmar.
 */
export async function enqueueSaleClose(
  payload: SaleClosePayload,
  key: string,
  database: PosDatabase = getPosDatabase(),
): Promise<IdempotencyQueueRow> {
  const row: IdempotencyQueueRow = {
    id: crypto.randomUUID(),
    key,
    payload: JSON.stringify(payload),
    status: 'pending',
    created_at: new Date().toISOString(),
    operation: 'sale',
    // La fecha de negocio viaja en el cuerpo congelado; se copia sin
    // reinterpretar, nunca se re-deriva del reloj (docs/DATABASE.md §4 `sales`).
    business_date: payload.sold_on,
    sale_id: payload.sale_id,
    payment_method: payload.payment_method,
    total_cents: payload.total_cents,
    last_error: null,
  }

  await database.transaction('rw', database.idempotency_queue, database.cart, async () => {
    await database.idempotency_queue.add({ ...row })
    await database.cart.clear()
  })

  notifySaleQueueChanged()
  notifyCartChanged()

  return row
}

/**
 * Mueve el evento de una clave al estado que el envío dictó. La clave no es
 * única en la tienda —el servidor impone la unicidad sobre `(tenant_id, key)` y
 * marcarla localmente bloquearía un reencolado legítimo—, así que la fila se
 * busca por su índice y se actualiza por su clave primaria. Una clave
 * desconocida no inventa una fila.
 */
export async function markSaleQueueStatus(
  key: string,
  status: QueueStatus,
  error?: string,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  const row = await database.idempotency_queue.where('key').equals(key).first()

  if (row === undefined) return

  await database.idempotency_queue.update(row.id, {
    status,
    last_error: error ?? null,
  })
  notifySaleQueueChanged()
}

/**
 * El ledger de ventas del dispositivo: la venta cerrada y su anulación. Las dos
 * son eventos de venta y las dos mueven el esperado del arqueo; el cierre de
 * caja se queda afuera porque no es una venta.
 */
export async function readQueuedSales(
  database: PosDatabase = getPosDatabase(),
): Promise<readonly IdempotencyQueueRow[]> {
  const rows = await database.idempotency_queue.toArray()

  return rows.filter((row) => row.operation === 'sale' || row.operation === 'void')
}

/**
 * Cuántas ventas cerradas esperan confirmación. Es lo que anuncia el badge de
 * la cabecera: una anulación pendiente no es una venta y una venta confirmada o
 * fallida ya no espera nada.
 */
export function pendingSaleCount(rows: readonly IdempotencyQueueRow[]): number {
  return rows.filter((row) => row.operation === 'sale' && row.status === 'pending').length
}
