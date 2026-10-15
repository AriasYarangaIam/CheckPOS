# SPRINTS — Plan de entrega por semanas (POS SaaS para bodegas y minimarkets)

`SPRINTS.md` es el plan de entrega del MVP. No fija arquitectura ni esquema: la autoridad de arquitectura es `ARCHITECTURE.md`, y la autoridad de esquema es `docs/DATABASE.md`, que se lee **como enmienda** de `ARCHITECTURE.md` por su Sección 8 (`ARCHITECTURE.md:5`; `docs/DATABASE.md` §1, §8). Si este documento discrepa de cualquiera de los dos, gana el contrato.

> **Nota de idioma.** En este repositorio los **contratos** técnicos están en inglés (`ARCHITECTURE.md`, `docs/DATABASE.md`) y los **planes** están en español (`docs/PRODUCT.md`, `docs/BUSINESS.md`, `docs/DESIGN.md`, `docs/FRONTEND.md`, `docs/BACKEND.md`). `SPRINTS.md` es un plan, así que pertenece al conjunto en español: esta versión se escribe en español neutral profesional y reemplaza la versión anterior en inglés. Se conservan en inglés solo identificadores, código, SQL, rutas HTTP y nombres de tablas, columnas y literales (`payment_method`, `FORCE RLS`, `POST /sales`, `SET LOCAL app.current_tenant_id`).

## 1. Cómo leer este plan

1. Se toma el bloque de semana siguiente, en orden. La dependencia real corre **Base de Datos → Backend → Frontend**, y el Frontend lleva una vía paralela que no se bloquea con el backend (§2).
2. En cada tarea: primero el test que falla, después la implementación más pequeña que lo hace pasar, después el refactor. El test viaja en la misma unidad de trabajo que el comportamiento.
3. Una tarea está Done solo cuando su criterio de aceptación es **demostrablemente** cierto, no cuando existe el código (`docs/BACKEND.md` §9.1).

**Autoridad y alcance.** El orden de módulos es Identity → Catalog → Venta de mostrador → Cierre de caja → Sync → Fiado → Piloto. `ARCHITECTURE.md` manda sobre este documento; `docs/DATABASE.md` se lee como su enmienda (§8). `docs/BACKEND.md` secuencia la capa servidor y conserva los IDs de tarea previos; `docs/FRONTEND.md` es el plan maestro de cliente.

**Convenciones de cita.** Cada número, duración o presupuesto tomado de una fuente lleva cita inline en el estilo del corpus (`ARCHITECTURE.md:111-116`, `docs/DATABASE.md` §7.9, `docs/BACKEND.md` §10, `docs/FRONTEND.md:45-53`). Todo lo que se propone más allá de las fuentes se marca `(propuesta)`. Este documento no se cita a sí mismo por número de línea.

**Convención de IDs.** Cada tarea lleva un ID primario `W<semana>-<capa>-<n>` (por ejemplo `W2-DB-1`, `W2-BE-1`, `W2-FE-1`; capas: `DB`, `BE`, `FE`) y una columna **`ID previo`** con el ID heredado del plan anterior cuando la tarea desciende de él. Esa columna no es decorativa: `docs/BACKEND.md` §10, §11.4 y §12 citan los IDs del grupo `S0-*` … `S7-*`, y el historial de commits los usa. El mapeo completo está en §9.

---

## 2. Orden por capas y bloques de sprint (una semana por sprint)

El plan se ordena por capa dentro de cada sprint: **Base de Datos → Backend → Frontend**.

**Fundamento `(propuesta)`.** El contrato de datos va primero porque es el que hace posible o imposible todo lo demás. El esquema fija el aislamiento por tenant (`FORCE RLS` + `SET LOCAL` por transacción), el particionado mensual del ledger y el protocolo de idempotencia *antes* de que exista una API correcta (`ARCHITECTURE.md:19`, `:173`, `:177`; `docs/DATABASE.md` §5, §6, §7.1). Después viene la capa que **hace cumplir** ese contrato: el servidor posee stock, dinero y saldos de fiado, y el teléfono es solo caché y cola de eventos, nunca el libro de registro (`ARCHITECTURE.md:3`). Al final viene la capa que lo **consume**: si la UI se construye antes que el contrato que la sostiene, se construye contra supuestos y luego se rehace.

**Vía paralela del Frontend `(propuesta)`.** El Frontend no espera al backend para arrancar. Su andamiaje — shell de la PWA, tokens de diseño, persistencia del carrito y cola de eventos en IndexedDB — empieza en el Sprint 1 (Semana 1) y avanza en paralelo (`docs/FRONTEND.md:78-106`). Lo que sí se espera es el **slice de UI de cada módulo**: la UI de un módulo arranca recién cuando el slice de backend de ese mismo módulo está entregado. El estado local vive en IndexedDB como subconjunto de catálogo más cola de eventos pendientes, nunca como libro de registro (`ARCHITECTURE.md:22`).

| Sprint (Semana) | Foco | Capas | Origen en el plan anterior |
| :--- | :--- | :--- | :--- |
| 1 (Semana 1) | Fundaciones y congelamiento | DB + BE + FE (shell) | Sprint 0 completo + los runners del Test Plan |
| 2 (Semana 2) | Identidad y aislamiento de tenant | DB → BE → FE | Sprint 1 |
| 3 (Semana 3) | Catálogo de tenant (sin universo global) | DB → BE → FE | Sprint 2 |
| 4 (Semana 4) | Venta de mostrador | DB → BE → FE | Sprint 3 |
| 5 (Semana 5) | Cierre de caja y durabilidad | DB → BE → FE | Sprint 4 |
| 6 (Semana 6) | Sincronización offline (el núcleo) | DB → BE → FE | Sprint 5 |
| 7 (Semana 7) | Fiados | DB → BE → FE | Sprint 6 |
| 8 (Semana 8) | Dashboard Admin y endurecimiento | FE + BE + DB | Nuevo (absorbe el delta D12) |
| 9 (Semana 9) | Piloto de campo — alta, instrumentación y arranque | producto completo | Sprint 7 |
| 10 (Semana 10) | Piloto de campo — cierre y kill list | producto completo | Sprint 7 |

**Duración total.** Diez bloques, un sprint por semana: del Sprint 1 (Semana 1) al Sprint 10 (Semana 10), aproximadamente diez semanas. Las duraciones del plan anterior (Sprint 0 de 3 días; Sprints 1, 2, 3, 4 y 6 de 1 semana; Sprint 5 de 1–2 semanas; Sprint 7 de 2 semanas) se conservan solo como origen: aquí cada sprint es exactamente una semana, y el piloto de dos semanas se reparte entre el Sprint 9 (Semana 9) y el Sprint 10 (Semana 10). El reparto del Sprint 0 de 3 días dentro del Sprint 1 (Semana 1) es `(propuesta)`: la semana absorbe el congelamiento y le suma el andamiaje de las tres capas. La escala del piloto es de 10 bodegas (`docs/DATABASE.md` §7.2, que usa 10 bodegas como escala de piloto).

---

## 3. Roles

| Rol | Responsabilidad | Titular |
| :--- | :--- | :--- |
| Product Owner | Ordena el backlog, acepta o rechaza el incremento semanal contra los criterios de aceptación, firma la lista de corte del piloto | TBD |
| Scrum Master | Corre las ceremonias, desbloquea, protege el congelamiento de alcance | TBD |
| Dev Team | Implementa test-first, tiene un Owner por fila de capa dentro de la semana, demuestra el entregable en la revisión por capa | TBD |

> Cada fila de tarea de §7 recibe un Owner y una estimación antes de empezar. La estimación no se reinterpreta entre documentos: `docs/BACKEND.md` §10 declara explícitamente que no reproduce la columna de estimaciones del plan anterior porque esa unidad no está definida.

---

## 4. Ceremonias (cadencia semanal, con revisión por capa)

- [ ] **Planificación semanal (inicio de semana).** Confirmar el objetivo del bloque, tomar del Product Backlog las tareas de cada capa, asignar Owner y estimación, y acordar el Definition of Done de la semana.
- [ ] **Diaria (15 min).** Qué se movió, qué está bloqueado, quién necesita ayuda.
- [ ] **Revisión por capa (durante la semana).** La capa que entrega primero se revisa primero: Base de Datos, después Backend, después Frontend. Una capa no se acepta antes que la capa que la sostiene: la capa de datos fija el contrato, el backend lo hace cumplir, el frontend lo consume. La revisión de UI de un módulo no se abre hasta que el slice de backend del módulo esté entregado.
- [ ] **Demostración semanal (cierre de semana).** Se demuestra el entregable demostrable de cada capa en vivo; el Product Owner acepta o devuelve.
- [ ] **Retrospectiva semanal.** Un keep, un fix y una acción que entra en la planificación siguiente.

---

## 5. Flujo Backlog → Sprint Backlog

1. El Product Backlog está ordenado por dependencia: Identity primero, piloto al final, con el orden Base de Datos → Backend → Frontend dentro de cada módulo.
2. La planificación semanal selecciona un slice vertical por semana y lo descompone por capa (§7), no un módulo completo de una sola vez.
3. Cada fila seleccionada recibe un Owner y una estimación antes de empezar.
4. La UI de un módulo no se toma del backlog hasta que su slice de backend esté Done.
5. Los ítems sin terminar vuelven al Product Backlog; no ruedan en silencio a la semana siguiente.
6. Los epics posteriores quedan estacionados hasta que los Sprints 9 y 10 (Semanas 9 y 10) produzcan evidencia, en la forma de una lista firmada de qué se corta y qué se construye después (`docs/BUSINESS.md:75`, `:83`).

---

