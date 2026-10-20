// Sin IndexedDB a la vista: el checkout no toca la base, recibe las líneas.
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { CheckoutScreen } from '@/screens/CheckoutScreen'

/*
 * Pantalla de cobro (W4-FE-2; docs/DESIGN.md §2.1-2.2, SPRINTS.md W4-FE-2):
 * tender ÚNICO — exactamente seis métodos, uno por venta, sin split tender
 * (docs/DATABASE.md §8 D9) —, monto recibido y vuelto en tipografía grande.
 */

/** El detalle que el checkout recibe ya congelado, línea por línea. */
const DETAIL: readonly { readonly name: string; readonly lineTotal: number }[] = [
  { name: 'Pan francés', lineTotal: 60 },
  { name: 'Arroz Costeño 1kg', lineTotal: 2720 },
]

const TOTAL = 2780 // 2720 + 60

function methodButtons(): HTMLButtonElement[] {
  return ['cash', 'yape', 'plin', 'card', 'other', 'fiado']
    .map((value) => screen.getByTestId(`tender-method-${value}`) as HTMLButtonElement)
}

describe('CheckoutScreen', () => {
  it('offers exactly the six methods, one card each', () => {
    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={() => undefined} />,
    )

    const cards = methodButtons()

    expect(cards).toHaveLength(6)
    expect(cards.map((card) => card.getAttribute('data-method'))).toEqual([
      'cash',
      'yape',
      'plin',
      'card',
      'other',
      'fiado',
    ])
    expect(cards.map((card) => card.textContent)).toEqual([
      'Efectivo',
      'Yape',
      'Plin',
      'Tarjeta',
      'Otro',
      'Fiado',
    ])
  })

  it('selects a single method: the choice replaces the previous one and there is no split tender', () => {
    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={() => undefined} />,
    )

    fireEvent.click(screen.getByTestId('tender-method-yape'))
    fireEvent.click(screen.getByTestId('tender-method-card'))

    const pressed = methodButtons().filter((card) => card.getAttribute('aria-pressed') === 'true')

    // Un solo tender por venta: una sola tarjeta marcada.
    expect(pressed).toHaveLength(1)
    expect(pressed[0]?.getAttribute('data-method')).toBe('card')
  })

  it('fixes the value that travels to sales.payment_method of the shipped POST', () => {
    const onConfirm = vi.fn()

    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={onConfirm} />,
    )

    fireEvent.click(screen.getByTestId('tender-method-yape'))
    fireEvent.click(screen.getByRole('button', { name: 'Finalizar venta' }))

    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onConfirm).toHaveBeenCalledWith('yape', TOTAL)
  })

  it('demands cash received at or above the total before finishing', () => {
    const onConfirm = vi.fn()

    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={onConfirm} />,
    )

    fireEvent.click(screen.getByTestId('tender-method-cash'))

    const finish = screen.getByRole('button', { name: 'Finalizar venta' }) as HTMLButtonElement

    // Sin monto recibido no hay cierre.
    expect(finish.disabled).toBe(true)

    // Los chips de billetes rápidos existentes: el chip de S/ 50 aporta 5000 centavos.
    fireEvent.click(screen.getByRole('button', { name: 'S/ 50' }))

    expect(finish.disabled).toBe(false)
    // Vuelto en pantalla gigante: 5000 − 2780 = 2220.
    expect(screen.getByTestId('checkout-change').textContent).toContain('S/ 22.20')

    fireEvent.click(finish)

    expect(onConfirm).toHaveBeenCalledWith('cash', 5000)
  })

  it('shows the giant change even before any bill chip, with the exact payment', () => {
    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={() => undefined} />,
    )

    fireEvent.click(screen.getByTestId('tender-method-cash'))
    fireEvent.click(screen.getByRole('button', { name: 'Exacto' }))

    expect(screen.getByTestId('checkout-change').textContent).toContain('S/ 0.00')
  })

  it('charges the exact amount for the wallet methods, without bill chips', () => {
    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={() => undefined} />,
    )

    fireEvent.click(screen.getByTestId('tender-method-plin'))

    expect(screen.queryByRole('button', { name: 'S/ 50' })).toBeNull()
    expect(screen.getByTestId('checkout-received').textContent).toContain('S/ 27.80')
    expect(screen.getByTestId('checkout-change').textContent).toContain('S/ 0.00')
  })

  it('closes a fiado sale with no received cash and no change', () => {
    const onConfirm = vi.fn()

    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={onConfirm} />,
    )

    fireEvent.click(screen.getByTestId('tender-method-fiado'))
    fireEvent.click(screen.getByRole('button', { name: 'Finalizar venta' }))

    expect(onConfirm).toHaveBeenCalledWith('fiado', 0)
  })

  it('shows the total first, in the money role', () => {
    render(
      <CheckoutScreen totalCents={TOTAL} lines={DETAIL} onConfirm={() => undefined} />,
    )

    expect(screen.getByTestId('checkout-total').textContent).toContain('S/ 27.80')
  })
})
