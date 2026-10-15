# Business

> **Propietario de este documento.** `docs/BUSINESS.md` es la fuente de verdad del contenido comercial: viabilidad, planes y precios, estrategia Go-to-Market y horizonte de portafolio. No decide arquitectura.
>
> - Contratos técnicos: `ARCHITECTURE.md` (contrato de arquitectura) y `docs/DATABASE.md` (contrato de esquema, que modifica a `ARCHITECTURE.md`; léelo **como enmendado** por su Sección 8).
> - Definición de producto: `docs/PRODUCT.md`.
> - Secuenciación de entrega: `SPRINTS.md`.
> - Pitch heredado `sistema_pos_saas_bodegas_minimarkets.md`: **eliminado del repositorio**. Su contenido de negocio vive en este archivo; su contenido técnico vive en los contratos congelados. No fue commiteado, así que no existe en el historial de git.

## Propósito

Este documento fija la dimensión comercial del POS SaaS para micro-retail barrial: si el negocio es viable, cómo se cobra, cómo se adquiere al primer cliente y cuál es el orden estratégico de las fases de producto. Todo lo que aparece aquí es intención de negocio heredada del pitch original; nada de aquí introduce una decisión técnica. Cuando una necesidad comercial choca con un contrato congelado, gana el contrato y la necesidad se reescribe como horizonte futuro (ver *Horizonte de portafolio*).

## Contexto de mercado

El sector objetivo son bodegas, tiendas de abarrotes, minimarkets y tiendas de conveniencia con escasa alfabetización digital. Los sistemas POS y ERP tradicionales fracasan ahí porque imponen barreras severas: computadoras dedicadas, periféricos costosos, inventariado previo de miles de productos y capacitaciones engorrosas.

El detalle completo del diagnóstico (tabla de dolor → solución, cinco filas) vive en `docs/PRODUCT.md`, sección *Diagnóstico del sector y necesidades resueltas*. Aquí no se duplica.

## Viabilidad multidimensional

El pitch evalúa la viabilidad en tres ejes, con veredicto explícito en cada uno:

| Eje | Veredicto |
| --- | --- |
| Técnica | **Alta** |
| Operativa y de adopción | **Media-Alta** |
| Comercial y de monetización | **Alta** |

**Viabilidad técnica — Alta.** La tecnología web actual (PWA, *service workers*, IndexedDB, *web workers* y Web APIs de hardware) permite que un navegador moderno se comporte como un software de escritorio o una aplicación nativa de alto rendimiento. El consumo de memoria y CPU se mantiene mínimo desacoplando las tareas pesadas —en particular la decodificación de imagen— hacia *workers* dedicados.

**Viabilidad operativa y de adopción — Media-Alta.** Curva de aprendizaje plana: interfaz basada en iconos universales, botones de acción grandes (mínimo 48px para pulsación táctil con una sola mano) y flujos que no superan 3 pasos. Onboarding inmediato: el comerciante empieza a vender en menos de 2 minutos tras registrarse, sin necesidad de configurar parámetros fiscales ni importar archivos CSV. El veredicto es deliberadamente *Media-Alta* y no *Alta*: la adopción depende del comportamiento del usuario final en el mostrador, no solo de la plataforma.

**Viabilidad comercial y de monetización — Alta.** Los costos marginales por usuario son muy reducidos gracias a la arquitectura multi-tenant compartida. El modelo freemium o de suscripción de muy bajo costo reduce la barrera de prueba y desincentiva el regreso al cuaderno tradicional.

## Planes y precios

El modelo comercial es de **precios por niveles (tiered pricing)** con tres planes. Cada nivel hereda del anterior; la diferencia está en el volumen de catálogo, la profundidad del módulo de fiados y el acceso a periféricos y a la facturación local.

| | Plan Básico (Bodega Inicial) | Plan Estándar (Bodega Consolidada) | Plan Pro (Minimarket) |
| --- | --- | --- | --- |
| **Modelo de cobro** | Freemium / Micro-pago | Suscripción mensual | Suscripción escalar |
| **Venta por cámara móvil** | Sí | Sí | Sí |
| **Ticket en pantalla** | Sí | Sí | Sí |
| **Límite de productos propios** | Hasta 200 | Ilimitados | Ilimitados |
| **Módulo de Fiados** | Básico | Completo | Completo |
| **Catálogo maestro compartido** | — | Sí | Sí |
| **Reporte diario de caja** | Sí | Sí | Sí |
| **Reportes de utilidad neta** | — | Sí | Sí |
| **Respaldo cloud diario** | — | Sí | Sí |
| **Multiusuario y turnos de cajero** | — | — | Sí |
| **Soporte tiqueteras ESC/POS** | — | — | Sí |
| **Facturación electrónica local** | — | — | Sí |
| **Alertas de quiebre de stock** | — | — | Sí |
| **Módulo de compras / proveedores** | — | — | Sí |

