import { cva, type VariantProps } from 'class-variance-authority'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * Etiqueta corta de estado. Los tonos con nombre propio son los del sistema de
 * tokens de `docs/DESIGN.md` §2.1: el conectado/escaneo (acento), la merma y el
 * fiado (`badge-fiado`) y la identidad de los tenders digitales.
 *
 * El ámbar del fiado lleva texto primario en lugar de blanco: sobre `#F59E0B`
 * el blanco queda en 2.2:1 y no pasa AA, mientras que `#18181B` llega a 8.6:1.
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
        neutral: 'bg-surface-subtle text-text-secondary',
        accent: 'bg-accent-primary text-white',
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
