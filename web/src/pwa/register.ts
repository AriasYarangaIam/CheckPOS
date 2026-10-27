/**
 * Registro del service worker del shell de PWA (W6-FE-2; ARCHITECTURE.md:91).
 *
 * La versión del build viaja en la URL de registro: un despliegue nuevo registra
 * `/sw.js?v=<otra versión>`, el navegador instala un worker nuevo y su `activate` borra la
 * caché anterior. Es el mecanismo que hace que la ruta de cobro no quede fijada a una
 * versión vieja (decisión D8).
 *
 * Reglas:
 *
 *   - no se registra nada sin soporte del navegador ni en el servidor de desarrollo: en dev
 *     el service worker estorbaría al HMR;
 *   - el registro es best-effort: si falla, la app sigue funcionando —sin shell offline,
 *     pero vendiendo—. Un error de registro jamás llega al cajero.
 */

/** Versión del service worker registrada: la del build, o `dev` fuera de producción. */
export function serviceWorkerUrl(version: string): string {
  return `/sw.js?v=${encodeURIComponent(version)}`
}

/**
 * Registra el service worker si el entorno lo permite. Un fallo de registro no se propaga:
 * sin shell offline la app sigue vendiendo.
 */
export function registerServiceWorker(): void {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  if (!import.meta.env.PROD) return

  const version = typeof __APP_VERSION__ === 'string' ? __APP_VERSION__ : 'dev'

  void navigator.serviceWorker.register(serviceWorkerUrl(version), { type: 'module' }).catch(() => {
    // Sin shell offline la app sigue vendiendo: el fallo no se muestra ni se reintenta acá.
  })
}
