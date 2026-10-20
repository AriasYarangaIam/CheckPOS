import { Check } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/Button'
import type { SaleCloseOutcome } from '@/sales/saleClose'
import type { TenderMethod } from '@/sales/tenderMethods'
import { TENDER_LABELS } from '@/sales/tenderMethods'
import { formatCentsToSoles } from '@/lib/money'

/**
 * Ticket en pantalla de la venta cerrada (W4-FE-4; docs/DESIGN.md §176,
 * §257; SPRINTS.md:230). Es la verdad local: el total congelado del payload,
 * el tender con su vuelto, el número de operación (el `sale_id` que viajó al
 * servidor) y el estado HONESTO de la entrega —el acuse no dicta si la venta
 * existe—. La acción secundaria es el share sheet NATIVO del sistema: fuera
 * del MVP queda todo camino `wa.me` (SPRINTS.md S6-4), así que sin
 * `navigator.share` el botón directamente no existe.
 */

export interface SaleTicketProps {
  readonly method: TenderMethod
  readonly receivedCents: number
  /** Total congelado del payload: la vista nunca lo recomputa. */
  readonly totalCents: number
  /** El `sale_id` del cierre; sale como número de operación del ticket. */
  readonly operationNumber: string | null
  /** Verdad del envío: acuse, rechazo, desajuste, sin confirmar o sin envío. */
  readonly delivery: SaleCloseOutcome | 'not-sent' | undefined
  /** Inyectable para las pruebas; en producción es `navigator.share`. */
  readonly share?: (data?: ShareData) => Promise<void>
}

/** Línea del tender con el vuelto: la que se muestra y la que comparte. */
function methodLabelOf(method: TenderMethod, receivedCents: number, totalCents: number): string {
  const label = TENDER_LABELS[method]

  if (method === 'fiado') return `Fiado: el cargo queda en la cuenta del cliente.`

  const change = receivedCents - totalCents

  return change > 0
    ? `${label}: recibido ${formatCentsToSoles(receivedCents)}, vuelto ${formatCentsToSoles(change)}.`
    : `${label}: cobro exacto de ${formatCentsToSoles(receivedCents)}.`
}

/** Verdad de entrega del ticket: misma prosa honesta de W4-FE-2. */
function deliveryTextOf(delivery: SaleTicketProps['delivery']): string {
  return delivery === undefined
    ? 'El envío al servidor está en camino.'
    : delivery === 'not-sent'
      ? 'La venta cerró en este dispositivo; el envío queda pendiente hasta que la sesión en línea esté disponible.'
      : delivery.status === 'acknowledged'
        ? 'El servidor confirmó la venta con acuse.'
        : delivery.status === 'mismatch'
          ? 'El servidor detectó esta clave con un cuerpo distinto. La venta siguió cerrada localmente.'
          : delivery.status === 'rejected'
            ? `El servidor rechazó el cuerpo (${delivery.error.code}). La venta siguió cerrada localmente.`
            : 'El envío no quedó confirmado (red o conflicto en vuelo). La venta siguió cerrada localmente.'
}

/**
 * Texto que viaja en el share sheet: los valores congelados tal cual, sin
 * recomputar ni un centavo y sin ningún enlace de WhatsApp.
 */
export function composeTicketShareText(input: {
  readonly method: TenderMethod
  readonly receivedCents: number
  readonly totalCents: number
  readonly operationNumber?: string | null
}): string {
  const lines = [
    'Venta de mostrador',
    `Total: ${formatCentsToSoles(input.totalCents)}`,
    methodLabelOf(input.method, input.receivedCents, input.totalCents),
  ]

  if (input.operationNumber !== null && input.operationNumber !== undefined) {
    lines.push(`N.º de operación: ${input.operationNumber}`)
  }

  return lines.join('\n')
}

/** Patrón háptico del éxito de venta (§257), solo donde hay motor. */
const SALE_SUCCESS_VIBRATION: readonly number[] = [60, 60]

export function SaleTicket({
  method,
  receivedCents,
  totalCents,
  operationNumber,
  delivery,
  share,
}: SaleTicketProps) {
  const [shared, setShared] = useState(false)
  // El share sheet puede venir por prop (prueba) o por el navegador. En un
  // escritorio sin la API el botón no existe: degradar es no invitar.
  const shareImpl = share ?? navigator.share?.bind(navigator)

  useEffect(() => {
    // El mostrador puede ser un equipo sin motor de vibración —y jsdom no lo
    // implementa—: la ausencia se ignora (patrón de la grilla y del acceso).
    const vibrate = navigator.vibrate

    if (typeof vibrate === 'function') {
      vibrate.call(navigator, [...SALE_SUCCESS_VIBRATION])
    }
  }, [])

  async function shareTicket(): Promise<void> {
    if (shareImpl === undefined) return

    try {
      await shareImpl({
        title: 'Venta de mostrador',
        text: composeTicketShareText({
          method,
          receivedCents,
          totalCents,
          operationNumber,
        }),
      })
      setShared(true)
    } catch {
      // El usuario canceló el sheet (AbortError) o el sistema lo denegó: el
      // ticket sigue en pantalla, la venta ya está cerrada de todos modos.
      return
    }
  }

  return (
    <section
      aria-label="Ticket de la venta cerrada"
      className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4"
      data-testid="sale-closed-summary"
    >
      <div className="flex items-center gap-1">
        <Check aria-hidden="true" className="size-5 text-accent-hover" />
        <span className="text-caption-xs font-semibold uppercase tracking-wide text-text-secondary">
          Ticket
        </span>
      </div>
      <span className="text-caption-xs uppercase text-text-secondary">Total</span>
      <p className="font-mono tabular-nums text-display-lg text-text-primary">
        {formatCentsToSoles(totalCents)}
      </p>
      <p className="text-body-sm text-text-primary">{methodLabelOf(method, receivedCents, totalCents)}</p>
      {operationNumber !== null ? (
        <p className="break-all font-mono text-caption-xs text-text-secondary">
          N.º de operación: {operationNumber}
        </p>
      ) : null}
      <p
        className="text-body-sm text-text-secondary"
        data-testid="sale-delivery-status"
        role="status"
      >
        {deliveryTextOf(delivery)}
      </p>
      {shareImpl !== undefined ? (
        <Button data-testid="share-ticket" onClick={() => void shareTicket()}>
          Compartir ticket
        </Button>
      ) : null}
      {shared ? <p role="status">Compartido con el sistema.</p> : null}
    </section>
  )
}
