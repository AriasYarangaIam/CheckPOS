import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/utils'

/**
 * Numpad ergonómico de 56px (docs/DESIGN.md §1.2:143-146; docs/FRONTEND.md:41,
 * :43, :117-119). Lo reusa más de un módulo: la pantalla de acceso del empleado
 * lo usa como teclado de PIN (W2-FE-1) y la pantalla de venta como teclado de
 * monto manual (Módulo 1).
 *
 * El **display no vive acá**. El componente emite la tecla pulsada y cada
 * pantalla es dueña de su display: el acceso del empleado muestra cuatro
 * posiciones enmascaradas y la venta mostrará `S/ 0.00` con etiqueta
 * `MONTO MANUAL` (§1.2:144). Mezclar los dos roles en un componente obligaría a
 * que el teclado conociera el formato de la pantalla que lo hospeda.
 *
 * Contrato táctil: matriz 3x4 con filas `1-2-3` / `4-5-6` / `7-8-9` /
 * `00-0-Borrar`, teclas de 56px (`h-14`, por encima del mínimo táctil de 48px),
 * radio `rounded-xl` (§2.3:71) y la sombra `shadow-keypad` (§2.3:76). La tecla
 * `Borrar` va con `danger-action` (§2.1:54). La respuesta al toque es la del
 * sistema —`:active scale(0.98)` con `transition: transform 120ms ease-out`,
 * nunca `transition: all` (§6.2:302)— y no hay animación de entrada: el teclado
 * es una acción de latencia cero (§6.2:298-299) y §5.2:267 prohíbe las
 * animaciones gratuitas.
 *
 * Cada tecla es un `<button>` real, así que el teclado físico también opera el
 * numpad: foco con Tab y activación con Enter o Espacio, sin una simulación de
 * teclado propia (docs/FRONTEND.md:23; §6.2:305-311).
 */

/** La matriz completa, en orden de lectura. Es la autoridad de las 12 teclas. */
export const NUMPAD_KEYS = [
  '1',
  '2',
  '3',
  '4',
  '5',
  '6',
  '7',
  '8',
  '9',
  '00',
  '0',
  'Borrar',
] as const

export type NumpadKey = (typeof NUMPAD_KEYS)[number]

/** La única tecla que no aporta un dígito: borra el último dígito ingresado. */
export const NUMPAD_DELETE_KEY: NumpadKey = 'Borrar'

export interface ErgonomicNumpadProps {
  /** Se llama con la tecla pulsada. La pantalla decide qué hacer con ella. */
  readonly onKey: (key: NumpadKey) => void
  readonly disabled?: boolean
  readonly className?: string
}

export function ErgonomicNumpad({ onKey, disabled = false, className }: ErgonomicNumpadProps) {
  return (
    <div
      data-testid="numpad-grid"
      className={cn('grid grid-cols-3 gap-2', className)}
      role="group"
      aria-label="Teclado numérico"
    >
      {NUMPAD_KEYS.map((key) => (
        <Button
          key={key}
          variant={key === NUMPAD_DELETE_KEY ? 'danger' : 'ghost'}
          disabled={disabled}
          onClick={() => onKey(key)}
          className={cn(
            // La altura, el radio y la sombra son de la tecla, no del rol del
            // botón: valen igual para las de dígito y para `Borrar`.
            'h-14 rounded-xl shadow-keypad',
            key === NUMPAD_DELETE_KEY
              ? ''
              : 'border border-surface-border bg-surface-card text-heading-md text-text-primary',
          )}
        >
          {key}
        </Button>
      ))}
    </div>
  )
}
