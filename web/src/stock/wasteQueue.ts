import { getPosDatabase, type IdempotencyQueueRow, type PosDatabase } from '@/db/posDatabase'
import { isStorageQuotaError, StorageQuotaBlockedError } from '@/db/quota'
import { notifySaleQueueChanged } from '@/sales/saleQueue'
import { formatBusinessDate } from '@/sales/saleClose'

/**
 * Cola de la merma (W8-FE-5; docs/BACKEND.md §4.14; docs/DATABASE.md §7.6). Un delta FIRMADO
 * negativo sobre el log del stock, con `source = 'manual'` y uno de los tres motivos exactos.
 *
 * **El cajero escribe una cantidad; el signo lo pone este módulo.** La merma descuenta, así que
 * el `qty_delta` del cuerpo es negativo y viaja como TEXTO con tres decimales (`-2.500`), que es
 * la escala de `NUMERIC(10,3)`: el número nunca pasa por float en el camino al servidor.
 *
 * **La proyección local se mueve con el encolado.** El dispositivo muestra el stock que conoce; si
 * la merma no lo moviera, el semáforo seguiría diciendo que hay mercadería que ya se dio de baja.
 * El ack del servidor y el pull son la verdad final, pero el operador ve el efecto de inmediato y
 * sin conexión.
 *
 * **No hay foto.** `stock_adjustments.evidence_photo_url` está diferido (delta D5): el cuerpo no
 * tiene el campo y el formulario no tiene control de captura.
 */

/** Los TRES motivos exactos del contrato (`docs/DESIGN.md:217`; S13). */
export const WASTE_REASONS = ['Vencimiento', 'Rotura en transporte', 'Daño en vitrina'] as const

export type WasteReason = (typeof WASTE_REASONS)[number]

/** Informa si el texto es uno de los tres motivos exactos. */
export function isWasteReason(reason: string): reason is WasteReason {
  return (WASTE_REASONS as readonly string[]).includes(reason)
}

/** Cantidad escrita por el operador, con hasta tres decimales (`NUMERIC(10,3)`). */
const QUANTITY_PATTERN = /^[0-9]{1,7}(\.[0-9]{1,3})?$/

/**
 * Convierte la cantidad del operador en el `qty_delta` NEGATIVO del contrato. Devuelve `null`
 * cuando la cantidad no es un número positivo con a lo sumo tres decimales: el llamador decide
 * qué decir, y acá no se inventa un cero.
 */
export function qtyDeltaOf(quantity: string): string | null {
  const trimmed = quantity.trim()
  if (!QUANTITY_PATTERN.test(trimmed)) return null

  const [whole, fraction = ''] = trimmed.split('.')
  const padded = `${fraction}000`.slice(0, 3)
  const digits = `${whole}.${padded}`
  const numeric = Number(digits)
  if (!Number.isFinite(numeric) || numeric <= 0) return null

  return `-${digits}`
}

/** Cuerpo del movimiento tal como lo pide §4.14. */
function wastePayload(input: {
  readonly id: string
  readonly productId: string
  readonly qtyDelta: string
  readonly reason: WasteReason
}): Record<string, unknown> {
  return {
    id: input.id,
    product_id: input.productId,
    qty_delta: input.qtyDelta,
    // `source` viaja solo para que el servidor pueda rechazar cualquier otro valor: la superficie
    // escribe mermas, y los deltas de venta y de anulación los escribe el ledger.
    source: 'manual',
    reason: input.reason,
  }
}

export interface EnqueueWasteInput {
  readonly productId: string
  /** Cantidad POSITIVA que escribe el operador: «2.5 kg». */
  readonly quantity: string
  readonly reason: WasteReason
  readonly key?: string
  readonly adjustmentId?: string
  readonly now?: () => Date
  /** Nombre del producto, solo para el mensaje de error del llamador. */
  readonly productName?: string
}

/**
 * Encola la merma, mueve la proyección local del stock y registra el evento con su clave de
 * idempotencia. Todo en UNA transacción: un fallo no deja la fila de la cola sin su efecto, ni el
 * efecto sin la fila.
 */
export async function enqueueWaste(
  input: EnqueueWasteInput,
  database: PosDatabase = getPosDatabase(),
): Promise<IdempotencyQueueRow> {
  const qtyDelta = qtyDeltaOf(input.quantity)
  if (qtyDelta === null) {
    throw new Error('La merma necesita una cantidad positiva con hasta tres decimales')
  }
  if (!isWasteReason(input.reason)) {
    throw new Error('La merma necesita uno de los tres motivos del contrato')
  }

  const createdAt = (input.now?.() ?? new Date()).toISOString()
  const adjustmentId = input.adjustmentId ?? crypto.randomUUID()
  const key = input.key ?? crypto.randomUUID()

  const row: IdempotencyQueueRow = {
    id: crypto.randomUUID(),
    key,
    payload: JSON.stringify(
      wastePayload({ id: adjustmentId, productId: input.productId, qtyDelta, reason: input.reason }),
    ),
    status: 'pending',
    created_at: createdAt,
    operation: 'stock_adjustment',
    business_date: formatBusinessDate(new Date(createdAt)),
    sale_id: null,
    payment_method: null,
    // No es dinero: la merma mueve unidades, no caja.
    total_cents: 0,
    last_error: null,
  }

  const delta = Number(qtyDelta)

  try {
    await database.transaction('rw', [database.idempotency_queue, database.products], async () => {
      await database.idempotency_queue.add(row)

      const product = await database.products.get(input.productId)
      if (product !== undefined) {
        // Tres decimales, la escala del contrato: sin el redondeo la suma arrastraría el error
        // del punto flotante y el semáforo mostraría 4.699999.
        const stock = Math.round((product.stock + delta) * 1000) / 1000
        await database.products.update(input.productId, { stock })
      }
    })
  } catch (error) {
    if (isStorageQuotaError(error)) throw new StorageQuotaBlockedError()

    throw error
  }

  notifySaleQueueChanged()

  return row
}
