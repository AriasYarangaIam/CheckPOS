// Auditoría del sobre de error único (W8-BE-3; S9 de odd/tasks/sprint-8-admin-hardening.md).
//
// El criterio de aceptación es «toda respuesta de error usa la misma forma». Esa propiedad no se
// puede probar endpoint por endpoint sin una base: lo que la hace cierta o falsa es que NINGÚN
// camino escriba un cuerpo de error por fuera de `httpx.WriteError`. Esta auditoría recorre las
// fuentes y falla si aparece un segundo camino —`http.Error`, un `WriteHeader` con estado de
// error o un literal con el sobre—, que es exactamente la regresión que el criterio prohíbe.
package httpx_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// auditRoots son las raíces del módulo que atienden HTTP. El paquete `httpx` queda afuera: es el
// dueño del sobre y ahí sí se construye el cuerpo.
var auditRoots = []string{"internal", "cmd"}

// errorStatusPattern reconoce un estado de error pasado a WriteHeader, escrito como número o como
// constante del paquete http.
var errorStatusPattern = regexp.MustCompile(`WriteHeader\(\s*(?:http\.)?(?:Status)?([0-9]{3}|[A-Za-z]+)`)

// allowedHeaderStatuses son los estados que NO son de error: una escritura directa de cabecera con
// uno de ellos no compite con el sobre.
var allowedHeaderStatuses = map[string]bool{
	"http.StatusOK":        true,
	"http.StatusCreated":   true,
	"http.StatusAccepted":  true,
	"http.StatusNoContent": true,
}

// errorStatusCodeNames son las constantes de error de `net/http` que el código podría usar.
var errorStatusCodeNames = map[string]bool{
	"http.StatusBadRequest":          true,
	"http.StatusUnauthorized":        true,
	"http.StatusForbidden":           true,
	"http.StatusNotFound":            true,
	"http.StatusMethodNotAllowed":    true,
	"http.StatusConflict":            true,
	"http.StatusUnprocessableEntity": true,
	"http.StatusTooManyRequests":     true,
	"http.StatusInternalServerError": true,
	"http.StatusServiceUnavailable":  true,
}

// productionGoFiles devuelve las fuentes del módulo que no son de prueba y no son del paquete
// `httpx`.
func productionGoFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	for _, root := range auditRoots {
		err := filepath.WalkDir(filepath.Join("..", "..", "..", root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// El dueño del sobre: `internal/platform/httpx`.
			if strings.Contains(filepath.ToSlash(path), "/platform/httpx/") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatalf("recorrer %s: %v", root, err)
		}
	}
	if len(files) < 10 {
		t.Fatalf("la auditoría encontró %d fuentes: el recorrido está roto y el cero no probaría nada", len(files))
	}
	return files
}

// TestNoHandlerWritesAnErrorOutsideTheSharedEnvelope: la forma única no se sostiene por disciplina
// sino por ausencia del segundo camino.
func TestNoHandlerWritesAnErrorOutsideTheSharedEnvelope(t *testing.T) {
	offenders := []string{}

	for _, file := range productionGoFiles(t) {
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("leer %s: %v", file, err)
		}
		code := string(contents)

		if strings.Contains(code, "http.Error(") {
			offenders = append(offenders, file+": http.Error")
		}
		if strings.Contains(code, `"{"+"error"`) {
			offenders = append(offenders, file+": error literal construido")
		}

		for _, match := range errorStatusPattern.FindAllStringSubmatch(code, -1) {
			argument := match[1]
			switch {
			case len(argument) == 3 && argument[0] >= '4':
				offenders = append(offenders, file+": WriteHeader("+argument+")")
			case errorStatusCodeNames["http."+argument]:
				offenders = append(offenders, file+": WriteHeader(http."+argument+")")
			case allowedHeaderStatuses["http."+argument]:
				// Un 200/201/204 explícito no es un cuerpo de error.
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("hay %d camino(s) de error por fuera de httpx.WriteError:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// TestEveryMountedRouteGoesThroughTheRouter: una ruta montada fuera de `httpx.NewRouter` se
// quedaría sin el registro de peticiones y sin el sobre de las rutas desconocidas.
func TestEveryMountedRouteGoesThroughTheRouter(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "cmd", "api", "main.go"))
	if err != nil {
		t.Fatalf("leer cmd/api/main.go: %v", err)
	}

	main := string(contents)
	if !strings.Contains(main, "httpx.NewRouter(") {
		t.Fatal("cmd/api/main.go no monta el router de httpx")
	}
	if strings.Contains(main, "http.NewServeMux()") {
		t.Error("cmd/api/main.go construye su propio ServeMux: las rutas no pasarían por el router auditado")
	}
}
