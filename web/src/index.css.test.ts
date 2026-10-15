import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

import { describe, expect, it } from 'vitest'

/*
 * El contrato de tokens es CSS puro: `@theme` de Tailwind v4 no pasa por jsdom,
 * así que la capa de diseño se verifica leyendo la fuente de verdad del repo.
 * Un import suelto de CSS en un test no resolvería ni las custom properties ni
 * las utilidades derivadas.
 *
 * La autoridad no se transcribe acá: §2.1 y §2.2 de `docs/DESIGN.md` se parsean y
 * se comparan contra `src/index.css`. Esa lectura es lo que convierte la suite en
 * una verificación de conformidad y no en un detector de cambios: una lista
 * literal de valores solo avisa que alguien tocó el CSS, mientras que leer el
 * documento avisa además cuando el CSS y el contrato se separan.
 *
 * El entorno jsdom no deja una base de URL utilizable para llegar al sistema de
 * archivos (su `URL` resuelve contra `http://localhost:3000/`), así que las rutas
 * se arman con `process.cwd()` —la raíz de Vite— y `node:path`. Nunca con
 * `new URL(..., import.meta.url)`: bajo jsdom `import.meta.url` no es una URL
 * `file:` confiable.
 */
function firstExistingDir(candidates: readonly string[], marker: string, role: string): string {
  const found = candidates.find((candidate) => existsSync(join(candidate, marker)))

  if (found === undefined) {
    throw new Error(`No se encontró ${role} (cwd: ${process.cwd()})`)
  }

  return found
}

/** Directorio `src/` de la PWA; el segundo candidato cubre la corrida desde la raíz. */
function sourceDir(): string {
  return firstExistingDir(
    [join(process.cwd(), 'src'), join(process.cwd(), 'web', 'src')],
    'index.css',
    'el árbol de fuentes de la PWA',
  )
}

/** Directorio `docs/` del repositorio; el primer candidato cubre la corrida desde `web/`. */
function docsDir(): string {
  return firstExistingDir(
    [join(process.cwd(), '..', 'docs'), join(process.cwd(), 'docs')],
    'DESIGN.md',
    'el directorio de documentos',
  )
}

const INDEX_CSS = () => readFileSync(join(sourceDir(), 'index.css'), 'utf8')
const MAIN_TSX = () => readFileSync(join(sourceDir(), 'main.tsx'), 'utf8')
const INDEX_HTML = () => readFileSync(join(dirname(sourceDir()), 'index.html'), 'utf8')
const DESIGN_DOC = () => readFileSync(join(docsDir(), 'DESIGN.md'), 'utf8')

/*
 * Todo valor se afirma sobre CSS sin comentarios. Con texto crudo, un valor
 * copiado dentro del bloque de comentarios de `index.css` satisfacía el
 * `toContain` sin estar declarado en `@theme`, que es justo lo que estas pruebas
 * tienen que detectar.
 */
function cssWithoutComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

