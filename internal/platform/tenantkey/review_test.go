// Revisión de fuentes del namespace de Redis: hace MECÁNICA la regla «ninguna clave sin el
// prefijo del tenant» (S1-4 y W2-BE-4; docs/BACKEND.md §7.4; ARCHITECTURE.md:20, :86).
//
// # La regla exacta que aplica este escáner
//
// Recorre todos los archivos `.go` del árbol del repositorio y, en cada uno, aplica tres reglas:
//
//  1. IMPORT: un archivo que NO sea `_test.go` y que esté fuera del paquete auxiliar no puede
//     importar un cliente de Redis. La lista de módulos que se consideran cliente de Redis es
//     `redisClientModules`, y la excepción nominal es `redisClientAllowlist`.
//  2. PREFIJO: ningún archivo fuera del paquete auxiliar puede escribir el prefijo `tenant:`
//     PEGADO a lo que sigue. Un literal de cadena es un hallazgo cuando contiene `tenant:` y el
//     byte inmediatamente posterior NO es un espacio; `"tenant:"` solo, al final del literal,
//     también es un hallazgo. Las oraciones en prosa de este repositorio escriben siempre
//     `tenant: ` con espacio detrás, y por eso quedan fuera de la regla; la cabeza de una clave
//     —`tenant:` + id + `:` + partes— nunca lleva ese espacio.
//  3. CONCATENACIÓN: ningún archivo fuera del paquete auxiliar puede ARMAR el prefijo plegando
//     literales contiguos. `"tenant" + ":" + id` es un hallazgo aunque ningún literal contenga
//     `tenant:` por separado, y `"tenant" + ": " + id` no lo es, por la misma razón que en la
//     regla anterior: el espacio detrás es prosa. El pliegue no cruza un operando que no sea un
//     literal, así que la forma que toma el prefijo de otra constante o de un argumento sigue
//     siendo un límite declarado (ver abajo). Un literal que por sí solo ya delata el prefijo
//     no se informa dos veces: la regla 2 ya lo informó.
//
// El paquete auxiliar (`internal/platform/tenantkey`) es el único lugar exento de las tres
// reglas: es el que define el prefijo y el que tiene por delante la construcción de claves. Un
// archivo que necesite una clave usa `tenantkey.Key` y no toca el literal.
//
// # Qué NO detecta, deliberadamente
//
// El escáner es un plegado de literales sobre el AST, no un teorema. Lo que deja pasar, y que un
// revisor tiene que mirar a ojo:
//
//   - un prefijo que sale de OTRA CONSTANTE, de una variable o del resultado de una llamada
//     (`head + ":" + id`, con `const head = "tenant"`): el pliegue cruza literales contiguos, no
//     valores, y plegar un valor exigiría ejecutar el programa;
//   - el prefijo con espacio detrás (`fmt.Sprintf("tenant: %s", id)`), que es indistinguible de
//     la prosa;
//   - un cliente de Redis con otra ruta de módulo (un fork, un wrapper que lo reexporta, un
//     módulo interno que a su vez depende de él);
//   - un cliente RESP escrito a mano sobre `net.Dial`, que no es una dependencia;
//   - cualquier archivo que no sea `.go`: la PWA de `web/` no se escanea, y podría armar claves
//     por su cuenta (el contrato de Redis es del servidor, pero el escáner no lo prueba);
//   - todo lo que está dentro de un directorio que empieza con `.` o de `node_modules`, `vendor`,
//     `dist` o `build`: son dependencias, artefactos de build y tooling local, no código del
//     repositorio. `testdata` SÍ se escanea: las muestras del escáner van en `t.TempDir()`.
//
// Y no comprueba la forma de las claves que sí construye el paquete auxiliar: de eso se ocupa
// `tenantkey_test.go`.
package tenantkey_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	// tenantPrefixToken es el encabezado obligatorio de toda clave, literal.
	tenantPrefixToken = "tenant:"

	// helperPackageDir es la ruta, relativa a la raíz, del ÚNICO paquete exento: el que define el
	// prefijo y expone el constructor.
	helperPackageDir = "internal/platform/tenantkey"
)

