import { Package } from 'lucide-react'

import { Money } from '@/components/Money'
import type { ProductRow } from '@/db/posDatabase'
import { formatCentsToSoles } from '@/lib/money'
import { cn } from '@/lib/utils'

/**
 * Grilla de favoritos y granel (docs/DESIGN.md:136; SPRINTS.md W3-FE-1).
 *
 * Es un adaptador de entrada (ARCHITECTURE.md:37): el tile sabe emitir el
 * código del producto por `onProductIdentified(code)` y NADA más — ni el precio
 * se recalcula acá, ni el carrito se conoce. Eso vuelve a la grilla reusuable
 * por los tres caminos de entrada que fija la arquitectura: la grilla es el
 * happy path, la búsqueda hereda este mismo emisor y la cámara se conecta al
 * mismo código (W4-FE-3).
 *
 * **Identidad del código.** El tile emite el GTIN cuando la fila lo tiene —el
 * mismo código que el escáner leería— y el id local para el granel sin GTIN.
 * El código nunca es la identidad de la línea: qty y precio viven en el
 * carrito, que resuelve (ARCHITECTURE.md:80). Ver `@/cart/identifiedLine`.
 *
 * **Táctil.** Mínimo de 48px de altura (`min-h-12`, docs/DESIGN.md:136;
 * docs/PRODUCT.md:64), respuesta al toque del sistema — `:active` baja a
 * `scale(0.98)` con `transition: transform 120ms ease-out`, nunca
 * `transition: all` (§6.2:302)— y feedback háptico del agregado:
 * `navigator.vibrate([60])` (§1.2:136). El hover queda dentro de la variante
 * de Tailwind v4, que ya compila `hover:` bajo `@media (hover: hover)` y deja
 * el estado pegado solo en los equipos con puntero fino.
 *
 * **Sin animación de entrada.** La grilla es acción de latencia cero y §5.2
 * prohíbe las animaciones gratuitas: los tiles aparecen y filtran al instante,
 * no hay `animate-*` en ningún elemento.
 */

/** Patrón háptico del agregado con un toque (docs/DESIGN.md:136). */
const ADD_VIBRATION: readonly number[] = [60]

/**
 * El código que un tile identifica para el carrito: GTIN si la fila lo tiene,
 * id local si es un granel sin GTIN. Es la única decisión de identidad que la
 * grilla toma y la resuelve el carrito, nunca ella.
 */
export function identifiedCodeOfProduct(product: ProductRow): string {
  return product.gtin ?? product.id
}

function vibrateAddFeedback(): void {
  // El mostrador puede ser un equipo sin motor de vibración —y jsdom tampoco
  // lo implementa—: la ausencia se ignora en lugar de romper el agregado (el
  // patrón ya probado en la pantalla de acceso, para el error).
  const vibrate = navigator.vibrate

  if (typeof vibrate === 'function') {
    vibrate.call(navigator, [...ADD_VIBRATION])
  }
}

export interface ProductFavoritesGridProps {
  /** La proyección local del catálogo, ya filtrada por quien hospeda la grilla. */
  readonly products: readonly ProductRow[]
  /** Se llama con el código identificado en el toque. El carrito resuelve. */
  readonly onProductIdentified: (code: string) => void
  readonly className?: string
}

export function ProductFavoritesGrid({
  products,
  onProductIdentified,
  className,
}: ProductFavoritesGridProps) {
  // El estado vacío se dice, no se esconde: la grilla sin catálogo es la vista
  // que la sincronización de W3-FE-3 va a llenar y sin aviso parecería rota.
  if (products.length === 0) {
    return (
      <p
        className="text-body-md text-text-secondary"
        role="status"
        data-testid="favorites-grid-empty"
      >
        El catálogo de este tenant todavía no tiene productos en el dispositivo.
      </p>
    )
  }

  return (
    <ul
      aria-label="Grilla de productos"
      className={cn('grid grid-cols-3 gap-2', className)}
    >
      {products.map((product) => {
        const code = identifiedCodeOfProduct(product)

        return (
          <li key={product.id}>
            <button
              type="button"
              data-testid="product-tile"
              aria-label={`${product.name}, ${formatCentsToSoles(product.price_cents)}`}
              onClick={() => {
                vibrateAddFeedback()
                onProductIdentified(code)
              }}
              className={cn(
                'flex min-h-12 w-full flex-col items-center gap-1 rounded-xl',
                'border border-surface-border bg-surface-card px-2 py-2',
                'text-center',
                'transition-transform duration-[120ms] ease-out active:scale-[0.98]',
                // El hover es de puntero fino: la variante de Tailwind v4 la
                // compila bajo `@media (hover: hover)` y el dedo no pega nada.
                'hover:bg-zinc-100',
                'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
              )}
            >
              {product.photo_url === null ? (
                <Package aria-hidden="true" className="size-8 text-text-muted" />
              ) : (
                <img
                  src={product.photo_url}
                  alt=""
                  // No es decorativa para la vista, pero sí para el lector de
                  // pantalla: el nombre ya es el contenido del tile.
                  className="h-10 w-full rounded-lg object-cover"
                />
              )}
              <span className="w-full truncate text-left text-body-sm leading-tight text-text-primary">
                {product.name}
              </span>
              <Money cents={product.price_cents} className="text-body-md text-text-primary" />
            </button>
          </li>
        )
      })}
    </ul>
  )
}
