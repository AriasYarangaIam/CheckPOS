import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  ProductScanner,
  type BarcodeDetectorLike,
  type ScannerFailure,
} from '@/scanner/ProductScanner'

/*
 * Escáner best-effort de W4-FE-3 (docs/DESIGN.md §1.2:138, :251-252): la
 * cámara es un ADAPTADOR hacia `onProductIdentified`, el mismo camino que la
 * grilla. Todo fallo (permiso denegado, cámara ausente, error) se reporta
 * una sola vez por categoría y el componente cede sin bloquear la venta.
 */

const CODE = '7591234567890'

function fakeStream(): { stream: MediaStream; stop: ReturnType<typeof vi.fn> } {
  const stop = vi.fn()

  const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream

  return { stream, stop }
}

/** DOMException del entorno con `name` y `message` del mismo código de rechazo. */
function mediaError(name: string): DOMException {
  return new DOMException(name, name)
}

function asGetUserMedia(
  stub: ReturnType<typeof vi.fn>,
): typeof navigator.mediaDevices.getUserMedia {
  return stub as unknown as typeof navigator.mediaDevices.getUserMedia
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ProductScanner', () => {
  it('classifies the camera permission refusal as denied', async () => {
    const getUserMedia = vi.fn(async () => {
      throw mediaError('NotAllowedError')
    })
    const onFailure = vi.fn<(reason: ScannerFailure) => void>()

    render(
      <ProductScanner getUserMedia={asGetUserMedia(getUserMedia)} onFailure={onFailure} />,
    )

    await waitFor(() => expect(onFailure).toHaveBeenCalledTimes(1))
    expect(onFailure).toHaveBeenCalledWith('denied')
    expect(getUserMedia).toHaveBeenCalledWith({ video: true, audio: false })
  })

  it('treats a missing camera device as unavailable', async () => {
    const getUserMedia = vi.fn(async () => {
      throw mediaError('NotFoundError')
    })
    const onFailure = vi.fn<(reason: ScannerFailure) => void>()

    render(
      <ProductScanner getUserMedia={asGetUserMedia(getUserMedia)} onFailure={onFailure} />,
    )

    await waitFor(() => expect(onFailure).toHaveBeenCalledTimes(1))
    expect(onFailure).toHaveBeenCalledWith('unavailable')
  })

  it('classifies any other camera error as failed', async () => {
    const getUserMedia = vi.fn(async () => {
      throw mediaError('ReglaIncumplida')
    })
    const onFailure = vi.fn<(reason: ScannerFailure) => void>()

    render(
      <ProductScanner getUserMedia={asGetUserMedia(getUserMedia)} onFailure={onFailure} />,
    )

    await waitFor(() => expect(onFailure).toHaveBeenCalledTimes(1))
    expect(onFailure).toHaveBeenCalledWith('failed')
  })

  it('treats a broken decoder as an unavailable camera', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())

    const getUserMedia = vi.fn(async () => fakeStream().stream)
    const onFailure = vi.fn<(reason: ScannerFailure) => void>()

    render(
      <ProductScanner
        getUserMedia={asGetUserMedia(getUserMedia)}
        createBarcodeDetector={async () => {
          throw mediaError('SinDetector')
        }}
        onFailure={onFailure}
      />,
    )

    await waitFor(() => expect(onFailure).toHaveBeenCalledTimes(1))
    expect(onFailure).toHaveBeenCalledWith('unavailable')
  })

  it('emits a code once while it stays in the viewfinder, without re-adding while visible', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())

    const { stream, stop } = fakeStream()
    const getUserMedia = vi.fn(async () => stream)

    let frames = 0
    const detector: BarcodeDetectorLike = {
      detect: async () => {
        frames += 1

        return [{ rawValue: CODE }]
      },
    }
    const onProductIdentified = vi.fn()

    const view = render(
      <ProductScanner
        getUserMedia={asGetUserMedia(getUserMedia)}
        createBarcodeDetector={async () => detector}
        onProductIdentified={onProductIdentified}
        detectIntervalMs={10}
      />,
    )

    // El visor existe justo cuando el stream llega al video.
    expect(await screen.findByTestId('scanner-viewport')).toBeTruthy()
    // El mismo código visible en la cámara NO agrega línea tras línea: se
    // emite UNA vez mientras el paquete siga frente al lente (varias pasadas
    // del detector no vuelven a emitir).
    await waitFor(() => expect(frames).toBeGreaterThanOrEqual(5))
    expect(onProductIdentified).toHaveBeenCalledTimes(1)
    expect(onProductIdentified).toHaveBeenCalledWith(CODE)

    view.unmount()
    // El desmonte apaga la cámara: sin track vivo colgado del dispositivo.
    expect(stop).toHaveBeenCalled()
  })

  it('re-arms the same code after it left the viewfinder, like a deliberate rescan', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())

    const getUserMedia = vi.fn(async () => fakeStream().stream)

    // C, C (visible), dos pasadas vacías (el paquete salió), C (rescan).
    const frames = [CODE, CODE, '', '', CODE]
    let index = 0
    const detector: BarcodeDetectorLike = {
      detect: async () => {
        const frame = frames[Math.min(index, frames.length - 1)]

        index += 1

        return frame === '' ? [] : [{ rawValue: frame }]
      },
    }
    const onProductIdentified = vi.fn()

    render(
      <ProductScanner
        getUserMedia={asGetUserMedia(getUserMedia)}
        createBarcodeDetector={async () => detector}
        onProductIdentified={onProductIdentified}
        detectIntervalMs={10}
      />,
    )

    await waitFor(() =>
      expect(onProductIdentified.mock.calls).toEqual([[CODE], [CODE]]),
    )
  })

  it('emits a different code immediately regardless of what was seen before', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())

    const getUserMedia = vi.fn(async () => fakeStream().stream)
    const frames = ['AAA', 'AAA', 'BBB']

    let index = 0
    const detector: BarcodeDetectorLike = {
      detect: async () => {
        const frame = frames[Math.min(index, frames.length - 1)]
        index += 1

        return [{ rawValue: frame }]
      },
    }
    const onProductIdentified = vi.fn()

    render(
      <ProductScanner
        getUserMedia={asGetUserMedia(getUserMedia)}
        createBarcodeDetector={async () => detector}
        onProductIdentified={onProductIdentified}
        detectIntervalMs={5}
      />,
    )

    await waitFor(() =>
      expect(onProductIdentified.mock.calls).toEqual([['AAA'], ['BBB']]),
    )
  })
})
