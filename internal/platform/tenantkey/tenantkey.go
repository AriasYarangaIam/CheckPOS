// Package tenantkey construye las claves de Redis del proceso.
//
// Es el ÚNICO productor de claves de la base de código: toda clave es `tenant:{id}:...`, con el
// id del tenant del token adelante y las partes del nombre detrás (ARCHITECTURE.md:20, :86;
// docs/BACKEND.md §7.4; S1-4 y W2-BE-4 de SPRINTS.md §7).
//
// La regla no es estética. Redis es caché y cola, nunca fuente de verdad, y una clave sin el
// prefijo del tenant es la forma en que un caché compartido se convierte en una fuga de datos
// entre bodegas: dos tenants con el mismo nombre de catálogo, el mismo teléfono o el mismo
// cursor tienen que caer en claves distintas, y la única manera de garantizarlo es que el prefijo
// sea obligatorio y que nadie lo escriba a mano en otro lado.
//
// `tenantkey` no depende de ningún cliente de Redis: no hay instancia ni dependencia en este
// repositorio todavía (decisión D7), y este paquete existe antes que el cliente justamente para
// que el primer cliente que llegue tenga un solo lugar donde buscar la forma de la clave. La
// revisión que lo mantiene único es `review_test.go`, en este mismo directorio.
package tenantkey

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// prefix es el encabezado obligatorio de toda clave. ARCHITECTURE.md:20 y :86 lo fijan como
// `tenant:{id}:...`: literal, en minúsculas y con los dos puntos, sin variantes.
const prefix = "tenant:"

var (
	// ErrNilTenant es el rechazo de un tenant nulo. No es una formalidad: el UUID nulo no es el
	// id de ninguna fila del esquema, así que una clave construida con él no pertenecería a
	// ningún tenant y —al no llevar el id de uno real— sería exactamente el tipo de clave
	// compartida que la regla del prefijo existe para impedir.
	ErrNilTenant = errors.New("the tenant id is the nil uuid: a key without a real tenant is shared state between tenants")

	// ErrEmptyPart es el rechazo de un conjunto de partes vacío o de una parte en blanco. Sin
	// partes, la clave es `tenant:{id}:`, que no nombra nada; con una parte en blanco, la clave
	// lleva un segmento vacío que ninguna lectura posterior puede reconstruir sin ambigüedad.
	ErrEmptyPart = errors.New("a tenant key needs at least one non-empty part")
)

// Key construye la única forma de clave del contrato: `tenant:{id}:{part}:{part}`.
//
//	Key(id, "catalog", "global-product", gtin) -> "tenant:019a...:catalog:global-product:750..."
//
// El id del tenant es la primera parte después del prefijo y no se puede omitir. Las partes se
// unen con `:` en el orden recibido y se escriben tal cual: no se recortan ni se escapan. Una
// parte puede contener `:` —el invariante que el contrato fija es el PREFIJO, y una parte
// compuesta sigue estando bajo el tenant—, pero una parte vacía se rechaza porque produce un
// segmento que no se puede leer de vuelta.
//
// Devuelve error, y no una cadena vacía con un log, para que el llamador tenga que decidir qué
// hacer: una clave construida con un tenant nulo es un defecto de programación, no una entrada
// inválida de un usuario.
func Key(tenantID uuid.UUID, parts ...string) (string, error) {
	if tenantID == uuid.Nil {
		return "", ErrNilTenant
	}
	if len(parts) == 0 {
		return "", ErrEmptyPart
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return "", fmt.Errorf("%w: %q", ErrEmptyPart, part)
		}
	}

	return prefix + tenantID.String() + ":" + strings.Join(parts, ":"), nil
}
