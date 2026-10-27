package catalog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AriasYarangaIam/CheckPOS/internal/catalog"
	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Las pruebas de integración de W3-BE-1 corren el ciclo COMPLETO sobre una base desechable
// con prefijo `pos_test_`: router REAL httpx, store de POSTGRES sobre el pool de `pos_app`
// con RLS, y toda aserción de aislamiento leída POR el camino de producción (withTenant: el
// RLS decide qué filas existen para este tenant).
//
// Las resoluciones silenciosas de §7.7 y el LWW de §7.4 tienen que verse contra la base
// real: la única parcial de nombre, la de gtin y la pkey global son índices de POSTGRES, y
// el empate del comparador solo existe como timestamptz guardado.

// anotherTenantID es el SEGUNDO tenant de las pruebas de aislamiento: su token firmado lee
// y escribe solo lo que el RLS deja ver para su propio tenant.
const anotherTenantID = "019a0000-0000-7000-8000-0000000000f5"

// productFixture es la fila siembra de `products` con columnas explícitas. Los vacíos son
// NULL de SQL —igual que una fila creada por la API sin esos campos—.
type productFixture struct {
	id         string
	tenantID   string
	name       string
	gtin       string // "" → NULL
	priceCents int64
	uom        string
	stock      string
	attributes string // "" → NULL; si no, un objeto JSON completo
	updatedAt  time.Time
	deletedAt  *time.Time
}

// seedProduct siembra la fila como DUEÑO (superusuario, sin RLS) dentro del contexto del
// tenant. Es SIEMBRA solamente: ninguna aserción de aislamiento lee por acá (§5.7).
func seedProduct(t *testing.T, db *testDatabase, fixture productFixture) {
	t.Helper()

	if fixture.stock == "" {
		fixture.stock = "0.000"
	}
	stamp := fixture.updatedAt

	inTenant(t, db, fixture.tenantID, func(tx *sql.Tx) {
		var gtin any
		if fixture.gtin != "" {
			gtin = fixture.gtin
		}
		var attributes any
		if fixture.attributes != "" {
			attributes = fixture.attributes
		}
		var deletedAt any
		if fixture.deletedAt != nil {
			deletedAt = *fixture.deletedAt
		}

		execOn(t, tx, `INSERT INTO products (id, tenant_id, name, gtin, price_cents, uom,
				stock, attributes, created_at, updated_at, received_at, deleted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10, $11)`,
			mustParseUUID(t, fixture.id), mustParseUUID(t, fixture.tenantID),
			fixture.name, gtin, fixture.priceCents, fixture.uom, fixture.stock,
			attributes, stamp, stamp, deletedAt)
	})
}

// readStoredProduct lee la fila POR pos_app con RLS (el camino de producción), en la forma
// del contrato, para que la aserción compare lo que una petición real vería. found=false
// cuando el RLS no la deja ver: para este tenant no existe.
func readStoredProduct(t *testing.T, tenants *postgres.TenantPool, tenantID, productID string) (productAPIRow, bool) {
	t.Helper()

	var (
		found   bool
		row     productAPIRow
		scanErr error
	)
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		var (
			id         uuid.UUID
			name       string
			gtin       *string
			priceCents int64
			uom        string
			stock      string
			attributes []byte
			createdAt  time.Time
			updatedAt  time.Time
			receivedAt time.Time
			deletedAt  *time.Time
		)
		err := tx.QueryRow(context.Background(), `SELECT id, name, gtin, price_cents, uom,
				stock::text, COALESCE(attributes, '{}'::jsonb), created_at, updated_at,
				received_at, deleted_at
			FROM products WHERE id = $1`, mustParseUUID(t, productID)).
			Scan(&id, &name, &gtin, &priceCents, &uom, &stock, &attributes,
				&createdAt, &updatedAt, &receivedAt, &deletedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			scanErr = err
			return
		}

		found = true
		row.ID = id.String()
		row.Name = name
		row.GTIN = gtin
		row.PriceCents = priceCents
		row.UoM = uom
		row.Stock = stock
		if len(attributes) > 0 && !strings.EqualFold(string(attributes), "null") {
			row.Attributes = attributes
		}
		row.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		row.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		row.ReceivedAt = receivedAt.UTC().Format(time.RFC3339)
		if deletedAt != nil {
			row.DeletedAt = ptr(deletedAt.UTC().Format(time.RFC3339))
		}
	})
	if scanErr != nil {
		t.Fatalf("leer la fila %s del tenant %s por pos_app: %v", productID, tenantID, scanErr)
	}
	if !found {
		return productAPIRow{}, false
	}
	return row, true
}

