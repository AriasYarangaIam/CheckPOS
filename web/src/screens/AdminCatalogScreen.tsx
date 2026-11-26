import { useEffect, useState } from 'react'

import {
  enqueueProductCreate,
  lookupGlobalProduct,
  priceToCents,
  searchProducts,
  type GlobalProductMatch,
} from '@/catalog/productQueue'
import { getPosDatabase, type PosDatabase, type ProductRow } from '@/db/posDatabase'
import { useNetworkStatus } from '@/hooks/useNetworkStatus'
import { formatCentsToSoles } from '@/lib/money'
import { stockLevelOf } from '@/screens/AdminInventoryScreen'
import { cn } from '@/lib/utils'

/**
 * Catálogo Maestro del rol Administrador (W8-FE-5; docs/FRONTEND.md:53; docs/BACKEND.md §4.6-4.7).
 *
 * **El alta es encolable; la edición no.** `POST /products` está en la enumeración de POST
 * encolables, así que un producto nuevo se crea sin red y drena con su clave de idempotencia. La
 * edición va por `PATCH /products/{id}`, que **no** es encolable (§7.6): sin conexión el guardado se
 * deshabilita y la pantalla dice por qué, en lugar de fingir que se guardó.
 *
 * **El stock no viaja en ninguna de las dos.** El alta lo excluye por contrato y el PATCH lo
 * rechaza: el stock converge solo por deltas firmados (`stock_adjustments`).
 *
 * **La adopción es un atajo, no un requisito.** Si el GTIN aparece en la mediación del catálogo
 * global, el alta lleva `global_product_id` y el servidor copia los datos a la fila del tenant
 * (F13). Si no aparece, el alta manual sigue disponible con el mismo formulario.
 */

interface Draft {
  readonly gtin: string
  readonly name: string
  readonly price: string
  readonly uom: string
  readonly photoUrl: string
}

const EMPTY_DRAFT: Draft = { gtin: '', name: '', price: '', uom: 'unit', photoUrl: '' }

/** Cuerpo aditivo del PATCH: solo lo que cambió, y nunca `stock` (§4.7). */
function patchBodyOf(original: ProductRow, name: string, price: string): Record<string, unknown> | null {
  const body: Record<string, unknown> = {}

  const trimmedName = name.trim()
  if (trimmedName !== '' && trimmedName !== original.name) body.name = trimmedName

  if (price.trim() !== '') {
    const cents = priceToCents(price)
    if (cents === null) return null
    if (cents !== original.price_cents) body.price_cents = cents
  }

  if (Object.keys(body).length === 0) return null

  // La marca de mutación del cliente es el comparador LWW (§7.4); el servidor la preserva.
  body.updated_at = new Date().toISOString()

  return body
}

export interface AdminCatalogScreenProps {
  readonly database?: PosDatabase
  readonly token?: string
  readonly baseUrl?: string
  readonly fetchImpl?: typeof fetch
}

