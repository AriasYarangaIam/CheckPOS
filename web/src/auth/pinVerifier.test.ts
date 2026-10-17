import { pbkdf2Sync } from 'node:crypto'

import { describe, expect, it } from 'vitest'

import {
  PIN_LENGTH,
  PIN_SALT_BYTES,
  PIN_VERIFIER_BITS,
  PIN_VERIFIER_ITERATIONS,
  createPinSalt,
  derivePinVerifier,
  fromBase64,
  pinMatches,
  toBase64,
} from '@/auth/pinVerifier'

/*
 * El verificador local del PIN (docs/DESIGN.md:105; decisión D6 de
 * `odd/tasks/sprint-2-identity.md`). Se prueba contra una implementación
 * independiente (`node:crypto`'s `pbkdf2Sync`), no contra una copia del valor
 * que produce el módulo: un vector literal solo avisaría que alguien tocó el
 * archivo, mientras que la comparación cruzada avisa cuando el algoritmo, las
 * iteraciones, el salt o el largo de salida se separan del contrato.
 *
 * Alcance honesto: sobre un PIN de 4 dígitos la derivación **retrasa** una
 * enumeración, no la impide —10.000 candidatos es un espacio pequeño—. Los
 * límites reales son el `pin_hash` argon2id del servidor, que el cliente nunca
 * recibe, y la ventana de 7 días. Ninguna prueba de acá afirma lo contrario.
 */

const PIN = '7391'
const SALT = new Uint8Array([9, 4, 2, 7, 0, 1, 6, 3, 8, 5, 1, 2, 3, 4, 5, 6])

describe('pinVerifier', () => {
  it('has WebCrypto in this runner, so the derivation path really runs', () => {
    // El setup instala `crypto.subtle` desde `node:crypto` (ver
    // `src/test-setup.ts`). Si eso dejara de correr, estas pruebas no podrían
    // derivar y tienen que fallar en voz alta en lugar de saltarse en silencio.
    expect(globalThis.crypto).toBeDefined()
    expect(globalThis.crypto.subtle).toBeDefined()
  })

  it('derives PBKDF2-SHA256 with the documented parameters', async () => {
    const expected = pbkdf2Sync(
      PIN,
      SALT,
      PIN_VERIFIER_ITERATIONS,
      PIN_VERIFIER_BITS / 8,
      'sha256',
    ).toString('base64')

    await expect(derivePinVerifier(PIN, SALT)).resolves.toBe(expected)
  })

  it('is deterministic for one salt and different across salts', async () => {
    const first = await derivePinVerifier(PIN, SALT)
    const again = await derivePinVerifier(PIN, SALT)
    const other = await derivePinVerifier(PIN, createPinSalt())

    expect(again).toBe(first)
    expect(other).not.toBe(first)
  })

  it('generates a fresh salt of the documented size and never a constant', () => {
    const salts = new Set(Array.from({ length: 8 }, () => toBase64(createPinSalt())))

    expect(salts.size).toBe(8)

    for (const salt of salts) {
      expect(fromBase64(salt)).toHaveLength(PIN_SALT_BYTES)
    }
  })

  it('keeps the clear PIN out of both halves of the stored verifier', async () => {
    const saltBytes = createPinSalt()
    const salt = toBase64(saltBytes)
    const verifier = await derivePinVerifier(PIN, saltBytes)

    expect(salt).not.toContain(PIN)
    expect(verifier).not.toContain(PIN)
    expect(fromBase64(verifier)).toHaveLength(PIN_VERIFIER_BITS / 8)
  })

  it('round-trips a byte string through base64', () => {
    expect(fromBase64(toBase64(SALT))).toEqual(SALT)
  })

  it('accepts the PIN that produced the verifier', async () => {
    const saltBytes = createPinSalt()
    const salt = toBase64(saltBytes)
    const verifier = await derivePinVerifier(PIN, saltBytes)

    await expect(pinMatches(PIN, salt, verifier)).resolves.toBe(true)
  })

  it('rejects every other four-digit PIN of the space it can enumerate', async () => {
    const saltBytes = createPinSalt()
    const salt = toBase64(saltBytes)
    const verifier = await derivePinVerifier(PIN, saltBytes)
    const candidates = ['0000', '0001', PIN.replace('1', '2'), '4821', '9999'].filter(
      (candidate) => candidate !== PIN && candidate.length === PIN_LENGTH,
    )

    expect(candidates).toHaveLength(5)

    for (const candidate of candidates) {
      await expect(pinMatches(candidate, salt, verifier)).resolves.toBe(false)
    }
  })

  it('rejects a verifier that differs only in its last byte', async () => {
    /*
     * Mordida sobre la comparación: un `return a[0] === b[0]` —o cualquier
     * salida temprana que mire solo el primer byte— aceptaría este verifier,
     * porque el primer byte no cambió. El último byte también se compara.
     */
    const saltBytes = createPinSalt()
    const salt = toBase64(saltBytes)
    const verifier = fromBase64(await derivePinVerifier(PIN, saltBytes))

    expect(verifier).not.toBeNull()

    const tampered = Uint8Array.from(verifier ?? [])
    const last = tampered.length - 1

    tampered[last] = (tampered[last] ?? 0) ^ 0x01

    await expect(pinMatches(PIN, salt, toBase64(tampered))).resolves.toBe(false)
  })

  it('refuses garbage without throwing', async () => {
    const saltBytes = createPinSalt()
    const salt = toBase64(saltBytes)
    const verifier = await derivePinVerifier(PIN, saltBytes)

    const garbage: Array<[string, string, string]> = [
      ['', salt, verifier],
      [PIN, '', verifier],
      [PIN, 'no-es-base64!!', verifier],
      [PIN, salt, 'no-es-base64!!'],
      [PIN, salt, ''],
      [PIN, toBase64(new Uint8Array(4)), verifier],
      [PIN, salt, toBase64(new Uint8Array(4))],
      ['7391extra', salt, verifier],
      [PIN, salt, `${verifier}==`],
    ]

    for (const [pin, candidateSalt, candidateVerifier] of garbage) {
      await expect(
        pinMatches(pin, candidateSalt, candidateVerifier),
        `pin=${pin} salt=${candidateSalt} verifier=${candidateVerifier}`,
      ).resolves.toBe(false)
    }
  })

  it('refuses a caller that passes no string at all', async () => {
    const saltBytes = createPinSalt()
    const salt = toBase64(saltBytes)
    const verifier = await derivePinVerifier(PIN, saltBytes)

    await expect(pinMatches(undefined as unknown as string, salt, verifier)).resolves.toBe(false)
    await expect(pinMatches(PIN, null as unknown as string, verifier)).resolves.toBe(false)
    await expect(pinMatches(PIN, salt, undefined as unknown as string)).resolves.toBe(false)
  })
})
