import { AlertTriangle, Banknote, LayoutDashboard, Package, Tags, Users, X } from 'lucide-react'
import { useState } from 'react'

import { clearAdminSession, type AdminSession } from '@/auth/accessSession'
import { ConnectivityBadge } from '@/components/ConnectivityBadge'
import { PendingSyncBadge } from '@/components/PendingSyncBadge'
import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'
import { cn } from '@/lib/utils'
import { AdminCatalogScreen } from '@/screens/AdminCatalogScreen'
import { AdminDashboardScreen } from '@/screens/AdminDashboardScreen'
import { AdminInventoryScreen } from '@/screens/AdminInventoryScreen'
import { AdminWasteScreen } from '@/screens/AdminWasteScreen'
import { CashCloseScreen } from '@/screens/CashCloseScreen'
import { FiadoAccountsScreen } from '@/screens/FiadoAccountsScreen'

/**
 * Shell del rol Administrador (W8-FE-2; docs/FRONTEND.md:48; docs/DESIGN.md:318, :340, :573-645).
 *
 * **Los seis destinos son los del diseño**, ni uno más: Dashboard / Auditoría, Cierre de caja,
 * Cuentas y Fiados, Inventario / Stock Rápido, Mermas y Daños y Catálogo Maestro. «Ajustes de
 * dispositivos» queda fuera a propósito (delta D3). La sidebar es de 240px y colapsa a una
 * columna apilada en pantallas angostas: la suite Admin es de escritorio 1440x900 (§7.1) y el
 * shell móvil del empleado es otro.
 *
 * **El cierre de sesión es del lado del cliente.** `ARCHITECTURE.md:122-135` enumera la
 * superficie HTTP y no tiene endpoint de logout, así que el botón borra la fila de
 * `sessionStorage` y nada más: no se inventa una ruta que el contrato no tiene.
 *
 * **Los seis destinos tienen módulo.** Dashboard, Cierre de caja, Cuentas y Fiados, Inventario,
 * Mermas y Catálogo son pantallas reales; ninguna queda anunciada como pendiente.
 */

/** Los seis destinos del diseño, en su orden. */
export type AdminDestinationId = 'dashboard' | 'cash-close' | 'accounts' | 'inventory' | 'waste' | 'catalog'

interface AdminDestination {
  readonly id: AdminDestinationId
  readonly label: string
  readonly icon: typeof LayoutDashboard
}

export const ADMIN_DESTINATIONS: readonly AdminDestination[] = [
  { id: 'dashboard', label: 'Dashboard / Auditoría', icon: LayoutDashboard },
  { id: 'cash-close', label: 'Cierre de caja', icon: Banknote },
  { id: 'accounts', label: 'Cuentas y Fiados', icon: Users },
  { id: 'inventory', label: 'Inventario / Stock Rápido', icon: Package },
  { id: 'waste', label: 'Mermas y Daños', icon: AlertTriangle },
  { id: 'catalog', label: 'Catálogo Maestro', icon: Tags },
]

export interface AdminShellProps {
  readonly session: AdminSession
  /** Se llama después de borrar la sesión: el shell vuelve al selector de rol. */
  readonly onLogout: () => void
  readonly initialDestination?: AdminDestinationId
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Inyectable para las pruebas; en producción, `navigator.share` del sistema. */
  readonly share?: (data?: ShareData) => Promise<void>
}