/** Cuerpo de la sección que abre con ese encabezado, hasta el encabezado siguiente. */
function section(doc: string, heading: string): string {
  const start = doc.indexOf(heading)

  if (start === -1) {
    throw new Error(`docs/DESIGN.md ya no tiene la sección ${heading}`)
  }

  const body = doc.slice(start + heading.length)
  const end = body.search(/^#{2,3} /m)

  return end === -1 ? body : body.slice(0, end)
}

/** Celdas de las filas de datos de una tabla Markdown (sin encabezado ni separador). */
function tableRows(sectionBody: string): string[][] {
  return sectionBody
    .split('\n')
    .filter((line) => line.startsWith('|'))
    .map((line) =>
      line
        .split('|')
        .slice(1, -1)
        .map((cell) => cell.trim()),
    )
    .filter((cells) => cells.length >= 3 && /^`[a-z0-9-]+`$/.test(cells[0] ?? ''))
}

/*
 * §2.1 leída como tabla: token → primer valor de la columna Hex Code. El primer
 * valor importa porque `surface-border` declara dos grises y la implementación
 * toma el primero, como registra el comentario de `index.css`.
 */
function paletteFromDoc(): Map<string, string> {
  const palette = new Map<string, string>()

  for (const [name = '', hex = ''] of tableRows(section(DESIGN_DOC(), '### 2.1 '))) {
    const token = name.replaceAll('`', '')
    const value = (hex.split('/')[0] ?? '').replaceAll('`', '').trim()

    palette.set(token, value)
  }

  return palette
}

type TypeStep = {
  readonly sizeRem: number
  readonly lineHeightRem: number
  readonly weight: number
}

const PX_PER_REM = 16

/*
 * §2.2 leída como tabla. El documento declara el tamaño en px y en rem a la vez:
 * se toma el px y se convierte a 16px de raíz, que es la raíz de la PWA, y se
 * exige que la forma en rem del propio documento coincida con esa conversión.
 */
function typeScaleFromDoc(): Map<string, TypeStep> {
  const scale = new Map<string, TypeStep>()

  for (const [name = '', size = '', lineHeight = '', weight = ''] of tableRows(
    section(DESIGN_DOC(), '### 2.2 '),
  )) {
    const sizeParts = size.match(/^(\d+(?:\.\d+)?)px \((\d+(?:\.\d+)?)rem\)$/)
    const lineHeightParts = lineHeight.match(/^(\d+(?:\.\d+)?)px$/)
    const weightParts = weight.match(/^(\d+)/)

    if (sizeParts === null || lineHeightParts === null || weightParts === null) {
      throw new Error(`Fila de §2.2 fuera de formato: ${name} | ${size} | ${lineHeight} | ${weight}`)
    }

    const sizePx = Number(sizeParts[1])
    const sizeRem = Number(sizeParts[2])

    expect(sizePx / PX_PER_REM).toBeCloseTo(sizeRem, 6)

    scale.set(name.replaceAll('`', ''), {
      sizeRem,
      lineHeightRem: Number(lineHeightParts[1]) / PX_PER_REM,
      weight: Number(weightParts[1]),
    })
  }

  return scale
}

/** Valor declarado de una custom property de `index.css`, ya sin comentarios. */
function declaredValue(css: string, property: string): string | undefined {
  const match = css.match(new RegExp(`${property}:\\s*([^;]+);`))

  return match?.[1].trim()
}

/** Longitud en rem de un valor de `index.css`, sin la unidad. */
function lengthInRem(declared: string): number {
  const value = declared.match(/^(\d+(?:\.\d+)?)rem$/)

  return value === null ? Number.NaN : Number(value[1])
}

type Rgba = {
  readonly r: number
  readonly g: number
  readonly b: number
  readonly a: number
}

/** Color de un literal `#rrggbb` (o `#rgb`) o de una forma `rgb()` / `rgba()`. */
function parseColor(raw: string): Rgba | null {
  const value = raw.trim()

  if (value.startsWith('#')) {
    const digits = value.slice(1)
    const expanded =
      digits.length === 3
        ? [...digits].map((digit) => digit + digit).join('')
        : digits

    if (!/^[0-9a-fA-F]{6}$/.test(expanded)) return null

    return {
      r: Number.parseInt(expanded.slice(0, 2), 16),
      g: Number.parseInt(expanded.slice(2, 4), 16),
      b: Number.parseInt(expanded.slice(4, 6), 16),
      a: 1,
    }
  }

  const functional = value.match(/^rgba?\(([^)]+)\)$/)

  if (functional === null) return null

  const parts = (functional[1] ?? '').split(/[\s,/]+/).filter((part) => part !== '')

  if (parts.length !== 3 && parts.length !== 4) return null

  const [r = '0', g = '0', b = '0', alpha = '1'] = parts

  return { r: Number(r), g: Number(g), b: Number(b), a: Number(alpha) }
}

/**
 * Forma canónica de un color, para comparar el documento y el CSS entre sí:
 * `rgba(16,185,129,0.3)` y `rgb(16 185 129 / 0.3)` son el mismo color, y también
 * `#F4F4F5` y `#f4f4f5`.
 */
function canonicalColor(raw: string): string {
  const parsed = parseColor(raw)

  if (parsed === null) return `sin-parsear:${raw.trim()}`

  return `${parsed.r},${parsed.g},${parsed.b},${parsed.a}`
}

/*
 * Inventario explícito de §2.1 y §2.2. Los valores salen del documento, pero los
 * nombres no: si una fila desapareciera del documento —o el parseo se quedara
 * corto— la tabla quedaría incompleta y esta lista lo delata.
 */
const PALETTE_TOKENS: readonly string[] = [
  'surface-bg',
  'surface-card',
  'surface-subtle',
  'surface-border',
  'text-primary',
  'text-secondary',
  'text-muted',
  'accent-primary',
  'accent-hover',
  'accent-glow',
  'badge-fiado',
  'badge-yape',
  'badge-plin',
  'badge-other',
  'badge-card',
  'danger-action',
]

const SCALE_NAMES: readonly string[] = [
  'display-xl',
  'display-lg',
  'heading-md',
  'body-md',
  'body-sm',
  'caption-xs',
]

