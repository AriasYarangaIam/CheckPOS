import { Badge } from '@/components/ui/Badge'
import { useQueuedSaleCount } from '@/hooks/useQueuedSaleCount'
import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'
import { cn } from '@/lib/utils'

/**
 * Marca de ventas pendientes de sincronizar (W5-FE-1; docs/DESIGN.md §5.1:255).
 *
 * La copia es contrato byte a byte: `1 venta por sincronizar` en singular y
 * `N ventas por sincronizar` en plural. El tono es el ámbar `badge-fiado` que
 * §2.1:49 fija para el aviso offline —el mismo del badge de conectividad— y el
 * primitivo `Badge` es el que lo pinta, con la verificación de contraste AA de
 * W1-FE-3.
 *
 * **No interrumpe ni bloquea.** Es un `<span>` de estado con `role="status"`: no
 * recibe foco, no es un control y no abre ninguna ventana. Con cero pendientes
 * no se renderiza nada —un badge que dice «0 ventas» sería ruido en la
 * cabecera—, así que el contador se anuncia solo cuando hay algo que decir.
 */
export const PENDING_SYNC_COPY = 'venta por sincronizar'

/** Singular y plural de la copia; el número nunca cambia la unidad. */
export function pendingSyncText(count: number): string {
  return count === 1 ? `1 ${PENDING_SYNC_COPY}` : `${count} ventas por sincronizar`
}

export interface PendingSyncBadgeProps {
  readonly database?: PosDatabase
  readonly className?: string
}

export function PendingSyncBadge({ database = getPosDatabase(), className }: PendingSyncBadgeProps) {
  const count = useQueuedSaleCount(database)

  if (count === 0) return null

  return (
    <Badge
      className={cn('whitespace-nowrap', className)}
      data-testid="pending-sync-badge"
      role="status"
      tone="fiado"
    >
      {pendingSyncText(count)}
    </Badge>
  )
}
