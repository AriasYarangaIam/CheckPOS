import { useState } from 'react'

import { CheckoutScreen } from '@/screens/CheckoutScreen'
import { CartPanel } from '@/components/CartPanel'
import { SaleTicket } from '@/components/SaleTicket'
import { Button } from '@/components/ui/Button'
import { cartTotalCents, payableCartLines } from '@/cart/cartStore'
import { useCartLines } from '@/hooks/useCartLines'
import { submitSaleClose, buildSaleClosePayload, type SaleCloseOutcome } from '@/sales/saleClose'
import { enqueueSaleClose, markSaleQueueStatus } from '@/sales/saleQueue'
import type { TenderMethod } from '@/sales/tenderMethods'
import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'
import { formatCentsToSoles } from '@/lib/money'
import { cn } from '@/lib/utils'
import { addIdentifiedLine } from '@/cart/identifiedLine'
import {
  ProductScanner,
  type BarcodeDetectorFactory,
  type ScannerFailure,
} from '@/scanner/ProductScanner'
import { ProductCatalogScreen } from '@/screens/ProductCatalogScreen'

/**
 * Pantalla de venta de mostrador (W4-FE-1/W4-FE-2; docs/DESIGN.md §Módulo 1-2).
 * Es la superficie donde viven la grilla de productos, el carrito y el cobro.
 *
 * **La red no está en el camino crítico del cobro** (ARCHITECTURE.md:98):
 * `Finalizar venta` arma el payload congelado, **persiste el evento en la cola
 * local y vacía el carrito en una sola transacción**, y recién entonces el POST
 * sale con su `Idempotency-Key` (W5-FE-1). El acuse no dicta si la venta existe:
 * solo se dice en pantalla su estado real, y la cola conserva lo que quedó sin
 * confirmar para el badge de la cabecera.
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
  /** Inyectable para las pruebas; en producción, `navigator.mediaDevices`. */
  readonly getUserMedia?: typeof navigator.mediaDevices.getUserMedia
  /** Inyectable para las pruebas; en producción, el `BarcodeDetector` nativo. */
  readonly createBarcodeDetector?: BarcodeDetectorFactory
  /** Inyectable para las pruebas; en producción, `navigator.share` del sheet. */
  readonly share?: (data?: ShareData) => Promise<void>
}

type FlowView = 'sale' | 'checkout' | 'closed'

/**
 * Aviso no modal por cada fallo de cámara (§251-252): la venta nunca queda
 * bloqueada por el escáner y la verdad es específica del motivo.
 */
const SCANNER_FAILURE_NOTICES: Record<ScannerFailure, string> = {
  denied: 'Permiso de cámara denegado. La venta sigue desde la grilla.',
  unavailable: 'La cámara no está disponible. La venta sigue desde la grilla.',
  failed: 'La cámara falló. La venta sigue desde la grilla.',
}

/**
 * Mapa honesto del resultado del envío sobre la fila de la cola (W5-FE-1).
 * `unconfirmed` no toca la fila: la venta sigue `pending` y el badge la cuenta
 * hasta que el envío se pueda confirmar (el motor de sync es de la Semana 6).
 */
async function recordSaleOutcome(
  key: string,
  outcome: SaleCloseOutcome,
  database: PosDatabase,
): Promise<void> {
  if (outcome.status === 'acknowledged') {
    await markSaleQueueStatus(key, 'completed', undefined, database)

    return
  }

  if (outcome.status === 'rejected' || outcome.status === 'mismatch') {
    await markSaleQueueStatus(key, 'failed', outcome.error.code, database)
  }
}