export function AdminCatalogScreen({
  database = getPosDatabase(),
  token,
  baseUrl,
  fetchImpl,
}: AdminCatalogScreenProps) {
  const { isOnline } = useNetworkStatus()
  const [products, setProducts] = useState<readonly ProductRow[]>([])
  const [loaded, setLoaded] = useState(false)
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState<Draft>(EMPTY_DRAFT)
  const [adopted, setAdopted] = useState<GlobalProductMatch | null>(null)
  const [lookupMissed, setLookupMissed] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editName, setEditName] = useState('')
  const [editPrice, setEditPrice] = useState('')
  const [saving, setSaving] = useState(false)

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

  const visible = searchProducts(products, query)
  const canSaveOnline = isOnline && token !== undefined && token.trim() !== ''

  function reload(): void {
    void database.products.toArray().then((rows) => {
      setProducts(rows.slice().sort((left, right) => left.name.localeCompare(right.name)))
    })
  }

  async function adopt(): Promise<void> {
    setLookupMissed(false)
    setAdopted(null)
    setError(null)

    const match = await lookupGlobalProduct(draft.gtin, { fetchImpl, baseUrl, token })
    if (match === null) {
      // El camino de fallo de la mediación es la AUSENCIA del producto, no un error del servidor.
      setLookupMissed(true)

      return
    }

    setAdopted(match)
    setDraft((current) => ({
      ...current,
      name: match.name,
      photoUrl:
        typeof match.attributes?.['photo_url'] === 'string' ? (match.attributes['photo_url'] as string) : '',
    }))
  }

  async function create(): Promise<void> {
    const priceCents = priceToCents(draft.price)
    if (draft.name.trim() === '' || priceCents === null || draft.uom.trim() === '') {
      setError('El producto necesita nombre, precio en soles y unidad de medida.')

      return
    }

    try {
      await enqueueProductCreate(
        {
          draft: {
            name: draft.name,
            gtin: draft.gtin.trim() === '' ? null : draft.gtin.trim(),
            priceCents,
            uom: draft.uom,
            photoUrl: draft.photoUrl.trim() === '' ? null : draft.photoUrl.trim(),
            attributes: adopted?.attributes ?? null,
            globalProductId: adopted?.id ?? null,
          },
        },
        database,
      )
      setNotice(`${draft.name.trim()} quedó en la cola del dispositivo: se sincroniza solo.`)
      setCreating(false)
      setDraft(EMPTY_DRAFT)
      setAdopted(null)
      setLookupMissed(false)
      setError(null)
      reload()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'El producto no se pudo guardar.')
    }
  }

  function startEditing(product: ProductRow): void {
    setEditingId(product.id)
    setEditName(product.name)
    setEditPrice((product.price_cents / 100).toFixed(2))
    setError(null)
    setNotice(null)
  }

  async function saveEdit(product: ProductRow): Promise<void> {
    if (!canSaveOnline) return

    const body = patchBodyOf(product, editName, editPrice)
    if (body === null) {
      setError('La edición necesita un nombre o un precio distinto, con el precio en soles.')

      return
    }

    setSaving(true)
    const network = fetchImpl ?? globalThis.fetch

    try {
      const response = await network(`${baseUrl ?? ''}/products/${product.id}`, {
        method: 'PATCH',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${token ?? ''}`,
        },
        body: JSON.stringify(body),
      })
      if (!response.ok) {
        setError('La edición no se pudo guardar. Intenta de nuevo.')
        setSaving(false)

        return
      }

      setNotice('Producto actualizado.')
      setEditingId(null)
      reload()
    } catch {
      setError('La edición no se pudo guardar: no hay conexión con el servidor.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section aria-labelledby="admin-catalog-title" className="mx-auto flex w-full max-w-3xl flex-col gap-3 p-4">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-heading-md font-bold" id="admin-catalog-title">
          Catálogo Maestro
        </h2>
        <button
          type="button"
          data-testid="catalog-new"
          onClick={() => {
            setCreating(true)
            setDraft(EMPTY_DRAFT)
            setAdopted(null)
            setLookupMissed(false)
            setNotice(null)
            setError(null)
          }}
          className="min-h-9 rounded-lg px-2 text-body-md font-semibold text-accent-hover hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
        >
          Nuevo producto
        </button>
      </div>

      {notice === null ? null : (
        <p
          data-testid="catalog-notice"
          role="status"
          className="rounded-2xl border border-accent-hover bg-accent-primary/10 p-3 text-body-sm text-text-primary"
        >
          {notice}
        </p>
      )}

      {error === null ? null : (
        <p data-testid="catalog-error" role="alert" className="text-body-sm text-danger-action">
          {error}
        </p>
      )}

      {creating ? (
        <div className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4">
          <span className="text-caption-xs uppercase text-text-secondary">Alta de producto</span>
          <label className="flex flex-col gap-1" htmlFor="catalog-gtin">
            <span className="text-caption-xs text-text-secondary">GTIN (opcional)</span>
            <input
              data-testid="catalog-gtin"
              id="catalog-gtin"
              onChange={(event) => {
                setDraft((current) => ({ ...current, gtin: event.target.value }))
                setAdopted(null)
                setLookupMissed(false)
              }}
              type="text"
              value={draft.gtin}
              className="min-h-11 rounded-xl border border-surface-border px-3 font-mono text-body-md"
            />
          </label>

          <button
            type="button"
            data-testid="catalog-adopt"
            disabled={draft.gtin.trim() === '' || token === undefined}
            onClick={() => void adopt()}
            className="min-h-11 rounded-xl border border-surface-border px-3 text-body-md text-text-primary hover:bg-zinc-100 disabled:pointer-events-none disabled:opacity-40"
          >
            Buscar en el catálogo global
          </button>

          {adopted === null ? null : (
            <p data-testid="catalog-adopted" className="text-body-sm text-accent-hover">
              Adoptado del catálogo global: {adopted.name}
            </p>
          )}
          {lookupMissed ? (
            <p data-testid="catalog-adopt-miss" className="text-body-sm text-text-secondary">
              Ese GTIN no está en el catálogo global: crea el producto a mano.
            </p>
          ) : null}

          <label className="flex flex-col gap-1" htmlFor="catalog-name">
            <span className="text-caption-xs text-text-secondary">Nombre</span>
            <input
              data-testid="catalog-name"
              id="catalog-name"
              onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))}
              type="text"
              value={draft.name}
              className="min-h-11 rounded-xl border border-surface-border px-3 text-body-md"
            />
          </label>

          <label className="flex flex-col gap-1" htmlFor="catalog-price">
            <span className="text-caption-xs text-text-secondary">Precio en soles</span>
            <input
              data-testid="catalog-price"
              id="catalog-price"
              inputMode="decimal"
              onChange={(event) => setDraft((current) => ({ ...current, price: event.target.value }))}
              placeholder="4.50"
              type="text"
              value={draft.price}
              className="min-h-11 rounded-xl border border-surface-border px-3 font-mono text-body-md"
            />
          </label>

          <label className="flex flex-col gap-1" htmlFor="catalog-uom">
            <span className="text-caption-xs text-text-secondary">Unidad de medida</span>
            <input
              data-testid="catalog-uom"
              id="catalog-uom"
              onChange={(event) => setDraft((current) => ({ ...current, uom: event.target.value }))}
              type="text"
              value={draft.uom}
              className="min-h-11 rounded-xl border border-surface-border px-3 text-body-md"
            />
          </label>

          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              data-testid="catalog-create-submit"
              onClick={() => void create()}
              className="min-h-12 flex-1 rounded-xl bg-accent-hover px-3 text-body-md font-bold text-white focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              Guardar producto
            </button>
            <button
              type="button"
              onClick={() => setCreating(false)}
              className="min-h-12 flex-1 rounded-xl border border-surface-border px-3 text-body-md font-semibold text-text-primary hover:bg-zinc-100"
            >
              Cancelar
            </button>
          </div>
          <p className="text-caption-xs text-text-muted">
            El alta se encola y se sincroniza sola. El stock no se edita acá: se mueve con ventas y
            mermas.
          </p>
        </div>
      ) : null}

      <input
        aria-label="Buscar producto"
        data-testid="catalog-search"
        onChange={(event) => setQuery(event.target.value)}
        placeholder="Buscar por nombre o GTIN"
        type="search"
        value={query}
        className="min-h-12 rounded-xl border border-surface-border bg-surface-card px-3 text-body-md text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
      />

      {!loaded ? (
        <p data-testid="catalog-loading" className="text-body-sm text-text-secondary">
          Leyendo el catálogo del dispositivo…
        </p>
      ) : products.length === 0 ? (
        <p data-testid="catalog-empty" className="rounded-2xl border border-surface-border bg-surface-card p-4 text-body-sm text-text-secondary">
          Este dispositivo todavía no tiene productos. Da de alta el primero con «Nuevo producto».
        </p>
      ) : visible.length === 0 ? (
        <p data-testid="catalog-no-hits" className="text-body-sm text-text-secondary">
          Ningún producto coincide con la búsqueda.
        </p>
      ) : (
        <ul aria-label="Productos" className="flex flex-col gap-1">
          {visible.map((product) => {
            const level = stockLevelOf(product.stock)
            const editing = editingId === product.id

            return (
              <li
                key={product.id}
                data-level={level}
                data-testid={`catalog-row-${product.id}`}
                className="flex flex-col gap-2 rounded-xl border border-surface-border bg-surface-card px-3 py-2"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="flex min-w-0 items-center gap-2">
                    {product.photo_url === null ? null : (
                      <img alt="" className="size-8 rounded-lg object-cover" src={product.photo_url} />
                    )}
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate text-body-md font-semibold text-text-primary">{product.name}</span>
                      <span className="font-mono text-caption-xs tabular-nums text-text-secondary">
                        {product.gtin ?? 'sin GTIN'} · {product.uom} · stock {Math.round(product.stock * 1000) / 1000}
                      </span>
                    </span>
                  </span>

                  <span className="flex shrink-0 items-center gap-2">
                    <span className="font-mono tabular-nums text-body-md text-text-primary">
                      {formatCentsToSoles(product.price_cents)}
                    </span>
                    <button
                      type="button"
                      data-testid={`catalog-edit-${product.id}`}
                      onClick={() => startEditing(product)}
                      className={cn(
                        'min-h-9 rounded-lg border border-surface-border px-2 text-body-sm text-text-primary hover:bg-zinc-100',
                        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                      )}
                    >
                      Editar
                    </button>
                  </span>
                </div>

                {editing ? (
                  <div className="flex flex-col gap-2 border-t border-surface-border pt-2">
                    <input
                      aria-label="Nombre del producto"
                      data-testid="catalog-edit-name"
                      onChange={(event) => setEditName(event.target.value)}
                      type="text"
                      value={editName}
                      className="min-h-11 rounded-xl border border-surface-border px-3 text-body-md"
                    />
                    <input
                      aria-label="Precio en soles"
                      data-testid="catalog-edit-price"
                      inputMode="decimal"
                      onChange={(event) => setEditPrice(event.target.value)}
                      type="text"
                      value={editPrice}
                      className="min-h-11 rounded-xl border border-surface-border px-3 font-mono text-body-md"
                    />
                    {canSaveOnline ? null : (
                      <p data-testid="catalog-edit-offline" className="text-body-sm text-text-secondary">
                        La edición es solo en línea: el PATCH no se encola y sin conexión no se guarda.
                      </p>
                    )}
                    <div className="flex flex-wrap gap-2">
                      <button
                        type="button"
                        data-testid="catalog-edit-save"
                        disabled={!canSaveOnline || saving}
                        onClick={() => void saveEdit(product)}
                        className="min-h-11 flex-1 rounded-xl bg-accent-hover px-3 text-body-md font-bold text-white disabled:pointer-events-none disabled:opacity-40"
                      >
                        {saving ? 'Guardando…' : 'Guardar cambios'}
                      </button>
                      <button
                        type="button"
                        onClick={() => setEditingId(null)}
                        className="min-h-11 flex-1 rounded-xl border border-surface-border px-3 text-body-md font-semibold text-text-primary hover:bg-zinc-100"
                      >
                        Cancelar
                      </button>
                    </div>
                  </div>
                ) : null}
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
