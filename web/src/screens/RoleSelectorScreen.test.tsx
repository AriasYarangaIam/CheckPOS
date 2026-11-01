import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { RoleSelectorScreen } from '@/screens/RoleSelectorScreen'

/*
 * Login-1 del rol Administrador (W8-FE-1; docs/FRONTEND.md:46; docs/DESIGN.md:451-492).
 *
 * Lo que se fija: dos tarjetas de rol y nada más —sin campos de credencial y sin un solo dato
 * del tenant antes de autenticar—, selección exclusiva, y el teclado del contrato
 * (`←`/`→` para elegir, `Enter` para continuar).
 */

describe('RoleSelectorScreen', () => {
  it('offers exactly the two roles with their contract sublines', () => {
    render(<RoleSelectorScreen onSelect={() => undefined} />)

    const employee = screen.getByTestId('role-card-employee')
    const admin = screen.getByTestId('role-card-admin')

    expect(employee.textContent).toContain('Empleado')
    expect(employee.textContent).toContain('PIN de 4 dígitos')
    expect(admin.textContent).toContain('Administrador')
    expect(admin.textContent).toContain('contraseña maestra')
    expect(screen.getAllByTestId(/^role-card-/)).toHaveLength(2)
  })

  it('shows no credential field and no tenant data before authenticating', () => {
    render(<RoleSelectorScreen onSelect={() => undefined} />)

    expect(screen.queryByRole('textbox')).toBeNull()
    expect(screen.queryByLabelText(/teléfono/i)).toBeNull()
    expect(screen.queryByLabelText(/contraseña/i)).toBeNull()
    // `docs/DESIGN.md:492`: nada del local antes de autenticar. El wordmark del producto vive en
    // la cabecera del shell, que es la única `h1` de la página.
    expect(screen.queryByText(/bodega/i)).toBeNull()
    expect(screen.queryByText(/tenant/i)).toBeNull()
  })

  it('selects a single role and continues with it', () => {
    const onSelect = vi.fn()

    render(<RoleSelectorScreen onSelect={onSelect} />)

    const employee = screen.getByTestId('role-card-employee')
    const admin = screen.getByTestId('role-card-admin')

    // Sin rol elegido no se puede continuar: la tarjeta es la decisión, no un adorno.
    expect((screen.getByTestId('role-continue') as HTMLButtonElement).disabled).toBe(true)

    fireEvent.click(admin)
    expect(admin.getAttribute('aria-pressed')).toBe('true')
    expect(employee.getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(employee)
    expect(employee.getAttribute('aria-pressed')).toBe('true')
    expect(admin.getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(screen.getByTestId('role-continue'))
    expect(onSelect).toHaveBeenCalledWith('employee')
  })

  it('moves the selection with the arrow keys and continues with Enter', () => {
    const onSelect = vi.fn()

    render(<RoleSelectorScreen onSelect={onSelect} />)

    // `docs/DESIGN.md:492`: `←`/`→` para elegir, `Enter` para continuar.
    fireEvent.keyDown(screen.getByTestId('role-selector'), { key: 'ArrowRight' })
    expect(screen.getByTestId('role-card-admin').getAttribute('aria-pressed')).toBe('true')

    fireEvent.keyDown(screen.getByTestId('role-selector'), { key: 'ArrowLeft' })
    expect(screen.getByTestId('role-card-employee').getAttribute('aria-pressed')).toBe('true')

    fireEvent.keyDown(screen.getByTestId('role-selector'), { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('employee')
  })

  it('does not continue with Enter when no role was chosen', () => {
    const onSelect = vi.fn()

    render(<RoleSelectorScreen onSelect={onSelect} />)

    fireEvent.keyDown(screen.getByTestId('role-selector'), { key: 'Enter' })

    expect(onSelect).not.toHaveBeenCalled()
  })
})
