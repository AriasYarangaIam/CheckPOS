/**
 * Frontera de dinero de la interfaz: el contrato guarda centavos enteros
 * (`BIGINT` con sufijo `*_cents`) y la pantalla muestra soles (`S/ 12.34`).
 *
 * Reglas que sí fija el contrato:
 * - La entrada es el centavo entero; nunca un flotante ni una cadena ya
 *   formateada (docs/DATABASE.md §2.3).
 * - La salida usa el símbolo `S/` seguido de un espacio, con dos decimales
 *   siempre visibles: `S/ 0.00`, `S/ 12.00`, `S/ 23.50` (docs/DESIGN.md §2.2,
 *   docs/FRONTEND.md:119).
 * - No se agrupan los miles: el contrato no define separador de miles y sus
 *   únicos ejemplos documentados son importes de dos dígitos.
 *
 * Lo que el contrato NO define: cómo se renderiza un importe negativo. El
 * contrato solo fija el signo de `amount_cents` como dato (cargo positivo, pago
 * negativo; docs/DATABASE.md:450), nunca su presentación. Anteponer el signo
 * aquí es un marcador provisional y no tiene test que lo respalde: la unidad de
 * trabajo que ponga movimientos firmados en pantalla debe fijar la regla.
 */

const CENTS_PER_SOL = 100

/**
 * Convierte centavos enteros al texto en soles que consume la interfaz.
 */
export function formatCentsToSoles(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const absolute = Math.abs(cents)
  const soles = Math.floor(absolute / CENTS_PER_SOL)
  const centavos = absolute % CENTS_PER_SOL

  return `${sign}S/ ${soles}.${String(centavos).padStart(2, '0')}`
}