## 6. Congelamiento de alcance

**Trabajo del MVP:** un solo cajero, en un solo teléfono, vende y registra fiados sin perder dinero cuando la red se cae (`docs/BACKEND.md` §1).

| Dentro del MVP | Fuera del MVP |
| :--- | :--- |
| 1 dispositivo, 1 cajero, PIN de 4 dígitos y sesión ligada al dispositivo | Multicajero y turnos (`ARCHITECTURE.md:162`) |
| Catálogo propio del tenant: nombre, GTIN opcional, precio en centavos, UoM, stock proyectado, URL de foto (`ARCHITECTURE.md:36`) | Escrituras de tenants al catálogo global; catálogo colaborativo (`ARCHITECTURE.md:156`) |
| Grilla de favoritos como happy path; cámara como best-effort no bloqueante (`ARCHITECTURE.md:37`; `docs/DESIGN.md:135-141`) | Cámara como camino principal de venta |
| Venta, vuelto y cierre de caja; **un solo tender por venta**, seis valores (`ARCHITECTURE.md:39`, `:131`; `docs/DATABASE.md` §4 `sales`) | Pago repartido entre dos medios en una misma venta (`ARCHITECTURE.md:163`) |
| Reconciliación automática de Yape/Plin | Reconciliación automática de billeteras: el tender lo elige el cajero (`ARCHITECTURE.md:159`) |
| Servidor autoritativo; IndexedDB es caché y cola de eventos (`ARCHITECTURE.md:3`, `:22`) | IndexedDB como libro de registro |
| Sync idempotente con retroceso y **reconciliación silenciosa** (`ARCHITECTURE.md:24`, `:111-116`) | Interfaz de conflicto: el POS **nunca** muestra un error de sincronización al cajero (`ARCHITECTURE.md:178`; `docs/DATABASE.md` §7.9) |
| Ticket en pantalla; share sheet nativo opcional (`docs/DESIGN.md:176`) | WhatsApp Business y automatización `wa.me` (`ARCHITECTURE.md:158`) |
| Campos internos de comprobante no fiscal reservados en `sales` (`docs/DATABASE.md` §4 `sales`) | SUNAT y facturación electrónica (`ARCHITECTURE.md:160`) |
| PWA, Android-first (`ARCHITECTURE.md:17`) | Tauri, WebUSB, balanzas, ESC/POS (`ARCHITECTURE.md:27`, `:161`) |
| Catálogo global curado por plataforma, lectura-solo para el tenant vía API (`ARCHITECTURE.md:36`, `:89`) | Un volcado global de SKUs hacia los dispositivos (`ARCHITECTURE.md:89`) |

**Cómo se lee la tabla.** El catálogo global existe en el contrato y es de solo lectura para el tenant; lo que queda fuera es la escritura por parte del tenant y la distribución masiva hacia los teléfonos. `tenants.plan` (`free`, `standard`, `pro`) es un campo informativo: no hay tablas de planes, suscripciones ni facturación (`docs/DATABASE.md` §8 D7; `docs/BUSINESS.md:60`).

**Stack congelado.** PWA (React + Vite) · Go API, un solo proceso y REST · PostgreSQL con RLS · Redis como caché y cola únicamente · object storage para fotos · PostgreSQL 15 como mínimo (`ARCHITECTURE.md:17-21`; `docs/BACKEND.md` §1.3; `docs/DATABASE.md` §2.2). El contrato de módulos vive en `ARCHITECTURE.md:33-43`.

---

## 7. Plan por sprints (una semana por sprint)

Cada bloque declara su objetivo, su ventana, su alcance por capa con entregable demostrable, sus criterios de aceptación, sus dependencias de entrada y su Definition of Done expresado como un hecho demostrable.

### Sprint 1 (Semana 1) - Fundaciones y congelamiento

**Objetivo.** Dejar de construir desde un pitch. Congelar el contrato de datos, montar las tres capas y probar que la verificación existe.

**Duración y ventana.** Una semana; absorbe el Sprint 0 de 3 días del plan anterior y le suma el andamiaje de las tres capas `(propuesta)`.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W1-DB-0 | DB | Base local y runner de migraciones: Postgres en `docker compose`, runner versionado en el repositorio y el comando que aplica el catálogo contra una base limpia `(propuesta; tarea no nombrada por el plan previo)` | El comando de migración corre contra una base vacía y termina sin error; la base queda accesible para los tests de integración | — |
| W1-DB-1 | DB | Migración inicial con las **14 tablas** del contrato en sus 7 grupos de módulos, con las convenciones globales: UUIDv7 generado en la aplicación, dinero `BIGINT` en centavos, cantidades `NUMERIC(10,3)`, `TIMESTAMPTZ`, FKs `ON DELETE RESTRICT` e índices que empiezan por `tenant_id` | Esquema migrado que coincide tabla por tabla con la propiedad de datos de `ARCHITECTURE.md:144-152` y con `docs/DATABASE.md` §4 | S0-2 |
| W1-DB-2 | DB | Particionado mensual de `sales` sobre `sold_on` con PK `(sold_on, sale_id)` más la partición `sales_default` como red de seguridad | Una fila con `sold_on` fuera de rango aterriza en `sales_default` y se puede consultar; la PK compuesta es la clave de partición | — |
| W1-DB-3 | DB | Restricciones vinculantes de la primera migración: los dos `CHECK` de `payment_method` con seis valores, el par-nulidad y el vínculo de `kind` en `sales`, la unicidad parcial de `products` por nombre y GTIN, y `UNIQUE (tenant_id, closed_on)` en `cash_closes` | Una migración que intente crear una tabla `tenders` o `sync_events` falla la revisión: ninguna de las dos es tabla en el contrato | — |
| W1-BE-1 | BE | Instalar y probar el runner `go test ./...`, y montar el proceso único Go con su layout de paquetes | El runner existe en disco, corre y pasa en un commit base | — |
| W1-BE-2 | BE | Glosario de dominio de una página: tenant, device, cashier, product, GTIN, sale, tender, fiado, sync event, cash close, merma (`docs/BACKEND.md` §3.1) | Cada término tiene un solo nombre y un solo significado, revisado contra `ARCHITECTURE.md` | S0-1 |
| W1-BE-3 | BE | Secuencia del protocolo offline: cierre offline → reintento → POST duplicado → ack, con la rama de reconciliación silenciosa del lado servidor | El diagrama cubre la reutilización de la clave de idempotencia, el manejo de `409`/`422` y la regla de que el POS nunca ve un error de sincronización | S0-3 |
| W1-BE-4 | BE | Apuntes de amenaza: tenant solo por JWT, aislamiento por RLS, teléfono perdido o robado y XSS por datos de catálogo (`docs/BACKEND.md` §5.7) | Los apuntes declaran `FORCE RLS` más `SET LOCAL app.current_tenant_id` por transacción y la expiración de PIN y sesión offline | S0-4 |
| W1-FE-1 | FE | Instalar y probar el runner `vitest`, y montar el scaffold de la PWA: Vite, React, TypeScript estricto, Tailwind y alias de importación (`docs/FRONTEND.md:81-84`) | El runner existe en disco, corre y pasa en un commit base | — |
| W1-FE-2 | FE | Sistema de tokens 60-30-10 y tipografía tabular (`docs/DESIGN.md` §2.1, §2.2) | Un importe en soles renderiza en `JetBrains Mono` con `tnum` y no produce salto de layout | — |
| W1-FE-3 | FE | Primitivas base: `Button` con `:active { transform: scale(0.98) }`, `Card`, `Badge`, `Input` y `Dialog` con la curva de resorte | Los primitivos existen y el modal abre con resorte amortiguado, sin `transition: all` | — |
| W1-FE-4 | FE | Esquema de IndexedDB con Dexie: `products`, `cart` e `idempotency_queue` (`docs/FRONTEND.md:96-100`) | El carrito y la cola persisten entre recargas; ninguna tabla local guarda el catálogo global | — |

**Criterios de aceptación**

- El esquema migrado tiene exactamente **14 tablas** en 7 grupos de módulos (`ARCHITECTURE.md:142`; `docs/DATABASE.md` §2.1, §3).
- `tenders` y `sync_events` **no existen como tablas**: el tender es la columna `sales.payment_method` y el evento de sincronización vive en `idempotency_keys` (`docs/DATABASE.md` §8 enmiendas 1 y 5, y `:15`).
- Toda columna de dinero es `BIGINT` con sufijo `*_cents`; no hay `NUMERIC` ni punto flotante para dinero (`docs/DATABASE.md` §2.3).
- JSONB aparece en exactamente dos columnas: `products.attributes` y `global_products.attributes` (`ARCHITECTURE.md:175`; `docs/DATABASE.md` §2.9).
- `sales` está particionada por mes sobre `sold_on` y su PK es `(sold_on, sale_id)` (`ARCHITECTURE.md:177`; `docs/DATABASE.md` §6.1).
- `go test ./...` y `vitest` existen, corren y pasan; antes de eso no hay verificación posible.
- Glosario, secuencia del protocolo y apuntes de amenaza revisados; sin código de negocio.

**Dependencias de entrada.** Ninguna. El repositorio y los contratos congelados son la entrada.

**Definition of Done.** Los dos runners corren y pasan; el catálogo de migraciones aplicado muestra 14 tablas en 7 grupos, con la partición mensual y `sales_default` creadas; el glosario, la secuencia del protocolo offline y los apuntes de amenaza están revisados y no contradicen `ARCHITECTURE.md`.

> **Nota honesta sobre los runners.** Ni `go test ./...` ni `vitest` existen todavía en disco. Instalarlos y **probar que corren** es la primera tarea de esta semana (`docs/BACKEND.md` §9.2). Hasta que ambos pasen, ninguna tarea de este plan puede declararse verificada.

