import { useState } from 'react'

import { ErgonomicNumpad, NUMPAD_DELETE_KEY, type NumpadKey } from '@/components/ErgonomicNumpad'
import { Money } from '@/components/Money'
import type { FiadoAccountRow } from '@/db/posDatabase'
import { searchFiadoAccounts } from '@/fiado/fiadoStore'
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
 * `fiado` cierra con cero recibido.
 *
 * **Deudor en dos toques.** `fiado` no cierra sin deudor: el cajero toca el
 * método y después toca un deudor de la cartera, o lo registra escribiendo el
 * nombre (teléfono opcional) y confirmando. Ese registro viaja con el primer
 * movimiento, porque el contrato no tiene un `POST /fiado-accounts` (decisión 1
 * de forma del sprint 7).
 *
 * Esta pantalla NO envía el POST: entrega (método, recibido, deudor) al
 * hospedador, que arma los payloads congelados y cierra localmente.
 */

/** Billetes rápidos del §2.2, en centavos enteros. */
const QUICK_BILL_CENTS: readonly { label: string; cents: number }[] = [
  { label: 'S/ 10', cents: 1000 },
  { label: 'S/ 20', cents: 2000 },
  { label: 'S/ 50', cents: 5000 },
  { label: 'S/ 100', cents: 10000 },
]

/**
 * Deudor elegido para la venta a fiado. `isNew` distingue el alta —que viaja en
 * el cuerpo del movimiento y el servidor materializa con su upsert atómico— de
 * un deudor que ya está registrado y no necesita reenvío.
 */
export interface CheckoutDebtor {
  readonly id: string
  readonly name: string
  readonly phone: string | null
  readonly isNew: boolean
}

export interface CheckoutScreenProps {
  readonly lines: readonly { readonly name: string; readonly lineTotal: number }[]
  readonly totalCents: number
  /** Se llama al finalizar: fija `sales.payment_method`, el monto recibido y, en fiado, el deudor. */
  readonly onConfirm: (paymentMethod: TenderMethod, receivedCents: number, debtor?: CheckoutDebtor) => void
  readonly onBack?: () => void
  /** Cartera local de deudores para elegir en un toque (W7-FE-1). */
  readonly debtors?: readonly FiadoAccountRow[]
}

