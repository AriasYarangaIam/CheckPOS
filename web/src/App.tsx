/**
 * Shell mínimo de la PWA. Sprint 1 solo prueba que el runner tiene DOM y React
 * conectados; las pantallas de venta, el carrito y el checkout son unidades
 * posteriores (docs/FRONTEND.md:80-106).
 */
export function App() {
  return (
    <main className="min-h-screen bg-zinc-50 text-zinc-900">
      <div className="mx-auto flex max-w-3xl flex-col gap-2 p-6">
        <h1 className="text-2xl font-bold">POS SaaS</h1>
        <p className="text-sm text-zinc-500">
          Andamiaje de la PWA listo. Las pantallas de venta llegan en las próximas
          unidades de trabajo.
        </p>
      </div>
    </main>
  )
}
