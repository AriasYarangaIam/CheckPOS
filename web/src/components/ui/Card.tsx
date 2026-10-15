import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * Superficie de contenido del 60/30/10: tarjeta blanca sobre el fondo neutro,
 * con el radio de 16px y el borde sutil del sistema de tokens
 * (docs/DESIGN.md §2.1:40-42, §2.3:72).
 */
export function Card({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={cn(
        'rounded-2xl border border-surface-border bg-surface-card shadow-xs',
        className,
      )}
      {...props}
    />
  )
}

export function CardHeader({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex flex-col gap-1 px-4 pt-4', className)} {...props} />
}

export function CardTitle({ className, ...props }: ComponentProps<'h3'>) {
  return <h3 className={cn('text-heading-md text-text-primary', className)} {...props} />
}

export function CardContent({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('px-4 pb-4 text-body-md text-text-secondary', className)} {...props} />
}
