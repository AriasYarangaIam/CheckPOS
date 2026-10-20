import { useState } from 'react'

import { CheckoutScreen } from '@/screens/CheckoutScreen'
import { CartPanel } from '@/components/CartPanel'
import { Button } from '@/components/ui/Button'
import { cartTotalCents, emptyCart, payableCartLines } from '@/cart/cartStore'
import { useCartLines } from '@/hooks/useCartLines'
import { submitSaleClose, buildSaleClosePayload, type SaleCloseOutcome } from '@/sales/saleClose'
import type { TenderMethod } from '@/sales/tenderMethods'
import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'
import { formatCentsToSoles } from '@/lib/money'
import { TENDER_LABELS } from '@/sales/tenderMethods'
import { ProductCatalogScreen } from '@/screens/ProductCatalogScreen'

/**
 * Pantalla de venta de mostrador (W4-FE-1/W4-FE-2; docs/DESIGN.md §Módulo 1-2).
 * Es la superficie donde viven la grilla de productos, el carrito y el cobro.
 *
 * **La red no está en el camino crítico del cobro** (ARCHITECTURE.md:98):
 * `Finalizar venta` arma el payload congelado, cierra con la venta
 * localmente (carrito vaciado) y el POST sale DESPUÉS con su
 * `Idempotency-Key`. El acuse no dicta si la venta existe: solo se dice en
 * pantalla su estado real.
 *
 * Sin token disponibles (el desbloqueo por PIN no restaura sesión en línea)
 * el POST no se dispara: el estado honesto de la entrega queda «sin
 * confirmar» y la venta cierra localmente de todos modos.
 */
export interface CounterSaleScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
  /** Access token de la sesión en línea; sin él el POST no se dispara. */
  readonly token?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
}

type FlowView = 'sale' | 'checkout' | 'closed'

/** Línea del resumen con el tender y el vuelto de la venta cerrada. */
function methodLabelOf(
  method: TenderMethod,
  receivedCents: number,
  totalCents: number,
): string {
  const label = TENDER_LABELS[method]

  if (method === 'fiado') return `Fiado: el cargo queda en la cuenta del cliente.`

  const change = receivedCents - totalCents

  return change > 0
    ? `${label}: recibido ${formatCentsToSoles(receivedCents)}, vuelto ${formatCentsToSoles(change)}.`
    : `${label}: cobro exacto de ${formatCentsToSoles(receivedCents)}.`
}

export function CounterSaleScreen({
  database = getPosDatabase(),
  token,
  fetchImpl,
}: CounterSaleScreenProps) {
  const [view, setView] = useState<FlowView>('sale')
  const [delivery, setDelivery] = useState<SaleCloseOutcome | 'not-sent' | undefined>(undefined)
  const [closedSale, setClosedSale] = useState<{
    readonly method: TenderMethod
    readonly receivedCents: number
    readonly totalCents: number
  } | null>(null)
  const lines = useCartLines(database)
  const payable = payableCartLines(lines)
  const total = cartTotalCents(payable)

  async function confirmSale(method: TenderMethod, receivedCents: number): Promise<void> {
    // El payload congela las líneas tal cual; se PLANEA antes de vaciar el
    // carrito para que el cierre y el POST sean de la misma venta.
    const payload = buildSaleClosePayload(payable, { paymentMethod: method })

    // Cierre local: es el hecho que el cajero ve, no el acuse remoto.
    setClosedSale({
      method,
      receivedCents,
      totalCents: payload.total_cents,
    })
    setDelivery(token === undefined ? 'not-sent' : undefined)
    setView('closed')
    await emptyCart(database)

    if (token === undefined) return

    // El largo del POST no interrumpe nada: la pantalla ya está en el estado
    // cerrado y el acuse llega cuando llega.
    void submitSaleClose({ token, payload, fetchImpl })
      .then((outcome) => setDelivery(outcome))
      .catch(() => setDelivery({ status: 'unconfirmed' }))
  }

  if (view === 'checkout') {
    return (
      <CheckoutScreen
        lines={payable.map((line) => ({ name: line.name, lineTotal: cartTotalCents([line]) }))}
        totalCents={total}
        onBack={() => setView('sale')}
        onConfirm={confirmSale}
      />
    )
  }

  if (view === 'closed') {
    return (
      <section
        aria-labelledby="sale-closed-title"
        className="mx-auto flex w-full max-w-md flex-col gap-3 p-4"
      >
        <h2 className="text-heading-md font-bold" id="sale-closed-title">
          Venta cerrada
        </h2>
        <div data-testid="sale-closed-summary" className="rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Total</span>
          <p className="font-mono tabular-nums text-display-lg text-text-primary">
            {closedSale === null ? '' : formatCentsToSoles(closedSale.totalCents)}
          </p>
          <p className="text-body-sm text-text-secondary">
            {closedSale === null
              ? null
              : methodLabelOf(closedSale.method, closedSale.receivedCents, closedSale.totalCents)}
          </p>
          <p className="text-body-sm text-text-secondary">
            {delivery === undefined
              ? 'El envío al servidor está en camino.'
              : delivery === 'not-sent'
                ? 'La venta cerró en este dispositivo; el envío queda pendiente hasta que la sesión en línea esté disponible.'
                : delivery.status === 'acknowledged'
                  ? 'El servidor confirmó la venta con acuse.'
                  : delivery.status === 'mismatch'
                    ? 'El servidor detectó esta clave con un cuerpo distinto. La venta siguió cerrada localmente.'
                    : delivery.status === 'rejected'
                      ? `El servidor rechazó el cuerpo (${delivery.error.code}). La venta siguió cerrada localmente.`
                      : 'El envío no quedó confirmado (red o conflicto en vuelo). La venta siguió cerrada localmente.'}
          </p>
        </div>
        <Button onClick={() => setView('sale')}>Nueva venta</Button>
      </section>
    )
  }

  return (
    <section
      aria-labelledby="counter-sale-title"
      className="mx-auto flex w-full max-w-md flex-col gap-4 p-4"
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="counter-sale-title">
          Punto de venta
        </h2>
        <p className="text-body-sm text-text-secondary">
          Un toque en la grilla agrega la línea; el carrito queda guardado en el dispositivo
        </p>
      </div>

      <ProductCatalogScreen database={database} />
      <CartPanel database={database} />

      {/*
       * Barra inferior persistente (§1.3): resumen del carrito y el COBRAR
       * con el total en tipografía tabular. Deshabilitado sin líneas: una
       * venta cierra con al menos una línea no anulada (§4.9).
       */}
      <div className="flex items-center justify-between gap-2 rounded-2xl border border-surface-border bg-surface-card p-3">
        <span className="text-body-md text-text-secondary">
          {payable.length === 1 ? '1 línea' : `${payable.length} líneas`}
          {` de ${formatCentsToSoles(total)}`}
        </span>
        <Button size="cta" data-testid="cobrar-button" disabled={payable.length === 0} onClick={() => setView('checkout')}>
          COBRAR
        </Button>
      </div>
    </section>
  )
}
