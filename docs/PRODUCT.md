# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

React + Vite + Tailwind CSS + PWA (IndexedDB / Dexie.js para soporte Offline-First)

## Users

Comerciantes de micro-retail y comercio barrial tradicional (bodegueros, dueños de tiendas de abarrotes, minimarkets y tiendas de conveniencia) y sus empleados/cajeros.

## Product Purpose

Plataforma de Punto de Venta (POS) SaaS accesible y ultra-rápida que elimina la fricción tecnológica, permitiendo operar con $0 en hardware adicional (usando un smartphone y cámara) o escalar modularmente a estaciones completas de minimarket con periféricos USB/Bluetooth.

## Diagnóstico del sector y necesidades resueltas

Los sistemas tradicionales de POS y ERP fracasan en las bodegas de barrio porque imponen barreras severas: requieren computadoras dedicadas, periféricos costosos, inventariado previo de miles de productos y capacitaciones engorrosas. Cada uno de esos dolores tiene una contraparte concreta en el producto:

| Dolor del bodeguero tradicional | Respuesta del producto |
| :--- | :--- |
| **Pérdida de dinero por "fiados" desordenados:** se anota en libretas desgastadas, se pierden cuentas y no se recuerda quién debe ni cuánto. | **Módulo ágil de Cuentas Corrientes ("Fiados"):** registro de deudor en 2 clics, historial de abonos parciales y alertas visuales. |
| **Colapso en horas pico:** clientes impacientes; teclear códigos o buscar listas en Excel hace lenta la atención. | **Venta de mostrador ultra-veloz (< 3 segundos):** reconocimiento inmediato por cámara o lector, botones de un solo toque para productos sin código y cálculo automático del vuelto en pantalla completa. |
| **La barrera del inventariado inicial:** cargar 1,500 a 3,000 productos manualmente antes de empezar desmotiva al 90% de los comerciantes. | **Catálogo Maestro:** datos de producto precargados en el dispositivo; al escanear por primera vez el sistema autocompleta nombre, marca y foto, y el bodeguero solo digita su precio y stock. El catálogo global colaborativo es un epic diferido, no alcance actual. |
| **Descuadres al cerrar el día:** confusión entre efectivo en caja, pagos por billeteras digitales / QR (Yape, Plin, Mercado Pago) y tarjetas. | **Cuadre de caja multimétodo simplificado:** separación automática por método de cobro en tiempo real, con reporte de diferencias inmediato al cerrar turno. |
| **Cortes frecuentes de internet y fallas eléctricas:** pérdida del sistema en momentos críticos de venta. | **Arquitectura Offline-First:** el sistema nunca se detiene; opera localmente y sincroniza de forma transparente en cuanto detecta conectividad. |

## Positioning

POS SaaS enfocado en el sector micro-retail tradicional con arquitectura Offline-First garantizada, escaneo por cámara acelerado en GPU, registro de ventas en < 3 segundos, módulo de fiados/cuentas corrientes y catálogo maestro precargado con SKUs locales.

## Operating Context

Mostrador de atención rápida en bodegas y minimarkets durante horas pico, escaneo por cámara de smartphone o lector USB/BT, pantalla visual de vuelto sin necesidad de impresión obligatoria, ticket en pantalla con share sheet nativo y cuadre de caja multimétodo al cierre de turno.

## Capabilities and Constraints

- Interfaz Mobile-First adaptativa (optimizado para smartphones de 390px–430px de ancho y extensible a tablets/pantallas de escritorio).
- Arquitectura 100% Offline-First con IndexedDB (Dexie.js) y motor de sincronización en segundo plano.
- Captura de código de barras acelerada por hardware (`BarcodeDetector API` con fallback a `@zxing/library` en Web Worker).
- **Matriz dual de adaptabilidad:** el mismo SaaS ajusta interfaz y configuración según el perfil del negocio.

  | Dimensión | Perfil Bodega Tradicional (Micro) | Perfil Minimarket (Comercio Consolidado) |
  | :--- | :--- | :--- |
  | **Dispositivo principal** | Teléfono celular propio del comerciante o tablet Android económica. | Computadora all-in-one, laptop o tablet de mostrador dedicada. |
  | **Hardware requerido** | **$0 USD en hardware adicional.** Solo el dispositivo móvil existente. | Lector de código de barras 2D, tiquetera térmica de 80mm y cajón de dinero. |
  | **Modo de escaneo** | Cámara integrada con activación rápida de flash. | Pistola lectora de mostrador (manos libres) conectada por USB/BT. |
  | **Comprobantes** | Visual en pantalla; el envío digital usa el share sheet nativo del sistema (WhatsApp fuera del alcance MVP). | Ticket físico impreso en el acto y/o boleta/factura electrónica legal. |
  | **Productos a granel** | Cuadrícula con fotos de alta resolución y precios fijos o por unidad. | Integración con balanza digital (lectura de peso por puerto serie o etiquetas). |
  | **Conexión a red** | Conexión móvil intermitente (3G/4G) o Wi-Fi residencial. | Conexión de fibra óptica o Wi-Fi local estable. |

- Módulo de Fiados / Cuentas Corrientes con registro rápido, historial de abonos y alertas.
- Catálogo Maestro precargado en el dispositivo para acelerar el inventariado inicial. El catálogo global colaborativo es un epic diferido, no alcance actual.

## Brand Commitments

- Diseño ergonómico, táctil y de alta legibilidad.
- Regla de color 60-30-10 personalizada por negocio.
- Botones táctiles de gran tamaño (mínimo 48px - 56px de alto) diseñados para operarse con el pulgar o con una sola mano.

## Evidence on Hand

- `docs/BUSINESS.md`: Propietario del contenido comercial — viabilidad multidimensional, planes y precios, estrategia Go-to-Market y horizonte de portafolio.
- **Ausencia declarada:** el pitch original `sistema_pos_saas_bodegas_minimarkets.md` (2024) fue eliminado del repositorio. Su contenido de negocio vive en `docs/BUSINESS.md` y el técnico en los contratos congelados. No fue commiteado, así que no existe en el historial de git — no hay que buscar ni reconstruir ese archivo.
- `docs/PRODUCT.md` (este documento): Propietario de la definición de producto.
- `DESIGN.md`: Propuesta UI/UX final y especificación de diseño para Google Stitch.
- `ARCHITECTURE.md`, `docs/DATABASE.md`, `SPRINTS.md`: Documentos técnicos del proyecto — arquitectura, contrato de esquema y secuenciación de entrega.

## Product Principles

1. **Cero fricción tecnológica:** Experiencia táctil e intuitiva que elimina la jerga contable compleja.
2. **Cero dependencia obligatoria de hardware:** Operatividad total desde un smartphone de gama de entrada.
3. **Venta de mostrador ultra-veloz (< 3 segundos):** Flujos de trabajo acelerados para evitar colas.
4. **Confiabilidad Offline-First:** El sistema nunca se detiene ante la falta de conexión a internet o cortes eléctricos.

## Accessibility & Inclusion

- Botones de acción y teclado numérico de alto impacto táctil (mínimo 56px de alto).
- Feedback multisensorial al escanear: auditivo (`Web Audio API`), háptico (`navigator.vibrate`) y visual (delimitador de enfoque con cambio de color).
- Alto contraste visual para operarse bajo iluminación variable o vitrinas oscuras.
