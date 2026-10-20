import { getPosDatabase, type CartRow, type PosDatabase } from '@/db/posDatabase'
import { addUnit, formatDecimalQty, halfUpCents, parseDecimalQty, subtractUnit } from '@/lib/decimal'

/**
 * Store del carrito en curso (W4-FE-1; docs/FRONTEND.md TASK 2.3:104-106).
 * El estado ES la tabla `cart` de IndexedDB: cada mutación persiste ANTES de
 * devolver y los suscriptores (el panel del carrito, la barra de cobro) se
 * enteran por el emisor propio — no hay segunda copia del carrito en React.
 *
 * Acciones sobre la línea: cantidad exacta, override de precio, anulación
 * visible, quitar y vaciar. El cierre de venta toma solo las líneas que no
 * están anuladas (`payableCartLines`).
 */

type CartListener = () => void

const listeners = new Set<CartListener>()

/** Suscripción para `useCartLines` / pruebas de panel. */
export function subscribeToCart(listener: CartListener): () => void {
  listeners.add(listener)

  return () => {
    listeners.delete(listener)
  }
}

/** Emite el cambio del carrito: es lo que `useCartLines` relee. */
export function notifyCartChanged(): void {
  for (const listener of listeners) listener()
}

export async function setCartLineQty(
  lineId: string,
  qty: string,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  // La validación es del borde de la acción: una cantidad inservible no toca
  // la fila (`docs/DATABASE.md` §2.4, forma del qty).
  if (parseDecimalQty(qty) === null) {
    throw new Error('La cantidad debe ser positiva, con hasta tres decimales')
  }

  await database.cart.update(lineId, { qty })
  notifyCartChanged()
}

/**
 * Mueve la cantidad exactamente `units` unidades de medida. Al quedar debajo
 * de la cantidad mínima operable la línea SE QUITA: un carrito no conserva
 * líneas en cero.
 */
export async function changeCartLineQtyBy(
  lineId: string,
  units: number,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  const line = await database.cart.get(lineId)

  if (line === undefined) return

  const current = parseDecimalQty(line.qty)

  if (current === null) return

  let next: ReturnType<typeof subtractUnit> = current

  for (let step = 0; step < Math.abs(units); step += 1) {
    next = units > 0 ? addUnit(next as NonNullable<typeof next>) : subtractUnit(next as NonNullable<typeof next>)

    if (next === null) {
      await removeCartLine(lineId, database)
      return
    }
  }

  await setCartLineQty(lineId, formatDecimalQty(next), database)
}

export async function setCartLineUnitPrice(
  lineId: string,
  unitPriceCents: number,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  // Dinero: entero y no negativo; el override reemplaza al congelado y el
  // cierre lo usa tal cual (§4.9: el precio congelado es el de la línea).
  if (!Number.isInteger(unitPriceCents) || unitPriceCents < 0) {
    throw new Error('El precio debe ser un entero de centavos no negativo')
  }

  await database.cart.update(lineId, { unit_price_cents: unitPriceCents })
  notifyCartChanged()
}

export async function annulCartLine(
  lineId: string,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  await database.cart.update(lineId, { voided: true })
  notifyCartChanged()
}

export async function removeCartLine(
  lineId: string,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  await database.cart.delete(lineId)
  notifyCartChanged()
}

export async function emptyCart(
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  await database.cart.clear()
  notifyCartChanged()
}

/** Las líneas que el cierre de venta toma: las que no están anuladas. */
export function payableCartLines(lines: readonly CartRow[]): readonly CartRow[] {
  return lines.filter((line) => !line.voided)
}

/** Importe de UNA línea: qty exacta por precio congelado, half-up (§2.3). */
export function saleLineTotalCents(line: CartRow): number {
  const qty = parseDecimalQty(line.qty)

  return qty === null ? 0 : halfUpCents(qty, line.unit_price_cents)
}

/** Total del carrito: la suma de los importes de línea ya redondeados. */
export function cartTotalCents(lines: readonly CartRow[]): number {
  return payableCartLines(lines).reduce((sum, line) => sum + saleLineTotalCents(line), 0)
}