> **Nota de contrato.** `tenders` y `sync_events` aparecen como entidades en borradores anteriores y fueron pedidas por el plan previo. No son tablas en el contrato vinculante: el conteo es 14, no 15 (`docs/DATABASE.md` §8, `:15`; `docs/BACKEND.md` §3.1, §10.1).

---

### Sprint 2 (Semana 2) - Identidad y aislamiento de tenant

**Objetivo.** Que nadie venda sin tenant y que nadie lea los datos de otro tenant.

**Duración y ventana.** Una semana; corresponde al Sprint 1 del plan anterior.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W2-DB-1 | DB | `FORCE RLS` y las políticas `USING` / `WITH CHECK` sobre las **11 tablas** de tenant, con el predicado que lee `current_setting('app.current_tenant_id')::uuid` | Sin `SET LOCAL` la consulta devuelve cero filas y toda escritura con otro `tenant_id` se rechaza: el comportamiento es fail-closed | S1-2 |
| W2-DB-2 | DB | Matriz de concesiones con cinco roles (`pos_app`, `pos_auth`, `pos_platform`, `pos_maintenance`, `owner/migration`) y los dos caminos de bootstrap pre-auth: alta de tenant y usuario, y la lectura de `users` por `pos_auth` | `pos_app` no tiene `SELECT` sobre `tenants` ni `DELETE` sobre ninguna tabla de negocio; ningún rol de runtime tiene `BYPASSRLS` | — |
| W2-BE-1 | BE | `POST /auth/login` que emite un JWT con `tenant_id` y `user_id` y **sin** claim `role` (token de empleado) | Una petición sin tenant en el cuerpo o en un encabezado resuelve el tenant únicamente desde el token | S1-1 |
| W2-BE-2 | BE | Helper de transacción que fija `SET LOCAL app.current_tenant_id` como primera sentencia y lo deja resetear al confirmar; ninguna consulta corre fuera del helper | Un pooler en modo transacción no filtra el contexto de tenant entre clientes que reutilizan la conexión | — |
| W2-BE-3 | BE | `POST /auth/device-bind` con `devices.pin_hash` en argon2id y `offline_session_valid_until` fijado a la hora de bind más **7 días** como mínimo, refrescado en cada autenticación en línea | Un access token expirado con un PIN válido todavía desbloquea el POS offline dentro de la ventana | S1-3 |
| W2-BE-4 | BE | Namespacing obligatorio de todas las claves de Redis como `tenant:{id}:...` | Cualquier clave sin prefijo de tenant falla la revisión por grep | S1-4 |
| W2-FE-1 | FE | Pantalla de acceso del Empleado: display de PIN y reuso del numpad ergonómico de 56px (`docs/DESIGN.md:142-145`; `docs/FRONTEND.md:41`, `:47`) | El desbloqueo offline funciona dentro de la ventana y se rechaza, sin bloquear el intento en línea posterior, una vez pasada la expiración | — |
| W2-FE-2 | FE | Estado de conectividad observable: `useNetworkStatus` y badge de cabecera (`docs/FRONTEND.md:101-103`) | El badge alterna entre en línea y modo offline sin interrumpir ni bloquear la operación | — |

**Criterios de aceptación**

- Ningún endpoint acepta `tenant_id` desde el cuerpo ni desde un encabezado; el tenant sale solo del JWT (`ARCHITECTURE.md:23`, `:124`).
- El aislamiento se prueba *fail-then-pass*: antes de aplicar RLS la consulta cross-tenant devuelve filas y el test falla por diseño; después devuelve cero filas y pasa (`docs/DATABASE.md` §5.7).
- Un access token expirado con un PIN válido todavía desbloquea el POS offline durante al menos 7 días (`docs/DATABASE.md` §4 `devices`).
- El PIN y la sesión offline son estado del dispositivo, no del usuario (`docs/DATABASE.md` §4 `users`, `devices`).
- Ninguna clave de Redis carece del prefijo `tenant:{id}:` (`ARCHITECTURE.md:20`, `:86`).

**Dependencias de entrada.** Semana 1: la migración de identidad y las convenciones ya existen, y ambos runners corren.

**Definition of Done.** El camino login → JWT → consulta con scope de tenant queda probado por tests de aislamiento que fallan antes de RLS y pasan después, y ninguna petición acepta un valor de tenant desde el cuerpo o un encabezado.

> **Corrección de arrastre.** El plan anterior exigía, en esta zona, una interfaz que mostrara el total del servidor frente al del dispositivo. Ese diálogo no existe: la política vinculante es la reconciliación silenciosa (§7.9 de `docs/DATABASE.md` y `docs/DESIGN.md:256`) y su lugar está en el Sprint 6 (Semana 6).

---

### Sprint 3 (Semana 3) - Catálogo de tenant (sin universo global)

**Objetivo.** Que la bodega venda entre 50 y 200 productos propios sin depender de un catálogo maestro y sin descargar jamás un universo global.

**Duración y ventana.** Una semana; corresponde al Sprint 2 del plan anterior.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W3-DB-1 | DB | `products` con unicidad parcial `(tenant_id, name)` y `(tenant_id, gtin)` con predicado de soft-delete, el índice `products_tenant_updated_at_idx` **sin** predicado parcial, y `stock_adjustments` como log append-only de deltas firmados | El índice de cursor devuelve también las filas tombstoned; el índice parcial de unicidad no bloquea un nombre ya dado de baja | — |
| W3-BE-1 | BE | `GET /products`, `POST /products` y `PATCH /products/{id}`: nombre, GTIN opcional, precio en centavos, UoM, URL de foto y `attributes`; `stock` **excluido** del payload | Un producto creado con UoM `kg` persiste el campo UoM y no un JSONB; un `PATCH` que intenta quitar un campo devuelve `422` | S2-1 |
| W3-BE-2 | BE | Stub de búsqueda de GTIN bajo demanda, solo en el camino de fallo de caché y mediado por API | El teléfono nunca descarga una lista global de SKUs; el camino de fallo queda verificado | S2-3 |
| W3-BE-3 | BE | Fotos en object storage; la fila de producto guarda solo la URL | La carga devuelve una URL y ningún byte de blob entra en PostgreSQL, Redis ni IndexedDB | S2-4 |
| W3-FE-1 | FE | Grilla de favoritos: foto, nombre y precio, con tiles táctiles y agregado en un solo toque a través de `onProductIdentified(code)` (`docs/DESIGN.md:135-137`) | Un toque en la grilla agrega la línea correcta al carrito | S2-2 |
| W3-FE-2 | FE | Búsqueda instantánea por nombre y filtro por categoría, sin animación de entrada ni retraso (`docs/DESIGN.md:137`; `docs/FRONTEND.md:23`) | La escritura en el buscador no produce latencia ni salto de layout | — |
| W3-FE-3 | FE | Cache local del subconjunto de catálogo en IndexedDB, poblado por `GET /products` | La grilla renderiza desde el subconjunto local y el catálogo global nunca aparece en el dispositivo | — |

**Criterios de aceptación**

- El precio y el stock son columnas tipadas, no JSONB; el UoM se guarda como campo (`docs/DATABASE.md` §2.9, §4 `products`).
- `stock` no viaja en los payloads de `POST` ni de `PATCH` y converge solo por deltas firmados: `products.stock = SUM(stock_adjustments.qty_delta)` (`ARCHITECTURE.md:176`; `docs/DATABASE.md` §4 `products`, `stock_adjustments`).
- No se impone la restricción `stock >= 0`: el oversell offline se acepta a propósito y el log registra la realidad (`docs/DATABASE.md` §4 `products`).
- La colisión `(tenant_id, name)` produce un renombrado determinista y la colisión `(tenant_id, gtin)` deja el GTIN en `NULL`, ambas en silencio (`docs/DATABASE.md` §7.7).
- El teléfono nunca descarga un volcado global de SKUs (`ARCHITECTURE.md:89`, `:127`).
- Un catálogo de 200 productos propios renderiza la grilla en un Android de gama baja (`docs/BUSINESS.md:45`; `docs/BACKEND.md` §10.3).
- Los tiles y los controles críticos respetan el mínimo táctil de 48px (`docs/DESIGN.md:136`; `docs/PRODUCT.md:64`).

**Dependencias de entrada.** Semana 2: el aislamiento por tenant y el camino de identidad están entregados.

**Definition of Done.** Un catálogo de 200 productos de un tenant renderiza en un Android de gama baja, las escrituras de catálogo quedan con scope de tenant y no existe ningún camino de escritura hacia el catálogo global.

> **Advertencia de alcance.** `docs/DESIGN.md:238` describe un catálogo maestro colaborativo con escritura de tenants. Eso **no** es alcance: en el MVP el catálogo global es curado por plataforma y de solo lectura para el tenant, y el crowdsourcing es un epic posterior (`ARCHITECTURE.md:156`; `docs/DATABASE.md` §8 D6, §9). La adopción se resuelve copiando los datos globales a las columnas del tenant en el momento de adoptar; el puntero `products.global_product_id` es linaje, nunca una dependencia viva.

---

### Sprint 4 (Semana 4) - Venta de mostrador

**Objetivo.** Cobrar rápido sin depender de la cámara, y cerrar la venta sin duplicar dinero ni stock.

