/**
 * Cantidad decimal exacta de la frontera de venta: el contrato guarda `qty`
 * como TEXTO verbatim («2.750», docs/DATABASE.md §2.4) y el importe de línea
 * lo redondea el CLIENTE half-up («El servidor nunca recomputa dinero»,
 * docs/DATABASE.md §2.3; SPRINTS.md W4-FE-2). Ningún `parseFloat` del carrito:
 * el texto se parte en numerador y escala y toda la aritmética es entera.
 */

/**
 * La forma §2.4 del POST: hasta 7 dígitos de parte entera y hasta 3 decimales,
 * con un dígito distinto de cero en algún lado (`0` o `0.000` no son venta).
 */
const QTY_SHAPE = /^[0-9]{1,7}(\.[0-9]{1,3})?$/

export interface DecimalQty {
  /** La mantisa entera del texto: `2.750` es 2750 con escala 3. */
  readonly numerator: number
  /** Cantidad de decimales del texto: `2.750` tiene escala 3. */
  readonly scale: number
}

/** La cantidad mínima operable del carrito: un gramo (`0.001`). */
const SMALLEST_QTY: DecimalQty = { numerator: 1, scale: 3 }

export function parseDecimalQty(text: string): DecimalQty | null {
  if (!QTY_SHAPE.test(text)) return null
  if (!/[1-9]/.test(text)) return null

  const separator = text.indexOf('.')

  if (separator === -1) return { numerator: Number(text), scale: 0 }

  return {
    numerator: Number(text.replaceAll('.', '')),
    scale: text.length - separator - 1,
  }
}

/**
 * Vuelve al texto del contrato, sin ceros de escala sobrantes: `2.750` vuelve
 * como `2.75` porque son el MISMO número — la forma es del contrato, la
 * equivalencia del valor no cambia con el trim.
 */
export function formatDecimalQty(qty: DecimalQty): string {
  if (qty.scale === 0) return String(qty.numerator)

  const digits = String(qty.numerator).padStart(qty.scale + 1, '0')
  const trimmed = digits.slice(0, digits.length - qty.scale).concat(
    '.',
    digits.slice(digits.length - qty.scale),
  ).replace(/\.?0+$/, '')

  return trimmed
}

/** Avanza exactamente una unidad de medida: `1` → `2`, `2.750 kg` → `3.750 kg`. */
export function addUnit(qty: DecimalQty): DecimalQty {
  return { numerator: qty.numerator + 10 ** qty.scale, scale: qty.scale }
}

/**
 * Retrocede una unidad. `null` significa «quedó debajo de la cantidad mínima
 * operable» — el valor devuelto nunca es invalido: quien recibe null saca la
 * línea, nunca la deja en cero.
 */
export function subtractUnit(qty: DecimalQty): DecimalQty | null {
  const numerator = qty.numerator - 10 ** qty.scale

  if (
    numerator < SMALLEST_QTY.numerator * 10 ** (qty.scale - SMALLEST_QTY.scale) ||
    qty.scale < SMALLEST_QTY.scale
  ) {
    return null
  }

  return { numerator, scale: qty.scale }
}

/**
 * Importe de línea con el redondeo half-up del cierre: solo aritmética entera
 * (`línea = round_half_up(qty × unit_price_cents)`; docs/DATABASE.md §2.3).
 */
export function halfUpCents(qty: DecimalQty, unitPriceCents: number): number {
  const divisor = 10 ** qty.scale
  const product = qty.numerator * unitPriceCents
  const whole = Math.floor(product / divisor)
  const remainder = product - whole * divisor

  return remainder * 2 >= divisor ? whole + 1 : whole
}
