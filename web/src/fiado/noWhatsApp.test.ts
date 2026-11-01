import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

/*
 * W7-BE-4 (S7 y S11) y `ARCHITECTURE.md:158`: en el MVP no existe NINGÚN camino
 * de recordatorio de deuda por WhatsApp — ni WABA, ni `wa.me`, ni un enlace de
 * `whatsapp://`. El recordatorio queda declarado posterior en
 * `docs/DESIGN.md:207`, y en esta versión el estado de cuenta se comparte con el
 * share sheet nativo del sistema (`navigator.share`), que no sabe de WhatsApp.
 *
 * La prueba recorre las fuentes y los assets públicos: un enlace agregado "solo
 * para probar" es exactamente lo que el criterio de aceptación prohíbe, y una
 * revisión manual no lo detectaría en el siguiente sprint.
 *
 * Lo que se inspecciona es el CÓDIGO, no la prosa: los comentarios que nombran
 * la prohibición (`// sin camino wa.me`) y las aserciones negativas de los tests
 * —que son donde la regla se fija— no son un camino de recordatorio. Por eso se
 * descartan los bloques de comentario y los archivos de prueba antes de buscar.
 */

const SOURCE_ROOTS = ['src', 'public'] as const

const FORBIDDEN = [/wa\.me/i, /whatsapp/i, /\bwaba\b/i] as const

/** Quita los comentarios de bloque y las líneas de comentario. */
function stripComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

/** ¿Es un archivo de prueba? Ahí la regla se fija, incluso con aserciones negativas. */
function isTestFile(file: string): boolean {
  return file.endsWith('.test.ts') || file.endsWith('.test.tsx')
}

/** Todos los archivos de las raíces indicadas, recursivamente. */
function filesUnder(root: string, relative = ''): string[] {
  const entries = readdirSync(join(process.cwd(), root, relative), { withFileTypes: true })
  const files: string[] = []

  for (const entry of entries) {
    const path = relative === '' ? entry.name : join(relative, entry.name)

    if (entry.isDirectory()) {
      // Esta misma prueba contiene los patrones prohibidos: se excluye a sí misma
      // y solo a sí misma.
      if (entry.name === 'node_modules') continue
      files.push(...filesUnder(root, path))
      continue
    }
    files.push(join(root, path))
  }

  return files
}

describe('sin recordatorios por WhatsApp en el MVP', () => {
  it('no hay ningún camino WABA, wa.me ni whatsapp en el código ni en los assets públicos', () => {
    const offenders: string[] = []

    for (const root of SOURCE_ROOTS) {
      for (const file of filesUnder(root)) {
        if (isTestFile(file)) continue

        const code = stripComments(readFileSync(join(process.cwd(), file), 'utf8'))
        for (const pattern of FORBIDDEN) {
          if (pattern.test(code)) offenders.push(`${file} (${pattern.source})`)
        }
      }
    }

    expect(offenders).toEqual([])
  })
})