// redisClientModules es la lista de módulos que cuentan como «un cliente de Redis». Es la regla
// tal como está escrita hoy: un cliente nuevo bajo otra ruta no la activa, y agregarlo acá es
// parte de la revisión que lo incorpora.
var redisClientModules = []string{
	"github.com/redis/go-redis",
	"github.com/go-redis/redis",
	"github.com/gomodule/redigo",
	"github.com/redis/rueidis",
	"github.com/rueian/rueidis",
	"github.com/mediocregopher/radix",
	"github.com/go-redsync/redsync",
}

// redisClientAllowlist es la excepción nominal a la regla de import: paquetes que pueden importar
// un cliente de Redis sin pasar por el paquete auxiliar.
//
// Está VACÍA a propósito. Toda clave del proceso se construye con `tenantkey.Key`, así que el
// primer cliente de Redis que llegue tiene que llegar por el paquete auxiliar; agregar una entrada
// acá es una decisión de revisión, no un atajo, y por eso cada entrada debería venir con su razón
// en el mismo commit.
var redisClientAllowlist = map[string]bool{}

// prunedDirectories son los directorios de dependencias y artefactos que no son código del
// repositorio. Además de estos, se saltean todos los que empiezan con `.` (herramientas locales,
// el historial de git).
var prunedDirectories = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
}

// finding es una violación de la regla, con la ubicación que un revisor necesita para verla.
type finding struct {
	path   string
	line   int
	detail string
}

func (f finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.path, f.line, f.detail)
}

// scanResult es lo que deja una pasada: cuántos archivos se revisaron y qué se encontró.
//
// El conteo de archivos no es decorativo: es lo que impide que una raíz equivocada —o un walk que
// no camina— produzca un verde vacío. Una revisión que no revisó nada tiene que fallar.
type scanResult struct {
	files    int
	findings []finding
}

// TestTheRepositoryHasNoKeyWithoutTheTenantPrefix es la revisión de este repositorio: la regla
// del namespace se cumple hoy, y se cumple de forma comprobable y no por convención.
func TestTheRepositoryHasNoKeyWithoutTheTenantPrefix(t *testing.T) {
	root := repositoryRoot(t)

	result, err := scanTenantKeys(root)
	if err != nil {
		t.Fatalf("escanear %s: %v", root, err)
	}

	// El piso: menos que esto significa que la raíz o el walk están mal, y un verde sin archivos
	// revisados no probaría nada.
	if result.files < 30 {
		t.Fatalf("el escáner revisó %d archivos .go bajo %s, want al menos 30: la revisión no está mirando el repositorio", result.files, root)
	}

	for _, finding := range result.findings {
		t.Errorf("clave de Redis sin el prefijo del tenant (docs/BACKEND.md §7.4): %s", finding)
	}
	t.Logf("revisados %d archivos .go bajo %s: ninguna clave fuera de tenantkey", result.files, root)
}

