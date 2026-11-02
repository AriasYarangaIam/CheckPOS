import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ADMIN_SESSION_KEY, readAdminSession } from '@/auth/accessSession'
import { AdminCredentialScreen } from '@/screens/AdminCredentialScreen'

/*
 * Login-2 del rol Administrador (W8-FE-1; docs/FRONTEND.md:47; docs/BACKEND.md §4.4).
 *
 * Lo que se fija: el cuerpo EXACTO de `POST /auth/admin-login`, que el token quede en
 * `sessionStorage` y en ningún otro lado, que el `401` muestre el mensaje genérico —sin
 * distinguir teléfono inexistente de contraseña incorrecta— y que ni la contraseña ni el token
 * aparezcan en el almacenamiento más allá del token mismo.
 */

afterEach(() => {
  sessionStorage.clear()
  vi.unstubAllGlobals()
})

/** Respuesta 200 del contrato: token y nombre del tenant, nada más. */
function loginSuccess(): Response {
  return new Response(
    JSON.stringify({ token: 'jwt-del-dueno', tenant: { name: 'Bodega Doña Rosa' } }),
    { status: 200, headers: { 'content-type': 'application/json' } },
  )
}

/** Respuesta 401 del contrato: sobre único, mensaje constante. */
function loginRefused(): Response {
  return new Response(
    JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'invalid credentials' } }),
    { status: 401, headers: { 'content-type': 'application/json' } },
  )
}

/** Completa el formulario con un teléfono y una contraseña. */
function fillCredential(phone = '+51988000111', password = 'clave-maestra'): void {
  fireEvent.change(screen.getByTestId('admin-phone'), { target: { value: phone } })
  fireEvent.change(screen.getByTestId('admin-password'), { target: { value: password } })
}

describe('AdminCredentialScreen', () => {
  it('asks for the phone and the master password, with the contract label', () => {
    render(<AdminCredentialScreen onAuthenticated={() => undefined} />)

    expect(screen.getByLabelText('Teléfono')).toBeTruthy()
    expect(screen.getByLabelText('Contraseña maestra')).toBeTruthy()
    expect((screen.getByTestId('admin-submit') as HTMLButtonElement).textContent).toBe('INGRESAR')
    // Sin credencial completa no hay envío.
    expect((screen.getByTestId('admin-submit') as HTMLButtonElement).disabled).toBe(true)
  })

  it('posts the contract body to the contract route and stores the session', async () => {
    const onAuthenticated = vi.fn()
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) => loginSuccess())

    render(
      <AdminCredentialScreen
        fetchImpl={fetchSpy as unknown as typeof fetch}
        onAuthenticated={onAuthenticated}
      />,
    )

    fillCredential()
    fireEvent.click(screen.getByTestId('admin-submit'))

    await waitFor(() => expect(onAuthenticated).toHaveBeenCalled())

    const [url, init] = (fetchSpy.mock.calls[0] ?? []) as [string, RequestInit | undefined]
    expect(url).toBe('/auth/admin-login')
    expect(init?.method).toBe('POST')
    // §4.4: `{ phone, master_password }`. Ni un campo de más, ni el tenant, ni el rol.
    expect(JSON.parse(String(init?.body))).toEqual({
      phone: '+51988000111',
      master_password: 'clave-maestra',
    })
    // Es la ruta PRE-AUTH: no lleva token.
    expect((init?.headers as Record<string, string>).authorization).toBeUndefined()

    expect(onAuthenticated).toHaveBeenCalledWith({
      token: 'jwt-del-dueno',
      tenantName: 'Bodega Doña Rosa',
      phone: '+51988000111',
    })
    expect(readAdminSession()).toEqual({
      token: 'jwt-del-dueno',
      tenantName: 'Bodega Doña Rosa',
      phone: '+51988000111',
    })

    // La contraseña no se guarda en ninguna parte del cliente.
    expect(sessionStorage.getItem(ADMIN_SESSION_KEY)).not.toContain('clave-maestra')
  })

  it('answers a generic message on 401 and stores nothing', async () => {
    const onAuthenticated = vi.fn()
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) => loginRefused())

    render(
      <AdminCredentialScreen
        fetchImpl={fetchSpy as unknown as typeof fetch}
        onAuthenticated={onAuthenticated}
      />,
    )

    fillCredential('+51900000000', 'otra-clave')
    fireEvent.click(screen.getByTestId('admin-submit'))

    const notice = await screen.findByTestId('admin-error')
    // Genérico y constante: no dice si el teléfono existe ni cuál de los dos datos falló.
    expect(notice.textContent).toContain('Teléfono o contraseña incorrectos')
    expect(notice.textContent).not.toMatch(/teléfono no|no existe|contraseña incorrecta\./i)
    expect(onAuthenticated).not.toHaveBeenCalled()
    expect(readAdminSession()).toBeNull()
  })

  it('says the connection failed instead of blaming the credential', async () => {
    const fetchSpy = vi.fn(async (_url: string | URL, _init?: RequestInit) => {
      throw new Error('network down')
    })

    render(<AdminCredentialScreen fetchImpl={fetchSpy as unknown as typeof fetch} onAuthenticated={() => undefined} />)

    fillCredential()
    fireEvent.click(screen.getByTestId('admin-submit'))

    const notice = await screen.findByTestId('admin-error')
    expect(notice.textContent).toContain('No se pudo verificar')
    expect(notice.textContent).not.toContain('Teléfono o contraseña incorrectos')
  })

  it('offers a back affordance to the role selector', () => {
    const onBack = vi.fn()

    render(<AdminCredentialScreen onAuthenticated={() => undefined} onBack={onBack} />)

    fireEvent.click(screen.getByRole('button', { name: 'Volver' }))

    expect(onBack).toHaveBeenCalled()
  })

  it('never renders the password in clear text', () => {
    render(<AdminCredentialScreen onAuthenticated={() => undefined} />)

    const password = screen.getByLabelText('Contraseña maestra') as HTMLInputElement

    expect(password.type).toBe('password')
  })
})
