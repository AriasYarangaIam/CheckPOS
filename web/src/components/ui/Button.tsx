import { cva, type VariantProps } from 'class-variance-authority'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * Rol de pulsación táctil (docs/DESIGN.md §6.2:302): el `:active` baja a
 * `scale(0.98)` con `transition: transform 120ms ease-out`. La transición se
 * declara solo sobre `transform`; `transition: all` está prohibido en todo el
 * sistema (docs/FRONTEND.md:20).
 */
const buttonVariants = cva(
  [
    'inline-flex select-none items-center justify-center gap-2 whitespace-nowrap',
    'rounded-xl font-semibold',
    'transition-transform duration-[120ms] ease-out',
    'active:scale-[0.98]',
    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-primary',
    'disabled:pointer-events-none disabled:opacity-40',
  ],
  {
    variants: {
      variant: {
        // El acento 10% con su resplandor de elevación es el rol del botón
        // principal (docs/DESIGN.md §2.1:46-48, §2.3:77).
        primary: 'bg-accent-primary text-white shadow-glow-emerald hover:bg-accent-hover',
        secondary: 'border border-surface-border bg-surface-subtle text-text-primary hover:bg-zinc-200',
        ghost: 'text-text-primary hover:bg-surface-subtle',
        danger: 'bg-danger-action text-white hover:bg-rose-500',
      },
      size: {
        sm: 'h-9 px-3 text-body-sm',
        md: 'h-11 px-4 text-body-md',
        // Altura táctil mínima del botón principal: 52px (docs/DESIGN.md §1.1:19).
        cta: 'h-[52px] px-6 text-heading-md',
      },
    },
    defaultVariants: {
      variant: 'primary',
      size: 'md',
    },
  },
)

export type ButtonProps = ComponentProps<'button'> & VariantProps<typeof buttonVariants>

export function Button({ className, variant, size, type = 'button', ...props }: ButtonProps) {
  // `type` por defecto `button`: dentro de un formulario, un botón sin tipo es
  // un submit implícito y dispara envíos que nadie pidió.
  return (
    <button className={cn(buttonVariants({ variant, size }), className)} type={type} {...props} />
  )
}

export { buttonVariants }