// TestTheReviewCatchesAHandBuiltPrefix prueba por mutación que la regla del prefijo tiene dientes:
// sobre un directorio temporal se arma el repositorio mínimo con una clave escrita a mano y otra
// que pasa por el constructor, y el escáner tiene que marcar exactamente la primera.
//
// La mutación se hace SIEMPRE sobre un `t.TempDir()`, nunca sobre el repositorio: la revisión de
// arriba mide el repositorio real y una muestra que lo tocara la volvería mentira.
func TestTheReviewCatchesAHandBuiltPrefix(t *testing.T) {
	root := t.TempDir()

	// El paquete auxiliar: el único que puede escribir el prefijo. No es un hallazgo.
	writeFixture(t, root, helperPackageDir+"/keys.go", `package tenantkey

const prefix = "tenant:"

func key(id string) string { return prefix + id }
`)

	// Una clave armada a mano, fuera del paquete auxiliar. Es el hallazgo.
	writeFixture(t, root, "internal/catalog/cache.go", `package catalog

func cacheKey(tenant string) string { return "tenant:" + tenant + ":catalog" }
`)

	// El mismo delito en un archivo de prueba: la regla del prefijo rige también para los tests
	// (el import de un cliente, no: ver la otra prueba).
	writeFixture(t, root, "internal/catalog/cache_test.go", `package catalog

const seededKey = "tenant:x:catalog:page"
`)

	// Prosa: `tenant: ` con espacio detrás es una oración, no una clave. No es un hallazgo, y es
	// la razón por la que la regla exige el prefijo PEGADO a lo que sigue.
	writeFixture(t, root, "internal/catalog/doc.go", `package catalog

const doc = "el catalogo de un tenant: sin prefijo no hay aislamiento"
`)

	// El camino correcto: la clave sale del constructor, así que no hay literal que detectar.
	writeFixture(t, root, "internal/catalog/tenant_keys.go", `package catalog

import "github.com/AriasYarangaIam/CheckPOS/internal/platform/tenantkey"

func pageKey(id [16]byte) (string, error) { return tenantkey.Key(id, "catalog", "page") }
`)

	result, err := scanTenantKeys(root)
	if err != nil {
		t.Fatalf("escanear la muestra: %v", err)
	}

	// Cinco archivos en la muestra y cuatro revisados: el del paquete auxiliar queda fuera por
	// diseño, y contarlo probaría que el escáner no entiende de quién es el prefijo.
	if result.files != 4 {
		t.Fatalf("el escáner revisó %d archivos, want los 4 de la muestra que no son del paquete auxiliar", result.files)
	}

	got := findingPaths(result)
	want := []string{"internal/catalog/cache.go", "internal/catalog/cache_test.go"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("hallazgos = %v, want %v", got, want)
	}
}

// TestTheReviewCatchesAPiecewisePrefix prueba la tercera regla por mutación: el prefijo armado
// plegando literales contiguos es un hallazgo aunque ningún literal lo contenga por separado, y
// el mismo prefijo con espacio detrás sigue siendo prosa.
//
// Mide tres cosas que la regla anterior no puede ver, y una que sí: la forma literal del mismo
// delito se informa UNA vez —por la regla 2, no dos veces—, porque contar dos veces el mismo
// hallazgo inflaría el reporte sin agregar información.
func TestTheReviewCatchesAPiecewisePrefix(t *testing.T) {
	root := t.TempDir()

	// El delito: la mitad del encabezado en cada literal. Ninguno de los dos contiene `tenant:`
	// pegado a lo que sigue, así que la regla de los literales no lo ve.
	writeFixture(t, root, "internal/catalog/piecewise.go", `package catalog

func cursorKey(id string) string { return "tenant" + ":" + id + ":cursor" }
`)

	// El mismo delito en la forma que la regla 2 ya informaba: se marca una sola vez.
	writeFixture(t, root, "internal/catalog/literal.go", `package catalog

func pageKey(id string) string { return "tenant:" + id + ":page" }
`)

	// Prosa por partes: el espacio detrás del prefijo la deja fuera de las dos reglas, igual que
	// en la forma literal.
	writeFixture(t, root, "internal/catalog/prose.go", `package catalog

const doc = "el catalogo de un tenant" + ": " + "sin prefijo no hay aislamiento"
`)

	result, err := scanTenantKeys(root)
	if err != nil {
		t.Fatalf("escanear la muestra: %v", err)
	}

	if result.files != 3 {
		t.Fatalf("el escáner revisó %d archivos, want los 3 de la muestra", result.files)
	}

	// La ubicación exacta importa: el reporte tiene que decir archivo y línea del hallazgo.
	got := findingLocations(result)
	want := []string{"internal/catalog/literal.go:3", "internal/catalog/piecewise.go:3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("hallazgos = %v, want %v", got, want)
	}

	if len(result.findings) != len(want) {
		t.Errorf("el escáner informó %d hallazgo(s), want %d: la regla de la concatenación no repite el hallazgo del literal", len(result.findings), len(want))
	}
}

// TestTheReviewStopsAtANonLiteralOperand documenta el límite que queda después de la tercera
// regla: el pliegue cruza literales contiguos, no valores. Un prefijo que vive en una constante
// sigue necesitando ojo humano, y este es el borde exacto.
func TestTheReviewStopsAtANonLiteralOperand(t *testing.T) {
	root := t.TempDir()

	writeFixture(t, root, "internal/catalog/constant_head.go", `package catalog

const head = "tenant"

func key(id string) string { return head + ":" + id }
`)

	result, err := scanTenantKeys(root)
	if err != nil {
		t.Fatalf("escanear la muestra: %v", err)
	}

	if len(result.findings) != 0 {
		t.Errorf("hallazgos = %v, want ninguno: el pliegue no cruza un operando que no sea un literal, y esa es la forma que este límite declara", result.findings)
	}
}

