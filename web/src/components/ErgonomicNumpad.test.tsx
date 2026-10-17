import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ErgonomicNumpad, NUMPAD_KEYS } from '@/components/ErgonomicNumpad'

/*
 * Numpad ergonómico (docs/DESIGN.md §1.2:142-145; docs/FRONTEND.md:41, :43,
 * :117-119). La matriz, el alto de tecla y el radio son contrato: 3x4, teclas
 * de 56px (`h-14`), `rounded-xl` y `shadow-keypad` (§2.3:71-76).
 *
 * El display NO vive acá: el numpad emite la tecla y cada pantalla es dueña de
 * su display. Por eso estas pruebas miran teclas y devoluciones de llamada, y
 * ninguna busca un importe ni un `S/`.
 */

function repoDocsDir(): string {
  const candidates = [join(process.cwd(), '..', 'docs'), join(process.cwd(), 'docs')]
  const found = candidates.find((candidate) => existsSync(join(candidate, 'DESIGN.md')))

  if (found === undefined) {
    throw new Error(`No se encontró docs/DESIGN.md (cwd: ${process.cwd()})`)
  }

  return found
}

const PINNED_MATRIX: readonly string[] = [
  '1',
  '2',
  '3',
  '4',
  '5',
  '6',
  '7',
  '8',
  '9',
  '00',
  '0',
  'Borrar',
]

describe('ErgonomicNumpad', () => {
  it('renders the pinned 3x4 matrix in reading order', () => {
    // Una grilla de 3 columnas con 12 teclas es la matriz 3x4 del contrato:
    // filas `1-2-3` / `4-5-6` / `7-8-9` / `00-0-Borrar`.
    render(<ErgonomicNumpad onKey={vi.fn()} />)

    const keys = screen.getAllByRole('button')

    expect(keys.map((key) => key.textContent)).toEqual([...PINNED_MATRIX])
    expect(screen.getByTestId('numpad-grid').className).toContain('grid-cols-3')
  })

  it('matches the last row the design document pins', () => {
    // El literal de arriba y el documento tienen que decir lo mismo: si el
    // contrato moviera la fila inferior, esta comparación lo delata.
    const design = readFileSync(join(repoDocsDir(), 'DESIGN.md'), 'utf8')
    const line = design.split('\n').find((candidate) => candidate.includes('Matriz 3x4'))

    expect(line, 'docs/DESIGN.md ya no describe la matriz del numpad').toBeDefined()

    const bottomRow = line?.match(/fila inferior `([^`]+)`/)?.[1].split(' - ')

    expect(bottomRow).toEqual(['00', '0', 'Borrar'])
  })

  it('gives every key the 56px ergonomic height of §2.3', () => {
    render(<ErgonomicNumpad onKey={vi.fn()} />)

    for (const key of screen.getAllByRole('button')) {
      // 56px de alto (`h-14`) con radio `rounded-xl` y la sombra `shadow-keypad`
      // (docs/DESIGN.md:71, :76, :145). El mínimo táctil de 48px queda cubierto
      // por el alto y por el ancho de la celda de la grilla de 3 columnas.
      expect(key.className).toContain('h-14')
      expect(key.className).toContain('rounded-xl')
      expect(key.className).toContain('shadow-keypad')
    }
  })

  it('keeps the zero and backspace keys in the residual group', () => {
    // `00` y `0` son teclas de dato, no de acción: van sobre la superficie de
    // la tarjeta y no se pintan con el acento.
    render(<ErgonomicNumpad onKey={vi.fn()} />)

    for (const label of ['00', '0']) {
      const key = screen.getByRole('button', { name: label })

      expect(key.className).toContain('bg-surface-card')
      expect(key.className).not.toContain('bg-danger-action')
    }
  })

  it('paints the delete key with the danger action token', () => {
    render(<ErgonomicNumpad onKey={vi.fn()} />)

    const borrar = screen.getByRole('button', { name: 'Borrar' })

    // `danger-action` (§2.1:54) es el token del «Botón borrar numpad»; el tipo
    // `danger` del primitivo Button ya lo aplica.
    expect(borrar.className).toContain('bg-danger-action')
    expect(borrar.textContent).toBe('Borrar')
  })

  it('carries the damped press response and no entrance animation', () => {
    render(<ErgonomicNumpad onKey={vi.fn()} />)

    const key = screen.getByRole('button', { name: '5' })

    // §6.2:302: `:active scale(0.98)` con `transition: transform 120ms
    // ease-out`, nunca `transition: all`. Y nada de animación de entrada: el
    // numpad es una acción de latencia cero (§6.2:298-299) y §5.2:267 prohíbe
    // las animaciones gratuitas.
    expect(key.className).toContain('active:scale-[0.98]')
    expect(key.className).toContain('transition-transform')
    expect(key.className).toContain('duration-[120ms]')
    expect(key.className).toContain('ease-out')
    expect(key.className).not.toContain(`transition${'-'}all`)
    expect(key.className).not.toContain('animate-')
  })

  it('emits the pressed key for each of its twelve keys', () => {
    const onKey = vi.fn()

    render(<ErgonomicNumpad onKey={onKey} />)

    for (const label of PINNED_MATRIX) {
      fireEvent.click(screen.getByRole('button', { name: label }))
    }

    expect(onKey.mock.calls.map(([key]) => key)).toEqual([...PINNED_MATRIX])
  })

  it('is operable from the keyboard, because the mostrador has a physical keyboard too', () => {
    // docs/FRONTEND.md:23 y §6.2:305-311: el teclado físico es un camino de
    // primera clase. Cada tecla es un `<button>` real, así que el foco y la
    // activación por Enter/Espacio son del navegador y no una simulación.
    const onKey = vi.fn()

    render(<ErgonomicNumpad onKey={onKey} />)

    const seven = screen.getByRole('button', { name: '7' })

    seven.focus()

    expect(document.activeElement).toBe(seven)

    fireEvent.click(seven)
    fireEvent.keyDown(seven, { key: 'Enter' })
    fireEvent.keyUp(seven, { key: ' ' })

    expect(onKey).toHaveBeenCalled()
    expect(seven.tagName).toBe('BUTTON')
    expect((seven as HTMLButtonElement).type).toBe('button')
  })

  it('stops emitting while the caller disables it', () => {
    const onKey = vi.fn()

    render(<ErgonomicNumpad onKey={onKey} disabled />)

    const grid = screen.getByTestId('numpad-grid')

    for (const key of within(grid).getAllByRole('button')) {
      expect((key as HTMLButtonElement).disabled).toBe(true)

      fireEvent.click(key)
    }

    expect(onKey).not.toHaveBeenCalled()
  })

  it('exposes the matrix as data so a caller never retypes it', () => {
    expect([...NUMPAD_KEYS]).toEqual([...PINNED_MATRIX])
  })
})
