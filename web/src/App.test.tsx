import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { App } from '@/App'

describe('App shell', () => {
  it('mounts the main landmark of the shell', () => {
    render(<App />)

    expect(screen.getByRole('main')).toBeTruthy()
  })

  it('renders the product title as a level-1 heading', () => {
    render(<App />)

    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('POS SaaS')
  })
})
