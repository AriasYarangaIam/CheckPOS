import { webcrypto } from 'node:crypto'

/*
 * WebCrypto en el runner. jsdom expone `crypto.getRandomValues` pero no
 * `crypto.subtle`, y el verificador local del PIN (docs/DESIGN.md:105, decisión
 * D6 de `odd/tasks/sprint-2-identity.md`) deriva con PBKDF2-SHA256, que vive en
 * `subtle`. Sin esta instalación el camino de derivación no existiría en los
 * tests y una suite verde no probaría nada.
 *
 * Se instala solo si falta: si el entorno ya trae un `subtle` —Node moderno lo
 * expone de forma global— ese se conserva, porque reemplazarlo por otra
 * implementación de la misma primitiva sería cambiar el sujeto de la prueba.
 *
 * No se simula ni se reemplaza el algoritmo: `node:crypto` es WebCrypto real, y
 * los tests que derivan lo declaran de forma explícita.
 */
const installed = globalThis.crypto as Crypto | undefined

if (installed === undefined || installed.subtle === undefined) {
  Object.defineProperty(globalThis, 'crypto', {
    value: webcrypto as unknown as Crypto,
    configurable: true,
  })
}
