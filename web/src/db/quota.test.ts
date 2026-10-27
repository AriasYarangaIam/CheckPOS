import { describe, expect, it } from 'vitest'

import {
  boundedCatalogRows,
  isStorageQuotaError,
  MAX_LOCAL_PRODUCTS,
  StorageQuotaBlockedError,
  STORAGE_BLOCKED_REASON,
} from '@/db/quota'

/**
 * Reglas de la degradación por cuota (W6-FE-1): reconocer el agotamiento de espacio y
 * acotar el subconjunto local. La prueba de la pantalla —el bloqueo visible con su
 * motivo— vive en CounterSaleScreen.test.tsx.
 */
describe('quota degradation', () => {
  it('recognises the quota error by name, including the error Dexie wraps', () => {
    expect(isStorageQuotaError(new DOMException('lleno', 'QuotaExceededError'))).toBe(true)
    expect(isStorageQuotaError({ name: 'QuotaExceededError' })).toBe(true)
    expect(isStorageQuotaError({ name: 'DexieError', inner: { name: 'QuotaExceededError' } })).toBe(true)
  })

  it('does not mistake another write failure for exhausted space', () => {
    expect(isStorageQuotaError(new Error('disco lleno'))).toBe(false)
    expect(isStorageQuotaError({ name: 'ConstraintError' })).toBe(false)
    expect(isStorageQuotaError(null)).toBe(false)
    expect(isStorageQuotaError(undefined)).toBe(false)
  })

  it('names the block with an operator-readable reason', () => {
    const blocked = new StorageQuotaBlockedError()

    expect(blocked.reason).toBe(STORAGE_BLOCKED_REASON)
    expect(blocked.name).toBe('StorageQuotaBlockedError')
    expect(blocked.reason).toContain('El cobro sigue abierto')
  })

  it('caps the local catalog subset at the bound and keeps the serving order', () => {
    const rows = Array.from({ length: MAX_LOCAL_PRODUCTS + 5 }, (_, index) => index)

    const bounded = boundedCatalogRows(rows)

    expect(bounded).toHaveLength(MAX_LOCAL_PRODUCTS)
    expect(bounded[0]).toBe(0)
    expect(bounded[bounded.length - 1]).toBe(MAX_LOCAL_PRODUCTS - 1)
  })
})
