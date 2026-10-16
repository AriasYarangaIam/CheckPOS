import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Badge, badgeVariants, type BadgeProps } from '@/components/ui/Badge'
import { Button, buttonVariants, type ButtonProps } from '@/components/ui/Button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/Dialog'
import { Input } from '@/components/ui/Input'
import { cn } from '@/lib/utils'

type BadgeTone = NonNullable<BadgeProps['tone']>
type ButtonVariant = NonNullable<ButtonProps['variant']>
type ButtonSize = NonNullable<ButtonProps['size']>

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

/**
 * Escalones de la paleta por defecto de Tailwind que usan los primitivos y que
 * no viven en la capa de tokens de `src/index.css`. No se derivan de
 * `theme.css`: Tailwind v4 los declara en `oklch()` y su conversión a sRGB cae
 * fuera de gamut (`rose-700` daría `#C70036` en lugar de `#BE123C`), así que se
 * fija el hex publicado de cada escalón.
 */
const TAILWIND_PALETTE: Record<string, string> = {
  white: '#ffffff',
  'zinc-200': '#e4e4e7',
  'rose-700': '#be123c',
}

/**
 * Prefijo de color de una utilidad, o `undefined` si no es una utilidad de
 * color. El caso negativo importa: `text-body-md` comparte prefijo con las
 * utilidades de color sin serlo, y `outline-2` / `outline-offset-2` comparten
 * prefijo con `outline-<color>`. `border-` entra por el borde de foco del
 * `Input`; un ancho (`border-2`) no tiene custom property que lo resuelva y
 * vuelve `undefined`, así que sigue cayendo del lado negativo.
 */
function colorPrefix(token: string): 'bg-' | 'text-' | 'outline-' | 'border-' | undefined {
  if (token.startsWith('bg-')) return 'bg-'
  if (token.startsWith('text-')) return 'text-'
  if (token.startsWith('outline-')) return 'outline-'
  if (token.startsWith('border-')) return 'border-'

  return undefined
}

/** Color de una utilidad suelta, o `undefined` si no es una utilidad de color. */
function utilityColor(css: string, utility: string): string | undefined {
  const token = utility.slice(utility.lastIndexOf(':') + 1)
  const prefix = colorPrefix(token)

  if (prefix === undefined) return undefined

  const name = token.slice(prefix.length)

  return TAILWIND_PALETTE[name] ?? colorToken(css, name)
}

/**
 * Color del token `--color-<token>`; falla si el contrato dejó de declararlo.
 */
function requiredToken(css: string, token: string): string {
  const value = colorToken(css, token)

  if (value === undefined) {
    throw new Error(`El contrato no declara la custom property --color-${token}`)
  }

  return value
}

