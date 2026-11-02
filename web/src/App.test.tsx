// Debe ser la primera importación: la pantalla de acceso lee IndexedDB.
import 'fake-indexeddb/auto'

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from '@/App'
import { clearDeviceSession, saveDeviceSession } from '@/auth/deviceSession'

/*
 * Shell de la PWA. La unidad W2-FE-1 lo convirtió en la entrada real de la
 * aplicación —la pantalla de acceso del empleado— y W2-FE-2 le sumó el badge de
 * conectividad en la cabecera. Lo que se afirma acá es el comportamiento del
 * conjunto: el armazón, la pantalla, el badge y la ausencia de peticiones antes
 * de autenticar.
 */

/** Fija `navigator.onLine` para el test y lo restaura al terminar. */
function setOnLine(value: boolean): void {
  Object.defineProperty(window.navigator, 'onLine', { configurable: true, get: () => value })
}

afterEach(() => {
  Reflect.deleteProperty(window.navigator, 'onLine')
  sessionStorage.clear()
  vi.unstubAllGlobals()
})

/** Espera a que la lectura de la sesión termine y el numpad quede operable. */
async function waitForNumpad(): Promise<HTMLButtonElement> {
  await waitFor(() => {
    expect((screen.getByRole('button', { name: '1' }) as HTMLButtonElement).disabled).toBe(false)
  })

  return screen.getByRole('button', { name: '1' }) as HTMLButtonElement
}

/** Entra por la puerta del empleado: el rol y después el PIN. */
function chooseEmployee(): void {
  fireEvent.click(screen.getByTestId('role-card-employee'))
  fireEvent.click(screen.getByTestId('role-continue'))
}

/** Entra por la puerta del administrador hasta la pantalla de credencial. */
function chooseAdmin(): void {
  fireEvent.click(screen.getByTestId('role-card-admin'))
  fireEvent.click(screen.getByTestId('role-continue'))
}