// TestTheReviewCatchesARedisClientOutsideTheHelper prueba la otra mitad por mutación: un cliente
// de Redis importado fuera del paquete auxiliar en un archivo que no es de prueba es un hallazgo;
// el mismo import en un archivo de prueba, o dentro del paquete auxiliar, no lo es.
func TestTheReviewCatchesARedisClientOutsideTheHelper(t *testing.T) {
	root := t.TempDir()

	// Producción: hallazgo.
	writeFixture(t, root, "internal/catalog/store.go", `package catalog

import _ "github.com/redis/go-redis/v9"
`)

	// Prueba: exenta. Un doble de servidor de Redis es cosa de las pruebas, y la regla de import
	// habla de los archivos que se compilan en el proceso.
	writeFixture(t, root, "internal/catalog/store_test.go", `package catalog

import _ "github.com/alicebob/miniredis/v2"
`)

	// El paquete auxiliar: el cliente de Redis que llegue va a vivir acá, así que el directorio
	// entero está fuera del alcance de las dos reglas.
	writeFixture(t, root, helperPackageDir+"/client.go", `package tenantkey

import _ "github.com/redis/go-redis/v9"
`)

	result, err := scanTenantKeys(root)
	if err != nil {
		t.Fatalf("escanear la muestra: %v", err)
	}

	got := findingPaths(result)
	want := []string{"internal/catalog/store.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("hallazgos = %v, want %v", got, want)
	}
}

// TestTheReviewDoesNotLookInsideDependencies documenta el límite más grande del escáner: lo que
// está dentro de una dependencia o de una herramienta local no se revisa. Se prueba para que el
// límite sea una decisión escrita y no una sorpresa.
func TestTheReviewDoesNotLookInsideDependencies(t *testing.T) {
	root := t.TempDir()

	writeFixture(t, root, "node_modules/some-client/keys.go", `package someclient

func key(tenant string) string { return "tenant:" + tenant }
`)
	writeFixture(t, root, ".tooling/generator.go", `package tooling

func key(tenant string) string { return "tenant:" + tenant }
`)

	result, err := scanTenantKeys(root)
	if err != nil {
		t.Fatalf("escanear la muestra: %v", err)
	}

	if result.files != 0 {
		t.Fatalf("el escáner revisó %d archivos dentro de una dependencia, want 0", result.files)
	}
	if len(result.findings) != 0 {
		t.Errorf("hallazgos = %v, want ninguno: esos directorios están fuera del alcance de la revisión", result.findings)
	}
}

// repositoryRoot resuelve la raíz del repositorio desde el directorio del paquete de prueba y
// verifica que sea la raíz de verdad: sin `go.mod` no hay repositorio que revisar y una raíz
// equivocada tiene que fallar acá, no producir un verde vacío.
func repositoryRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("leer el directorio de trabajo: %v", err)
	}

	root := filepath.Clean(filepath.Join(dir, "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s no parece la raíz del repositorio (no hay go.mod): %v", root, err)
	}
	return root
}

// writeFixture escribe un archivo de muestra, creando los directorios que hagan falta.
func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("crear el directorio de %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("escribir la muestra %s: %v", relative, err)
	}
}

// findingPaths devuelve las rutas de los hallazgos, ordenadas y sin repetir.
func findingPaths(result scanResult) []string {
	seen := map[string]bool{}
	var paths []string

	for _, finding := range result.findings {
		if seen[finding.path] {
			continue
		}
		seen[finding.path] = true
		paths = append(paths, finding.path)
	}

	sort.Strings(paths)
	return paths
}

