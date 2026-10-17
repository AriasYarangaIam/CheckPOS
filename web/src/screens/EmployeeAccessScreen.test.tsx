// Debe ser la primera importación: Dexie resuelve `indexedDB` del entorno global.
import 'fake-indexeddb/auto'

import Dexie from 'dexie'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { saveDeviceSession } from '@/auth/deviceSession'
import { POS_DATABASE_NAME, PosDatabase } from '@/db/posDatabase'
import { EmployeeAccessScreen } from '@/screens/EmployeeAccessScreen'

/*
 * Pantalla de acceso del empleado (docs/DESIGN.md:104-105; docs/FRONTEND.md:47).
 *
 * Lo que se prueba acá es el criterio de aceptación de W2-FE-1 completo:
 * desbloqueo dentro de la ventana, rechazo con motivo visible pasada la
 * expiración y **sin** bloquear el intento en línea posterior. Y las dos
 * propiedades de privacidad que el contrato pide: el PIN nunca se renderiza y
 * nunca llega al almacén.
 */

const PIN = '7391'
const WRONG_PIN = '0001'
const DEVICE_ID = '018f2b8e-8f9a-7c3d-9e10-000000000001'
const USER_ID = '018f2b8e-8f9a-7c3d-9e10-1a2b3c4d5e6f'
const OPEN_WINDOW = '2099-01-01T00:00:00.000Z'
const EXPIRED_WINDOW = '2020-01-01T00:00:00.000Z'

const createdNames = new Set<string>()
let nextId = 0

function freshDatabase(): PosDatabase {
  nextId += 1
  const name = `${POS_DATABASE_NAME}-access-test-${nextId}`
  createdNames.add(name)

  return new PosDatabase(name)
}

async function boundDatabase(validUntil = OPEN_WINDOW): Promise<PosDatabase> {
  const database = freshDatabase()

  await saveDeviceSession({ deviceId: DEVICE_ID, userId: USER_ID, validUntil, pin: PIN }, database)

  return database
}

afterEach(async () => {
  Reflect.deleteProperty(window.navigator, 'vibrate')
  await Promise.all([...createdNames].map((name) => Dexie.delete(name)))
  createdNames.clear()
})

/** Teclea el PIN en el numpad, dígito por dígito, como lo haría un dedo. */
function typePin(digits: string): void {
  for (const digit of digits) {
    fireEvent.click(screen.getByRole('button', { name: digit }))
  }
}

async function waitForReadyNumpad(): Promise<void> {
  await waitFor(() => {
    expect((screen.getByRole('button', { name: '1' }) as HTMLButtonElement).disabled).toBe(false)
  })
}

/** Todo lo que quedó escrito en la base local, serializado. */
async function storedText(database: PosDatabase): Promise<string> {
  const rows = await Promise.all(database.tables.map((table) => table.toArray()))

  return JSON.stringify(rows)
}

function installVibrate(): ReturnType<typeof vi.fn> {
  const vibrate = vi.fn()

  Object.defineProperty(window.navigator, 'vibrate', { configurable: true, value: vibrate })

  return vibrate
}

