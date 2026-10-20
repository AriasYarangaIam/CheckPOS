import { useEffect, useRef, useState } from 'react'

/**
 * Escáner de códigos de barras como adaptador best-effort (W4-FE-3;
 * docs/DESIGN.md §1.2:138, §251-252). La cámara NUNCA bloquea la venta:
 * cualquier fallo se reporta una sola vez por categoría a `onFailure` y el
 * hospedador decide el fallback (la pantalla vuelve a la grilla de Productos
 * con un aviso no modal; la venta se completa igual desde ahí).
 *
 * El resultado del decodificado viaja por `onProductIdentified`: el mismo
 * camino que la grilla, con el agregado y el debounce de W4-FE-1.
 *
 * getUserMedia y la fábrica de BarcodeDetector son inyectables para las
 * pruebas; en producción usa el del navegador y el detector nativo
 * (BarcodeDetector API). El bucle emite cada código UNA vez mientras está
 * frente al lente: repite solo cuando el código salió del recuadro (rescan
 * deliberado) o llega un código distinto. Un paquete parado frente a la
 * cámara no agrega línea tras línea.
 */

export interface DetectedBarcodeLike {
  readonly rawValue: string
}

export interface BarcodeDetectorLike {
  readonly detect: (source: HTMLVideoElement) => Promise<readonly DetectedBarcodeLike[]>
}

export type BarcodeDetectorFactory = () => Promise<BarcodeDetectorLike>

/** Motivo por el que la cámara no pudo seguir: lo traduce la pantalla host. */
export type ScannerFailure = 'denied' | 'unavailable' | 'failed'

/** Subconjunto de `MediaStream` que el componente necesita apagar. */
export type CameraStream = Pick<MediaStream, 'getTracks'>

export interface ProductScannerProps {
  /** El código decodificado entra por el mismo camino que la grilla. */
  readonly onProductIdentified?: (code: string) => void
  /** Un fallo de la cámara, una sola vez: el hospedador regresa a la grilla. */
  readonly onFailure?: (reason: ScannerFailure) => void
  /** Inyectable para las pruebas; en producción, `navigator.mediaDevices`. */
  readonly getUserMedia?: typeof navigator.mediaDevices.getUserMedia
  /** Inyectable para las pruebas; en producción, `BarcodeDetector` nativo. */
  readonly createBarcodeDetector?: BarcodeDetectorFactory
  /** Pausa entre pasadas del bucle de detección, en ms. */
  readonly detectIntervalMs?: number
}

/** Pausa del bucle: suficiente para no reventar la CPU, corto para no frustrar. */
export const DEFAULT_DETECT_INTERVAL_MS = 300

declare global {
  interface Window {
    /** Detector nativo (Chrome/Android); falta en otros navegadores. */
    BarcodeDetector?: new (options?: {
      readonly formats?: readonly string[]
    }) => BarcodeDetectorLike
  }
}

/** Clasificación del rechazo de `getUserMedia` (§251-252). */
function classifyMediaFailure(error: unknown): ScannerFailure {
  // Por `instanceof Error`: el `DOMException` de jsdom no hereda de `Error`.
  const name = (error as { name?: string } | null | undefined)?.name ?? ''

  if (name === 'NotAllowedError' || name === 'SecurityError') return 'denied'
  if (
    name === 'NotFoundError' ||
    name === 'OverconstrainedError' ||
    name === 'NotReadableError'
  ) {
    return 'unavailable'
  }

  return 'failed'
}

/** Lazos del navegador; el `bind` conserva el `this` de `mediaDevices`. */
function defaultCamera(): typeof navigator.mediaDevices.getUserMedia | undefined {
  return navigator.mediaDevices?.getUserMedia?.bind(navigator.mediaDevices)
}

