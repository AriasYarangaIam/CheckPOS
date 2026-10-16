/**
 * Verificador local del PIN (docs/DESIGN.md:105; decisión D6 de
 * `odd/tasks/sprint-2-identity.md`).
 *
 * El cliente **nunca** recibe `devices.pin_hash`, que es argon2id y vive solo en
 * el servidor (docs/DATABASE.md §4 `devices`). Para que el mostrador se
 * desbloquee sin red dentro de la ventana offline, el dispositivo deriva su
 * propio verificador en el momento del bind —cuando el cajero tipea el PIN— con
 * PBKDF2-SHA256 sobre una sal aleatoria por dispositivo, y persiste la sal y el
 * verificador. El PIN en claro no se guarda en ningún lado.
 *
 * **Alcance honesto de esta primitiva.** Un PIN de 4 dígitos tiene 10.000
 * candidatos: la derivación encarece cada intento, pero quien tenga el
 * verificador almacenado puede enumerar el espacio completo fuera de línea. La
 * derivación **retrasa** ese ataque, no lo impide. Los límites reales son el
 * `pin_hash` argon2id del servidor —que este módulo no ve— y la ventana de al
 * menos 7 días, que acota por cuánto tiempo el desbloqueo offline existe. Este
 * comentario no reclama más que eso, y ninguna prueba de la unidad lo hace
 * tampoco.
 */

/** Largo del PIN del contrato: exactamente cuatro dígitos (docs/DESIGN.md:105). */
export const PIN_LENGTH = 4

/** Bytes de sal aleatoria por dispositivo. */
export const PIN_SALT_BYTES = 16

/** Iteraciones de PBKDF2. Sobre 10.000 candidatos, esto es una demora, no una barrera. */
export const PIN_VERIFIER_ITERATIONS = 100_000

/** Bits del verificador derivado. */
export const PIN_VERIFIER_BITS = 256

const SALT_BASE64 = /^[A-Za-z0-9+/]+={0,2}$/

/** Sal nueva desde el generador criptográfico del navegador. */
export function createPinSalt(): Uint8Array {
  const salt = new Uint8Array(PIN_SALT_BYTES)

  globalThis.crypto.getRandomValues(salt)

  return salt
}

/** Base64 estándar de un bloque de bytes. */
export function toBase64(bytes: Uint8Array): string {
  let binary = ''

  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }

  return btoa(binary)
}

/**
 * Bytes de un base64, o `null` si el texto no es base64 utilizable. Devuelve
 * `null` en lugar de lanzar porque su único consumidor, `pinMatches`, tiene que
 * rechazar basura sin romper: un valor corrupto en IndexedDB no puede colgar el
 * arranque del mostrador.
 */
export function fromBase64(text: string): Uint8Array | null {
  if (typeof text !== 'string' || text.length === 0 || text.length % 4 !== 0) return null
  if (!SALT_BASE64.test(text)) return null

  try {
    const binary = atob(text)
    const bytes = new Uint8Array(binary.length)

    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index)
    }

    return bytes
  } catch {
    return null
  }
}

/**
 * Verificador derivado del PIN: PBKDF2-SHA256 con la sal del dispositivo, en
 * base64. Es determinista para el par (PIN, sal), así que la misma sal y el
 * mismo PIN reproducen el mismo valor.
 */
export async function derivePinVerifier(pin: string, salt: Uint8Array): Promise<string> {
  const material = await globalThis.crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(pin),
    'PBKDF2',
    false,
    ['deriveBits'],
  )
  const bits = await globalThis.crypto.subtle.deriveBits(
    { name: 'PBKDF2', salt: salt as unknown as BufferSource, iterations: PIN_VERIFIER_ITERATIONS, hash: 'SHA-256' },
    material,
    PIN_VERIFIER_BITS,
  )

  return toBase64(new Uint8Array(bits))
}

/**
 * Comparación sin salida temprana: recorre los bytes completos de los dos
 * bloques y acumula la diferencia. La longitud es pública —el verificador mide
 * siempre 32 bytes—, así que cortar por longitud no filtra nada; lo que no se
 * corta es el contenido, para que el tiempo de la comparación no dependa de
 * cuántos bytes coincidieron.
 */
function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) return false

  let difference = 0

  for (let index = 0; index < left.length; index += 1) {
    difference |= (left[index] ?? 0) ^ (right[index] ?? 0)
  }

  return difference === 0
}

/**
 * ¿El PIN tipeado corresponde a la sal y al verificador almacenados? Nunca
 * lanza: cualquier entrada que no se pueda derivar —PIN con otra forma, sal
 * ilegible, verificador de largo equivocado, valores ausentes— es un `false`, y
 * el desbloqueo offline se rechaza.
 */
export async function pinMatches(
  pin: string,
  salt: string,
  verifier: string,
): Promise<boolean> {
  if (typeof pin !== 'string' || typeof salt !== 'string' || typeof verifier !== 'string') {
    return false
  }
  if (pin.length !== PIN_LENGTH) return false

  const saltBytes = fromBase64(salt)
  const expected = fromBase64(verifier)

  if (saltBytes === null || saltBytes.length !== PIN_SALT_BYTES) return false
  if (expected === null || expected.length !== PIN_VERIFIER_BITS / 8) return false

  try {
    const candidate = fromBase64(await derivePinVerifier(pin, saltBytes))

    // Sin `crypto.subtle` no hay forma de derivar: el `catch` de arriba
    // convierte esa ausencia en un rechazo, que es el lado seguro.
    return candidate !== null && equalBytes(candidate, expected)
  } catch {
    return false
  }
}
