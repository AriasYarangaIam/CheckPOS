import { useState } from 'react'

import { ConnectivityBadge } from '@/components/ConnectivityBadge'
import { PendingSyncBadge } from '@/components/PendingSyncBadge'
import { CounterSaleScreen } from '@/screens/CounterSaleScreen'
import { EmployeeAccessScreen } from '@/screens/EmployeeAccessScreen'

/**
 * Shell de la PWA. Su entrada es la pantalla de acceso del empleado
 * (`docs/DESIGN.md:104-105`) y su cabecera lleva el badge de conectividad en
 * vivo (`docs/DESIGN.md:127-130`; `docs/FRONTEND.md:101-103`).
 *
 * El badge vive en la cabecera y no sobre la operación: §1.1:130 pide que el
 * POS conmute a almacenamiento local «sin interrumpir ventas ni alertar con
 * ventanas bloqueantes», así que el estado de red se lee sin abrir nada.
 *
 * La cabecera es un `<header>` de primer nivel —fuera de `<main>`, para que sea
 * un landmark `banner` y no un `header` con scope de contenido— y el shell no
 * pide nada al servidor antes de autenticar: el tenant sale del token y de
 * ningún otro lado (`ARCHITECTURE.md:23`), así que no hay datos de tenant que
 * traer en el arranque.
 *
 * Tras el desbloqueo del PIN el shell sustituye la pantalla de acceso por la
 * venta de mostrador (W4-FE-1): la autenticación offline no lleva token, así
 * que el shell no pide nada a la red para mostrar la operación.
 */
export function App() {
  const [unlocked, setUnlocked] = useState(false)

  return (
    <div className="flex min-h-screen flex-col bg-surface-bg text-text-primary">
      <header className="flex items-center justify-between gap-2 border-b border-surface-border bg-surface-card px-4 py-3">
        <h1 className="text-heading-md font-bold">POS SaaS</h1>
        {/*
         * Los dos badges de estado van juntos al lado opuesto del wordmark: el
         * de conectividad (W2-FE-2) y el de ventas por sincronizar (W5-FE-1).
         * Los dos crecen hacia su lado libre, sin desplazar al resto de la fila.
         */}
        <div className="flex items-center gap-2">
          <PendingSyncBadge />
          <ConnectivityBadge />
        </div>
      </header>
      <main className="flex-1">
        {unlocked ? (
          <CounterSaleScreen />
        ) : (
          <EmployeeAccessScreen onUnlocked={() => setUnlocked(true)} />
        )}
      </main>
    </div>
  )
}
