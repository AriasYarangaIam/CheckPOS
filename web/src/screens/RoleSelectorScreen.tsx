import { Shield, User } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/utils'

/**
 * Login-1: selector de rol (W8-FE-1; docs/FRONTEND.md:46; docs/DESIGN.md:451-492).
 *
 * La primera pantalla del rol Administrador no pide nada: dos tarjetas —Empleado y
 * Administrador— y el wordmark del producto. **Ningún dato del tenant antes de autenticar**: el
 * local sale del token y de ningún otro lado (`ARCHITECTURE.md:23`), y `pos_app` ni siquiera
 * puede leer el registro de tenants (`docs/DATABASE.md` F14).
 *
 * El teclado es el del contrato: `←`/`→` para elegir y `Enter` para continuar. La selección es
 * exclusiva —un solo rol, el que decide la puerta— y no hay valor tácito: sin rol elegido, el
 * botón de continuar está deshabilitado.
 */

/** Los dos roles del contrato. El de empleado entra por PIN; el de administrador, por credencial. */
export type Role = 'employee' | 'admin'

interface RoleCard {
  readonly id: Role
  readonly label: string
  readonly subline: string
  readonly icon: typeof User
}

const ROLE_CARDS: readonly RoleCard[] = [
  {
    id: 'employee',
    label: 'Empleado',
    subline: 'PIN de 4 dígitos, sin depender de red',
    icon: User,
  },
  {
    id: 'admin',
    label: 'Administrador',
    subline: 'Teléfono + contraseña maestra, acceso a métricas',
    icon: Shield,
  },
]

export interface RoleSelectorScreenProps {
  /** Rol elegido y confirmado. */
  readonly onSelect: (role: Role) => void
}

export function RoleSelectorScreen({ onSelect }: RoleSelectorScreenProps) {
  const [selected, setSelected] = useState<Role | null>(null)

  function continueWith(role: Role | null): void {
    if (role === null) return

    onSelect(role)
  }

  function handleKeyDown(event: React.KeyboardEvent<HTMLElement>): void {
    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault()
        setSelected('admin')
        return
      case 'ArrowLeft':
        event.preventDefault()
        setSelected('employee')
        return
      case 'Enter':
        event.preventDefault()
        continueWith(selected)
        return
      default:
        return
    }
  }

  return (
    <section
      aria-labelledby="role-selector-title"
      className="mx-auto flex w-full max-w-xl flex-col gap-4 p-4"
      data-testid="role-selector"
      onKeyDown={handleKeyDown}
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-heading-md font-bold" id="role-selector-title">
          ¿Con qué rol entras?
        </h2>
        <p className="text-body-sm text-text-secondary">
          Elige el rol de esta sesión. El wordmark y el estado de la conexión están en la cabecera.
        </p>
      </div>

      <div aria-label="Rol" className="grid grid-cols-1 gap-3 sm:grid-cols-2" role="group">
        {ROLE_CARDS.map((card) => {
          const Icon = card.icon
          const isSelected = selected === card.id

          return (
            <button
              key={card.id}
              type="button"
              aria-pressed={isSelected}
              data-testid={`role-card-${card.id}`}
              onClick={() => setSelected(card.id)}
              className={cn(
                'flex min-h-40 flex-col items-start justify-between gap-3 rounded-2xl border bg-surface-card p-4 text-left',
                'transition-transform duration-[120ms] ease-out active:scale-[0.98]',
                'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-hover',
                isSelected
                  ? 'border-accent-hover ring-2 ring-accent-primary/40'
                  : 'border-surface-border hover:bg-zinc-50',
              )}
            >
              <Icon aria-hidden="true" className={cn('size-6', isSelected ? 'text-accent-hover' : 'text-text-secondary')} />
              <span className="flex flex-col gap-1">
                <span className="text-heading-md font-bold text-text-primary">{card.label}</span>
                <span className="text-caption-xs uppercase text-text-secondary">{card.subline}</span>
              </span>
            </button>
          )
        })}
      </div>

      <p className="text-caption-xs uppercase text-text-muted">
        Usa ← → para elegir · Enter para continuar
      </p>

      <Button
        data-testid="role-continue"
        disabled={selected === null}
        onClick={() => continueWith(selected)}
        size="cta"
      >
        CONTINUAR
      </Button>
    </section>
  )
}
