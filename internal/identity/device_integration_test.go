// Prueba de integración de `POST /auth/device-bind` (W2-BE-3) contra una base migrada de
// verdad, con el rol de aplicación real (`pos_app`) y el RLS de tenant activo.
//
// Cubre los criterios de aceptación del sprint que la base puede demostrar y la capa unitaria
// no: la ventana offline de 7 días o más que NO está acotada por la vida del access token, el
// re-bind que refresca la misma fila, y el `device_id` de otro tenant que no cambia nada.
//
// Es una prueba de integración, así que se salta bajo `testing.Short()` (SPRINTS.md §8).
package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/config"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/postgres"
)

// contractMinimumOfflineWindow es el piso de la ventana offline según el contrato
// (docs/DATABASE.md §4 `devices`; docs/BACKEND.md §5.2). Es un literal A PROPÓSITO: la prueba
// mide el comportamiento contra el contrato, no contra la constante que lo implementa, así que
// bajar la constante no puede dejar la prueba en verde.
const contractMinimumOfflineWindow = 7 * 24 * time.Hour

// deviceFixture es un dispositivo ya enlazado a un cajero: el estado del que parte la prueba
// del re-bind y el de otro tenant que no debe cambiar.
type deviceFixture struct {
	label      string
	tenantID   string
	userID     string
	deviceID   string
	pin        string
	validUntil time.Time
}

// seed inserta el dispositivo como DUEÑO, con el contexto de tenant fijado en la misma
// transacción: `devices` está bajo FORCE RLS y la siembra pasa por el mismo camino que la
// aplicación (docs/DATABASE.md §5.2).
func (f deviceFixture) seed(t *testing.T, db *testDatabase) {
	t.Helper()

	pinHash, err := identity.HashCredential(f.pin)
	if err != nil {
		t.Fatalf("HashCredential del dispositivo %s: %v", f.label, err)
	}

	inTenant(t, db, f.tenantID, func(tx *sql.Tx) {
		execOn(t, tx, `
			INSERT INTO devices (id, tenant_id, user_id, pin_hash, offline_session_valid_until, created_at)
			VALUES ($1, $2, $3, $4, $5, now())`,
			f.deviceID, f.tenantID, f.userID, pinHash, f.validUntil)
	})
}

// storedDevice es la fila completa de `devices`, leída con el contexto del tenant. Comparar
// todos sus campos es lo que hace literal el «byte a byte sin cambios» de la otra pupila.
type storedDevice struct {
	id         string
	tenantID   string
	userID     string
	pinHash    string
	validUntil time.Time
	createdAt  time.Time
}

// inTenant corre fn dentro de una transacción del dueño con el contexto de tenant fijado.
//
// El dueño del contenedor local es `pos`, que el rol de la imagen crea como superusuario
// (`rolsuper = t`, `rolbypassrls = t`, medido con psql): un superusuario NO queda sujeto al RLS
// ni con `FORCE`. Por eso esta función solo SIEMBRA filas con columnas explícitas y ninguna
// aserción de aislamiento lee por acá; para eso está withTenant, que va por `pos_app`.
func inTenant(t *testing.T, db *testDatabase, tenantID string, fn func(tx *sql.Tx)) {
	t.Helper()

	ctx := context.Background()
	tx, err := db.owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("abrir la transacción del tenant %s: %v", tenantID, err)
	}

	var setting string
	if err := tx.QueryRowContext(ctx, setTenantSettingSQL, tenantID).Scan(&setting); err != nil {
		t.Fatalf("fijar el contexto de tenant %s: %v", tenantID, err)
	}

	fn(tx)

	if err := tx.Commit(); err != nil {
		t.Fatalf("confirmar la transacción del tenant %s: %v", tenantID, err)
	}
}

// withTenant lee por el CAMINO DE PRODUCCIÓN: el pool de `pos_app` dentro del helper de
// transacción de tenant. Es la única lente con la que una aserción de aislamiento mide algo: el
// RLS decide qué filas existen para este tenant, y un dispositivo de otro tenant simplemente no
// está (docs/DATABASE.md §5.2, §5.7).
func withTenant(t *testing.T, tenants *postgres.TenantPool, tenantID string, fn func(tx postgres.TenantTx)) {
	t.Helper()

	err := tenants.WithTenant(context.Background(), uuid.MustParse(tenantID), func(_ context.Context, tx postgres.TenantTx) error {
		fn(tx)
		return nil
	})
	if err != nil {
		t.Fatalf("correr la transacción de tenant %s por pos_app: %v", tenantID, err)
	}
}

