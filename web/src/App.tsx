import { ConnectivityBadge } from '@/components/ConnectivityBadge'
import { EmployeeAccessScreen } from '@/screens/EmployeeAccessScreen'

/**
 * Shell de la PWA. Su entrada es la pantalla de acceso del empleado
 * (`docs/DESIGN.md:104-105`) y su cabecera lleva el badge de conectividad en
 * vivo (`docs/DESIGN.md:127-130`; `docs/FRONTEND.md:101-103`).
 *
 * El badge vive en la cabecera y no sobre la operación: §1.1:130 pide que el
 * POS conmute a almacenamiento local «sin interrumpir ventas ni alertar con
 * ventanas bloqueantes», así que el estado de red se lee sin abrir nada.
 *
 * La cabecera es un `<header>` de primer nivel —fuera de `<main>`, para que sea
 * un landmark `banner` y no un `header` con scope de contenido— y el shell no
 * pide nada al servidor antes de autenticar: el tenant sale del token y de
 * ningún otro lado (`ARCHITECTURE.md:23`), así que no hay datos de tenant que
 * traer en el arranque. La venta, el carrito y el checkout son unidades
 * posteriores (docs/FRONTEND.md:80-106).
 */
export function App() {
  return (
    <div className="flex min-h-screen flex-col bg-surface-bg text-text-primary">
      <header className="flex items-center justify-between gap-2 border-b border-surface-border bg-surface-card px-4 py-3">
        <h1 className="text-heading-md font-bold">POS SaaS</h1>
        <ConnectivityBadge />
      </header>
      <main className="flex-1">
        <EmployeeAccessScreen />
      </main>
    </div>
  )
}
