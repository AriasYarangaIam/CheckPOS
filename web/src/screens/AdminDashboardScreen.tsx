import { useEffect, useState } from 'react'

import { Money } from '@/components/Money'
import { getPosDatabase, type CashCloseRow, type IdempotencyQueueRow, type PosDatabase } from '@/db/posDatabase'
import {
  projectLedger,
  readReceivable,
  tenderRows,
  type LedgerProjection,
  type LocalCloseAudit,
  type Receivable,
} from '@/dashboard/ledgerProjection'
import { useNetworkStatus } from '@/hooks/useNetworkStatus'
import { formatCentsToSoles } from '@/lib/money'
import { formatBusinessDate } from '@/sales/saleClose'
import { CASH_CLOSE_LABELS } from '@/sales/cashClose'
import { cn } from '@/lib/utils'

/**
 * Dashboard / Auditoría del rol Administrador (W8-FE-3; docs/FRONTEND.md:49;
 * `ARCHITECTURE.md:138`).
 *
 * **Dos fuentes y ninguna más.** La proyección del ledger local que el dispositivo ya tiene
 * —la cola de eventos y los cierres guardados— más `GET /fiado-accounts` para la deuda viva. No
 * hay `GET /sales` ni `GET /dashboard/summary`: el delta D12 sigue diferido y este bloque no
 * agrega ninguna superficie de datos.
 *
 * **El día del dueño incluye lo pendiente.** Las ventas sin sincronizar cuentan y el contador de
 * pendientes dice qué parte no está confirmada (decisión D-E). Una venta rechazada por el servidor
 * no cuenta: nunca fue una venta.
 *
 * **La deuda de fiado puede no saberse.** Sin sesión en línea o sin red, la tarjeta dice
 * «pendiente» y no muestra un número: un cero diría que nadie debe nada, que es una mentira
 * distinta de «no lo sé».
 *
 * **El fiado del desglose es informacional.** Su fila viaja marcada y nunca se suma como faltante
 * (§4.13, F32).
 */

export interface AdminDashboardScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
  /** Token de la sesión del dueño; sin él la deuda de fiado queda pendiente. */
  readonly token?: string
  readonly baseUrl?: string
  readonly fetchImpl?: typeof fetch
  /** Reloj, inyectable para las pruebas: define la fecha de negocio. */
  readonly now?: () => Date
}

/** Etiqueta del cierre local según su diferencia agregada. */
const CLOSE_KIND_LABELS: Readonly<Record<LocalCloseAudit['kind'], string>> = {
  surplus: 'Sobrante',
  shortage: 'Faltante',
  balanced: 'Cuadrado',
}

