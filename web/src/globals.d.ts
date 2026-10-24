/**
 * Declaraciones de los valores que Vite inyecta en el bundle
 * (`define` de `vite.config.ts`). El panel lateral muestra la versión del
 * software y la lee del `package.json` en tiempo de build: copiarla a mano en
 * un componente la dejaría desincronizada del artefacto.
 */
declare const __APP_VERSION__: string
