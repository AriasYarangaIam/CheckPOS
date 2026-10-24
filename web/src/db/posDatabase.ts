import Dexie, { type Table } from 'dexie'

import type { TenderMethod } from '@/sales/tenderMethods'

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

/**
 * Línea del carrito en curso. Los campos del comercio viajan congelados al
 * agregar la línea («name», «uom», precio): es exactamente la forma §4.9 que
 * `POST /sales` pide congelada, así que el cierre la serializa tal cual
 * (docs/DATABASE.md §4.9 `sale_lines`).
 */
export interface CartRow {
  readonly id: string
  readonly product_id: string
  /**
   * Cantidad en el TEXTO exacto del contrato (§2.4): «1», «0.500», «2.750».
   * El importe de línea lo redondea el cierre half-up sobre esta mantisa
   * (`@/lib/decimal`).
   */
  readonly qty: string
  /** Precio congelado al agregar la línea, en centavos enteros. */
  readonly unit_price_cents: number
  /** Nombre del producto congelado al agregar la línea. */
  readonly name: string
  /** Unidad de medida congelada al agregar la línea (`unit`, `kg`, …). */
  readonly uom: string
  /**
   * Línea anulada en mostrador: la fila permanece visible (tachada) y sale de
   * los importes y del cierre. La venta nunca viaja con una línea nula: el
   * cierre solo toma las líneas que no están anuladas.
   */
  readonly voided: boolean
}

/**
 * Estado local de un evento encolado. `in_flight` y `completed` son los del
 * ciclo de reclamación del servidor (`idempotency_keys_state_chk`,
 * DATABASE.md §4.7:555); `pending` y `failed` son el eje de reintento, que solo
 * existe del lado del dispositivo.
 */
export type QueueStatus = 'pending' | 'in_flight' | 'completed' | 'failed'

/**
 * Qué evento guarda la fila. La cola nació en el Sprint 1 sin consumidor; el
 * Sprint 5 la usa para la venta cerrada y el cierre de caja, y el contrato ya
 * tiene el camino de la anulación (`POST /sales/{id}/void`, sin superficie de
 * UI todavía).
 */
export type QueueOperation = 'sale' | 'void' | 'cash_close'

/**
 * Evento pendiente de sincronizar, con la clave de idempotencia que lo cubre.
 *
 * Las columnas de proyección (`business_date`, `payment_method`,
 * `total_cents`) son copias CONGELADAS del cuerpo serializado, no un segundo
 * cálculo: el arqueo agrupa por método y fecha de negocio lo que el evento ya
 * cerró, sin volver a mirar las líneas (`docs/DATABASE.md` §2.3; SPRINTS.md
 * W5-FE-2). `last_error` conserva el código del servidor que dejó la fila en
 * `failed`; es informativo y nunca reemplaza al estado.
 */
export interface IdempotencyQueueRow {
  readonly id: string
  /** Clave `(tenant_id, key)` de la operación; la unicidad la fija el servidor. */
  readonly key: string
  /** Cuerpo serializado del POST, tal como se enviará. */
  readonly payload: string
  readonly status: QueueStatus
  /** Marca de tiempo ISO 8601 del encolado. */
  readonly created_at: string
  readonly operation: QueueOperation
  /** Fecha de negocio LOCAL congelada por el evento (`YYYY-MM-DD`). */
  readonly business_date: string
  /** Venta del evento: la del cierre o la que la anulación compensa. */
  readonly sale_id: string | null
  /** Método de pago congelado; el arqueo agrupa por este campo. */
  readonly payment_method: TenderMethod | null
  /** Total congelado en centavos enteros; nunca se recomputa de las líneas. */
  readonly total_cents: number
  /** Código del último error del servidor, si la fila quedó en `failed`. */
  readonly last_error: string | null
}

/**
 * Sesión offline de este dispositivo, con el verificador local del PIN
 * (docs/DATABASE.md §4 `devices`; docs/DESIGN.md:105; decisión D6 de
 * `odd/tasks/sprint-2-identity.md`).
 *
 * Guarda la sal aleatoria y el verificador derivado, nunca el PIN en claro ni el
 * `pin_hash` del servidor: ese valor argon2id no sale de la base y el cliente no
 * lo conoce. La marca `offline_session_valid_until` es la del contrato, sin
 * reinterpretar: el cliente la compara tal cual contra el reloj.
 */
