package catalog_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// La integración de W3-BE-2 corre la mediación COMPLETA contra la base desechable: la
// tabla global sembrada como dueño, la consulta por el pool del rol `pos_platform` (la
// MISMA base, el DSN derivado), y los tokens de DOS tenants leyendo la MISMA fila global.
//
// Acá nada consulta por pos_app ni por contexto de tenant: las tablas globales no tienen
// RLS y no reciben contexto (docs/DATABASE.md §5.6), así que el arnés de pos_app no entra
// en esta prueba; su invariante —pos_app NO puede leerlas— lo garantiza la matriz de
// concesiones y lo acredita el arranque con AssertExpectedRole.

// globalProductID es la fila global de la prueba.
const globalProductID = "019a0000-0000-7000-8000-0000000000d1"

// seedGlobalProduct siembra una fila de catálogo global con su código de barras, como dueño
// y sin contexto de tenant: es el registro de plataforma que ningún tenant escribe.
func seedGlobalProduct(t *testing.T, db *testDatabase, gtin string) {
	t.Helper()

	inTenantUnscoped(t, db, func(tx *sql.Tx) {
		execOn(t, tx, `INSERT INTO global_products (id, name, attributes, created_at)
			VALUES ($1, 'yerba playa 1kg', $2, now())`,
			mustParseUUID(t, globalProductID), `{"marca":"playa"}`)
		execOn(t, tx, `INSERT INTO global_barcodes (id, gtin, global_product_id, created_at)
			VALUES (gen_random_uuid(), $1, $2, now())`,
			gtin, mustParseUUID(t, globalProductID))
	})
}

// TestGtinLookupServesBothTenantsFromTheSameGlobalRow: la MISMA fila global es visible por
// la mediación para cualquier tenant que pregunte por su gtin —la propiedad que hace al
// catálogo global compartido— y el miss es 404 para ambos.
func TestGtinLookupServesBothTenantsFromTheSameGlobalRow(t *testing.T) {
	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedTenant(t, db, anotherTenantID)
	seedGlobalProduct(t, db, "7791009010014")

	platformPool := db.rolePool(t, config.PlatformDatabaseURL(), "pos_platform")
	router, issuer := newGtinRouter(t, catalog.NewPlatformGtinLookup(platformPool), &strings.Builder{})

	for name, token := range map[string]string{
		"first tenant":  productToken(t, issuer),
		"second tenant": productTokenFor(t, issuer, anotherTenantID),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := doCatalogRequest(t, router, http.MethodGet, "/gtin-lookup?gtin=7791009010014", token, "", nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}

			var decoded struct {
				Product struct {
					ID         string          `json:"id"`
					Name       string          `json:"name"`
					Attributes json.RawMessage `json:"attributes"`
				} `json:"product"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("la respuesta no es JSON: %v (%q)", err, recorder.Body.String())
			}
			if decoded.Product.ID != globalProductID || decoded.Product.Name != "yerba playa 1kg" {
				t.Errorf("product = {id %s, name %q}, want la fila global sembrada", decoded.Product.ID, decoded.Product.Name)
			}
			if !strings.Contains(string(decoded.Product.Attributes), "playa") {
				t.Errorf("attributes = %q, want el JSONB global", decoded.Product.Attributes)
			}
		})
	}

	// El miss de la mediación contra la base de verdad: un gtin que el catálogo global no
	// conoce es 404, con el mismo sobre genérico, y lo compruebo por el primer tenant.
	recorder := doCatalogRequest(t, router, http.MethodGet, "/gtin-lookup?gtin=7790000000000", productToken(t, issuer), "", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("miss: estado = %d, want 404 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeNotFound {
		t.Errorf("miss: error.code = %q, want %q", got, httpx.CodeNotFound)
	}
}
