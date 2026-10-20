import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SaleTicket, composeTicketShareText } from '@/components/SaleTicket'

/*
 * Ticket en pantalla de W4-FE-4 (docs/DESIGN.md §176; SPRINTS.md:230): el
 * resumen de la venta cerrada con SU número de operación y el share sheet
 * NATIVO del sistema — jamás un camino `wa.me`, que está fuera del alcance
 * MVP. Sin `navigator.share` (escritorio) el botón desaparece: la venta
 * termina igual y no se invita a un compartir que no existe.
 */

function asShare(
  stub: ReturnType<typeof vi.fn>,
): (data?: ShareData) => Promise<void> {
  return stub as unknown as (data?: ShareData) => Promise<void>
}

afterEach(() => {
  vi.restoreAllMocks()
  Reflect.deleteProperty(window.navigator, 'vibrate')
})

describe('SaleTicket', () => {
  it('renders the closed sale summary with total, method label and the operation number', () => {
    render(
      <SaleTicket
        method="yape"
        receivedCents={30}
        totalCents={30}
        operationNumber="9f1c2a44-0b1e-4b6d-8f2a-71c9d4b20a11"
        delivery={{ status: 'acknowledged', ack: {} }}
      />,
    )

    expect(screen.getByTestId('sale-closed-summary')).toBeTruthy()
    expect(screen.getByText('S/ 0.30')).toBeTruthy()
    expect(screen.getByText('Yape: cobro exacto de S/ 0.30.')).toBeTruthy()
    expect(
      screen.getByText(/9f1c2a44-0b1e-4b6d-8f2a-71c9d4b20a11/).textContent,
    ).toContain('N.º de operación')
    expect(screen.getByText('El servidor confirmó la venta con acuse.')).toBeTruthy()
  })

  it('tells the honest delivery truth for a sale closed without a token', () => {
    render(
      <SaleTicket
        method="cash"
        receivedCents={1000}
        totalCents={300}
        operationNumber="id-local"
        delivery="not-sent"
      />,
    )

    expect(screen.getByText('La venta cerró en este dispositivo; el envío queda pendiente hasta que la sesión en línea esté disponible.')).toBeTruthy()
    expect(screen.getByText('Efectivo: recibido S/ 10.00, vuelto S/ 7.00.')).toBeTruthy()
  })

  it('vibrates the sale success pattern on mount', () => {
    // jsdom no trae motor de vibración: el patrón §257 se prueba con el
    // troquel en el navigator, igual que el agregado de la grilla.
    const vibrate = vi.fn()
    ;(window.navigator as { vibrate?: unknown }).vibrate = vibrate

    render(
      <SaleTicket
        method="yape"
        receivedCents={30}
        totalCents={30}
        operationNumber="id"
        delivery={{ status: 'acknowledged', ack: {} }}
      />,
    )

    expect(vibrate).toHaveBeenCalledWith([60, 60])
  })

  it('offers no share action when the system share sheet is unavailable', () => {
    render(
      <SaleTicket
        method="yape"
        receivedCents={30}
        totalCents={30}
        operationNumber="id"
        delivery={{ status: 'acknowledged', ack: {} }}
        share={undefined}
      />,
    )

    expect(screen.queryByTestId('share-ticket')).toBeNull()
  })

  it('shares the composed ticket through the system share sheet, with no wa.me path', async () => {
    // Sin camino wa.me (§176): ni un link, ni window.open, ni intento de
    // WhatsApp. El destino lo elige el share sheet del sistema.
    const open = vi.spyOn(window, 'open')
    const share = vi.fn(async (_data?: ShareData): Promise<void> => {})

    render(
      <SaleTicket
        method="yape"
        receivedCents={30}
        totalCents={30}
        operationNumber="op-1"
        delivery={{ status: 'acknowledged', ack: {} }}
        share={asShare(share)}
      />,
    )

    fireEvent.click(screen.getByTestId('share-ticket'))

    await waitFor(() => expect(share).toHaveBeenCalledTimes(1))

    const shareData = share.mock.calls[0]?.[0]

    expect(shareData?.title).toBe('Venta de mostrador')
    expect(String(shareData?.text)).toContain('S/ 0.30')
    expect(String(shareData?.text)).toContain('Yape')
    expect(String(shareData?.text)).not.toContain('wa.me')
    expect(open).not.toHaveBeenCalled()
    // El resultado honesto queda en pantalla.
    expect(await screen.findByText('Compartido con el sistema.')).toBeTruthy()
  })

  it('stays steady when the user cancels the share sheet', async () => {
    const share = vi.fn(async () => {
      throw new DOMException('AbortError', 'AbortError')
    })

    render(<SaleTicket
        method="cash"
        receivedCents={30}
        totalCents={30}
        operationNumber="id"
        delivery={{ status: 'acknowledged', ack: {} }}
        share={asShare(share)}
      />,
    )

    fireEvent.click(screen.getByTestId('share-ticket'))

    await waitFor(() => expect(share).toHaveBeenCalledTimes(1))

    // Cancelar no es un error del cajero ni de la venta: el ticket sigue tal cual.
    expect(screen.queryByText('Compartido con el sistema.')).toBeNull()
    expect(screen.queryByTestId('share-ticket')).toBeTruthy()
  })

  it('composes the share text from the frozen values, never recomputing money', () => {
    const text = composeTicketShareText({
      method: 'cash',
      receivedCents: 1000,
      totalCents: 300,
      operationNumber: 'op-7',
    })

    expect(text).toContain('Venta de mostrador')
    expect(text).toContain('Total: S/ 3.00')
    expect(text).toContain('Efectivo: recibido S/ 10.00, vuelto S/ 7.00.')
    expect(text).toContain('N.º de operación: op-7')

    const noNumber = composeTicketShareText({
      method: 'fiado',
      receivedCents: 0,
      totalCents: 300,
      operationNumber: null,
    })

    expect(noNumber).not.toContain('N.º de operación')
    expect(noNumber).toContain('Fiado: el cargo queda en la cuenta del cliente.')
  })
})
