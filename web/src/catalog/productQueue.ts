import { getPosDatabase, type IdempotencyQueueRow, type PosDatabase } from '@/db/posDatabase'
import { isStorageQuotaError, StorageQuotaBlockedError } from '@/db/quota'
import { searchProducts } from '@/screens/AdminInventoryScreen'
import { notifySaleQueueChanged } from '@/sales/saleQueue'

/**
 * Alta de producto encolable (W8-FE-5; docs/BACKEND.md §4.6; docs/DATABASE.md §7.6).
 *
 * `POST /products` es uno de los seis POST encolables, así que el alta funciona sin red: el
 * producto nace en la cola con su clave de idempotencia y su fila local, y drena cuando haya
 * conexión. Al servidor le llega el cuerpo del contrato, y **el stock no viaja**: converge solo por
 * deltas firmados (`stock_adjustments`), nunca por el alta (§4 `products`).
 *
 * La adopción del catálogo global es un puntero: cuando el operador encuentra el GTIN en la
 * mediación, `global_product_id` viaja en el cuerpo y el servidor copia los datos a la fila del
 * tenant (F13). Si no lo encuentra, el alta es manual y el puntero no existe.
 */

/** Borrador del alta, con el dinero ya en centavos enteros. */
export interface ProductDraft {
  /** UUIDv7 generado por el cliente: el producto nace en el dispositivo (2.2). */
  readonly id: string
  readonly name: string
  readonly gtin: string | null
  readonly priceCents: number
  readonly uom: string
  readonly photoUrl?: string | null
  readonly attributes?: Record<string, unknown> | null
  readonly globalProductId?: string | null
}

/**
 * Convierte el precio escrito en soles a centavos enteros, sin pasar por float: `4.50` → `450`.
 * Devuelve `null` cuando el texto no es un precio válido (vacío, negativo, más de dos decimales).
 */
export function priceToCents(text: string): number | null {
  const trimmed = text.trim()
  if (!/^[0-9]{1,7}(\.[0-9]{1,2})?$/.test(trimmed)) return null

  const [whole, fraction = ''] = trimmed.split('.')
  const cents = Number(`${whole}${`${fraction}00`.slice(0, 2)}`)

  return Number.isSafeInteger(cents) ? cents : null
}

/** Cuerpo del alta tal como lo pide §4.6: sin `stock` y con la marca de mutación del cliente. */
function productPayload(draft: ProductDraft, updatedAt: string): Record<string, unknown> {
  const body: Record<string, unknown> = {
    id: draft.id,
    name: draft.name.trim(),
    price_cents: draft.priceCents,
    uom: draft.uom.trim(),
    // Marca de mutación del cliente: el comparador LWW del contrato (§2.5, §7.4).
    updated_at: updatedAt,
  }

  if (draft.gtin !== null && draft.gtin.trim() !== '') body.gtin = draft.gtin.trim()
  if (draft.photoUrl !== undefined && draft.photoUrl !== null) body.photo_url = draft.photoUrl
  if (draft.attributes !== undefined && draft.attributes !== null) body.attributes = draft.attributes
  if (draft.globalProductId !== undefined && draft.globalProductId !== null) {
    body.global_product_id = draft.globalProductId
  }

  return body
}

export interface EnqueueProductInput {
  readonly draft: Omit<ProductDraft, 'id'> & { readonly id?: string }
  readonly key?: string
  readonly now?: () => Date
}

/**
 * Encola el alta y crea la fila local en la MISMA transacción: la tabla del catálogo muestra el
 * producto en el acto y el evento espera su acuse. El stock local arranca en cero porque el alta
 * no lo trae; el pull del servidor y los deltas son los que lo mueven.
 */
export async function enqueueProductCreate(
  input: EnqueueProductInput,
  database: PosDatabase = getPosDatabase(),
): Promise<IdempotencyQueueRow> {
  const name = input.draft.name.trim()
  if (name === '') throw new Error('El producto necesita un nombre')
  if (!Number.isInteger(input.draft.priceCents) || input.draft.priceCents < 0) {
    throw new Error('El precio del producto es un entero de centavos no negativo')
  }
  if (input.draft.uom.trim() === '') throw new Error('El producto necesita una unidad de medida')

  const now = input.now?.() ?? new Date()
  const createdAt = now.toISOString()
  const id = input.draft.id ?? crypto.randomUUID()
  const key = input.key ?? crypto.randomUUID()
  const gtin = input.draft.gtin?.trim() === '' ? null : (input.draft.gtin?.trim() ?? null)

  const row: IdempotencyQueueRow = {
    id: crypto.randomUUID(),
    key,
    payload: JSON.stringify(
      productPayload({ ...input.draft, id, name, gtin }, createdAt),
    ),
    status: 'pending',
    created_at: createdAt,
    operation: 'product',
    business_date: createdAt.slice(0, 10),
    sale_id: null,
    payment_method: null,
    total_cents: input.draft.priceCents,
    last_error: null,
  }

  try {
    await database.transaction('rw', [database.idempotency_queue, database.products], async () => {
      await database.idempotency_queue.add(row)
      await database.products.put({
        id,
        gtin,
        name,
        price_cents: input.draft.priceCents,
        uom: input.draft.uom.trim(),
        stock: 0,
        photo_url: input.draft.photoUrl ?? null,
        category: null,
      })
    })
  } catch (error) {
    if (isStorageQuotaError(error)) throw new StorageQuotaBlockedError()

    throw error
  }

  notifySaleQueueChanged()

  return row
}

/** Producto global resuelto por la mediación. */
export interface GlobalProductMatch {
  readonly id: string
  readonly name: string
  readonly attributes: Record<string, unknown> | null
}

/**
 * Busca el GTIN en el catálogo global. Sin red devuelve `null` y el llamador ofrece el alta manual:
 * la adopción es un atajo, nunca un requisito para vender.
 */
export async function lookupGlobalProduct(
  gtin: string,
  input: { readonly fetchImpl?: typeof fetch; readonly baseUrl?: string; readonly token?: string } = {},
): Promise<GlobalProductMatch | null> {
  const token = input.token?.trim() ?? ''
  if (token === '' || gtin.trim() === '') return null

  const network = input.fetchImpl ?? globalThis.fetch

  let response: Response
  try {
    response = await network(`${input.baseUrl ?? ''}/gtin-lookup?gtin=${encodeURIComponent(gtin.trim())}`, {
      method: 'GET',
      headers: { authorization: `Bearer ${token}` },
    })
  } catch {
    return null
  }
  if (!response.ok) return null

  let body: unknown
  try {
    body = await response.json()
  } catch {
    return null
  }

  const product = (body as { product?: { id?: unknown; name?: unknown; attributes?: unknown } })?.product
  if (product === undefined || typeof product.id !== 'string' || typeof product.name !== 'string') return null

  return {
    id: product.id,
    name: product.name,
    attributes:
      typeof product.attributes === 'object' && product.attributes !== null
        ? (product.attributes as Record<string, unknown>)
        : null,
  }
}

/** Reexporta la búsqueda del catálogo local para que la pantalla no la duplique. */
export { searchProducts }
