# PILOT — Kit operativo del piloto de campo (Sprint 9 · Semana 9 y Sprint 10 · Semana 10)

`docs/PILOT.md` es el **instrumento operativo** de los Sprints 9 y 10: las planillas que el equipo de
campo llena para que el piloto produzca la evidencia que el plan exige. No fija arquitectura ni
esquema (`ARCHITECTURE.md` y `docs/DATABASE.md` mandan) y **no agrega alcance**: cada planilla existe
porque una fila o un criterio de `SPRINTS.md` lo pide. Este documento es un plan, así que pertenece al
conjunto en español (`SPRINTS.md:5`).

> **Regla que gobierna todo el kit.** Nada se afirma sin haberse medido: «los percentiles, los tiempos
> de alta y las métricas de flush son objetivos a registrar durante el piloto, no resultados»
> (`SPRINTS.md`, Sprint 9, nota de honestidad de medición). Una casilla vacía se deja vacía; no se
> rellena con una estimación.

---

## 1. Alcance de este kit

| Dentro de este kit | Fuera de este kit |
| :--- | :--- |
| Checklist previo al día 1 (§2) | El diseño del producto (`docs/DESIGN.md`) |
| Alta cronometrada en sus dos modos (§3, `W9-FE-1`) | La operación técnica de respaldo: `docs/RECOVERY.md` §6 |
| Bitácora de los siete días (§4, `W9-BE-2`) | El contrato de datos y el backend (`ARCHITECTURE.md`, `docs/BACKEND.md`) |
| Registro de percentiles por dispositivo (§5, `W9-FE-2`) | Los epics posteriores, que siguen estacionados hasta §6 |
| Cierre del piloto y lista de corte (§6, Sprint 10) | Cualquier promesa de despliegue o fecha comercial |

---

## 2. Checklist previo al día 1

Ninguna fila del piloto empieza antes de que esto esté completo. Cada casilla se marca con la
evidencia, no con la intención.

**Producto y despliegue**