export function CheckoutScreen({ lines, totalCents, onConfirm, onBack, debtors = [] }: CheckoutScreenProps) {
  const [method, setMethod] = useState<TenderMethod | null>(null)
  // El monto recibido se tipea en dígitos de centavos: `2500` es S/ 25.00.
  const [receivedEntry, setReceivedEntry] = useState('')
  // Deudor del fiado: la cartera se filtra por nombre o teléfono y el alta se
  // escribe en el mismo lugar (docs/FRONTEND.md:51; docs/PRODUCT.md:27).
  const [debtorQuery, setDebtorQuery] = useState('')
  const [selectedDebtor, setSelectedDebtor] = useState<CheckoutDebtor | null>(null)
  const [registering, setRegistering] = useState(false)
  const [debtorName, setDebtorName] = useState('')
  const [debtorPhone, setDebtorPhone] = useState('')

  const matchingDebtors = searchFiadoAccounts(debtors, debtorQuery).slice(0, 5)

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
  // los demás métodos cobran el monto exacto por definición del tender. El
  // fiado suma su propia precondición: sin deudor no hay a quién cobrarle.
  const canFinish =
    method !== null &&
    (method === 'cash'
      ? receivedCents !== null && receivedCents >= totalCents
      : method === 'fiado'
        ? selectedDebtor !== null
        : true)

  /** Registra un deudor nuevo sin salir del cobro (dos toques: nombre y confirmar). */
  function registerDebtor(): void {
    const name = debtorName.trim()
    if (name === '') return

    setSelectedDebtor({
      id: crypto.randomUUID(),
      name,
      phone: debtorPhone.trim() === '' ? null : debtorPhone.trim(),
      isNew: true,
    })
    setRegistering(false)
  }

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

      {method === 'fiado' ? (
        <section
          aria-labelledby="checkout-debtor-title"
          className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4"
        >
          <h3 className="text-caption-xs uppercase text-text-secondary" id="checkout-debtor-title">
            Deudor
          </h3>

          {selectedDebtor === null ? (
            <>
              {registering ? (
                <div className="flex flex-col gap-2">
                  <input
                    aria-label="Nombre del deudor"
                    autoFocus
                    data-testid="debtor-name"
                    onChange={(event) => setDebtorName(event.target.value)}
                    placeholder="Nombre"
                    type="text"
                    value={debtorName}
                    className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                  />
                  <input
                    aria-label="Teléfono del deudor (opcional)"
                    data-testid="debtor-phone"
                    onChange={(event) => setDebtorPhone(event.target.value)}
                    placeholder="Teléfono (opcional)"
                    type="tel"
                    value={debtorPhone}
                    className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                  />
                  <button
                    type="button"
                    data-testid="debtor-register"
                    disabled={debtorName.trim() === ''}
                    onClick={registerDebtor}
                    className="min-h-12 rounded-xl bg-accent-hover px-3 text-body-md font-semibold text-white disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                  >
                    Registrar deudor
                  </button>
                </div>
              ) : (
                <>
                  <input
                    aria-label="Buscar deudor"
                    data-testid="debtor-search"
                    onChange={(event) => setDebtorQuery(event.target.value)}
                    placeholder="Buscar por nombre o teléfono"
                    type="search"
                    value={debtorQuery}
                    className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                  />
                  {matchingDebtors.length === 0 ? (
                    <p className="text-body-sm text-text-secondary" data-testid="debtor-empty">
                      {debtors.length === 0
                        ? 'Todavía no hay deudores. Registra el primero.'
                        : 'Ningún deudor coincide con la búsqueda.'}
                    </p>
                  ) : (
                    <ul aria-label="Deudores" className="flex flex-col gap-1">
                      {matchingDebtors.map((debtor) => (
                        <li key={debtor.id}>
                          <button
                            type="button"
                            data-testid={`debtor-option-${debtor.id}`}
                            onClick={() =>
                              setSelectedDebtor({
                                id: debtor.id,
                                name: debtor.name,
                                phone: debtor.phone,
                                isNew: false,
                              })
                            }
                            className="flex min-h-12 w-full items-center justify-between gap-2 rounded-xl border border-surface-border px-3 text-left text-body-md text-text-primary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                          >
                            <span className="font-semibold">{debtor.name}</span>
                            <span className="font-mono text-caption-xs tabular-nums text-text-secondary">
                              {debtor.phone ?? 'sin teléfono'}
                            </span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                  <button
                    type="button"
                    data-testid="debtor-new"
                    onClick={() => setRegistering(true)}
                    className="min-h-12 rounded-xl border border-dashed border-surface-border px-3 text-body-md text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                  >
                    Nuevo deudor
                  </button>
                </>
              )}
            </>
          ) : (
            <div className="flex items-center justify-between gap-2">
              <span className="flex flex-col">
                <span className="text-body-md font-semibold text-text-primary" data-testid="debtor-selected">
                  {selectedDebtor.name}
                </span>
                <span className="font-mono text-caption-xs tabular-nums text-text-secondary">
                  {selectedDebtor.phone ?? 'sin teléfono'}
                  {selectedDebtor.isNew ? ' · se registra con este cargo' : ''}
                </span>
              </span>
              <button
                type="button"
                data-testid="debtor-change"
                onClick={() => {
                  setSelectedDebtor(null)
                  setDebtorQuery('')
                }}
                className="min-h-9 rounded-lg px-2 text-body-sm text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
              >
                Cambiar
              </button>
            </div>
          )}
        </section>
      ) : null}

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
              ? 'El cobro queda en cuenta corriente del deudor elegido.'
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

          if (method === 'fiado') {
            onConfirm(method, 0, selectedDebtor ?? undefined)

            return
          }

          onConfirm(method, method === 'cash' ? (receivedCents ?? 0) : totalCents)
        }}
        className="min-h-[52px] rounded-xl bg-accent-hover text-heading-md font-bold text-white shadow-glow-emerald transition-transform duration-[120ms] ease-out hover:brightness-105 active:scale-[0.98] disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
      >
        Finalizar venta
      </button>
    </section>
  )
}
