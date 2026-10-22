# Recuperación y sincronización del plan gratuito

Este documento responde dos preguntas concretas del Sprint 5: **qué se pierde y qué
sobrevive en el plan gratuito**, y **cómo se recupera el historial cuando el teléfono se
pierde o se reinstala**. Distingue explícitamente lo que el contrato ya sirve hoy de lo que
todavía no existe, para no prometer una recuperación que la versión actual no ejecuta.

Nada de lo que sigue inventa endpoints: cada afirmación cita el contrato que la sostiene.
Lo que no está fijado por las fuentes se marca como `(propuesta)`.

---

## 1. El modelo de sincronización del plan gratuito

### 1.1 La venta se cierra en el dispositivo

El cajero cierra la venta en el teléfono. El cliente calcula y **congela** los valores
—`total_cents`, `line_total_cents`, `qty`— y los envía tal cual: el servidor nunca recomputa
dinero (`docs/DATABASE.md` §2.3; `docs/BACKEND.md` §4.9). La venta cerrada se persiste
localmente **antes** del ack, para que matar la pestaña a mitad del envío no la pierda
(W5-FE-1, `SPRINTS.md` Sprint 5). El envío es un `POST /sales` encolable
(`docs/DATABASE.md` §7.6), con `Idempotency-Key` obligatoria.

### 1.2 El servidor es la copia durable cuando el claim se completa

Cada POST encolable reclama una clave en `idempotency_keys` dentro de **una sola
transacción** con su escritura de negocio (`docs/DATABASE.md` §7.1, MD-4). El COMMIT es
todo-o-nada: la fila del claim y la venta nacen o mueren juntas, y la fila confirmada queda
`completed` **con su snapshot de respuesta adjunto** —una CHECK acoplada al estado impide
que una fila `in_flight` se sirva como éxito fantasma (`docs/DATABASE.md` §4
`idempotency_keys`, F4)—.

Por eso la frontera de durabilidad es el **ack**: cuando el servidor respondió 200, la venta
viaja en PostgreSQL y en el snapshot del claim. Antes del ack, la única copia es la del
dispositivo.

```text
cierre local  ──►  cola local  ──►  POST /sales ──►  COMMIT (claim + venta + stock)
     │                 │                                    │
     │                 │                                    └─ copia durable del servidor
     │                 └─ copia local (IndexedDB)
     └─ ticket en pantalla
```

### 1.3 "Respaldo" no es lo mismo que "el ledger ya vive en PostgreSQL"

Son dos cosas distintas y el plan gratuito solo tiene una:

| Concepto | Qué es | Plan gratuito |
| :--- | :--- | :--- |
| **Respaldo cloud diario** | Una copia programada que permite **restaurar** la base a un punto anterior | **No** — es una capacidad del plan Estándar (`docs/BUSINESS.md:50`) |
| **El ledger en PostgreSQL** | El registro append-only del servidor: `sales`, `sale_lines`, `stock_adjustments`, `cash_closes`, `cash_close_counts` (`docs/DATABASE.md` §4) | **Sí** — es donde el servidor escribe cada venta confirmada |

En el plan gratuito, entonces, **el servidor es la única copia durable y no hay una segunda
copia**. Que la venta sobreviva a la pérdida del teléfono no viene de un respaldo: viene de
que la fila ya está en el ledger del servidor. Y si esa base se perdiera, el plan gratuito no
tiene mecanismo de restauración; la política de respaldo y PITR está declarada `(propuesta)`
y **no** fijada por las fuentes (`docs/BACKEND.md` §7.8).

---

## 2. Qué sobrevive y qué no

| Escenario | Qué sobrevive | Qué se pierde |
| :--- | :--- | :--- |
| Se mata la pestaña a mitad de la cola | Las ventas **cerradas y persistidas localmente antes del ack** (W5-FE-1), que se reenvían al reiniciar | Nada de lo cerrado; el envío en vuelo se reanuda |
| El servidor confirma y después se pierde el teléfono | Todo lo confirmado: está en PostgreSQL y en el snapshot del claim | Nada del historial confirmado |
| Se borra el teléfono o se reinstala la PWA con eventos **sin confirmar** | Lo ya confirmado por el servidor | Las ventas cerradas localmente que **nunca llegaron** al servidor: mueren con el dispositivo |
| Se pierde la base del servidor en el plan gratuito | — | Todo: no hay respaldo cloud diario en ese plan (`docs/BUSINESS.md:50`) |