- [ ] Los PR del bloque técnico están fusionados: Sprint 8 en `main` (#64) y bloque técnico del
      Sprint 9 en `develop` (#65).
- [ ] PWA desplegada y abierta en cada teléfono del piloto desde la URL de producción.
- [ ] Un tenant real dado de alta, con `tenants.master_password_hash` configurado (login Admin).
- [ ] El catálogo de la bodega cargado: al menos **20 favoritos** con foto, nombre y precio
      (el conteo de 20 favoritos viene del plan anterior; `SPRINTS.md`, Sprint 9, fila `W9-FE-1`).
- [ ] Venta de prueba en línea y una venta con red caída que drena sola al reconectar.

**Respaldo (antes de la primera venta del piloto)**

- [ ] `maintenance backup --dir` programado como instantánea diaria (`docs/RECOVERY.md` §6.2).
- [ ] **Ensayo de restauración ejecutado y registrado** dentro de la ventana del piloto
      (`SPRINTS.md`, Sprint 9, fila `W9-DB-1`; registro en `docs/RECOVERY.md` §6.6, §5 abajo).

**Equipo y medición**

- [ ] Teléfono Android de gama baja disponible para la medición de escaneo, **menor a US$100**
      (`SPRINTS.md`, Sprint 9, fila `W9-FE-2`).
- [ ] Cronómetro (o reloj con segundos) por cada tienda para la alta cronometrada.
- [ ] Una persona designada por tienda para llenar §3 y §4; una sola persona firma el día.
- [ ] Los dispositivos tienen `device_id` vinculado (la tarjeta de desempeño y el percentil por
      dispositivo dependen de esa identidad).

---

## 3. `W9-FE-1` — Alta cronometrada (dos criterios, ambos exigibles)

> | `W9-FE-1` | FE | Alta cronometrada de una bodega en sus dos modos: autoservicio (registro, carga de 20 favoritos y primera venta) y asistida en el mostrador | Dos criterios cronometrados en vivo sobre un Android de gama baja real: menos de 10 minutos de alta autoservicio sin ayuda, y 5 minutos de alta asistida en el teléfono del propio bodeguero (`docs/BUSINESS.md`) |

> **Nota de decisión (2026-10-14), conservada verbatim:** el criterio heredado medía el alta
> autoservicio en menos de 10 minutos, mientras `docs/BUSINESS.md` declara un alta asistida en el
> mostrador de 5 minutos. Decisión del Product Owner: **se conservan como dos criterios separados y
> ambos son exigibles**, porque miden cosas distintas. Ninguno de los dos sustituye al otro ni se
> promedia con el otro.

**Cómo se cronometra.** El cronómetro arranca en la primera interacción y se detiene cuando se
cumple la condición de la fila; cualquier paso que el equipo de campo hace **en lugar del bodeguero
no cuenta como autoservicio** — ese intento se anota como asistido.

| Modo | Condición de arranque | Condición de parada | Objetivo |
| :--- | :--- | :--- | :--- |
| **Autoservicio** | El bodeguero toca la primera pantalla solo | Primera venta cerrada con registro + 20 favoritos cargados | **< 10 min**, sin ayuda |
| **Asistida** | El equipo de campo empieza en el mostrador | Primera venta cerrada en el teléfono del propio bodeguero | **≤ 5 min**, cronometrado en campo |

**Registro por intento**

| Fecha | Tienda | Modo | Tiempo | ¿Autoservicio sin ayuda? | Dónde se atascó | Observaciones |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
|  |  | autoservicio / asistida | `mm:ss` | sí / no |  |  |
|  |  |  |  |  |  |  |

> El dolor que justifica que la alta sea corta está en `docs/PRODUCT.md` (la barrera del inventariado
> inicial desmotiva al 90 % de los comerciantes). Si el alta tarda, se registra **dónde** tardó: esa
> es la evidencia que después usa la lista de corte (§6).

---

## 4. `W9-BE-2` — Bitácora de los siete días corridos

> | `W9-BE-2` | BE | Arrancar la operación de siete días consecutivos: ventas, fiados y cierre de caja con sincronización después de cada corte | La operación de siete días arranca y las jornadas registradas hasta el cierre de este bloque quedan sin duplicados silenciosos ni fugas cross-tenant |

**Regla del bloque:** la operación de siete días **arranca dentro de la semana** del Sprint 9 y se
**cierra** en el Sprint 10, con al menos **un corte de red por jornada**
(`SPRINTS.md`, Sprint 9 y Sprint 10, criterios de aceptación).

**Corta de red por jornada.** Un corte se provoca y se observa, no se supone: modo avión durante una
venta, o apagar el router. Lo que se verifica cada vez: la venta se cierra igual, el badge de
sincronización muestra la cola, al reconectar drena sola y **el cajero no ve ningún error, prompt ni
reintento manual** (`ARCHITECTURE.md:178`).

**Planilla diaria — una fila por jornada, firmada por la persona designada**

| Día | Fecha | Ventas cerradas | Cierres de caja | Cortes de red (≥1) | ¿Venta recuperada tras matar la pestaña? | ¿Venta recuperada tras reinstalar? | Duplicados observados | Resoluciones silenciosas observadas | Notas |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 1 |  |  |  |  | sí/no + cuál | sí/no + cuál | 0 / detalle |  |  |
| 2 |  |  |  |  |  |  |  |  |  |
| 3 |  |  |  |  |  |  |  |  |  |
| 4 |  |  |  |  |  |  |  |  |  |
| 5 |  |  |  |  |  |  |  |  |  |
| 6 |  |  |  |  |  |  |  |  |  |
| 7 |  |  |  |  |  |  |  |  |  |

**Revisión de estados en campo.** Junto con la fila del día se comprueba que los estados de
`docs/DESIGN.md` §5.1 se comporten como la especificación: badge ámbar «X ventas por sincronizar» y
verde con «Sincronizado» al reconectar; permiso de cámara denegado con su tarjeta y fallback a la
grilla; código no encontrado con frame rojo + beep + vibración; `COBRAR` deshabilitado con carrito
vacío; ticket con check, beep y vibración. La **reconciliación silenciosa** es el estado que más
importa: el servidor manda y el POS **nunca** muestra un diálogo de conflicto.

**Lo que nunca se registra como «problema del cajero»:** un error de sincronización en pantalla.
Eso no existe por contrato; si aparece, es un defecto y se abre como tal.

---

## 5. `W9-FE-2` y los percentiles — registro de mediciones

> | `W9-BE-1` | BE | Instrumentar y medir los percentiles 50 y 95 del cierre de venta y de la latencia de flush, y comparar el percentil 95 del escaneo contra el umbral de 3 segundos | Métricas registradas por dispositivo; la grilla sigue siendo el camino por defecto si el percentil 95 de la cámara supera 3 segundos |
> | `W9-FE-2` | FE | Medir el camino de escaneo a carrito en dispositivos de menos de US$100 | Muestras registradas por dispositivo, sin promediar sobre hardware distinto |

**Dónde se mira.**

- **Cliente (por dispositivo):** tarjeta «Desempeño del dispositivo» en el Dashboard Admin — p50 y p95
  de `scan` (detección → carrito) y de `flush` (inicio → ack), con el umbral de 3 s declarado como
  objetivo. Sin muestras dice «todavía no hay mediciones»: **no** se rellena con un cero.
- **Servidor (por ruta):** `GET /metrics` en texto Prometheus, con p50/p95 por ruta de negocio. Es
  ruta de operación, fuera de la superficie de negocio de `docs/BACKEND.md` §4.

**Regla de honestidad (S5/S6).** Ninguna latencia se declara como lograda sin medición previa, y las
cifras no se promedian entre hardware distinto: cada dispositivo tiene su propia fila.

| Fecha | Tienda | `device_id` | Modelo | Precio aprox. | Muestras `scan` | p50 escaneo | p95 escaneo | ¿p95 > 3 s? | Grilla como camino por defecto | Muestras `flush` | p50 flush | p95 flush |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
|  |  |  |  | US$ |  |  |  | sí/no | confirmado en campo:  |  |  |  |

**Registro del ensayo de restauración** (mismo formato de `docs/RECOVERY.md` §6.6; se copia acá para
que la bitácora del piloto quede en un solo lugar):

| Campo | Ejemplo |
| :--- | :--- |
| Fecha y hora (UTC) | 2026-11-25T03:20:00Z |
| Dump y manifiesto | `20261125T031500Z-pos.dump` / `.manifest.json` |
| SHA-256 del dump | el del manifiesto |
| Tablas verificadas | 14 de 14 |
| Resultado | `schema_match=true`, `row_count_match=true`, `dropped=true` |
| Operador | quien ejecutó el ensayo |

---

## 6. Cierre del piloto (Sprint 10 · Semana 10) y lista de corte

| ID | Capa | Trabajo (verbatim de `SPRINTS.md`) | Entregable |
| :--- | :--- | :--- | :--- |
| `W10-DB-1` | DB | Cierre del particionado del periodo del piloto y revisión de la remediación de la partición por defecto | El periodo del piloto tiene sus particiones mensuales y la partición por defecto queda vacía |
| `W10-BE-1` | BE | Cerrar los siete días y publicar la lista de corte: qué se corta y qué se construye después | Lista de corte firmada por el Product Owner, con evidencia del piloto |
| `W10-FE-1` | FE | Cierre del piloto: bitácora de los siete días y revisión de estados en campo contra la especificación de estados | Bitácora completa con cada jornada, cada corte de red y cada resolución silenciosa observada |

**Criterios de aceptación del cierre** (`SPRINTS.md`, Sprint 10, verbatim):

- Siete días corridos de operación completos, con al menos un corte de red por jornada; cada venta
  cerrada se recupera tras matar la pestaña y tras reinstalar.
- Cero duplicación de dinero o stock por reintento.
- Cero fugas cross-tenant: el test de aislamiento falla antes de RLS y pasa después.
- Las resoluciones silenciosas y los conflictos de las jornadas quedan revisados jornada por jornada.
- La lista de corte queda firmada y los epics posteriores siguen sin agendar hasta que esa evidencia
  exista.

**Lista de corte (plantilla).** Se llena con la evidencia de §4 y §5; la firma es del Product Owner.

| # | Qué se corta (no se construye) | Evidencia que lo decide (fila de bitácora / medición) | Firmado |
| :--- | :--- | :--- | :--- |
| 1 |  |  |  |
| 2 |  |  |  |

| # | Qué se construye después (prioridad) | Evidencia que lo habilita | Firmado |
| :--- | :--- | :--- | :--- |
| 1 |  |  |  |

> **Horizonte.** Los epics posteriores —catálogo colaborativo de GTIN, lector HID, ESC/POS,
> multicajero, compras, SUNAT, WhatsApp Business, balanzas— siguen **estacionados** hasta que esa
> lista firmada exista (`docs/BUSINESS.md`, *Horizonte de portafolio*; `SPRINTS.md` §11).

**Definition of Done del piloto** (`SPRINTS.md`, verbatim): «La bodega se da de alta en menos de
10 minutos en autoservicio y en 5 minutos asistida en el mostrador, los percentiles del piloto
quedan registrados y la operación de siete días ha arrancado con al menos un corte de red
observado.» Y para el cierre: «La bitácora de siete días está completa, la lista de corte está
firmada por el Product Owner y los epics posteriores siguen sin agendar.»

---

## 7. Quién hace qué

| Rol | En este kit | Responsable |
| :--- | :--- | :--- |
| Equipo de campo | Llena §3, §4 y §5 el mismo día, sin cerrar la casilla «por si acaso» | TBD |
| Owner técnico | Ejecuta y registra el ensayo de restauración (§2, §5) y revisa `GET /metrics` | TBD |
| Product Owner | Acepta o devuelve la bitácora y **firma** la lista de corte (§6) | TBD |
| Scrum Master | Protege los siete días corridos y el congelamiento de alcance | TBD |

> Los titulares siguen siendo `TBD` igual que en `SPRINTS.md` §3: asignarlos es una decisión de la
> organización, no de este documento.