Notas de alcance, para que este documento no se lea como un mapa de lo ya construido:

- El pitch **no fija cifras** para ningún nivel. Solo define el modelo de cobro de cada tier. Cualquier precio concreto es una decisión comercial posterior, no un dato heredado.
- El esquema ya reserva los tres identificadores de plan —`free`, `standard`, `pro`— en `tenants.plan`, pero el billing está explícitamente diferido en `docs/DATABASE.md`: no existen tablas de planes, suscripciones ni facturación.
- Las filas de capacidades de los niveles superiores (ESC/POS, facturación electrónica, multiusuario, compras) son **intención comercial de cartera futura**, no alcance actual. El alcance vigente está congelado en `SPRINTS.md`.

## Go-to-Market

Tres palancas de adquisición y crecimiento, en orden de prioridad:

1. **Adquisición en campo (grassroots / on-the-ground).** Visitas directas en conglomerados comerciales y zonas residenciales de alta densidad. El onboarding se realiza en el mostrador, en 5 minutos, en el propio teléfono del bodeguero.
2. **Alianzas B2B con distribuidores mayoristas.** Convenios con distribuidores de alimentos y bebidas para ofrecer el software a sus clientes detallistas, facilitar que el distribuidor reciba pedidos digitales consolidados y abrir un canal de pedido sugerido desde el POS.
3. **Efecto de red en el catálogo.** Cada nuevo producto o código de barra que una tienda registra y valida enriquece la base de datos global de productos, haciendo el sistema progresivamente más valioso para los siguientes usuarios.

La palanca 3 es la única cuyo mecanismo técnico es un epic diferido: el catálogo colaborativo de GTIN no está construido, y su construcción depende de la evidencia del piloto (ver *Horizonte de portafolio*).

## Horizonte de portafolio

Este horizonte es **estratégico, no una planificación**. No contiene fechas, duraciones ni conteo de meses, porque la secuencia de entrega y sus criterios de avance son propiedad de `SPRINTS.md` — que además prohíbe arrastrar epics al calendario antes de que exista evidencia del piloto de campo. El objetivo de esta sección es registrar *hacia dónde* va el producto, no *cuándo* llega.

### Horizonte vigente — Núcleo "Bodega Cero Hardware"

Es el alcance que se está construyendo ahora según `SPRINTS.md`. Incluye PWA Offline-First con IndexedDB (Dexie.js); escaneo por cámara con linterna y retroalimentación de sonido y vibración; registro de venta ultra-rápido con vuelto en pantalla; y módulo básico de Cuentas Corrientes ("Fiados"). La escalabilidad multi-tenant con PostgreSQL RLS, el motor de sincronización bidireccional con reintentos y el cuadre de caja multimétodo también pertenecen a este horizonte: no son una idea de negocio futura, ya están congelados en `ARCHITECTURE.md` y se entregan según `SPRINTS.md`. El alcance del piloto de campo está definido en el Sprint 7 de `SPRINTS.md`.

### Horizonte posterior — Expansión modular "Minimarket Ready"

Horizonte estratégico, explícitamente sin fecha: soporte para periféricos físicos (lectores USB/BT HID y tiqueteras térmicas), control multiusuario y gestión de turnos de personal, reportes de rotación de inventario y alertas de stock bajo, y el módulo de compras y proveedores. La entrada a este horizonte no la decide este documento: la decide la evidencia del piloto de campo, en la forma de una lista firmada de qué se corta y qué se construye después.

### Horizonte posterior — Ecosistema y facturación fiscal

Horizonte estratégico, explícitamente sin fecha: emisión de factura electrónica según la regulación local, catálogo global colaborativo de GTIN (crowdsourced, moderación, acotado por país) y conexión de pedidos sugeridos hacia distribuidores mayoristas de abarrotes, que es la parte de infraestructura de la segunda palanca de GTM.
