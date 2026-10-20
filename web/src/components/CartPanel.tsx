import { useState } from 'react'

import {
  annulCartLine,
  cartTotalCents,
  changeCartLineQtyBy,
  removeCartLine,
  saleLineTotalCents,
  setCartLineUnitPrice,
} from '@/cart/cartStore'
import { useCartLines } from '@/hooks/useCartLines'
import { getPosDatabase, type CartRow, type PosDatabase } from '@/db/posDatabase'
import { Money } from '@/components/Money'
import { cn } from '@/lib/utils'

/**
 * Panel del carrito en curso (W4-FE-1): la lista de líneas con sus mutaciones
 * — cantidad por unidad, override de precio, anular y quitar — y el total con
 * el redondeo half-up por línea ya aplicado.
 *
 * El estado del carrito NO vive acá: es la tabla `cart` de IndexedDB y este
 * panel solo la lee en vivo (`useCartLines`) y despacha mutaciones. Un doble
 * toque en la grilla ya quedó coalescido en `addIdentifiedLine`; el panel no
 * repite ese trabajo.
 *
 * Anulación visible: una línea anulada permanece tachada con la marca
 * `Anulada` — el cajero conserva la evidencia del toque equivocado — y sale
 * de los importes: la venta nunca viaja con una línea anulada.
 */

/** El precio del override se tipea en soles y se aplica en centavos enteros. */
function solesToCentsEntry(entry: string): number | null {
  if (entry.trim() === '') return null

  const parsed = Number(entry)

  return Number.isFinite(parsed) && parsed >= 0 ? Math.round(parsed * 100) : null
}

interface CartLineRowProps {
  readonly line: CartRow
  readonly database: PosDatabase
}

function CartLineRow({ line, database }: CartLineRowProps) {
  const [priceEntry, setPriceEntry] = useState('')

  return (
    <li
      data-testid="cart-line"
      className="flex flex-col gap-1 rounded-xl border border-surface-border bg-surface-card p-2"
    >
      <div className="flex items-baseline justify-between gap-2">
        <span
          className={cn(
            'min-w-0 truncate text-body-md text-text-primary',
            line.voided ? 'line-through opacity-60' : '',
          )}
        >
          {line.name}
        </span>
        {line.voided ? (
          <span
            data-testid="cart-line-voided"
            className="text-caption-xs uppercase text-danger-action"
          >
            Anulada
          </span>
        ) : null}
      </div>

      <div className="flex items-center justify-between gap-2">
        {line.voided ? (
          <span className="text-body-md text-text-secondary">
            {line.qty} {line.uom}
          </span>
        ) : (
          <div className="flex items-center gap-1">
            <button
              type="button"
              aria-label={`Quitar una unidad de ${line.name}`}
              data-testid="cart-line-minus"
              onClick={() => void changeCartLineQtyBy(line.id, -1, database)}
              className="min-h-9 rounded-lg border border-surface-border bg-surface-subtle px-2 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              −
            </button>
            <span className="min-w-10 text-center font-mono tabular-nums text-body-md text-text-primary">
              {line.qty} {line.uom}
            </span>
            <button
              type="button"
              aria-label={`Agregar una unidad de ${line.name}`}
              data-testid="cart-line-plus"
              onClick={() => void changeCartLineQtyBy(line.id, 1, database)}
              className="min-h-9 rounded-lg border border-surface-border bg-surface-subtle px-2 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
            >
              +
            </button>
          </div>
        )}
        <Money
          cents={saleLineTotalCents(line)}
          className={cn('text-body-md text-text-primary', line.voided ? 'opacity-50' : '')}
        />
      </div>

      {line.voided ? null : (
        <div className="flex items-center gap-2">
          {/*
           * Override de precio (W4-FE-1): el tipeo es en soles; la mutación es
           * en centavos enteros y reemplaza al precio congelado por línea, sin
           * tocar al resto del carrito.
           */}
          <input
            type="number"
            step="0.01"
            min="0"
            aria-label={`Precio de ${line.name}`}
            data-testid="cart-line-price-entry"
            placeholder={(line.unit_price_cents / 100).toFixed(2)}
            value={priceEntry}
            onChange={(event) => setPriceEntry(event.target.value)}
            className="min-h-9 w-24 rounded-lg border border-surface-border bg-white px-2 font-mono tabular-nums text-body-sm text-text-primary"
          />
          <button
            type="button"
            data-testid="cart-line-price-apply"
            onClick={() => {
              const cents = solesToCentsEntry(priceEntry)

              if (cents !== null) void setCartLineUnitPrice(line.id, cents, database)

              setPriceEntry('')
            }}
            className="min-h-9 rounded-lg border border-surface-border bg-surface-subtle px-2 text-body-sm font-semibold text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            Aplicar precio
          </button>
          <button
            type="button"
            aria-label={`Anular ${line.name}`}
            data-testid="cart-line-annul"
            onClick={() => void annulCartLine(line.id, database)}
            className="min-h-9 rounded-lg px-2 text-body-sm font-semibold text-danger-action hover:bg-rose-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            Anular
          </button>
        </div>
      )}

      <button
        type="button"
        aria-label={`Quitar ${line.name} del carrito`}
        data-testid="cart-line-remove"
        onClick={() => void removeCartLine(line.id, database)}
        className="self-start min-h-9 rounded-lg px-2 text-body-sm text-text-secondary hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
      >
        Quitar
      </button>
    </li>
  )
}

export interface CartPanelProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

export function CartPanel({ database = getPosDatabase() }: CartPanelProps) {
  const lines = useCartLines(database)
  const total = cartTotalCents(lines)

  if (lines.length === 0) {
    return (
      <section
        aria-labelledby="cart-panel-title"
        className="flex flex-col gap-2"
      >
        <h3 className="text-heading-md font-bold" id="cart-panel-title">
          Carrito
        </h3>
        <p
          role="status"
          data-testid="cart-panel-empty"
          className="text-body-md text-text-secondary"
        >
          El carrito está vacío.
        </p>
      </section>
    )
  }

  return (
    <section aria-labelledby="cart-panel-title" className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="text-heading-md font-bold" id="cart-panel-title">
          Carrito
        </h3>
        <span
          data-testid="cart-total"
          className="font-mono tabular-nums text-heading-md text-text-primary"
        >
          <Money cents={total} />
        </span>
      </div>

      <ul aria-label="Líneas del carrito" className="flex flex-col gap-2">
        {lines.map((line) => (
          <CartLineRow key={line.id} line={line} database={database} />
        ))}
      </ul>
    </section>
  )
}
