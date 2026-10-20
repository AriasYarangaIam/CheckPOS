import { useState } from 'react'

import { ErgonomicNumpad, NUMPAD_DELETE_KEY, type NumpadKey } from '@/components/ErgonomicNumpad'
import { Money } from '@/components/Money'
import { formatCentsToSoles } from '@/lib/money'
import { TENDER_LABELS, TENDER_METHODS, type TenderMethod } from '@/sales/tenderMethods'
import { cn } from '@/lib/utils'
/**
 * Pantalla de cobro (W4-FE-2; docs/DESIGN.md §Módulo 2, SPRINTS.md W4-FE-2).
 *
 * **Tender único.** Seis tarjetas alineadas con el CHECK de
 * `sales.payment_method` (`cash, yape, plin, card, other, fiado`); elegir un
 * método fija el valor exacto que el POST envía y NO existe camino de split
 * tender: la selección es una sola (`aria-pressed` única).
 *
 * **Vuelto.** Solo `cash` activa la calculadora de vuelto con los chips de
 * billetes rápidos y el card gigante; los otros métodos cobran el monto
 * exacto (Yape/Plin confirman recepción, tarjeta termina en la terminal) y
 * `fiado` cierra con cero recibido — el deudor se registra en Semana 7.
 *
 * Esta pantalla NO envía el POST: entrega (método, recibido) al hospedador,
 * que arma el payload congelado y cierra localmente.
 */

/** Billetes rápidos del §2.2, en centavos enteros. */
const QUICK_BILL_CENTS: readonly { label: string; cents: number }[] = [
  { label: 'S/ 10', cents: 1000 },
  { label: 'S/ 20', cents: 2000 },
  { label: 'S/ 50', cents: 5000 },
  { label: 'S/ 100', cents: 10000 },
]

export interface CheckoutScreenProps {
  readonly lines: readonly { readonly name: string; readonly lineTotal: number }[]
  readonly totalCents: number
  /** Se llama al finalizar: fija `sales.payment_method` y el monto recibido. */
  readonly onConfirm: (paymentMethod: TenderMethod, receivedCents: number) => void
  readonly onBack?: () => void
}

