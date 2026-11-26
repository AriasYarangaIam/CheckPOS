import { useEffect, useState } from 'react'

import { getPosDatabase, type PosDatabase, type ProductRow } from '@/db/posDatabase'
import { cn } from '@/lib/utils'
import { searchProducts } from '@/screens/AdminInventoryScreen'
import { enqueueWaste, isWasteReason, qtyDeltaOf, WASTE_REASONS, type WasteReason } from '@/stock/wasteQueue'

/**
 * Mermas y Daños del rol Administrador (W8-FE-5; docs/FRONTEND.md:52; docs/DESIGN.md:214-218).
 *
 * **Los tres motivos son la lista cerrada del contrato**: `Vencimiento`, `Rotura en transporte` y
 * `Daño en vitrina`, con sus tildes y sus espacios. El borde del servidor rechaza cualquier otro
 * texto y esta pantalla no ofrece ninguno más.
 *
 * **La confirmación es destructiva y va ANTES de escribir.** La merma da de baja mercadería: el
 * operador ve qué producto, cuánto y por qué, y recién ahí confirma. Cancelar no deja rastro.
 *
 * **Sin foto.** `stock_adjustments.evidence_photo_url` está diferido (delta D5): no hay control de
 * captura ni campo en el cuerpo, y no se inventa uno.
 *
 * **Funciona sin red.** La merma se encola con su clave de idempotencia y drena cuando haya
 * conexión; la pantalla no llama al servidor en ningún momento.
 */

/** Identificador estable de cada motivo para el marcado y las pruebas. */
const REASON_IDS: Readonly<Record<WasteReason, string>> = {
  Vencimiento: 'vencimiento',
  'Rotura en transporte': 'transporte',
  'Daño en vitrina': 'vitrina',
}

const INVALID_QUANTITY_NOTICE =
  'La cantidad debe ser mayor que cero y con hasta tres decimales (por ejemplo 2.5).'

const MISSING_REASON_NOTICE = 'Elige uno de los tres motivos de la merma.'

export interface AdminWasteScreenProps {
  readonly database?: PosDatabase
  /** Producto ya elegido por el inventario; sin él, el operador lo busca acá. */
  readonly initialProductId?: string
  /** Se llama después de encolar la merma. */
  readonly onRegistered?: () => void
}