describe('App shell', () => {
  it('mounts the main landmark of the shell', () => {
    render(<App />)

    expect(screen.getByRole('main')).toBeTruthy()
  })

  it('renders the product title as a level-1 heading', () => {
    render(<App />)

    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('POS SaaS')
  })

  it('opens on the role selector, with no credential and no tenant data', () => {
    render(<App />)

    // W8-FE-1: la primera pantalla es la puerta del rol (docs/FRONTEND.md:46;
    // docs/DESIGN.md:492), y antes de autenticar no hay ni un dato del local.
    expect(screen.getByTestId('role-selector')).toBeTruthy()
    expect(screen.getByTestId('role-card-employee')).toBeTruthy()
    expect(screen.getByTestId('role-card-admin')).toBeTruthy()
    expect(screen.queryByLabelText(/teléfono/i)).toBeNull()
    expect(screen.queryByText(/bodega/i)).toBeNull()
    // El wordmark y el estado de la conexión son los del shell, presentes también acá.
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('POS SaaS')
  })

  it('reaches the employee PIN screen from the role selector', () => {
    render(<App />)

    chooseEmployee()

    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Acceso del empleado')
    expect(screen.getByText('PIN')).toBeTruthy()
  })

  it('asks the server for nothing before authentication', () => {
    /*
     * El tenant sale solo del token (`ARCHITECTURE.md:23`), así que el arranque
     * no tiene nada que pedir. La mordida es el espía: cualquier `fetch` en el
     * montaje —un catálogo, una sesión, un `/me`— lo hace fallar.
     */
    const fetchSpy = vi.fn()

    vi.stubGlobal('fetch', fetchSpy)
    render(<App />)

    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('shows the connectivity badge in the shell header, next to the wordmark', () => {
    setOnLine(true)
    render(<App />)

    const header = screen.getByRole('banner')

    expect(within(header).getByText('En línea')).toBeTruthy()
    expect(within(header).getByRole('heading', { level: 1 }).textContent).toBe('POS SaaS')
  })

  it('never blocks a rendered interaction when the connection flips', async () => {
    /*
     * Criterio de aceptación de W2-FE-2: el badge alterna entre en línea y modo
     * offline «sin interrumpir ni bloquear la operación». El hijo interactivo es
     * una tecla real del numpad: se opera antes del cambio, se cambia el estado
     * de red y la tecla tiene que seguir respondiendo —y conservar el foco— sin
     * que aparezca nada por encima (docs/DESIGN.md:130, :255).
     */
    setOnLine(true)
    render(<App />)
    chooseEmployee()

    await waitForNumpad()

    fireEvent.click(screen.getByRole('button', { name: '1' }))
    expect(screen.getByTestId('pin-display').textContent).toBe('●○○○')

    const interactive = screen.getByRole('button', { name: '2' }) as HTMLButtonElement

    interactive.focus()
    expect(document.activeElement).toBe(interactive)

    const header = screen.getByRole('banner')
    const before = [...header.children]

    setOnLine(false)
    act(() => {
      window.dispatchEvent(new Event('offline'))
    })

    expect(screen.getByText('Modo Offline - IndexedDB')).toBeTruthy()

    // El hijo interactivo sigue ahí, habilitado y con el foco.
    expect(document.activeElement).toBe(interactive)
    expect(interactive.disabled).toBe(false)

    fireEvent.click(interactive)

    expect(screen.getByTestId('pin-display').textContent).toBe('●●○○')

    // Nada se abrió encima de la operación: sin modal ni overlay.
    expect(screen.queryByRole('dialog')).toBeNull()

    // Y la cabecera no cambió de forma: los mismos hijos, en el mismo orden. El
    // badge crece hacia su lado libre y no desplaza al wordmark.
    expect([...screen.getByRole('banner').children]).toEqual(before)
    expect(header.firstElementChild?.tagName).toBe('H1')
  })

  it('comes back to En línea when the connection returns', async () => {
    setOnLine(false)
    render(<App />)
    chooseEmployee()

    expect(screen.getByText('Modo Offline - IndexedDB')).toBeTruthy()

    setOnLine(true)
    act(() => {
      window.dispatchEvent(new Event('online'))
    })

    expect(screen.getByText('En línea')).toBeTruthy()
    expect(screen.queryByText('Modo Offline - IndexedDB')).toBeNull()

    // La vuelta de la conexión tampoco interrumpe: el numpad sigue operable.
    await waitForNumpad()
  })

  it('replaces the access screen with the counter sale after the PIN unlocks', async () => {
    /*
     * W4-FE-1 conecta el shell con la venta: un PIN correcto dentro de la
     * ventana desbloquea y el shell muestra la venta de mostrador, sin pedir
     * nada a la red (el desbloqueo offline no lleva token).
     */
    const validUntil = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()

    await saveDeviceSession({
      deviceId: 'device-app-test',
      userId: 'user-app-test',
      validUntil,
      pin: '1357',
    })

    try {
      render(<App />)
      chooseEmployee()
      await waitForNumpad()

      fireEvent.click(screen.getByRole('button', { name: '1' }))
      fireEvent.click(screen.getByRole('button', { name: '3' }))
      fireEvent.click(screen.getByRole('button', { name: '5' }))
      fireEvent.click(screen.getByRole('button', { name: '7' }))

      await screen.findByText('Punto de venta')
    } finally {
      await clearDeviceSession()
    }
  })

  it('never offers the navigation menu before the device PIN unlocks', () => {
    render(<App />)
    chooseEmployee()

    // El menú es de la operación, no del acceso: la pantalla de PIN sigue
    // siendo la puerta y no hay panel que abrir antes de desbloquear.
    expect(screen.queryByTestId('open-navigation')).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens the admin credential screen from the role selector', () => {
    render(<App />)

    chooseAdmin()

    expect(screen.getByTestId('admin-credential')).toBeTruthy()
    expect(screen.getByLabelText('Teléfono')).toBeTruthy()
    expect(screen.getByLabelText('Contraseña maestra')).toBeTruthy()
  })

  it('reaches the admin shell after a successful login and stores the token for the tab', async () => {
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) =>
      new Response(
        JSON.stringify({ token: 'jwt-de-prueba', tenant: { name: 'Bodega Doña Rosa' } }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
    )
    vi.stubGlobal('fetch', fetchSpy)

    try {
      render(<App />)
      chooseAdmin()

      fireEvent.change(screen.getByTestId('admin-phone'), { target: { value: '+51988000111' } })
      fireEvent.change(screen.getByTestId('admin-password'), { target: { value: 'clave-maestra' } })
      fireEvent.click(screen.getByTestId('admin-submit'))

      // El shell del administrador sustituye la cabecera del empleado: su marca es el tenant.
      expect(await screen.findByTestId('admin-brand')).toBeTruthy()
      expect(screen.getByTestId('admin-brand').textContent).toContain('Bodega Doña Rosa')
      expect(screen.getByRole('navigation', { name: 'Módulos de administración' })).toBeTruthy()
      expect(sessionStorage.getItem('pos-admin-session')).not.toBeNull()

      // Y el cierre de sesión vuelve al selector de rol, sin llamar a ningún endpoint.
      fireEvent.click(screen.getByTestId('admin-logout'))
      expect(await screen.findByTestId('role-selector')).toBeTruthy()
      expect(sessionStorage.getItem('pos-admin-session')).toBeNull()
    } finally {
      sessionStorage.clear()
    }
  })

  it('resumes the admin shell on a reload, because the session lives in the tab', () => {
    sessionStorage.setItem(
      'pos-admin-session',
      JSON.stringify({ token: 'jwt-guardado', tenantName: 'Bodega Doña Rosa', phone: '+51988000111' }),
    )

    try {
      render(<App />)

      expect(screen.getByTestId('admin-brand').textContent).toContain('Bodega Doña Rosa')
      // No vuelve a pedir la credencial: la sesión de la pestaña alcanza.
      expect(screen.queryByTestId('admin-credential')).toBeNull()
    } finally {
      sessionStorage.clear()
    }
  })

  it('opens the navigation sheet and switches the surface to the cash close and back', async () => {
    /*
     * W5-FE-3: el botón del menú vive en la cabecera y el panel lista los
     * destinos. De los seis, solo dos tienen superficie: Punto de Venta y
     * Gestión de Caja y Turnos. El shell cambia de superficie sin recargar.
     */
    const validUntil = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()

    await saveDeviceSession({
      deviceId: 'device-nav-app-test',
      userId: 'user-nav-app-test',
      validUntil,
      pin: '2468',
    })

    try {
      render(<App />)
      chooseEmployee()
      await waitForNumpad()

      for (const digit of '2468') {
        fireEvent.click(screen.getByRole('button', { name: digit }))
      }

      await screen.findByText('Punto de venta')

      fireEvent.click(screen.getByTestId('open-navigation'))

      const sheet = await screen.findByRole('dialog')

      expect(within(sheet).getAllByTestId(/^nav-destination-/)).toHaveLength(6)

      fireEvent.click(screen.getByTestId('nav-destination-cash-close'))

      expect(await screen.findByRole('heading', { name: 'Cierre de caja' })).toBeTruthy()
      // El panel se cerró al navegar.
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

      fireEvent.click(screen.getByTestId('open-navigation'))
      await screen.findByRole('dialog')
      fireEvent.click(screen.getByTestId('nav-destination-sale'))

      expect(await screen.findByRole('heading', { name: 'Punto de venta' })).toBeTruthy()
    } finally {
      await clearDeviceSession()
    }
  })
})
