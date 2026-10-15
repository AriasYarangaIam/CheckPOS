import Dexie, { type Table } from 'dexie'

/**
 * Esquema de la base local (docs/FRONTEND.md TASK 2.1). Guarda tres cosas y
 * solo tres: la proyección local del catálogo del tenant, el carrito en curso y
 * la cola de eventos pendientes de sincronizar.
 *
 * Lo que NO guarda: el catálogo global. `global_products` vive en el servidor y
 * el POS solo lo ve a través de la adopción (docs/DATABASE.md §4.6, F13:277):
 * el servidor copia los datos a la fila del tenant en el momento de adoptarla y
 * desde ahí la fila local es autónoma. Espejar el catálogo global en el
 * dispositivo sería una copia sin dueño — el servidor es la fuente de verdad y
 * nunca manda un volcado global (ARCHITECTURE.md:20).
 */

/** Fila local del catálogo del tenant, alineada con `products` (DATABASE.md §4.6). */
export interface ProductRow {
  /** UUID generado en el cliente; `products.id` no es un serial local. */
  readonly id: string
  /** Código de barras; `NULL` cuando la adopción global no trajo GTIN. */
  readonly gtin: string | null
  readonly name: string
  /** Precio de venta en centavos enteros (DATABASE.md §2.3). */
  readonly price_cents: number
  /** Unidad de medida: `unit`, `kg`, etc. */
  readonly uom: string
  /** Proyección sumada de los ajustes firmados; la edición de precio no la toca. */
  readonly stock: number
  /** URL en object storage; las fotos no son entidades de base de datos. */
  readonly photo_url: string | null
  readonly category: string | null
}

/** Línea del carrito en curso. */
export interface CartRow {
  readonly id: string
  readonly product_id: string
  readonly qty: number
  /** Precio congelado al agregar la línea, en centavos enteros. */
  readonly unit_price_cents: number
}

/**
 * Estado local de un evento encolado. `in_flight` y `completed` son los del
 * ciclo de reclamación del servidor (`idempotency_keys_state_chk`,
 * DATABASE.md §4.7:555); `pending` y `failed` son el eje de reintento, que solo
 * existe del lado del dispositivo.
 */
export type QueueStatus = 'pending' | 'in_flight' | 'completed' | 'failed'

/** Evento pendiente de sincronizar, con la clave de idempotencia que lo cubre. */
export interface IdempotencyQueueRow {
  readonly id: string
  /** Clave `(tenant_id, key)` de la operación; la unicidad la fija el servidor. */
  readonly key: string
  /** Cuerpo serializado del POST, tal como se enviará. */
  readonly payload: string
  readonly status: QueueStatus
  /** Marca de tiempo ISO 8601 del encolado. */
  readonly created_at: string
}

/** Nombre por defecto de la base. */
export const POS_DATABASE_NAME = 'pos-saas'

export class PosDatabase extends Dexie {
  // Sin inicializador: Dexie resuelve cada `Table` a partir de `stores()`.
  products!: Table<ProductRow, string>
  cart!: Table<CartRow, string>
  idempotency_queue!: Table<IdempotencyQueueRow, string>

  constructor(name: string = POS_DATABASE_NAME) {
    super(name)

    /*
     * Los índices son los que el plan nombra (docs/FRONTEND.md:96-100), sin
     * `price_cents` ni `stock`: esas columnas se filtran en memoria y agregar
     * índices solo encarece cada escritura. La unicidad de `key` tampoco se
     * declara acá — el servidor la impone sobre `(tenant_id, key)` y marcarla
     * localmente bloquearía un reencolado legítimo del mismo evento.
     */
    this.version(1).stores({
      products: 'id, gtin, name, category',
      cart: 'id, product_id',
      idempotency_queue: 'id, key, status, created_at',
    })
  }
}

let instance: PosDatabase | undefined

/**
 * Base local de la aplicación. Es perezosa a propósito: la conexión no se abre
 * al importar el módulo, así el arranque de la PWA no depende de IndexedDB y
 * los tests pueden montar instancias propias sobre `fake-indexeddb` sin tocar
 * la de producción.
 */
export function getPosDatabase(): PosDatabase {
  instance ??= new PosDatabase()

  return instance
}
