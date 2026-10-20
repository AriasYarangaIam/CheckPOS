import { describe, expect, it } from 'vitest'

import {
  addUnit,
  formatDecimalQty,
  halfUpCents,
  parseDecimalQty,
  subtractUnit,
} from '@/lib/decimal'

/*
 * Cantidad exacta y redondeo half-up por línea (docs/DATABASE.md §2.3, §2.4):
 * el contrato guarda el qty como TEXTO verbatim («2.750») y el importe de línea
 * lo redondea el CLIENTE — el servidor congeló tal cual llegue. La aritmética
 * vive en esta frontera: parsear, avanzar una unidad y redondear sin flotantes
 * a la vista.
 */

describe('parseDecimalQty', () => {
  it('parses the contract shape: integers and up to three decimals', () => {
    expect(parseDecimalQty('1')).toEqual({ numerator: 1, scale: 0 })
    expect(parseDecimalQty('12')).toEqual({ numerator: 12, scale: 0 })
    expect(parseDecimalQty('2.750')).toEqual({ numerator: 2750, scale: 3 })
    expect(parseDecimalQty('0.5')).toEqual({ numerator: 5, scale: 1 })
  })

  it('rejects zero, negative-shape and malformed quantities', () => {
    expect(parseDecimalQty('')).toBeNull()
    expect(parseDecimalQty('0')).toBeNull()
    expect(parseDecimalQty('0.000')).toBeNull()
    expect(parseDecimalQty('abc')).toBeNull()
    expect(parseDecimalQty('2,75')).toBeNull()
    expect(parseDecimalQty('1.')).toBeNull()
    expect(parseDecimalQty('.5')).toBeNull()
  })

  it('rejects more than three decimals or an oversized integer part', () => {
    expect(parseDecimalQty('2.7500')).toBeNull()
    expect(parseDecimalQty('12345678')).toBeNull()
  })
})

describe('formatDecimalQty', () => {
  it('trims the trailing zeros so the text stays what the cashier meant', () => {
    expect(formatDecimalQty({ numerator: 2750, scale: 3 })).toBe('2.75')
    expect(formatDecimalQty({ numerator: 1, scale: 0 })).toBe('1')
    expect(formatDecimalQty({ numerator: 35, scale: 1 })).toBe('3.5')
  })
})

describe('addUnit and subtractUnit', () => {
  it('moves exactly one unit of measurement in the same scale', () => {
    expect(addUnit({ numerator: 1, scale: 0 })).toEqual({ numerator: 2, scale: 0 })
    expect(addUnit({ numerator: 250, scale: 3 })).toEqual({ numerator: 1250, scale: 3 })

    // «Restar una unidad» sobre 0.500 ya no deja cantidad válida: null
    // significa «la línea tiene que irse», no «cantidad cero».
    expect(subtractUnit({ numerator: 500, scale: 3 })).toBeNull()
    expect(subtractUnit({ numerator: 2750, scale: 3 })).toEqual({ numerator: 1750, scale: 3 })
    expect(subtractUnit({ numerator: 1, scale: 0 })).toBeNull()
  })
})

describe('halfUpCents', () => {
  it('rounds half-up at the line, with integer arithmetic only', () => {
    // 2.750 × S/ 27.19 (989 ¢) = 2719.75 → 2720 (la mitad sube).
    expect(halfUpCents({ numerator: 2750, scale: 3 }, 989)).toBe(2720)
    // 0.333 × 999 = 332.667 → 333.
    expect(halfUpCents({ numerator: 333, scale: 3 }, 999)).toBe(333)
    // Enteros exactos no se redondean.
    expect(halfUpCents({ numerator: 2, scale: 0 }, 4999)).toBe(9998)
  })
})
