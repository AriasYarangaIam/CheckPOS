import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// Fuente única de verdad del andamiaje: `vite dev`, `vite build` y `vitest`
// leen este mismo archivo, por lo que el alias `@/` y los plugins resuelven
// igual en los tres caminos (docs/FRONTEND.md:81-84).
export default defineConfig({
  plugins: [react(), tailwindcss()],

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
