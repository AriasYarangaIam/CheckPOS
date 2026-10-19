import { useEffect, useMemo, useRef, useState } from 'react'

import { addIdentifiedLine } from '@/cart/identifiedLine'
import { ProductFavoritesGrid } from '@/components/ProductFavoritesGrid'
import { Input } from '@/components/ui/Input'
import { getPosDatabase, type PosDatabase, type ProductRow } from '@/db/posDatabase'
import { cn } from '@/lib/utils'

/**
 * Segmento Productos de la venta —la vista por defecto del MVP
 * (docs/DESIGN.md §1.2:135-137; SPRINTS.md W3-FE-2)—: la grilla de favoritos
 * con búsqueda instantánea por nombre y filtro por categoría.
 *
 * **Latencia cero.** El filtro no tiene debounce, ni `await`, ni retraso de
 * ningún tipo (docs/FRONTEND.md:23): el nombre tipeado filtra en el propio
 * render, sobre un `useMemo` del arreglo ya leído de IndexedDB. La lectura de
 * la base es la única operación asíncrona y ocurre una sola vez al abrir.
 * Tampoco hay animación de entrada (§5.2:266): el catálogo catálogo aparece y
 * se filtra al instante, igual que el numpad.
 *
 * **Filtro por categoría.** Los chips son los valores distintos y no nulos que
 * el subconjunto local conoce —la columna `category` es la proyección que
 * vive en la fila local (W3-FE-3)—. Un chip activo es `aria-pressed` y
 * «Todas» limpia el filtro; combinar categoría y búsqueda encadena los dos
 * criterios sobre la misma lista.
 *
 * **El carrito no vive acá.** La pantalla hospeda la grilla (adaptador de
 * entrada) y cablea su emisión al agregado mínimo de W3-FE-1; la semántica
 * completa de mutaciones del carrito es de W4-FE-1.
 */

/**
 * El filtro de nombre es una búsqueda por frase, no por token: «pan mama» no
 * aparecía con un split en palabras. La comparación no recorta ni normaliza
 * acentos: el catálogo es del tenant y su fila ya está tal como el dueño la
 * escribió.
 */
function filtersByName(product: ProductRow, query: string): boolean {
  return product.name.toLowerCase().includes(query.toLowerCase())
}

export interface ProductCatalogScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

export function ProductCatalogScreen({
  database = getPosDatabase(),
}: ProductCatalogScreenProps) {
  // `undefined` es «la base todavía no respondió»; el arreglo vacío es el
  // estado vacío honesto de un tenant sin productos sincronizados.
  const [products, setProducts] = useState<readonly ProductRow[] | undefined>(undefined)
  const [query, setQuery] = useState('')
  const [category, setCategory] = useState<string | null>(null)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true

    void database.products.toArray().then((rows) => {
      if (mounted.current) setProducts(rows)
    })

    return () => {
      mounted.current = false
    }
  }, [database])

  /** Categorías distintas del subconjunto, en un orden estable por nombre. */
  const categories = useMemo(() => {
    if (products === undefined) return []

    return [...new Set(products.flatMap((product) => (product.category === null ? [] : [product.category])))].sort()
  }, [products])

  /**
   * La lista visible: el filtro corre en el render y de forma síncrona, que es
   * el requisito del segmento — el tipeo no carga nada ni respira.
   */
  const visible =
    products === undefined
      ? []
      : products.filter(
          (product) =>
            (category === null || product.category === category) &&
            filtersByName(product, query),
        )

  function identify(code: string): void {
    void addIdentifiedLine(code, database)
  }

  return (
    <section
      aria-labelledby="product-catalog-title"
      className="mx-auto flex w-full max-w-md flex-col gap-3 p-4"
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="product-catalog-title">
          Productos
        </h2>
        <p className="text-body-sm text-text-secondary">
          Favoritos y granel: un toque agrega la línea
        </p>
      </div>

      {/* El buscador es un campo sin transiciones y sin debounce: cada tecla
          re-filtra en el render siguiente (docs/FRONTEND.md:23). Los valores
          de autocomplete dan al campo el perfil de búsqueda de catálogo y
          desconectan los formularios de contacto del navegador. */}
      <Input
        type="search"
        aria-label="Buscar producto"
        placeholder="Buscar por nombre"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="none"
        spellCheck={false}
        enterKeyHint="search"
      />

      {/* Filtro por categoría: los chips son los que trae el subconjunto. Sin
          transición y sin recarga: el cambio de filtro es solo un render más. */}
      <div
        role="group"
        aria-label="Filtrar por categoría"
        className="flex gap-2 overflow-x-auto pb-1"
      >
        {(
          [
            { label: 'Todas', value: null },
            ...categories.map((name) => ({ label: name, value: name })),
          ] as const
        ).map(({ label, value }) => (
          <button
            key={label}
            type="button"
            aria-pressed={category === value}
            onClick={() => setCategory(value)}
            data-testid="category-chip"
            className={cn(
              'min-h-12 shrink-0 rounded-xl border px-3 text-body-md font-semibold',
              // El seleccionado se marca con el acento del sistema: borde y
              // tinte de la misma decisión de color del 10% de acento.
              category === value
                ? 'border-accent-hover bg-accent-primary/10 text-text-primary'
                : 'border-surface-border bg-surface-card text-text-secondary hover:bg-zinc-100',
              'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
            )}
          >
            {label}
          </button>
        ))}
      </div>

      <ProductFavoritesGrid products={visible} onProductIdentified={identify} />
    </section>
  )
}
