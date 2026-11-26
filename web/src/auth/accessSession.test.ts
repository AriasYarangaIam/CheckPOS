import { describe, expect, it } from 'vitest'

import {
  ADMIN_SESSION_KEY,
  clearAdminSession,
  readAdminSession,
  saveAdminSession,
  type AdminSession,
} from '@/auth/accessSession'

/*
 * Sesión del dueño (D-C = C2 del sprint 8): `sessionStorage`, lectura fail-closed y cierre de
 * sesión del lado del cliente. Estas pruebas usan un almacenamiento en memoria para no depender
 * del global del runner, salvo la que fija DÓNDE vive la sesión.
 */

/** Almacenamiento en memoria con la forma de `Storage`. */
function memoryStorage(): Storage {
  const items = new Map<string, string>()

  return {
    get length() {
      return items.size
    },
    clear: () => items.clear(),
    getItem: (key: string) => items.get(key) ?? null,
    key: (index: number) => [...items.keys()][index] ?? null,
    removeItem: (key: string) => void items.delete(key),
    setItem: (key: string, value: string) => void items.set(key, value),
  } as Storage
}

const SESSION: AdminSession = {
  token: 'jwt-de-administrador',
  tenantName: 'Bodega Doña Rosa',
  phone: '+51988000111',
}

describe('la sesión del administrador', () => {
  it('guarda y vuelve a leer el token, el tenant y el teléfono', () => {
    const storage = memoryStorage()

    saveAdminSession(SESSION, storage)

    expect(readAdminSession(storage)).toEqual(SESSION)
  })

  it('vive en sessionStorage y no en localStorage', () => {
    // D-C = C2: la pestaña. Si la sesión estuviera en `localStorage`, un dispositivo perdido
    // conservaría la credencial en el disco.
    sessionStorage.clear()
    localStorage.clear()

    try {
      saveAdminSession(SESSION)

      expect(sessionStorage.getItem(ADMIN_SESSION_KEY)).not.toBeNull()
      expect(localStorage.getItem(ADMIN_SESSION_KEY)).toBeNull()
      expect(readAdminSession()).toEqual(SESSION)
    } finally {
      sessionStorage.clear()
      localStorage.clear()
    }
  })

  it('no lanza y devuelve null cuando no hay sesión', () => {
    expect(readAdminSession(memoryStorage())).toBeNull()
  })

  it('devuelve null con un JSON roto, un valor que no es objeto o un token vacío', () => {
    const cases = ['{', 'null', '[]', '"texto"', '{"token":""}', '{"token":"x"}', '{"tenantName":"Bodega"}']

    for (const raw of cases) {
      const storage = memoryStorage()
      storage.setItem(ADMIN_SESSION_KEY, raw)

      expect(readAdminSession(storage), raw).toBeNull()
    }
  })

  it('no lanza cuando el almacenamiento está bloqueado', () => {
    const blocked = {
      getItem: () => {
        throw new Error('SecurityError')
      },
      setItem: () => {
        throw new Error('SecurityError')
      },
      removeItem: () => {
        throw new Error('SecurityError')
      },
    } as unknown as Storage

    expect(readAdminSession(blocked)).toBeNull()
  })

  it('el cierre de sesión borra la fila', () => {
    const storage = memoryStorage()
    saveAdminSession(SESSION, storage)

    clearAdminSession(storage)

    expect(readAdminSession(storage)).toBeNull()
  })
})