export function AdminDashboardScreen({
  database = getPosDatabase(),
  token,
  baseUrl,
  fetchImpl,
  now = () => new Date(),
}: AdminDashboardScreenProps) {
  const { isOnline } = useNetworkStatus()
  const [projection, setProjection] = useState<LedgerProjection>(() =>
    projectLedger([], [], formatBusinessDate(now())),
  )
  const [receivable, setReceivable] = useState<Receivable>({ status: 'offline' })
  // Antes de la primera lectura local no hay números que mostrar: un cero inicial diría «hoy no
  // vendiste nada», que es una mentira distinta de «todavía no leí».
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    let live = true

    void Promise.all([
      database.idempotency_queue.toArray(),
      database.cash_closes.toArray(),
    ]).then(([events, closes]: [IdempotencyQueueRow[], CashCloseRow[]]) => {
      if (!live) return

      setProjection(projectLedger(events, closes, formatBusinessDate(now())))
      setLoaded(true)
    })

    return () => {
      live = false
    }
  }, [database, now])

  useEffect(() => {
    let live = true

    void readReceivable({ token, isOnline, fetchImpl, baseUrl }).then((read) => {
      if (live) setReceivable(read)
    })

    return () => {
      live = false
    }
  }, [token, isOnline, fetchImpl, baseUrl])

  const tenders = tenderRows(projection.byTender)
  const maxHour = Math.max(1, ...projection.byHour.map((cents) => Math.abs(cents)))

  return (
    <section aria-labelledby="admin-dashboard-title" className="mx-auto flex w-full max-w-4xl flex-col gap-4 p-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="admin-dashboard-title">
          Dashboard / Auditoría
        </h2>
        <p className="text-body-sm text-text-secondary">
          {projection.businessDate} · datos del dispositivo, sin endpoint de reporte
        </p>
      </div>

      <div aria-busy={!loaded} className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <article className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Ventas netas</span>
          <span data-testid="dashboard-net-sales" className="font-mono tabular-nums text-heading-md text-text-primary">
            {loaded ? <Money cents={projection.netSalesCents} /> : '—'}
          </span>
        </article>

        <article className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Ticket promedio</span>
          <span data-testid="dashboard-average-ticket" className="font-mono tabular-nums text-heading-md text-text-primary">
            {loaded ? <Money cents={projection.averageTicketCents} /> : '—'}
          </span>
        </article>

        <article className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Tickets</span>
          <span data-testid="dashboard-tickets" className="font-mono tabular-nums text-heading-md text-text-primary">
            {loaded ? projection.ticketCount : '—'}
          </span>
        </article>

        <article className="flex flex-col gap-1 rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Fiados por cobrar</span>
          <span
            data-testid="dashboard-receivable"
            className={cn(
              'font-mono tabular-nums text-heading-md',
              receivable.status === 'live' ? 'text-text-primary' : 'text-text-secondary',
            )}
          >
            {receivable.status === 'live' ? <Money cents={receivable.balanceCents} /> : 'pendiente'}
          </span>
        </article>
      </div>

      {projection.pendingSales > 0 ? (
        <p
          data-testid="dashboard-pending"
          role="status"
          className="rounded-2xl border border-amber-500 bg-amber-50 p-3 text-body-sm text-text-primary"
        >
          {projection.pendingSales === 1
            ? '1 venta por sincronizar'
            : `${projection.pendingSales} ventas por sincronizar`}
        </p>
      ) : null}

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <article className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4">
          <h3 className="text-caption-xs uppercase text-text-secondary">Desglose por método</h3>
          <ul aria-label="Desglose por método" className="flex flex-col gap-1">
            {tenders.map((row) => {
              const informational = row.method === 'fiado'

              return (
                <li
                  key={row.method}
                  data-informational={informational ? 'true' : 'false'}
                  data-method={row.method}
                  data-testid={`dashboard-tender-${row.method}`}
                  className="flex items-center justify-between gap-2"
                >
                  <span className="text-body-sm text-text-secondary">
                    {CASH_CLOSE_LABELS[row.method]}
                    {informational ? ' · informacional' : ''}
                  </span>
                  <span className="font-mono tabular-nums text-body-md text-text-primary">
                    <Money cents={row.cents} />
                  </span>
                </li>
              )
            })}
          </ul>
        </article>

        <figure className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4">
          <figcaption className="text-caption-xs uppercase text-text-secondary">
            Distribución por hora local
          </figcaption>
          <div aria-hidden="true" className="flex h-28 items-end gap-[2px]">
            {projection.byHour.map((cents, hour) => (
              <div
                key={hour}
                aria-label={`${hour}:00 — ${formatCentsToSoles(cents)}`}
                data-testid={`dashboard-hour-${hour}`}
                style={{ height: `${Math.max(2, (Math.abs(cents) / maxHour) * 100)}%` }}
                className={cn(
                  'flex-1 rounded-t-sm',
                  cents < 0 ? 'bg-rose-500' : cents === 0 ? 'bg-surface-subtle' : 'bg-accent-primary',
                )}
              />
            ))}
          </div>
          <p className="text-caption-xs text-text-muted">0 h — 23 h, en el reloj del dispositivo</p>
        </figure>
      </div>

      <article className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4">
        <h3 className="text-caption-xs uppercase text-text-secondary">Cierres locales</h3>
        {projection.localCloses.length === 0 ? (
          <p data-testid="dashboard-closes-empty" className="text-body-sm text-text-secondary">
            Todavía no hay cierres de caja en este dispositivo.
          </p>
        ) : (
          <ul aria-label="Cierres locales" className="flex flex-col gap-1">
            {projection.localCloses.map((audit) => (
              <li
                key={audit.closedOn}
                data-testid={`dashboard-close-${audit.closedOn}`}
                className="flex items-center justify-between gap-2 rounded-xl border border-surface-border px-3 py-2"
              >
                <span className="flex min-w-0 flex-col">
                  <span className="font-mono tabular-nums text-body-md text-text-primary">{audit.closedOn}</span>
                  <span className="text-caption-xs uppercase text-text-secondary">
                    {CLOSE_KIND_LABELS[audit.kind]}
                    {audit.acknowledged ? ' · acusado' : ' · sin acuse'}
                  </span>
                </span>
                <span className="shrink-0 font-mono tabular-nums text-body-md text-text-primary">
                  <Money cents={audit.differenceCents} />
                </span>
              </li>
            ))}
          </ul>
        )}
        <p className="text-caption-xs text-text-muted">
          La diferencia del fiado es informacional y nunca se cuenta como faltante.
        </p>
      </article>
    </section>
  )
}