export function AdminWasteScreen({
  database = getPosDatabase(),
  initialProductId,
  onRegistered,
}: AdminWasteScreenProps) {
  const [products, setProducts] = useState<readonly ProductRow[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(initialProductId ?? null)
  const [query, setQuery] = useState('')
  const [quantity, setQuantity] = useState('')
  const [reason, setReason] = useState<WasteReason | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    let live = true

    void database.products.toArray().then((rows) => {
      if (!live) return

      setProducts(rows.slice().sort((left, right) => left.name.localeCompare(right.name)))
      setLoaded(true)
    })

    return () => {
      live = false
    }
  }, [database])

  const selected = products.find((product) => product.id === selectedId) ?? null
  const visible = searchProducts(products, query)

  /**
   * El botón se puede pulsar con el producto elegido y el formulario se valida ACÁ: un botón
   * deshabilitado sin explicación deja al operador sin saber qué falta.
   */
  function askConfirmation(): void {
    if (qtyDeltaOf(quantity) === null) {
      setError(INVALID_QUANTITY_NOTICE)

      return
    }
    if (reason === null) {
      setError(MISSING_REASON_NOTICE)

      return
    }

    setError(null)
    setConfirming(true)
  }

  async function confirm(): Promise<void> {
    if (selected === null || reason === null) return

    try {
      await enqueueWaste({ productId: selected.id, quantity, reason }, database)
      setNotice(`Merma registrada: ${quantity.trim()} ${selected.uom} de ${selected.name} por ${reason}.`)
      setConfirming(false)
      setQuantity('')
      setReason(null)
      setSelectedId(null)
      onRegistered?.()
    } catch (failure) {
      setConfirming(false)
      setError(failure instanceof Error ? failure.message : INVALID_QUANTITY_NOTICE)
    }
  }

  return (
    <section aria-labelledby="admin-waste-title" className="mx-auto flex w-full max-w-2xl flex-col gap-3 p-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="admin-waste-title">
          Mermas y Daños
        </h2>
        <p className="text-body-sm text-text-secondary">
          La baja descuenta stock sin generar una venta. Queda en la cola del dispositivo hasta que
          el servidor la acuse.
        </p>
      </div>

      {notice === null ? null : (
        <p
          data-testid="merma-notice"
          role="status"
          className="rounded-2xl border border-accent-hover bg-accent-primary/10 p-3 text-body-sm text-text-primary"
        >
          {notice}
        </p>
      )}

      {!loaded ? (
        <p data-testid="merma-loading" className="text-body-sm text-text-secondary">
          Leyendo el catálogo del dispositivo…
        </p>
      ) : products.length === 0 ? (
        <p
          data-testid="merma-empty"
          className="rounded-2xl border border-surface-border bg-surface-card p-4 text-body-sm text-text-secondary"
        >
          Este dispositivo todavía no tiene catálogo: sin productos no hay merma que registrar.
        </p>
      ) : selected === null ? (
        <>
          <input
            aria-label="Buscar producto para la merma"
            data-testid="merma-search"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Buscar por nombre o GTIN"
            type="search"
            value={query}
            className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          />
          <ul aria-label="Productos" className="flex flex-col gap-1">
            {visible.map((product) => (
              <li key={product.id}>
                <button
                  type="button"
                  data-testid={`merma-select-${product.id}`}
                  onClick={() => {
                    setSelectedId(product.id)
                    setNotice(null)
                    setError(null)
                  }}
                  className="flex min-h-12 w-full items-center justify-between gap-2 rounded-xl border border-surface-border bg-surface-card px-3 text-left hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                >
                  <span className="truncate text-body-md font-semibold text-text-primary">{product.name}</span>
                  <span className="shrink-0 font-mono text-caption-xs tabular-nums text-text-secondary">
                    {product.gtin ?? 'sin GTIN'}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      ) : (
        <>
          <div
            data-testid="merma-selected"
            className="flex items-center justify-between gap-2 rounded-2xl border border-surface-border bg-surface-card p-4"
          >
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-body-md font-semibold text-text-primary">{selected.name}</span>
              <span className="font-mono text-caption-xs tabular-nums text-text-secondary">
                {selected.gtin ?? 'sin GTIN'} · stock {selected.stock} {selected.uom}
              </span>
            </span>
            <button
              type="button"
              data-testid="merma-change"
              onClick={() => setSelectedId(null)}
              className="min-h-9 rounded-lg px-2 text-body-sm text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              Cambiar
            </button>
          </div>

          <label className="flex flex-col gap-1" htmlFor="merma-quantity">
            <span className="text-caption-xs uppercase text-text-secondary">
              Cantidad que se da de baja
            </span>
            <input
              data-testid="merma-quantity"
              id="merma-quantity"
              inputMode="decimal"
              onChange={(event) => {
                setQuantity(event.target.value)
                setError(null)
              }}
              placeholder="2.5"
              type="text"
              value={quantity}
              className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 font-mono tabular-nums text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            />
          </label>

          <div role="group" aria-label="Motivo de la merma" className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            {WASTE_REASONS.map((candidate) => (
              <button
                key={candidate}
                type="button"
                aria-pressed={reason === candidate}
                data-testid={`merma-reason-${REASON_IDS[candidate]}`}
                onClick={() => {
                  setReason(candidate)
                  setError(null)
                }}
                className={cn(
                  'min-h-12 rounded-xl border px-3 text-body-md font-semibold',
                  reason === candidate
                    ? 'border-danger-action bg-rose-50 text-text-primary'
                    : 'border-surface-border bg-surface-card text-text-secondary hover:bg-zinc-100',
                  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                )}
              >
                {candidate}
              </button>
            ))}
          </div>

          {error === null ? null : (
            <p data-testid="merma-error" role="alert" className="text-body-sm text-danger-action">
              {error}
            </p>
          )}

          {confirming ? (
            <div
              data-testid="merma-confirm"
              className="flex flex-col gap-2 rounded-2xl border border-danger-action bg-surface-card p-4"
            >
              <p className="text-body-md text-text-primary">
                ¿Confirmas la baja de {quantity.trim()} {selected.uom} de {selected.name} por{' '}
                {reason ?? ''}?
              </p>
              <p className="text-body-sm text-text-secondary">
                Descuenta stock y no se puede deshacer: una corrección posterior es otra merma.
              </p>
              <div className="flex flex-wrap gap-2">
                <button
                  type="button"
                  data-testid="merma-confirm-submit"
                  onClick={() => void confirm()}
                  className="min-h-12 flex-1 rounded-xl bg-danger-action px-3 text-body-md font-bold text-white focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                >
                  Confirmar merma
                </button>
                <button
                  type="button"
                  data-testid="merma-cancel"
                  onClick={() => setConfirming(false)}
                  className="min-h-12 flex-1 rounded-xl border border-surface-border px-3 text-body-md font-semibold text-text-primary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                >
                  Cancelar
                </button>
              </div>
            </div>
          ) : (
            <button
              type="button"
              data-testid="merma-submit"
              disabled={selected === null}
              onClick={askConfirmation}
              className="min-h-[52px] rounded-xl bg-danger-action text-heading-md font-bold text-white disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              Registrar merma
            </button>
          )}
        </>
      )}
    </section>
  )
}

/** Reexporta el guardián del motivo para el shell y las pruebas del módulo. */
export { isWasteReason }