export function CounterSaleScreen({
  database = getPosDatabase(),
  token,
  fetchImpl,
  getUserMedia,
  createBarcodeDetector,
  share,
}: CounterSaleScreenProps) {
  const [view, setView] = useState<FlowView>('sale')
  // §1.2:134 — Productos (grilla) es la vista por defecto; la cámara es el
  // segmento best-effort que el cajero abre a pedido.
  const [segment, setSegment] = useState<'productos' | 'escáner'>('productos')
  const [scannerNotice, setScannerNotice] = useState<string | null>(null)
  // Aviso de la venta que NO se pudo persistir: sin evento local no hay cierre
  // que sobreviva a un crash, así que la pantalla dice la verdad y deja el
  // carrito intacto para reintentar.
  const [closeFailure, setCloseFailure] = useState<string | null>(null)
  const [delivery, setDelivery] = useState<SaleCloseOutcome | 'not-sent' | undefined>(undefined)
  const [closedSale, setClosedSale] = useState<{
    readonly method: TenderMethod
    readonly receivedCents: number
    readonly totalCents: number
    readonly saleId: string
  } | null>(null)
  const lines = useCartLines(database)
  const payable = payableCartLines(lines)
  const total = cartTotalCents(payable)

  /** Fallback no bloqueante: el detalle del motivo y regreso a la grilla. */
  function handleScannerFailure(reason: ScannerFailure): void {
    setSegment('productos')
    setScannerNotice(SCANNER_FAILURE_NOTICES[reason])
  }

  /** La cámara comparte el camino de la grilla: mismo agregado, mismo debounce. */
  function identifyByScan(code: string): void {
    void addIdentifiedLine(code, database)
  }

  async function confirmSale(method: TenderMethod, receivedCents: number): Promise<void> {
    // El payload congela las líneas tal cual; se PLANEA antes de tocar la cola
    // y el carrito para que el evento y el envío sean de la misma venta.
    const payload = buildSaleClosePayload(payable, { paymentMethod: method })
    // La misma clave cubre el evento local y el claim del servidor: reintentar
    // el envío jamás abre una segunda venta.
    const key = crypto.randomUUID()

    /*
     * La escritura va PRIMERO y es atómica (W5-FE-1): fila `pending` +
     * carrito vacío en una transacción. Matar la pestaña entre las dos
     * escrituras no puede perder el evento, y si la escritura falla el carrito
     * sigue ahí — el cobro no se pierde, se reintenta.
     */
    try {
      await enqueueSaleClose(payload, key, database)
    } catch {
      setCloseFailure(
        'La venta no se guardó en el dispositivo. El cobro sigue abierto; intenta de nuevo.',
      )
      setView('sale')

      return
    }

    // Cierre local: es el hecho que el cajero ve, no el acuse remoto. El
    // número de operación del ticket es el sale_id del payload congelado, el
    // mismo que el servidor reclama — válido también sin conexión.
    setCloseFailure(null)
    setClosedSale({
      method,
      receivedCents,
      totalCents: payload.total_cents,
      saleId: payload.sale_id,
    })
    setDelivery(token === undefined ? 'not-sent' : undefined)
    setView('closed')

    if (token === undefined) return

    // El largo del POST no interrumpe nada: la pantalla ya está en el estado
    // cerrado y el acuse llega cuando llega.
    void submitSaleClose({ token, payload, fetchImpl, key })
      .then((outcome) => {
        setDelivery(outcome)
        // El resultado se mapea sobre la fila: acuse → `completed`, rechazo o
        // desajuste → `failed` con el código; sin acuse la fila sigue
        // `pending`, que es lo que el badge cuenta. Un fallo de este mapa no
        // cambia la verdad que el ticket ya mostró.
        void recordSaleOutcome(key, outcome, database)
      })
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
        {closedSale === null ? null : (
          <SaleTicket
            delivery={delivery}
            method={closedSale.method}
            operationNumber={closedSale.saleId}
            receivedCents={closedSale.receivedCents}
            share={share}
            totalCents={closedSale.totalCents}
          />
        )}
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

      {closeFailure === null ? null : (
        <p
          className="rounded-2xl border border-danger-action bg-surface-card p-3 text-body-sm text-text-primary"
          data-testid="close-failure-notice"
          role="alert"
        >
          {closeFailure}
        </p>
      )}

      {/*
       * Aviso no modal del fallo de cámara (§251-252): vive arriba del
       * segmento, no interrumpe ni bloquea, y desaparece al reabrir la cámara.
       */}
      {scannerNotice !== null ? (
        <p
          className="rounded-2xl border border-surface-border bg-zinc-100 p-3 text-body-sm text-text-primary"
          data-testid="scanner-notice"
          role="status"
        >
          {scannerNotice}
        </p>
      ) : null}

      {/* Control de segmentos (§1.2:133): grilla por defecto, escáner a pedido. */}
      <div
        aria-label="Vista de venta"
        className="flex gap-1 rounded-2xl border border-surface-border bg-surface-card p-1"
        role="group"
      >
        {(
          [
            { label: 'Productos', value: 'productos' },
            { label: 'Escáner', value: 'escáner' },
          ] as const
        ).map(({ label, value }) => (
          <button
            key={value}
            type="button"
            aria-pressed={segment === value}
            data-testid={`segment-${value === 'escáner' ? 'escaner' : value}`}
            onClick={() => {
              setSegment(value)
              setScannerNotice(null)
            }}
            className={cn(
              'min-h-12 flex-1 rounded-xl px-3 text-body-md font-semibold',
              segment === value
                ? 'border border-accent-hover bg-accent-primary/10 text-text-primary'
                : 'border border-transparent text-text-secondary hover:bg-zinc-100',
              'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
            )}
          >
            {label}
          </button>
        ))}
      </div>

      {segment === 'escáner' ? (
        <ProductScanner
          createBarcodeDetector={createBarcodeDetector}
          getUserMedia={getUserMedia}
          onProductIdentified={identifyByScan}
          onFailure={handleScannerFailure}
        />
      ) : (
        <ProductCatalogScreen database={database} />
      )}
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