El límite honesto, y el que hay que decirle al usuario: **una venta que se cerró en el
teléfono y nunca se confirmó en el servidor no se puede recuperar**. No existe una copia
mágica. A partir del ack, sí: el evento vive en el servidor y sobrevive a la pérdida del
dispositivo (`ARCHITECTURE.md:168`).

---

## 3. Recuperación de teléfono perdido

### 3.1 El recorrido

1. **Instalar o abrir la PWA en el teléfono nuevo.** No hay estado previo: el
   almacenamiento local está vacío.
2. **`POST /auth/login`** con teléfono y contraseña (`docs/BACKEND.md` §4.2). La respuesta es
   el token de empleado con `tenant_id` y `user_id` y sin claim de rol
   (`ARCHITECTURE.md:23`). El teléfono es la identidad de login y es globalmente único
   (`docs/DATABASE.md` §4 `users`). El cuerpo **no** lleva tenant: el tenant llega solo
   dentro del token.
3. **`POST /auth/device-bind`** con el PIN (`docs/BACKEND.md` §4.3): enlaza el teléfono nuevo
   al cajero y fija `offline_session_valid_until`, que se refresca en cada autenticación
   online (`docs/DATABASE.md` §4 `devices`). A partir de acá el dispositivo puede operar
   offline dentro de esa ventana.
4. **Reconstrucción local.** Ver §3.2: es donde el contrato actual se queda corto y hay que
   decirlo.

### 3.2 Qué sirve el contrato hoy y qué sigue pendiente

| Pieza | Estado en esta versión | Fuente |
| :--- | :--- | :--- |
| Identidad (login + token de empleado) | **Implementado** | `docs/BACKEND.md` §4.2 |
| Dispositivo y ventana offline (bind) | **Implementado** | `docs/BACKEND.md` §4.3 |
| Catálogo del tenant (`GET /products`) | **Implementado** — es una lectura completa del catálogo del tenant, sin volcado global | `docs/BACKEND.md` §4.5 |
| Venta, anulación y cierre de caja (escritura) | **Implementado** | `docs/BACKEND.md` §4.9, §4.10, §4.13 |
| Descarga incremental del catálogo y perfiles (`GET /sync/cursor`) | **Pendiente: Sprint 6.** Además, el cursor **no transporta dinero ni stock**: solo ediciones de catálogo y perfil | `docs/BACKEND.md` §4.8; `docs/DATABASE.md` §7.3 |
| Re-descarga del historial de ventas | **No existe en esta versión.** No hay `GET /sales` ni endpoint de reporte agregado | `docs/BACKEND.md` §4.15, §11.3 |
| Cuentas de fiado (`GET /fiado-accounts`) | **Pendiente: Sprint 7** | `docs/BACKEND.md` §4.12 |

Conclusión operativa, sin adornos: tras el re-login y el bind, **el historial no se pierde
porque ya está en el servidor, pero esta versión no tiene todavía un camino para volver a
descargarlo al dispositivo**. El catálogo sí se puede repoblar con `GET /products`; el
historial de ventas espera al pull de Sprint 6 o a un `GET /sales` que hoy es no-meta
explícita. Cualquier interfaz que prometa "restaurar el historial" en esta versión estaría
prometiendo algo que el backend no sirve.

---

## 4. Verificación manual de borrado y reinstalación (S4-4)

El criterio del sprint es un recorrido observable, no una afirmación: se borra el
almacenamiento del dispositivo y se comprueba qué queda del lado del servidor y qué puede
volver.

### 4.1 Preparación

1. `docker compose up -d` (PostgreSQL local) y la API arriba.
2. En el teléfono (o en el navegador con el perfil móvil), iniciar sesión y vincular el
   dispositivo.