export function CheckoutScreen({ lines, totalCents, onConfirm, onBack }: CheckoutScreenProps) {
  const [method, setMethod] = useState<TenderMethod | null>(null)
  // El monto recibido se tipea en dígitos de centavos: `2500` es S/ 25.00.
  const [receivedEntry, setReceivedEntry] = useState('')

  const receivedCents =
    method === 'fiado' ? 0 : receivedEntry === '' ? null : Number(receivedEntry)
  // El vuelto es la pantalla gigante del §2.2: existe desde que hay método
  // elegido — con efectivo espera el monto tipeado, con los medios exactos es
  // cero de una vez.
  const changeCents =
    method === null
      ? null
      : method === 'cash'
        ? receivedCents === null
          ? null
          : receivedCents - totalCents
        : 0
  // El cierre exige vuelto posible: efectivo necesita un recibido ≥ total, y
  // los demás métodos cobran el monto exacto por definición del tender.
  const canFinish =
    method !== null &&
    (method === 'cash'
      ? receivedCents !== null && receivedCents >= totalCents
      : true)

  function pressReceived(key: NumpadKey): void {
    if (key === NUMPAD_DELETE_KEY) {
      setReceivedEntry((entry) => entry.slice(0, -1))

      return
    }

    setReceivedEntry((entry) => `${entry}${key}`.slice(0, 9))
  }

  return (
    <section
      aria-labelledby="checkout-title"
      className="mx-auto flex w-full max-w-md flex-col gap-3 p-4"
    >
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-heading-md font-bold" id="checkout-title">
          Cobrar
        </h2>
        {onBack !== undefined ? (
          <button
            type="button"
            onClick={onBack}
            className="min-h-9 rounded-lg px-2 text-body-md text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            Volver
          </button>
        ) : null}
      </div>

      <div className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
        <span className="text-caption-xs uppercase text-text-secondary">Total a cobrar</span>
        <span data-testid="checkout-total" className="font-mono tabular-nums text-heading-md text-text-primary">
          <Money cents={totalCents} />
        </span>
      </div>

      {/*
       * Selector de tender único (§2.1): cada tarjeta es un toggle exclusivo —
       * elegir una reemplaza a la anterior, nunca acumula dos medios.
       */}
      <div role="group" aria-label="Método de pago" className="grid grid-cols-3 gap-2">
        {TENDER_METHODS.map((value) => (
          <button
            key={value}
            type="button"
            data-testid={`tender-method-${value}`}
            data-method={value}
            aria-pressed={method === value}
            onClick={() => setMethod(value)}
            className={cn(
              'flex min-h-12 items-center justify-center rounded-xl border px-2 text-body-md font-semibold',
              'transition-transform duration-[120ms] ease-out active:scale-[0.98]',
              method === value
                ? 'border-accent-hover bg-accent-primary/10 text-text-primary'
                : 'border-surface-border bg-surface-card text-text-secondary hover:bg-zinc-100',
              'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
            )}
          >
            {TENDER_LABELS[value]}
          </button>
        ))}
      </div>

      {method === 'cash' ? (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap gap-2">
            {QUICK_BILL_CENTS.map((bill) => (
              <button
                key={bill.label}
                type="button"
                onClick={() => {
                  setReceivedEntry(String(receivedEntry === '' ? bill.cents : Number(receivedEntry) + bill.cents))
                }}
                className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 font-mono tabular-nums text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
              >
                {bill.label}
              </button>
            ))}
            <button
              type="button"
              onClick={() => setReceivedEntry(String(totalCents))}
              className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 font-mono tabular-nums text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              Exacto
            </button>
          </div>

          <div className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
            <span className="text-caption-xs uppercase text-text-secondary">Monto recibido</span>
            <span data-testid="checkout-received" className="font-mono tabular-nums text-heading-md text-text-primary">
              <Money cents={receivedEntry === '' ? 0 : Number(receivedEntry)} />
            </span>
          </div>

          <ErgonomicNumpad onKey={pressReceived} />
        </div>
      ) : method !== null ? (
        <div className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Monto recibido</span>
          <span data-testid="checkout-received" className="font-mono tabular-nums text-heading-md text-text-primary">
            <Money cents={method === 'fiado' ? 0 : totalCents} />
          </span>
          <p className="text-body-sm text-text-secondary">
            {method === 'fiado'
              ? 'El cobro queda en cuenta corriente del cliente.'
              : 'El cobro se registra por el importe exacto de la venta.'}
          </p>
        </div>
      ) : null}

      <ul aria-label="Detalle de la venta" className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
        {lines.map((line, index) => (
          <li key={index} className="flex justify-between gap-2 text-body-sm text-text-secondary">
            <span>{line.name}</span>
            <span className="font-mono tabular-nums">{formatCentsToSoles(line.lineTotal)}</span>
          </li>
        ))}
      </ul>

      {/*
       * Pantalla gigante de vuelto (§2.2): tarjeta verde suave con el número
       * en JetBrains Mono grande y tabular. Con efectivo espera el monto
       * tipeado; los medios exactos muestran el vuelto S/ 0.00 de una vez.
       */}
      {changeCents !== null ? (
        <div
          data-testid="checkout-change"
          className="flex flex-col gap-1 rounded-2xl border border-emerald-500 bg-emerald-50 p-4"
        >
          <span className="text-caption-xs uppercase text-text-secondary">Vuelto</span>
          <Money cents={changeCents} className="text-display-lg text-text-primary" />
        </div>
      ) : null}

      <button
        type="button"
        disabled={!canFinish}
        onClick={() => {
          if (method === null) return

          onConfirm(method, method === 'cash' ? (receivedCents ?? 0) : method === 'fiado' ? 0 : totalCents)
        }}
        className="min-h-[52px] rounded-xl bg-accent-hover text-heading-md font-bold text-white shadow-glow-emerald transition-transform duration-[120ms] ease-out hover:brightness-105 active:scale-[0.98] disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
      >
        Finalizar venta
      </button>
    </section>
  )
}
