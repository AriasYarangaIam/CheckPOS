import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useNetworkStatus } from '@/hooks/useNetworkStatus'

/*
 * Estado de conectividad observable (docs/FRONTEND.md:101-103).
 *
 * El hook lee el estado inicial de `navigator.onLine` y escucha los dos eventos
 * del `window`. Se prueba el camino real: se redefine `navigator.onLine` y se
 * despachan eventos de verdad, en lugar de llamar a un `setState` de mentira.
 */

/** Fija `navigator.onLine` para el test y lo restaura al terminar. */
function setOnLine(value: boolean): void {
  Object.defineProperty(window.navigator, 'onLine', { configurable: true, get: () => value })
}

afterEach(() => {
  Reflect.deleteProperty(window.navigator, 'onLine')
})

function StatusProbe() {
  const { isOnline } = useNetworkStatus()

  return <output data-testid="status">{isOnline ? 'online' : 'offline'}</output>
}

function status(): string {
  return screen.getByTestId('status').textContent ?? ''
}

describe('useNetworkStatus', () => {
  it('reads the initial state from navigator.onLine', () => {
    setOnLine(false)
    render(<StatusProbe />)

    expect(status()).toBe('offline')

    setOnLine(true)
    render(<StatusProbe />)

    expect(screen.getAllByTestId('status').map((node) => node.textContent)).toEqual([
      'offline',
      'online',
    ])
  })

  it('follows the offline and online events of the window', () => {
    setOnLine(true)
    render(<StatusProbe />)

    expect(status()).toBe('online')

    setOnLine(false)
    act(() => {
      window.dispatchEvent(new Event('offline'))
    })

    expect(status()).toBe('offline')

    setOnLine(true)
    act(() => {
      window.dispatchEvent(new Event('online'))
    })

    expect(status()).toBe('online')
  })

  it('believes navigator.onLine and not the name of the event', () => {
    /*
     * Mordida: la versión que mapea `'offline' -> false` por el nombre del
     * evento —en lugar de releer `navigator.onLine`— queda desincronizada
     * cuando el navegador emite un evento que no coincide con el estado real
     * (un `offline` que llega tarde, ya reconectado). Acá se emite ese evento
     * y el estado tiene que seguir siendo el de `navigator`.
     */
    setOnLine(true)
    render(<StatusProbe />)

    act(() => {
      window.dispatchEvent(new Event('offline'))
    })

    expect(status()).toBe('online')

    setOnLine(false)
    act(() => {
      window.dispatchEvent(new Event('online'))
    })

    expect(status()).toBe('offline')
  })

  it('removes both listeners when it unmounts', () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')

    setOnLine(true)

    const { unmount } = render(<StatusProbe />)
    const added = add.mock.calls.filter(([type]) => type === 'online' || type === 'offline')

    expect(added.map(([type]) => type).sort()).toEqual(['offline', 'online'])

    unmount()

    const removed = remove.mock.calls.filter(([type]) => type === 'online' || type === 'offline')

    expect(removed.map(([type]) => type).sort()).toEqual(['offline', 'online'])

    // El mismo handler que se registró es el que se da de baja: dar de baja
    // otra referencia deja el listener vivo y actualizando estado en un
    // componente desmontado.
    expect(removed.map(([, handler]) => handler)).toEqual(added.map(([, handler]) => handler))

    add.mockRestore()
    remove.mockRestore()
  })
})
