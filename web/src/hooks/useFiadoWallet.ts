import { useEffect, useState } from 'react'

import { getPosDatabase, type FiadoAccountRow, type FiadoMovementRow, type PosDatabase } from '@/db/posDatabase'
import { readFiadoWallet } from '@/fiado/fiadoStore'

/**
 * Lectura de la cartera de deudores para el render (W7-FE-1). El estado
 * verdadero son las dos tablas de fiado de IndexedDB; el hook relee cuando el
 * componente se monta o cuando la pantalla lo pide con `reload`.
 *
 * No hay saldo acá: los movimientos viajan completos y quien los consume computa
 * la suma (`accountBalanceCents`), igual que el servidor (decisión 7 del
 * sprint 7).
 */
export interface FiadoWallet {
  readonly accounts: readonly FiadoAccountRow[]
  readonly movements: readonly FiadoMovementRow[]
}

export function useFiadoWallet(database: PosDatabase = getPosDatabase()): {
  readonly wallet: FiadoWallet
  readonly reload: () => void
} {
  const [wallet, setWallet] = useState<FiadoWallet>({ accounts: [], movements: [] })
  const [generation, setGeneration] = useState(0)

  useEffect(() => {
    let live = true

    void readFiadoWallet(database).then((read) => {
      if (live) setWallet(read)
    })

    return () => {
      live = false
    }
  }, [database, generation])

  return {
    wallet,
    reload: () => setGeneration((current) => current + 1),
  }
}
