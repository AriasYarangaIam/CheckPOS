/**
 * Cuota del almacenamiento local (W6-FE-1; SPRINTS.md Semana 6, docs/BACKEND.md §8.3,
 * docs/DESIGN.md:254-255).
 *
 * El dispositivo guarda un SUBCONJUNTO acotado del catálogo más la cola de eventos
 * pendientes. Cuando la cuota de IndexedDB se agota, la venta no puede guardarse: el cobro
 * se BLOQUEA con su motivo visible y el carrito queda intacto. Lo que nunca pasa es una
 * pantalla en blanco ni una venta cerrada sin evento —una venta cobrada sin fila local se
 * perdería al matar la pestaña—.
 */

/**
 * Techo del catálogo local: el catálogo objetivo de una bodega es de 50-200 productos
 * (SPRINTS.md Semana 3), así que 2000 filas es un techo de seguridad que ningún tenant real
 * alcanza y que acota lo que el dispositivo puede llegar a guardar.
 */
export const MAX_LOCAL_PRODUCTS = 2000

/**
 * Motivo visible del bloqueo por cuota. Es el texto que ve el cajero: dice QUÉ pasó, que el
 * cobro sigue abierto y qué hacer, sin lenguaje técnico.
 */
export const STORAGE_BLOCKED_REASON =
  'No hay espacio en el dispositivo para guardar la venta. El cobro sigue abierto: libera espacio y vuelve a intentar.'

/**
 * Venta bloqueada por falta de espacio. Es un error de OPERACIÓN, no un fallo de negocio:
 * el carrito no se toca y la venta no se cierra en pantalla.
 */
export class StorageQuotaBlockedError extends Error {
  /** El motivo visible, listo para mostrarse sin traducción adicional. */
  readonly reason: string

  constructor(reason: string = STORAGE_BLOCKED_REASON) {
    super(reason)
    this.name = 'StorageQuotaBlockedError'
    this.reason = reason
  }
}

/**
 * Reconoce el agotamiento de cuota. IndexedDB no tiene un error propio en todos los
 * navegadores: algunos lanzan `DOMException` con `name = 'QuotaExceededError'`, otros un
 * `Error` con ese `name`, y Dexie envuelve el error original en `.inner`. Se mira el nombre
 * en los tres lugares en vez de la clase, porque la clase no es la misma en jsdom.
 */
export function isStorageQuotaError(error: unknown): boolean {
  let candidate: unknown = error

  for (let depth = 0; depth < 4 && candidate !== null && typeof candidate === 'object'; depth += 1) {
    const named = candidate as { name?: unknown; inner?: unknown }
    if (named.name === 'QuotaExceededError') return true
    candidate = named.inner
  }

  return false
}

/**
 * Acota una proyección de catálogo al techo local. Es una función pura para que el límite
 * sea verificable sin base de datos; el recorte de lo ya guardado lo hace el cache.
 */
export function boundedCatalogRows<T>(rows: readonly T[], limit: number = MAX_LOCAL_PRODUCTS): readonly T[] {
  if (limit <= 0) return []
  return rows.length <= limit ? rows : rows.slice(0, limit)
}
