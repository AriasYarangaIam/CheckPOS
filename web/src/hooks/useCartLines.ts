import { useEffect, useState } from 'react'

import { subscribeToCart } from '@/cart/cartStore'
import { getPosDatabase, type CartRow, type PosDatabase } from '@/db/posDatabase'

/**
 * Lectura en vivo de las líneas del carrito para el render (W4-FE-1). El
 * estado verdadero es la tabla `cart` de IndexedDB: el hook relee la tabla
 * cuando cualquier mutación la toca —cada mutación emite `subscribeToCart`—,
 * así que al recargar la aplicación el carrito se restaura con una lectura
 * (docs/FRONTEND.md:99-106; SPRINTS.md W4-FE-1).
 */
export function useCartLines(database: PosDatabase = getPosDatabase()): readonly CartRow[] {
  const [lines, setLines] = useState<readonly CartRow[]>([])
  const [generation, setGeneration] = useState(0)

  // El emisor sobrevive al remount: la suscripción no depende de props, así
  // que una remontura del panel no pierde notificaciones en el hueco.
  useEffect(
    () => subscribeToCart(() => setGeneration((generation) => generation + 1)),
    [],
  )

  useEffect(() => {
    let live = true

    void database.cart.toArray().then((rows) => {
      if (live) setLines(rows)
    })

    return () => {
      live = false
    }
  }, [database, generation])

  return lines
}
