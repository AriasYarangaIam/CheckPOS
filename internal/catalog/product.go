package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// Este archivo implementa la mitad de datos de /products (W3-BE-1): la fila de `products`
// tal como es, el store que la consulta y las escrituras con sus resoluciones
// (docs/DATABASE.md §4 `products`; docs/BACKEND.md §4.5-§4.7).
//
// Tres cosas que este store NUNCA hace, porque el contrato lo prohíbe:
//
//   - tocar `stock` en una escritura: converge solo por deltas firmados de
//     `stock_adjustments` (docs/DATABASE.md §4; ARCHITECTURE.md:176), así que la fila
//     nueva nace con el DEFAULT 0 y ningún campo mutado por LWW la toca (§7.4);
//   - inventar un `updated_at`: el comparador de LWW es la marca de mutación del cliente y
//     el servidor la preserva verbatim; `received_at` es la única marca de servidor y es
//     de auditoría (§2.5, §7.4);
//   - pelearse contra una violación única: las dos colisiones de esta tabla se resuelven
//     en silencio con las reglas de §7.7 — F20 renombra determinísticamente, F27 deja el
//     GTIN en NULL — y la fila sobrevive siempre.

// Product es la fila de `products` (docs/DATABASE.md §4) tal como el módulo la lee y la
// devuelve. `Stock` viaja como el TEXTO del NUMERIC(10,3) verbatim —`0.000`— para no leer
// la cantidad como float: es la proyección de servidor y es de solo lectura.
type Product struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Name            string
	GTIN            *string
	PriceCents      int64
	UoM             string
	Stock           string
	PhotoURL        *string
	Attributes      []byte
	GlobalProductID *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ReceivedAt      time.Time
	DeletedAt       *time.Time
}

// ErrUnknownProduct es el resultado de una lectura/escritura sobre un producto que el RLS
// no deja ver. Cubre las dos causas del 404 de §4.1 sin distinguirlas: el producto no
// existe, o existe fuera del tenant y el RLS devolvió cero filas.
var ErrUnknownProduct = errors.New("no product with that id exists in this tenant")

// ErrDuplicateProductID es el resultado de crear con un `id` ya usado por una fila que el
// tenant NO puede ver (la violación es sobre la clave primaria GLOBAL, no sobre las únicas
// de tenant). Es un cuerpo inservible, no una resolución silenciosa: la traducción es 422.
var ErrDuplicateProductID = errors.New("the product id is already in use")

// ProductFilter acota el listado de §4.5. Los parámetros son (propuesta): las fuentes fijan
// el propósito («tenant catalog page; no global dump») pero no la paginación.
type ProductFilter struct {
	// Query busca por nombre (subcadena) o por GTIN exacto (docs/BACKEND.md §4.5).
	Query string
	// Limit acota la página; <= 0 el store usa su default.
	Limit int
}

// ProductMutation son los campos mutables de producto (docs/BACKEND.md §4.6-§4.7). Un
// puntero nulo es «el campo no viajó»; un puntero NUNCA significa «poner nulo»: quitar por
// PATCH no existe, y eso lo rechaza el handler antes de llegar al store.
type ProductMutation struct {
	// ID es el uuid del cliente en Create (device-originated, §2.2) y el id destino en
	// Update (la llave del WHERE, no una columna a escribir).
	ID              uuid.UUID
	Name            *string
	GTIN            *string
	PriceCents      *int64
	UoM             *string
	PhotoURL        *string
	Attributes      []byte
	GlobalProductID *uuid.UUID
	// UpdatedAt es la marca de mutación del cliente: el comparador de LWW, preservado
	// verbatim (§7.4). En empate la mutación pierde —es el mismo instante de la fila ya
	// guardada—, así que repetir un PATCH con la misma marca es idempotente por diseño.
	UpdatedAt time.Time
}

