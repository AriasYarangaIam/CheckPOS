import { render, screen, waitFor } from '@testing-library/react'
import 'fake-indexeddb/auto'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { PerformanceCard, SCAN_THRESHOLD_MS } from '@/components/PerformanceCard'
import { PosDatabase } from '@/db/posDatabase'

/**
 * Tarjeta «Desempeño del dispositivo» (Sprint 9, W9-BE-1): sin muestras dice que no midió y
 * con muestras muestra p50/p95; el umbral de 3 s se nombra como objetivo del piloto.
 */

let database: PosDatabase
let counter = 0

beforeEach(() => {
  counter += 1
  database = new PosDatabase(`pos-saas-performance-card-${counter}`)
})

afterEach(async () => {
  await database.delete()
})

async function addSamples(kind: 'scan' | 'flush', durations: number[]): Promise<void> {
  for (const duration of durations) {
    await database.performance_samples.add({
      kind,
      duration_ms: duration,
      device_id: 'device-1',
      recorded_at: '2026-11-25T10:00:00Z',
    })
  }
}

describe('PerformanceCard', () => {
  it('says there are no measurements instead of rendering zeros', async () => {
    render(<PerformanceCard database={database} />)

    expect(await screen.findByTestId('performance-empty')).toBeTruthy()
    expect(screen.queryByText(/p50/)).toBeNull()
  })

  it('renders p50 and p95 for each measurement', async () => {
    await addSamples('scan', [100, 200, 300, 400])
    await addSamples('flush', [40, 60])

    render(<PerformanceCard database={database} />)

    const scan = await screen.findByTestId('performance-scan')
    await waitFor(() => expect(scan.textContent).toContain('p50 200 ms'))
    expect(scan.textContent).toContain('p95 400 ms')
    expect(scan.textContent).toContain('4 muestras')

    const flush = screen.getByTestId('performance-flush')
    expect(flush.textContent).toContain('p50 40 ms')
    expect(flush.textContent).toContain('p95 60 ms')
  })

  it('names the 3 second threshold as a measurement target, never as a result', async () => {
    await addSamples('scan', [100])

    render(<PerformanceCard database={database} />)

    expect(await screen.findByText(/objetivo de medición del piloto, no un resultado/)).toBeTruthy()
  })

  it('flags a p95 above the threshold and keeps the grid as the default path', async () => {
    await addSamples('scan', [100, SCAN_THRESHOLD_MS + 1])

    render(<PerformanceCard database={database} />)

    expect(await screen.findByText(/supera: la grilla sigue siendo el camino por defecto/)).toBeTruthy()
  })
})
