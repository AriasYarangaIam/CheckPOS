import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

/*
 * Auditoría de movimiento del sistema de diseño. Se lee la fuente tal como está
 * en el repo: el CSS de Tailwind no pasa por jsdom y las reglas que se verifican
 * acá son de contrato, no de comportamiento en tiempo de ejecución.
 *
 * El entorno jsdom no deja una base de URL utilizable para llegar al sistema de
 * archivos (su `URL` resuelve contra `http://localhost:3000/`), así que la raíz
 * se toma de `process.cwd()`, que es la raíz del proyecto Vite. La segunda
 * ubicación cubre la corrida lanzada desde la raíz del repositorio, y la
 * ausencia de ambas falla en voz alta en lugar de dejar la auditoría vacía.
 */
function sourceDir(): string {
  const candidates = [join(process.cwd(), 'src'), join(process.cwd(), 'web', 'src')]
  const found = candidates.find((candidate) => existsSync(join(candidate, 'index.css')))

  if (found === undefined) {
    throw new Error(`No se encontró el árbol de fuentes de la PWA (cwd: ${process.cwd()})`)
  }

  return found
}

function readSource(relativePath: string): string {
  return readFileSync(join(sourceDir(), relativePath), 'utf8')
}

function sourceFiles(): readonly string[] {
  return readdirSync(sourceDir(), { recursive: true, encoding: 'utf8' })
    // La auditoría cubre el código que se empaqueta: los archivos de test
    // afirman la ausencia del patrón y por eso lo nombran.
    .filter((entry) => /\.(ts|tsx|css)$/.test(entry) && !/\.test\./.test(entry))
    .map((entry) => join(sourceDir(), entry))
}

/**
 * La prohibición es sobre el código, no sobre la prosa: los comentarios que
 * explican la regla tienen que poder nombrarla sin convertirse en infractores.
 */
function stripComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:])\/\/.*$/gm, '$1')
}

// Los literales prohibidos se arman en tiempo de ejecución para que este mismo
// archivo no se delate al recorrer `src/`.
const BLANKET_CLASS = ['transition', 'all'].join('-')
const BLANKET_DECLARATION = new RegExp(['transition', '\\s*:', '\\s*all'].join(''))

describe('motion contract: no blanket transitions', () => {
  it('scans the whole source tree', () => {
    // Guarda contra una auditoría vacía: si el recorrido no encuentra archivos,
    // la ausencia de la transición global no probaría nada.
    const files = sourceFiles()

    expect(files.length).toBeGreaterThanOrEqual(10)
    expect(files.some((file) => file.endsWith('index.css'))).toBe(true)
    expect(files.some((file) => file.endsWith(join('ui', 'Button.tsx')))).toBe(true)
  })

  it('never uses the blanket Tailwind utility in any source file', () => {
    const offenders = sourceFiles().filter((file) =>
      stripComments(readFileSync(file, 'utf8')).includes(BLANKET_CLASS),
    )

    expect(offenders).toEqual([])
  })

  it('never uses the raw CSS declaration in any source file', () => {
    const offenders = sourceFiles().filter((file) =>
      BLANKET_DECLARATION.test(stripComments(readFileSync(file, 'utf8'))),
    )

    expect(offenders).toEqual([])
  })

  it('animates named properties instead', () => {
    expect(readSource('components/ui/Button.tsx')).toContain('transition-transform')
    expect(readSource('components/ui/Dialog.tsx')).toContain('transition-colors')
  })
})

describe('modal spring', () => {
  it('declares the damped spring with the documented curve and duration', () => {
    // docs/FRONTEND.md:19 y docs/DESIGN.md §6.2:304 fijan la curva; 200ms queda
    // dentro de la banda de modales de §5.2.
    expect(readSource('index.css')).toContain(
      '--animate-modal-spring: modal-spring 200ms cubic-bezier(0.16, 1, 0.3, 1)',
    )
  })

  it('enters from scale(0.96) and never from scale(0)', () => {
    const keyframes = readSource('index.css').match(/@keyframes modal-spring \{[\s\S]*?\n {2}\}/)

    expect(keyframes?.[0]).toContain('scale(0.96)')
    expect(keyframes?.[0]).toContain('scale(1)')
    expect(keyframes?.[0]).not.toContain('scale(0)')
  })

  it('keeps a gentler variant under prefers-reduced-motion', () => {
    const css = readSource('index.css')

    expect(css).toMatch(/@media \(prefers-reduced-motion: reduce\)[\s\S]*\.animate-modal-spring/)
    expect(css).toContain('@keyframes modal-spring-reduced')
  })
})
