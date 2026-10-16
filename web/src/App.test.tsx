// Debe ser la primera importación: la pantalla de acceso lee IndexedDB.
import 'fake-indexeddb/auto'

import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from '@/App'

/*
 * Shell de la PWA. La unidad W2-FE-1 lo convierte en la entrada real de la
 * aplicación: la pantalla de acceso del empleado. Lo que se afirma acá es el
 * comportamiento del conjunto —el armazón, la pantalla y la ausencia de
 * peticiones antes de autenticar—, no una tautología sobre un `div`.
 */

afterEach(() => {
  vi.unstubAllGlobals()
})

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
})
