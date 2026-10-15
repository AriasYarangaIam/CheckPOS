import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/*
 * Diálogo base. Radix aporta lo que un `<div>` superpuesto no tiene: foco
 * atrapado, `Esc` para cerrar, `aria-modal` y el bloqueo del scroll de fondo.
 * No se reimplementa a mano.
 */
export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

/**
 * Contenido del diálogo. El resorte amortiguado vive en la capa interna y no en
 * la caja de posicionamiento: la animación escribe `transform: scale(...)`, y
 * si compartiera el elemento con el `-translate-x-1/2` del centrado, el modal
 * saltaría a la esquina en cada apertura.
 *
 * El `transform-origin` se queda en el centro por defecto: un diálogo no cuelga
 * de un disparador, así que no aplica la regla de origen del popover
 * (docs/DESIGN.md §6.2:304; docs/FRONTEND.md:19 fija `scale(0.96) -> scale(1)`
 * con la curva `cubic-bezier(0.16, 1, 0.3, 1)`).
 */
export function DialogContent({ className, children, ...props }: ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-zinc-900/40" />
      <DialogPrimitive.Content
        className="fixed left-1/2 top-1/2 z-50 w-[calc(100vw-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2"
        {...props}
      >
        <div
          className={cn(
            'animate-modal-spring rounded-2xl border border-surface-border bg-surface-card p-5 shadow-lg',
            'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-primary',
            className,
          )}
        >
          {children}
          <DialogPrimitive.Close
            aria-label="Cerrar"
            className="absolute right-3 top-3 rounded-xl p-2 text-text-muted transition-colors duration-[120ms] ease-out hover:bg-surface-subtle hover:text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-primary"
          >
            <X aria-hidden={true} className="size-4" />
          </DialogPrimitive.Close>
        </div>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

export function DialogHeader({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex flex-col gap-1 pr-8', className)} {...props} />
}

export function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      className={cn('text-heading-md text-text-primary', className)}
      {...props}
    />
  )
}

export function DialogDescription({
  className,
  ...props
}: ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      className={cn('text-body-md text-text-secondary', className)}
      {...props}
    />
  )
}

export function DialogFooter({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('mt-4 flex justify-end gap-2', className)} {...props} />
}
