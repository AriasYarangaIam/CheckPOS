import { describe, expect, it } from 'vitest'

import { offlineSessionVerdict, windowClosed } from '@/auth/offlineSession'

/*
 * La regla de la ventana offline es una función pura de (sesión, ahora):
 * desbloqueo permitido **en o antes** de `offline_session_valid_until` y
 * rechazado después (docs/DATABASE.md §4 `devices`: «the client refuses offline
 * unlock once passed»). Se prueba con un `now` explícito para que la pureza sea
 * verificable: si la implementación leyera el reloj del proceso, el caso 2026
 * pasaría y el de 2030 fallaría.
 */

const VALID_UNTIL = '2026-10-23T09:15:00.000Z'

function sessionAt(validUntil: string | null) {
  return validUntil === null ? null : { offline_session_valid_until: validUntil }
}

describe('offlineSessionVerdict', () => {
  it('allows the unlock well inside the window', () => {
    expect(offlineSessionVerdict(sessionAt(VALID_UNTIL), new Date('2026-10-16T09:15:00.000Z'))).toBe(
      'allowed',
    )
  })

  it('allows the unlock exactly at the mark', () => {
    // El límite es inclusivo: la marca es «hasta», no «antes de».
    expect(offlineSessionVerdict(sessionAt(VALID_UNTIL), new Date(VALID_UNTIL))).toBe('allowed')
  })

  it('refuses the unlock one millisecond past the mark', () => {
    expect(
      offlineSessionVerdict(sessionAt(VALID_UNTIL), new Date(Date.parse(VALID_UNTIL) + 1)),
    ).toBe('expired')
  })

  it('refuses a session that is not there', () => {
    expect(offlineSessionVerdict(sessionAt(null), new Date('2026-10-16T09:15:00.000Z'))).toBe(
      'unreadable',
    )
  })

  it('refuses an unparseable mark instead of assuming it is open', () => {
    // Fail-closed: una marca que no se puede leer no habilita el mostrador de
    // forma optimista; se rechaza y el intento en línea queda disponible.
    for (const broken of ['', 'mañana', '2026-13-45T00:00:00.000Z', '1730000000']) {
      expect(offlineSessionVerdict(sessionAt(broken), new Date('2026-10-16T09:15:00.000Z'))).toBe(
        'unreadable',
      )
    }
  })

  it('reads nothing but its two arguments', () => {
    // El reloj del proceso está en 2026 y la ventana vence en 2030: si la
    // función consultara `Date.now()` devolvería 'expired' en lugar de
    // 'allowed'. La mordida es la fecha pasada por parámetro.
    expect(offlineSessionVerdict(sessionAt('2030-01-01T00:00:00.000Z'), new Date('2026-10-16T09:15:00.000Z'))).toBe(
      'allowed',
    )
  })
})

describe('windowClosed', () => {
  it('is the boolean face of the same rule', () => {
    expect(windowClosed(VALID_UNTIL, new Date(VALID_UNTIL))).toBe(false)
    expect(windowClosed(VALID_UNTIL, new Date(Date.parse(VALID_UNTIL) + 1))).toBe(true)
    expect(windowClosed('no-es-fecha', new Date(VALID_UNTIL))).toBe(true)
  })
})
