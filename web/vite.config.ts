import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// Fuente única de verdad del andamiaje: `vite dev`, `vite build` y `vitest`
// leen este mismo archivo, por lo que el alias `@/` y los plugins resuelven
// igual en los tres caminos (docs/FRONTEND.md:81-84).
//
// La versión del `package.json` entra al bundle como constante de build: el
// navigation sheet la muestra en su cabecera (docs/DESIGN.md §3.1:184) y una
// copia escrita a mano en un componente se desincronizaría del artefacto.
const packageVersion = (
  JSON.parse(readFileSync(fileURLToPath(new URL('./package.json', import.meta.url)), 'utf8')) as {
    version: string
  }
).version

export default defineConfig({
  plugins: [react(), tailwindcss()],

  define: {
    __APP_VERSION__: JSON.stringify(packageVersion),
  },

  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },

  test: {
    // El shell es una PWA en el navegador: los tests necesitan DOM real.
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    // jsdom no trae `crypto.subtle` y el verificador local del PIN deriva con
    // PBKDF2 (ver `src/test-setup.ts`).
    setupFiles: ['./src/test-setup.ts'],
  },
})