describe('design tokens (docs/DESIGN.md §2.1)', () => {
  it('parses the whole §2.1 palette of the document', () => {
    // La tabla del documento es la entrada de las 16 afirmaciones de abajo: si
    // una fila desapareciera, el parseo quedaría incompleto y esto lo delata.
    expect([...paletteFromDoc().keys()].sort()).toEqual([...PALETTE_TOKENS].sort())
  })

  it.each(PALETTE_TOKENS)('declares %s with the value §2.1 fixes', (token) => {
    const expected = paletteFromDoc().get(token)

    expect(expected, `docs/DESIGN.md §2.1 ya no lista ${token}`).toBeDefined()

    const declared = declaredValue(cssWithoutComments(INDEX_CSS()), `--color-${token}`)

    expect(declared, `index.css no declara --color-${token}`).toBeDefined()
    expect(canonicalColor(declared ?? ''), `--color-${token}`).toBe(canonicalColor(expected ?? ''))
  })

  it('declares the 60-30-10 families the frontend plan names', () => {
    // 60% neutros, 30% secundarios y 10% acento, tal como los enumera
    // docs/FRONTEND.md TASK 1.2 (:87-89).
    expect(cssWithoutComments(INDEX_CSS())).toContain('--color-surface-bg')
    expect(cssWithoutComments(INDEX_CSS())).toContain('--color-surface-card')
    expect(cssWithoutComments(INDEX_CSS())).toContain('--color-text-primary')
    expect(cssWithoutComments(INDEX_CSS())).toContain('--color-surface-subtle')
    expect(cssWithoutComments(INDEX_CSS())).toContain('--color-accent-primary')
    expect(cssWithoutComments(INDEX_CSS())).toContain('--color-accent-hover')
    expect(cssWithoutComments(INDEX_CSS())).toContain('--shadow-glow-emerald')
  })

  /*
   * §2.3 mantiene una divergencia conocida y documentada: el documento nombra la
   * sombra `shadow-emerald-glow`, mientras el token implementado es
   * `--shadow-glow-emerald` (el nombre que fija `docs/FRONTEND.md:89`, el plan que
   * ejecuta W1-FE-3). El valor es el mismo y solo cambia el nombre, así que la
   * divergencia se afirma de forma explícita —ni se saltea ni se deja fallar la
   * comparación de nombres— y se compara el color, que sí debe coincidir.
   */
  it('records the §2.3 shadow-name divergence and compares the color behind it', () => {
    const documentShadow = DESIGN_DOC().match(/shadow-emerald-glow`:\s*`([^`]+)`/)
    const cssShadow = cssWithoutComments(INDEX_CSS()).match(/--shadow-glow-emerald:\s*([^;]+);/)

    expect(documentShadow?.[1]).toBeDefined()
    expect(cssShadow?.[1]).toBeDefined()

    const documentColor = documentShadow?.[1]?.match(/rgba?\([^)]*\)/)?.[0]
    const cssColor = cssShadow?.[1]?.match(/rgba?\([^)]*\)/)?.[0]

    expect(documentColor).toBeDefined()
    expect(cssColor).toBeDefined()
    expect(canonicalColor(cssColor ?? '')).toBe(canonicalColor(documentColor ?? ''))
    expect(cssColor).toContain('0.35')
  })
})

describe('tabular typography (docs/DESIGN.md §2.2)', () => {
  it('declares the whole §2.2 scale with its line height and weight', () => {
    const scale = typeScaleFromDoc()
    const css = cssWithoutComments(INDEX_CSS())

    expect([...scale.keys()].sort()).toEqual([...SCALE_NAMES].sort())

    for (const [name, step] of scale) {
      const size = declaredValue(css, `--text-${name}`)
      const lineHeight = declaredValue(css, `--text-${name}--line-height`)
      const weight = declaredValue(css, `--text-${name}--font-weight`)

      expect(size, `index.css no declara --text-${name}`).toBeDefined()
      expect(lineHeight, `index.css no declara --text-${name}--line-height`).toBeDefined()
      expect(weight, `index.css no declara --text-${name}--font-weight`).toBeDefined()
      expect(lengthInRem(size ?? ''), `--text-${name}`).toBeCloseTo(step.sizeRem, 6)
      expect(lengthInRem(lineHeight ?? ''), `--text-${name}--line-height`).toBeCloseTo(
        step.lineHeightRem,
        6,
      )
      expect(Number(weight), `--text-${name}--font-weight`).toBe(step.weight)
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
    expect(cssWithoutComments(INDEX_CSS())).toContain('font-feature-settings: "tnum" 1')
    expect(cssWithoutComments(INDEX_CSS())).toMatch(/\.font-mono\s*\{[^}]*"tnum"/)
  })
})

describe('offline-first fonts', () => {
  it('bundles the font packages instead of requesting them from a CDN', () => {
    expect(MAIN_TSX()).toContain('@fontsource-variable/inter')
    expect(MAIN_TSX()).toContain('@fontsource-variable/jetbrains-mono')
    expect(MAIN_TSX()).toContain('@/index.css')
  })

  it('never links a remote font origin in the shell or the stylesheet', () => {
    // Texto crudo a propósito: acá la afirmación es negativa, así que también
    // tiene que fallar si el origen remoto aparece solo dentro de un comentario.
    for (const source of [INDEX_HTML(), INDEX_CSS(), MAIN_TSX()]) {
      expect(source).not.toContain('fonts.googleapis.com')
      expect(source).not.toContain('fonts.gstatic.com')
    }

    expect(INDEX_CSS()).not.toMatch(/@import\s+url\(\s*["']?https?:/)
  })
})