// productTokenFor firma un token de empleado para el tenant dado: la pieza que necesitan
// las pruebas de aislamiento con DOS tenants.
func productTokenFor(t *testing.T, issuer *identity.TokenIssuer, tenantID string) string {
	t.Helper()

	token, err := issuer.Issue(identity.Claims{
		TenantID: uuid.MustParse(tenantID),
		UserID:   uuid.MustParse(tokenUserID),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// newProductIntegrationRouter arma el router REAL con el store de POSTGRES sobre el pool de
// `pos_app` de la base desechable, igual que cmd/api lo monta.
func newProductIntegrationRouter(t *testing.T, db *testDatabase) (http.Handler, *identity.TokenIssuer, *postgres.TenantPool) {
	t.Helper()

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))
	store := catalog.NewPostgresProductStore(tenants)
	router, issuer := newCatalogRouter(t, store, &strings.Builder{})
	return router, issuer, tenants
}

// freshIntegrationCase siembra el arnés completo de una prueba: la base desechable, los dos
// tenants y el router con el store de POSTGRES. La base queda a mano para SIEMBRAR filas
// como dueño; nunca para leer aserciones con ella.
func freshIntegrationCase(t *testing.T) (http.Handler, *identity.TokenIssuer, *postgres.TenantPool, *testDatabase) {
	t.Helper()

	db := newThrowawayDatabase(t)
	seedTenant(t, db, tokenTenantID)
	seedTenant(t, db, anotherTenantID)
	router, issuer, tenants := newProductIntegrationRouter(t, db)
	return router, issuer, tenants, db
}

// TestProductCreatePersistsTheTypedColumns: el POST persiste la fila con las columnas de su
// tipo —uom como columna TEXT (el entregable del sprint: un `kg` persiste columna, no
// JSONB), precio BIGINT, stock NUMERIC DEFAULT 0 que ninguna petición toca— y los
// timestamps del contrato: created/updated duplican la marca del cliente y received_at es
// la de auditoría del servidor.
func TestProductCreatePersistsTheTypedColumns(t *testing.T) {
	router, issuer, tenants, _ := freshIntegrationCase(t)
	token := productToken(t, issuer)

	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	row, found := readStoredProduct(t, tenants, tokenTenantID, "019a0000-0000-7000-8000-0000000000b1")
	if !found {
		t.Fatalf("la fila no quedó persistida: %q", recorder.Body.String())
	}

	if row.Name != "arroz especial 1kg" {
		t.Errorf("name = %q, want el del cuerpo", row.Name)
	}
	if row.GTIN == nil || *row.GTIN != "7791234567890" {
		t.Errorf("gtin = %v, want 7791234567890", row.GTIN)
	}
	if row.PriceCents != 1550 {
		t.Errorf("price_cents = %d, want 1550", row.PriceCents)
	}
	if row.UoM != "kg" {
		t.Errorf("uom = %q, want la columna TEXT con \"kg\": el JSONB de uom era el defecto a corregir", row.UoM)
	}
	if row.Stock != "0.000" {
		t.Errorf("stock = %q, want el DEFAULT 0.000: ninguna petición lo escribe", row.Stock)
	}
	if string(row.Attributes) == "" || string(row.Attributes) == "null" {
		t.Errorf("attributes = %q, want el objeto JSON del cuerpo", row.Attributes)
	}
	if row.CreatedAt != "2026-10-18T12:00:00Z" || row.UpdatedAt != "2026-10-18T12:00:00Z" {
		t.Errorf("timestamps = %q / %q, want la marca del cliente duplicada verbatim", row.CreatedAt, row.UpdatedAt)
	}
	received, err := time.Parse(time.RFC3339, row.ReceivedAt)
	if err != nil {
		t.Fatalf("received_at no es RFC3339: %q", row.ReceivedAt)
	}
	if d := time.Since(received); d < -2*time.Minute || d > 2*time.Minute {
		t.Errorf("received_at = %v, want la marca de auditoría del momento de la petición", received)
	}
	if row.DeletedAt != nil {
		t.Errorf("deleted_at = %v, want null: la fila nace activa", row.DeletedAt)
	}
}

// TestProductCreateReplaysTheSameId: reenviar el MISMO cuerpo (la idempotencia natural de un
// dispositivo cuya respuesta no llegó) devuelve la MISMA fila, sin renombrar de nuevo y sin
// duplicar.
func TestProductCreateReplaysTheSameId(t *testing.T) {
	router, issuer, tenants, _ := freshIntegrationCase(t)
	token := productToken(t, issuer)

	first := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("primera creación: estado = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	second := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("reenvío con el mismo id: estado = %d, want 200 (cuerpo %q)", second.Code, second.Body.String())
	}

	row, _ := readStoredProduct(t, tenants, tokenTenantID, "019a0000-0000-7000-8000-0000000000b1")
	if row.Name != "arroz especial 1kg" {
		t.Errorf("name = %q, want el original intacto: la repetición no vuelve a resolver colisiones", row.Name)
	}
	if count := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM products`); count != 1 {
		t.Errorf("products tiene %d filas, want 1: el reenvío no duplica", count)
	}
}

// TestProductCreateResolvesCollisionsSilently: las dos colisiones de §7.7 contra los
// índices REALES — F20 renombra la fila NUEVA con el sufijo determinístico del id de la
// PROPIA fila nueva, F27 deja el gtin en NULL — y la fila preexistente queda intacta.
func TestProductCreateResolvesCollisionsSilently(t *testing.T) {
	router, issuer, tenants, db := freshIntegrationCase(t)
	token := productToken(t, issuer)

	stamp := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c1",
		tenantID:   tokenTenantID,
		name:       "arroz especial 1kg",
		priceCents: 1500,
		uom:        "kg",
		updatedAt:  stamp,
	})
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c2",
		tenantID:   tokenTenantID,
		name:       "yerba tradicional 1kg",
		gtin:       "7791234567890",
		priceCents: 2400,
		uom:        "unit",
		updatedAt:  stamp,
	})

	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200: la colisión no es un error del cliente (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	row := decodeProductRow(t, recorder)
	if row.Name != "arroz especial 1kg (019a0000)" {
		t.Errorf("name = %q, want el renombrado de F20 con los primeros dígitos del id de la fila nueva", row.Name)
	}
	if row.GTIN != nil {
		t.Errorf("gtin = %v, want null: la colisión de F27 deja la fila sin código", row.GTIN)
	}

	stored, found := readStoredProduct(t, tenants, tokenTenantID, "019a0000-0000-7000-8000-0000000000b1")
	if !found {
		t.Fatalf("la fila nueva no quedó persistida")
	}
	if stored.Name != row.Name || stored.GTIN != nil {
		t.Errorf("la fila guardada (%q, gtin %v) no coincide con la respuesta", stored.Name, stored.GTIN)
	}

	// La fila PREEXISTENTE no cambió: la colisión no renombra ni borra al otro.
	holder, found := readStoredProduct(t, tenants, tokenTenantID, "019a0000-0000-7000-8000-0000000000c1")
	if !found || holder.Name != "arroz especial 1kg" {
		t.Errorf("la fila preexistente quedó alterada: %q", holder.Name)
	}
	coded, found := readStoredProduct(t, tenants, tokenTenantID, "019a0000-0000-7000-8000-0000000000c2")
	if !found || coded.GTIN == nil || *coded.GTIN != "7791234567890" {
		t.Errorf("la fila preexistente con gtin quedó alterada: %v", coded.GTIN)
	}
}

// TestProductCreateRejectsAnIdOwnedByAnotherTenant: el id del cuerpo ya existe en OTRO
// tenant —la pkey es global— y el resultado es 422 con el campo id, sin revelar el registro
// ajeno.
func TestProductCreateRejectsAnIdOwnedByAnotherTenant(t *testing.T) {
	router, issuer, tenants, db := freshIntegrationCase(t)
	token := productTokenFor(t, issuer, anotherTenantID)

	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000b1",
		tenantID:   tokenTenantID,
		name:       "arroz del primer tenant",
		priceCents: 1500,
		uom:        "kg",
		updatedAt:  time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC),
	})

	recorder := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, nil)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeValidationError {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
	}
	details, ok := envelope.Error.Details.(map[string]any)
	if !ok || details["field"] != "id" {
		t.Errorf("details = %#v, want un objeto que nombre el campo id", envelope.Error.Details)
	}
	if count := queryCount(t, tenants, anotherTenantID, `SELECT count(*) FROM products`); count != 0 {
		t.Errorf("products del segundo tenant tiene %d filas, want 0", count)
	}
}

// TestProductPatchResolvesTheMutationsByLastWriteWins: el LWW de §7.4 contra timestamptz
// reales —el empate pierde, la marca anterior pierde, la posterior gana y su marca queda
// verbatim; received_at solo se estampa en la escritura ganadora; el stock del fixture no
// se toca jamás.
func TestProductPatchResolvesTheMutationsByLastWriteWins(t *testing.T) {
	router, issuer, _, db := freshIntegrationCase(t)
	token := productToken(t, issuer)

	initial := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000b1",
		tenantID:   tokenTenantID,
		name:       "arroz especial 1kg",
		gtin:       "7791234567890",
		priceCents: 1550,
		uom:        "kg",
		stock:      "5.000",
		updatedAt:  initial,
	})
	productPath := "/products/019a0000-0000-7000-8000-0000000000b1"

	// Empate: la mutación pierde y la fila vuelve intacta.
	tieBody := `{"name":"empate","updated_at":"2026-10-18T12:00:00Z"}`
	recorder := doCatalogRequest(t, router, http.MethodPatch, productPath, token, tieBody, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("marca con empate: estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	row := decodeProductRow(t, recorder)
	if row.Name != "arroz especial 1kg" || row.UpdatedAt != "2026-10-18T12:00:00Z" {
		t.Errorf("el empate modificó la fila: name %q, updated_at %q", row.Name, row.UpdatedAt)
	}

	// Marca anterior: pierde, también en silencio.
	olderBody := `{"name":"atrasado","updated_at":"2026-10-18T11:00:00Z"}`
	recorder = doCatalogRequest(t, router, http.MethodPatch, productPath, token, olderBody, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("marca anterior: estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	row = decodeProductRow(t, recorder)
	if row.Name != "arroz especial 1kg" || row.UpdatedAt != "2026-10-18T12:00:00Z" {
		t.Errorf("la marca anterior modificó la fila: name %q, updated_at %q", row.Name, row.UpdatedAt)
	}

	// Marca posterior: gana; la respuesta es la fila con el patch APLICADO.
	receivedBefore, err := time.Parse(time.RFC3339, row.ReceivedAt)
	if err != nil {
		t.Fatalf("received_at inicial no es RFC3339: %q", row.ReceivedAt)
	}

	newerBody := `{"name":"arroz premium 1kg","price_cents":1600,"updated_at":"2026-10-19T08:00:00Z"}`
	recorder = doCatalogRequest(t, router, http.MethodPatch, productPath, token, newerBody, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("marca posterior: estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	row = decodeProductRow(t, recorder)
	if row.Name != "arroz premium 1kg" {
		t.Errorf("name = %q, want el patch aplicado", row.Name)
	}
	if row.PriceCents != 1600 {
		t.Errorf("price_cents = %d, want 1600", row.PriceCents)
	}
	if row.UpdatedAt != "2026-10-19T08:00:00Z" {
		t.Errorf("updated_at = %q, want la marca del ganador VERBATIM", row.UpdatedAt)
	}
	if row.GTIN == nil || *row.GTIN != "7791234567890" {
		t.Errorf("gtin = %v, want intacto: un patch parcial no toca lo no provisto", row.GTIN)
	}
	if row.UoM != "kg" {
		t.Errorf("uom = %q, want \"kg\": un patch parcial no toca lo no provisto", row.UoM)
	}
	if row.Stock != "5.000" {
		t.Errorf("stock = %q, want \"5.000\": LWW nunca toca stock", row.Stock)
	}
	receivedAfter, err := time.Parse(time.RFC3339, row.ReceivedAt)
	if err != nil {
		t.Fatalf("received_at no es RFC3339: %q", row.ReceivedAt)
	}
	if !receivedAfter.After(receivedBefore.Add(time.Second)) {
		t.Errorf("received_at = %v, want una marca de auditoría posterior a la del patch anterior", receivedAfter)
	}
}

// TestProductsGetReturnsOnlyTheTenantCatalog: el listado trae las filas ACTIVAS del tenant
// del token —ni las del otro tenant, ni las tumbas (las propagará el pull del cursor de
// §7.3)— con el filtro `q` operativo por nombre subcadena y por GTIN exacto.
func TestProductsGetReturnsOnlyTheTenantCatalog(t *testing.T) {
	router, issuer, _, db := freshIntegrationCase(t)
	token := productToken(t, issuer)

	stamp := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c1",
		tenantID:   tokenTenantID,
		name:       "arroz especial 1kg",
		gtin:       "7791234567890",
		priceCents: 1550,
		uom:        "kg",
		updatedAt:  stamp,
	})
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c3",
		tenantID:   tokenTenantID,
		name:       "aceite girasol 900ml",
		priceCents: 890,
		uom:        "unit",
		updatedAt:  stamp,
	})
	// La tumba: una fila del PROPIO tenant con deleted_at. El listado la excluye.
	tombstone := stamp
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c4",
		tenantID:   tokenTenantID,
		name:       "azúcar impalpable",
		priceCents: 700,
		uom:        "kg",
		updatedAt:  stamp,
		deletedAt:  &tombstone,
	})
	// El mismo nombre en el OTRO tenant: el RLS lo saca de la página del primero.
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c5",
		tenantID:   anotherTenantID,
		name:       "arroz especial 1kg",
		priceCents: 1500,
		uom:        "kg",
		updatedAt:  stamp,
	})

	// Página completa: dos filas activas del tenant, ordenadas por nombre.
	recorder := doCatalogRequest(t, router, http.MethodGet, "/products", token, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	var page productsPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("la respuesta no es { \"data\": [...] }: %v (%q)", err, recorder.Body.String())
	}
	if len(page.Data) != 2 {
		t.Fatalf("data tiene %d filas (%q), want 2: la tumba y el otro tenant quedan fuera", len(page.Data), recorder.Body.String())
	}
	if page.Data[0].Name != "aceite girasol 900ml" || page.Data[1].Name != "arroz especial 1kg" {
		t.Errorf("nombres = %q, %q; want ordenados por nombre y solo del tenant", page.Data[0].Name, page.Data[1].Name)
	}
	for _, row := range page.Data {
		if strings.Contains(row.Name, "azúcar") && row.DeletedAt != nil {
			t.Errorf("el listado sirvió una tumba: %q", row.Name)
		}
	}

	// Filtro por nombre subcadena.
	recorder = doCatalogRequest(t, router, http.MethodGet, "/products?q=aceite", token, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("filtro por nombre: estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	page = productsPage{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("la respuesta del filtro no es { \"data\": [...] }: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Name != "aceite girasol 900ml" {
		t.Errorf("filtro por nombre devolvió %+v, want solo la fila de aceite", page.Data)
	}

	// Filtro por GTIN exacto.
	recorder = doCatalogRequest(t, router, http.MethodGet, "/products?q=7791234567890", token, "", nil)
	page = productsPage{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("la respuesta del filtro de gtin no es { \"data\": [...] }: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].GTIN == nil || *page.Data[0].GTIN != "7791234567890" {
		t.Errorf("filtro por gtin devolvió %+v, want solo la fila codificada", page.Data)
	}
}

// TestProductPatchAnswers404ForAForeignProduct: el PATCH por el id de RUTA de OTRO tenant
// responde el MISMO 404 que un id inexistente, y la fila ajena no cambia.
func TestProductPatchAnswers404ForAForeignProduct(t *testing.T) {
	router, issuer, tenants, db := freshIntegrationCase(t)
	token := productToken(t, issuer)

	stamp := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	seedProduct(t, db, productFixture{
		id:         "019a0000-0000-7000-8000-0000000000c2",
		tenantID:   anotherTenantID,
		name:       "producto del otro tenant",
		priceCents: 1500,
		uom:        "kg",
		updatedAt:  stamp,
	})

	body := `{"name":"invasion","updated_at":"2026-10-19T08:00:00Z"}`
	for name, path := range map[string]string{
		"the other tenant's product": "/products/019a0000-0000-7000-8000-0000000000c2",
		"a product nobody has":       "/products/019a0000-0000-7000-8000-0000000000d9",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := doCatalogRequest(t, router, http.MethodPatch, path, token, body, nil)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("estado = %d, want 404 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			envelope := decodeEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeNotFound {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeNotFound)
			}
		})
	}

	// La fila del otro tenant sigue intacta.
	foreign, found := readStoredProduct(t, tenants, anotherTenantID, "019a0000-0000-7000-8000-0000000000c2")
	if !found || foreign.Name != "producto del otro tenant" || foreign.UpdatedAt != "2026-10-18T12:00:00Z" {
		t.Errorf("la fila del otro tenant quedó alterada: %+v", foreign)
	}
}

// TestProductCreateCollapsesADuplicateToASingleRowAndTheSameSnapshot: con la MISMA clave y
// el MISMO cuerpo, el segundo POST es una repetición verdadera del claim —200 con el
// snapshot almacenado, byte a byte— y la tabla queda con UNA fila. Es el criterio del
// sprint: los duplicados colapsan a un solo ack (docs/BACKEND.md §8.2, §6.1).
func TestProductCreateCollapsesADuplicateToASingleRowAndTheSameSnapshot(t *testing.T) {
	router, issuer, tenants, _ := freshIntegrationCase(t)
	token := productToken(t, issuer)
	headers := map[string]string{"Idempotency-Key": "clave-venta-offline-1"}

	first := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("primera creación: estado = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	second := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, headers)
	if second.Code != http.StatusOK {
		t.Fatalf("repetición: estado = %d, want 200 (cuerpo %q)", second.Code, second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("la repetición no devolvió el snapshot almacenado:\nprimera: %s\nsegunda: %s",
			first.Body.String(), second.Body.String())
	}

	if rows := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM products`); rows != 1 {
		t.Errorf("filas = %d, want 1: un duplicado no abre una segunda fila", rows)
	}
	if claims := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM idempotency_keys WHERE state = 'completed'`); claims != 1 {
		t.Errorf("claims completados = %d, want 1", claims)
	}
}

// TestProductCreateRejectsTheSameKeyWithAnotherBody: la misma clave con otro payload es el
// 422 del claim, sin segunda escritura (docs/DATABASE.md §7.1 Branch A).
func TestProductCreateRejectsTheSameKeyWithAnotherBody(t *testing.T) {
	router, issuer, tenants, _ := freshIntegrationCase(t)
	token := productToken(t, issuer)
	headers := map[string]string{"Idempotency-Key": "clave-venta-offline-2"}

	first := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("primera creación: estado = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	otherBody := strings.Replace(validCreateBody, `"price_cents":1550`, `"price_cents":1600`, 1)
	second := doCatalogRequest(t, router, http.MethodPost, "/products", token, otherBody, headers)
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("misma clave, otro cuerpo: estado = %d, want 422 (cuerpo %q)", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "idempotency.payload_mismatch") {
		t.Errorf("el 422 no trae el código del claim: %s", second.Body.String())
	}

	if rows := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM products`); rows != 1 {
		t.Errorf("filas = %d, want 1: el conflicto de payload no escribe nada", rows)
	}
}