export interface DeviceSessionRow {
  /**
   * Clave de la fila única. El alcance congelado es 1 dispositivo y 1 cajero
   * (docs/DESIGN.md:106), así que la sesión del dispositivo es una sola fila con
   * una clave constante: un segundo dispositivo sería otro alcance, no otra fila.
   */
  readonly id: string
  /** `devices.id` de la respuesta del bind. */
  readonly device_id: string
  /** `users.id` del cajero, tal como lo devuelve `POST /auth/login`. */
  readonly user_id: string
  /** Marca RFC3339 del contrato: hasta cuándo vale el desbloqueo offline. */
  readonly offline_session_valid_until: string
  /** Sal aleatoria por dispositivo, en base64. */
  readonly pin_salt: string
  /** PBKDF2-SHA256 del PIN sobre esa sal, en base64. */
  readonly pin_verifier: string
}

/**
 * Cierre de caja local de UNA fecha de negocio (W5-FE-2; docs/DESIGN.md
 * §194-198; decisión F37 del sprint). Es lo que vuelve el arqueo de una fecha
 * ya cerrada de **solo lectura**: reabrir la pantalla muestra el cierre
 * guardado, no un formulario editable.
 *
 * La clave primaria es `closed_on` y no `id`: el contrato tiene UN cierre por
 * fecha de negocio y tenant (`UNIQUE (tenant_id, closed_on)`), así que la misma
 * regla se impone acá sin una segunda consulta. `counts` guarda el desglose
 * serializado de los seis métodos —la proyección local primero y el congelado
 * del servidor cuando el acuse llega—; `acknowledged` es la única diferencia
 * entre un cierre que el servidor confirmó y uno que solo existe en el
 * dispositivo.
 */
export interface CashCloseRow {
  /** `cash_closes.id` del servidor, o el id que el cliente propuso al cerrar. */
  readonly id: string
  /** Fecha de negocio LOCAL (`YYYY-MM-DD`); es también la clave primaria. */
  readonly closed_on: string
  readonly created_at: string
  /** `CashCloseCountLine[]` serializado, en el orden fijo de los seis métodos. */
  readonly counts: string
  readonly acknowledged: boolean
}

/** Nombre por defecto de la base. */
export const POS_DATABASE_NAME = 'pos-saas'

export class PosDatabase extends Dexie {
  // Sin inicializador: Dexie resuelve cada `Table` a partir de `stores()`.
  products!: Table<ProductRow, string>
  cart!: Table<CartRow, string>
  idempotency_queue!: Table<IdempotencyQueueRow, string>
  device_session!: Table<DeviceSessionRow, string>
  cash_closes!: Table<CashCloseRow, string>

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

    /*
     * Versión 2 (W2-FE-1): una sola tienda nueva, la sesión offline del
     * dispositivo.
     *
     * Las tres anteriores se repiten **idénticas** aunque Dexie herede lo que no
     * se repite —omitir una tienda acá no la borra, se verificó con una sonda
     * sobre el esquema—: repetirlas deja el esquema completo legible en cada
     * versión, así que el diff entre dos versiones se lee de un vistazo. Lo que
     * sí destruye datos es cambiar la clave primaria de una tienda existente:
     * Dexie aborta el ascenso con `UpgradeError`, y eso es lo que la prueba de
     * la unidad muerde.
     *
     * Sin índices: la tienda tiene una fila y se lee por su clave primaria.
     * Declarar índices sobre columnas que nunca se consultan solo encarecería
     * cada escritura.
     */
    this.version(2).stores({
      products: 'id, gtin, name, category',
      cart: 'id, product_id',
      idempotency_queue: 'id, key, status, created_at',
      device_session: 'id',
    })

    /*
     * Versión 3 (W5-FE-2): una sola tienda nueva, el cierre de caja local.
     *
     * La clave primaria es `closed_on` porque el contrato tiene UN cierre por
     * fecha de negocio y tenant (`docs/DATABASE.md` §4 `cash_closes`, D10): la
     * misma regla queda impuesta por el esquema local. `id` se declara como
     * índice solo para poder rastrear un cierre acusado desde su fila.
     *
     * Las cuatro tiendas anteriores se repiten **idénticas**, con la misma
     * convención de las versiones 1 y 2: el esquema completo se lee en cada
     * versión aunque Dexie herede lo que no se repite.
     */
    this.version(3).stores({
      products: 'id, gtin, name, category',
      cart: 'id, product_id',
      idempotency_queue: 'id, key, status, created_at',
      device_session: 'id',
      cash_closes: 'closed_on, id',
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
