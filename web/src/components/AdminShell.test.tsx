// Debe ser la primera importación: el shell monta pantallas que leen IndexedDB.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ADMIN_SESSION_KEY, readAdminSession, saveAdminSession } from '@/auth/accessSession'
import { AdminShell, ADMIN_DESTINATIONS } from '@/components/AdminShell'
import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'

/*
 * Shell del rol Administrador (W8-FE-2; docs/FRONTEND.md:48; docs/DESIGN.md:318, :340, :573-645).
 *
 * Lo que se fija: los SEIS destinos del diseño con sus etiquetas y en su orden, el bloque de
 * marca con el nombre del tenant, el badge de rol, el footer de sesión con el teléfono y el
 * cierre de sesión del lado del cliente —sin endpoint, porque el contrato no tiene uno—, y que
 * un destino sin módulo todavía muestre un estado honesto en vez de inventar una pantalla.
 */

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-admin-shell-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

afterEach(async () => {
  sessionStorage.clear()
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
  vi.unstubAllGlobals()
})

const SESSION = {
  token: 'jwt-del-dueno',
  tenantName: 'Bodega Doña Rosa',
  phone: '+51988000111',
} as const

/** Monta el shell con una base propia y un cierre de sesión espiado. */
function renderShell(overrides: { onLogout?: () => void; initialDestination?: string } = {}) {
  const database = freshDatabase()
  const onLogout = overrides.onLogout ?? (() => undefined)

  const result = render(
    <AdminShell
      database={database}
      onLogout={onLogout}
      session={SESSION}
      initialDestination={overrides.initialDestination as never}
    />,
  )

  return { ...result, database, onLogout }
}

describe('AdminShell', () => {
  it('navigates the six destinations of the design, with their labels', () => {
    const { database } = renderShell()

    const nav = screen.getByRole('navigation', { name: 'Módulos de administración' })
    const labels = within(nav)
      .getAllByRole('button')
      .map((button) => button.getAttribute('data-label'))

    expect(labels).toEqual([
      'Dashboard / Auditoría',
      'Cierre de caja',
      'Cuentas y Fiados',
      'Inventario / Stock Rápido',
      'Mermas y Daños',
      'Catálogo Maestro',
    ])
    // `Ajustes de dispositivos` queda fuera a propósito (delta D3).
    expect(screen.queryByText(/ajustes de dispositivos/i)).toBeNull()

    database.close()
  })

  it('shows the tenant name as the brand and the admin role badge', () => {
    const { database } = renderShell()

    const brand = screen.getByTestId('admin-brand')
    expect(brand.textContent).toContain('Bodega Doña Rosa')
    expect(screen.getByTestId('admin-role-badge').textContent).toContain('Administrador')

    database.close()
  })

  it('closes the session on the client and returns to the selector', () => {
    const onLogout = vi.fn()

    // Una sesión guardada al montar, para probar que el cierre la borra de verdad.
    saveAdminSession(SESSION)
    const { database } = renderShell({ onLogout })

    const footer = screen.getByTestId('admin-session-footer')
    expect(footer.textContent).toContain(SESSION.phone)

    fireEvent.click(screen.getByTestId('admin-logout'))

    expect(onLogout).toHaveBeenCalled()
    expect(sessionStorage.getItem(ADMIN_SESSION_KEY)).toBeNull()
    expect(readAdminSession()).toBeNull()

    database.close()
  })

  it('opens on the dashboard with the welcome empty state while its module is not built', () => {
    const { database } = renderShell()

    // `docs/DESIGN.md:645`: el shell se renderiza con un estado de bienvenida vacío.
    expect(screen.getByTestId('admin-welcome').textContent).toContain('Selecciona un módulo')

    database.close()
  })

  it('renders the module that already exists when its destination is chosen', async () => {
    const { database } = renderShell()

    // El módulo se busca DENTRO del panel de contenido: la etiqueta del destino repite el nombre.
    fireEvent.click(screen.getByTestId('admin-destination-cash-close'))
    expect(
      await within(screen.getByRole('main')).findByRole('heading', { name: 'Cierre de caja' }),
    ).toBeTruthy()

    fireEvent.click(screen.getByTestId('admin-destination-accounts'))
    expect(
      await within(screen.getByRole('main')).findByRole('heading', { name: 'Cuentas y Fiados' }),
    ).toBeTruthy()

    database.close()
  })

  it('announces the destinations whose module is not part of this unit, without inventing one', () => {
    const { database } = renderShell()

    for (const destination of ADMIN_DESTINATIONS) {
      fireEvent.click(screen.getByTestId(`admin-destination-${destination.id}`))

      if (destination.id === 'dashboard' || destination.id === 'inventory' || destination.id === 'waste' || destination.id === 'catalog') {
        expect(screen.getByTestId('admin-pending').textContent).toContain(destination.label)
      }
    }

    database.close()
  })

  it('marks the active destination and keeps navigation available offline', async () => {
    Object.defineProperty(window.navigator, 'onLine', { configurable: true, get: () => false })
    const { database } = renderShell()

    try {
      const dashboard = screen.getByTestId('admin-destination-dashboard')
      expect(dashboard.getAttribute('aria-current')).toBe('page')

      fireEvent.click(screen.getByTestId('admin-destination-cash-close'))

      // Con la red caída la navegación NO se bloquea (offline-first, `docs/DESIGN.md:643`).
      expect(
        await within(screen.getByRole('main')).findByRole('heading', { name: 'Cierre de caja' }),
      ).toBeTruthy()
      expect(screen.getByTestId('admin-destination-cash-close').getAttribute('aria-current')).toBe('page')
      expect(screen.getByText('Modo Offline - IndexedDB')).toBeTruthy()
    } finally {
      Reflect.deleteProperty(window.navigator, 'onLine')
      database.close()
    }
  })

  it('hands the session token to the fiado wallet so it can restore from the server', async () => {
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({ accounts: [] }), { status: 200, headers: { 'content-type': 'application/json' } }),
    )
    const database = freshDatabase()

    render(
      <AdminShell
        database={database}
        fetchImpl={fetchSpy as unknown as typeof fetch}
        onLogout={() => undefined}
        session={SESSION}
      />,
    )

    fireEvent.click(screen.getByTestId('admin-destination-accounts'))
    fireEvent.click(await screen.findByTestId('fiado-restore'))

    await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
    const [url, init] = (fetchSpy.mock.calls[0] ?? []) as [string, RequestInit | undefined]
    expect(url).toBe('/fiado-accounts')
    expect((init?.headers as Record<string, string>).authorization).toBe(`Bearer ${SESSION.token}`)

    database.close()
  })
})
