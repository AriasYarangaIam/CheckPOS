import { getPosDatabase, type PosDatabase, type ProductRow } from '@/db/posDatabase'

/**
 * Cache local del subconjunto del catálogo del tenant (SPRINTS.md W3-FE-3;
 * docs/FRONTEND.md:96-100): `GET /products` es la única fuente y su respuesta
 * escribe la tabla `products` de la base local — la proyección que la grilla
 * de favoritos consume.
 *
 * Lo que el dispositivo NUNCA guarda: el catálogo global (`global_products`
 * vive solo en el servidor; ARCHITECTURE.md:20, :89, :127). La tabla no existe
 * en el esquema local y este módulo tampoco construiría una: el único HTTP que
 * sale de acá es el listado del tenant, con el scope en el token y de ningún
 * otro lado.
 *
 * **Origen del token.** El camino de UI que consigue/emite el token no existe
 * todavía — el selector de rol y la credencial del Empleado son de la semana 8
 * (el mismo retraso que `bindDeviceSession` Sigue a la espera)—. El sync es
 * explícito: quien lo llame trae el token de la sesión en línea. Offline no se
 * llama: la grilla renderiza con el subconjunto ya cacheado y el usuario nunca
 * ve el catálogo global como fallback.
 */

/**
 * Fila del contrato §4.5 en su forma de protocolo: los campos llegan tal como
 * `productBody` los serializa. `stock` es el texto NUMERIC verbatim y
 * `attributes` el JSONB elástico; las marcas del contrato (`created_at`,
 * `received_at`, `deleted_at`) no se persisten acá — la fila local es para
 * mostrar, no para arbitrar LWW, que es tarea del servidor.
 */
export interface ApiProductRow {
  id?: unknown
  name?: unknown
  gtin?: unknown
  price_cents?: unknown
  uom?: unknown
  stock?: unknown
  photo_url?: unknown
  attributes?: unknown
  /* Las marcas del contrato también viajan; el cache no las persiste. */
  created_at?: unknown
  updated_at?: unknown
}

export interface SyncCatalogInput {
  /** Access token del empleado; el tenant sale de sus claims (§5.1). */
  readonly token: string
  /** Origen de la API. Vacío es el mismo origen de la PWA. */
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

/** El stock del contrato es NUMERIC verbatim, entero o con decimal. */
const NUMERIC_STOCK = /^\d+(\.\d+)?$/

/**
 * La `category` local es la proyección del valor elástico del contrato (§2.9):
 * los atributos de la fila son el único hogar que el JSONB da a una propiedad
 * comercial, y la columna es lo que los chips de filtro leen. Cualquier otro
 * valor de esos atributos se ignora: el cache no describe el JSONB entero.
 */
function localCategoryFromAttributes(attributes: unknown): string | null {
  if (typeof attributes !== 'object' || attributes === null) return null

  const raw = (attributes as Record<string, unknown>).category

  return typeof raw === 'string' && raw.trim() !== '' ? raw : null
}

function toLocalProduct(row: unknown): ProductRow | null {
  if (typeof row !== 'object' || row === null) return null

  const candidate = row as ApiProductRow
  const id = typeof candidate.id === 'string' ? candidate.id : ''
  const name = typeof candidate.name === 'string' ? candidate.name : ''
  const uom = typeof candidate.uom === 'string' ? candidate.uom.trim() : ''
  const priceCents = candidate.price_cents

  if (id === '' || name === '' || uom === '') return null
  if (typeof priceCents !== 'number' || !Number.isInteger(priceCents) || priceCents < 0) {
    return null
  }

  const gtin = typeof candidate.gtin === 'string' && candidate.gtin !== '' ? candidate.gtin : null
  const photoURL =
    typeof candidate.photo_url === 'string' && candidate.photo_url !== ''
      ? candidate.photo_url
      : null
  // El stock del contrato es NUMERIC verbatim: el número local es la
  // instantánea para mostrar y su convergencia autoritativa es el log de
  // deltas, no esta columna. Un valor que no se lee como NUMERIC no es stock:
  // se proyecta a cero en lugar de romper el subset entero.
  const stock =
    typeof candidate.stock === 'string' && NUMERIC_STOCK.test(candidate.stock)
      ? Number(candidate.stock)
      : 0

  const local: ProductRow = {
    id,
    gtin,
    name,
    price_cents: priceCents,
    uom,
    stock,
    photo_url: photoURL,
    category: localCategoryFromAttributes(candidate.attributes),
  }

  return local
}

export async function syncCatalogFromApi(input: SyncCatalogInput): Promise<ProductRow[]> {
  const fetchImpl = input.fetchImpl ?? globalThis.fetch

  if (typeof fetchImpl !== 'function') {
    throw new Error('No hay `fetch` para pedir el catálogo del tenant')
  }

  const response = await fetchImpl(`${input.baseUrl ?? ''}/products`, {
    headers: { authorization: `Bearer ${input.token}` },
  })

  if (!response.ok) {
    throw new Error(`GET /products respondió ${response.status}`)
  }

  const body = (await response.json()) as { data?: unknown }

  if (!Array.isArray(body?.data)) {
    throw new Error('GET /products respondió sin el arreglo `data` del contrato')
  }

  const projection = body.data
    .map(toLocalProduct)
    .filter((row): row is ProductRow => row !== null)

  // `bulkPut` por id: la fila del cache se reemplaza con la verdad del
  // servidor — el arbitraje LWW del catálogo es del servidor, que es la
  // fuente. Acá no se borra lo que el listado no trajo: el endpoint admite
  // `limit`, así que una página acotada no es la prueba de que una fila
  // dejó de existir; la baja real viaja por el pull del cursor (§7.3) y
  // hasta que ese camino sea del dispositivo, el cache no destruye.
  const database = input.database ?? getPosDatabase()

  await database.products.bulkPut(projection)

  return projection
}