// findingLocations devuelve `archivo:línea` de cada hallazgo, ordenado y sin repetir. Es la forma
// del reporte que un revisor lee, y por eso las muestras afirman sobre ella y no sólo sobre el
// conjunto de archivos.
func findingLocations(result scanResult) []string {
	seen := map[string]bool{}
	var locations []string

	for _, finding := range result.findings {
		location := fmt.Sprintf("%s:%d", finding.path, finding.line)
		if seen[location] {
			continue
		}
		seen[location] = true
		locations = append(locations, location)
	}

	sort.Strings(locations)
	return locations
}

// scanTenantKeys recorre root y devuelve los hallazgos de las TRES reglas. Es el escáner entero, y
// su contrato —qué mira, qué deja pasar— está en el comentario de arriba del archivo.
func scanTenantKeys(root string) (scanResult, error) {
	var result scanResult

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if path == root {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || prunedDirectories[name] {
				return fs.SkipDir
			}
			return nil
		}

		if filepath.Ext(entry.Name()) != ".go" {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)

		// El paquete auxiliar entero queda fuera: es el que define el prefijo y el único que
		// puede construir la clave sin pasar por el constructor.
		if relative == helperPackageDir || strings.HasPrefix(relative, helperPackageDir+"/") {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		result.files++
		result.findings = append(result.findings, reviewSource(relative, source)...)
		return nil
	})
	if err != nil {
		return scanResult{}, err
	}

	sort.Slice(result.findings, func(i, j int) bool {
		if result.findings[i].path != result.findings[j].path {
			return result.findings[i].path < result.findings[j].path
		}
		return result.findings[i].line < result.findings[j].line
	})
	return result, nil
}

// reviewSource aplica las tres reglas a un archivo.
//
// Un archivo que no parsea es un hallazgo y no un archivo salteado: lo que no se puede leer no se
// puede revisar, y dar por bueno un fuente ilegible sería exactamente la forma en que una revisión
// deja de ser una barrera.
func reviewSource(relative string, source []byte) []finding {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, relative, source, parser.ParseComments)
	if err != nil {
		return []finding{{path: relative, line: 1, detail: fmt.Sprintf("no se pudo analizar el archivo, así que la revisión no pudo revisarlo: %v", err)}}
	}

	var findings []finding
	isTest := strings.HasSuffix(relative, "_test.go")

	// Regla 1: un cliente de Redis en un archivo que se compila en el proceso.
	if !isTest {
		for _, imported := range file.Imports {
			module, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				continue
			}
			if isRedisClient(module) {
				findings = append(findings, finding{
					path:   relative,
					line:   fileset.Position(imported.Pos()).Line,
					detail: fmt.Sprintf("importa un cliente de Redis (%q) fuera del paquete auxiliar: el cliente llega por %s", module, helperPackageDir),
				})
			}
		}
	}

	// Regla 2: el prefijo escrito a mano, en cualquier archivo, tests incluidos.
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}

		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		if handBuildsThePrefix(value) {
			findings = append(findings, finding{
				path:   relative,
				line:   fileset.Position(literal.Pos()).Line,
				detail: fmt.Sprintf("construye el prefijo %q a mano: la clave del tenant se arma con tenantkey.Key", tenantPrefixToken),
			})
		}
		return true
	})

	// Regla 3: el prefijo ARMADO POR PARTES. El recorrido es preorden, así que una cadena de `+`
	// se informa en su nodo más externo y los `+` internos quedan marcados: sin eso, `("a" + "b")
	// + c` produciría un hallazgo por subexpresión.
	var folded = make(map[ast.Node]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || binary.Op != token.ADD {
			return true
		}
		if folded[binary] {
			return true
		}

		if finding, ok := concatenatedPrefixFinding(relative, fileset, binary, concatenationOperands(binary, folded)); ok {
			findings = append(findings, finding)
		}
		return true
	})

	return findings
}

// concatenationOperands aplana una cadena de `+` y marca cada `+` interno como ya leído, para
// que el recorrido preorden informe la cadena una sola vez.
//
// Un operando que no sea un `+` se devuelve tal cual: lo que el pliegue de la regla 3 decide
// después es si ese operando es un literal de cadena o no.
func concatenationOperands(binary *ast.BinaryExpr, folded map[ast.Node]bool) []ast.Expr {
	folded[binary] = true

	var operands []ast.Expr
	var flatten func(expr ast.Expr)
	flatten = func(expr ast.Expr) {
		nested, ok := expr.(*ast.BinaryExpr)
		if !ok || nested.Op != token.ADD {
			operands = append(operands, expr)
			return
		}
		folded[nested] = true
		flatten(nested.X)
		flatten(nested.Y)
	}
	flatten(binary.X)
	flatten(binary.Y)

	return operands
}

