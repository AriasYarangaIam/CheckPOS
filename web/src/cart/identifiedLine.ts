import { getPosDatabase, type CartRow, type PosDatabase, type ProductRow } from '@/db/posDatabase'
import { notifyCartChanged } from '@/cart/cartStore'
import { addUnit, formatDecimalQty, parseDecimalQty } from '@/lib/decimal'

/**
 * Agregado de línea identificado — la frontera donde la grilla y el escáner
 * dejan su código y el carrito resuelve (docs/DESIGN.md §1.2:135-141).
 *
 * **Doble toque.** W3-FE-1 dejó la grilla sin debounce a propósito; W4-FE-1 lo
 * agrega acá (docs/FRONTEND.md:104-106): los toques repetidos rápidos sobre el
 * MISMO código dentro de la ventana —el gesto físico mal disparado— producen
 * UNA línea con cantidad UNO (SPRINTS.md W4-FE-1). La ventana es corta
 * (350 ms): el toque deliberado de la segunda unidad, que llega después, cae
 * fuera y agrupa sobre la misma línea en lugar de crear otra.
 *
 * **Congelado §4.9.** Cada línea congela nombre, unidad y precio al agregar:
 * un cambio de catálogo después del toque no renombra lo que el cajero ya
 * sumó, y el cierre serializa los congelados tal cual los pide el POST.
 */

export async function resolveIdentifiedProduct(
  code: string,
  database: PosDatabase = getPosDatabase(),
): Promise<ProductRow | null> {
  const trimmed = code.trim()

  // Un código vacío no navega: el índice sobre `gtin` tampoco tendría nada que
  // igualar, y pedir la clave primaria con una cadena vacía sería devolver una
  // fila que no existe.
  if (trimmed === '') return null

  const byGtin = await database.products.where('gtin').equals(trimmed).toArray()

  return byGtin[0] ?? (await database.products.get(trimmed)) ?? null
}

export interface AddIdentifiedLineOptions {
  /** Reloj inyectable para las pruebas; en producción es el del dispositivo. */
  readonly now?: () => number
  /**
   * Ventana del doble toque en ms. 350 cubre el rebote del gesto y no se come
   * la suma rápida de unidades deliberada.
   */
  readonly debounceMs?: number
}

/** Ventana anti doble toque por defecto: el gesto rebota dentro de los 350 ms. */
export const DEFAULT_TAP_DEBOUNCE_MS = 350

/** Último toque registrado por código — estado del módulo, un device = uno. */
const lastIdentifiedAddAt = new Map<string, number>()

/** Borra la memoria de toques (las pruebas empiezan sin gesto previo). */
export function resetIdentifiedTapMemory(): void {
  lastIdentifiedAddAt.clear()
}

/**
 * Devuelve la línea activa (sin anular, precio de lista) del mismo producto,
 * o `null`. Agrupar sobre la línea activa es lo que hace que una venta tenga
 * una línea por producto y no una por toque.
 */
async function activeLineOfProduct(
  productId: string,
  listPriceCents: number,
  database: PosDatabase,
): Promise<CartRow | null> {
  const candidates = await database.cart.where('product_id').equals(productId).toArray()

  return (
    candidates.find(
      (line) => !line.voided && line.unit_price_cents === listPriceCents,
    ) ?? null
  )
}

/**
 * Agrega (o agrupa) la línea del producto identificado y PERSISTE la
 * mutación completa antes de volver: el carrito vive en IndexedDB, no en
 * memoria de React (docs/FRONTEND.md:99; ARCHITECTURE.md:38).
 *
 * Devuelve la línea afectada, `null` si el código no resolvió contra el
 * catálogo local, o la línea que ya estaba si el toque cayó dentro de la
 * ventana del doble toque.
 */
export async function addIdentifiedLine(
  code: string,
  database: PosDatabase = getPosDatabase(),
  options: AddIdentifiedLineOptions = {},
): Promise<CartRow | null> {
  const trimmed = code.trim()
  const product = await resolveIdentifiedProduct(trimmed, database)

  if (product === null) return null

  const now = (options.now ?? Date.now)()
  const debounceMs = options.debounceMs ?? DEFAULT_TAP_DEBOUNCE_MS
  const lastAddAt = lastIdentifiedAddAt.get(trimmed)

  if (lastAddAt !== undefined && now - lastAddAt < debounceMs) {
    return activeLineOfProduct(product.id, product.price_cents, database)
  }

  lastIdentifiedAddAt.set(trimmed, now)

  const existing = await activeLineOfProduct(product.id, product.price_cents, database)

  if (existing !== null) {
    const currentQty = parseDecimalQty(existing.qty)
    const next = currentQty === null ? null : formatDecimalQty(addUnit(currentQty))

    if (next === null) return existing

    const bumped: CartRow = { ...existing, qty: next }

    await database.cart.put(bumped)
    notifyCartChanged()

    return bumped
  }

  const line: CartRow = {
    // El id de la línea nace local: el carrito es del dispositivo y su
    // identidad completa la define el equipo, no el servidor.
    id: crypto.randomUUID(),
    product_id: product.id,
    qty: '1',
    unit_price_cents: product.price_cents,
    name: product.name,
    uom: product.uom,
    voided: false,
  }

  await database.cart.add(line)
  notifyCartChanged()

  return line
}
