import { getPosDatabase, type FiadoAccountRow, type FiadoMovementRow, type PosDatabase } from '@/db/posDatabase'

/**
 * Registro local de deudores (W7-FE-1; docs/FRONTEND.md:51; docs/PRODUCT.md:27).
 *
 * La invariante que este módulo sostiene es la del contrato: **no hay saldo
 * guardado**. El saldo de una cuenta es `SUM(fiado_movements.amount_cents)` y se
 * computa al leer, igual que en el servidor (`docs/DATABASE.md` §4
 * `fiado_accounts`, decisión 7 del sprint 7). Guardarlo acá sería una segunda
 * fuente de verdad que se desincroniza en cuanto un movimiento entra por otra vía
 * —el acuse del servidor o la restauración—.
 *
 * La búsqueda trabaja sobre lo que el cajero recuerda de un deudor: el nombre
 * (sin distinguir mayúsculas ni acentos) y el teléfono (solo los dígitos, para
 * que «+51 987 111 222» y «987111222» encuentren lo mismo).
 */

/** Datos editables de un deudor: lo único que el contrato deja escribir (LWW, §7.4). */
export interface FiadoAccountInput {
  readonly id: string
  readonly name: string
  readonly phone: string | null
}

/**
 * Saldo computado de una cuenta: la suma firmada de sus movimientos. Un saldo
 * negativo es legítimo —un abono puede adelantar el pago de una deuda— y no se
 * recorta: el contrato permite el saldo a favor.
 */
export function accountBalanceCents(movements: readonly FiadoMovementRow[]): number {
  return movements.reduce((total, movement) => total + movement.amount_cents, 0)
}

/** Normaliza un texto para buscar: minúsculas y sin diacríticos. */
export function normalizeForSearch(value: string): string {
  return value
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .trim()
}

/** Deja solo los dígitos de un teléfono, para compararlo sin prefijo ni separadores. */
export function phoneDigits(value: string | null): string {
  return value === null ? '' : value.replace(/\D/g, '')
}

/**
 * Filtra la cartera por nombre o teléfono. Una consulta vacía devuelve la cartera
 * completa; una consulta sin coincidencias devuelve la lista vacía —nunca el
 * listado entero, que haría creer que la búsqueda funcionó—.
 */
export function searchFiadoAccounts(
  accounts: readonly FiadoAccountRow[],
  query: string,
): readonly FiadoAccountRow[] {
  const normalized = normalizeForSearch(query)
  const digits = phoneDigits(query)

  if (normalized === '' && digits === '') return accounts

  return accounts.filter((account) => {
    const nameMatches = normalized !== '' && normalizeForSearch(account.name).includes(normalized)
    const phoneMatches = digits !== '' && phoneDigits(account.phone).includes(digits)

    return nameMatches || phoneMatches
  })
}

/**
 * Siembra la cuenta local si no existe. NUNCA pisa una fila existente: el nombre
 * y el teléfono que ya están en el dispositivo son los que el operador registró,
 * y el servidor es quien resuelve una mutación con el comparador LWW (§7.4). El
 * alta local existe para que la cartera muestre la deuda en el mismo instante en
 * que el cargo se encola, sin conexión.
 */
export async function seedFiadoAccount(
  account: FiadoAccountInput,
  mutatedAt: string,
  database: PosDatabase = getPosDatabase(),
): Promise<void> {
  await database.transaction('rw', database.fiado_accounts, async () => {
    const existing = await database.fiado_accounts.get(account.id)

    if (existing !== undefined) return

    await database.fiado_accounts.add({
      id: account.id,
      name: account.name,
      phone: account.phone,
      created_at: mutatedAt,
      updated_at: mutatedAt,
    })
  })
}

/** Lee la cartera completa con su historial, ordenada por nombre como el servidor. */
export async function readFiadoWallet(database: PosDatabase = getPosDatabase()): Promise<{
  readonly accounts: readonly FiadoAccountRow[]
  readonly movements: readonly FiadoMovementRow[]
}> {
  const [accounts, movements] = await Promise.all([
    database.fiado_accounts.toArray(),
    database.fiado_movements.toArray(),
  ])

  return {
    accounts: accounts.sort((left, right) => left.name.localeCompare(right.name)),
    movements,
  }
}