// concatenatedPrefixFinding decide si una cadena de concatenaciones arma el prefijo del tenant.
//
// Plegar literales contiguos es lo que atrapa `"tenant" + ":" + id`: por separado ningún literal
// contiene `tenant:` pegado a lo que sigue, y juntos sí. El pliegue NO cruza un operando que no
// sea un literal —una constante, una variable, el resultado de una llamada—, así que la forma que
// toma el prefijo de un valor que vive en otra parte sigue siendo un límite declarado del escáner.
//
// Un literal que por sí solo ya delata el prefijo no se informa otra vez: la regla 2 ya lo
// informó, y repetirlo contaría el mismo hallazgo dos veces.
func concatenatedPrefixFinding(relative string, fileset *token.FileSet, binary *ast.BinaryExpr, operands []ast.Expr) (finding, bool) {
	var (
		texts    = make([]string, 0, len(operands))
		constant = make([]bool, 0, len(operands))
	)

	for _, operand := range operands {
		value, ok := stringLiteral(operand)
		if !ok {
			texts = append(texts, "")
			constant = append(constant, false)
			continue
		}
		if handBuildsThePrefix(value) {
			return finding{}, false
		}

		texts = append(texts, value)
		constant = append(constant, true)
	}

	// El pliegue: los literales contiguos son un solo texto constante, porque `"tenant"` y `":"`
	// no son dos partes de la clave sino la mitad del encabezado cada uno.
	for index := 0; index < len(texts); {
		if !constant[index] {
			index++
			continue
		}

		assembled := texts[index]
		next := index + 1
		for next < len(texts) && constant[next] {
			assembled += texts[next]
			next++
		}

		if handBuildsThePrefix(assembled) {
			return finding{
				path:   relative,
				line:   fileset.Position(binary.Pos()).Line,
				detail: fmt.Sprintf("arma el prefijo %q concatenando literales: la clave del tenant se arma con tenantkey.Key", tenantPrefixToken),
			}, true
		}
		index = next
	}

	return finding{}, false
}

// stringLiteral devuelve el valor de un literal de cadena y si el operando lo era.
func stringLiteral(operand ast.Expr) (string, bool) {
	literal, ok := operand.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}

	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// handBuildsThePrefix informa si la cadena contiene el prefijo pegado a lo que sigue, que es la
// forma de una cabeza de clave. `tenant: ` con espacio detrás es prosa y no cuenta.
func handBuildsThePrefix(value string) bool {
	for offset := 0; offset <= len(value); {
		index := strings.Index(value[offset:], tenantPrefixToken)
		if index < 0 {
			return false
		}
		index += offset

		next := index + len(tenantPrefixToken)
		// El literal termina en el prefijo, o lo que sigue no es un espacio: en los dos casos es
		// una clave en construcción.
		if next >= len(value) || value[next] != ' ' {
			return true
		}
		offset = next
	}
	return false
}

// isRedisClient informa si la ruta de import es un cliente de Redis que la regla no exime.
func isRedisClient(importPath string) bool {
	if matchesModule(redisClientAllowlist, importPath) {
		return false
	}
	return moduleInList(redisClientModules, importPath)
}

// moduleInList compara una ruta de import contra una lista de módulos, aceptando también sus
// subpaquetes.
func moduleInList(modules []string, importPath string) bool {
	for _, module := range modules {
		if importPath == module || strings.HasPrefix(importPath, module+"/") {
			return true
		}
	}
	return false
}

// matchesModule es la misma comparación sobre el mapa de excepciones.
func matchesModule(exceptions map[string]bool, importPath string) bool {
	for module := range exceptions {
		if importPath == module || strings.HasPrefix(importPath, module+"/") {
			return true
		}
	}
	return false
}
