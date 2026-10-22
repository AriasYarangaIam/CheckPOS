import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  AlertTriangle,
  Banknote,
  Clock,
  LayoutDashboard,
  Lock,
  Package,
  ShoppingBag,
  Users,
  X,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { attemptOfflineUnlock } from '@/auth/deviceSession'
import { PIN_LENGTH } from '@/auth/pinVerifier'
import { ErgonomicNumpad, NUMPAD_DELETE_KEY, type NumpadKey } from '@/components/ErgonomicNumpad'
import { DialogDescription, DialogTitle } from '@/components/ui/Dialog'
import { getPosDatabase, type PosDatabase } from '@/db/posDatabase'
import { cn } from '@/lib/utils'

/**
 * Navigation sheet (W5-FE-3; docs/DESIGN.md §3.1:182-192; docs/FRONTEND.md FASE
 * 6). Panel lateral de Radix —foco atrapado, `Esc` y `aria-modal` los aporta el
 * primitivo, no se reimplementan— con los destinos en el orden del documento.
 *
 * **Solo dos destinos tienen superficie en este sprint.** `Punto de Venta`
 * cierra el panel y `Gestión de Caja y Turnos` abre el arqueo; los otros cuatro
 * son de las Semanas 7 y 8 y se muestran como explícitamente NO disponibles
 * (`aria-disabled` + «Próximamente») en lugar de navegar a una pantalla que no
 * existe (decisión 6 del sprint).
 *
 * **El bloque de administración va separado en la base y tras el PIN.** El
 * único verificador que el contrato tiene hoy es el del dispositivo
 * (`attemptOfflineUnlock`, docs/DATABASE.md §4 `devices`); el «PIN maestro» del
 * documento es de la decisión D1 y todavía no existe, así que el bloque usa el
 * PIN del empleado y lo documenta en pantalla. El PIN se muestra enmascarado:
 * los dígitos no tienen representación legible en el DOM.
 *
 * **Cabecera.** El nombre del local todavía no vive en el dispositivo —sale de
 * la sesión en línea, Semana 8—, así que la cabecera muestra el producto y la
 * versión del artefacto; el panel lo dice sin inventar un nombre.
 */

export type NavigationSurface = 'sale' | 'cash-close'

/** Un destino del menú, en el orden del §3.1. */
export interface NavigationDestination {
  readonly id: NavigationSurface | 'accounts' | 'inventory' | 'waste' | 'attendance'
  readonly label: string
  readonly icon: typeof ShoppingBag
}

export const NAVIGATION_DESTINATIONS: readonly NavigationDestination[] = [
  { id: 'sale', label: 'Punto de Venta', icon: ShoppingBag },
  { id: 'cash-close', label: 'Gestión de Caja y Turnos', icon: Banknote },
  { id: 'accounts', label: 'Cuentas y Fiados', icon: Users },
  { id: 'inventory', label: 'Inventario / Stock Rápido', icon: Package },
  { id: 'waste', label: 'Mermas y Daños', icon: AlertTriangle },
  { id: 'attendance', label: 'Marcaje de Asistencia', icon: Clock },
]

/** La superficie que el destino abre, o `null` si es de un sprint posterior. */
function surfaceOf(id: NavigationDestination['id']): NavigationSurface | null {
  return id === 'sale' || id === 'cash-close' ? id : null
}

/** Motivo visible del rechazo del PIN, con la misma taxonomía del acceso. */
const REFUSAL_REASON = {
  'no-session': 'Este dispositivo todavía no tiene una sesión offline.',
  'window-expired': 'La sesión offline de este dispositivo venció.',
  'window-unreadable': 'La marca de la sesión offline no se puede leer.',
} as const

