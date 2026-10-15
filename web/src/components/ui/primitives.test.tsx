import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/Dialog'
import { Input } from '@/components/ui/Input'

describe('Button', () => {
  it('renders its label as a button', () => {
    render(<Button>COBRAR</Button>)

    expect(screen.getByRole('button', { name: 'COBRAR' })).toBeTruthy()
  })

  it('carries the damped press transform on a specific property', () => {
    render(<Button>COBRAR</Button>)

    const button = screen.getByRole('button', { name: 'COBRAR' })

    // Criterio de W1-FE-3: `:active { transform: scale(0.98) }` y nada de
    // `transition: all` (docs/DESIGN.md §6.2:302).
    expect(button.className).toContain('active:scale-[0.98]')
    expect(button.className).toContain('transition-transform')
    expect(button.className).toContain('duration-[120ms]')
    expect(button.className).not.toContain('transition-all')
  })

  it('stays a plain button so it never submits a form by accident', () => {
    render(<Button>Agregar</Button>)

    const button = screen.getByRole('button', { name: 'Agregar' }) as HTMLButtonElement

    expect(button.type).toBe('button')
  })

  it('reaches the 52px ergonomic height in the cta role', () => {
    render(<Button size="cta">COBRAR</Button>)

    expect(screen.getByRole('button', { name: 'COBRAR' }).className).toContain('h-[52px]')
  })
})

describe('Card', () => {
  it('renders a titled content surface', () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Resumen de orden</CardTitle>
        </CardHeader>
        <CardContent>3 artículos</CardContent>
      </Card>,
    )

    expect(screen.getByRole('heading', { name: 'Resumen de orden' })).toBeTruthy()
    expect(screen.getByText('3 artículos')).toBeTruthy()
  })
})

describe('Badge', () => {
  it('renders a status label', () => {
    render(<Badge tone="accent">En línea</Badge>)

    const badge = screen.getByText('En línea')

    expect(badge.className).toContain('rounded-full')
    expect(badge.className).toContain('bg-accent-primary')
  })

  it('gives the fiado tone a readable foreground', () => {
    render(<Badge tone="fiado">Fiado</Badge>)

    const badge = screen.getByText('Fiado')

    expect(badge.className).toContain('bg-badge-fiado')
    expect(badge.className).toContain('text-text-primary')
  })
})

describe('Input', () => {
  it('renders a labelled text field', () => {
    render(<Input aria-label="Buscar producto" />)

    expect(screen.getByRole('textbox', { name: 'Buscar producto' })).toBeTruthy()
  })

  it('carries no transition, because search is a zero-latency action', () => {
    render(<Input aria-label="Buscar producto" />)

    const input = screen.getByRole('textbox', { name: 'Buscar producto' })

    expect(input.className).not.toContain('transition')
  })
})

describe('Dialog', () => {
  it('opens with the modal spring on the animated shell', () => {
    render(
      <Dialog>
        <DialogTrigger>Abrir cobro</DialogTrigger>
        <DialogContent>
          <DialogTitle>Cobrar</DialogTitle>
          <DialogDescription>Elige el método de pago</DialogDescription>
        </DialogContent>
      </Dialog>,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Abrir cobro' }))

    const dialog = screen.getByRole('dialog')
    const shell = dialog.firstElementChild as HTMLElement

    expect(shell.className).toContain('animate-modal-spring')
    // El resorte no comparte elemento con el `translate` del centrado: si lo
    // compartiera, el `scale` de la animación pisaría el centrado.
    expect(dialog.className).not.toContain('animate-modal-spring')
    expect(dialog.className).toContain('-translate-x-1/2')
  })

  it('closes on Escape', () => {
    render(
      <Dialog>
        <DialogTrigger>Abrir cobro</DialogTrigger>
        <DialogContent>
          <DialogTitle>Cobrar</DialogTitle>
        </DialogContent>
      </Dialog>,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Abrir cobro' }))
    expect(screen.getByRole('dialog')).toBeTruthy()

    fireEvent.keyDown(document, { key: 'Escape' })

    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
