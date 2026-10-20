import { useEffect, useRef, useState } from 'react'

import { ErgonomicNumpad, NUMPAD_DELETE_KEY, type NumpadKey } from '@/components/ErgonomicNumpad'
import { Button } from '@/components/ui/Button'
import { getPosDatabase, type CashCloseRow, type PosDatabase } from '@/db/posDatabase'
import { formatCentsToSoles } from '@/lib/money'
import { cn } from '@/lib/utils'
import {
  CASH_CLOSE_LABELS,
  CASH_CLOSE_METHODS,
  acknowledgeCashClose,
  buildCashClosePayload,
  cashCloseLinesOf,
  closeTotalSummary,
  countLine,
  expectedByMethod,
  persistCashClose,
  readCashClose,
  submitCashClose,
  type CashCloseCountLine,
  type CashCloseOutcome,
  type ExpectedByMethod,
} from '@/sales/cashClose'
import { formatBusinessDate } from '@/sales/saleClose'
import { readQueuedSales } from '@/sales/saleQueue'
import type { TenderMethod } from '@/sales/tenderMethods'

/**
 * Arqueo y cierre de caja (W5-FE-2; docs/DESIGN.md §3.2:194-198; SPRINTS.md
 * W5-FE-2). Una fila por método —`expected` / `counted` / `difference`— sobre
 * los seis valores del CHECK, y la fila de `fiado` bloqueada en cero con su
 * diferencia informacional: nunca un faltante (§4.13, F32).
 *
 * **El esperado es la proyección del ledger local de ESA fecha de negocio**, no
 * un recálculo de líneas ni un saldo corriente (decisión 7 del sprint). El
 * acuse del servidor congela el suyo y, cuando llega, reemplaza al local en
 * pantalla: el servidor manda.
 *
 * **Sin fondo inicial ni egresos.** `cash_closes` no tiene esas columnas y la
 * fórmula de diferencia no tiene término de egreso en esta versión
 * (`docs/DATABASE.md` §4 D10; `docs/BACKEND.md` §11.2).
 *
 * **El cierre local se escribe antes del envío** y la fecha de negocio es su
 * clave: reabrir el arqueo de una fecha ya cerrada muestra el cierre guardado en
 * lugar de un formulario editable (F37). Un duplicado no es un error: el cierre
 * que ya existía prevalece y la pantalla lo dice sin modal (F38).
 */

export interface CashCloseScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
  /** Access token de la sesión en línea; sin él el cierre no se puede enviar. */
  readonly token?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Reloj de la fecha de negocio, inyectable para las pruebas. */
  readonly now?: () => Date
  /** Vuelta al punto de venta; el navigation sheet lo engancha en W5-FE-3. */
  readonly onBack?: () => void
}

/** Entrada vacía de cada método: los dígitos que el cajero va tipeando. */
type CountEntries = Record<TenderMethod, string>

function emptyEntries(): CountEntries {
  return { cash: '', yape: '', plin: '', card: '', other: '', fiado: '' }
}

function emptyExpected(): ExpectedByMethod {
  return { cash: 0, yape: 0, plin: 0, card: 0, other: 0, fiado: 0 }
}

/**
 * Texto de la diferencia. El signo es explícito para el sobrante y el faltante
 * conserva el guion de `formatCentsToSoles`; el fiado no llega acá porque su
 * fila se rotula «Informativo».
 */
function differenceText(cents: number): string {
  if (cents > 0) return `+${formatCentsToSoles(cents)}`

  return formatCentsToSoles(cents)
}

/** Verdad de entrega del cierre: acuse, duplicado, rechazo o sin confirmar. */
function deliveryTextOf(outcome: CashCloseOutcome | null, acknowledged: boolean): string {
  if (outcome === null) {
    return acknowledged
      ? 'El servidor confirmó este cierre.'
      : 'El cierre está guardado en este dispositivo; el envío no está confirmado.'
  }

  switch (outcome.status) {
    case 'acknowledged':
      return 'El servidor confirmó el cierre con acuse.'
    case 'duplicate':
      return 'El cierre de esta fecha ya estaba registrado; se muestra el guardado.'
    case 'rejected':
      return `El servidor rechazó el cierre (${outcome.error.code}). El cierre quedó guardado en este dispositivo.`
    case 'unconfirmed':
      return 'El cierre quedó guardado en este dispositivo; el envío no se pudo confirmar.'
  }
}

