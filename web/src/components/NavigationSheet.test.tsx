// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { saveDeviceSession } from '@/auth/deviceSession'
import { NavigationSheet } from '@/components/NavigationSheet'
import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'

/*
 * Navigation sheet (W5-FE-3; docs/DESIGN.md §3.1:182-192; docs/FRONTEND.md FASE
 * 6). Menú lateral con los destinos en el orden del documento: los dos que
 * tienen superficie en este sprint navegan y los otros cuatro se declaran no
 * disponibles en lugar de navegar a una pantalla inexistente (decisión 6 del
 * sprint). El bloque de administración vive separado en la base del panel y
 * queda tras el PIN del dispositivo.
 */

const PIN = '7391'
const WRONG_PIN = '0001'
const OPEN_WINDOW = '2099-01-01T00:00:00.000Z'

const createdNames = new Set<string>()
let nextSuffix = 0

function freshDatabase(): PosDatabase {
  nextSuffix += 1
  const name = `${POS_DATABASE_NAME}-nav-sheet-test-${nextSuffix}`
  createdNames.add(name)

  return new PosDatabase(name)
}

async function boundDatabase(): Promise<PosDatabase> {
  const database = freshDatabase()

  await saveDeviceSession(
    { deviceId: 'device-nav-test', userId: 'user-nav-test', validUntil: OPEN_WINDOW, pin: PIN },
    database,
  )

  return database
}

