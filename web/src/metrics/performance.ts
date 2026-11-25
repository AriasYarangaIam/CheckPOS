import { loadDeviceSession } from '@/auth/deviceSession'
import { getPosDatabase, type PerformanceSampleRow, type PosDatabase } from '@/db/posDatabase'

/**
 * Muestras de desempeño del dispositivo (Sprint 9, W9-BE-1).
 *
 * El servidor mide lo que ve: la latencia de sus rutas. No puede medir el escaneo ni el
 * drenaje de la cola, porque ocurren antes de que exista una petición y el token no transporta
 * el `device_id` (solo `tenant_id`, `user_id` y `role`). Por eso la mitad cliente del criterio
 * —«métricas registradas por dispositivo»— vive acá, en IndexedDB, y el servidor expone sus
 * propios percentiles en `GET /metrics`.
 *
 * Regla de honestidad: sin muestras no hay número. `summarize` devuelve `samples: 0` y las
 * tarjetas dicen que todavía no se midió, en lugar de mostrar un cero que parece un resultado.
 */

/** Las dos mediciones del piloto. */
export type PerformanceKind = 'scan' | 'flush'

/** Identificador usado cuando el teléfono todavía no se vinculó a un device. */
export const UNBOUND_DEVICE_ID = 'unbound'

/** Resumen de una medición. */
export interface PerformanceSummary {
  readonly kind: PerformanceKind
  readonly samples: number
  readonly p50Ms: number
  readonly p95Ms: number
}

/** Las dos mediciones juntas, tal como las lee la tarjeta del dashboard. */
export interface PerformanceReport {
  readonly scan: PerformanceSummary
  readonly flush: PerformanceSummary
}

/**
 * Percentil sobre una lista de duraciones, con el método nearest-rank —el mismo del servidor—:
 * el menor valor cuya posición acumulada alcanza el cuantil. Sin muestra devuelve 0.
 */
export function percentileMs(values: readonly number[], quantile: number): number {
  if (values.length === 0) return 0

  const sorted = [...values].sort((left, right) => left - right)
  const rank = Math.max(1, Math.ceil(quantile * sorted.length))

  return sorted[Math.min(rank, sorted.length) - 1]
}

/** Resume las muestras de una medición. */
export function summarize(values: readonly number[], kind: PerformanceKind): PerformanceSummary {
  return {
    kind,
    samples: values.length,
    p50Ms: percentileMs(values, 0.5),
    p95Ms: percentileMs(values, 0.95),
  }
}

/** Reloj monótono del navegador; `performance.now` cuando existe, `Date.now` en su defecto. */
export function nowMs(): number {
  if (typeof performance !== 'undefined' && typeof performance.now === 'function') {
    return performance.now()
  }

  return Date.now()
}

/** Opciones de escritura de una muestra. */
export interface RecordSampleOptions {
  readonly database?: PosDatabase
  /** Inyectable para las pruebas; en producción sale de la sesión del bind. */
  readonly deviceId?: string
  /** Inyectable para las pruebas: fija `recorded_at`. */
  readonly now?: () => Date
}

/**
 * Escribe una muestra. Es un entero no negativo: un reloj que retrocede no puede dejar una
 * duración negativa en la bitácora.
 */
export async function recordPerformanceSample(
  kind: PerformanceKind,
  durationMs: number,
  options: RecordSampleOptions = {},
): Promise<void> {
  const database = options.database ?? getPosDatabase()
  const deviceId = options.deviceId ?? (await loadDeviceSession(database))?.device_id ?? UNBOUND_DEVICE_ID
  const now = options.now ?? (() => new Date())

  await database.performance_samples.add({
    kind,
    duration_ms: Math.max(0, Math.round(durationMs)),
    device_id: deviceId,
    recorded_at: now().toISOString(),
  })
}

/**
 * Registra una muestra sin bloquear ni romper el flujo que la origina: un fallo de IndexedDB
 * no puede tumbar un escaneo ni un drenaje. Es observabilidad, no negocio.
 */
export function recordSampleInBackground(kind: PerformanceKind, durationMs: number): void {
  void recordPerformanceSample(kind, durationMs).catch(() => {
    // Silencio deliberado: la muestra es un extra y su pérdida no cambia una venta.
  })
}

/** Lee y resume todas las muestras del dispositivo. */
export async function loadPerformanceReport(database: PosDatabase = getPosDatabase()): Promise<PerformanceReport> {
  const rows = await database.performance_samples.toArray()

  return {
    scan: summarize(
      rows.filter((row) => row.kind === 'scan').map((row) => row.duration_ms),
      'scan',
    ),
    flush: summarize(
      rows.filter((row) => row.kind === 'flush').map((row) => row.duration_ms),
      'flush',
    ),
  }
}

/** Exporta las muestras crudas para la bitácora del piloto, ordenadas por fecha. */
export async function exportPerformanceSamples(database: PosDatabase = getPosDatabase()): Promise<string> {
  const rows = await database.performance_samples.orderBy('recorded_at').toArray()
  const payload: { exported_at: string; samples: readonly PerformanceSampleRow[] } = {
    exported_at: new Date().toISOString(),
    samples: rows,
  }

  return JSON.stringify(payload, null, 2)
}
