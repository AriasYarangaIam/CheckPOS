import { getPosDatabase, type CartRow, type PosDatabase, type ProductRow } from '@/db/posDatabase'

/**
 * Mínimo del camino Input → Cart para W3-FE-1 (ARCHITECTURE.md:37, :80): un
 * adaptador de entrada emite `onProductIdentified(code)` y acá se resuelve el
 * código contra el catálogo que ya vive en la base local.
 *
 * La regla del contrato: el código NO es la identidad del producto. Puede ser
 * el GTIN —el mismo código que un día traerá la cámara— o, para el granel sin
 * GTIN (pan, huevos, hielo), el id local de la fila. La resolución es tarea del
 * carrito y el adaptador nunca conoce precios ni stock (ARCHITECTURE.md:37).
 *
 * El alcance de W3 se corta en una línea de qty 1 con el precio congelado.
 * La fusión de cantidades (el micro-badge `×2`), el debounce anti doble toque,
 * el override de precio y el resto de la semántica de mutaciones son de
 * W4-FE-1; acá solo existe el agregado mínimo que la grilla necesita.
 */

/**
 * Resuelve el código de un adaptador contra el subconjunto local del catálogo.
 *
 * Primero el GTIN indexado —el equivalente de la lectura del escáner— y, si el
 * código no es un GTIN, la clave primaria: es el camino del granel, cuyo código
 * emitido es su id. Devuelve `null` cuando nada resuelve: el carrito no
 * inventa líneas para códigos desconocidos.
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

/**
 * Agrega una única línea al carrito en curso: qty 1 y el precio congelado al
 * instante del agregado (la intencionalidad de `CartRow.unit_price_cents`).
 * Devuelve la línea escrita, o `null` si el código no resolvió contra el
 * catálogo local.
 */
export async function addIdentifiedLine(
  code: string,
  database: PosDatabase = getPosDatabase(),
): Promise<CartRow | null> {
  const product = await resolveIdentifiedProduct(code, database)

  if (product === null) return null

  const line: CartRow = {
    // El id de la línea nace local: el carrito es del dispositivo y su
    // identidad completa la define el equipo, no el servidor.
    id: crypto.randomUUID(),
    product_id: product.id,
    qty: 1,
    unit_price_cents: product.price_cents,
  }

  // Cada mutación persiste: el carrito vive en IndexedDB, no en memoria de
  // React (docs/FRONTEND.md:99; ARCHITECTURE.md:38).
  await database.cart.add(line)

  return line
}
