package photos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Este archivo implementa la mitad de datos de las fotos (W3-BE-3, docs/BACKEND.md
// §10.3): la abstracción de almacenamiento de objetos del contrato.
//
// El cuidado central es que la BASE DE DATOS no guarda bytes: `products.photo_url` es TEXT
// (docs/DATABASE.md §4) y acá el store es la única pieza que toca el almacenamiento de
// objetos. La interface una, locales y sin SDK nuevo (docs/BACKEND.md §10.3: "filesystem
// mvp over a Store interface"), para que un swap a SDK de objetos reales no toque ni al
// handler ni a la superficie.
//
// Dos cosas que este package nunca hace, porque el contrato lo prohíbe:
//
//   - tocar la base de datos: PUT devuelve una URL y NADA más —la integración con la fila
//     de producto es materia de la superficie de /products, que ya recibe photo_url—;
//   - aceptar cualquier tipo de contenido: la lista del contrato es CERRADA (jpeg, png,
//     webp) y la comparte el handler para rechazar temprano con el campo nombrado.

// Store es la caja fuerte del contrato: la única forma por la que el módulo guarda bytes,
// con el tenant y el contenido, y devuelve la URL pública con la que el cliente
// referenciará la foto después en photo_url (§10.3).
type Store interface {
	Put(ctx context.Context, tenantID uuid.UUID, photo []byte, contentType string) (string, error)
}

// contentExtensions es la lista CERRADA de tipos de contenido del contrato y su extensión
// de archivo: es la única tabla, compartida por el handler (rechazo temprano) y por el
// store (nombre del archivo).
var contentExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// Extension devuelve la extensión del contrato para un tipo de contenido aceptado y dice
// si el tipo está en la lista cerrada. Es la misma tabla para las dos mitades.
func Extension(contentType string) (string, bool) {
	extension, ok := contentExtensions[strings.TrimSpace(strings.ToLower(contentType))]
	return extension, ok
}

// LocalStore guarda cada foto como archivo del sistema de archivos, en un directorio por
// tenant y con nombre uuidv7. La dirección pública que devuelve es la MISMA forma de la
// URL que el almacenamiento de objetos real representará: <base>/photos/<tenant>/<nombre>.
type LocalStore struct {
	dir     string
	baseURL string
}

// NewLocalStore valida la dirección del almacenamiento (dónde y con qué base pública) y
// crea el directorio raíz si no existe.
func NewLocalStore(dir, baseURL string) (*LocalStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("the object storage needs a directory")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("the object storage needs a public base url")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create the object storage dir %q: %w", dir, err)
	}
	return &LocalStore{dir: dir, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

// Put guarda la foto ENTERA (sin recortes ni transcodificación: el objeto es exactamente
// el blob del cliente) y devuelve la URL pública del objeto. El nombre lo inventa el store
// —uuidv7, ordenable por tiempo de subida— y el tipo de contenido decide solo la
// extensión: el nombre del cliente no entra jamás en el archivo, por dónde puede colar
// un path traversal.
func (s *LocalStore) Put(ctx context.Context, tenantID uuid.UUID, photo []byte, contentType string) (string, error) {
	if s == nil {
		return "", errors.New("the object storage is not built")
	}
	if tenantID == uuid.Nil {
		return "", errors.New("the photo upload needs a tenant")
	}
	if len(photo) == 0 {
		return "", errors.New("the photo upload needs a photo body")
	}

	extension, ok := Extension(contentType)
	if !ok {
		return "", fmt.Errorf("the content type %q is not in the accepted list", contentType)
	}

	name, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate the object name: %w", err)
	}
	filename := name.String() + extension

	tenantDir := filepath.Join(s.dir, tenantID.String())
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		return "", fmt.Errorf("create the tenant dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tenantDir, filename), photo, 0o644); err != nil {
		return "", fmt.Errorf("write the photo: %w", err)
	}

	return s.urlOf(tenantID, filename), nil
}

// urlOf arma la dirección pública del objeto. El plano HTTP siempre usa /, aun cuando el
// archivo de abajo viva en una ruta de Windows.
func (s *LocalStore) urlOf(tenantID uuid.UUID, filename string) string {
	return s.baseURL + "/photos/" + tenantID.String() + "/" + filename
}
