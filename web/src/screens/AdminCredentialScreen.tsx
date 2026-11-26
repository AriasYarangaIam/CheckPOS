import { useState } from 'react'

import { saveAdminSession, type AdminSession } from '@/auth/accessSession'
import { Input } from '@/components/ui/Input'
import { cn } from '@/lib/utils'

/**
 * Login-2 del rol Administrador (W8-FE-1; docs/FRONTEND.md:47; docs/BACKEND.md §4.4;
 * `ARCHITECTURE.md:126`).
 *
 * **El primer token del cliente.** Hasta esta pantalla la PWA no tenía sesión en línea: el
 * empleado entra por PIN contra `devices.pin_hash` y opera offline (decisión D-D = D1 del
 * sprint 8). Acá el dueño escribe su teléfono y la contraseña MAESTRA del tenant, y el token que
 * vuelve —con el claim `role:"admin"`— habilita las pantallas de métricas y la sincronización de
 * la cartera de fiado.
 *
 * **El 401 es genérico y constante.** «Teléfono o contraseña incorrectos» no distingue si el
 * teléfono existe ni cuál de los dos datos falló: el servidor no lo distingue y la pantalla
 * tampoco puede insinuarlo (§4.4). Un fallo de red se dice como lo que es, y no se disfraza de
 * credencial inválida.
 *
 * **La contraseña no se guarda.** El token va a `sessionStorage` (decisión D-C = C2) y la
 * contraseña no sale de esta pantalla: ni en el almacenamiento, ni en la URL, ni en un log.
 */

export interface AdminCredentialScreenProps {
  /** Sesión lista: el shell del rol Administrador la recibe y la guarda ya está escrita. */
  readonly onAuthenticated: (session: AdminSession) => void
  readonly onBack?: () => void
  /** Origen de la API. Vacío es el mismo origen de la PWA. */
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Inyectable para las pruebas; en producción es el `sessionStorage` de la pestaña. */
  readonly storage?: Storage | null
}

/** El mensaje ÚNICO del 401: no enumera usuarios ni distingue causa (§4.4). */
const INVALID_CREDENTIALS_NOTICE = 'Teléfono o contraseña incorrectos. Revisa los datos e intenta de nuevo.'

/** Un fallo de red no es una credencial inválida, y decirlo no filtra nada. */
const UNREACHABLE_NOTICE = 'No se pudo verificar la credencial. Revisa tu conexión e intenta de nuevo.'

export function AdminCredentialScreen({
  onAuthenticated,
  onBack,
  baseUrl,
  fetchImpl,
  storage,
}: AdminCredentialScreenProps) {
  const [phone, setPhone] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  const canSubmit = phone.trim() !== '' && password !== '' && !submitting

  async function submit(): Promise<void> {
    if (!canSubmit) return

    setSubmitting(true)
    setNotice(null)

    const network = fetchImpl ?? globalThis.fetch

    let response: Response
    try {
      response = await network(`${baseUrl ?? ''}/auth/admin-login`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ phone: phone.trim(), master_password: password }),
      })
    } catch {
      setNotice(UNREACHABLE_NOTICE)
      setSubmitting(false)

      return
    }

    if (!response.ok) {
      // 401 genérico del contrato y cualquier otro rechazo: la misma frase para el cajero, y el
      // cuerpo del servidor no se muestra (no aporta y puede insinuar la causa).
      setNotice(response.status === 401 ? INVALID_CREDENTIALS_NOTICE : UNREACHABLE_NOTICE)
      setSubmitting(false)

      return
    }

    let body: unknown
    try {
      body = await response.json()
    } catch {
      setNotice(UNREACHABLE_NOTICE)
      setSubmitting(false)

      return
    }

    const parsed = body as { token?: unknown; tenant?: { name?: unknown } }
    if (typeof parsed.token !== 'string' || parsed.token === '' || typeof parsed.tenant?.name !== 'string') {
      setNotice(UNREACHABLE_NOTICE)
      setSubmitting(false)

      return
    }

    const session: AdminSession = {
      token: parsed.token,
      tenantName: parsed.tenant.name,
      phone: phone.trim(),
    }

    saveAdminSession(session, storage)
    // La contraseña se suelta en cuanto se usó.
    setPassword('')
    setSubmitting(false)
    onAuthenticated(session)
  }

  return (
    <section
      aria-labelledby="admin-credential-title"
      className="mx-auto flex w-full max-w-md flex-col gap-3 p-4"
      data-testid="admin-credential"
    >
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-heading-md font-bold" id="admin-credential-title">
          Acceso de administrador
        </h2>
        {onBack === undefined ? null : (
          <button
            type="button"
            onClick={onBack}
            className="min-h-9 rounded-lg px-2 text-body-md text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            Volver
          </button>
        )}
      </div>

      <div className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4">
        <label className="flex flex-col gap-1" htmlFor="admin-phone">
          <span className="text-caption-xs uppercase text-text-secondary">Teléfono</span>
          <Input
            autoComplete="tel"
            data-testid="admin-phone"
            id="admin-phone"
            inputMode="tel"
            onChange={(event) => setPhone(event.target.value)}
            type="tel"
            value={phone}
          />
        </label>

        <label className="flex flex-col gap-1" htmlFor="admin-password">
          <span className="text-caption-xs uppercase text-text-secondary">Contraseña maestra</span>
          <Input
            autoComplete="current-password"
            data-testid="admin-password"
            id="admin-password"
            onChange={(event) => setPassword(event.target.value)}
            type="password"
            value={password}
          />
        </label>

        {notice === null ? null : (
          <p className="text-body-sm text-danger-action" data-testid="admin-error" role="alert">
            {notice}
          </p>
        )}

        <button
          type="button"
          data-testid="admin-submit"
          disabled={!canSubmit}
          onClick={() => void submit()}
          className={cn(
            'min-h-[52px] rounded-xl bg-accent-hover text-heading-md font-bold text-white shadow-glow-emerald',
            'transition-transform duration-[120ms] ease-out active:scale-[0.98]',
            'disabled:pointer-events-none disabled:opacity-40',
            'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
          )}
        >
          INGRESAR
        </button>
      </div>
    </section>
  )
}
