import { describe, expect, it } from 'vitest'

import { formatCentsToSoles } from '@/lib/money'

type MoneyCase = {
  readonly label: string
  readonly cents: number
  readonly expected: string
}

// Tabla de casos de la frontera de dinero. El caso negativo queda fuera a
// propósito: el contrato no define cómo se renderiza un importe negativo.
const MONEY_CASES: MoneyCase[] = [
  { label: 'zero', cents: 0, expected: 'S/ 0.00' },
  { label: 'below one sol: five cents', cents: 5, expected: 'S/ 0.05' },
  { label: 'below one sol: forty-five cents', cents: 45, expected: 'S/ 0.45' },
  { label: 'a trailing zero cent', cents: 1230, expected: 'S/ 12.30' },
  { label: 'a large four-digit amount', cents: 999999, expected: 'S/ 9999.99' },
]

describe('formatCentsToSoles', () => {
  it.each(MONEY_CASES)('formats $label', ({ cents, expected }) => {
    expect(formatCentsToSoles(cents)).toBe(expected)
  })
})
