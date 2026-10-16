import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { ConnectivityBadge } from '@/components/ConnectivityBadge'

/*
 * Badge de conectividad (docs/DESIGN.md §1.1:127-130, :489). El texto es
 * contrato byte a byte: `En línea` en verde y `Modo Offline - IndexedDB` en
 * ámbar. El tono ámbar es el `badge-fiado` que §2.1:49 fija, y el verde sale
 * del acento; el primitivo `Badge` —con la verificación de contraste AA de la
 * unidad W1-FE-3— es el que pinta los dos.
 */

function repoDocsDir(): string {
  const candidates = [join(process.cwd(), '..', 'docs'), join(process.cwd(), 'docs')]
  const found = candidates.find((candidate) => existsSync(join(candidate, 'DESIGN.md')))

  if (found === undefined) {
    throw new Error(`No se encontró docs/DESIGN.md (cwd: ${process.cwd()})`)
  }

  return found
}

function setOnLine(value: boolean): void {
  Object.defineProperty(window.navigator, 'onLine', { configurable: true, get: () => value })
}

afterEach(() => {
  Reflect.deleteProperty(window.navigator, 'onLine')
})

describe('ConnectivityBadge', () => {
  it('reads En línea while the connection is up', () => {
    setOnLine(true)
    render(<ConnectivityBadge />)

    const badge = screen.getByText('En línea')

    expect(badge.textContent).toBe('En línea')
    expect(badge.className).toContain('bg-accent-primary')
    expect(badge.className).toContain('text-text-primary')
  })

  it('reads Modo Offline - IndexedDB when the connection drops', () => {
    setOnLine(false)
    render(<ConnectivityBadge />)

    const badge = screen.getByText('Modo Offline - IndexedDB')

    expect(badge.textContent).toBe('Modo Offline - IndexedDB')
    // Ámbar `badge-fiado` (§2.1:49): el mismo tono de la unidad de fiados, con
    // texto primario porque sobre `#F59E0B` el blanco se queda en 2,15:1.
    expect(badge.className).toContain('bg-badge-fiado')
    expect(badge.className).toContain('text-text-primary')
  })

  it('alternates between the two states with the copy the document pins', () => {
    setOnLine(true)
    render(<ConnectivityBadge />)

    expect(screen.getByText('En línea')).toBeTruthy()

    setOnLine(false)
    act(() => {
      window.dispatchEvent(new Event('offline'))
    })

    expect(screen.queryByText('En línea')).toBeNull()
    expect(screen.getByText('Modo Offline - IndexedDB')).toBeTruthy()

    setOnLine(true)
    act(() => {
      window.dispatchEvent(new Event('online'))
    })

    expect(screen.getByText('En línea')).toBeTruthy()
    expect(screen.queryByText('Modo Offline - IndexedDB')).toBeNull()
  })

  it('still says what the design document says', () => {
    // Guarda de deriva: el documento es la autoridad de las dos cadenas.
    const design = readFileSync(join(repoDocsDir(), 'DESIGN.md'), 'utf8')

    expect(design).toContain('`En línea`')
    expect(design).toContain('`Modo Offline - IndexedDB`')
  })

  it('announces the change without stealing focus or opening a window', () => {
    setOnLine(true)
    render(<ConnectivityBadge />)

    const badge = screen.getByText('En línea')

    // El badge es un `<span>` de estado: no es un control, no recibe foco y
    // anuncia el cambio de forma cortés en lugar de interrumpir (§5.1:255; la
    // reconexión «no alerta con ventanas bloqueantes» §1.1:130).
    expect(badge.tagName).toBe('SPAN')
    expect(badge.getAttribute('aria-live')).toBe('polite')
    expect(badge.getAttribute('tabindex')).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('button')).toBeNull()

    setOnLine(false)
    act(() => {
      window.dispatchEvent(new Event('offline'))
    })

    expect(document.activeElement).toBe(document.body)
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