**Duración y ventana.** Una semana; corresponde al Sprint 3 del plan anterior.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W4-DB-1 | DB | Append-only efectivo sobre `sales` y `sale_lines`: grants de `UPDATE`/`DELETE` revocados para el rol de aplicación y guardas de trigger `BEFORE UPDATE OR DELETE` | Un `UPDATE` o un `DELETE` sobre una venta cerrada falla: no existe camino de edición de historia | — |
| W4-DB-2 | DB | Proyección de stock derivada del ledger: la venta escribe exactamente un delta negativo en `stock_adjustments` y la proyección se aplica de forma aditiva | La invariante `products.stock = SUM(stock_adjustments.qty_delta)` se sostiene después de cerrar una venta, y la escritura de la proyección no modifica `products.updated_at` | — |
| W4-BE-1 | BE | `POST /sales`: cierre append-only de `sales` más `sale_lines` en una sola transacción, con los totales congelados que envía el cliente y el delta de stock correspondiente | El servidor almacena los `line_total_cents` redondeados y **nunca** recomputa dinero | S3-3 |
| W4-BE-2 | BE | Protocolo de claim de idempotencia reutilizable: hash del payload, las tres ramas (clave nueva, clave repetida, duplicado concurrente) y las respuestas `200` / `422` / `409` con `Retry-After` | La clave se reclama con la restricción única, nunca con un `SELECT` seguido de un `INSERT` | — |
| W4-FE-1 | FE | Carrito: agregar, cantidad, quitar, override de precio, anular línea y persistencia en cada mutación, con debounce anti doble toque (`docs/FRONTEND.md:104-106`) | Recargar la aplicación restaura el carrito; un doble toque produce una sola línea | S3-1 |
| W4-FE-2 | FE | Pantalla de cobro: total, monto recibido y vuelto en tipografía grande, con **tender único** y exactamente **seis** métodos (`docs/DESIGN.md:158-176`; `docs/FRONTEND.md:45`) | Elegir un método fija el valor de `sales.payment_method`; una venta no puede repartirse entre dos medios | S3-2 |
| W4-FE-3 | FE | Cámara como adaptador best-effort hacia `onProductIdentified`, con estado de permiso denegado y fallback no bloqueante a la grilla (`docs/DESIGN.md:138-141`, `:251-252`) | Si la cámara falla o se deniega el permiso, la venta se completa igual desde la grilla o la búsqueda | S3-4 |
| W4-FE-4 | FE | Ticket en pantalla más share sheet nativo, sin llamada a `wa.me` ni dependencia de un ticket en la nube (`docs/DESIGN.md:176`) | La venta termina y el ticket se comparte por el share sheet del sistema, sin ningún camino `wa.me` | S3-5 |

**Criterios de aceptación**

- Repetir la misma clave con el mismo cuerpo devuelve el mismo ack sin duplicar dinero ni stock (`ARCHITECTURE.md:106`, `:169`; `docs/DATABASE.md` §7.1).
- La misma clave con un cuerpo distinto devuelve `422` y no crea una segunda venta (`ARCHITECTURE.md:106`).
- Un duplicado en vuelo devuelve `409` con `Retry-After` y el cliente espera; no abre una segunda venta (`ARCHITECTURE.md:107`; `docs/DATABASE.md` §7.1, rama C).
- `payment_method` acepta exactamente seis valores: `cash`, `yape`, `plin`, `card`, `other`, `fiado` (`docs/DATABASE.md` §4 `sales`, `:341`, `:354`).
- Un solo tender por venta: no se admite más de un medio de pago en la misma venta (`ARCHITECTURE.md:27`, `:39`, `:163`; `docs/DATABASE.md` §8 D9 y §9).
- El servidor nunca recomputa dinero: el redondeo half-up por línea lo hace el cliente al cerrar (`docs/DATABASE.md` §2.3).
- La venta se cierra localmente y el POST ocurre después: la red no está en el camino crítico del cobro (`ARCHITECTURE.md:98`).
- La cámara nunca bloquea: la grilla sigue siendo el happy path (`ARCHITECTURE.md:37`).

**Dependencias de entrada.** Semana 3: catálogo de tenant entregado y `stock` convergiendo por deltas.

**Definition of Done.** La grilla → carrito → cobro → cierre → ticket funciona en línea; el test de repetición de idempotencia pasa; la cámara es opcional y nunca bloquea; ninguna venta puede cerrarse con dos medios de pago.

> **Corrección de arrastre.** El plan anterior exigía, en su criterio de venta, que un cierre con efectivo más Yape diera el vuelto correcto, y listaba cinco métodos de pago. Ambas cosas contradicen el contrato: el CHECK fija **seis** valores y el pago repartido entre dos medios está fuera del alcance (`docs/DATABASE.md` §4 `sales` y `cash_close_counts`, §8 D9/D11 y enmienda 7, §9; `ARCHITECTURE.md:27`, `:163`). El criterio queda reescrito como tender único con seis valores.

---

### Sprint 5 (Semana 5) - Cierre de caja y durabilidad

**Objetivo.** Que los números de fin de día cuadren con el ledger, incluso después de un crash.

**Duración y ventana.** Una semana; corresponde al Sprint 4 del plan anterior.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W5-DB-1 | DB | `cash_closes` insert-once con `UNIQUE (tenant_id, closed_on)` y `cash_close_counts` append-only con PK `(cash_close_id, payment_method)` y el `CHECK` que fuerza `counted_cents = 0` cuando el método es `fiado` | Un segundo cierre de la misma fecha de negocio y tenant no puede insertarse; la regla de fiado contado en cero es estructural y no convencional | — |
| W5-DB-2 | DB | La fórmula de proyección de dinero servida por el índice `sales_tenant_id_sold_on_payment_method_idx` | La proyección por método de una fecha de negocio se resuelve con ese índice y con pruning de partición | — |
| W5-BE-1 | BE | `POST /cash-closes`: congela `expected_cents` por método con la fórmula `SUM(CASE kind WHEN 'sale' THEN +total_cents WHEN 'void' THEN -total_cents END)` y recibe `counted_cents` del operador | El cierre iguala la suma del ledger por método y la diferencia es computada, nunca almacenada: no existe un segundo contador | S4-1 |
| W5-BE-2 | BE | `POST /sales/{id}/void` como evento compensatorio: anexa una fila con `kind = 'void'` que espeja el método y el total positivo del original, y revierte el stock con un delta nuevo | La venta original se conserva íntegra; el stock se mueve en reversa; una anulación de anulación se rechaza | S4-2 |
| W5-BE-3 | BE | Cierre duplicado en cola resuelto como no-op silencioso: prevalece el cierre existente y el duplicado se confirma con la proyección existente | El duplicado nunca se le muestra al cajero, nunca se reintenta y nunca se marca como error | — |
| W5-BE-4 | BE | Documentar la sincronización del plan gratuito y la recuperación de teléfono perdido: el re-login restaura el historial desde el servidor | Un recorrido de borrado y reinstalación restaura el historial de ventas desde el servidor | S4-4 |
| W5-FE-1 | FE | Persistir la venta cerrada en IndexedDB **antes** del ack y marcar en la interfaz las ventas todavía sin sincronizar (`docs/DESIGN.md:254-255`) | Matar la pestaña a mitad de la cola no pierde ninguna venta cerrada después de reiniciar | S4-3 |
| W5-FE-2 | FE | Modal de arqueo y cierre de caja con una fila por método (`expected` / `counted` / `difference`) sobre los seis valores, y la fila de fiado informacional con contado bloqueado en cero | El arqueo muestra seis filas y la diferencia por método, sin fondo inicial ni egresos | — |
| W5-FE-3 | FE | Navigation sheet con los destinos operativos y el bloque de administración protegido por PIN (`docs/DESIGN.md:182-192`) | Los destinos navegan y el bloque administrativo queda tras el PIN | — |

**Criterios de aceptación**

- El cierre es una proyección de los eventos del ledger, no un segundo contador en ejecución (`ARCHITECTURE.md:41`; `docs/DATABASE.md` §4 `cash_close_counts`).
- Existe exactamente un cierre por fecha de negocio por tenant, y no hay camino de enmienda en el MVP (`docs/DATABASE.md` §8 D10, §9).
- `counted_cents = 0` para `fiado` está forzado por `CHECK`, y la diferencia de fiado es informacional por diseño: nunca se marca como faltante (`docs/DATABASE.md` §4 `cash_close_counts` y `cash_closes`; §8 D11).
- Las anulaciones nunca borran: la venta original permanece y el stock se mueve en reversa (`ARCHITECTURE.md:132`; `docs/DATABASE.md` §4 `sales`).
- Una venta cerrada se recupera tras matar la pestaña (evento en IndexedDB) y tras perder el teléfono (evento en el servidor) (`ARCHITECTURE.md:168`).

**Dependencias de entrada.** Semana 4: `POST /sales` y el protocolo de claim entregados.

**Definition of Done.** El cierre de caja reconcilia con la proyección del ledger y muestra la diferencia computada por método; las anulaciones no borran nada; ninguna venta cerrada se pierde al matar la pestaña.

> **Advertencia de forma del cierre.** El formulario de cierre **no** incluye fondo inicial ni egresos: `cash_closes` no tiene esas columnas y la fórmula de diferencia no tiene término de egreso en esta versión (`docs/DESIGN.md:194-198` describe el arqueo completo, que es posterior). El delta futuro está registrado (`docs/DATABASE.md` §4 `cash_closes`, nota delta D10; `docs/BACKEND.md` §11.2).

---

### Sprint 6 (Semana 6) - Sincronización offline (el núcleo)

**Objetivo.** Que una caída de 3G no detenga la venta ni cobre dos veces, y que ningún error de sincronización llegue jamás al mostrador.