// ProductStore es el único camino de datos de `products`.
//
// Es una interfaz para que el handler se pruebe sin base, y para que exista exactamente un
// lector y escritor de filas de producto. `receivedAt` es la marca de auditoría del
// servidor y la entrega el llamador: el store no lee el reloj.
type ProductStore interface {
	List(ctx context.Context, tenantID uuid.UUID, filter ProductFilter) ([]Product, error)
	Create(ctx context.Context, tenantID uuid.UUID, mutation ProductMutation, receivedAt time.Time) (Product, error)
	Update(ctx context.Context, tenantID, productID uuid.UUID, mutation ProductMutation, receivedAt time.Time) (Product, error)
}

// Límites de página del listado (propuesta §4.5): el catálogo objetivo es de 50–200
// productos (SPRINTS.md Sprint 3), así que el default entrega el catálogo completo y el
// techo cubre un catálogo crecido sin introducir paginación infinita.
const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

// PostgresProductStore lee y escribe `products` sobre el pool de aplicación envuelto en el
// helper de transacción de tenant.
type PostgresProductStore struct {
	tenants *postgres.TenantPool
}

// NewPostgresProductStore construye el store sobre el pool de `pos_app`.
func NewPostgresProductStore(tenants *postgres.TenantPool) *PostgresProductStore {
	return &PostgresProductStore{tenants: tenants}
}

// productColumns es el paquete de columnas de §4 `products` que TODAS las lecturas piden.
// `stock` sale como el texto del NUMERIC: nunca float, y el cliente recibe el mismo
// literal (`0.000`) que habría en SQL.
const productColumns = `id, tenant_id, name, gtin, price_cents, uom, stock::text, photo_url,
	attributes, global_product_id, created_at, updated_at, received_at, deleted_at`

// selectProductSQL lee una fila por id. El WHERE nombra SOLO el id: el aislamiento lo
// impone la política de RLS del tenant que el helper ya fijó, y repetir el `tenant_id` acá
// taparía un GUC mal fijado en lugar de dejarlo ver como cero filas (§5.2, §5.3).
const selectProductSQL = `SELECT ` + productColumns + ` FROM products WHERE id = $1`

// insertProductSQL crea la fila del cliente. `created_at` recibe la MISMA marca que
// `updated_at` —la creación del dispositivo ES la primera mutación—, `received_at` la de
// auditoría, y `stock` no se menciona: la fila nace con el DEFAULT 0 y ningún delta la
// escribió todavía (docs/DATABASE.md §4).
const insertProductSQL = `INSERT INTO products (id, tenant_id, name, gtin, price_cents, uom,
	photo_url, attributes, global_product_id, created_at, updated_at, received_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $10, $11)`

// errNoRowsAffected es el resultado de un UPDATE que no tocó NINGUNA fila: o el id de la
// ruta desapareció del tenant entre la lectura y la escritura, o otra mutación ganó la
// carrera de LWW justo antes. El store vuelve a leer y devuelve la fila ganadora en
// silencio (§7.7).
var errNoRowsAffected = errors.New("the update did not affect any row")

// errNameCollisionStuck frena el bucle de resoluciones cuando hasta el renombrado completo
// vuelve a chocar: es teóricamente imposible (el sufijo del uuid completo es por
// construcción único por fila), y un 500 lo reporta en vez de dejar esperando al llamador
// una fila que no existió.
var errNameCollisionStuck = errors.New("the name collision persisted after every deterministic rename")

// insertViolation representa una violación única a resolver en silencio. Guarda qué índice
// chocó para que el bucle exterior sepa qué campo ajustar, y el reintento corre en una
// transacción NUEVA: pgx no permite volver a escribir en la transacción que abortó.
type insertViolation struct {
	constraintName string
}

