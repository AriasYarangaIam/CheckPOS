import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

/**
 * Nombres propios de la escala tipográfica de `docs/DESIGN.md` §2.2. Hay que
 * registrarlos: sin esto `tailwind-merge` no puede distinguir un tamaño
 * (`text-display-xl`) de un color de texto (`text-text-primary`) y descarta uno
 * de los dos al fusionar.
 */
const FONT_SIZE_TOKENS = [
  'display-xl',
  'display-lg',
  'heading-md',
  'body-md',
  'body-sm',
  'caption-xs',
] as const

/**
 * Nombres propios de elevación y de animación declarados en `@theme`. Se
 * registran para que sigan siendo sobrescribibles (`shadow-none`,
 * `animate-none`) en lugar de quedar fuera de todo grupo y sobrevivir a la
 * fusión.
 */
const SHADOW_TOKENS = ['glow-emerald'] as const
const ANIMATE_TOKENS = ['modal-spring', 'fade-in'] as const

const twMerge = extendTailwindMerge({
  extend: {
    theme: {
      shadow: [...SHADOW_TOKENS],
      animate: [...ANIMATE_TOKENS],
    },
    classGroups: {
      'font-size': [{ text: [...FONT_SIZE_TOKENS] }],
    },
  },
})

/**
 * Une clases condicionales (`clsx`) y resuelve los conflictos de la capa de
 * utilidades quedándose con la última (`tailwind-merge`), para que un
 * `className` externo pueda sobrescribir el rol por defecto de un componente
 * (por ejemplo el tamaño tipográfico de un importe).
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