export function CashCloseScreen({
  database = getPosDatabase(),
  token,
  fetchImpl,
  now,
  onBack,
}: CashCloseScreenProps) {
  // La fecha de negocio se fija al abrir el arqueo: es LOCAL al mostrador y no
  // se re-deriva del reloj en cada render (`toISOString` la movería de día).
  const [closedOn] = useState(() => formatBusinessDate((now ?? (() => new Date()))()))
  const [expected, setExpected] = useState<ExpectedByMethod>(emptyExpected)
  // `undefined` es «todavía leo el cierre guardado»; `null` es «esta fecha está
  // abierta». La distinción evita mostrar un formulario que después se bloquea.
  const [stored, setStored] = useState<CashCloseRow | null | undefined>(undefined)
  const [entries, setEntries] = useState<CountEntries>(emptyEntries)
  const [selected, setSelected] = useState<TenderMethod>('cash')
  const [submitting, setSubmitting] = useState(false)
  const [outcome, setOutcome] = useState<CashCloseOutcome | null>(null)
  const [storageError, setStorageError] = useState<string | null>(null)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true

    void Promise.all([readQueuedSales(database), readCashClose(closedOn, database)])
      .then(([events, row]) => {
        if (!mounted.current) return

        setExpected(expectedByMethod(events, closedOn))
        setStored(row)
      })
      .catch(() => {
        // Sin IndexedDB no hay proyección ni cierre guardado: la fecha se trata
        // como abierta y el envío fallará con su estado honesto.
        if (mounted.current) setStored(null)
      })

    return () => {
      mounted.current = false
    }
  }, [database, closedOn])

  const countedOf = (method: TenderMethod): number =>
    entries[method] === '' ? 0 : Number(entries[method])

  const draftLines: readonly CashCloseCountLine[] = CASH_CLOSE_METHODS.map((method) =>
    countLine(method, expected[method], countedOf(method)),
  )
  const summary = closeTotalSummary(draftLines)
  const readOnly = stored !== null && stored !== undefined
  const lines = readOnly ? cashCloseLinesOf(stored) : draftLines
  const summaryOfView = readOnly ? closeTotalSummary(lines) : summary

  function press(key: NumpadKey): void {
    if (submitting || readOnly) return

    if (key === NUMPAD_DELETE_KEY) {
      setEntries((current) => ({ ...current, [selected]: current[selected].slice(0, -1) }))

      return
    }

    // Dígitos de centavos, igual que el monto recibido del cobro: `2500` es
    // S/ 25.00. El tope de 9 dígitos es el del cobro y no desborda un entero.
    setEntries((current) => ({
      ...current,
      [selected]: `${current[selected]}${key}`.slice(0, 9),
    }))
  }

  async function closeCash(): Promise<void> {
    if (token === undefined || submitting) return

    const counted = CASH_CLOSE_METHODS.reduce<Record<TenderMethod, number>>(
      (totals, method) => {
        totals[method] = countedOf(method)

        return totals
      },
      emptyExpected() as Record<TenderMethod, number>,
    )
    const payload = buildCashClosePayload(counted, { now })
    const localLines = CASH_CLOSE_METHODS.map((method) =>
      countLine(method, expected[method], counted[method]),
    )

    setSubmitting(true)
    setStorageError(null)

    let localRow: CashCloseRow

    /*
     * El cierre local va PRIMERO: si la pestaña muere a mitad del envío, la
     * fecha queda cerrada con su proyección y el arqueo vuelve en lectura.
     */
    try {
      localRow = await persistCashClose(payload, localLines, database)
    } catch {
      if (mounted.current) {
        setSubmitting(false)
        setStorageError('El cierre no se pudo guardar en el dispositivo. Intenta de nuevo.')
      }

      return
    }

    if (!mounted.current) return

    const result = await submitCashClose({ token, payload, fetchImpl, key: payload.id })

    if (!mounted.current) return

    if (result.status === 'acknowledged' || result.status === 'duplicate') {
      // El acuse reemplaza la proyección local: el esperado congelado del
      // servidor y su diferencia pasan a ser lo que se muestra y se guarda.
      const acknowledged = await acknowledgeCashClose(result.ack, database).catch(() => localRow)

      if (!mounted.current) return

      setStored(acknowledged)
    } else {
      setStored(localRow)
    }

    setOutcome(result)
    setSubmitting(false)
  }

  return (
    <section
      aria-labelledby="cash-close-title"
      className="mx-auto flex w-full max-w-md flex-col gap-3 p-4"
    >
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-heading-md font-bold" id="cash-close-title">
          Cierre de caja
        </h2>
        {onBack === undefined ? null : (
          <button
            type="button"
            onClick={onBack}
            className="min-h-9 rounded-lg px-2 text-body-md text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            Volver
          </button>
        )}
      </div>

      <p className="text-body-sm text-text-secondary">
        Arqueo de {closedOn}. Sin fondo inicial ni egresos en esta versión.
      </p>

      {stored === undefined ? (
        <p className="text-body-md text-text-secondary" role="status">
          Leyendo el cierre de la fecha…
        </p>
      ) : (
        <>
          <div
            aria-hidden="true"
            className="grid grid-cols-[minmax(0,1fr)_auto_auto_auto] gap-x-2 px-3 text-caption-xs uppercase text-text-secondary"
          >
            <span>Método</span>
            <span className="text-right">Esperado</span>
            <span className="text-right">Contado</span>
            <span className="text-right">Diferencia</span>
          </div>

          <ul aria-label="Arqueo por método" className="flex flex-col gap-1">
            {CASH_CLOSE_METHODS.map((method) => {
              // Una fila guardada que no traiga el método se lee como cero: el
              // rótulo «Informativo» es solo del fiado, no de un dato faltante.
              const line = lines.find((entry) => entry.payment_method === method) ??
                countLine(method, 0, 0)
              const isFiado = method === 'fiado'
              const content = (
                <RowContent
                  expectedCents={line.expected_cents}
                  method={method}
                  countedCents={line.counted_cents}
                  differenceCents={line.difference_cents}
                />
              )

              return (
                <li
                  key={method}
                  aria-disabled={isFiado ? 'true' : undefined}
                  data-label={CASH_CLOSE_LABELS[method]}
                  data-method={method}
                  data-testid={`cash-close-row-${method}`}
                >
                  {readOnly || isFiado ? (
                    // Lectura (o fiado): no hay botón que seleccione la fila. La
                    // diferencia del fiado nunca se edita ni se cuenta.
                    <div
                      className={cn(
                        'flex min-h-14 items-center rounded-xl border border-surface-border bg-surface-card px-3',
                        isFiado ? 'opacity-70' : '',
                      )}
                    >
                      {content}
                    </div>
                  ) : (
                    <button
                      type="button"
                      aria-pressed={selected === method}
                      onClick={() => setSelected(method)}
                      className={cn(
                        'flex min-h-14 w-full items-center rounded-xl border px-3',
                        'transition-transform duration-[120ms] ease-out active:scale-[0.99]',
                        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                        selected === method
                          ? 'border-accent-hover bg-accent-primary/10'
                          : 'border-surface-border bg-surface-card hover:bg-zinc-100',
                      )}
                    >
                      {content}
                    </button>
                  )}
                </li>
              )
            })}
          </ul>

          {storageError === null ? null : (
            <p
              className="rounded-xl border border-danger-action bg-surface-card p-3 text-body-sm text-text-primary"
              role="alert"
            >
              {storageError}
            </p>
          )}

          {outcome?.status === 'duplicate' ? (
            // F38: el cierre que ya existía prevalece. Aviso no modal, sin
            // trampa de foco y sin tratarlo como error.
            <p
              className="rounded-xl border border-badge-fiado bg-surface-card p-3 text-body-sm text-text-primary"
              data-testid="cash-close-duplicate-notice"
              role="status"
            >
              El cierre de esta fecha ya estaba registrado. Se muestra el cierre guardado.
            </p>
          ) : null}

          <p
            className="text-body-sm text-text-secondary"
            data-testid="cash-close-delivery"
            role={outcome?.status === 'rejected' ? 'alert' : 'status'}
          >
            {deliveryTextOf(outcome, readOnly ? stored.acknowledged : false)}
          </p>

          {readOnly ? (
            <div
              className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4"
              data-testid="cash-close-readonly"
            >
              <span className="text-caption-xs uppercase text-text-secondary">Cierre guardado</span>
              <p className="text-body-sm text-text-primary">
                {stored.acknowledged
                  ? 'Este cierre está confirmado por el servidor y ya no se edita.'
                  : 'Este cierre existe en el dispositivo y ya no se edita.'}
              </p>
            </div>
          ) : (
            <>
              <div className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
                <span className="text-caption-xs uppercase text-text-secondary">
                  {CASH_CLOSE_LABELS[selected]} contado
                </span>
                <span
                  className="font-mono tabular-nums text-display-lg text-text-primary"
                  data-testid="cash-close-entry"
                >
                  {formatCentsToSoles(countedOf(selected))}
                </span>
                {/*
                 * Decisión de forma: el conteo se tipea en céntimos con el mismo
                 * numpad del cobro —`2500` es S/ 25.00—, que es el caso real de
                 * una bodega (soles enteros) y no obliga a un separador decimal.
                 */}
                <span className="text-body-sm text-text-secondary">
                  Escribe en céntimos: 2500 = S/ 25.00
                </span>
              </div>

              <ErgonomicNumpad disabled={submitting} onKey={press} />
            </>
          )}

          <div
            className="grid grid-cols-2 gap-2 rounded-2xl border border-surface-border bg-surface-card p-4"
            data-testid="cash-close-summary"
          >
            <div className="flex flex-col gap-1">
              <span className="text-caption-xs uppercase text-text-secondary">Sobrante</span>
              <span
                className="font-mono tabular-nums text-body-md text-text-primary"
                data-testid="cash-close-surplus"
              >
                {formatCentsToSoles(summaryOfView.surplusCents)}
              </span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-caption-xs uppercase text-text-secondary">Faltante</span>
              <span
                className="font-mono tabular-nums text-body-md text-text-primary"
                data-testid="cash-close-shortage"
              >
                {formatCentsToSoles(summaryOfView.shortageCents)}
              </span>
            </div>
          </div>

          {readOnly ? null : (
            <>
              <Button
                className="w-full"
                data-testid="cash-close-submit"
                disabled={token === undefined || submitting}
                onClick={() => void closeCash()}
                size="cta"
              >
                {submitting ? 'Cerrando…' : 'CERRAR CAJA'}
              </Button>
              {token === undefined ? (
                <p
                  className="text-body-sm text-text-secondary"
                  data-testid="cash-close-no-session"
                >
                  Sin sesión en línea no se puede cerrar la caja: el cierre necesita el acuse del
                  servidor.
                </p>
              ) : null}
            </>
          )}
        </>
      )}
    </section>
  )
}

