// Package photos implementa el módulo Photos: la foto de un producto como URL de object
// storage.
//
// Tablas que posee: ninguna. La base guarda solo la URL en `products.photo_url`, y los
// bytes viven en object storage (ARCHITECTURE.md:21,87; docs/BACKEND.md §2.2).
//
// Frontera: no posee identidad de producto. Este módulo no decide qué producto es una
// foto ni escribe `products`: produce y valida URLs, y el catálogo es quien las guarda.
// Ningún módulo escribe las tablas de otro módulo (ARCHITECTURE.md:31).
//
// Sprint 1 solo declara la frontera: la carga y la emisión de URLs son Sprint 3
// (docs/BACKEND.md §10.3, §7.5).
package photos
