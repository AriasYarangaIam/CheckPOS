import { CartPanel } from '@/components/CartPanel'
import { ProductCatalogScreen } from '@/screens/ProductCatalogScreen'
import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'

/**
 * Pantalla de venta de mostrador (W4-FE-1; docs/DESIGN.md §Módulo 1). Es la
 * superficie donde viven la grilla de productos y el carrito en curso: la
 * grilla entrega el código identificado y el carrito resuelve y persiste.
 *
 * El resto del flujo —pantalla de cobro, ticket y cámara best-effort— son las
 * unidades siguientes de este mismo slice: esta pantalla es su contenedor.
 */
export interface CounterSaleScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

export function CounterSaleScreen({
  database = getPosDatabase(),
}: CounterSaleScreenProps) {
  return (
    <section
      aria-labelledby="counter-sale-title"
      className="mx-auto flex w-full max-w-md flex-col gap-4 p-4"
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="counter-sale-title">
          Punto de venta
        </h2>
        <p className="text-body-sm text-text-secondary">
          Un toque en la grilla agrega la línea; el carrito queda guardado en el dispositivo
        </p>
      </div>

      <ProductCatalogScreen database={database} />
      <CartPanel database={database} />
    </section>
  )
}