// List implementa el listado de §4.5: la página del catálogo ACTIVO del tenant (filas
// tombstoned fuera: las propagan los pulls del cursor de §7.3, no este endpoint), con el
// filtro de nombre/GTIN y el límite de página.
func (s *PostgresProductStore) List(ctx context.Context, tenantID uuid.UUID, filter ProductFilter) ([]Product, error) {
	if s == nil || s.tenants == nil {
		return nil, errors.New("the product store has no tenant pool")
	}
	if tenantID == uuid.Nil {
		return nil, errors.New("the product list needs a tenant id")
	}

	// La paginación es (propuesta): ausente → default; presente fuera de rango se acota a
	// los límites del store, porque el handler ya rechazó lo que es ilegible.
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	args := make([]any, 0, 2)
	addParam := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	var listSQL strings.Builder
	listSQL.WriteString(`SELECT ` + productColumns + ` FROM products WHERE deleted_at IS NULL`)
	if query := strings.TrimSpace(filter.Query); query != "" {
		param := addParam(query)
		fmt.Fprintf(&listSQL, " AND (name ILIKE '%%' || %s || '%%' OR gtin = %s)", param, param)
	}
	fmt.Fprintf(&listSQL, " ORDER BY name, id LIMIT %s", addParam(limit))

	var products []Product
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		rows, err := tx.Query(ctx, listSQL.String(), args...)
		if err != nil {
			return fmt.Errorf("list the products: %w", err)
		}
		defer rows.Close()

		products = make([]Product, 0)
		for rows.Next() {
			product, err := scanProductRow(rows)
			if err != nil {
				return err
			}
			products = append(products, *product)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return products, nil
}

// Create implementa la creación de §4.6 con las DOS resoluciones silenciosas de §7.7:
//
//   - F20 — (tenant_id, name) choca → nombre renombrado con una derivación determinística
//     del id de la PROPIA fila (`nombre (019a…a1b2c3d4)`), extendiendo el sufijo mientras
//     siga chocando; reenviar la MISMA fila reproduce exactamente el mismo nombre;
//   - F27 — (tenant_id, gtin) choca → GTIN va a NULL y la fila sobrevive sin código,
//     re-barcodeable a mano después (§7.7).
//
// El id lo inventó el cliente: una fila de OTRO tenant hace chocar la clave primaria
// GLOBAL (no es una colisión de negocio, no hay nada que resolver en silencio) y es 422;
// contra una fila PROPIA la creación es naturalmente idempotente y devuelve esa fila.
func (s *PostgresProductStore) Create(ctx context.Context, tenantID uuid.UUID, mutation ProductMutation, receivedAt time.Time) (Product, error) {
	if err := guardWrite(tenantID, &mutation); err != nil {
		return Product{}, err
	}

	// Cada intento corre en una transacción NUEVA: la violación única aborta la anterior, y
	// pgx no permite volver a escribir sobre ella. Los cuatro candidatos de nombre y el
	// NULL del gtin acotan el proceso en 8 intentos.
	renameStep := 0
	dropGTIN := false
	for attempt := 0; attempt < 8; attempt++ {
		name, ok := renameCandidate(*mutation.Name, mutation.ID, renameStep)
		if !ok {
			return Product{}, errNameCollisionStuck
		}

		violation, insertErr := s.tryInsert(ctx, tenantID, mutation, name, gtinToStore(mutation.GTIN, dropGTIN), receivedAt)
		switch {
		case insertErr != nil:
			return Product{}, insertErr

		case violation == nil:
			// La fila quedó escrita.

		case isConstraint(violation, "products_pkey"):
			// La clave primaria es global: o este tenant reenvió el id —y el resultado es la
			// MISMA fila, la idempotencia natural de un dispositivo cuya respuesta no
			// llegó— o otro tenant lo usó, que es un cuerpo inservible.
			product, found, err := s.readProduct(ctx, tenantID, mutation.ID)
			if err != nil {
				return Product{}, err
			}
			if found {
				return product, nil
			}
			return Product{}, ErrDuplicateProductID

		case isConstraint(violation, "products_tenant_name_key"):
			renameStep++
			continue

		case isConstraint(violation, "products_tenant_gtin_key"):
			// F27: nadie inventa un GTIN nuevo; la fila queda sin código.
			dropGTIN = true
			continue

		default:
			return Product{}, violationAsError(violation)
		}

		break
	}

	product, found, err := s.readProduct(ctx, tenantID, mutation.ID)
	if err != nil {
		return Product{}, err
	}
	if !found {
		return Product{}, errors.New("the inserted product is not visible after the insert")
	}
	return product, nil
}

// tryInsert realiza UN intento de creación y traduce una violación única a un error tipado
// para que el bucle exterior decida. La transacción la cierra el helper (WithTenant), que
// revierte el intento si la inserción choca. Un error que NO es una violación única sube
// tal cual: el bucle no lo reintenta.
func (s *PostgresProductStore) tryInsert(ctx context.Context, tenantID uuid.UUID, mutation ProductMutation, name string, gtin *string, receivedAt time.Time) (*insertViolation, error) {
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		_, err := tx.Exec(ctx, insertProductSQL,
			mutation.ID, tenantID, name, gtin, *mutation.PriceCents, *mutation.UoM,
			mutation.PhotoURL, attributesToStore(mutation.Attributes), mutation.GlobalProductID,
			mutation.UpdatedAt, receivedAt)
		return err
	})
	if err == nil {
		return nil, nil
	}

	var violation insertViolation
	if violationFrom(err, &violation) {
		return &violation, nil
	}
	return nil, err
}

