# Feature: Alineación de contrato + endurecimiento del Sprint 2

## Objetivo
Corregir las 4 divergencias medidas entre `docs/DATABASE.md` y lo implementado, y cerrar
los 3 warnings de la verificación adversarial (2 en `be-auth`, 1 en `fe`) **antes** de
arrancar el Sprint 3. Entregar el resultado como un PR revisable sobre `develop`.

## Problema
La verificación independiente y la medición real contra el contenedor mostraron que el
contrato escrito describe un comportamiento que PostgreSQL no tiene: `INSERT ...
RETURNING` rechazado por concesión (42501), un GUC ausente que **errora** (42704/22P02)
en lugar de filtrar, `FORCE RLS` que no liga a un dueño superusuario, y particiones de
`sales` que no heredan concesiones ni políticas. Además, el arranque del proceso no
detecta un intercambio de roles entre pools (fallo silencioso, no de fuga).

## Alcance
- `docs/DATABASE.md`: 4 correcciones **in-place** (§5.2, §5.4, §5.5, §5.7). El archivo
  `docs/DESIGN.md` queda intocado; las citas `:NNN` de terceros hacia `DATABASE.md`
  deben seguir resolviendo → cada edición reemplaza texto dentro de la misma línea,
  **delta de líneas = 0**.
- `W1` (be-auth, WARNING 1): aserción de rol esperado por pool al arrancar
  (`current_user` = `pos_app` / `pos_auth`); TDD estricto.
- `W2` (be-auth, WARNING 2): ya documentado por `4e90f74` y los comentarios de
  `config.go`; se cierra con evidencia + una frase en `.env.example` que nombra la
  aserción de arranque (viaja con `W1`, mismo commit).
- `W3` (fe, WARNING 3): test que guarda "un PIN incorrecto no hace ninguna llamada de
  red" (spy de fetch). TDD estricto.
- `W4` (fe, hallazgo SUGGESTION con mejor ROI): re-evaluar el veredicto `unlocked`
  cuando la fila de sesión se recarga (`EmployeeAccessScreen.tsx:93`), para que una
  sesión vencida o borrada no deje la pantalla desbloqueada.
- Comentarios y docs: en español neutral; identificadores/commits: en inglés.

## Fuera de alcance
- `docs/DESIGN.md` y sus punteros de línea (archivo protegido).
- `SPRINTS.md` (los refs hacia BACKEND.md de ese archivo quedan como están).
- Cambios de comportamiento en la migración ya aplicada; el default del marcador y las
  políticas ya son las correctas.

## Tareas
- [x] `CH-1` — Documento de feature + espejo en engram, y correcciones in-place a
      `docs/DATABASE.md` (delta de líneas = 0). → commits `b3de2e6` + `7078591`
      (`numstat 5/5`: punteros `:NNN` externos intactos).
- [x] `CH-2` — `fix(api)`: `AssertExpectedRole` por pool (`pos_app`/`pos_auth`) al
      arrancar + test de integración que falla primero + frase en `.env.example`.
      → commit `092186c` (RED observado; mutación invierte la comparación y el test
      falla en ambos lados; suite Go 6/6 ok).
- [x] `CH-3` — `test(web)`: spy de fetch que guarda 0 llamadas en el camino de PIN
      incorrecto. → commit `14b885e` (163→164 verdes; la falsificación con el fetch
      inyectado en `deviceSession.ts` rompe el guard en 1/15 de ese archivo).
      Hallazgo de propio paso: `deviceSession.ts` no importa ningún cliente HTTP
      (solo WebCrypto PBKDF2 + Dexie); `fetch` solo existe en `bindDeviceSession`.
- [x] `CH-4` — `fix(web)`: re-evaluar el veredicto `unlocked` al recargar sesión.
      → commit `e52abee` (2 RED observados: fila vencida y fila borrada bajan a
      `refused/window-expired` o `refused/no-session`; 166/166 verdes).
- [x] `CH-5` — Prueba de que cada corto se sostiene solo (build + suites por commit).
      → tabla en "Evidencia".

## Método (orden de trabajo)
1. `CH-1` (inline, lo cerró el orquestador): feature doc + espejo; 5 ediciones in-place
   a `docs/DATABASE.md` (delta de líneas = 0 verificado con `git diff --numstat`).
2. `CH-2`, después `CH-3`+`CH-4`: escritores delegados, uno por vez (mismo arbol de
   trabajo), TDD estricto con su runner; commits por unidad de trabajo en la rama
   `fix/sprint-2-contract-hardening` (base `develop` `368cfe7`).
3. Verificación final: suites completas (Go 6 pkgs, web vitest) + prueba por commit +
   smoke del proceso si cabe.

## Decisiones
- **D1** Cada párrafo corregido lleva la marca "Measured:"/"Corrected 2026-10-16:" y
  concluye con la conducta real; el lector ve el motivo sin abrir el registro de
  verificación. Las ediciones reemplazan texto dentro de la misma línea: ninguna
  referencia `:NNN` externa corre.
- **D2** La aserción nueva (`AssertExpectedRole`) reusa el archivo `runtime_role.go`
  para no duplicar helpers; `AssertLeastPrivilegeRole` no cambia de firma (comportamiento
  ya congelado por el corte de endurecimiento mergeado).
- **D3** El test de red espía el `fetch` global (lo que el módulo use), sin acoplarse a
  la implementación del cliente HTTP.
- **D4** El warning W2 cierra por documentación existente (`4e90f74`, comentarios de
  `config.go`) más la armadura de `W1`; "fallar rápido" ES `W1`.

## Estrategia de entrega
- Rama `fix/sprint-2-contract-hardening` ← `develop` (`368cfe7`), push como la corta
  única del fix; merge con commit de merge, sin squash (el repo usa merge commits).
- Previsión de líneas: ~240 (docs) + ~180 (Go) + ~110 (web) ≈ 530 total, 5 commits;
  cada corta es una unidad revisable. El apilado sobre merge-commit mantiene el diff.

## Verificación / evidencia por corto
- `CH-1`: `git show --numstat 7078591` → `5 5 docs/DATABASE.md` — delta de líneas 0,
  punteros `:NNN` externos (DESIGN.md/FRONTEND.md) intactos.
- `CH-2`: commit `092186c` (+136). RED = build del primer test `undefined:
  postgres.AssertExpectedRole`; suite Go 6/6 ok; mutación → el test falla por ambos
  caminos (`accepted ... 401 para siempre` y `rechazado y no debería`).
- `CH-3/4`: vitest 163 → 164 (`14b885e`) → 166 (`e52abee`); RED de `e52abee`
  (2 tests) observado y luego verde; falsificación guard de red mordiendo (1 fail).
- `CH-5`: cada uno de los 5 commits, checked-out aislado, pasa `go build` y
  `go test ./... -count=1` (6 ok / 0 FAIL); los que tocan web, además, vitest verde.
- Estado final de `develop` tras el merge: pendiente (usuario mergea).