// readDevice lee la fila completa con el contexto del tenant dado, por `pos_app`. Devuelve
// found=false cuando el RLS no la deja ver, que es exactamente lo que el endpoint traduce a 404.
func readDevice(t *testing.T, tenants *postgres.TenantPool, tenantID, deviceID string) (storedDevice, bool) {
	t.Helper()

	var (
		device storedDevice
		found  bool
	)

	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		// Los identificadores se leen como texto: la aserción compara bytes y no depende de
		// cómo el driver decida devolver un uuid.
		err := tx.QueryRow(context.Background(), `
			SELECT id::text, tenant_id::text, user_id::text, pin_hash,
			       offline_session_valid_until, created_at
			FROM devices WHERE id = $1::uuid`, deviceID).
			Scan(&device.id, &device.tenantID, &device.userID, &device.pinHash, &device.validUntil, &device.createdAt)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			t.Fatalf("leer el dispositivo %s del tenant %s: %v", deviceID, tenantID, err)
		default:
			found = true
		}
	})

	return device, found
}

// countDevices cuenta las filas visibles con el contexto del tenant, por `pos_app`.
func countDevices(t *testing.T, tenants *postgres.TenantPool, tenantID string) int {
	t.Helper()

	count := -1
	withTenant(t, tenants, tenantID, func(tx postgres.TenantTx) {
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM devices`).Scan(&count); err != nil {
			t.Fatalf("contar los dispositivos del tenant %s: %v", tenantID, err)
		}
	})
	return count
}

// newDeviceBindIntegrationRouter arma el router del proceso con la ruta real sobre el pool de
// `pos_app` envuelto en el helper de transacción de tenant: `device-bind` es su primer
// consumidor de producción (W2-BE-2, docs/BACKEND.md §7.7).
func newDeviceBindIntegrationRouter(t *testing.T, tenants *postgres.TenantPool, ttl time.Duration, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	binder := identity.NewPostgresDeviceBinder(tenants)
	if binder == nil {
		t.Fatal("NewPostgresDeviceBinder devolvió nil")
	}

	return newDeviceBindRouter(t, binder, ttl, logBuf)
}

// waitForExpiry espera a que el token deje de verificar por vencimiento. Es la medición que
// separa «la ventana offline» de «la vida del access token»: el instante en que el token muere
// es el instante en el que el POS ya no puede autenticarse en línea y sí debe desbloquearse con
// el PIN.
func waitForExpiry(t *testing.T, issuer *identity.TokenIssuer, token string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := issuer.Verify(token)
		if errors.Is(err, jwt.ErrTokenExpired) {
			return
		}
		if err != nil {
			t.Fatalf("Verify devolvió %v, want un vencimiento", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("el access token no venció en 20 s: la prueba de la ventana no probaría nada")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestDeviceBindSetsTheWindowAndRefreshesItOnRebind cubre los criterios (a), (b) y (e):
//
//   - (a) la ventana guardada es de 7 días o más desde el bind y NO está acotada por la vida
//     del access token: el token de la prueba vive un segundo y la ventana lo sobrevive;
//   - (b) un segundo bind con el mismo `device_id` refresca la ventana y deja UNA sola fila;
//   - (e) el `pin_hash` guardado es argon2id, verifica contra el PIN enviado, y el PIN en claro
//     no está en la fila, ni en la respuesta, ni en el registro del proceso.
func TestDeviceBindSetsTheWindowAndRefreshesItOnRebind(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	fixture := loginFixture{
		label:       "A",
		tenantID:    "019a0000-0000-7000-8000-0000000000c1",
		userID:      "019a0000-0000-7000-8000-0000000000c2",
		phone:       "+51900000201",
		password:    "secreto-del-tenant-a",
		productID:   "019a0000-0000-7000-8000-0000000000c3",
		productName: "producto de A",
	}
	fixture.seed(t, db)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))

	// El emisor vive UN SEGUNDO: es la forma de medir que la ventana offline no depende de la
	// vida del access token (docs/BACKEND.md §5.2).
	var logBuf strings.Builder
	router, issuer := newDeviceBindIntegrationRouter(t, tenants, time.Second, &logBuf)

	firstPIN := "1234"
	beforeFirstBind := time.Now()
	firstToken := deviceToken(t, issuer, fixture.tenantID, fixture.userID)

	recorder := postDeviceBind(t, router, firstToken, `{"pin":"`+firstPIN+`"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("el primer bind = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	payload := decodeDeviceBindPayload(t, recorder)

	// Los ids los genera la aplicación como UUIDv7 (docs/DATABASE.md §2.2).
	deviceID, err := uuid.Parse(payload.Device.ID)
	if err != nil {
		t.Fatalf("device.id = %q no es un uuid: %v", payload.Device.ID, err)
	}
	if deviceID.Version() != 7 {
		t.Errorf("device.id = %s (versión %d), want un UUIDv7", deviceID, deviceID.Version())
	}

	stored, found := readDevice(t, tenants, fixture.tenantID, payload.Device.ID)
	if !found {
		t.Fatalf("el dispositivo %s no quedó guardado en el tenant %s", payload.Device.ID, fixture.tenantID)
	}

	// (c) La fila es del tenant y del usuario del TOKEN.
	if stored.tenantID != fixture.tenantID {
		t.Errorf("devices.tenant_id = %q, want %q", stored.tenantID, fixture.tenantID)
	}
	if stored.userID != fixture.userID {
		t.Errorf("devices.user_id = %q, want %q", stored.userID, fixture.userID)
	}

	// (a) La ventana: medida DENTRO de la fila (ventana menos hora de bind) y contra el piso del
	// contrato, que es un literal independiente de la constante del código.
	window := stored.validUntil.Sub(stored.createdAt)
	if window < contractMinimumOfflineWindow {
		t.Errorf("offline_session_valid_until - created_at = %v, want al menos %v", window, contractMinimumOfflineWindow)
	}
	if stored.validUntil.Before(beforeFirstBind.Add(contractMinimumOfflineWindow)) {
		t.Errorf("offline_session_valid_until = %v, want al menos %v desde el bind", stored.validUntil, contractMinimumOfflineWindow)
	}
	if identity.MinimumOfflineSessionWindow < contractMinimumOfflineWindow {
		t.Errorf("identity.MinimumOfflineSessionWindow = %v, want al menos el piso del contrato %v",
			identity.MinimumOfflineSessionWindow, contractMinimumOfflineWindow)
	}

	// La ventana guardada y la que devuelve la respuesta son la misma marca, al segundo (el
	// contrato la serializa en RFC3339).
	reported, err := time.Parse(time.RFC3339, payload.Device.ValidUntil)
	if err != nil {
		t.Fatalf("offline_session_valid_until = %q no es RFC3339: %v", payload.Device.ValidUntil, err)
	}
	if !reported.Equal(stored.validUntil.UTC().Truncate(time.Second)) {
		t.Errorf("offline_session_valid_until de la respuesta = %v, want %v", reported, stored.validUntil.UTC().Truncate(time.Second))
	}

	// (e) El hash guardado es argon2id y verifica contra el PIN enviado.
	if !strings.HasPrefix(stored.pinHash, "$argon2id$v=19$") {
		t.Errorf("pin_hash = %q, want argon2id en formato PHC", stored.pinHash)
	}
	if matches, err := identity.VerifyCredential(stored.pinHash, firstPIN); err != nil || !matches {
		t.Errorf("VerifyCredential(pin_hash, pin) = (%v, %v), want (true, nil)", matches, err)
	}
	if strings.Contains(stored.pinHash, firstPIN) {
		t.Errorf("pin_hash contiene el PIN en claro: %q", stored.pinHash)
	}

	// El PIN tampoco está en la fila como dato propio: ninguna columna lo lleva.
	rowText := strings.Join([]string{stored.id, stored.tenantID, stored.userID, stored.pinHash, stored.validUntil.String(), stored.createdAt.String()}, " ")
	if strings.Contains(rowText, firstPIN) {
		t.Errorf("la fila del dispositivo contiene el PIN en claro: %q", rowText)
	}

	// (b) El re-bind: mismo `device_id`, otro PIN. Se emite un token nuevo porque el anterior
	// vive un segundo, y es lo que hace un cliente que vuelve a autenticarse.
	time.Sleep(50 * time.Millisecond)

	secondPIN := "9876"
	secondToken := deviceToken(t, issuer, fixture.tenantID, fixture.userID)

	second := postDeviceBind(t, router, secondToken, `{"pin":"`+secondPIN+`","device_id":"`+payload.Device.ID+`"}`, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("el re-bind = %d, want 200 (cuerpo %q)", second.Code, second.Body.String())
	}
	if got := decodeDeviceBindPayload(t, second).Device.ID; got != payload.Device.ID {
		t.Errorf("el re-bind devolvió device.id = %q, want el mismo %q", got, payload.Device.ID)
	}

	// Repetir el bind no crea un segundo dispositivo (decisión D5).
	if got := countDevices(t, tenants, fixture.tenantID); got != 1 {
		t.Fatalf("devices del tenant = %d filas, want 1: el re-bind no puede duplicar el dispositivo", got)
	}

	refreshed, found := readDevice(t, tenants, fixture.tenantID, payload.Device.ID)
	if !found {
		t.Fatalf("el dispositivo %s desapareció tras el re-bind", payload.Device.ID)
	}
	if !refreshed.validUntil.After(stored.validUntil) {
		t.Errorf("offline_session_valid_until = %v tras el re-bind, want más tarde que %v", refreshed.validUntil, stored.validUntil)
	}
	// `created_at` es la hora del bind original: las columnas mutables del ciclo de auth son
	// `user_id`, `pin_hash` y la ventana (docs/DATABASE.md §4 `devices`).
	if !refreshed.createdAt.Equal(stored.createdAt) {
		t.Errorf("created_at = %v tras el re-bind, want %v sin cambios", refreshed.createdAt, stored.createdAt)
	}
	if refreshed.pinHash == stored.pinHash {
		t.Error("pin_hash no cambió: el re-bind tiene que volver a hashear el PIN nuevo")
	}
	if matches, err := identity.VerifyCredential(refreshed.pinHash, secondPIN); err != nil || !matches {
		t.Errorf("VerifyCredential(pin_hash, segundo pin) = (%v, %v), want (true, nil)", matches, err)
	}
	if matches, err := identity.VerifyCredential(refreshed.pinHash, firstPIN); err != nil || matches {
		t.Errorf("el PIN anterior sigue verificando tras el re-bind: (%v, %v)", matches, err)
	}

	// (a) cierre del criterio: el access token del PRIMER bind ya venció y la ventana offline
	// sigue abierta, con el PIN del re-bind todavía verificando contra la fila. Eso es «un
	// access token expirado con un PIN válido todavía desbloquea el POS offline».
	waitForExpiry(t, issuer, firstToken)

	afterExpiry, found := readDevice(t, tenants, fixture.tenantID, payload.Device.ID)
	if !found {
		t.Fatalf("el dispositivo %s no se puede leer tras el vencimiento del token", payload.Device.ID)
	}
	if !afterExpiry.validUntil.After(time.Now()) {
		t.Errorf("offline_session_valid_until = %v ya venció cuando el token venció: la ventana está acotada por el token", afterExpiry.validUntil)
	}
	if matches, err := identity.VerifyCredential(afterExpiry.pinHash, secondPIN); err != nil || !matches {
		t.Errorf("con el access token vencido, el PIN dejó de verificar: (%v, %v)", matches, err)
	}

	// (e) El registro del proceso lleva el id del dispositivo y nunca el PIN.
	if strings.Contains(logBuf.String(), firstPIN) || strings.Contains(logBuf.String(), secondPIN) {
		t.Errorf("el registro del proceso contiene un PIN: %q", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), payload.Device.ID) {
		t.Errorf("el registro del proceso no lleva el id del dispositivo: %q", logBuf.String())
	}
}

// TestDeviceBindScopesToTheTokensTenantAndAnswers404OutsideIt cubre los criterios (c) y (d):
//
//   - (c) la fila nueva lleva el tenant y el usuario del TOKEN, aunque el cuerpo y un encabezado
//     `X-Tenant-Id` traigan el tenant de otro;
//   - (d) un `device_id` que pertenece a OTRO tenant no cambia nada y responde 404, con la fila
//     ajena intacta byte a byte.
func TestDeviceBindScopesToTheTokensTenantAndAnswers404OutsideIt(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	tenantA := loginFixture{
		label:       "A",
		tenantID:    "019a0000-0000-7000-8000-0000000000d1",
		userID:      "019a0000-0000-7000-8000-0000000000d2",
		phone:       "+51900000211",
		password:    "secreto-del-tenant-a",
		productID:   "019a0000-0000-7000-8000-0000000000d3",
		productName: "producto de A",
	}
	tenantB := loginFixture{
		label:       "B",
		tenantID:    "019a0000-0000-7000-8000-0000000000e1",
		userID:      "019a0000-0000-7000-8000-0000000000e2",
		phone:       "+51900000212",
		password:    "secreto-del-tenant-b",
		productID:   "019a0000-0000-7000-8000-0000000000e3",
		productName: "producto de B",
	}
	tenantA.seed(t, db)
	tenantB.seed(t, db)

	deviceB := deviceFixture{
		label:      "B",
		tenantID:   tenantB.tenantID,
		userID:     tenantB.userID,
		deviceID:   "019a0000-0000-7000-8000-0000000000e4",
		pin:        "4321",
		validUntil: time.Now().Add(10 * 24 * time.Hour),
	}
	deviceB.seed(t, db)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))

	var logBuf strings.Builder
	router, issuer := newDeviceBindIntegrationRouter(t, tenants, time.Hour, &logBuf)

	// (c) El cuerpo lleva el tenant de B y el encabezado también; el token es de A.
	forgedBody := `{"pin":"1357","tenant_id":"` + tenantB.tenantID + `","tenantId":"` + tenantB.tenantID + `"}`
	recorder := postDeviceBind(t, router, deviceToken(t, issuer, tenantA.tenantID, tenantA.userID), forgedBody, map[string]string{
		"X-Tenant-Id": tenantB.tenantID,
		"X-Tenant":    tenantB.tenantID,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200: el campo de tenant desconocido se ignora (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	payload := decodeDeviceBindPayload(t, recorder)
	if strings.Contains(recorder.Body.String(), tenantB.tenantID) {
		t.Errorf("la respuesta repite el tenant falsificado: %q", recorder.Body.String())
	}

	stored, found := readDevice(t, tenants, tenantA.tenantID, payload.Device.ID)
	if !found {
		t.Fatalf("el dispositivo %s no quedó en el tenant del token (%s)", payload.Device.ID, tenantA.tenantID)
	}
	if stored.tenantID != tenantA.tenantID {
		t.Errorf("devices.tenant_id = %q, want el del token %q y NO el del cuerpo/encabezado %q",
			stored.tenantID, tenantA.tenantID, tenantB.tenantID)
	}
	if stored.userID != tenantA.userID {
		t.Errorf("devices.user_id = %q, want el del token %q", stored.userID, tenantA.userID)
	}

	// El control que hace falsable la aserción: la fila nueva NO es visible con el contexto de
	// B, así que el tenant que quedó escrito es realmente el que el RLS aplica.
	if _, visible := readDevice(t, tenants, tenantB.tenantID, payload.Device.ID); visible {
		t.Errorf("el dispositivo del tenant A es visible desde el contexto de B: el aislamiento no se aplicó")
	}

	// Las dos filas siguen existiendo, una por tenant.
	if got := countDevices(t, tenants, tenantA.tenantID); got != 1 {
		t.Errorf("devices del tenant A = %d filas, want 1", got)
	}
	if got := countDevices(t, tenants, tenantB.tenantID); got != 1 {
		t.Errorf("devices del tenant B = %d filas, want 1", got)
	}

	// (d) El `device_id` de B con el token de A: 404 y la fila de B intacta.
	before, found := readDevice(t, tenants, tenantB.tenantID, deviceB.deviceID)
	if !found {
		t.Fatalf("el dispositivo sembrado %s de B no existe", deviceB.deviceID)
	}

	second := postDeviceBind(t, router, deviceToken(t, issuer, tenantA.tenantID, tenantA.userID), `{"pin":"2468","device_id":"`+deviceB.deviceID+`"}`, nil)

	if second.Code != http.StatusNotFound {
		t.Fatalf("el bind de un device_id de otro tenant = %d, want 404 (cuerpo %q)", second.Code, second.Body.String())
	}
	if got := decodeEnvelope(t, second).Error.Code; got != httpx.CodeNotFound {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeNotFound)
	}

	after, found := readDevice(t, tenants, tenantB.tenantID, deviceB.deviceID)
	if !found {
		t.Fatalf("el dispositivo %s de B desapareció", deviceB.deviceID)
	}
	// Byte a byte: las seis columnas de la fila ajena, sin excepción.
	if after.id != before.id || after.tenantID != before.tenantID || after.userID != before.userID ||
		after.pinHash != before.pinHash ||
		!after.validUntil.Equal(before.validUntil) || !after.createdAt.Equal(before.createdAt) {
		t.Errorf("la fila del tenant B cambió:\n  antes   = %+v\n  después = %+v", before, after)
	}
	// Y el intento no dejó una fila nueva en el tenant del token: el 404 no crea nada.
	if got := countDevices(t, tenants, tenantA.tenantID); got != 1 {
		t.Errorf("devices del tenant A = %d filas tras el 404, want 1", got)
	}

	// Un device_id que no existe en ningún tenant recorre el mismo camino: cero filas, 404.
	unknown := postDeviceBind(t, router, deviceToken(t, issuer, tenantA.tenantID, tenantA.userID),
		`{"pin":"2468","device_id":"019a0000-0000-7000-8000-0000000000ff"}`, nil)
	if unknown.Code != http.StatusNotFound {
		t.Errorf("el bind de un device_id inexistente = %d, want 404 (cuerpo %q)", unknown.Code, unknown.Body.String())
	}

	if strings.Contains(logBuf.String(), "1357") || strings.Contains(logBuf.String(), "2468") {
		t.Errorf("el registro del proceso contiene un PIN: %q", logBuf.String())
	}
}

// TestDeviceBindNeedsAnAccessToken cubre el criterio (f) sobre el router real y la base real: un
// token ausente o mal formado no llega al enlazador y no escribe ninguna fila.
func TestDeviceBindNeedsAnAccessToken(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de integración: necesita el contenedor local de PostgreSQL (docker compose up -d); se salta con -short (SPRINTS.md §8)")
	}

	db := newThrowawayDatabase(t)

	fixture := loginFixture{
		label:       "A",
		tenantID:    "019a0000-0000-7000-8000-0000000000f1",
		userID:      "019a0000-0000-7000-8000-0000000000f2",
		phone:       "+51900000221",
		password:    "secreto-del-tenant-a",
		productID:   "019a0000-0000-7000-8000-0000000000f3",
		productName: "producto de A",
	}
	fixture.seed(t, db)

	tenants := postgres.NewTenantPool(db.rolePool(t, config.AppDatabaseURL(), "pos_app"))

	var logBuf strings.Builder
	router, _ := newDeviceBindIntegrationRouter(t, tenants, time.Hour, &logBuf)

	for name, authorization := range map[string]string{
		"absent header":  "",
		"another scheme": "Basic dXNhcmlvOnNlY3JldG8=",
		"not a jwt":      "Bearer no-soy-un-token",
		"expired token":  "Bearer " + expiredToken(t),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/auth/device-bind", strings.NewReader(`{"pin":"1234"}`))
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if got := decodeEnvelope(t, recorder).Error.Code; got != httpx.CodeUnauthenticated {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeUnauthenticated)
			}
		})
	}

	if got := countDevices(t, tenants, fixture.tenantID); got != 0 {
		t.Errorf("devices del tenant = %d filas, want 0: una petición sin token no escribe nada", got)
	}
	if strings.Contains(logBuf.String(), "1234") {
		t.Errorf("el registro del proceso contiene el PIN: %q", logBuf.String())
	}
}