**Duración y ventana.** Una semana; corresponde al Sprint 5 del plan anterior (1–2 semanas). El recorte a una semana es `(propuesta)`, apoyado en que el claim y la proyección de stock ya se entregaron en las Semanas 4 y 5.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W6-DB-1 | DB | Ciclo de vida del claim en `idempotency_keys`: `in_flight` → `completed` con el snapshot de respuesta acoplado al estado, y la retención de **400 días** ejecutada por el rol de mantenimiento | Un snapshot en vuelo nunca se sirve como éxito fantasma; el borrado de claves corre solo por el rol de mantenimiento, sobre esa tabla y ninguna otra | — |
| W6-DB-2 | DB | Mantenimiento de particiones: pre-creación idempotente **seis meses** hacia adelante, alerta por filas en `sales_default` y remediación detach/re-attach en una transacción | Correr el job dos veces el mismo día deja el mismo conjunto de particiones; la remediación deja `sales_default` vacía o revierte completa | — |
| W6-BE-1 | BE | Soporte del worker de sincronización: flush por lotes, jitter, retroceso exponencial y POST idempotente, respetando `Retry-After` en el `409` | Cien ventas encoladas se vacían sin estampida y los duplicados colapsan a un ack cada uno | S5-2 |
| W6-BE-2 | BE | **Reconciliación silenciosa** del lado servidor ante violaciones de unicidad inbound y reordenamiento padre → hijo del lote | Una violación de unicidad entra, se resuelve del lado servidor y devuelve `200` con la fila resuelta: el POS no recibe ningún error | S5-3 |
| W6-BE-3 | BE | `GET /sync/cursor`: filas de las tablas mutables con `updated_at > cursor`, incluyendo tombstones, con avance solo hasta el máximo realmente devuelto | Una fila actualizada mientras se armaba la respuesta se vuelve a traer en el pull siguiente, dentro del solape de 15 minutos | — |
| W6-FE-1 | FE | IndexedDB limitado al subconjunto de catálogo más la cola de eventos pendientes, con degradación visible cuando se agota la cuota | Al agotarse la cuota aparece una venta bloqueada con su motivo, nunca una pantalla en blanco | S5-1 |
| W6-FE-2 | FE | Shell de PWA con caché versionada, sin Cache-First ciego para la lógica de la aplicación | Un despliegue nuevo llega al dispositivo y la ruta de cobro no queda fijada a una versión vieja | S5-4 |
| W6-FE-3 | FE | Motor de sincronización del cliente: flush por lotes al recuperar conectividad, con retroceso y sin abrir jamás una segunda venta | Con la red intermitente, la cola se vacía sola y el cajero nunca ve un error, un prompt ni un reintento manual | — |

**Criterios de aceptación**

- Cien ventas encoladas se vacían en lotes sin estampida y cada duplicado colapsa a un solo ack (`docs/BACKEND.md` §8.2; `ARCHITECTURE.md:84`).
- **Ninguna violación de unicidad inbound llega al cajero**: el renombrado determinista por nombre, el GTIN puesto en `NULL` y el cierre duplicado como no-op se resuelven del lado servidor y el POS no recibe ningún error (`ARCHITECTURE.md:24`, `:111-116`, `:178`; `docs/DATABASE.md` §7.7, §7.8, §7.9; `docs/DESIGN.md:256`).
- Dinero y stock **no** se resuelven por last-write-wins: convergen solo por eventos append-only y deltas firmados (`ARCHITECTURE.md:25`; `docs/DATABASE.md` §7.4, §7.5).
- El cursor avanza solo hasta el máximo `updated_at` realmente devuelto, con solape de 15 minutos, y nunca transporta dinero ni stock (`docs/DATABASE.md` §7.3).
- El motor de sincronización corre en el cliente: no hay WebSocket y no se depende de la Background Sync API (`ARCHITECTURE.md:91`).
- Al agotarse la cuota local, la venta se bloquea con motivo visible en lugar de fallar en blanco.
- Los únicos POST encolables son los seis de la enumeración exhaustiva; la edición de producto con `PATCH` es solo en línea (`docs/DATABASE.md` §7.6; `docs/FRONTEND.md:53`).

**Dependencias de entrada.** Semana 5: el cierre de caja, la anulación y la persistencia local antes del ack están entregados.

**Definition of Done.** El cierre offline → reconexión → flush por lotes → ack queda probado por tests de cola y de repetición; ninguna violación de unicidad inbound llegó al cajero en la prueba; ninguna venta se perdió al matar la pestaña a mitad de la cola.

> **Corrección de arrastre (la corrección más importante del refactor).** El plan anterior exigía, en esta zona y en su Definition of Done, que los conflictos se *mostraran* en la interfaz con el total del servidor frente al del dispositivo, y prohibía el last-write-wins silencioso. Las dos cosas quedaron invertidas en el contrato: el servidor reconcilia en silencio y el POS **nunca** expone un error de sincronización (`ARCHITECTURE.md:24`, `:111-116`, `:178`; `docs/DATABASE.md` §7.9 y §8 enmienda 3; `docs/DESIGN.md:256`). Esta semana entrega reconciliación silenciosa, y el plan de pruebas se corrigió en la misma dirección.

> **Nota de costo de protocolo.** Dos clientes nunca llegan a un empate visual porque no hay superficie visual: si dos mutaciones comparten `updated_at`, gana el `id` lexicográficamente mayor y el resultado converge igual (`docs/DATABASE.md` §7.4).

---

### Sprint 7 (Semana 7) - Fiados

**Objetivo.** Que saber quién debe qué sobreviva a un teléfono robado.

**Duración y ventana.** Una semana; corresponde al Sprint 6 del plan anterior.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W7-DB-1 | DB | `fiado_accounts` sin columna de saldo escribible y `fiado_movements` como ledger append-only donde el **signo** de `amount_cents` es el tipo, con par-nulidad del enlace opcional a la venta | La escritura directa de un saldo se rechaza; el saldo computado coincide con la suma de movimientos | — |
| W7-DB-2 | DB | Upsert atómico de la cuenta dentro de la transacción del movimiento, e índice de cursor `fiado_accounts_tenant_id_updated_at_idx` | Un movimiento que referencia una cuenta inexistente crea la cuenta y anexa el movimiento en una sola transacción; si el claim se revierte, la cuenta también | — |
| W7-BE-1 | BE | Registro de cliente (nombre obligatorio, teléfono opcional) y `GET /fiado-accounts` con el saldo computado como `SUM(fiado_movements.amount_cents)` | El saldo es computado y no existe columna escribible que lo sostenga | S6-1 |
| W7-BE-2 | BE | `POST /fiado-movements` para cargo y pago parcial, encolable offline con las mismas reglas de idempotencia que las ventas | Un cargo a fiado emitido offline se encola, sincroniza y se repite de forma idempotente | S6-2 |
| W7-BE-3 | BE | Servir el historial de movimientos firmados para restaurar cuentas y saldos tras una reinstalación | Un recorrido de borrado y reinstalación restaura cuentas y movimientos después del login | S6-3 |
| W7-BE-4 | BE | Confirmar que no se envía ningún recordatorio de deuda por WhatsApp en el MVP | No existe ningún camino WABA ni `wa.me` en el incremento | S6-4 |
| W7-FE-1 | FE | Cartera de deudores con búsqueda por nombre o teléfono y saldo computado (`docs/FRONTEND.md:51`) | La búsqueda encuentra por nombre y por teléfono y muestra el saldo computado | — |
| W7-FE-2 | FE | Cargo a fiado desde el carrito y registro del deudor en 2 clics, más abono parcial, sin salir del flujo de venta (`docs/PRODUCT.md:27`; `docs/FRONTEND.md:51`) | Un cargo a fiado se completa sin salir del flujo de venta y el abono amortiza la deuda | — |
| W7-FE-3 | FE | Historial de movimientos firmados y restauración desde el servidor tras reinstalar | Tras reinstalar, las cuentas y los movimientos vuelven desde el servidor | — |

**Criterios de aceptación**

- El saldo iguala `SUM(fiado_movements.amount_cents)` y la escritura directa de un saldo se rechaza (`ARCHITECTURE.md:134`, `:172`; `docs/DATABASE.md` §4 `fiado_accounts`).
- El signo del monto es el tipo: cargo positivo, pago negativo; no existe una columna `kind` adicional (`docs/DATABASE.md` §4 `fiado_movements`).
- La anulación de una venta a fiado produce un movimiento compensatorio con signo invertido, nunca un `DELETE` ni una edición (`docs/DATABASE.md` §4 `fiado_movements`).
- El cargo a fiado funciona offline y sincroniza de forma idempotente (`docs/BACKEND.md` §10.7).
- El registro del deudor se hace en dos toques y la deuda sobrevive a la reinstalación (`docs/PRODUCT.md:27`).
- No existe ningún camino de recordatorio por WhatsApp (`ARCHITECTURE.md:158`; `docs/DESIGN.md:207` lo declara posterior).

**Dependencias de entrada.** Semana 6: la reconciliación silenciosa y el pull por cursor están entregados.

**Definition of Done.** El cargo a fiado y el pago parcial funcionan offline y sincronizan de forma idempotente; los saldos son computados y nunca escritos; ninguna cuenta ni movimiento se pierde tras reinstalar.

> **Advertencia de alcance.** La interfaz **no** renderiza el aviso de límite de crédito que describe `docs/DESIGN.md:205`. La columna `fiado_accounts.credit_limit_cents` está adoptada en el contrato pero es una nota docs-only sin DDL: la UI espera el contrato, no lo inventa (`docs/DATABASE.md` §4 `fiado_accounts`, nota delta D4; `docs/BACKEND.md` §11.2).

---

### Sprint 8 (Semana 8) - Dashboard Admin y endurecimiento

