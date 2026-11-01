import { useState } from 'react'

import { ErgonomicNumpad, NUMPAD_DELETE_KEY, type NumpadKey } from '@/components/ErgonomicNumpad'
import { Money } from '@/components/Money'
import { getPosDatabase, type FiadoAccountRow, type PosDatabase } from '@/db/posDatabase'
import { enqueueFiadoPayment } from '@/fiado/fiadoQueue'
import { accountBalanceCents, searchFiadoAccounts } from '@/fiado/fiadoStore'
import { restoreFiadoAccounts, type RestoreFiadoOutcome } from '@/fiado/restoreFiado'
import { useFiadoWallet } from '@/hooks/useFiadoWallet'
import { cn } from '@/lib/utils'

/**
 * Cartera de deudores (W7-FE-1, W7-FE-2, W7-FE-3; docs/FRONTEND.md:51;
 * docs/PRODUCT.md:27; docs/DESIGN.md:787).
 *
 * **El saldo se computa.** No hay una columna de saldo ni en el servidor ni acá:
 * el saldo de cada deudor es la suma firmada de sus movimientos, así que la
 * pantalla nunca muestra un número que no salga del ledger.
 *
 * **El signo es el tipo.** Un cargo se ve en el color del texto y un abono en
 * esmeralda; no hay etiqueta que lo repita porque el contrato no tiene columna
 * de tipo (`docs/DATABASE.md` §4 `fiado_movements`).
 *
 * **El abono es encolable offline.** Se escribe en la cola local con su clave de
 * idempotencia y drena por el motor de sincronización; el cajero no ve nunca un
 * error de sincronización (§7.9). El tender Efectivo/transferencia es de
 * interfaz y no se persiste: el ledger no tiene esa columna (decisión 6 del
 * sprint 7).
 *
 * **La restauración es del servidor.** Un dispositivo reinstalado pide
 * `GET /fiado-accounts` y reconstruye cuentas, movimientos y saldos; el estado de
 * cuenta se comparte con el share sheet nativo, sin ningún camino de WhatsApp
 * (S6-4, `ARCHITECTURE.md:158`).
 */

type AbonoTender = 'cash' | 'transfer'

const ABONO_TENDERS: readonly { readonly value: AbonoTender; readonly label: string }[] = [
  { value: 'cash', label: 'Efectivo' },
  { value: 'transfer', label: 'Transferencia' },
]

export interface FiadoAccountsScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
  /** Access token de la sesión en línea; sin él la restauración se saltea. */
  readonly token?: string
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Inyectable para las pruebas; en producción, `navigator.share` del sistema. */
  readonly share?: (data?: ShareData) => Promise<void>
  readonly onBack?: () => void
}

