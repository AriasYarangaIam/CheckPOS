// @ts-ignore -- la política la importa el service worker real: es ESM puro, sin tipos.
import { CACHE_PREFIX, cacheNameFor, isApiRequest, isHashedAsset, staleCacheNames, strategyFor } from '../../public/sw-policy.js'

import { describe, expect, it } from 'vitest'

import { serviceWorkerUrl } from '@/pwa/register'

/**
 * Reglas del shell de PWA (W6-FE-2, decisión D8): nada de cache-first ciego, caché
 * versionada y limpieza de la versión anterior. Las tres juntas son lo que hace que un
 * despliegue nuevo llegue al dispositivo y la ruta de cobro no quede fijada.
 */
describe('PWA cache policy', () => {
  it('serves hashed assets from the cache and everything else from the network first', () => {
    expect(strategyFor('/assets/index-4f9a2c1b.js')).toBe('cache-first')
    expect(strategyFor('/assets/index.css-8d3e7a12.css')).toBe('cache-first')

    // La navegación y el shell NUNCA se sirven primero de la caché: es la ruta de cobro.
    expect(strategyFor('/')).toBe('network-first')
    expect(strategyFor('/index.html')).toBe('network-first')
    expect(strategyFor('/sw.js')).toBe('network-first')
  })

  it('recognises the assets that are immutable by construction', () => {
    expect(isHashedAsset('/assets/index-4f9a2c1b.js')).toBe(true)
    expect(isHashedAsset('/assets/index.js')).toBe(false)
    expect(isHashedAsset('/index.html')).toBe(false)
  })

  it('treats the contract API as data, never as cacheable shell', () => {
    expect(isApiRequest('/sales')).toBe(true)
    expect(isApiRequest('/sales/019a/void')).toBe(true)
    expect(isApiRequest('/sync/cursor')).toBe(true)
    expect(isApiRequest('/products')).toBe(true)
    expect(isApiRequest('/index.html')).toBe(false)
  })

  it('versions the cache name with the build version', () => {
    expect(cacheNameFor('1.2.3')).toBe(`${CACHE_PREFIX}1.2.3`)
    expect(serviceWorkerUrl('1.2.3')).toBe('/sw.js?v=1.2.3')
    expect(cacheNameFor('1.2.3')).not.toBe(cacheNameFor('1.2.4'))
  })

  it('deletes only the shell caches of other versions', () => {
    const all = [cacheNameFor('1.2.2'), cacheNameFor('1.2.3'), 'otra-app-cache']

    expect(staleCacheNames(cacheNameFor('1.2.3'), all)).toEqual([cacheNameFor('1.2.2')])
  })
})