// isConstraint dice si la violación guardada es sobre la constante buscada.
func isConstraint(violation *insertViolation, constraint string) bool {
	return violation != nil && violation.constraintName == constraint
}

// violationAsError envuelve una violación no resuelta: no es una colisión que esté
// permitido silenciar, así que sigue hacia el 500 con su causa.
func violationAsError(violation *insertViolation) error {
	return fmt.Errorf("resolve the unique violation: %s", violation.constraintName)
}

// Update implementa el patch de §4.7, online-only (la enumeración de encolables no incluye
// PATCH): lee la fila del tenant, resuelve el LWW ANTES de escribir (§7.4) y aplica SOLO
// los campos provistos, preservando verbatim la marca del cliente y estampando
// `received_at` como auditoría (§2.5).
//
// Una mutación que NO gana se responde con la fila ganadora en 200 —nunca 409: la única
// mención del 409 del contrato es el claim de idempotencia en vuelo, que es mejor de W4—.
func (s *PostgresProductStore) Update(ctx context.Context, tenantID, productID uuid.UUID, mutation ProductMutation, receivedAt time.Time) (Product, error) {
	if s == nil || s.tenants == nil {
		return Product{}, errors.New("the product store has no tenant pool")
	}
	if tenantID == uuid.Nil {
		return Product{}, errors.New("the product update needs a tenant id")
	}
	if productID == uuid.Nil {
		return Product{}, errors.New("the product update needs a product id")
	}
	if mutation.UpdatedAt.IsZero() {
		return Product{}, errors.New("the product update needs the mutation time")
	}

	// Fila inexistente/fuera del tenant → 404 del contrato; misma respuesta, sin distinguir.
	current, found, err := s.readProduct(ctx, tenantID, productID)
	if err != nil {
		return Product{}, err
	}
	if !found {
		return Product{}, ErrUnknownProduct
	}

	// LWW estricto: solo gana una mutación más NUEVA. Empate = pierde: repetir un PATCH con
	// la misma marca es idempotente y no vuelve a estampar `received_at`.
	if !mutation.UpdatedAt.After(current.UpdatedAt) {
		return current, nil
	}

	renameStep := 0
	dropGTIN := false
	nameSet := mutation.Name != nil
	baseName := ""
	if nameSet {
		baseName = *mutation.Name
	}

	for attempt := 0; attempt < 8; attempt++ {
		// El renombrado solo aplica cuando el patch TRAE name.
		name, ok := renameCandidate(baseName, productID, renameStep)
		if nameSet && !ok {
			return Product{}, errNameCollisionStuck
		}

		violation, patchErr, lost := s.tryUpdate(ctx, tenantID, productID, mutation, name, nameSet, dropGTIN, receivedAt)
		switch {
		case patchErr != nil:
			return Product{}, patchErr

		case lost:
			// Otra mutación ganó entre nuestra lectura y nuestra escritura: la fila actual ES
			// la ganadora del LWW y se devuelve en su lugar, en silencio (docs/BACKEND.md §6).
			winner, found, err := s.readProduct(ctx, tenantID, productID)
			if err != nil {
				return Product{}, err
			}
			if !found {
				return Product{}, ErrUnknownProduct
			}
			return winner, nil

		case violation == nil:
			// El patch quedó aplicado.

		case isConstraint(violation, "products_tenant_name_key"):
			renameStep++
			continue

		case isConstraint(violation, "products_tenant_gtin_key"):
			// La misma regla de F27 en una edición: el código choca con otro producto y la
			// fila sobrevive sin él.
			dropGTIN = true
			continue

		default:
			return Product{}, violationAsError(violation)
		}

		break
	}

	updated, found, err := s.readProduct(ctx, tenantID, productID)
	if err != nil {
		return Product{}, err
	}
	if !found {
		return Product{}, ErrUnknownProduct
	}
	return updated, nil
}

