import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Badge, badgeVariants, type BadgeProps } from '@/components/ui/Badge'
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

type BadgeTone = NonNullable<BadgeProps['tone']>

/*
 * Contraste de los tonos del Badge. El texto del Badge es `text-caption-xs`
 * (10px), es decir texto normal bajo WCAG 2.1: el umbral es 4,5:1, no los 3:1
 * del texto grande, así que ningún tono puede quedar por debajo. Los colores no
 * se copian acá: se resuelven contra `src/index.css` (la capa de tokens) y
 * contra las clases que el propio `cva` emite, de modo que un tono que pierda
 * contraste falle en la suite y no en una revisión visual.
 *
 * El entorno jsdom no deja una base de URL utilizable para llegar al sistema de
 * archivos, así que la raíz se toma de `process.cwd()` (la raíz de Vite), con el
 * segundo candidato para la corrida lanzada desde la raíz del repositorio.
 */
function tokenSource(): string {
  const candidates = [join(process.cwd(), 'src'), join(process.cwd(), 'web', 'src')]
  const found = candidates.find((candidate) => existsSync(join(candidate, 'index.css')))

  if (found === undefined) {
    throw new Error(`No se encontró el árbol de fuentes de la PWA (cwd: ${process.cwd()})`)
  }

  return readFileSync(join(found, 'index.css'), 'utf8')
}

/** `#rrggbb` de la custom property `--color-<token>` declarada en `@theme`. */
function colorToken(css: string, token: string): string | undefined {
  const match = css.match(new RegExp(`--color-${token}:\\s*(#[0-9A-Fa-f]{3,8})\\s*;`))

  return match?.[1].toLowerCase()
}

/** `text-white` sale de la escala por defecto de Tailwind, no del contrato. */
const TAILWIND_WHITE = '#ffffff'

/**
 * Valor de la primera utilidad con ese prefijo cuyo sufijo sea un token de color
 * de la capa de diseño. El filtro importa: `badgeVariants` emite además
 * `text-caption-xs`, que comparte prefijo con las utilidades de color sin serlo.
 */
function resolvedColor(css: string, classes: string, prefix: 'bg-' | 'text-'): string {
  for (const candidate of classes.split(/\s+/)) {
    if (!candidate.startsWith(prefix)) continue

    const token = candidate.slice(prefix.length)

    if (token === 'white') return TAILWIND_WHITE

    const value = colorToken(css, token)

    if (value !== undefined) return value
  }

  throw new Error(`El tono no declara ninguna utilidad de color ${prefix}*: ${classes}`)
}

/** Luminancia relativa de WCAG 2.1 sobre un `#rrggbb` en sRGB. */
function relativeLuminance(hex: string): number {
  const linear = [1, 3, 5].map((offset) => {
    const channel = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255

    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  })

  const [red = 0, green = 0, blue = 0] = linear

  return 0.2126 * red + 0.7152 * green + 0.0722 * blue
}

/** Ratio de contraste de dos colores opacos (WCAG 2.1): de 1:1 a 21:1. */
function contrastRatio(background: string, foreground: string): number {
  const backgroundLuminance = relativeLuminance(background)
  const foregroundLuminance = relativeLuminance(foreground)
  const lighter = Math.max(backgroundLuminance, foregroundLuminance)
  const darker = Math.min(backgroundLuminance, foregroundLuminance)

  return (lighter + 0.05) / (darker + 0.05)
}

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
    // El literal se arma por partes a propósito: Tailwind escanea también los
    // archivos de test como fuente, y leer el nombre completo de la utilidad de
    // transición global bastaba para emitir su regla, muerta, en el CSS
    // publicado. La garantía de fondo vive en `motion-contract.test.ts`; acá
    // solo evitamos inyectar en el bundle justo lo que prohibimos.
    expect(button.className).not.toContain(`transition${'-'}all`)
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

/*
 * Inventario exhaustivo por construcción: `Record<BadgeTone, true>` obliga a que
 * todo tono declarado en el `cva` tenga su fila acá — si el primitivo suma un
 * tono, `tsc` falla en esta tabla hasta que el tono entre a la verificación.
 */
const TONE_CONTRAST_TABLE: Record<BadgeTone, true> = {
  neutral: true,
  accent: true,
  danger: true,
  fiado: true,
  yape: true,
  plin: true,
  card: true,
  other: true,
}

const BADGE_TONES = Object.keys(TONE_CONTRAST_TABLE) as BadgeTone[]

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

  it.each(BADGE_TONES)('keeps the %s tone at or above 4,5:1', (tone) => {
    const css = tokenSource()
    const classes = badgeVariants({ tone })

    const ratio = contrastRatio(
      resolvedColor(css, classes, 'bg-'),
      resolvedColor(css, classes, 'text-'),
    )

    expect(ratio).toBeGreaterThanOrEqual(4.5)
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