**Objetivo.** Dar al dueño la lectura de su propio negocio y cerrar los bordes del contrato, sin agregar una sola superficie de datos nueva.

**Duración y ventana.** Una semana. Bloque **nuevo**: no corresponde a ningún sprint del plan anterior. Absorbe el delta D12 y la suite Admin de `docs/DESIGN.md:436-449`.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W8-FE-1 | FE | Acceso Admin en dos pantallas: selector de rol (Empleado / Administrador) y credencial (teléfono más contraseña maestra para Admin; PIN para Empleado) (`docs/DESIGN.md:442-443`; `docs/FRONTEND.md:46-47`) | Las dos pantallas renderizan contra `POST /auth/admin-login` y contra `devices.pin_hash`; no hay datos de tenant antes de autenticar | — |
| W8-FE-2 | FE | Shell Admin: sidebar de 240px con 6 destinos, badge de rol y footer de sesión con cierre de sesión del lado cliente (`docs/FRONTEND.md:48`) | El shell navega los 6 destinos; no existe endpoint de cierre de sesión y no se inventa uno | — |
| W8-FE-3 | FE | Dashboard Admin: ventas netas, ticket promedio, desglose por tender (los seis), fiados por cobrar, cierres locales con sobrante o faltante y distribución por hora (`docs/FRONTEND.md:49`) | Los números salen **únicamente** de la proyección del ledger local en IndexedDB más `GET /fiado-accounts`, sin ningún endpoint de reporte | — |
| W8-FE-4 | FE | Cierre de caja del rol Admin: seis filas con `expected` / `counted` / `difference`, fila de fiado informacional con contado bloqueado en cero, y formulario de solo lectura cuando el cierre del día ya existe (`docs/FRONTEND.md:50`) | El arqueo renderiza las seis filas y liga a `POST /cash-closes`; un cierre ya existente no se puede volver a enviar | — |
| W8-FE-5 | FE | Inventario, mermas y catálogo del rol Admin: semáforo de stock, registro de merma con los tres motivos exactos, tabla de SKUs y adopción desde el catálogo global (`docs/FRONTEND.md:52-53`) | El registro de merma encola `POST /stock-adjustments` con un motivo de la lista exacta y **sin** control de foto; la edición de producto usa `PATCH` y es solo en línea | — |
| W8-BE-1 | BE | `POST /auth/admin-login`: resuelve teléfono → tenant, verifica `tenants.master_password_hash` con argon2id y emite un JWT con `role:"admin"` | La respuesta devuelve token y nombre del tenant, y un `401` constante y genérico sin enumeración de usuarios | — |
| W8-BE-2 | BE | `POST /stock-adjustments`: delta firmado negativo para una merma, con `source = 'manual'` y uno de los tres motivos exactos | La merma anexa una fila al log y mueve el stock de forma aditiva sin tocar `products.updated_at`; el stock negativo se permite | — |
| W8-BE-3 | BE | Endurecimiento del borde: un solo sobre de error `{ error: { code, message, details? } }`, validación de los conjuntos acotados en el borde y chequeos de salud `(propuesta)` | Un `PATCH` que envía `stock` o que intenta quitar un campo devuelve `422`; toda respuesta de error usa la misma forma | — |
| W8-BE-4 | BE | Observabilidad y alerta operativa `(propuesta)`: logs estructurados sin PIN, fotos ni payloads, y la alerta por filas en `sales_default` dirigida al canal de operaciones | La alerta de particiones llega al canal de operaciones y en ningún caso al cajero | — |
| W8-DB-1 | DB | Migración de `tenants.master_password_hash` como `TEXT NOT NULL` con argon2id, ejecutada por el rol de migración, más el backfill de alta de administrador | La columna existe, `POST /auth/admin-login` puede implementarse y queda decidido qué ocurre con un tenant sin hash | — |
| W8-DB-2 | DB | Revisión final del contrato de esquema contra el checklist de arquitectura: 14 tablas, dos columnas JSONB, seis valores de `payment_method`, cero concesiones de tenant sobre las tablas globales y append-only en las cinco tablas | `pos_app` no tiene `DELETE` sobre ninguna tabla de negocio ni `BYPASSRLS`, y las tablas globales no tienen política de tenant | — |

**Criterios de aceptación**

- El dashboard Admin renderiza **solo** la proyección del ledger local más `GET /fiado-accounts`: no se agrega ningún endpoint de reporte ni de agregados (`ARCHITECTURE.md:138`; `docs/BACKEND.md` §4.15).
- Un token de empleado no lleva claim `role` y un token de Admin sí lleva `role: "admin"`; la ausencia del claim significa empleado (`ARCHITECTURE.md:23`, `:126`).
- El `401` del login de administrador es genérico y constante, sin distinguir entre teléfono inexistente y contraseña incorrecta (`ARCHITECTURE.md:126`).
- Los tres motivos de merma son exactamente `Vencimiento`, `Rotura en transporte` y `Daño en vitrina` (`docs/DESIGN.md:217`).
- La merma no tiene captura de foto en esta versión (`docs/DATABASE.md` §4 `stock_adjustments`, nota delta D5).
- Ningún rol de runtime puede borrar ni actualizar una fila del ledger (matriz de concesiones de `docs/DATABASE.md` §5.5).

**Dependencias de entrada.** Semana 7. Además, esta semana depende de que la Semana 4 haya dejado el sobre de error único y el protocolo de claim operativos.

**Definition of Done.** El dueño entra con la contraseña maestra del tenant y ve sus propios números sin que exista un endpoint de reporte; el registro de merma escribe un delta firmado con un motivo de la lista exacta; el checklist de esquema de arquitectura se verifica entero sobre las 14 tablas.

> **Alcance del dashboard (delta D12, diferido).** El delta registrado que proponía `GET /sales` o `GET /dashboard/summary` más una tabla de asistencia **sigue diferido**: no se agrega ningún endpoint de reporte en esta versión y el marcaje de asistencia y el listado de turnos quedan fuera (`ARCHITECTURE.md:138`; `docs/DESIGN.md:231`; `docs/BACKEND.md` §11.2 y §11.3). Tampoco existen columnas de impuestos ni de descuentos en el esquema (`docs/DATABASE.md` §4 `sales`), ni `products.cost_cents` (nota delta D11): la pantalla de catálogo no renderiza márgenes ni alertas de costo.

> **Advertencia de forma del login.** La contraseña maestra vive como columna futura documentada, y su declaración `TEXT NOT NULL` sin valor por defecto convive mal con la regla "tenant sin hash → 401 genérico". La decisión queda registrada como divergencia abierta en `docs/BACKEND.md` §11.4 #7 y debe resolverse antes de esta semana.

---

### Sprint 9 (Semana 9) - Piloto de campo (10 bodegas, no 20)

**Objetivo.** Probar el analgésico antes de cualquier trabajo de minimarket. La escala es de 10 bodegas (`docs/DATABASE.md` §7.2). Este bloque arranca el piloto: alta cronometrada, instrumentación y comienzo de la operación.

**Duración y ventana.** Una semana; corresponde a la primera mitad del Sprint 7 del plan anterior. El reparto interno entre el Sprint 9 y el Sprint 10 es `(propuesta)`.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W9-DB-1 | DB | Operación de respaldo y recuperación `(propuesta)`: instantánea diaria más archivado continuo, recuperación a un punto en el tiempo alineada con la retención de los claims, y un ensayo de restauración probado | Un ensayo de restauración ejecutado y registrado dentro de la ventana del piloto, no solo la existencia del respaldo | — |
| W9-BE-1 | BE | Instrumentar y medir los percentiles 50 y 95 del cierre de venta y de la latencia de flush, y comparar el percentil 95 del escaneo contra el umbral de 3 segundos (`docs/BACKEND.md` §8.1) | Métricas registradas por dispositivo; la grilla sigue siendo el camino por defecto si el percentil 95 de la cámara supera 3 segundos | S7-2 |
| W9-BE-2 | BE | Arrancar la operación de siete días consecutivos: ventas, fiados y cierre de caja con sincronización después de cada corte | La operación de siete días arranca y las jornadas registradas hasta el cierre de este bloque quedan sin duplicados silenciosos ni fugas cross-tenant | S7-3 |
| W9-FE-1 | FE | Alta cronometrada de una bodega en sus dos modos: autoservicio (registro, carga de 20 favoritos y primera venta) y asistida en el mostrador (el dolor que justifica que el alta sea corta está en `docs/PRODUCT.md:29`; el conteo de 20 favoritos proviene del plan anterior) | Dos criterios cronometrados en vivo sobre un Android de gama baja real: menos de 10 minutos de alta autoservicio sin ayuda, y 5 minutos de alta asistida en el teléfono del propio bodeguero (`docs/BUSINESS.md:67`) | S7-1 |
| W9-FE-2 | FE | Medir el camino de escaneo a carrito en dispositivos de menos de US$100 | Muestras registradas por dispositivo, sin promediar sobre hardware distinto | — |

**Criterios de aceptación**

- La operación de siete días corridos con al menos un corte de red por jornada **arranca** dentro de esta semana; cada venta cerrada se recupera tras matar la pestaña y tras reinstalar (`ARCHITECTURE.md:168`).
- El alta **autoservicio**, sin ayuda del equipo, se completa en menos de 10 minutos con registro, 20 favoritos y primera venta.
- El alta **asistida** en el mostrador se completa en 5 minutos en el teléfono del propio bodeguero, cronometrada en campo (`docs/BUSINESS.md:67`).
- Los percentiles quedan **medidos y registrados**; ninguna latencia se declara como lograda sin medición previa (`docs/BACKEND.md` §8.1). La grilla permanece como camino por defecto si el percentil 95 de la cámara supera 3 segundos.

