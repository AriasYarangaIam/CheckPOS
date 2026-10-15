# CheckPOS — Punto de venta SaaS para bodegas y minimarkets

CheckPOS es una plataforma de Punto de Venta (POS) pensada para el comercio barrial peruano: bodegas, minimarkets y tiendas de conveniencia. Registra ventas, cobros y fiados, y **sigue operando cuando se corta el internet**.

Su premisa es una sola: cero fricción tecnológica. El negocio no necesita comprar hardware dedicado, ni cargar su inventario completo antes de empezar, ni capacitarse para vender.

## Qué resuelve

| Problema del negocio | Cómo lo resuelve |
| --- | --- |
| **Fiados desordenados.** Se anotan en libretas que se pierden y nadie recuerda quién debe ni cuánto. | Cuentas corrientes con registro del deudor en dos toques, historial de abonos parciales y saldo siempre calculado a partir de los movimientos. |
| **Colapso en la hora punta.** Teclear códigos o buscar en listas hace lenta la atención. | Venta de mostrador en menos de tres segundos, por código de barras o con botones de un solo toque para los productos sin código. |
| **La barrera del inventario inicial.** Cargar cientos de productos a mano desanima antes de la primera venta. | Catálogo maestro precargado en el dispositivo: al escanear un producto por primera vez se autocompletan nombre, marca y foto, y el comerciante solo define precio y stock. |
| **Descuadres al cerrar el día.** Se mezclan el efectivo en caja, las billeteras digitales y las tarjetas. | Cuadre de caja por método de cobro, con la diferencia entre lo esperado y lo contado a la vista al cerrar el turno. |
| **Cortes de internet y fallas eléctricas.** El sistema se cae justo cuando más se vende. | Funcionamiento sin conexión: la venta se registra en el dispositivo y se sincroniza sola en cuanto vuelve la red, sin interrumpir la atención. |

## Para quién

Comerciantes de micro-retail y comercio barrial tradicional —bodegueros, dueños de tiendas de abarrotes, minimarkets y tiendas de conveniencia— y sus empleados o cajeros.

El mismo producto se adapta a dos perfiles de negocio:

- **Bodega tradicional:** funciona con el teléfono del propio comerciante, sin inversión en hardware adicional.
- **Minimarket o tienda consolidada:** estación de mostrador con lector de códigos, impresora térmica y cajón de dinero.

## Características principales

- Interfaz mobile-first, operable con una sola mano, con teclado numérico ergonómico y vuelto en pantalla completa.
- Escaneo por la cámara del teléfono, con la grilla de productos favoritos como camino principal y nunca como paso obligatorio.
- Operación sin conexión: la venta continúa y la sincronización ocurre en segundo plano.
- Cuentas corrientes (fiados) con registro rápido, abonos parciales y alertas de deuda.
- Cuadre de caja por método de cobro al cierre del turno.
- Instalable como aplicación web progresiva (PWA).

## Estado del proyecto

El MVP está en construcción. Su alcance y su contrato técnico están congelados, y la implementación avanza por sprints de una semana.

Lo que existe hoy en el repositorio es la base del sistema: la base de datos local en contenedor con las catorce tablas del contrato de esquema, el proceso del servidor con sus sondas de estado, y el andamiaje de la aplicación web con su entorno de pruebas.

La documentación de arquitectura y la puesta en marcha completa se agregarán a este documento cuando el incremento esté cerrado.

## Documentación de referencia

Los contratos y planes del proyecto viven en `docs/`:

| Documento | Contenido |
| --- | --- |
| `ARCHITECTURE.md` | Contrato de arquitectura del sistema |
| `docs/DATABASE.md` | Contrato de esquema de la base de datos |
| `docs/BACKEND.md` | Plan de implementación del servidor |
| `docs/FRONTEND.md` | Plan de implementación de la interfaz |
| `docs/DESIGN.md` | Sistema de diseño e interfaz |
| `docs/PRODUCT.md` | Definición de producto |
| `docs/BUSINESS.md` | Contenido comercial |
| `SPRINTS.md` | Plan de entrega por sprints |

## Puesta en marcha (provisional)

> Sección provisoria. Se completará cuando el incremento esté cerrado.

Con Docker y Go instalados, levantar la base local y aplicar las migraciones:

```powershell
docker compose up -d
go run ./cmd/migrate up
```

El puerto publicado es **5433** y no 5432, porque 5432 ya estaba ocupado en la máquina de desarrollo. Las credenciales del contenedor son valores de desarrollo local, no secretos.
