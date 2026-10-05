# dual-login-mermas-cleanup

Rediseño del acceso y cierre de brechas en el proyecto Stitch `POS SaaS Admin Clean` (`projects/1977626098118198984`): (1) nuevo **login dual en 2 columnas** que reemplaza P1+P2 — izquierda con imagen/fondo limpio de marca, derecha con anchors de rol (Empleado/Administrador) y formulario dinámico; el sistema detecta el tipo de usuario y otorga permisos; (2) pantalla dedicada **Mermas y Daños** (destino del nav sin pantalla propia); (3) lista canónica de exportación (los duplicados no se pueden borrar por MCP — se excluyen del export); (4) re-verificación de Inventario (P7 ya verificado limpio).

## Problem

- El login actual son 3 pantallas separadas (P1 selector + P2 Admin + P2 Empleado). El usuario quiere el clásico login partido: mitad marca, mitad formulario con anchors de rol.
- "Mermas y Daños" es destino del nav (6 destinos) pero nunca tuvo pantalla propia: P7 cubre inventario + modal de merma, no el historial/registro dedicado.
- El proyecto nuevo ya acumuló un duplicado (2 Dashboards: `97171a2c` actual + `bc82441f` viejo). Stitch MCP no expone rename ni delete: los duplicados se excluyen del export con lista canónica exacta.

## Why

Decisión explícita del usuario (2026-10-05): un solo split login + sistema otorga permisos por rol detectado; Mermas y Daños pendiente como sección; usar impeccable enfocado al buen diseño del login; ciclo ODD.

## Scope

Dentro:
1. Generar pantalla Login Dual split (izquierda marca/imagen limpia, derecha anchors de rol + formulario dinámico PIN vs teléfono+maestra). Reemplaza P1/P2A/P2E en la lista canónica.
2. Generar pantalla Mermas y Daños dedicada (historial de mermas + entrada de registro + empty state), anclada a stock_adjustments (motivos exactos, sin foto por D5 diferido).
3. Verificación G1/G2 por HTML de ambas + rondas acotadas (≤2 por pantalla).
4. Lista canónica final de exportación (9 pantallas) + batería posterior sobre el Figma nuevo.

Fuera: tocar P3-P8 existentes (ya aceptados); borrar/renombrar en Stitch (imposible por MCP); código de repo; PRs.

## Constraints

- Tokens Block T §2.1 + tipografía §2.2 + radios §2.3 (DESIGN.md). UI copy español neutro profesional, sin voseo.
- Login: viewport 1440x900; columna izquierda SOLO marca/motivo abstracto + conectividad (sin dashboards falsos con datos inventados — anti-slop); columna derecha con tabs de rol accesibles por teclado.
- Auth-admin: password maestra por tenant; login identifier = teléfono (sin email). JWT role claim ya existe en arquitectura.
- Mermas: motivos EXACTOS `Vencimiento` / `Rotura en transporte` / `Daño en vitrina`; sin foto (D5); sample data solo Pan/Huevos/Hielo y la proyección documentada (Pan qty 2).
- Cero writes al repo salvo este doc. Sin commits sin confirmación (permiso `git commit` = ask). Sin PRs (decisión vigente del usuario: solo documentación).

## Tasks

- [x] **D1** Generar Login Dual split (Stitch, flash-lite) — reemplaza P1/P2A/P2E — HECHO 2026-10-05: screen `849b707f...` (`POS SaaS - Selección de Rol y Login Dual`), G1/G2 PASS a la primera
- [x] **D2** Generar Mermas y Daños dedicada (Stitch, flash-lite) — HECHO 2026-10-05: screen `85d03bb7...`, G1/G2 PASS a la primera (1 fila Pan −2, modal 3 motivos, sin foto)
- [x] **D3** Verificación G1/G2 por HTML de D1+D2 + rondas acotadas (≤2 c/u) — HECHO: ambas PASS sin rondas (residual leve: fecha 26/10/2026 no pineada en spec)
- [x] **D4** Lista canónica final + commit del doc (`6f89194`) + resumen para batería Figma — HECHO 2026-10-05
  - Login Dual `849b707f...` (reemplaza P1/P2A/P2E) · P3 `c845efa5...` · P4 `97171a2c...` · P5 `088ce929...` · P6 `ebc6c5f8...` · P7 `c924b742...` · P8 `c225d39c...` · Mermas `85d03bb7...`
  - Excluir TODO lo demás del proyecto (duplicados, legacy, P1/P2 viejos)

## Authorized scope

Stitch project `projects/1977626098118198984` (generate/edit/get/list); Engram mirror; este doc. Prohibido: borrar/renombrar pantallas (inexistente en MCP), Figma write (read-only), repo fuera de este doc.

## Acceptance criteria

- [ ] Login Dual: 1 frame 1440x900, 2 columnas (marca | formulario con anchors de rol), PIN funcional visual para Empleado, teléfono+maestra+INGRESAR para Admin, cero baneados, cero datos inventados en el panel de marca.
- [ ] Mermas y Daños: shell canónico + historial + motivos exactos + empty state, cero baneados, sin foto, sin costos.
- [ ] G1/G2 PASS en ambas (HTML observado, no reportes).
- [ ] Lista canónica de 9 IDs para export + batería.

## Checks applicable

- G1 banned-literal scan (lista maestra obs 371) sobre HTML observado.
- G2 doc-strings presentes (copy §7.1 verbatim donde aplique; strings nuevos del split marcados como user-approved).
- G3 geometría 1440x900 best-effort (autoridad: batería Figma).
- TDD: STRICT TDD MODE ACTIVE por sdd-init (user policy) — N/A aquí: cero código, sin runners en disco; verificación estática únicamente, sin fabricar evidencia.

## Progress

- 2026-10-05: doc creado; decisión login single-split cerrada por el usuario; impeccable cargada (launcher N/A: sin app local; aplican principios Operate/onboard/clarify).
