import { cva, type VariantProps } from 'class-variance-authority'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * Etiqueta corta de estado. Los tonos con nombre propio son los del sistema de
 * tokens de `docs/DESIGN.md` §2.1: el conectado/escaneo (acento), la merma y el
 * fiado (`badge-fiado`) y la identidad de los tenders digitales.
 *
 * El texto del Badge es `text-caption-xs` (10px), que bajo WCAG 2.1 es texto
 * normal: cada tono necesita 4,5:1, no los 3:1 del texto grande. El blanco solo
 * se conserva sobre los fondos que lo admiten.
 *
 * - `fiado` y `accent` llevan texto primario: sobre el ámbar `#F59E0B` el
 *   `#18181B` mide 8,25:1 y sobre el esmeralda `#10B981` mide 6,98:1, mientras
 *   que el blanco se queda en 2,15:1 y 2,54:1.
 * - `neutral` también lleva texto primario: sobre `#E4E4E7` el primario mide
 *   13,96:1 y el secundario se quedaba en 3,81:1.
 * - `plin` conserva el blanco porque el token `--color-badge-plin` bajó a
 *   `#0369A1` (5,93:1); sobre el celeste anterior el blanco medía 4,10:1.
 *
 * `danger`, `yape`, `card` y `other` ya pasaban AA con blanco y no se tocan.
 */
const badgeVariants = cva(
  [
    'inline-flex select-none items-center gap-1',
    'rounded-full px-2 py-0.5',
    'text-caption-xs uppercase',
  ],
  {
    variants: {
      tone: {
        neutral: 'bg-surface-subtle text-text-primary',
        accent: 'bg-accent-primary text-text-primary',
        danger: 'bg-danger-action text-white',
        fiado: 'bg-badge-fiado text-text-primary',
        yape: 'bg-badge-yape text-white',
        plin: 'bg-badge-plin text-white',
        card: 'bg-badge-card text-white',
        other: 'bg-badge-other text-white',
      },
    },
    defaultVariants: {
      tone: 'neutral',
    },
  },
)

export type BadgeProps = ComponentProps<'span'> & VariantProps<typeof badgeVariants>

export function Badge({ className, tone, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />
}

export { badgeVariants }
