/**
 * Service worker del shell de PWA (W6-FE-2; ARCHITECTURE.md:91, docs/DESIGN.md:254-255).
 *
 * Es un módulo —el registro pasa `type: 'module'`— que importa la política de
 * `sw-policy.js`: una sola fuente de la regla, compartida con las pruebas. No usa la
 * Background Sync API (el motor de sincronización corre en el cliente, en la pestaña) y no
 * cachea respuestas de datos: solo el shell y los assets con hash.
 */

import { cacheNameFor, isApiRequest, staleCacheNames, strategyFor } from './sw-policy.js'

/**
 * La versión del build llega por la URL de registro (`/sw.js?v=<version>`): un despliegue
 * nuevo registra una URL distinta, el navegador instala un worker nuevo y su `activate`
 * borra la caché anterior.
 */
const version = new URL(self.location.href).searchParams.get('v') ?? 'dev'
const cacheName = cacheNameFor(version)

/** Arranque: el shell mínimo, para poder abrir la app sin red. */
const SHELL_URLS = ['/', '/index.html', '/manifest.webmanifest']

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(cacheName)
      // `addAll` es atómico: o entra el shell completo o no entra nada.
      await cache.addAll(SHELL_URLS)
      await self.skipWaiting()
    })(),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys()
      await Promise.all(staleCacheNames(cacheName, names).map((name) => caches.delete(name)))
      await self.clients.claim()
    })(),
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return

  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return

  // La API nunca se sirve desde la caché: los datos del POS son locales (IndexedDB) y la
  // red primero es lo que impide que una versión vieja de los datos se quede pegada.
  if (isApiRequest(url.pathname)) {
    return
  }

  if (strategyFor(url.pathname) === 'cache-first') {
    event.respondWith(
      (async () => {
        const cache = await caches.open(cacheName)
        const cached = await cache.match(request)
        if (cached !== undefined) return cached

        const response = await fetch(request)
        if (response.ok) await cache.put(request, response.clone())
        return response
      })(),
    )
    return
  }

  // Network-first: el shell de esta versión manda, y la copia solo rescata una navegación
  // sin red. Un despliegue nuevo entra en la primera visita con conexión.
  event.respondWith(
    (async () => {
      try {
        const response = await fetch(request)
        if (request.mode === 'navigate' && response.ok) {
          const cache = await caches.open(cacheName)
          await cache.put('/index.html', response.clone())
        }
        return response
      } catch (error) {
        if (request.mode === 'navigate') {
          const cached = await caches.match('/index.html')
          if (cached !== undefined) return cached
        }
        throw error
      }
    })(),
  )
})