// tryUpdate aplica UN intento del patch con los campos provistos. Devuelve la restricción
// que chocó, un error que no es una violación única (para subir tal cual), y lost=true
// cuando el UPDATE no tocó ninguna fila.
func (s *PostgresProductStore) tryUpdate(ctx context.Context, tenantID, productID uuid.UUID, mutation ProductMutation, name string, nameSet, dropGTIN bool, receivedAt time.Time) (*insertViolation, error, bool) {
	setSQL, args := buildUpdate(mutation, productID, name, nameSet, dropGTIN, receivedAt)
	var lost bool
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		// El WHERE re-verifica la carrera de LWW contra una escritura concurrente: una fila
		// que ya recibió una marca ganadora no se toca —es el empate de §7.4 resuelto sin
		// carrera entre la lectura y la escritura del mismo handler.
		tag, err := tx.Exec(ctx, setSQL, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			lost = true
		}
		return nil
	})
	if lost {
		return nil, nil, true
	}
	if err != nil {
		var violation insertViolation
		if violationFrom(err, &violation) {
			return &violation, nil, false
		}
		return nil, err, false
	}
	return nil, nil, false
}

// buildUpdate arma el UPDATE con SOLO los campos provistos y los dos stamps. Devuelve la
// sentencia y los argumentos listos para el Exec, con la carrera de LWW re-verificada en el
// WHERE: `updated_at < la marca entrante` —un empate no escribe nada.
func buildUpdate(mutation ProductMutation, productID uuid.UUID, name string, nameSet, dropGTIN bool, receivedAt time.Time) (string, []any) {
	args := make([]any, 0, 10)
	addParam := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	set := make([]string, 0, 8)
	if nameSet {
		set = append(set, "name = "+addParam(name))
	}
	if dropGTIN {
		// F27 también en edición: nadie inventa un GTIN nuevo sobre una colisión.
		set = append(set, "gtin = NULL")
	} else if mutation.GTIN != nil {
		set = append(set, "gtin = "+addParam(*mutation.GTIN))
	}
	if mutation.PriceCents != nil {
		set = append(set, "price_cents = "+addParam(*mutation.PriceCents))
	}
	if mutation.UoM != nil {
		set = append(set, "uom = "+addParam(*mutation.UoM))
	}
	if mutation.PhotoURL != nil {
		set = append(set, "photo_url = "+addParam(*mutation.PhotoURL))
	}
	if mutation.Attributes != nil {
		set = append(set, "attributes = "+addParam(json.RawMessage(mutation.Attributes)))
	}
	// Los dos stamps de servidor: la marca de LWW verbatim y la auditoría del instante.
	set = append(set,
		"updated_at = "+addParam(mutation.UpdatedAt),
		"received_at = "+addParam(receivedAt),
	)

	setSQL := "UPDATE products SET " + strings.Join(set, ", ") +
		" WHERE id = " + addParam(productID) +
		" AND updated_at < " + addParam(mutation.UpdatedAt)
	return setSQL, args
}

// readProduct lee una fila por id dentro del tenant y devuelve found=false cuando el RLS
// no la deja ver —que es exactamente lo que el handler traduce a 404.
func (s *PostgresProductStore) readProduct(ctx context.Context, tenantID, productID uuid.UUID) (Product, bool, error) {
	var (
		product Product
		found   bool
	)
	err := s.tenants.WithTenant(ctx, tenantID, func(ctx context.Context, tx postgres.TenantTx) error {
		row := tx.QueryRow(ctx, selectProductSQL, productID)
		scanned, err := scanOneProduct(row)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("read the product: %w", err)
		default:
			product = *scanned
			found = true
			return nil
		}
	})
	if err != nil {
		return Product{}, false, err
	}
	return product, found, nil
}

