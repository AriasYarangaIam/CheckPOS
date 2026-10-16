import { cva, type VariantProps } from 'class-variance-authority'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * Rol de pulsación táctil (docs/DESIGN.md §6.2:302): el `:active` baja a
 * `scale(0.98)` con `transition: transform 120ms ease-out`. La transición se
 * declara solo sobre `transform`; `transition: all` está prohibido en todo el
 * sistema (docs/FRONTEND.md:20).
 *
 * Contraste (docs/DESIGN.md §2.1:47 y el CTA `COBRAR` de §1.3:153): el relleno
 * del botón principal es `accent-hover` (`#059669`), no `accent-primary`. Sobre
 * `#10B981` el blanco mide 2,54:1 y no alcanza AA ni para texto grande; sobre
 * `#059669` mide 3,77:1, que sí es AA para texto grande, pero no llega a los
 * 4,5:1 del texto normal. En esta escala solo `cta` es texto grande
 * (`text-heading-md`, 20px/700 ≥ 18,66px en negrita): `sm` (`text-body-sm`,
 * 12px/500) y `md` (`text-body-md`, 14px/600) son texto normal y ambos llevan
 * `text-primary` (`#18181B`, 4,70:1 sobre el mismo relleno) vía
 * `compoundVariants`. El resplandor de elevación sigue siendo el de §2.1:48 y
 * §2.3:77 (`shadow-glow-emerald`). El hover repite el relleno en lugar de
 * cambiarlo, porque §1.3:153 fija que «el estado hover/active conserva el
 * relleno»: ningún escalón del acento queda expuesto con la tinta equivocada.
 *
 * `danger` conserva `danger-action` en reposo (blanco: 4,70:1) y baja el hover
 * a `rose-700`: blanco sobre `rose-500` (`#F43F5E`) mide 3,67:1 y se queda corto
 * para el texto normal de `sm` y `md`; sobre `rose-700` (`#BE123C`) mide 6,29:1
 * y mantiene el matiz que el contrato ya usa para este token (§2.1:54,
 * `bg-rose-600`). `secondary` y `ghost` no cambian: sobre `surface-subtle`
 * (`#E4E4E7`) el `text-primary` mide 13,96:1.
 *
 * Anillo de foco (WCAG 1.4.11, non-text contrast: 3:1): el `outline` vive en
 * `accent-hover` (`#059669`) y no en `accent-primary`. El anillo es no textual y
 * el foco puede caer sobre cualquiera de las dos superficies de §2.1:39-40; el
 * escalón claro solo mide 2,54:1 sobre `surface-card` (`#FFFFFF`) y 2,31:1 sobre
 * `surface-bg` (`#F4F4F5`), así que no cruza el piso en ninguna. El oscuro mide
 * 3,77:1 y 3,43:1 y sí lo cruza en ambas. Por eso §2.1:46-47 movió el rol
 * «anillos de foco» de `accent-primary` a `accent-hover`, sin sumar un token
 * nuevo.
 */
const buttonVariants = cva(
  [
    'inline-flex select-none items-center justify-center gap-2 whitespace-nowrap',
    'rounded-xl font-semibold',
    'transition-transform duration-[120ms] ease-out',
    'active:scale-[0.98]',
    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
    'disabled:pointer-events-none disabled:opacity-40',
  ],
  {
    variants: {
      variant: {
        primary: 'bg-accent-hover text-white shadow-glow-emerald hover:bg-accent-hover',
        secondary: 'border border-surface-border bg-surface-subtle text-text-primary hover:bg-zinc-200',
        ghost: 'text-text-primary hover:bg-surface-subtle',
        danger: 'bg-danger-action text-white hover:bg-rose-700',
      },
      size: {
        sm: 'h-9 px-3 text-body-sm',
        md: 'h-11 px-4 text-body-md',
        // Altura táctil mínima del botón principal: 52px (docs/DESIGN.md §1.1:19).
        cta: 'h-[52px] px-6 text-heading-md',
      },
    },
    compoundVariants: [
      // Blanco sobre `#059669` mide 3,77:1: alcanza AA solo como texto grande,
      // así que la regla se limita a los dos tamaños de texto normal. El
      // compuesto va al final de la cadena del `cva` y `cn` conserva la última
      // utilidad de cada grupo, de modo que reemplaza al `text-white` del rol
      // primario.
      { variant: 'primary', size: ['sm', 'md'], class: 'text-text-primary' },
    ],
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
