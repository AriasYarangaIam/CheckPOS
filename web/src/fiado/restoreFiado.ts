import { getPosDatabase, type FiadoAccountRow, type FiadoMovementRow, type PosDatabase } from '@/db/posDatabase'

/**
 * Restauración de la cartera de fiado (W7-FE-3; docs/BACKEND.md §4.12; decisión 10
 * del sprint 7).
 *
 * La deuda sobrevive a un teléfono robado porque el servidor es la fuente de
 * verdad: esta lectura trae las cuentas con su historial firmado COMPLETO y
 * reemplaza las dos tiendas locales en una sola transacción. No toca la cola de
 * eventos, el carrito, el catálogo ni los cierres de caja —lo que el dispositivo
 * todavía no confirmó no se pierde por restaurar la vista— y nunca lanza un error
 * de sincronización al cajero: sin sesión en línea se saltea, y una red caída
 * devuelve «sin confirmar» (§7.9, decisión S12 del sprint 6).
 */

/** El pull no es una operación de la cola: no reclama clave ni se reintenta solo. */
export interface RestoreFiadoInput {
  readonly token?: string
  /** Origen de la API. Vacío es el mismo origen de la PWA. */
  readonly baseUrl?: string
  /** Inyectable para las pruebas; en producción es el `fetch` del navegador. */
  readonly fetchImpl?: typeof fetch
  readonly database?: PosDatabase
}

/** Desenlace de la restauración, con la forma honesta de los tres casos posibles. */
export type RestoreFiadoOutcome =
  | { readonly status: 'restored'; readonly accounts: number; readonly movements: number }
  | { readonly status: 'skipped'; readonly reason: 'no-session' }
  | { readonly status: 'unconfirmed' }

/** Cuenta del servidor tal como la sirve §4.12 (decisión 4 de forma). */
interface RemoteAccount {
  readonly id: string
  readonly name: string
  readonly phone: string | null
  readonly created_at: string
  readonly updated_at: string
  readonly movements: readonly {
    readonly id: string
    readonly fiado_account_id: string
    readonly amount_cents: number
    readonly note: string | null
    readonly sold_on: string | null
    readonly sale_id: string | null
    readonly created_at: string
  }[]
}

/** Lectura tolerante del cuerpo: un campo raro no puede romper la restauración. */
function readString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function readNullableString(value: unknown): string | null {
  return typeof value === 'string' ? value : null
}

function readCents(value: unknown): number {
  return typeof value === 'number' && Number.isInteger(value) ? value : 0
}

/** Proyecta la respuesta del servidor a las dos tiendas locales. */
function project(body: unknown): { accounts: FiadoAccountRow[]; movements: FiadoMovementRow[] } {
  const rawAccounts = (body as { accounts?: unknown })?.accounts
  const accounts: FiadoAccountRow[] = []
  const movements: FiadoMovementRow[] = []

  if (!Array.isArray(rawAccounts)) return { accounts, movements }

  for (const raw of rawAccounts as RemoteAccount[]) {
    const id = readString(raw?.id)
    if (id === '') continue

    accounts.push({
      id,
      name: readString(raw.name),
      phone: readNullableString(raw.phone),
      created_at: readString(raw.created_at),
      updated_at: readString(raw.updated_at),
    })

    for (const rawMovement of Array.isArray(raw.movements) ? raw.movements : []) {
      const movementID = readString(rawMovement?.id)
      if (movementID === '') continue

      movements.push({
        id: movementID,
        fiado_account_id: id,
        amount_cents: readCents(rawMovement.amount_cents),
        note: readNullableString(rawMovement.note),
        sold_on: readNullableString(rawMovement.sold_on),
        sale_id: readNullableString(rawMovement.sale_id),
        created_at: readString(rawMovement.created_at),
      })
    }
  }

  return { accounts, movements }
}

/**
 * Pide la cartera al servidor y reemplaza la vista local en UNA transacción. Las
 * dos tiendas se declaran explícitas: la escritura cubre `fiado_accounts` y
 * `fiado_movements`, y nada más.
 */
export async function restoreFiadoAccounts(
  input: RestoreFiadoInput = {},
): Promise<RestoreFiadoOutcome> {
  const token = input.token?.trim() ?? ''
  if (token === '') {
    // Sin sesión en línea no hay a quién pedirle nada: la cartera local sigue
    // siendo lo único que este dispositivo conoce (el login llega en la Semana 8).
    return { status: 'skipped', reason: 'no-session' }
  }

  const database = input.database ?? getPosDatabase()
  const fetchImpl = input.fetchImpl ?? globalThis.fetch

  let response: Response
  try {
    response = await fetchImpl(`${input.baseUrl ?? ''}/fiado-accounts`, {
      method: 'GET',
      headers: { authorization: `Bearer ${token}` },
    })
  } catch {
    return { status: 'unconfirmed' }
  }

  if (!response.ok) return { status: 'unconfirmed' }

  let body: unknown
  try {
    body = await response.json()
  } catch {
    return { status: 'unconfirmed' }
  }

  const { accounts, movements } = project(body)

  await database.transaction('rw', [database.fiado_accounts, database.fiado_movements], async () => {
    await database.fiado_accounts.clear()
    await database.fiado_movements.clear()
    await database.fiado_accounts.bulkPut(accounts)
    await database.fiado_movements.bulkPut(movements)
  })

  return { status: 'restored', accounts: accounts.length, movements: movements.length }
}