/** Las tres cifras de una fila. El fiado rotula su diferencia como informativa. */
function RowContent({
  method,
  expectedCents,
  countedCents,
  differenceCents,
}: {
  readonly method: TenderMethod
  readonly expectedCents: number
  readonly countedCents: number
  readonly differenceCents: number | null
}) {
  return (
    <div
      className="grid w-full grid-cols-[minmax(0,1fr)_auto_auto_auto] items-baseline gap-x-2 text-body-sm"
      data-method={method}
    >
      <span className="truncate font-semibold text-text-primary">{CASH_CLOSE_LABELS[method]}</span>
      <span
        className="text-right font-mono tabular-nums text-text-secondary"
        data-testid={`cash-close-expected-${method}`}
      >
        {formatCentsToSoles(expectedCents)}
      </span>
      <span
        className="text-right font-mono tabular-nums text-text-primary"
        data-testid={`cash-close-counted-${method}`}
      >
        {formatCentsToSoles(countedCents)}
      </span>
      <span
        className="text-right font-mono tabular-nums text-text-secondary"
        data-testid={`cash-close-difference-${method}`}
      >
        {/* `null` es la diferencia del fiado: informativa, nunca un faltante. */}
        {differenceCents === null ? 'Informativo' : differenceText(differenceCents)}
      </span>
    </div>
  )
}