export function FiadoAccountsScreen({
  database = getPosDatabase(),
  token,
  baseUrl,
  fetchImpl,
  share,
  onBack,
}: FiadoAccountsScreenProps) {
  const { wallet, reload } = useFiadoWallet(database)
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [abonoEntry, setAbonoEntry] = useState('')
  const [tender, setTender] = useState<AbonoTender>('cash')
  const [notice, setNotice] = useState<string | null>(null)
  const [restoring, setRestoring] = useState(false)

  const visible = searchFiadoAccounts(wallet.accounts, query)
  const selected: FiadoAccountRow | null =
    visible.find((account) => account.id === selectedId) ?? visible[0] ?? null
  const movementsOf = (accountId: string): typeof wallet.movements =>
    wallet.movements
      .filter((movement) => movement.fiado_account_id === accountId)
      .slice()
      .sort((left, right) => left.created_at.localeCompare(right.created_at))

  function pressAbono(key: NumpadKey): void {
    if (key === NUMPAD_DELETE_KEY) {
      setAbonoEntry((entry) => entry.slice(0, -1))

      return
    }
    setAbonoEntry((entry) => `${entry}${key}`.slice(0, 9))
  }

  async function registerAbono(): Promise<void> {
    if (selected === null) return

    const amountCents = abonoEntry === '' ? 0 : Number(abonoEntry)
    if (amountCents <= 0) return

    // El tender se elige en pantalla y NO viaja: el contrato no tiene columna de
    // medio de pago en el ledger (decisión 6 del sprint 7).
    void tender

    try {
      await enqueueFiadoPayment({ accountId: selected.id, amountCents }, database)
      setAbonoEntry('')
      setNotice('Abono registrado. Se sincroniza solo cuando haya conexión.')
      reload()
    } catch {
      // Ningún error de sincronización llega al cajero: el abono queda en la cola.
      setNotice('El abono no se pudo guardar en el dispositivo. Intenta de nuevo.')
    }
  }

  async function restore(): Promise<void> {
    setRestoring(true)
    try {
      const outcome: RestoreFiadoOutcome = await restoreFiadoAccounts({
        token,
        baseUrl,
        fetchImpl,
        database,
      })
      setNotice(
        outcome.status === 'restored'
          ? `Cartera restaurada: ${outcome.accounts} cuenta(s) y ${outcome.movements} movimiento(s).`
          : outcome.status === 'skipped'
            ? 'Sin sesión en línea no hay nada que restaurar todavía.'
            : 'La cartera no se pudo restaurar. La copia del dispositivo sigue disponible.',
      )
      reload()
    } finally {
      setRestoring(false)
    }
  }

  async function shareStatement(): Promise<void> {
    if (selected === null || share === undefined) return

    const movements = movementsOf(selected.id)
    const lines = movements.map(
      (movement) =>
        `${movement.created_at.slice(0, 10)}  ${(movement.amount_cents / 100).toFixed(2)}${movement.note === null ? '' : `  ${movement.note}`}`,
    )

    await share({
      title: `Estado de cuenta · ${selected.name}`,
      text: [
        `Estado de cuenta · ${selected.name}`,
        ...lines,
        `Saldo: ${(accountBalanceCents(movements) / 100).toFixed(2)}`,
      ].join('\n'),
    })
  }

  return (
    <section
      aria-labelledby="fiado-title"
      className="mx-auto flex w-full max-w-md flex-col gap-3 p-4"
    >
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-heading-md font-bold" id="fiado-title">
          Cuentas y Fiados
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

      {notice === null ? null : (
        <p
          data-testid="fiado-notice"
          role="status"
          className="rounded-2xl border border-surface-border bg-zinc-100 p-3 text-body-sm text-text-primary"
        >
          {notice}
        </p>
      )}

      <input
        aria-label="Buscar deudor"
        data-testid="fiado-search"
        onChange={(event) => setQuery(event.target.value)}
        placeholder="Buscar por nombre o teléfono"
        type="search"
        value={query}
        className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
      />

      {wallet.accounts.length === 0 ? (
        <p
          data-testid="fiado-empty"
          className="rounded-2xl border border-surface-border bg-surface-card p-4 text-body-sm text-text-secondary"
        >
          Todavía no hay deudores. El primero se registra al cobrar una venta a fiado, en dos
          toques.
        </p>
      ) : visible.length === 0 ? (
        <p
          data-testid="fiado-no-hits"
          className="rounded-2xl border border-surface-border bg-surface-card p-4 text-body-sm text-text-secondary"
        >
          Ningún deudor coincide con la búsqueda.
        </p>
      ) : (
        <ul aria-label="Deudores" className="flex flex-col gap-1">
          {visible.map((account) => {
            const balance = accountBalanceCents(movementsOf(account.id))
            const active = selected?.id === account.id

            return (
              <li key={account.id}>
                <button
                  type="button"
                  aria-current={active ? 'true' : undefined}
                  data-testid={`fiado-account-${account.id}`}
                  onClick={() => {
                    setSelectedId(account.id)
                    setAbonoEntry('')
                    setNotice(null)
                  }}
                  className={cn(
                    'flex min-h-12 w-full items-center justify-between gap-2 rounded-xl border px-3 text-left',
                    'transition-colors duration-[120ms] ease-out',
                    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                    active
                      ? 'border-accent-hover bg-accent-primary/10'
                      : 'border-surface-border bg-surface-card hover:bg-zinc-100',
                  )}
                >
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate text-body-md font-semibold text-text-primary">
                      {account.name}
                    </span>
                    <span className="truncate font-mono text-caption-xs tabular-nums text-text-secondary">
                      {account.phone ?? 'sin teléfono'}
                    </span>
                  </span>
                  <span
                    data-testid={`fiado-balance-${account.id}`}
                    className="shrink-0 font-mono tabular-nums text-body-md text-text-primary"
                  >
                    <Money cents={balance} />
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}

      {/*
       * La restauración vive FUERA del detalle a propósito: el caso que la
       * justifica es un dispositivo reinstalado, que después del login no tiene
       * ni una cuenta local y por lo tanto no tiene detalle que mostrar.
       */}
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          data-testid="fiado-share"
          disabled={selected === null || share === undefined}
          onClick={() => void shareStatement()}
          className="min-h-12 flex-1 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary hover:bg-zinc-100 disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
        >
          Compartir estado de cuenta
        </button>
        <button
          type="button"
          data-testid="fiado-restore"
          disabled={restoring}
          onClick={() => void restore()}
          className="min-h-12 flex-1 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary hover:bg-zinc-100 disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
        >
          Restaurar desde el servidor
        </button>
      </div>

      {selected === null ? null : (
        <section aria-labelledby="fiado-detail-title" className="flex flex-col gap-2">
          <h3 className="text-caption-xs uppercase text-text-secondary" id="fiado-detail-title">
            Movimientos de {selected.name}
          </h3>

          {movementsOf(selected.id).length === 0 ? (
            <p data-testid="fiado-detail-empty" className="text-body-sm text-text-secondary">
              El deudor todavía no tiene movimientos.
            </p>
          ) : (
            <ul aria-label="Movimientos firmados" className="flex flex-col gap-1">
              {movementsOf(selected.id).map((movement) => (
                <li
                  key={movement.id}
                  data-testid={`fiado-movement-${movement.id}`}
                  className="flex items-center justify-between gap-2 rounded-xl border border-surface-border bg-surface-card px-3 py-2"
                >
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate text-body-sm text-text-primary">
                      {movement.note ?? 'Movimiento'}
                    </span>
                    <span className="font-mono text-caption-xs tabular-nums text-text-secondary">
                      {movement.created_at.slice(0, 10)}
                    </span>
                  </span>
                  <span
                    className={cn(
                      'shrink-0 font-mono tabular-nums text-body-md',
                      movement.amount_cents < 0 ? 'text-emerald-600' : 'text-text-primary',
                    )}
                  >
                    <Money cents={movement.amount_cents} />
                  </span>
                </li>
              ))}
            </ul>
          )}

          <div className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4">
            <span className="text-caption-xs uppercase text-text-secondary">Abono parcial</span>
            <span
              data-testid="fiado-abono-amount"
              className="font-mono tabular-nums text-heading-md text-text-primary"
            >
              <Money cents={abonoEntry === '' ? 0 : Number(abonoEntry)} />
            </span>

            <div role="group" aria-label="Medio del abono" className="grid grid-cols-2 gap-2">
              {ABONO_TENDERS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  aria-pressed={tender === option.value}
                  data-testid={`fiado-tender-${option.value}`}
                  onClick={() => setTender(option.value)}
                  className={cn(
                    'min-h-12 rounded-xl border px-2 text-body-md font-semibold',
                    tender === option.value
                      ? 'border-accent-hover bg-accent-primary/10 text-text-primary'
                      : 'border-surface-border bg-surface-card text-text-secondary hover:bg-zinc-100',
                    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                  )}
                >
                  {option.label}
                </button>
              ))}
            </div>

            <ErgonomicNumpad onKey={pressAbono} />

            <button
              type="button"
              data-testid="fiado-abono-submit"
              disabled={abonoEntry === ''}
              onClick={() => void registerAbono()}
              className="min-h-12 rounded-xl bg-accent-hover px-3 text-body-md font-bold text-white disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              Registrar abono
            </button>
          </div>
        </section>
      )}
    </section>
  )
}
