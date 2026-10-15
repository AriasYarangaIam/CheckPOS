import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * Campo de texto base. No lleva transiciones: la barra de búsqueda del catálogo
 * (F2) y el numpad son acciones de latencia cero por contrato
 * (docs/DESIGN.md §6.2:298-299).
 *
 * La altura mínima táctil es la del sistema (44px en `md`, 52px en el rol del
 * botón principal); la escala no baja de `body-md` (14px) y no hace falta
 * subirla, porque la PWA es Android-first (ARCHITECTURE.md:3) y Chrome para
 * Android no amplía el viewport al enfocar un campo.
 */
export function Input({ className, type = 'text', ...props }: ComponentProps<'input'>) {
  return (
    <input
      className={cn(
        'h-11 w-full rounded-xl border border-surface-border bg-surface-card px-3',
        'text-body-md text-text-primary placeholder:text-text-muted',
        'focus-visible:border-accent-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-primary',
        'disabled:pointer-events-none disabled:opacity-40',
        className,
      )}
      type={type}
      {...props}
    />
  )
}
