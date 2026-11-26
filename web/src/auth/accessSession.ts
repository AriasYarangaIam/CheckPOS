/**
 * Sesión del rol Administrador (D-C = C2 de odd/tasks/sprint-8-admin-hardening.md;
 * docs/FRONTEND.md:48, docs/DESIGN.md:441-449).
 *
 * El token del dueño vive en `sessionStorage` y no en `localStorage`: sobrevive las recargas de
 * la pestaña —el POS se recarga durante el turno— y muere al cerrarla, así que un dispositivo
 * perdido no deja la credencial escrita en el disco. El cierre de sesión es del lado del cliente
 * porque el contrato no tiene endpoint de logout (`ARCHITECTURE.md:122-135`).
 *
 * La lectura es FAIL-CLOSED: una fila ausente, un JSON roto o un objeto sin token devuelven
 * `null` en lugar de lanzar. Un shell que arranca con una sesión a medias sería peor que uno que
 * vuelve al selector de rol.
 */

/** Clave de la sesión en el almacenamiento de la pestaña. */
export const ADMIN_SESSION_KEY = 'pos-admin-session'

/** Sesión del dueño: el token y lo que la cabecera muestra sin inventar nada. */
export interface AdminSession {
  /** Access token de `POST /auth/admin-login`, con el claim `role:"admin"`. */
  readonly token: string
  /** `tenant.name` de la respuesta: alimenta el bloque de marca del shell. */
  readonly tenantName: string
  /**
   * Teléfono con el que el dueño entró. Es su identidad visible: `users.phone` es el único dato
   * de identidad del contrato (`docs/DATABASE.md` §4 `users`), y el footer del shell lo muestra.
   */
  readonly phone: string
}

/** Almacenamiento por defecto: la pestaña, nunca el disco. */
function defaultStorage(): Storage | null {
  if (typeof globalThis.sessionStorage === 'undefined') return null

  return globalThis.sessionStorage
}

/** Lee la sesión guardada, o `null` si no hay una utilizable. */
export function readAdminSession(storage: Storage | null = defaultStorage()): AdminSession | null {
  if (storage === null) return null

  let raw: string | null
  try {
    raw = storage.getItem(ADMIN_SESSION_KEY)
  } catch {
    // Un almacenamiento bloqueado (modo privado, cuota) no es una sesión.
    return null
  }
  if (raw === null) return null

  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null) return null

  const candidate = parsed as { token?: unknown; tenantName?: unknown; phone?: unknown }
  if (typeof candidate.token !== 'string' || candidate.token === '') return null
  if (typeof candidate.tenantName !== 'string') return null
  if (typeof candidate.phone !== 'string') return null

  return { token: candidate.token, tenantName: candidate.tenantName, phone: candidate.phone }
}

/** Guarda la sesión del dueño. */
export function saveAdminSession(session: AdminSession, storage: Storage | null = defaultStorage()): void {
  if (storage === null) return

  storage.setItem(ADMIN_SESSION_KEY, JSON.stringify(session))
}

/** Borra la sesión: es todo el cierre de sesión que el contrato permite. */
export function clearAdminSession(storage: Storage | null = defaultStorage()): void {
  if (storage === null) return

  storage.removeItem(ADMIN_SESSION_KEY)
}
