import { Badge } from '@/components/ui/Badge'
import { useNetworkStatus } from '@/hooks/useNetworkStatus'
import { cn } from '@/lib/utils'

/**
 * Badge de conectividad de la cabecera (docs/DESIGN.md §1.1:127-130; §7.2:489).
 *
 * El texto es contrato byte a byte para las dos copias: `En línea` en verde y
 * `Modo Offline - IndexedDB` en ámbar. El verde sale del acento y el ámbar del
 * token `badge-fiado` (§2.1:49) —el mismo tono del módulo de fiados—; el
 * primitivo `Badge` es el que los pinta, así que la verificación de contraste AA
 * de la unidad W1-FE-3 sigue cubriendo estos dos tonos.
 *
 * **No interrumpe ni bloquea.** Es un `<span>` de estado: no recibe foco, no es
 * un control, no abre ninguna ventana y no aparece encima de nada. §1.1:130 lo
 * dice para el caso offline —el POS «conmuta automáticamente a almacenamiento
 * local sin interrumpir ventas ni alertar con ventanas bloqueantes»— y §5.1:255
 * para la reconexión. El cambio de estado se anuncia de forma cortés
 * (`aria-live="polite"`), que informa sin robar el foco del mostrador.
 *
 * El ancho del badge cambia con la copia, así que la cabecera lo ancla al lado
 * opuesto del wordmark: al crecer, el badge empuja hacia su lado libre y no
 * desplaza al resto de la fila.
 */
export const CONNECTIVITY_COPY = {
  online: 'En línea',
  offline: 'Modo Offline - IndexedDB',
} as const

export interface ConnectivityBadgeProps {
  readonly className?: string
}

export function ConnectivityBadge({ className }: ConnectivityBadgeProps) {
  const { isOnline } = useNetworkStatus()

  return (
    <Badge
      tone={isOnline ? 'accent' : 'fiado'}
      aria-live="polite"
      className={cn('whitespace-nowrap', className)}
    >
      {isOnline ? CONNECTIVITY_COPY.online : CONNECTIVITY_COPY.offline}
    </Badge>
  )
}
