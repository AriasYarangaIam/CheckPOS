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
  vi.unstubAllGlobals()
})

/** Espera a que la lectura de la sesión termine y el numpad quede operable. */
async function waitForNumpad(): Promise<HTMLButtonElement> {
  await waitFor(() => {
    expect((screen.getByRole('button', { name: '1' }) as HTMLButtonElement).disabled).toBe(false)
  })

  return screen.getByRole('button', { name: '1' }) as HTMLButtonElement
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

  it('opens on the employee access screen', () => {
    render(<App />)

    // Sin datos de tenant antes de autenticar (docs/DESIGN.md:104-106;
    // docs/FRONTEND.md:47): el shell es el wordmark del producto y la pantalla
    // de PIN, nada más.
    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Acceso del empleado')
    expect(screen.getByText('PIN')).toBeTruthy()
    expect(screen.getAllByRole('button')).toHaveLength(12)
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
})
