import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

// Las fuentes viajan empaquetadas con la PWA (los woff2 los resuelve Vite desde
// node_modules): la app no pide tipografía por red y arranca igual sin
// conexión. Los `@font-face` traen `unicode-range`, así que el navegador solo
// descarga el subconjunto que el texto realmente usa.
import '@fontsource-variable/inter'
import '@fontsource-variable/jetbrains-mono'
import '@/index.css'

import { App } from '@/App'
import { registerServiceWorker } from '@/pwa/register'

const container = document.getElementById('root')

if (container === null) {
  throw new Error('Falta el contenedor #root en index.html')
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

// El shell de PWA se registra después de montar la app: el service worker nunca
// bloquea el primer render (W6-FE-2).
registerServiceWorker()
