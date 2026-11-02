import { useEffect, useState } from 'react'

import { getPosDatabase, type PosDatabase, type ProductRow } from '@/db/posDatabase'
import { cn } from '@/lib/utils'

/**
 * Inventario / Stock Rápido del rol Administrador (W8-FE-5; docs/FRONTEND.md:52;
 * docs/DESIGN.md:213).
 *
 * **El semáforo lee la proyección local del stock.** `products.stock` es la suma de los deltas
 * firmados que el dispositivo conoce (`docs/DATABASE.md` §4 `products`), y esta pantalla no la
 * recalcula: la clasifica.
 *
 * **Umbrales (decisión D-I del sprint 8).** `docs/FRONTEND.md:52` llama «spec-fixed» a los
 * umbrales, pero ningún documento publica el número: `docs/DESIGN.md:213` da la regla cualitativa
 * —normal, bajo, quiebre—. Se congela acá la propuesta mínima y se declara como tal: quiebre en
 * cero o menos, bajo hasta cinco unidades, normal por encima. Un stock negativo cuenta como
 * quiebre, no como «normal por absurdo».
 *
 * **La merma se pide desde la fila.** El operador ve el producto que ya tiene stock y pide la
 * merma desde ahí; la pantalla no escribe, solo navega.
 */

/** Nivel del semáforo de stock. */
export type StockLevel = 'normal' | 'low' | 'out'

/** Umbral propuesto de «stock bajo»: unidades restantes que piden reposición (D-I). */
export const LOW_STOCK_THRESHOLD = 5

const LEVEL_LABELS: Readonly<Record<StockLevel, string>> = {
  normal: 'Normal',
  low: 'Bajo',
  out: 'Quiebre',
}

const LEVEL_CLASSES: Readonly<Record<StockLevel, string>> = {
  normal: 'bg-accent-primary',
  low: 'bg-amber-500',
  out: 'bg-rose-600',
}

/** Clasifica la proyección local del stock. */
export function stockLevelOf(stock: number): StockLevel {
  if (stock <= 0) return 'out'
  if (stock <= LOW_STOCK_THRESHOLD) return 'low'

  return 'normal'
}

/** Filtra por nombre o GTIN: el cajero busca con lo que tiene a mano. */
export function searchProducts(products: readonly ProductRow[], query: string): readonly ProductRow[] {
  const normalized = query.trim().toLowerCase()
  if (normalized === '') return products

  return products.filter(
    (product) =>
      product.name.toLowerCase().includes(normalized) ||
      (product.gtin !== null && product.gtin.includes(normalized)),
  )
}

/** El stock con hasta tres decimales, sin ceros de relleno. */
function stockText(stock: number): string {
  return String(Math.round(stock * 1000) / 1000)
}

export interface AdminInventoryScreenProps {
  readonly database?: PosDatabase
  /** Pide la merma de un producto: el shell navega al módulo de mermas con él elegido. */
  readonly onWasteRequested?: (productId: string) => void
}

export function AdminInventoryScreen({
  database = getPosDatabase(),
  onWasteRequested,
}: AdminInventoryScreenProps) {
  const [products, setProducts] = useState<readonly ProductRow[]>([])
  const [pendingMovements, setPendingMovements] = useState(0)
  const [query, setQuery] = useState('')
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    let live = true

    void Promise.all([database.products.toArray(), database.idempotency_queue.toArray()]).then(
      ([rows, queue]) => {
        if (!live) return

        setProducts(rows.slice().sort((left, right) => left.name.localeCompare(right.name)))
        setPendingMovements(
          queue.filter((row) => row.operation === 'stock_adjustment' && row.status === 'pending').length,
        )
        setLoaded(true)
      },
    )

    return () => {
      live = false
    }
  }, [database])

  const visible = searchProducts(products, query)

  return (
    <section aria-labelledby="admin-inventory-title" className="mx-auto flex w-full max-w-3xl flex-col gap-3 p-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="admin-inventory-title">
          Inventario / Stock Rápido
        </h2>
        <p className="text-body-sm text-text-secondary">
          Stock del dispositivo: la suma de los deltas firmados que ya se conocen.
        </p>
      </div>

      {pendingMovements > 0 ? (
        <p
          data-testid="inventory-pending"
          role="status"
          className="rounded-2xl border border-amber-500 bg-amber-50 p-3 text-body-sm text-text-primary"
        >
          {pendingMovements === 1
            ? '1 movimiento de stock por sincronizar'
            : `${pendingMovements} movimientos de stock por sincronizar`}
        </p>
      ) : null}

      <input
        aria-label="Buscar producto"
        data-testid="inventory-search"
        onChange={(event) => setQuery(event.target.value)}
        placeholder="Buscar por nombre o GTIN"
        type="search"
        value={query}
        className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
      />

      {!loaded ? (
        <p data-testid="inventory-loading" className="text-body-sm text-text-secondary">
          Leyendo el catálogo del dispositivo…
        </p>
      ) : products.length === 0 ? (
        <p
          data-testid="inventory-empty"
          className="rounded-2xl border border-surface-border bg-surface-card p-4 text-body-sm text-text-secondary"
        >
          Este dispositivo todavía no tiene catálogo. Se llena con el alta de productos o con el
          pull del servidor.
        </p>
      ) : visible.length === 0 ? (
        <p
          data-testid="inventory-no-hits"
          className="rounded-2xl border border-surface-border bg-surface-card p-4 text-body-sm text-text-secondary"
        >
          Ningún producto coincide con la búsqueda.
        </p>
      ) : (
        <ul aria-label="Productos" className="flex flex-col gap-1">
          {visible.map((product) => {
            const level = stockLevelOf(product.stock)

            return (
              <li
                key={product.id}
                data-level={level}
                data-testid={`inventory-row-${product.id}`}
                className="flex items-center justify-between gap-2 rounded-xl border border-surface-border bg-surface-card px-3 py-2"
              >
                <span className="flex min-w-0 items-center gap-2">
                  <span aria-hidden="true" className={cn('size-3 shrink-0 rounded-full', LEVEL_CLASSES[level])} />
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate text-body-md font-semibold text-text-primary">{product.name}</span>
                    <span className="font-mono text-caption-xs tabular-nums text-text-secondary">
                      {product.gtin ?? 'sin GTIN'} · {stockText(product.stock)} {product.uom}
                    </span>
                  </span>
                </span>

                <span className="flex shrink-0 items-center gap-2">
                  <span className="text-caption-xs uppercase text-text-secondary">{LEVEL_LABELS[level]}</span>
                  {onWasteRequested === undefined ? null : (
                    <button
                      type="button"
                      onClick={() => onWasteRequested(product.id)}
                      className="min-h-9 rounded-lg border border-surface-border px-2 text-body-sm text-text-primary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
                    >
                      Merma
                    </button>
                  )}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