afterEach(async () => {
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

interface SheetHarnessProps {
  readonly database: PosDatabase
  readonly onNavigate?: (surface: 'sale' | 'cash-close') => void
  readonly onOpenChange?: (open: boolean) => void
  readonly activeSurface?: 'sale' | 'cash-close'
}

function renderSheet({
  database,
  onNavigate = () => {},
  onOpenChange = () => {},
  activeSurface = 'sale',
}: SheetHarnessProps) {
  return render(
    <NavigationSheet
      activeSurface={activeSurface}
      database={database}
      onNavigate={onNavigate}
      onOpenChange={onOpenChange}
      open
    />,
  )
}

function typePin(digits: string): void {
  for (const digit of digits) {
    fireEvent.click(screen.getByRole('button', { name: digit }))
  }
}

describe('NavigationSheet', () => {
  it('lists the six destinations in the order the design document fixes', () => {
    const database = freshDatabase()

    renderSheet({ database })

    const destinations = screen.getAllByTestId(/^nav-destination-/)

    expect(destinations.map((entry) => entry.dataset.destination)).toEqual([
      'sale',
      'cash-close',
      'accounts',
      'inventory',
      'waste',
      'attendance',
    ])
    expect(destinations.map((entry) => entry.dataset.label)).toEqual([
      'Punto de Venta',
      'Gestión de Caja y Turnos',
      'Cuentas y Fiados',
      'Inventario / Stock Rápido',
      'Mermas y Daños',
      'Marcaje de Asistencia',
    ])
    // El destino activo se anuncia como la página actual.
    expect(destinations[0]?.getAttribute('aria-current')).toBe('page')

    database.close()
  })

  it('closes the sheet for the sales surface and navigates to the cash close', () => {
    const database = freshDatabase()
    const onNavigate = vi.fn()
    const onOpenChange = vi.fn()

    renderSheet({ database, onNavigate, onOpenChange })

    fireEvent.click(screen.getByTestId('nav-destination-sale'))
    expect(onNavigate).toHaveBeenCalledWith('sale')
    expect(onOpenChange).toHaveBeenCalledWith(false)

    fireEvent.click(screen.getByTestId('nav-destination-cash-close'))
    expect(onNavigate).toHaveBeenCalledWith('cash-close')

    database.close()
  })

  it('renders the four surfaces of later sprints as unavailable and never navigates', () => {
    const database = freshDatabase()
    const onNavigate = vi.fn()

    renderSheet({ database, onNavigate })

    for (const destination of ['accounts', 'inventory', 'waste', 'attendance']) {
      const entry = screen.getByTestId(`nav-destination-${destination}`)

      // Declarado no disponible en lugar de navegar a una pantalla que no
      // existe: `aria-disabled` y el marcador visible, sin `disabled` para que
      // el foco y el lector lleguen al motivo.
      expect(entry.getAttribute('aria-disabled'), destination).toBe('true')
      expect(entry.textContent, destination).toContain('Próximamente')

      fireEvent.click(entry)
    }

    expect(onNavigate).not.toHaveBeenCalled()

    database.close()
  })

  it('keeps the admin block locked behind the device PIN', async () => {
    const database = await boundDatabase()

    renderSheet({ database })

    // Bloqueado: el dashboard no existe y el candado sí, separado del menú.
    expect(screen.getByTestId('nav-admin-lock')).toBeTruthy()
    expect(screen.queryByTestId('nav-admin-dashboard')).toBeNull()

    fireEvent.click(screen.getByTestId('nav-admin-lock'))

    // Un PIN incorrecto deja el bloque cerrado y lo dice.
    typePin(WRONG_PIN)

    expect(await screen.findByTestId('nav-admin-error')).toBeTruthy()
    expect(screen.getByTestId('nav-admin-error').textContent).toContain('PIN incorrecto')
    expect(screen.queryByTestId('nav-admin-dashboard')).toBeNull()

    // El PIN correcto revela la entrada, honestamente marcada como no
    // disponible en este sprint.
    typePin(PIN)

    const dashboard = await screen.findByTestId('nav-admin-dashboard')

    expect(dashboard.getAttribute('aria-disabled')).toBe('true')
    expect(dashboard.textContent).toContain('Próximamente')
    expect(screen.queryByTestId('nav-admin-error')).toBeNull()

    database.close()
  })

  it('never renders the digits of the admin PIN', async () => {
    const database = await boundDatabase()

    renderSheet({ database })
    fireEvent.click(screen.getByTestId('nav-admin-lock'))

    typePin('739')

    const display = screen.getByTestId('nav-admin-pin-display')

    expect(display.textContent).toBe('●●●○')
    // Ni texto, ni marcado, ni un campo con `value`: el PIN no tiene forma
    // legible en el DOM.
    expect(document.body.textContent).not.toContain('739')
    expect(document.body.innerHTML).not.toContain('739')
    expect(document.querySelectorAll('input')).toHaveLength(0)

    database.close()
  })

  it('relocks the admin block when the sheet closes', async () => {
    const database = await boundDatabase()
    const onOpenChange = vi.fn()

    const { rerender } = renderSheet({ database, onOpenChange })

    fireEvent.click(screen.getByTestId('nav-admin-lock'))
    typePin(PIN)
    await screen.findByTestId('nav-admin-dashboard')

    // Cerrar y volver a abrir dentro de la misma sesión: el desbloqueo era de
    // la visita, no de la jornada.
    rerender(
      <NavigationSheet
        activeSurface="sale"
        database={database}
        onNavigate={() => {}}
        onOpenChange={onOpenChange}
        open={false}
      />,
    )
    rerender(
      <NavigationSheet
        activeSurface="sale"
        database={database}
        onNavigate={() => {}}
        onOpenChange={onOpenChange}
        open
      />,
    )

    expect(screen.queryByTestId('nav-admin-dashboard')).toBeNull()
    expect(screen.getByTestId('nav-admin-lock')).toBeTruthy()

    database.close()
  })

  it('animates in with the sheet spring and fades under reduced motion', () => {
    const database = freshDatabase()

    renderSheet({ database })

    const dialog = screen.getByRole('dialog')

    // El resorte del panel lateral es el del §5.2 («Drawer / Sheet: spring
    // suave, salida 300ms») y su variante sin escala vive en `index.css`.
    expect(dialog.className).toContain('animate-sheet-in')

    const candidates = [join(process.cwd(), 'src'), join(process.cwd(), 'web', 'src')]
    const source = candidates.find((candidate) => existsSync(join(candidate, 'index.css')))

    expect(source, 'no se encontró el árbol de fuentes').toBeDefined()

    const css = readFileSync(join(source as string, 'index.css'), 'utf8')

    expect(css).toContain('--animate-sheet-in: sheet-in 300ms cubic-bezier(0.16, 1, 0.3, 1)')
    expect(css).toMatch(/@media \(prefers-reduced-motion: reduce\)[\s\S]*\.animate-sheet-in/)
    expect(css).toContain('@keyframes sheet-in-reduced')

    database.close()
  })

  it('shows the store name and the software version in the header', async () => {
    const database = freshDatabase()

    renderSheet({ database })

    const header = within(screen.getByRole('dialog'))

    expect(header.getByText('POS SaaS')).toBeTruthy()
    expect(header.getByTestId('nav-version').textContent).toMatch(/^v\d+\.\d+\.\d+$/)

    database.close()
  })
})
