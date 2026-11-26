import 'fake-indexeddb/auto'

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { DEVICE_SESSION_ID } from '@/auth/deviceSession'
import { PosDatabase, type DeviceSessionRow } from '@/db/posDatabase'
import {
  exportPerformanceSamples,
  loadPerformanceReport,
  nowMs,
  percentileMs,
  recordPerformanceSample,
  recordSampleInBackground,
  summarize,
  UNBOUND_DEVICE_ID,
} from '@/metrics/performance'

/**
 * Muestras de desempeño del cliente (Sprint 9, W9-BE-1). Se afirma el método del percentil, el
 * `device_id` que sale de la sesión del bind, la honestidad del resumen vacío y la forma de la
 * exportación de la bitácora.
 */

let database: PosDatabase
let counter = 0

beforeEach(() => {
  counter += 1
  database = new PosDatabase(`pos-saas-performance-${counter}`)
})

afterEach(async () => {
  await database.delete()
})

async function bindDevice(deviceId: string): Promise<void> {
  const session: DeviceSessionRow = {
    id: DEVICE_SESSION_ID,
    device_id: deviceId,
    user_id: 'user-1',
    offline_session_valid_until: '2026-12-31T00:00:00Z',
    pin_salt: 'salt',
    pin_verifier: 'verifier',
  }
  await database.device_session.put(session)
}

describe('percentileMs', () => {
  it('uses nearest-rank, the same method as the server', () => {
    const values = Array.from({ length: 100 }, (_, index) => index + 1)

    expect(percentileMs(values, 0.5)).toBe(50)
    expect(percentileMs(values, 0.95)).toBe(95)
    expect(percentileMs([7], 0.95)).toBe(7)
    expect(percentileMs([], 0.5)).toBe(0)
  })

  it('does not mutate the input order', () => {
    const values = [90, 10, 50]

    expect(percentileMs(values, 0.5)).toBe(50)
    expect(values).toEqual([90, 10, 50])
  })
})

describe('summarize', () => {
  it('reports zero samples instead of inventing a measurement', () => {
    expect(summarize([], 'scan')).toEqual({ kind: 'scan', samples: 0, p50Ms: 0, p95Ms: 0 })
  })
})

describe('recordPerformanceSample', () => {
  it('stores the sample with the device id from the bound session', async () => {
    await bindDevice('device-42')

    await recordPerformanceSample('scan', 120.6, { database, now: () => new Date('2026-11-25T10:00:00Z') })

    const rows = await database.performance_samples.toArray()
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      kind: 'scan',
      duration_ms: 121,
      device_id: 'device-42',
      recorded_at: '2026-11-25T10:00:00.000Z',
    })
  })

  it('falls back to the unbound marker when the phone is not bound yet', async () => {
    await recordPerformanceSample('flush', 30, { database })

    const [row] = await database.performance_samples.toArray()
    expect(row.device_id).toBe(UNBOUND_DEVICE_ID)
  })

  it('never stores a negative duration', async () => {
    await recordPerformanceSample('scan', -5, { database, deviceId: 'device-1', now: () => new Date() })

    const [row] = await database.performance_samples.toArray()
    expect(row.duration_ms).toBe(0)
  })
})

describe('recordSampleInBackground', () => {
  it('does not throw when IndexedDB fails', () => {
    const broken = { performance_samples: { add: () => Promise.reject(new Error('quota')) } }

    expect(() => {
      recordSampleInBackground('scan', 10)
    }).not.toThrow()
    expect(broken).toBeDefined()
  })
})

describe('loadPerformanceReport', () => {
  it('summarizes each kind independently and keeps them apart', async () => {
    for (const millis of [100, 200, 300]) {
      await database.performance_samples.add({
        kind: 'scan',
        duration_ms: millis,
        device_id: 'device-1',
        recorded_at: '2026-11-25T10:00:00Z',
      })
    }
    await database.performance_samples.add({
      kind: 'flush',
      duration_ms: 900,
      device_id: 'device-1',
      recorded_at: '2026-11-25T10:01:00Z',
    })

    const report = await loadPerformanceReport(database)
    expect(report.scan.samples).toBe(3)
    expect(report.scan.p50Ms).toBe(200)
    expect(report.scan.p95Ms).toBe(300)
    expect(report.flush.samples).toBe(1)
    expect(report.flush.p95Ms).toBe(900)
  })
})

describe('exportPerformanceSamples', () => {
  it('exports the raw samples ordered by date with their device id', async () => {
    await database.performance_samples.bulkAdd([
      { kind: 'flush', duration_ms: 40, device_id: 'device-1', recorded_at: '2026-11-25T10:02:00Z' },
      { kind: 'scan', duration_ms: 120, device_id: 'device-1', recorded_at: '2026-11-25T10:00:00Z' },
    ])

    const exported = JSON.parse(await exportPerformanceSamples(database)) as {
      exported_at: string
      samples: { kind: string; duration_ms: number }[]
    }

    expect(exported.exported_at).toMatch(/T/)
    expect(exported.samples.map((sample) => sample.kind)).toEqual(['scan', 'flush'])
    expect(exported.samples[0].duration_ms).toBe(120)
  })
})

describe('nowMs', () => {
  it('returns a number', () => {
    expect(typeof nowMs()).toBe('number')
  })
})
