/**
 * Los métodos de pago del CHECK de `sales.payment_method` (docs/DATABASE.md §4
 * `sales`; docs/DESIGN.md §2.1:161). Son EXACTAMENTE seis: tender único por
 * venta, sin split tender — una venta no se reparte entre dos medios
 * (docs/DATABASE.md §8 D9; ARCHITECTURE.md:163).
 */

export const TENDER_METHODS = ['cash', 'yape', 'plin', 'card', 'other', 'fiado'] as const

export type TenderMethod = (typeof TENDER_METHODS)[number]

/** Etiqueta visible de cada método (docs/DESIGN.md §2.1, en español neutro). */
export const TENDER_LABELS: Record<TenderMethod, string> = {
  cash: 'Efectivo',
  yape: 'Yape',
  plin: 'Plin',
  card: 'Tarjeta',
  other: 'Otro',
  fiado: 'Fiado',
}