describe('EmployeeAccessScreen', () => {
  it('shows the PIN label, four masked positions and the ergonomic numpad', async () => {
    const database = await boundDatabase()

    render(<EmployeeAccessScreen database={database} />)

    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Acceso del empleado')
    expect(screen.getByText('PIN de 4 dígitos, sin depender de red')).toBeTruthy()
    expect(screen.getByText('PIN')).toBeTruthy()

    const display = screen.getByTestId('pin-display')

    // Cuatro posiciones, todas vacías, y `display-lg` (30px/800) es la escala
    // del display del numpad (docs/DESIGN.md §7.2:483).
    expect(display.textContent).toBe('○○○○')
    expect(display.className).toContain('text-display-lg')
    expect(screen.getAllByRole('button')).toHaveLength(12)

    await waitForReadyNumpad()
    database.close()
  })

  it('masks the entry: the typed digits are never rendered', async () => {
    const database = await boundDatabase()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    typePin('7')
    typePin('3')
    typePin('9')

    expect(screen.getByTestId('pin-display').textContent).toBe('●●●○')
    // Ni el texto, ni el marcado, ni un campo de formulario: el PIN no tiene
    // una representación legible en la pantalla. Tampoco hay `<input>`, así que
    // no hay `value` que un autocompletado o un script puedan leer.
    expect(document.body.textContent).not.toContain('739')
    expect(document.body.innerHTML).not.toContain('739')
    expect(document.querySelectorAll('input')).toHaveLength(0)

    typePin('1')

    await screen.findByText('Sesión desbloqueada')
    database.close()
  })

  it('unlocks inside the window with the bound PIN and stops reading keys', async () => {
    const database = await boundDatabase()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    typePin(PIN)

    await screen.findByText('Sesión desbloqueada')

    for (const key of screen.getAllByRole('button')) {
      expect((key as HTMLButtonElement).disabled).toBe(true)
    }

    database.close()
  })

  it('reports a wrong PIN in danger ink and clears the entry', async () => {
    const database = await boundDatabase()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    typePin(WRONG_PIN)

    const error = await screen.findByText('PIN incorrecto. Intenta de nuevo.')

    // La tinta de la acción peligrosa (§2.1:54) es la del error, y el display
    // vuelve a cero posiciones: el intento siguiente arranca limpio.
    expect(error.className).toContain('text-danger-action')
    expect(screen.getByTestId('pin-display').textContent).toBe('○○○○')

    // Y el numpad sigue disponible: un error no es una pared.
    expect((screen.getByRole('button', { name: '1' }) as HTMLButtonElement).disabled).toBe(false)

    database.close()
  })

  it('vibrates the error pattern only where haptics exist', async () => {
    const database = await boundDatabase()
    const vibrate = installVibrate()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    typePin(WRONG_PIN)

    await screen.findByText('PIN incorrecto. Intenta de nuevo.')
    // Patrón de error de §5.2:266.
    expect(vibrate).toHaveBeenCalledWith([30, 60, 30])

    database.close()
  })

  it('shows the same error without haptics instead of throwing', async () => {
    // jsdom no implementa `navigator.vibrate` y el mostrador puede ser un
    // equipo sin motor de vibración: el camino tiene que existir igual.
    const database = await boundDatabase()

    expect(window.navigator.vibrate).toBeUndefined()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    typePin(WRONG_PIN)

    await screen.findByText('PIN incorrecto. Intenta de nuevo.')
    database.close()
  })

  it('refuses past the window with a visible reason and without a modal', async () => {
    const database = await boundDatabase(EXPIRED_WINDOW)

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    const reason = await screen.findByText(/La sesión offline de este dispositivo venció/)

    // No bloquea: es un aviso no modal, sin trampa de foco y con el numpad
    // todavía operable. El PIN correcto sigue sin desbloquear.
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(reason.tagName).not.toBe('DIALOG')
    expect(screen.getByText(/el intento en línea sigue disponible/)).toBeTruthy()

    typePin(PIN)

    await waitFor(() => {
      expect(screen.queryByText('Sesión desbloqueada')).toBeNull()
    })

    expect(screen.getByText(/La sesión offline de este dispositivo venció/)).toBeTruthy()
    expect((screen.getByRole('button', { name: '1' }) as HTMLButtonElement).disabled).toBe(false)

    database.close()
  })

  it('refuses when the device never bound a session', async () => {
    const database = freshDatabase()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    await screen.findByText(/Este dispositivo todavía no tiene una sesión offline/)

    expect(screen.queryByRole('dialog')).toBeNull()
    typePin(PIN)

    await waitFor(() => {
      expect(screen.queryByText('Sesión desbloqueada')).toBeNull()
    })

    database.close()
  })

  it('does not block a later online attempt: the renewed window unlocks without a reload', async () => {
    /*
     * El criterio de aceptación de W2-FE-1 en una sola prueba: primero se
     * rechaza fuera de la ventana, después se renueva la sesión —lo que hace la
     * autenticación en línea, que refresca `offline_session_valid_until`
     * (decisión D8)— y el desbloqueo offline vuelve a funcionar **sin recargar
     * la aplicación**. Antes de la renovación el intento estaba rechazado; el
     * rechazo no se pegó.
     */
    const database = await boundDatabase(EXPIRED_WINDOW)

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()
    await screen.findByText(/La sesión offline de este dispositivo venció/)

    typePin(PIN)
    await waitFor(() => {
      expect(screen.queryByText('Sesión desbloqueada')).toBeNull()
    })

    await saveDeviceSession(
      { deviceId: DEVICE_ID, userId: USER_ID, validUntil: OPEN_WINDOW, pin: PIN },
      database,
    )
    await act(async () => {
      window.dispatchEvent(new Event('online'))
    })

    await waitFor(() => {
      expect(screen.queryByText(/La sesión offline de este dispositivo venció/)).toBeNull()
    })

    typePin(PIN)

    await screen.findByText('Sesión desbloqueada')
    expect(screen.queryByRole('dialog')).toBeNull()

    database.close()
  })

  it('keeps the PIN out of the DOM and out of every local store', async () => {
    const database = await boundDatabase()

    render(<EmployeeAccessScreen database={database} />)
    await waitForReadyNumpad()

    typePin(PIN)
    await screen.findByText('Sesión desbloqueada')

    expect(document.body.textContent).not.toContain(PIN)
    expect(document.body.innerHTML).not.toContain(PIN)
    expect(document.documentElement.outerHTML).not.toContain(PIN)
    expect(JSON.stringify(window.localStorage)).not.toContain(PIN)
    expect(JSON.stringify(window.sessionStorage)).not.toContain(PIN)

    // El almacén local guarda la sal y el verificador derivado, no el PIN
    // (docs/DESIGN.md:105; decisión D6).
    await expect(storedText(database)).resolves.not.toContain(PIN)

    database.close()
  })
})
