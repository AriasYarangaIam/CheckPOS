// Package identity implementa el módulo Identity: el registro de inquilinos y las
// cuentas que cierran ventas.
//
// Tablas que posee: `tenants`, `users` y `devices` (docs/DATABASE.md §4, grupo 1).
//
// Frontera: no posee ventas ni catálogo. `tenants` es la raíz del registro y queda fuera
// del RLS de tenant; `users.phone` es globalmente UNIQUE porque el login no lleva
// discriminador de tenant, y el tenant llega únicamente dentro del token
// (docs/BACKEND.md §3.2).
//
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31). Un paquete de
// módulo tampoco importa el repositorio de otro: si otro módulo necesita un producto o un
// usuario, lo pide a su servicio, nunca a su SQL (docs/BACKEND.md §2.4).
//
// Sprint 1 solo declara la frontera. La emisión y verificación del JWT, el device-bind y
// el PIN de la ventana offline son Sprint 2 (docs/BACKEND.md §10.2).
package identity
