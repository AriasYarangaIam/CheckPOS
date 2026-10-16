import { useEffect, useRef, useState } from 'react'

import { attemptOfflineUnlock, loadDeviceSession, type UnlockVerdict } from '@/auth/deviceSession'
import { offlineSessionVerdict, type OfflineWindowVerdict } from '@/auth/offlineSession'
import { PIN_LENGTH } from '@/auth/pinVerifier'
import { ErgonomicNumpad, NUMPAD_DELETE_KEY, type NumpadKey } from '@/components/ErgonomicNumpad'
import { getPosDatabase, type DeviceSessionRow, type PosDatabase } from '@/db/posDatabase'

/**
 * Pantalla de acceso del empleado (docs/DESIGN.md:104-105; docs/FRONTEND.md:47;
 * W2-FE-1 de `SPRINTS.md` §7).
 *
 * El alcance congelado es 1 dispositivo y 1 cajero: no hay selector de cajero ni
 * de turno (§0.1:106 los declara post-MVP). El desbloqueo es un PIN de 4 dígitos
 * ligado al dispositivo, sin correo ni contraseñas largas y sin depender de red,
 * dentro de la ventana offline de 7+ días.
 *
 * **El display está acá y no en el numpad.** El teclado emite teclas; esta
 * pantalla las muestra como cuatro posiciones enmascaradas en `display-lg`
 * (§7.2:483 es la escala del display del numpad) y nunca renderiza los dígitos:
 * el PIN no tiene representación legible en el DOM, así que no se puede leer de
 * la pantalla ni del marcado.
 *
 * **Estados.** `checking` es el instante en que se lee IndexedDB; `idle` espera
 * el PIN; `pin-error` es un PIN incorrecto dentro de la ventana; `unlocked` es la
 * sesión habilitada; `refused` es la ventana cerrada o la ausencia de sesión
 * —el único estado que el contrato pide explícitamente que **no** bloquee el
 * intento en línea posterior, así que su aviso es no modal, sin trampa de foco y
 * con el teclado todavía operable.
 */

export interface EmployeeAccessScreenProps {
  /** Inyectable para las pruebas; en producción es la base local de la PWA. */
  readonly database?: PosDatabase
}

/** Patrón háptico del error (§5.2:266), solo donde hay motor de vibración. */
const ERROR_VIBRATION: readonly number[] = [30, 60, 30]

type RefusalReason = 'no-session' | 'window-expired' | 'window-unreadable'

/**
 * Motivo visible del rechazo. §1.1:130 pide que la conmutación offline no alerte
 * con ventanas bloqueantes; acá el rechazo es al revés —un aviso en la propia
 * pantalla, con el motivo— y el intento en línea queda disponible.
 */
const REFUSAL_REASON: Record<RefusalReason, string> = {
  'no-session':
    'Este dispositivo todavía no tiene una sesión offline. Ingresa con conexión para vincularlo.',
  'window-expired':
    'La sesión offline de este dispositivo venció. Ingresa con conexión para renovarla.',
  'window-unreadable':
    'La marca de la sesión offline no se puede leer. Ingresa con conexión para renovarla.',
}

/** Aviso común a los tres rechazos: el camino en línea sigue abierto. */
const REFUSAL_FOOTNOTE =
  'El desbloqueo con PIN no está disponible; el intento en línea sigue disponible.'

function vibrateError(): void {
  // El mostrador puede ser un equipo sin motor de vibración —y jsdom tampoco lo
  // implementa—: la ausencia se ignora en lugar de romper el intento.
  const vibrate = navigator.vibrate

  if (typeof vibrate === 'function') {
    vibrate.call(navigator, [...ERROR_VIBRATION])
  }
}

