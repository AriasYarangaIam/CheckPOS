import { useEffect, useState } from 'react'

/**
 * Estado de conectividad observable (docs/FRONTEND.md:101-103).
 *
 * `navigator.onLine` es la fuente de verdad y los eventos `online` / `offline`
 * del `window` son solo el aviso de que hay que releerlo: el handler vuelve a
 * consultar el estado en lugar de deducirlo del nombre del evento. Un evento
 * que llega tarde —el `offline` que se despacha cuando la conexión ya volvió—
 * no puede dejar el estado desincronizado.
 *
 * No hace ninguna petición: la conectividad es un dato del navegador y el POS
 * no depende de la red para arrancar.
 */

export interface NetworkStatus {
  readonly isOnline: boolean
}

/** Estado de red actual, con suscripción a los dos eventos que lo anuncian. */
export function useNetworkStatus(): NetworkStatus {
  // El estado inicial sale de `navigator.onLine`, no de una constante: un POS
  // que abre sin conexión tiene que saberlo desde el primer render.
  const [isOnline, setIsOnline] = useState(() => navigator.onLine)

  useEffect(() => {
    const sync = (): void => {
      setIsOnline(navigator.onLine)
    }

    window.addEventListener('online', sync)
    window.addEventListener('offline', sync)

    // Un cambio entre el primer render y este efecto no emite evento: se relee
    // una vez al suscribirse. Si el valor no cambió, React no vuelve a renderizar.
    sync()

    return () => {
      window.removeEventListener('online', sync)
      window.removeEventListener('offline', sync)
    }
  }, [])

  return { isOnline }
}
