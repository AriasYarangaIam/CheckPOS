import { useEffect, useState } from 'react'

import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'
import { pendingSaleCount, readQueuedSales, subscribeToSaleQueue } from '@/sales/saleQueue'

/**
 * Lectura en vivo de las ventas cerradas que todavía no confirmó el servidor
 * (W5-FE-1; docs/DESIGN.md §5.1:255). El estado verdadero es la cola de
 * IndexedDB: el hook relee la tabla cuando una mutación la toca —cada escritura
 * emite `subscribeToSaleQueue`—, así que un remontaje o una recarga de la PWA
 * vuelve a contar desde el almacén y no desde una copia en React.
 *
 * Sin IndexedDB legible el contador queda en cero y el badge no aparece: el
 * único estado que no se puede afirmar es el que no se pudo leer.
 */
export function useQueuedSaleCount(database: PosDatabase = getPosDatabase()): number {
  const [count, setCount] = useState(0)
  const [generation, setGeneration] = useState(0)

  // El emisor sobrevive al remount: la suscripción no depende de props.
  useEffect(
    () => subscribeToSaleQueue(() => setGeneration((current) => current + 1)),
    [],
  )

  useEffect(() => {
    let live = true

    void readQueuedSales(database)
      .then((rows) => {
        if (live) setCount(pendingSaleCount(rows))
      })
      .catch(() => {
        if (live) setCount(0)
      })

    return () => {
      live = false
    }
  }, [database, generation])

  return count
}