/** Primera utilidad de color con ese prefijo, resuelta a su valor. */
function resolvedColor(css: string, classes: string, prefix: 'bg-' | 'text-'): string {
  for (const candidate of classes.split(/\s+/)) {
    if (!candidate.startsWith(prefix)) continue

    const value = utilityColor(css, candidate)

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

/*
 * Escalones de brillo. Una utilidad de brillo en hover cambia el color pintado
 * sin cambiar ningún token, así que un resolutor que solo lea utilidades de color
 * no la ve: la verificación del hover quedaría hueca justo en el rol donde el
 * hover es el único cambio visual. El modelo de abajo reproduce lo que hace el
 * motor —normaliza el canal a [0,1] en coma flotante de 32 bits, multiplica por
 * el factor y trunca al cuantizar a 8 bits— y se fijó contra Chromium 155 con
 * `canvas` 2D (la misma implementación que el filtro CSS) sobre las 256
 * intensidades de cada factor: redondear en lugar de truncar fallaba en 130 de
 * los 256 valores al 95%.
 */

/**
 * Prefijo de las utilidades de brillo, armado por partes como el literal de la
 * transición global de más arriba: Tailwind escanea los archivos de test —y sus
 * comentarios— como fuente, así que nombrar un escalón completo acá publicaba su
 * regla, muerta, en el CSS del bundle.
 */
const BRIGHTNESS_PREFIX = ['brightness', ''].join('-')

/**
 * Factor de una utilidad de brillo (`…-105` → 1.05), o `undefined` si la
 * utilidad no declara brillo. Solo se leen los escalones con nombre: un valor
 * arbitrario tiene que entrar al resolutor, y para eso está la tabla de
 * `Button`, que falla si alguna variante declara un brillo que nadie resuelve.
 */
function brightnessFactor(utility: string): number | undefined {
  const token = utility.slice(utility.lastIndexOf(':') + 1)
  const named = token.match(new RegExp(`^${BRIGHTNESS_PREFIX}(\\d+)$`))

  return named !== null ? Number(named[1]) / 100 : undefined
}

/**
 * Canal de un `#rrggbb` tras multiplicarlo por el factor: la multiplicación es
 * lineal sobre el valor sRGB ya normalizado, y el truncado —no el redondeo— es
 * lo que hace el motor al volver a 8 bits.
 */
function brightenedChannel(channel: number, factor: number): number {
  const normalized = Math.fround(channel / 255)
  const scaled = Math.fround(normalized * Math.fround(factor))

  return Math.min(255, Math.max(0, Math.floor(scaled * 255)))
}

/** Color efectivo de un `#rrggbb` tras un filtro de brillo de ese factor. */
function brightenedColor(hex: string, factor: number): string {
  if (factor === 1) return hex

  const channels = [1, 3, 5].map((offset) =>
    brightenedChannel(Number.parseInt(hex.slice(offset, offset + 2), 16), factor),
  )

  return `#${channels.map((channel) => channel.toString(16).padStart(2, '0')).join('')}`
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

/*
 * Contraste del Button: variante × tamaño × estado. El rol principal es el que
 * fallaba —blanco sobre `#10B981` mide 2,54:1—, pero la tabla cubre los cuatro
 * roles para que la próxima variante no nazca sin verificación.
 */

const BUTTON_STATES = ['rest', 'hover'] as const

type ButtonState = (typeof BUTTON_STATES)[number]

/**
 * Utilidades de color de un estado: en reposo las que no llevan prefijo de
 * variante, en hover las que sí. `focus-visible:` y `disabled:` no entran: no
 * cambian ni la tinta ni el relleno que se está midiendo.
 */
function stateUtilities(classes: string, prefix: 'bg-' | 'text-', state: ButtonState): string[] {
  const selector = state === 'hover' ? `hover:${prefix}` : prefix

  return classes
    .split(/\s+/)
    .filter((candidate) => candidate.startsWith(selector))
    .map((candidate) => candidate.slice(candidate.lastIndexOf(':') + 1))
}

/**
 * Factor de brillo que un estado aplica al elemento, o 1 si no declara ninguno.
 * El filtro no es una utilidad de color, así que vive fuera de
 * `stateUtilities`: se busca con el mismo prefijo de estado para que `hover:`
 * solo cuente en hover.
 */
function stateBrightness(classes: string, state: ButtonState): number {
  const selector = state === 'hover' ? `hover:${BRIGHTNESS_PREFIX}` : BRIGHTNESS_PREFIX

  for (const candidate of classes.split(/\s+/)) {
    if (!candidate.startsWith(selector)) continue

    const factor = brightnessFactor(candidate)

    if (factor !== undefined) return factor
  }

  return 1
}

/**
 * Colores que un estado pinta con ese prefijo, resueltos contra los tokens y sin
 * repetir. El conjunto es la unidad de verificación: la fusión de `cn` tiene que
 * dejar exactamente una utilidad viva por grupo.
 */
function stateColors(
  css: string,
  classes: string,
  prefix: 'bg-' | 'text-',
  state: ButtonState,
): string[] {
  const colors = stateUtilities(classes, prefix, state)
    .map((utility) => utilityColor(css, utility))
    .filter((color): color is string => color !== undefined)

  return [...new Set(colors)]
}

/**
 * Par efectivo de un estado: si no declara color propio, hereda el del reposo
 * —igual que CSS con una utilidad `hover:` ausente—; si declara, tiene que ser
 * uno solo, porque `cn` ya fusionó los conflictos antes de llegar acá.
 */
function effectiveColors(
  css: string,
  classes: string,
  prefix: 'bg-' | 'text-',
  state: ButtonState,
): string[] {
  const declared = stateColors(css, classes, prefix, state)

  return declared.length > 0 ? declared : stateColors(css, classes, prefix, 'rest')
}

/**
 * Umbral WCAG 2.1 por clase tipográfica del tamaño (docs/DESIGN.md §2.2):
 * `text-heading-md` son 20px con peso 700, el único tamaño de la escala de
 * botones que supera el piso de «texto grande» (≥18,66px en negrita). `sm`
 * (`text-body-sm`, 12px/500) y `md` (`text-body-md`, 14px/600) son texto normal.
 */
const SIZE_MIN_RATIO: Record<ButtonSize, number> = {
  sm: 4.5,
  md: 4.5,
  cta: 3.0,
}

/** La tinta que el contrato exige sobre el relleno de cada par variante×tamaño. */
type ButtonInk = 'text-white' | 'text-text-primary'

/**
 * Inventario exhaustivo por construcción: `Record<ButtonVariant, Record<ButtonSize, …>>`
 * obliga a que toda variante y todo tamaño declarados en el `cva` tengan su fila
 * acá. Si el primitivo suma una variante o un tamaño, `tsc` falla en esta tabla
 * hasta que entren a la verificación.
 */
const BUTTON_INK: Record<ButtonVariant, Record<ButtonSize, ButtonInk>> = {
  primary: { sm: 'text-text-primary', md: 'text-text-primary', cta: 'text-white' },
  secondary: { sm: 'text-text-primary', md: 'text-text-primary', cta: 'text-text-primary' },
  ghost: { sm: 'text-text-primary', md: 'text-text-primary', cta: 'text-text-primary' },
  danger: { sm: 'text-white', md: 'text-white', cta: 'text-white' },
}

/**
 * `ghost` no declara relleno: se pinta sobre la superficie que lo contiene, y
 * las dos superficies del sistema (§2.1:39-40) entran a la verificación. Los
 * demás roles traen su propio fondo y dejan la lista vacía.
 */
const BUTTON_BACKDROPS: Record<ButtonVariant, readonly string[]> = {
  primary: [],
  secondary: [],
  ghost: ['surface-bg', 'surface-card'],
  danger: [],
}

const BUTTON_CASES: Array<[ButtonVariant, ButtonSize]> = (
  Object.keys(BUTTON_INK) as ButtonVariant[]
).flatMap((variant) =>
  (Object.keys(BUTTON_INK[variant]) as ButtonSize[]).map(
    (size): [ButtonVariant, ButtonSize] => [variant, size],
  ),
)

describe('Button contrast', () => {
  it('fills the primary CTA with the corrected contract step', () => {
    // El contrato movió el relleno a `accent-hover` porque el blanco sobre
    // `#10B981` mide 2,54:1. Si el componente vuelve a `accent-primary`, este
    // caso lo detiene sin depender del ojo de quien revisa.
    const classes = cn(buttonVariants({ variant: 'primary', size: 'cta' }))

    expect(classes).toContain('bg-accent-hover')
    expect(classes).not.toContain('bg-accent-primary')
  })

  it('lets cn collapse the ink conflict that the cva leaves open', () => {
    // En `sm` y `md` el `cva` emite las dos tintas a propósito —`text-white`
    // del rol y `text-text-primary` del compuesto—, y la fusión de `cn` tiene
    // que dejar viva la última. Sin esa fusión el navegador aplicaría la que
    // gane por orden de la hoja de estilos, no por la última declarada.
    const mdRaw = buttonVariants({ variant: 'primary', size: 'md' })
    const mdFused = cn(mdRaw)

    expect(mdRaw).toContain('text-white')
    expect(mdRaw).toContain('text-text-primary')
    expect(mdFused).not.toContain('text-white')
    expect(mdFused).toContain('text-text-primary')

    // En `cta` el compuesto no aplica y el blanco sobrevive como única tinta.
    const ctaFused = cn(buttonVariants({ variant: 'primary', size: 'cta' }))

    expect(ctaFused).toContain('text-white')
    expect(ctaFused).not.toContain('text-text-primary')
  })

  it.each(BUTTON_CASES)('keeps %s/%s at AA in every state', (variant, size) => {
    const css = tokenSource()
    // La cadena que llega al DOM es la que sale de `cn`: `tailwind-merge`
    // resuelve el conflicto entre la tinta del rol y la del compuesto, así que
    // la verificación se hace sobre la salida fusionada y no sobre la cruda del
    // `cva`, que a propósito trae las dos.
    const classes = cn(buttonVariants({ variant, size }))
    const minRatio = SIZE_MIN_RATIO[size]

    for (const state of BUTTON_STATES) {
      const inks = effectiveColors(css, classes, 'text-', state)

      // Exactamente una tinta viva, y la correcta. Si `cn` dejara las dos, o
      // ganara la equivocada, el umbral de `sm` y `md` también falla: blanco
      // sobre `#059669` mide 3,77:1.
      expect(inks, `tintas de ${variant}/${size} en ${state}`).toHaveLength(1)
      expect(inks[0]).toBe(utilityColor(css, BUTTON_INK[variant][size]))

      const declaredFills = stateColors(css, classes, 'bg-', state)
      const backdrops = BUTTON_BACKDROPS[variant]

      // Un rol con relleno propio declara exactamente uno por estado.
      if (backdrops.length === 0) {
        expect(declaredFills, `rellenos de ${variant}/${size} en ${state}`).toHaveLength(1)
      }

      // El relleno que se mide es el efectivo: un escalón de brillo declarado en el
      // estado aclara u oscurece el relleno sin cambiar el token, y leerlo sin el
      // filtro dejaría la verificación del hover en verde por lo que la utilidad no
      // declara.
      const fills = (
        declaredFills.length > 0
          ? declaredFills
          : backdrops.map((token) => requiredToken(css, token))
      ).map((fill) => brightenedColor(fill, stateBrightness(classes, state)))

      expect(fills.length).toBeGreaterThan(0)

      for (const fill of fills) {
        expect(
          contrastRatio(fill, inks[0]),
          `${variant}/${size} en ${state}: ${inks[0]} sobre ${fill}`,
        ).toBeGreaterThanOrEqual(minRatio)
      }
    }
  })
})

/*
 * Escalón de hover del rol primario. Los números son medidos en Chromium 155
 * (`canvas` 2D, la misma implementación que el filtro CSS) sobre el relleno
 * `accent-hover` (`#059669`), y el modelo de `brightenedColor` tiene que
 * reproducirlos. El par se mide contra las dos tintas del botón —la blanca de
 * `cta` y `text-primary` de `sm` y `md`— porque el filtro las mueve en sentidos
 * opuestos: el umbral que decide la dirección es el 4,5:1 del texto normal, y
 * oscurecer el relleno acerca la tinta oscura al fondo y la baja a 4,26:1.
 *
 * La ruta de `canvas` trunca al cuantizar; la superficie DOM redondea y deja el
 * relleno una unidad por canal más arriba (ver `PRIMARY_HOVER_DOM_PAIR`). Las dos
 * cuantizaciones cruzan —o no cruzan— los mismos umbrales, así que el veredicto
 * de la tabla no depende de cuál se mida.
 */

type PrimaryHoverStep = {
  /** Escalón de brillo, en porcentaje. */
  step: 95 | 105
  /** Relleno efectivo medido tras ese escalón. */
  fill: string
  /** Ratio medido contra la tinta blanca de `cta` (umbral 3,0). */
  whiteRatio: number
  /** Ratio medido contra la tinta oscura de `sm` y `md` (umbral 4,5). */
  darkRatio: number
}

const PRIMARY_HOVER_STEPS: Record<PrimaryHoverStep['step'], PrimaryHoverStep> = {
  95: { step: 95, fill: '#048e63', whiteRatio: 4.1547, darkRatio: 4.2643 },
  105: { step: 105, fill: '#059d6e', whiteRatio: 3.4686, darkRatio: 5.1078 },
}

const PRIMARY_HOVER_LIST = Object.values(PRIMARY_HOVER_STEPS)

/**
 * Utilidad de un escalón, armada por partes: el nombre completo de una utilidad
 * que el componente no declara bastaba para publicar su regla en el bundle.
 */
function brightnessUtility(step: PrimaryHoverStep['step']): string {
  return `${BRIGHTNESS_PREFIX}${step}`
}

/**
 * Par del hover medido sobre la superficie DOM de Chromium 155 (captura headless
 * decodificada a píxel), no sobre `canvas`: el filtro pinta el elemento entero,
 * así que la tinta oscura también sube un 5% (`#18181B` → `#19191C`), y la
 * cuantización del DOM redondea el relleno una unidad por canal por encima del
 * valor que trunca `canvas` (`#059D6E` → `#059E6E`). Valores medidos, no
 * derivados: el reposo coincide en las dos rutas (3,7682:1 con el blanco y
 * 4,7017:1 con la tinta oscura) porque no lleva filtro.
 */
const PRIMARY_HOVER_DOM_PAIR = {
  fill: '#059e6e',
  ink: '#19191c',
  whiteRatio: 3.4301,
  darkRatio: 5.1141,
} as const

/** Ratio de contraste redondeado a los cuatro decimales de la tabla medida. */
function publishedRatio(background: string, foreground: string): number {
  return Number(contrastRatio(background, foreground).toFixed(4))
}

describe('primary hover step', () => {
  it('wires the hover to the measured clear step and leaves the rest unfiltered', () => {
    const classes = cn(buttonVariants({ variant: 'primary', size: 'cta' }))

    expect(stateBrightness(classes, 'hover')).toBe(1.05)
    expect(stateBrightness(classes, 'rest')).toBe(1)
  })

  it('resolves every brightness step the button declares', () => {
    // Guarda contra la verificación hueca: si una variante suma un escalón que el
    // resolutor no entiende —un valor arbitrario, por ejemplo—, el barrido de
    // contraste de arriba lo ignora en silencio y este caso lo dice.
    const declared: string[] = []
    const unreadable: string[] = []

    for (const variant of Object.keys(BUTTON_INK) as ButtonVariant[]) {
      for (const size of Object.keys(BUTTON_INK[variant]) as ButtonSize[]) {
        for (const candidate of cn(buttonVariants({ variant, size })).split(/\s+/)) {
          if (!candidate.includes(BRIGHTNESS_PREFIX)) continue

          declared.push(candidate)
          if (brightnessFactor(candidate) === undefined) unreadable.push(candidate)
        }
      }
    }

    expect(declared.length).toBeGreaterThan(0)
    expect(unreadable).toEqual([])
  })

  it.each(PRIMARY_HOVER_LIST)(
    'reproduces the measured step $step',
    ({ step, fill, whiteRatio, darkRatio }) => {
      const css = tokenSource()
      const factor = brightnessFactor(brightnessUtility(step))

      if (factor === undefined) throw new Error(`El escalón ${step} no declara un factor`)

      expect(brightenedColor(requiredToken(css, 'accent-hover'), factor)).toBe(fill)
      expect(publishedRatio(fill, '#ffffff')).toBe(whiteRatio)
      expect(publishedRatio(fill, requiredToken(css, 'text-primary'))).toBe(darkRatio)
    },
  )

  it('rejects the darker step, which drops the dark ink below normal text', () => {
    // Veredicto de la tabla, no de la dirección elegida: el escalón oscuro sube
    // contra el blanco y baja contra la tinta oscura, y es el segundo el que
    // rompe AA. Si alguien invierte la dirección «para mejorar el contraste»,
    // esto lo detiene sin depender de un juicio visual.
    const darker = PRIMARY_HOVER_STEPS[95]
    const chosen = PRIMARY_HOVER_STEPS[105]

    expect(darker.darkRatio).toBeLessThan(SIZE_MIN_RATIO.sm)
    expect(darker.whiteRatio).toBeGreaterThanOrEqual(SIZE_MIN_RATIO.cta)
    expect(chosen.darkRatio).toBeGreaterThanOrEqual(SIZE_MIN_RATIO.sm)
    expect(chosen.whiteRatio).toBeGreaterThanOrEqual(SIZE_MIN_RATIO.cta)
  })

  it('holds AA in the real DOM pair, with the ink brightened by the same step', () => {
    // La tabla de arriba mide el relleno contra la tinta declarada; acá se mide lo
    // que la pantalla pinta de verdad, con la tinta aclarada por el mismo filtro.
    // Es la única fila que el modelo de `canvas` no reproduce —redondea el DOM
    // donde `canvas` trunca— y por eso entra como medición aparte.
    const { fill, ink, whiteRatio, darkRatio } = PRIMARY_HOVER_DOM_PAIR

    expect(publishedRatio(fill, ink)).toBe(darkRatio)
    expect(publishedRatio(fill, '#ffffff')).toBe(whiteRatio)
    expect(contrastRatio(fill, ink)).toBeGreaterThanOrEqual(SIZE_MIN_RATIO.sm)
    expect(contrastRatio(fill, '#ffffff')).toBeGreaterThanOrEqual(SIZE_MIN_RATIO.cta)
  })
})

/*
 * Indicador de foco (WCAG 1.4.11, non-text contrast: 3:1). Lo forman dos trazos:
 * el anillo (`focus-visible:outline-*`) y, en los campos, el borde del propio
 * control (`focus-visible:border-*`). Ningún trazo se mide contra una sola
 * superficie: puede dibujarse sobre la tarjeta y sobre el fondo de la aplicación
 * (§2.1:39-40), así que ambas entran a la verificación. Los colores no se copian:
 * se resuelven desde la utilidad que cada primitivo emite y desde la capa de
 * tokens de `src/index.css`, de modo que volver al escalón claro del acento
 * —2,54:1 sobre `#FFFFFF` y 2,31:1 sobre `#F4F4F5`— falle en la suite y no en una
 * revisión visual. El par borde/anillo es una sola decisión de color: dos
 * escalones distintos pintarían dos verdes en el mismo campo enfocado, y eso
 * también se mide acá.
 */

/**
 * Superficies de §2.1 sobre las que puede caer el indicador de foco. El `Record`
 * obliga a que cada superficie del sistema tenga su fila: una superficie nueva sin
 * verificar no compila hasta entrar a la medición.
 */
const FOCUS_SURFACES: Record<'surface-card' | 'surface-bg', true> = {
  'surface-card': true,
  'surface-bg': true,
}

/** Primitivos que dibujan indicador de foco y cuyas clases se resuelven acá. */
type FocusPrimitive = 'Button' | 'Input' | 'DialogContent' | 'DialogClose'

/** Indicador de foco de un primitivo: anillo siempre, borde si lo declara. */
type FocusIndicator = {
  /** Anillo: la primera utilidad `focus-visible:outline-*` con color. */
  ring: string
  /** Borde de foco: la primera utilidad `focus-visible:border-*` con color. */
  border?: string
}

/**
 * Primitivos que, además del anillo, declaran un borde de foco propio. El `Record`
 * obliga a que cada uno tenga su fila, y el valor es `true` y no un booleano, así
 * que la única forma de sacar a un primitivo de la medición es borrar la fila: el
 * conjunto resuelto desde el DOM se compara contra estas claves, de modo que un
 * borde nuevo sin declarar también detiene la suite.
 */
const FOCUS_BORDER_PRIMITIVES: Record<'Input', true> = {
  Input: true,
}

/**
 * Color de la primera utilidad `focus-visible:<propiedad>*` que declare color, o
 * `undefined` si el primitivo no declara esa propiedad.
 */
function focusUtilityColor(
  css: string,
  classes: string,
  property: 'border-' | 'outline-',
): string | undefined {
  for (const candidate of classes.split(/\s+/)) {
    if (!candidate.startsWith(`focus-visible:${property}`)) continue

    const value = utilityColor(css, candidate)

    if (value !== undefined) return value
  }

  return undefined
}

/** Anillo de foco del primitivo; falla si el contrato dejó de declararlo. */
function focusRingColor(css: string, classes: string): string {
  const ring = focusUtilityColor(css, classes, 'outline-')

  if (ring === undefined) {
    throw new Error(`El anillo de foco no declara ninguna utilidad de color outline-*: ${classes}`)
  }

  return ring
}

/** Indicador de foco de una lista de clases: el anillo y, si existe, el borde. */
function focusIndicator(css: string, classes: string): FocusIndicator {
  return {
    ring: focusRingColor(css, classes),
    border: focusUtilityColor(css, classes, 'border-'),
  }
}

/**
 * Indicador de cada primitivo que lo declara, resuelto a sus colores. Los que no
 * exportan su `cva` se leen del DOM, así que la verificación corre sobre la
 * cadena que de verdad llega a la pantalla y no sobre la fuente.
 */
function focusIndicators(): Record<FocusPrimitive, FocusIndicator> {
  const css = tokenSource()

  render(<Input aria-label="Campo con indicador de foco" />)
  const input = screen.getByRole('textbox', { name: 'Campo con indicador de foco' })

  render(
    <Dialog>
      <DialogTrigger>Abrir indicador de foco</DialogTrigger>
      <DialogContent>
        <DialogTitle>Indicador de foco</DialogTitle>
      </DialogContent>
    </Dialog>,
  )
  fireEvent.click(screen.getByRole('button', { name: 'Abrir indicador de foco' }))

  const shell = screen.getByRole('dialog').firstElementChild as HTMLElement

  return {
    Button: focusIndicator(css, cn(buttonVariants({ variant: 'primary', size: 'md' }))),
    Input: focusIndicator(css, input.className),
    DialogContent: focusIndicator(css, shell.className),
    DialogClose: focusIndicator(css, screen.getByLabelText('Cerrar').className),
  }
}

describe('focus indicator contrast', () => {
  it('clears 3:1 on every surface the ring can be drawn on', () => {
    const css = tokenSource()
    const surfaces = Object.keys(FOCUS_SURFACES)

    for (const [component, indicator] of Object.entries(focusIndicators())) {
      for (const surface of surfaces) {
        expect(
          contrastRatio(requiredToken(css, surface), indicator.ring),
          `${component}: anillo ${indicator.ring} sobre ${surface}`,
        ).toBeGreaterThanOrEqual(3)
      }
    }
  })

  it('clears 3:1 with the focus border on the same surfaces', () => {
    const css = tokenSource()
    const surfaces = Object.keys(FOCUS_SURFACES)
    const indicators = focusIndicators()
    const declared = Object.keys(FOCUS_BORDER_PRIMITIVES) as FocusPrimitive[]
    const resolved = (Object.keys(indicators) as FocusPrimitive[])
      .filter((primitive) => indicators[primitive].border !== undefined)
      .sort()

    // Ningún primitivo emite un borde de foco fuera de la tabla: si aparece uno
    // sin declarar, queda medido o la tabla lo rechaza.
    expect(resolved).toEqual([...declared].sort())

    for (const primitive of declared) {
      const { border } = indicators[primitive]

      // La comparación de arriba ya fija el conjunto; el guardia estrecha el tipo.
      if (border === undefined) {
        throw new Error(`${primitive} no emite ninguna utilidad focus-visible:border-*`)
      }

      for (const surface of surfaces) {
        expect(
          contrastRatio(requiredToken(css, surface), border),
          `${primitive}: borde ${border} sobre ${surface}`,
        ).toBeGreaterThanOrEqual(3)
      }
    }
  })

  it('draws every ring with the corrected contract step', () => {
    // Mismo criterio que el relleno del CTA: un primitivo que vuelva al escalón
    // claro del acento detiene la suite sin depender del ojo de quien revisa.
    const css = tokenSource()
    const expected = requiredToken(css, 'accent-hover')

    for (const [component, indicator] of Object.entries(focusIndicators())) {
      expect(indicator.ring, component).toBe(expected)
    }
  })

  it('keeps the focus border on the very same accent step as the ring', () => {
    // La propiedad que el contrato fija: el estado enfocado no divide el acento en
    // dos escalones. Con el borde en `accent-primary` y el anillo en
    // `accent-hover`, el mismo campo mostraba dos verdes distintos al enfocarse.
    const indicators = focusIndicators()

    for (const primitive of Object.keys(FOCUS_BORDER_PRIMITIVES) as FocusPrimitive[]) {
      expect(indicators[primitive].border, `${primitive}: borde de foco`).toBe(
        indicators[primitive].ring,
      )
    }
  })

  it('rejects the lighter accent step, which fails 3:1 on every surface', () => {
    // La prueba de mordida: si el umbral de arriba se relajara, esta medición
    // sigue fijando que `accent-primary` no sirve como anillo en ninguna
    // superficie del sistema.
    const css = tokenSource()
    const lighter = requiredToken(css, 'accent-primary')

    for (const surface of Object.keys(FOCUS_SURFACES)) {
      expect(
        contrastRatio(requiredToken(css, surface), lighter),
        `accent-primary sobre ${surface}`,
      ).toBeLessThan(3)
    }
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