// TestProductCreateResolvesTheCollisionUnderTheClaimAndReplaysIt: la violación única de
// nombre (F20) se resuelve del lado del servidor —200 con la fila renombrada, el POS no
// recibe ningún error— y la repetición de la MISMA clave devuelve esa fila resuelta, no un
// segundo renombrado: el renombrado es determinístico (docs/DATABASE.md §7.7, §7.9).
func TestProductCreateResolvesTheCollisionUnderTheClaimAndReplaysIt(t *testing.T) {
	router, issuer, tenants, _ := freshIntegrationCase(t)
	token := productToken(t, issuer)

	// La primera fila se queda con el nombre; la segunda choca contra la UNIQUE de nombre.
	first := doCatalogRequest(t, router, http.MethodPost, "/products", token, validCreateBody,
		map[string]string{"Idempotency-Key": "clave-colision-1"})
	if first.Code != http.StatusOK {
		t.Fatalf("primera creación: estado = %d, want 200 (cuerpo %q)", first.Code, first.Body.String())
	}

	collidingBody := strings.Replace(validCreateBody,
		`"id":"019a0000-0000-7000-8000-0000000000b1"`,
		`"id":"019a0000-0000-7000-8000-0000000000c2"`, 1)
	headers := map[string]string{"Idempotency-Key": "clave-colision-2"}

	resolved := doCatalogRequest(t, router, http.MethodPost, "/products", token, collidingBody, headers)
	if resolved.Code != http.StatusOK {
		t.Fatalf("colisión de nombre: estado = %d, want 200 — el POS nunca ve el conflicto (F20) (cuerpo %q)",
			resolved.Code, resolved.Body.String())
	}
	renamed := decodeProductRow(t, resolved)
	if renamed.Name == "arroz especial 1kg" {
		t.Fatalf("name = %q, want el renombrado determinístico de F20: el nombre no puede repetirse", renamed.Name)
	}
	if renamed.ID != "019a0000-0000-7000-8000-0000000000c2" {
		t.Errorf("id = %q, want el id del cliente", renamed.ID)
	}

	// La repetición de la misma clave devuelve la fila YA resuelta, no un segundo renombrado.
	replay := doCatalogRequest(t, router, http.MethodPost, "/products", token, collidingBody, headers)
	if replay.Code != http.StatusOK {
		t.Fatalf("repetición de la colisión: estado = %d, want 200 (cuerpo %q)", replay.Code, replay.Body.String())
	}
	if replay.Body.String() != resolved.Body.String() {
		t.Errorf("la repetición cambió la fila resuelta:\nprimera: %s\nrepetición: %s",
			resolved.Body.String(), replay.Body.String())
	}

	if rows := queryCount(t, tenants, tokenTenantID, `SELECT count(*) FROM products`); rows != 2 {
		t.Errorf("filas = %d, want 2: el reintento no crea una tercera fila", rows)
	}
}