// guardWrite valida las precondiciones de escritura antes de abrir la primera
// transacción: son defectos de programación del llamador, no peticiones erróneas (patrón
// de internal/identity/device.go). El handler ya validó el cuerpo: esto cierra el tipo.
func guardWrite(tenantID uuid.UUID, mutation *ProductMutation) error {
	if tenantID == uuid.Nil {
		return errors.New("the product write needs the tenant id")
	}
	if mutation == nil {
		return errors.New("the product write needs a mutation")
	}
	if mutation.ID == uuid.Nil {
		return errors.New("the product write needs the product id")
	}
	if mutation.Name == nil || strings.TrimSpace(*mutation.Name) == "" {
		return errors.New("the product write needs the name")
	}
	if mutation.PriceCents == nil {
		return errors.New("the product write needs the price in cents")
	}
	if mutation.UoM == nil || strings.TrimSpace(*mutation.UoM) == "" {
		return errors.New("the product write needs the unit of measure")
	}
	if mutation.UpdatedAt.IsZero() {
		return errors.New("the product write needs the mutation time")
	}
	return nil
}

// renameCandidate devuelve el nombre del intento `step` para la fila: base, +8, +16 y +32
// caracteres del HEX del id SIN guiones. Es determinístico: la misma fila con el mismo id
// reproduce SIEMPRE la misma secuencia, que es lo que pide F20. exists=false cuando el
// renombrado ya está agotado. (El paso 0 NO renombra: es el nombre provisto.)
func renameCandidate(base string, id uuid.UUID, step int) (string, bool) {
	hexID := strings.ReplaceAll(id.String(), "-", "")
	suffixes := []string{"", " (" + hexID[:8] + ")", " (" + hexID[:16] + ")", " (" + hexID + ")"}
	if step >= len(suffixes) {
		return "", false
	}
	return base + suffixes[step], true
}

// gtinToStore aplica F27 al intento actual: ya chocado → NULL, nunca un dato inventado.
func gtinToStore(gtin *string, drop bool) *string {
	if drop {
		return nil
	}
	return gtin
}

// attributesToStore normaliza los atributos para la columna JSONB: ausentes o nulos llegan
// como NULL de SQL, no como JSON null literal.
func attributesToStore(attributes []byte) json.RawMessage {
	if len(attributes) == 0 || strings.EqualFold(string(attributes), "null") {
		return nil
	}
	return json.RawMessage(attributes)
}

// violationFrom extrae la violación única de un error del driver: código 23505 de
// PostgreSQL con el nombre del índice responsable (la única parcial de nombre, la de gtin,
// o la products_pkey global). Otros errores no la llenan.
func violationFrom(err error, violation *insertViolation) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	violation.constraintName = pgErr.ConstraintName
	return true
}

// scanOneProduct escanea una fila completa del SELECT por id.
func scanOneProduct(row pgx.Row) (*Product, error) {
	var product Product
	err := product.scan(row.Scan)
	if err != nil {
		return nil, err
	}
	return &product, nil
}

// scanProductRow escanea la fila ACTUAL del iterador del listado.
func scanProductRow(rows pgx.Rows) (*Product, error) {
	var product Product
	err := product.scan(rows.Scan)
	if err != nil {
		return nil, err
	}
	return &product, nil
}

// scan es el orden de columnas de §4 tal como `productColumns` las pide: mismo orden en
// los dos lectores, definido una sola vez.
func (p *Product) scan(scan func(dest ...any) error) error {
	return scan(&p.ID, &p.TenantID, &p.Name, &p.GTIN,
		&p.PriceCents, &p.UoM, &p.Stock, &p.PhotoURL,
		&p.Attributes, &p.GlobalProductID, &p.CreatedAt, &p.UpdatedAt,
		&p.ReceivedAt, &p.DeletedAt)
}