export function AdminShell({
  session,
  onLogout,
  initialDestination = 'dashboard',
  database = getPosDatabase(),
  fetchImpl,
  share,
}: AdminShellProps) {
  const [destination, setDestination] = useState<AdminDestinationId>(initialDestination)
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  // El inventario pide la merma de un producto concreto: el shell la lleva al módulo con él
  // elegido, en lugar de obligar al operador a buscarlo otra vez.
  const [wasteProductId, setWasteProductId] = useState<string | undefined>(undefined)

  function logout(): void {
    // El cierre de sesión es un borrado local: no hay endpoint que llamar.
    clearAdminSession()
    onLogout()
  }

  function navigate(next: AdminDestinationId): void {
    setDestination(next)
    setMobileNavOpen(false)
  }

  const navigation = (
    <nav aria-label="Módulos de administración" className="flex flex-1 flex-col gap-1 overflow-y-auto p-3">
      {ADMIN_DESTINATIONS.map((entry) => {
        const Icon = entry.icon
        const isActive = entry.id === destination

        return (
          <button
            key={entry.id}
            type="button"
            aria-current={isActive ? 'page' : undefined}
            data-destination={entry.id}
            data-label={entry.label}
            data-testid={`admin-destination-${entry.id}`}
            onClick={() => navigate(entry.id)}
            className={cn(
              'flex min-h-12 items-center gap-3 rounded-xl px-3 text-left',
              'transition-colors duration-[120ms] ease-out',
              'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
              isActive
                ? 'border border-accent-hover bg-accent-primary/10 text-text-primary'
                : 'border border-transparent text-text-primary hover:bg-zinc-100',
            )}
          >
            <Icon aria-hidden="true" className="size-5 shrink-0" />
            <span className="text-body-md font-semibold">{entry.label}</span>
          </button>
        )
      })}
    </nav>
  )

  return (
    <div className="flex min-h-screen flex-col bg-surface-bg text-text-primary md:flex-row">
      <aside className="flex w-full shrink-0 flex-col border-b border-surface-border bg-surface-card md:w-60 md:border-b-0 md:border-r">
        <div className="flex items-center gap-2 border-b border-surface-border p-4">
          <div className="flex min-w-0 flex-col" data-testid="admin-brand">
            <h1 className="truncate text-body-md font-bold text-text-primary">{session.tenantName}</h1>
            <span className="font-mono text-caption-xs text-text-secondary">v{__APP_VERSION__}</span>
          </div>
          <button
            type="button"
            aria-expanded={mobileNavOpen}
            aria-label={mobileNavOpen ? 'Ocultar módulos' : 'Mostrar módulos'}
            className="ml-auto flex size-10 items-center justify-center rounded-xl text-text-secondary hover:bg-surface-subtle md:hidden"
            onClick={() => setMobileNavOpen((open) => !open)}
          >
            {mobileNavOpen ? <X aria-hidden="true" className="size-4" /> : <LayoutDashboard aria-hidden="true" className="size-4" />}
          </button>
        </div>

        <div className={cn('flex-col md:flex', mobileNavOpen ? 'flex' : 'hidden')}>
          {navigation}

          <div className="flex flex-col gap-2 border-t border-surface-border p-3">
            <div className="flex items-center justify-between gap-2">
              <span
                data-testid="admin-role-badge"
                className="rounded-full border border-accent-hover px-2 py-1 text-caption-xs uppercase text-text-primary"
              >
                Administrador
              </span>
              <ConnectivityBadge />
            </div>
            <PendingSyncBadge />
          </div>

          <div className="flex flex-col gap-1 border-t border-surface-border p-3" data-testid="admin-session-footer">
            <span className="font-mono text-caption-xs tabular-nums text-text-secondary">{session.phone}</span>
            <button
              type="button"
              data-testid="admin-logout"
              onClick={logout}
              className="flex min-h-12 items-center justify-center rounded-xl border border-surface-border px-3 text-body-md font-semibold text-text-primary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              Cerrar sesión
            </button>
          </div>
        </div>
      </aside>

      <main className="flex-1">
        {destination === 'dashboard' ? (
          <AdminDashboardScreen
            database={database}
            fetchImpl={fetchImpl}
            token={session.token}
          />
        ) : destination === 'cash-close' ? (
          <CashCloseScreen database={database} token={session.token} />
        ) : destination === 'accounts' ? (
          <FiadoAccountsScreen database={database} fetchImpl={fetchImpl} share={share} token={session.token} />
        ) : destination === 'inventory' ? (
          <AdminInventoryScreen
            database={database}
            onWasteRequested={(productId) => {
              setWasteProductId(productId)
              navigate('waste')
            }}
          />
        ) : destination === 'waste' ? (
          <AdminWasteScreen database={database} initialProductId={wasteProductId} />
        ) : (
          <AdminCatalogScreen database={database} fetchImpl={fetchImpl} token={session.token} />
        )}
      </main>
    </div>
  )
}