**Dependencias de entrada.** Sprints 1 a 8 (Semanas 1 a 8) completos, con ambos runners corriendo y ninguna métrica de cobertura reclamada sin medición.

**Definition of Done.** La bodega se da de alta en menos de 10 minutos en autoservicio y en 5 minutos asistida en el mostrador, los percentiles del piloto quedan registrados y la operación de siete días ha arrancado con al menos un corte de red observado.

> **Nota de decisión (2026-10-14).** El criterio heredado medía el alta autoservicio en menos de 10 minutos, mientras `docs/BUSINESS.md:67` declara un alta asistida en el mostrador de 5 minutos. Decisión del Product Owner: **se conservan como dos criterios separados y ambos son exigibles**, porque miden cosas distintas — el alta autoservicio demuestra que el producto se explica solo, y el alta asistida es la promesa comercial de adquisición. Ninguno de los dos sustituye al otro y ninguno se promedia con el otro.

> **Nota de honestidad de medición.** Nada en este bloque se afirma como ya medido: los percentiles, los tiempos de alta y las métricas de flush son objetivos a registrar durante el piloto, no resultados. Ninguna corrida de pruebas ni número de cobertura se declara sin haber sido ejecutado.

---

### Sprint 10 (Semana 10) - Piloto de campo, cierre y kill list

**Objetivo.** Cerrar los siete días corridos de operación, revisar conflictos y duplicados jornada por jornada, y publicar la lista de corte firmada.

**Duración y ventana.** Una semana; corresponde a la segunda mitad del Sprint 7 del plan anterior. El reparto interno entre el Sprint 9 y el Sprint 10 es `(propuesta)`.

| ID | Capa | Trabajo | Entregable demostrable | ID previo |
| :--- | :--- | :--- | :--- | :--- |
| W10-DB-1 | DB | Cierre del particionado del periodo del piloto y revisión de la remediación de la partición por defecto | El periodo del piloto tiene sus particiones mensuales y la partición por defecto queda vacía | — |
| W10-BE-1 | BE | Cerrar los siete días y publicar la lista de corte: qué se corta y qué se construye después | Lista de corte firmada por el Product Owner, con evidencia del piloto | S7-4 |
| W10-FE-1 | FE | Cierre del piloto: bitácora de los siete días y revisión de estados en campo contra la especificación de estados (`docs/DESIGN.md:248-267`) | Bitácora completa con cada jornada, cada corte de red y cada resolución silenciosa observada | — |

**Criterios de aceptación**

- Siete días corridos de operación completos, con al menos un corte de red por jornada; cada venta cerrada se recupera tras matar la pestaña y tras reinstalar (`ARCHITECTURE.md:168`).
- Cero duplicación de dinero o stock por reintento (`ARCHITECTURE.md:169`).
- Cero fugas cross-tenant: el test de aislamiento falla antes de RLS y pasa después (`docs/DATABASE.md` §5.7).
- Las resoluciones silenciosas y los conflictos de las jornadas quedan revisados jornada por jornada.
- La lista de corte queda firmada y los epics posteriores siguen sin agendar hasta que esa evidencia exista (`docs/BUSINESS.md:75`, `:83`).

**Dependencias de entrada.** Sprint 9 (Semana 9) completo: la operación de siete días arrancada y las métricas registradas.

**Definition of Done.** La bitácora de siete días está completa, la lista de corte está firmada por el Product Owner y los epics posteriores siguen sin agendar.

---

## 8. Plan de pruebas (TDD estricto — obligatorio)

**La regla.** No hay código sin un test que falle primero. Rojo → verde → refactor por tarea. Los tests se entregan en la misma unidad de trabajo que el comportamiento que verifican, y el revisor mira el test antes de la implementación. Una tarea está Done solo cuando su criterio de aceptación es demostrablemente cierto, no cuando existe el código (`docs/BACKEND.md` §9.1).

**Runners — instalar y probar es la primera tarea del Sprint 1 (Semana 1).** Los runners requeridos son `go test ./...` para la API y `vitest` para la PWA. **Ninguno existe todavía en disco.** Instalarlos y probar que corren es la primera tarea del Sprint 1 (Semana 1) (`W1-BE-1`, `W1-FE-1`), y hasta que ambos pasen la verificación no puede probarse (`docs/BACKEND.md` §9.2). Este plan no reclama ninguna corrida que no pueda existir.

**Capas.**

| Capa | Alcance | Mecánica |
| :--- | :--- | :--- |
| Unitaria | Funciones puras: totales, aritmética de saldos y el redondeo half-up de las líneas | Tabla de casos |
| Integración | API → PostgreSQL con RLS; sincronización → API | Se saltan bajo `testing.Short()` cuando son lentas |
| Cliente (PWA) | Persistencia del carrito, flush de la cola y **resolución silenciosa del lado servidor** | `vitest` |

**Reglas.** Probar comportamiento y transiciones de estado, no trivialidades. Usar `t.TempDir()` en los tests de filesystem. Los tests de RLS corren transacciones por tenant. Los de idempotencia repiten la misma clave dos veces. Los offline matan la pestaña o la red y después hacen flush (`docs/BACKEND.md` §9.3).

| Sprint (Semana) | Módulo | Objetivo de prueba | Runner | Primer test que falla |
| :--- | :--- | :--- | :--- | :--- |
| 2 (Semana 2) | Identity | El JWT lleva `tenant_id`; el tenant del cuerpo o del encabezado se ignora | `go test ./...` | `POST /sales` con un `tenant_id` falsificado en el cuerpo queda acotado al tenant del JWT |
| 2 (Semana 2) | Identity | Aislamiento por RLS (`FORCE RLS`, `SET LOCAL`, seguro frente al pooler) | `go test ./...` | El token del tenant A no puede seleccionar productos del tenant B |
| 2 (Semana 2) | Identity | Device bind y sesión offline por PIN de 7 días o más | `go test ./...` + `vitest` | Un access token expirado con PIN válido todavía desbloquea el POS offline |
| 3 (Semana 3) | Catalog | CRUD de producto por tenant, UoM y precio en centavos | `go test ./...` | Crear un producto con UoM `kg` persiste el campo UoM, no un JSONB |
| 3 (Semana 3) | Catalog | Grilla de favoritos → `onProductIdentified` | `vitest` | Un toque en la grilla agrega la línea correcta al carrito |
| 3 (Semana 3) | Photos | Solo URL de object storage | `go test ./...` | La fila de producto guarda una URL y los bytes de un blob se rechazan |
| 4 (Semana 4) | Cart/Sale | Persistencia del carrito en cada mutación y debounce | `vitest` | Recargar después de agregar restaura el carrito; un doble toque produce una sola línea |
| 4 (Semana 4) | Ledger | Cierre idempotente de `POST /sales` | `go test ./...` | La misma clave con el mismo cuerpo repetida produce una venta y un movimiento de stock |
| 4 (Semana 4) | Ledger | Reutilización de clave con cuerpo distinto y duplicado en vuelo | `go test ./...` | La misma clave con un cuerpo distinto devuelve `422` y no crea una segunda venta |
| 5 (Semana 5) | Ledger | `POST /sales/{id}/void` como evento compensatorio | `go test ./...` | La anulación conserva la venta original y el stock se mueve en reversa |
| 5 (Semana 5) | Cash close | `POST /cash-closes` como proyección frente a lo contado | `go test ./...` | El cierre iguala la suma del ledger por método y la diferencia se muestra computada |
| 6 (Semana 6) | Sync | Cola en IndexedDB y flush por lotes con retroceso | `vitest` | Cien eventos encolados se vacían en lotes y cada uno recibe un solo ack |
| 6 (Semana 6) | Sync | Caída y reconexión, con degradación por cuota | `vitest` | Matar la pestaña a mitad de la cola no pierde ventas cerradas; la cuota muestra el motivo |
| 6 (Semana 6) | Sync | **Reconciliación silenciosa** del lado servidor (reemplaza la fila de interfaz de conflicto) | `go test ./...` + `vitest` | Una violación de unicidad inbound por nombre, por GTIN o por cierre duplicado se resuelve del lado servidor con `200` y **el POS no recibe ningún error** |
| 7 (Semana 7) | Fiado | El saldo es `SUM(movements)` y nunca escribible | `go test ./...` | La escritura directa de un saldo se rechaza y el saldo computado coincide con los movimientos |
| 7 (Semana 7) | Fiado | `POST /fiado-movements` con cargo y pago parcial fuera de línea | `go test ./...` + `vitest` | Un cargo a fiado offline se encola y se repite de forma idempotente |
| 8 (Semana 8) | Admin | Login de administrador con `role: "admin"` y `401` genérico | `go test ./...` | Un tenant sin hash de contraseña maestra devuelve `401` sin revelar si el teléfono existe |

**Unidad de trabajo.** Cada tarea de §7 se cierra como una unidad de trabajo revisable: el commit contiene el comportamiento y el test que lo verifica, con un mensaje convencional, y la documentación de la tarea viaja con el cambio que explica. El commit es, además, el candidato natural a revisión de esa capa.

**Sin números de cobertura.** Ningún número de cobertura se reclama. Se agregan solo después de que los runners estén instalados y medidos (`docs/BACKEND.md` §9.5). Afirmar un porcentaje ahora sería fabricar evidencia.

---

## 9. Trazabilidad de IDs

Los IDs previos se conservan íntegros: `docs/BACKEND.md` §10, §11.4 y §12 los citan y el historial de commits y de memoria los usa. El mapeo exacto, tarea por tarea, está en la columna `ID previo` de §7.

