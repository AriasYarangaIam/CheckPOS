import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

import { describe, expect, it } from 'vitest'

/*
 * El contrato de tokens es CSS puro: `@theme` de Tailwind v4 no pasa por jsdom,
 * así que la capa de diseño se verifica leyendo la fuente de verdad del repo.
 * Un import suelto de CSS en un test no resolvería ni las custom properties ni
 * las utilidades derivadas.
 *
 * El entorno jsdom no deja una base de URL utilizable para llegar al sistema de
 * archivos (su `URL` resuelve contra `http://localhost:3000/`), así que la raíz
 * se toma de `process.cwd()`, que es la raíz del proyecto Vite. La segunda
 * ubicación cubre la corrida lanzada desde la raíz del repositorio.
 */
function sourceDir(): string {
  const candidates = [join(process.cwd(), 'src'), join(process.cwd(), 'web', 'src')]
  const found = candidates.find((candidate) => existsSync(join(candidate, 'index.css')))

  if (found === undefined) {
    throw new Error(`No se encontró el árbol de fuentes de la PWA (cwd: ${process.cwd()})`)
  }

  return found
}

const INDEX_CSS = () => readFileSync(join(sourceDir(), 'index.css'), 'utf8')
const MAIN_TSX = () => readFileSync(join(sourceDir(), 'main.tsx'), 'utf8')
const INDEX_HTML = () => readFileSync(join(dirname(sourceDir()), 'index.html'), 'utf8')

type TokenCase = {
  readonly token: string
  readonly hex: string
}

// Tabla literal de docs/DESIGN.md §2.1 (Paleta Cromática).
const PALETTE_CASES: TokenCase[] = [
  { token: 'surface-bg', hex: '#f4f4f5' },
  { token: 'surface-card', hex: '#ffffff' },
  { token: 'surface-subtle', hex: '#e4e4e7' },
  { token: 'surface-border', hex: '#e4e4e7' },
  { token: 'text-primary', hex: '#18181b' },
  { token: 'text-secondary', hex: '#71717a' },
  { token: 'text-muted', hex: '#a1a1aa' },
  { token: 'accent-primary', hex: '#10b981' },
  { token: 'accent-hover', hex: '#059669' },
  { token: 'danger-action', hex: '#e11d48' },
  { token: 'badge-fiado', hex: '#f59e0b' },
  { token: 'badge-yape', hex: '#7c3aed' },
  { token: 'badge-plin', hex: '#0369a1' },
  { token: 'badge-card', hex: '#2563eb' },
  { token: 'badge-other', hex: '#52525b' },
]

// Escala de docs/DESIGN.md §2.2: tamaño / interlineado / peso.
const TYPE_SCALE_CASES: readonly (readonly [string, string, string, string])[] = [
  ['display-xl', '2.25rem', '2.5rem', '900'],
  ['display-lg', '1.875rem', '2.25rem', '800'],
  ['heading-md', '1.25rem', '1.75rem', '700'],
  ['body-md', '0.875rem', '1.25rem', '600'],
  ['body-sm', '0.75rem', '1rem', '500'],
  ['caption-xs', '0.625rem', '0.75rem', '700'],
]

describe('design tokens (docs/DESIGN.md §2.1)', () => {
  it.each(PALETTE_CASES)('declares $token as $hex', ({ token, hex }) => {
    expect(INDEX_CSS().toLowerCase()).toContain(`--color-${token}: ${hex}`)
  })

  it('declares the 60-30-10 families the frontend plan names', () => {
    // 60% neutros, 30% secundarios y 10% acento, tal como los enumera
    // docs/FRONTEND.md TASK 1.2 (:87-89).
    expect(INDEX_CSS()).toContain('--color-surface-bg')
    expect(INDEX_CSS()).toContain('--color-surface-card')
    expect(INDEX_CSS()).toContain('--color-text-primary')
    expect(INDEX_CSS()).toContain('--color-surface-subtle')
    expect(INDEX_CSS()).toContain('--color-accent-primary')
    expect(INDEX_CSS()).toContain('--color-accent-hover')
    expect(INDEX_CSS()).toContain('--shadow-glow-emerald')
  })

  it('pins the accent glow to the rgba(16,185,129,0.35) elevation', () => {
    const glow = INDEX_CSS().match(/--shadow-glow-emerald:\s*([^;]+);/)

    expect(glow?.[1]).toContain('16 185 129')
    expect(glow?.[1]).toContain('0.35')
  })
})

describe('tabular typography (docs/DESIGN.md §2.2)', () => {
  it('declares the whole type scale with its line height and weight', () => {
    for (const [name, size, lineHeight, weight] of TYPE_SCALE_CASES) {
      expect(INDEX_CSS()).toContain(`--text-${name}: ${size};`)
      expect(INDEX_CSS()).toContain(`--text-${name}--line-height: ${lineHeight};`)
      expect(INDEX_CSS()).toContain(`--text-${name}--font-weight: ${weight};`)
    }
  })

  it('routes the UI chrome to Inter and the S/ amounts to JetBrains Mono', () => {
    const sans = INDEX_CSS().match(/--font-sans:\s*([^;]+);/)
    const mono = INDEX_CSS().match(/--font-mono:\s*([^;]+);/)

    expect(sans?.[1]).toContain('Inter')
    expect(mono?.[1]).toContain('JetBrains Mono')
    expect(mono?.[1]).toContain('ui-monospace')
  })

  it('enables the tnum feature on the mono role so digits keep a fixed width', () => {
    expect(INDEX_CSS()).toContain('font-feature-settings: "tnum" 1')
    expect(INDEX_CSS()).toMatch(/\.font-mono\s*\{[^}]*"tnum"/)
  })
})

describe('offline-first fonts', () => {
  it('bundles the font packages instead of requesting them from a CDN', () => {
    expect(MAIN_TSX()).toContain('@fontsource-variable/inter')
    expect(MAIN_TSX()).toContain('@fontsource-variable/jetbrains-mono')
    expect(MAIN_TSX()).toContain('@/index.css')
  })

  it('never links a remote font origin in the shell or the stylesheet', () => {
    for (const source of [INDEX_HTML(), INDEX_CSS(), MAIN_TSX()]) {
      expect(source).not.toContain('fonts.googleapis.com')
      expect(source).not.toContain('fonts.gstatic.com')
    }

    expect(INDEX_CSS()).not.toMatch(/@import\s+url\(\s*["']?https?:/)
  })
})