export interface NavigationSheetProps {
  readonly open: boolean
  readonly onOpenChange: (open: boolean) => void
  /** Superficie que el shell muestra ahora: marca el destino activo. */
  readonly activeSurface?: NavigationSurface
  readonly onNavigate: (surface: NavigationSurface) => void
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

type AdminPhase = 'locked' | 'prompt' | 'unlocked'

export function NavigationSheet({
  open,
  onOpenChange,
  activeSurface = 'sale',
  onNavigate,
  database = getPosDatabase(),
}: NavigationSheetProps) {
  const [adminPhase, setAdminPhase] = useState<AdminPhase>('locked')
  const [adminError, setAdminError] = useState<string | null>(null)
  const [entry, setEntry] = useState('')
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true

    return () => {
      mounted.current = false
    }
  }, [])

  useEffect(() => {
    // Cerrar el panel vuelve a bloquear el bloque de administración: el
    // desbloqueo vale para la visita, no para el resto de la jornada.
    if (open) return

    setAdminPhase('locked')
    setAdminError(null)
    setEntry('')
  }, [open])

  const filled = Math.min(entry.length, PIN_LENGTH)
  const dots = `${'●'.repeat(filled)}${'○'.repeat(PIN_LENGTH - filled)}`

  function navigate(surface: NavigationSurface): void {
    onOpenChange(false)
    onNavigate(surface)
  }

  async function verify(pin: string): Promise<void> {
    const outcome = await attemptOfflineUnlock(pin, new Date(), database)

    if (!mounted.current) return

    // El PIN se suelta en cuanto se usó, sea cual sea el veredicto.
    setEntry('')

    if (outcome.status === 'unlocked') {
      setAdminPhase('unlocked')
      setAdminError(null)

      return
    }

    setAdminPhase('prompt')
    setAdminError(
      outcome.status === 'rejected'
        ? 'PIN incorrecto. Intenta de nuevo.'
        : REFUSAL_REASON[outcome.reason],
    )
  }

  function press(key: NumpadKey): void {
    if (adminPhase !== 'prompt') return

    const next =
      key === NUMPAD_DELETE_KEY ? entry.slice(0, -1) : `${entry}${key}`.slice(0, PIN_LENGTH)

    setEntry(next)

    if (next.length === PIN_LENGTH) {
      void verify(next)
    }
  }

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-zinc-900/40" />
        <DialogPrimitive.Content
          className={cn(
            // Panel lateral: ocupa el alto completo y desliza desde su borde.
            'fixed inset-y-0 left-0 z-50 flex w-[86vw] max-w-xs flex-col',
            'animate-sheet-in border-r border-surface-border bg-surface-card shadow-lg',
            'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
          )}
          data-testid="navigation-sheet"
        >
          <div className="flex flex-col gap-1 border-b border-surface-border p-4 pr-12">
            <DialogTitle>POS SaaS</DialogTitle>
            <span
              className="font-mono text-caption-xs text-text-secondary"
              data-testid="nav-version"
            >
              v{__APP_VERSION__}
            </span>
            <DialogDescription className="text-body-sm">
              Menú operativo de este dispositivo.
            </DialogDescription>
          </div>

          <nav aria-label="Destinos" className="flex flex-1 flex-col gap-1 overflow-y-auto p-3">
            {NAVIGATION_DESTINATIONS.map((destination) => {
              const surface = surfaceOf(destination.id)
              const active = surface !== null && surface === activeSurface
              const Icon = destination.icon

              return (
                <button
                  key={destination.id}
                  type="button"
                  aria-current={active ? 'page' : undefined}
                  aria-disabled={surface === null ? 'true' : undefined}
                  data-destination={destination.id}
                  data-label={destination.label}
                  data-testid={`nav-destination-${destination.id}`}
                  onClick={() => {
                    // Un destino sin superficie no navega a ninguna parte: no
                    // hay a dónde ir todavía (decisión 6 del sprint).
                    if (surface !== null) navigate(surface)
                  }}
                  className={cn(
                    // §3.1:185 — enlaces táctiles de 48px de alto como mínimo.
                    'flex min-h-12 items-center gap-3 rounded-xl px-3 text-left',
                    'transition-colors duration-[120ms] ease-out',
                    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                    surface === null
                      ? 'cursor-not-allowed text-text-secondary opacity-70'
                      : active
                        ? 'border border-accent-hover bg-accent-primary/10 text-text-primary'
                        : 'border border-transparent text-text-primary hover:bg-zinc-100',
                  )}
                >
                  <Icon aria-hidden="true" className="size-5 shrink-0" />
                  <span className="text-body-md font-semibold">{destination.label}</span>
                  {surface === null ? (
                    <span className="ml-auto text-caption-xs uppercase text-text-muted">
                      Próximamente
                    </span>
                  ) : null}
                </button>
              )
            })}
          </nav>

          {/*
           * Bloque de administración: separado visualmente en la base y
           * bloqueado hasta que el PIN del dispositivo lo habilita (§3.1:192).
           */}
          <div className="border-t border-surface-border p-3">
            {adminPhase === 'locked' ? (
              <button
                type="button"
                data-testid="nav-admin-lock"
                onClick={() => {
                  setAdminPhase('prompt')
                  setAdminError(null)
                }}
                className={cn(
                  'flex min-h-12 w-full items-center gap-3 rounded-xl border border-surface-border px-3 text-left',
                  'transition-colors duration-[120ms] ease-out hover:bg-zinc-100',
                  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                )}
              >
                <Lock aria-hidden="true" className="size-4 shrink-0 text-text-secondary" />
                <span className="flex flex-col">
                  <span className="text-body-md font-semibold text-text-primary">
                    Administración
                  </span>
                  <span className="text-caption-xs uppercase text-text-secondary">
                    Protegido por PIN
                  </span>
                </span>
              </button>
            ) : null}

            {adminPhase === 'prompt' ? (
              <div className="flex flex-col gap-2" data-testid="nav-admin-prompt">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-caption-xs uppercase text-text-secondary">
                    PIN de administrador
                  </span>
                  {/* El avance se anuncia con la cantidad, nunca con los dígitos. */}
                  <span aria-live="polite" className="sr-only" role="status">
                    {filled} de {PIN_LENGTH} dígitos ingresados
                  </span>
                </div>
                <p
                  aria-hidden="true"
                  className="font-mono text-heading-md leading-none text-text-primary"
                  data-testid="nav-admin-pin-display"
                >
                  {dots}
                </p>
                {adminError === null ? null : (
                  <p className="text-body-sm text-danger-action" data-testid="nav-admin-error">
                    {adminError}
                  </p>
                )}
                {/* El único verificador del contrato es el del dispositivo. */}
                <ErgonomicNumpad onKey={press} />
              </div>
            ) : null}

            {adminPhase === 'unlocked' ? (
              <div className="flex flex-col gap-2">
                <p className="text-body-sm text-text-secondary" role="status">
                  Acceso de administrador desbloqueado en este dispositivo.
                </p>
                <button
                  type="button"
                  aria-disabled="true"
                  data-testid="nav-admin-dashboard"
                  className="flex min-h-12 w-full cursor-not-allowed items-center gap-3 rounded-xl border border-surface-border px-3 text-left opacity-70"
                >
                  <LayoutDashboard aria-hidden="true" className="size-5 shrink-0" />
                  <span className="text-body-md font-semibold text-text-primary">
                    Dashboard de administrador
                  </span>
                  <span className="ml-auto text-caption-xs uppercase text-text-muted">
                    Próximamente
                  </span>
                </button>
              </div>
            ) : null}
          </div>

          <DialogPrimitive.Close
            aria-label="Cerrar menú"
            className="absolute right-3 top-3 rounded-xl p-2 text-text-muted transition-colors duration-[120ms] ease-out hover:bg-surface-subtle hover:text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover"
          >
            <X aria-hidden="true" className="size-4" />
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