| Bloque de sprint (semana) | Grupo previo | Rango de IDs previos | Nota |
| :--- | :--- | :--- | :--- |
| Sprint 1 (Semana 1) | Sprint 0 (congelamiento) | `S0-*` (4 tareas) | El campo de dominio pasa a ser la migración de las 14 tablas; `tenders` y `sync_events` dejan de tratarse como tablas |
| Sprint 2 (Semana 2) | Sprint 1 | `S1-*` (4 tareas) | La tarea de RLS del grupo se ancla a la fila de base de datos (políticas y matriz de concesiones); su mitad de runtime es `W2-BE-2`, sin ID previo |
| Sprint 3 (Semana 3) | Sprint 2 | `S2-*` (4 tareas) | La grilla de favoritos es la única tarea de cliente del grupo |
| Sprint 4 (Semana 4) | Sprint 3 | `S3-*` (5 tareas) | El criterio de cobro del grupo queda reescrito: tender único y seis valores, sin pago repartido entre dos medios |
| Sprint 5 (Semana 5) | Sprint 4 | `S4-*` (4 tareas) | — |
| Sprint 6 (Semana 6) | Sprint 5 | `S5-*` (4 tareas) | La tarea de sincronización del grupo y su Definition of Done pasan de interfaz de conflicto a reconciliación silenciosa |
| Sprint 7 (Semana 7) | Sprint 6 | `S6-*` (4 tareas) | — |
| Sprint 8 (Semana 8) | — | ninguno | Bloque nuevo; absorbe el delta D12 como diferido, no como construido |
| Sprint 9 (Semana 9) | Sprint 7 | `S7-*` (3 de 4 tareas) | El piloto conserva su escala de 10 bodegas; el reparto interno entre el Sprint 9 y el Sprint 10 es `(propuesta)` |
| Sprint 10 (Semana 10) | Sprint 7 | `S7-*` (1 de 4 tareas) | Cierre del piloto: bitácora, revisión de conflictos y publicación de la lista de corte |

**Convención de citas cruzadas.** Los documentos que dependen de este plan (`docs/BACKEND.md`, `docs/DESIGN.md`, `docs/DATABASE.md`, `docs/FRONTEND.md`) citan `SPRINTS.md` por **sección, sprint o ID de tarea**, nunca por número de línea (por ejemplo `SPRINTS.md` §8, `SPRINTS.md` Sprint 6 (Semana 6), o un ID de tarea de la columna `ID previo`). Re-sincronizados los punteros, ya no queda ninguna cita de este documento por número de línea en los planes y contratos vivos.

Sobre los IDs no hay trabajo pendiente: los nombres del grupo `S0-*` … `S7-*` siguen existiendo, siguen resolviendo a una tarea concreta a través de la columna `ID previo` de §7, y así los citan `docs/BACKEND.md` §10, §11.4 y §12.

---

## 10. Registro de correcciones de este refactor

| # | Qué cambió frente a la versión anterior | Por qué | Fuente que lo fuerza |
| :--- | :--- | :--- | :--- |
| 1 | La interfaz de conflicto desaparece de todo el plan: de la secuencia del protocolo offline, de la tarea de sincronización, del Definition of Done de esa semana, de la descripción de capas del plan de pruebas y de su fila de pruebas | El contrato fija reconciliación silenciosa: el servidor resuelve y el POS nunca expone un error de sincronización al cajero | `ARCHITECTURE.md:24`, `:111-116`, `:178`; `docs/DATABASE.md` §7.7, §7.8, §7.9 y §8 enmienda 3; `docs/DESIGN.md:256` |
| 2 | El criterio de cobro deja de exigir que un pago repartido entre efectivo y Yape cierre con el vuelto correcto, y deja de listar cinco métodos: queda tender único con exactamente **seis** valores | El contrato prohíbe repartir el pago entre dos medios en una misma venta y fija el `CHECK` a seis valores | `ARCHITECTURE.md:27`, `:39`, `:163`; `docs/DATABASE.md` §4 `sales` (`:341`, `:354`) y `cash_close_counts` (`:511`, `:522`), §8 D9/D11 y enmienda 7, §9 |
| 3 | La lista de campos del Sprint 0 pasa a cubrir las **14 tablas** del contrato, y deja de pedir campos para dos entidades que no son tablas | Una de esas entidades queda superseded por la columna `sales.payment_method` y la otra se fusiona en `idempotency_keys`; el conteo vinculante es 14, no 15 | `docs/DATABASE.md` §8 enmiendas 1 y 5, §2.1 y `:15`; `ARCHITECTURE.md:142`; `docs/BACKEND.md` §3.1, §10.1 |
| 4 | El documento pasa del inglés al español neutral profesional | Convención observada del corpus: los contratos técnicos están en inglés y los planes en español; este archivo es un plan | `odd/tasks/sprints-week-plan.md:31` |
| 5 | Los sprints con duraciones sueltas pasan a bloques de sprint de una semana (un sprint por semana) con alcance declarado por capa y con el orden Base de Datos → Backend → Frontend | Antes no se declaraba de qué lado arrancaba cada módulo ni con qué orden de dependencia | `(propuesta)` de estructura; el fundamento por capa está en §2 y se apoya en `ARCHITECTURE.md:3`, `:19`, `:177` |
| 6 | Se agrega el Sprint 8 (Semana 8) (dashboard Admin y endurecimiento), que no existía en el plan anterior | El dashboard y los deltas de contrato no tenían bloque propio, y el delta de endpoints de reporte queda registrado como diferido en lugar de quedar sin lugar | `ARCHITECTURE.md:138`; `docs/BACKEND.md` §11.2; `odd/tasks/stitch-admin-prompts.md:121-127` |
| 7 | Cada tarea recibe un ID propio `W<semana>-<capa>-<n>` y una columna `ID previo` con el ID heredado | Los IDs heredados del grupo `S0-*` … `S7-*` son citados por `docs/BACKEND.md` §10, §11.4 y §12 y por el historial de commits; perderlos habría roto referencias reales | `docs/BACKEND.md` §10, §11.4, §12 |
| 8 | Las ceremonias pasan a cadencia semanal con revisión por capa | El plan ahora es semanal, y el orden por capa exige aceptar una capa antes que la siguiente; la UI de un módulo no se revisa antes que su backend | `(propuesta)`; roles, ceremonias y flujo de backlog heredados y adaptados del plan anterior |
| 9 | El Definition of Done de cada bloque se reescribe como hecho demostrable, no como lista de actividades | La regla del propio plan es que una tarea está Done cuando su criterio es demostrablemente cierto | `docs/BACKEND.md` §9.1 |
| 10 | Cada bloque declara sprint y semana con numeración 1:1, y el piloto se divide en dos bloques: Sprint 9 (Semana 9) y Sprint 10 (Semana 10) | Un sprint equivale a una semana; el bloque fusionado `Semanas 9–10` contradecía esa unidad, y el reparto del Sprint 7 previo no era uno a uno | Política de unidad declarada por el usuario (un sprint = una semana); reparto interno `(propuesta)` |

---

## 11. Epics posteriores (explícitamente sin agendar)

No se toma ninguno de estos hasta que los Sprints 9 y 10 (Semanas 9 y 10) produzcan evidencia en forma de lista de corte firmada. El horizonte es estratégico y sin fechas por diseño: la secuencia y los criterios de avance pertenecen a este documento (`docs/BUSINESS.md:75`, `:83`).

- Catálogo global de GTIN colaborativo (lectura mayoritaria, moderado, acotado por país)
- Modo lector USB/BT en HID (foco exclusivo, no un escucha global de teclado)
- Impresora ESC/POS (Chrome de escritorio o Android seleccionado; no `window.print()`)
- Multicajero y turnos
- Compras, proveedores y utilidad neta
- Adaptador SUNAT y facturación electrónica
- Plantillas de WhatsApp Business con consentimiento explícito
- Pedido B2B hacia distribuidores
- Balanzas, WebSerial y Tauri

**Deltas de contrato registrados y no construidos.** Todos están declarados como notas docs-only en su documento dueño; este plan no los adelanta, salvo donde se indica.

| Delta | Contenido | Estado en este plan |
| :--- | :--- | :--- |
| D1 | `tenants.master_password_hash` | Adoptado en el contrato; su DDL aterriza en el Sprint 8 (Semana 8) junto con `POST /auth/admin-login` `(propuesta de secuencia)` |
| D4 | `fiado_accounts.credit_limit_cents` | Adoptado en el contrato, sin DDL: el Sprint 7 (Semana 7) no renderiza el aviso de límite |
| D5 | `stock_adjustments.evidence_photo_url` | Diferido: el registro de merma no tiene captura de foto |
| D10 | `cash_closes.opening_float_cents` y egresos | Diferido: el formulario de cierre no tiene fondo inicial ni egresos |
| D11 | `products.cost_cents` y margen comercial | Diferido: sin márgenes ni alertas de costo |
| D12 | `GET /sales` / `GET /dashboard/summary` y tabla de asistencia | Diferido: el dashboard Admin usa el ledger local y `GET /fiado-accounts` |

Fuente de los deltas y de su estado: `docs/BACKEND.md` §11.2, con el detalle de cada columna futura en la nota delta de la tabla correspondiente de `docs/DATABASE.md` §4.

---

## 12. Leyenda del tablero

| Estado | Significado |
| :--- | :--- |
| Todo | No iniciado |
| Doing | Un solo Owner, en progreso |
| Blocked | Esperando una decisión o una persona con nombre |
| Done | El criterio de aceptación es demostrablemente cierto |

Cada fila de §7 se mueve por estas cuatro columnas y aporta, al pasar a Done, el runner o el recorrido concreto que la demuestra.