3. Cerrar **tres ventas** con métodos distintos (por ejemplo `cash`, `yape`, `fiado`) y
   **anular una** de ellas. Anotar los ids de venta y de anulación.
4. Confirmar del lado del servidor que las cuatro operaciones están. La consulta de control
   es `SELECT count(*) FROM sales` y `SELECT count(*) FROM idempotency_keys WHERE state =
   'completed'`: manual, con el rol de migración y `psql` contra la base del servidor (no es
   una prueba automatizada — las pruebas del repositorio solo usan bases `pos_test_`
   desechables). Lectura esperada: 4 filas en `sales` (3 ventas + 1 anulación) y 4 claims
   completados.

### 4.2 Borrado y reinstalación

5. Borrar el almacenamiento del sitio (DevTools → Application → Clear site data) o
   desinstalar y reinstalar la PWA.
6. Recargar. **Resultado esperado:** la aplicación arranca sin carrito y sin proyección
   local; el dispositivo se comporta como nuevo.
7. Iniciar sesión de nuevo con teléfono y contraseña. **Resultado esperado:** 200 con el
   token de empleado; ningún 401.
8. Vincular el dispositivo con el PIN. **Resultado esperado:** 200 con el dispositivo y su
   `offline_session_valid_until` en el futuro.
9. Repetir la consulta de control del paso 4. **Resultado esperado:** exactamente los mismos
   conteos. El servidor no perdió nada: el borrado fue del dispositivo, no del ledger.
10. Comprobar qué reconstruye la interfaz. **Resultado esperado en esta versión:** el catálogo
    puede repoblarse con `GET /products`; el historial de ventas **no** se re-descarga, porque
    no existe el endpoint (§3.2). La interfaz debe mostrar un historial vacío o local, no uno
    inventado.

### 4.3 Control negativo: el límite declarado

11. Con el servidor inalcanzable (red cortada), cerrar una venta y **no** reintentar.
12. Repetir el borrado del paso 5.
13. **Resultado esperado:** la venta del paso 11 no está en el servidor —nunca hubo ack— y
    tampoco en el dispositivo. La consulta de control del paso 4 devuelve los mismos conteos
    que antes del paso 11. Este es el límite honesto de §2: sin ack no hay copia durable.
14. Restablecer la red y confirmar que una venta nueva sí se confirma y sobrevive a un nuevo
    borrado (pasos 3-9).

### 4.4 Qué mide este recorrido

| Observación | Conclusión |
| :--- | :--- |
| Los conteos del servidor no cambian tras el borrado | El ledger vive en PostgreSQL y no depende del dispositivo |
| El re-login y el bind funcionan en un dispositivo nuevo | La identidad y la ventana offline son recuperables |
| El historial de ventas no vuelve al dispositivo | Falta el pull (Sprint 6) o un `GET /sales` que hoy no existe |
| La venta sin ack se pierde con el borrado | La durabilidad empieza en el COMMIT del claim, no antes |

---

## 5. Referencias

- `ARCHITECTURE.md:41` — el cierre de caja es una proyección del ledger, nunca un segundo
  total corriente.
- `ARCHITECTURE.md:132`, `:135` — la anulación es un evento compensatorio y el cierre es
  idempotente; la historia no se borra.
- `ARCHITECTURE.md:168` — una venta cerrada se recupera tras matar la pestaña y tras perder
  el teléfono.
- `docs/DATABASE.md` §4 (`idempotency_keys`, `sales`, `cash_closes`), §7.1 (protocolo de claim),
  §7.6 (POST encolables).
- `docs/BACKEND.md` §4.2 (login), §4.3 (device-bind), §4.5 (`GET /products`), §4.8
  (`GET /sync/cursor`, Sprint 6), §4.12 (`GET /fiado-accounts`, Sprint 7), §4.15 (no hay
  `GET /sales`), §7.8 (respaldo `(propuesta)`).
- `docs/BUSINESS.md:50` — el respaldo cloud diario es capacidad del plan Estándar.
- `SPRINTS.md` Sprint 5 (W5-BE-4) y Sprint 6 (sincronización offline).
