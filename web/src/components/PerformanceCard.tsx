import { useEffect, useState } from 'react'

import type { PosDatabase } from '@/db/posDatabase'
import { loadPerformanceReport, type PerformanceReport, type PerformanceSummary } from '@/metrics/performance'

/**
 * Tarjeta «Desempeño del dispositivo» del Dashboard Admin (Sprint 9, W9-BE-1).
 *
 * Muestra el p50/p95 de las dos mediciones que solo el cliente puede ver: escaneo a carrito y
 * drenaje de la cola. La regla es la misma que la del KPI de fiado: sin muestras no se inventa
 * un cero; se dice que todavía no se midió.
 *
 * El umbral de 3 s (`docs/BACKEND.md` §8.1) es un objetivo del piloto, no un resultado: la
 * tarjeta lo nombra así y no lo presenta como una promesa cumplida.
 */

/** Umbral de referencia del escaneo, en milisegundos (docs/BACKEND.md §8.1). */
export const SCAN_THRESHOLD_MS = 3000

export interface PerformanceCardProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

export function PerformanceCard({ database }: PerformanceCardProps) {
  const [report, setReport] = useState<PerformanceReport | null>(null)

  useEffect(() => {
    let active = true

    void loadPerformanceReport(database)
      .then((loaded) => {
        if (active) setReport(loaded)
      })
      .catch(() => {
        if (active) setReport(null)
      })

    return () => {
      active = false
    }
  }, [database])

  const isEmpty = report !== null && report.scan.samples === 0 && report.flush.samples === 0

  return (
    <article aria-labelledby="performance-title" className="flex flex-col gap-3 rounded-xl border border-surface-border p-4">
      <header className="flex flex-col gap-1">
        <h3 className="text-heading-sm font-bold" id="performance-title">
          Desempeño del dispositivo
        </h3>
        <p className="text-caption-xs text-text-secondary">
          Mediciones locales de este teléfono. El servidor mide sus propias rutas por separado.
        </p>
      </header>

      {isEmpty ? (
        <p className="text-body-md text-text-secondary" data-testid="performance-empty">
          Todavía no hay mediciones en este dispositivo: escanee un producto o drene la cola para
          registrar la primera.
        </p>
      ) : (
        <ul className="flex flex-col gap-2">
          <PerformanceRow label="Escaneo a carrito" id="scan" summary={report?.scan ?? null} />
          <PerformanceRow label="Drenaje de la cola" id="flush" summary={report?.flush ?? null} />
        </ul>
      )}

      <p className="text-caption-xs text-text-muted">
        El umbral de 3 s del escaneo es un objetivo de medición del piloto, no un resultado.
        {report !== null && report.scan.samples > 0 && report.scan.p95Ms > SCAN_THRESHOLD_MS
          ? ' El p95 de este dispositivo lo supera: la grilla sigue siendo el camino por defecto.'
          : ''}
      </p>
    </article>
  )
}

interface PerformanceRowProps {
  readonly id: 'scan' | 'flush'
  readonly label: string
  readonly summary: PerformanceSummary | null
}

function PerformanceRow({ id, label, summary }: PerformanceRowProps) {
  return (
    <li className="flex items-center justify-between gap-2 rounded-xl border border-surface-border px-3 py-2" data-testid={`performance-${id}`}>
      <span className="flex min-w-0 flex-col">
        <span className="text-body-md text-text-primary">{label}</span>
        <span className="text-caption-xs uppercase text-text-secondary">
          {summary === null || summary.samples === 0 ? 'sin mediciones' : `${summary.samples} muestras`}
        </span>
      </span>
      <span className="shrink-0 font-mono tabular-nums text-body-md text-text-primary">
        {summary === null || summary.samples === 0
          ? '—'
          : `p50 ${formatMs(summary.p50Ms)} · p95 ${formatMs(summary.p95Ms)}`}
      </span>
    </li>
  )
}

/** Formatea milisegundos sin decimales: la resolución de la muestra es el milisegundo. */
export function formatMs(value: number): string {
  return `${Math.round(value)} ms`
}
