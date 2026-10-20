import { Menu } from 'lucide-react'
import { useState } from 'react'

import { ConnectivityBadge } from '@/components/ConnectivityBadge'
import { NavigationSheet, type NavigationSurface } from '@/components/NavigationSheet'
import { PendingSyncBadge } from '@/components/PendingSyncBadge'
import { CashCloseScreen } from '@/screens/CashCloseScreen'
import { CounterSaleScreen } from '@/screens/CounterSaleScreen'
import { EmployeeAccessScreen } from '@/screens/EmployeeAccessScreen'

/**
 * Shell de la PWA. Su entrada es la pantalla de acceso del empleado
 * (`docs/DESIGN.md:104-105`) y su cabecera lleva los badges de estado en vivo
 * —conectividad (`docs/DESIGN.md:127-130`) y ventas por sincronizar (W5-FE-1)—.
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
 * operación (W4-FE-1) y habilita el menú lateral (W5-FE-3). El menú NO existe
 * antes del PIN: la pantalla de acceso sigue siendo la puerta y ningún panel
 * operativo se abre por delante de ella.
 *
 * Las dos superficies del sprint viven en `main` y el shell cambia entre ellas
 * sin recargar: la venta de mostrador y el arqueo de caja. El token en línea
 * todavía no se obtiene en el arranque —el ingreso en línea es de la Semana 8—,
 * así que la venta y el cierre se muestran sin token y cada uno dice su estado
 * honesto: la venta cierra localmente y el cierre de caja espera el acuse.
 */
export function App() {
  const [unlocked, setUnlocked] = useState(false)
  const [surface, setSurface] = useState<NavigationSurface>('sale')
  const [menuOpen, setMenuOpen] = useState(false)

  return (
    <div className="flex min-h-screen flex-col bg-surface-bg text-text-primary">
      <header className="flex items-center gap-2 border-b border-surface-border bg-surface-card px-4 py-3">
        {unlocked ? (
          <button
            type="button"
            aria-label="Abrir menú"
            data-testid="open-navigation"
            onClick={() => setMenuOpen(true)}
            className="flex size-12 shrink-0 items-center justify-center rounded-xl text-text-primary transition-colors duration-[120ms] ease-out hover:bg-surface-subtle focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            <Menu aria-hidden="true" className="size-5" />
          </button>
        ) : null}
        <h1 className="mr-auto text-heading-md font-bold">POS SaaS</h1>
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
          surface === 'cash-close' ? (
            <CashCloseScreen onBack={() => setSurface('sale')} />
          ) : (
            <CounterSaleScreen />
          )
        ) : (
          <EmployeeAccessScreen onUnlocked={() => setUnlocked(true)} />
        )}
      </main>
      {unlocked ? (
        <NavigationSheet
          activeSurface={surface}
          onNavigate={setSurface}
          onOpenChange={setMenuOpen}
          open={menuOpen}
        />
      ) : null}
    </div>
  )
}
