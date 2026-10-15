import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Money } from '@/components/Money'

describe('Money', () => {
  it('renders the soles amount in the mono tabular role', () => {
    render(<Money cents={1234} />)

    const amount = screen.getByText('S/ 12.34')

    // Criterio de aceptación de W1-FE-2: el importe en soles resuelve a la
    // familia mono con numeración tabular, que es la causa directa de que el
    // refresco del ticket no mueva el layout.
    expect(amount.className).toContain('font-mono')
    expect(amount.className).toContain('tabular-nums')
  })

  it('carries both role classes on the text node itself, not on a wrapper', () => {
    render(<Money cents={0} />)

    const amount = screen.getByText('S/ 0.00')

    expect(amount.textContent).toBe('S/ 0.00')
    expect(amount.classList.contains('font-mono')).toBe(true)
    expect(amount.classList.contains('tabular-nums')).toBe(true)
  })

  it('lets an external className override the role without losing it', () => {
    render(<Money cents={999999} className="text-display-xl text-text-primary" />)

    const amount = screen.getByText('S/ 9999.99')

    expect(amount.className).toContain('font-mono')
    expect(amount.className).toContain('tabular-nums')
    expect(amount.className).toContain('text-display-xl')
    expect(amount.className).toContain('text-text-primary')
  })
})