export function EmployeeAccessScreen({
  database = getPosDatabase(),
}: EmployeeAccessScreenProps) {
  // `undefined` es «todavía no leí la sesión»; `null` es «este teléfono no está
  // vinculado». La distinción importa: el primero deshabilita el teclado, el
  // segundo muestra el motivo del rechazo.
  const [session, setSession] = useState<DeviceSessionRow | null | undefined>(undefined)
  const [entry, setEntry] = useState('')
  const [verdict, setVerdict] = useState<UnlockVerdict | null>(null)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true

    const reload = (): void => {
      void loadDeviceSession(database)
        .then((row) => {
          if (!mounted.current) return

          setSession(row)
          // Un desbloqueo ya concedido no se revierte por releer la sesión; un
          // rechazo sí se reevalúa, para que una renovación en línea —lo que el
          // `online` anuncia— no deje el motivo colgado en pantalla.
          setVerdict((current) => (current?.status === 'unlocked' ? current : null))
        })
        .catch(() => {
          // Sin IndexedDB la sesión no se puede leer: se trata como ausente, que
          // es el lado que rechaza el desbloqueo offline.
          if (mounted.current) setSession(null)
        })
    }

    reload()
    window.addEventListener('online', reload)

    return () => {
      mounted.current = false
      window.removeEventListener('online', reload)
    }
  }, [database])

  /*
   * La ventana se evalúa sobre la sesión leída, no solo sobre el último
   * intento: una ventana cerrada se ve al abrir la pantalla, sin obligar al
   * cajero a tipear el PIN para enterarse. El instante sale del reloj del
   * dispositivo en cada render, que es lo que la regla pura recibe por
   * parámetro.
   */
  const windowState: OfflineWindowVerdict | 'checking' =
    session === undefined ? 'checking' : offlineSessionVerdict(session, new Date())

  const refusalReason: RefusalReason | null =
    verdict?.status === 'refused'
      ? verdict.reason
      : windowState === 'expired'
        ? 'window-expired'
        : windowState === 'unreadable'
          ? session === null
            ? 'no-session'
            : 'window-unreadable'
          : null

  const phase =
    verdict?.status === 'unlocked'
      ? 'unlocked'
      : verdict?.status === 'rejected'
        ? 'pin-error'
        : refusalReason !== null
          ? 'refused'
          : windowState === 'checking'
            ? 'checking'
            : 'idle'

  const filled = Math.min(entry.length, PIN_LENGTH)
  const dots = `${'●'.repeat(filled)}${'○'.repeat(PIN_LENGTH - filled)}`

  async function verify(pin: string): Promise<void> {
    const outcome = await attemptOfflineUnlock(pin, new Date(), database)

    if (!mounted.current) return

    if (outcome.status === 'rejected') vibrateError()

    // El PIN se suelta en cuanto se usó, sea cual sea el veredicto: no queda
    // retenido en el estado de React ni se puede releer de un render.
    setEntry('')
    setVerdict(outcome)
  }

  function press(key: NumpadKey): void {
    if (phase === 'unlocked' || phase === 'checking') return

    // `Borrar` retrocede un dígito; el resto aporta sus dígitos y la entrada se
    // corta en el largo del contrato, así que `00` sobre tres dígitos ingresa
    // uno solo en lugar de desbordar el PIN.
    const next =
      key === NUMPAD_DELETE_KEY ? entry.slice(0, -1) : `${entry}${key}`.slice(0, PIN_LENGTH)

    setEntry(next)

    if (next.length === PIN_LENGTH) {
      void verify(next)
    }
  }

  return (
    <section
      aria-labelledby="employee-access-title"
      className="mx-auto flex w-full max-w-md flex-col gap-4 p-4"
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="employee-access-title">
          Acceso del empleado
        </h2>
        <p className="text-body-sm text-text-secondary">
          PIN de 4 dígitos, sin depender de red
        </p>
      </div>

      <div className="flex flex-col gap-2 rounded-2xl border border-surface-border bg-surface-card p-4 shadow-xs">
        <div className="flex items-center justify-between gap-2">
          <span className="text-caption-xs uppercase text-text-secondary">PIN</span>
          {/*
           * El avance se anuncia con la cantidad, no con los dígitos: es lo que
           * un lector de pantalla necesita y lo único que no revela el PIN.
           */}
          <span aria-live="polite" className="sr-only" role="status">
            {filled} de {PIN_LENGTH} dígitos ingresados
          </span>
        </div>
        {/* Cuatro posiciones enmascaradas: nunca los dígitos (docs/DESIGN.md:105). */}
        <p
          aria-hidden="true"
          className="font-mono text-display-lg leading-none text-text-primary"
          data-testid="pin-display"
        >
          {dots}
        </p>
      </div>

      {phase === 'pin-error' ? (
        <p className="text-body-md text-danger-action">PIN incorrecto. Intenta de nuevo.</p>
      ) : null}

      {phase === 'unlocked' ? (
        <div className="flex flex-col gap-1 rounded-xl border border-surface-border bg-surface-card p-4">
          <p className="text-heading-md font-bold">Sesión desbloqueada</p>
          <p className="text-body-sm text-text-secondary">
            La venta de mostrador llega en la próxima unidad de trabajo.
          </p>
        </div>
      ) : null}

      {refusalReason === null ? null : (
        // Aviso en la pantalla, no una ventana: sin modal, sin trampa de foco y
        // con el numpad todavía operable, así que el intento en línea posterior
        // nunca queda bloqueado por este rechazo.
        <div className="flex flex-col gap-1 rounded-xl border border-surface-border bg-surface-card p-3">
          <p className="text-body-md text-text-primary">{REFUSAL_REASON[refusalReason]}</p>
          <p className="text-body-sm text-text-secondary">{REFUSAL_FOOTNOTE}</p>
        </div>
      )}

      <ErgonomicNumpad disabled={phase === 'unlocked' || phase === 'checking'} onKey={press} />
    </section>
  )
}
