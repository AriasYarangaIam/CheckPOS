/**
 * La regla de la ventana offline del dispositivo (docs/DATABASE.md §4
 * `devices`): `offline_session_valid_until` se fija en el bind a la hora del
 * bind más al menos 7 días y se refresca en cada autenticación en línea; el
 * cliente, del lado suyo, **rechaza el desbloqueo offline una vez pasada esa
 * marca**.
 *
 * Es una función pura de (sesión, ahora): no lee el reloj del proceso, no toca
 * el almacén y no tiene efectos. El instante entra por parámetro para que la
 * regla sea verificable en el límite exacto y para que ninguna prueba dependa de
 * cuándo corre.
 */

/** La parte de la sesión que la regla necesita: nada más que su marca. */
export interface OfflineWindowSession {
  readonly offline_session_valid_until: string
}

/**
 * - `allowed`: el desbloqueo offline está permitido (en la marca o antes).
 * - `expired`: la marca pasó; el desbloqueo offline se rechaza.
 * - `unreadable`: no hay sesión o la marca no se puede leer. Se rechaza igual:
 *   una marca ilegible no habilita el mostrador de forma optimista.
 */
export type OfflineWindowVerdict = 'allowed' | 'expired' | 'unreadable'

/**
 * Veredicto de la ventana. El límite es **inclusivo**: `now` igual a la marca
 * todavía se permite —la marca es «válido hasta», no «válido antes de»— y un
 * milisegundo después se rechaza.
 */
export function offlineSessionVerdict(
  session: OfflineWindowSession | null | undefined,
  now: Date,
): OfflineWindowVerdict {
  const validUntil = session?.offline_session_valid_until

  if (typeof validUntil !== 'string') return 'unreadable'

  const mark = Date.parse(validUntil)
  const instant = now.getTime()

  if (Number.isNaN(mark) || Number.isNaN(instant)) return 'unreadable'

  return instant <= mark ? 'allowed' : 'expired'
}

/** La cara booleana de la misma regla: la ventana está cerrada. */
export function windowClosed(validUntil: string, now: Date): boolean {
  return offlineSessionVerdict({ offline_session_valid_until: validUntil }, now) !== 'allowed'
}
