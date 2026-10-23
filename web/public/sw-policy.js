/**
 * Política de caché del shell de PWA (W6-FE-2; SPRINTS.md Semana 6, decisión D8 de
 * odd/tasks/sprint-6-offline-sync.md).
 *
 * Vive en `public/` —y no en `src/`— porque la importa el service worker REAL, que el
 * navegador carga como módulo sin pasar por el bundler. Es ESM puro, sin dependencias, para
 * que la misma fuente corra en el service worker y en las pruebas.
 *
 * La regla que el sprint exige es una sola, y es una regla de NEGOCIO, no de rendimiento:
 * nada de cache-first ciego. La navegación y la API van SIEMPRE a la red primero, así un
 * despliegue nuevo llega al dispositivo y la ruta de cobro no queda fijada a una versión
 * vieja. Cache-first queda solo para los assets con hash de contenido en el nombre, que son
 * inmutables por construcción: un asset distinto tiene otra URL.
 */

/** Prefijo del nombre de caché. La versión del build lo completa. */
export const CACHE_PREFIX = 'pos-shell-'

/**
 * Nombre de la caché de esta versión. Un despliegue nuevo trae otro nombre, y el `activate`
 * borra el anterior: no queda contenido viejo conviviendo con el nuevo.
 */
export function cacheNameFor(version) {
  return `${CACHE_PREFIX}${version}`
}

/** Un asset es inmutable cuando su URL lleva el hash de contenido del build. */
export function isHashedAsset(pathname) {
  return pathname.startsWith('/assets/') && /-[A-Za-z0-9_-]{8,}\./.test(pathname)
}

/** Rutas de API del contrato que el POS consume (docs/BACKEND.md §4). */
const API_PREFIXES = ['/auth/', '/products', '/sales', '/cash-closes', '/sync/', '/gtin-lookup',
  '/photos', '/health', '/ready']

/**
 * Estrategia de una petición: `cache-first` solo para assets inmutables; `network-first`
 * para todo lo demás —navegación, API, shell— porque el shell también cambia entre
 * despliegues.
 */
export function strategyFor(pathname) {
  return isHashedAsset(pathname) ? 'cache-first' : 'network-first'
}

/** ¿La ruta es de la API? Se usa para no cachear respuestas de datos. */
export function isApiRequest(pathname) {
  return API_PREFIXES.some((prefix) => pathname === prefix || pathname.startsWith(prefix))
}

/**
 * Cachés que el `activate` tiene que borrar: las del shell que no son la de esta versión.
 * Las ajenas —de otras apps del mismo origen— no se tocan.
 */
export function staleCacheNames(currentName, allNames) {
  return allNames.filter((name) => name.startsWith(CACHE_PREFIX) && name !== currentName)
}
