package photos_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/photos"
)

// W3-BE-3 (docs/BACKEND.md §10.3): la foto es un ARCHIVO en el almacenamiento de objetos y
// la base solo guarda la URL (products.photo_url es TEXT). El store acá es la abstracción
// del contrato —una interface única, sin SDK nuevo— y el store local es el mismo contrato
// dado con el sistema de archivos: guardar es escribir el contenido EXACTO y devolver la
// URL pública con la que el cliente referenciará la foto.
//
// El store NO toca la base de datos: la URL que devuelve es la única pieza que algún día
// viajará en un cuerpo de producto, y esa integración es materia de /products (W3-BE-1),
// que ya acepta photo_url como campo opcional.

// photoTenantID es el tenant que sube las fotos en estas pruebas.
var photoTenantID = uuid.MustParse("019a0000-0000-7000-8000-0000000000f1")

// TestLocalStorePutsAPhotoAndReturnsAURL: guardar deja el contenido en disco INTACTO y
// devuelve la URL pública que nombra al tenant y termina con la extensión del tipo de
// contenido aceptado —jpeg, png y webp son la lista completa del contrato—.
func TestLocalStorePutsAPhotoAndReturnsAURL(t *testing.T) {
	cases := map[string]struct {
		contentType string
		extension   string
	}{
		"a jpeg": {contentType: "image/jpeg", extension: ".jpg"},
		"a png":  {contentType: "image/png", extension: ".png"},
		"a webp": {contentType: "image/webp", extension: ".webp"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := photos.NewLocalStore(dir, "http://photos:9000")
			if err != nil {
				t.Fatalf("NewLocalStore: %v", err)
			}

			content := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01}
			url, err := store.Put(context.Background(), photoTenantID, content, tc.contentType)
			if err != nil {
				t.Fatalf("Put: %v", err)
			}

			if got, want := url[:len("http://photos:9000/photos/")], "http://photos:9000/photos/"; got != want {
				t.Errorf("url = %q, want base %q + /photos/<tenant>/", url, "http://photos:9000")
			}
			if !strings.HasSuffix(url, tc.extension) {
				t.Errorf("url %q no termina con la extensión %q del tipo %s", url, tc.extension, tc.contentType)
			}
			if strings.Contains(url, "\\") {
				t.Errorf("url %q usa un separador de ruta de Windows en el plano HTTP", url)
			}

			// El archivo queda en el disco con el contenido EXACTO: en el tenant propio y con
			// un nombre que el store inventó (uuidv7), no el del cliente.
			tenantDir := filepath.Join(dir, photoTenantID.String())
			entries, err := os.ReadDir(tenantDir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("el tenant no tiene exactamente un archivo en el disco: %v (%d entradas)", err, len(entries))
			}
			stored, err := os.ReadFile(filepath.Join(tenantDir, entries[0].Name()))
			if err != nil {
				t.Fatalf("leer el archivo guardado: %v", err)
			}
			if string(stored) != string(content) {
				t.Errorf("el contenido del disco cambió al guardar: %v, want %v", stored, content)
			}
		})
	}
}

// TestLocalStoreRejectsAnUnknownContentType: la lista del contrato es CERRADA; un tipo que
// no está es un rechazo del store, sin escribir archivo alguno.
func TestLocalStoreRejectsAnUnknownContentType(t *testing.T) {
	dir := t.TempDir()
	store, err := photos.NewLocalStore(dir, "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	if _, err := store.Put(context.Background(), photoTenantID, []byte{0x01}, "image/gif"); err == nil {
		t.Fatal("Put con image/gif: no dio error, want el rechazo del tipo no listado")
	}
	if entries, readErr := os.ReadDir(dir); readErr == nil && len(entries) > 0 {
		t.Errorf("el store escribió un archivo al rechazar un tipo no listado")
	}
}

// TestLocalStoreRejectsAnEmptyPayload: una foto sin bytes no es una foto; el store se
// rechaza (handler transparenta 422 —un multipart sin contenido es un cuerpo ilegal—).
func TestLocalStoreRejectsAnEmptyPayload(t *testing.T) {
	dir := t.TempDir()
	store, err := photos.NewLocalStore(dir, "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	if _, err := store.Put(context.Background(), photoTenantID, nil, "image/jpeg"); err == nil {
		t.Fatal("Put con un payload vacío: no dio error, want un rechazo")
	}
}

// TestLocalStoreNeedsBothPartsOfItsAddress: sin directorio no hay dónde escribir, y sin
// base pública la URL devuelta no referenciaría nada: el constructor lo rechaza.
func TestLocalStoreNeedsBothPartsOfItsAddress(t *testing.T) {
	if _, err := photos.NewLocalStore("", "http://photos:9000"); err == nil {
		t.Error("NewLocalStore sin directorio: no dio error, want un rechazo")
	}
	if _, err := photos.NewLocalStore(t.TempDir(), ""); err == nil {
		t.Error("NewLocalStore sin URL pública: no dio error, want un rechazo")
	}
}
