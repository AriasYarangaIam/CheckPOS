import { formatCentsToSoles } from '@/lib/money'
import { cn } from '@/lib/utils'

export interface MoneyProps {
  /** Importe en centavos enteros (el contrato nunca entrega flotantes). */
  readonly cents: number
  readonly className?: string
}

/**
 * Importe en soles (`S/ 12.34`) del rol numérico-tabular: `JetBrains Mono` con
 * `tabular-nums` y el `tnum` que fija `src/index.css` sobre el rol mono. Los
 * dígitos de ancho constante son lo que sostiene el *Layout Shift = 0* cuando
 * el ticket refresca sus totales (docs/DESIGN.md §2.2, §6.2:300).
 *
 * El formateo vive en `@/lib/money`: acá solo se aplica el rol tipográfico.
 */
export function Money({ cents, className }: MoneyProps) {
  return (
    <span className={cn('font-mono tabular-nums', className)}>
      {formatCentsToSoles(cents)}
    </span>
  )
}