/** Fábrica por defecto: rechaza cuando el navegador no trae el detector. */
async function defaultDetectorFactory(): Promise<BarcodeDetectorLike> {
  const NativeDetector = window.BarcodeDetector

  if (NativeDetector === undefined) {
    throw new Error('BarcodeDetector no está disponible en este navegador')
  }

  return new NativeDetector()
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

export function ProductScanner({
  onProductIdentified,
  onFailure,
  getUserMedia,
  createBarcodeDetector,
  detectIntervalMs = DEFAULT_DETECT_INTERVAL_MS,
}: ProductScannerProps) {
  const [status, setStatus] = useState<'starting' | 'scanning'>('starting')
  const videoRef = useRef<HTMLVideoElement | null>(null)

  // El ciclo de vida de la cámara es del MONTAJE del segmento: los props se
  // leen por referencia viva para que un re-render del carrito (agregar una
  // línea) no reinicie el stream. Se enciende una vez y se apaga al desmontar.
  const cameraRef = useRef({ onProductIdentified, onFailure, getUserMedia, createBarcodeDetector, detectIntervalMs })
  cameraRef.current = {
    onProductIdentified,
    onFailure,
    getUserMedia,
    createBarcodeDetector,
    detectIntervalMs,
  }

  useEffect(() => {
    let cancelled = false
    let stream: CameraStream | null = null

    void (async () => {
      const props = cameraRef.current
      const getCamera = props.getUserMedia ?? defaultCamera()

      if (getCamera === undefined) {
        props.onFailure?.('unavailable')

        return
      }

      try {
        stream = await getCamera({ video: true, audio: false })
      } catch (error) {
        props.onFailure?.(classifyMediaFailure(error))

        return
      }

      if (cancelled) return

      const video = videoRef.current

      if (video === null) {
        props.onFailure?.('failed')

        return
      }

      try {
        video.srcObject = stream as MediaStream
        // jsdom y navegadores sin gesto pueden bloquear el autoplay: el visor
        // no es la venta, así que la falla de `play` no sube de categoría.
        void Promise.resolve(video.play()).catch(() => {})
      } catch {
        props.onFailure?.('failed')

        return
      }

      let detector: BarcodeDetectorLike

      try {
        detector = props.createBarcodeDetector
          ? await props.createBarcodeDetector()
          : await defaultDetectorFactory()
      } catch {
        props.onFailure?.('unavailable')

        return
      }

      if (cancelled) return

      setStatus('scanning')

      // El código armado es el último emitido; dos pasadas vacías seguidas lo
      // desarmaron (el paquete salió del recuadro). Un detector que titila UNA
      // pasada no vuelve a emitir el mismo código.
      let armedCode: string | null = null
      let emptyStreak = 0

      while (!cancelled) {
        let barcodes: readonly DetectedBarcodeLike[]

        try {
          barcodes = await detector.detect(video)
        } catch {
          // Pasada fallida del detector: la próxima pasada reintenta gratis.
          await delay(props.detectIntervalMs)

          continue
        }

        if (cancelled) break

        const barcode = barcodes[0]
        const code = barcode === undefined ? '' : barcode.rawValue.trim()

        if (code === '') {
          emptyStreak += 1

          if (emptyStreak >= 2) armedCode = null
        } else {
          emptyStreak = 0

          if (armedCode !== code) {
            armedCode = code
            props.onProductIdentified?.(code)
          }
        }

        await delay(props.detectIntervalMs)
      }
    })()

    return () => {
      cancelled = true

      if (stream !== null) {
        for (const track of stream.getTracks()) track.stop()
      }
    }
    // La cámara vive del montaje del segmento; los props viajan por referencia.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <section
      aria-label="Escáner de códigos"
      className="flex w-full flex-col gap-2"
      data-testid="scanner-viewport"
    >
      {/*
       * Viewport 4:3 con guías de esquina en verde esmeralda (§1.2:139). Sin
       * animación de láser: el movimiento decorativo se excluye por diseño de
       * este slice y `prefers-reduced-motion` no queda en riesgo.
       */}
      <div className="relative aspect-[4/3] w-full overflow-hidden rounded-2xl bg-zinc-900">
        <video
          className="h-full w-full object-cover"
          data-testid="scanner-video"
          muted
          playsInline
          ref={videoRef}
        />
        {/* Guías de enfoque: cuatro esquinas en emerald sobre el video vivo. */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-4 border-[2px] border-emerald-500/0"
        >
          <span className="absolute left-0 top-0 h-4 w-4 border-l-2 border-t-2 border-emerald-500" />
          <span className="absolute right-0 top-0 h-4 w-4 border-r-2 border-t-2 border-emerald-500" />
          <span className="absolute bottom-0 left-0 h-4 w-4 border-b-2 border-l-2 border-emerald-500" />
          <span className="absolute bottom-0 right-0 h-4 w-4 border-b-2 border-r-2 border-emerald-500" />
        </div>
      </div>
      <p className="text-body-sm text-text-secondary">
        {status === 'starting'
          ? 'Iniciando la cámara…'
          : 'Apunta el código de barras al recuadro verde'}
      </p>
    </section>
  )
}
