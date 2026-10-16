import { EmployeeAccessScreen } from '@/screens/EmployeeAccessScreen'

/**
 * Shell de la PWA. Su entrada es la pantalla de acceso del empleado
 * (`docs/DESIGN.md:104-105`), y el shell no pide nada al servidor antes de
 * autenticar: el tenant sale del token y de ningún otro lado
 * (`ARCHITECTURE.md:23`), así que no hay datos de tenant que traer en el
 * arranque. La venta, el carrito y el checkout son unidades posteriores
 * (docs/FRONTEND.md:80-106).
 */
export function App() {
  return (
    <main className="min-h-screen bg-surface-bg text-text-primary">
      <div className="mx-auto flex w-full max-w-md flex-col gap-4 p-4">
        <h1 className="text-heading-md font-bold">POS SaaS</h1>
        <EmployeeAccessScreen />
      </div>
    </main>
  )
}
