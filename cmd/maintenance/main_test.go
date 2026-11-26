// Pruebas del subcomando `admin-password` (W8-DB-1, decisión D-B = B1 de
// odd/tasks/sprint-8-admin-hardening.md).
//
// El subcomando es la ÚNICA forma de fijar la contraseña maestra del tenant: la superficie HTTP
// no tiene un camino para eso (los 13 caminos de docs/BACKEND.md §4 la agotan). Lo que estas
// pruebas fijan sin base de datos es la disciplina del camino: la credencial se lee por entrada
// estándar —nunca por argumento, que quedaría en el historial del shell y en la tabla de
// procesos—, el tenant es obligatorio, una credencial vacía no se escribe y el registro
// estructurado no contiene la contraseña.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const commandTenantID = "019c0000-0000-7000-8000-0000000000b1"

// recordingSetter es el alta inyectada: registra lo que el subcomando le pidió sin tocar la base.
type recordingSetter struct {
	calls []struct {
		tenantID uuid.UUID
		password string
	}
	err error
}

func (s *recordingSetter) set(_ context.Context, tenantID uuid.UUID, password string) error {
	s.calls = append(s.calls, struct {
		tenantID uuid.UUID
		password string
	}{tenantID: tenantID, password: password})
	return s.err
}

// discardLogger es el registro de las pruebas que no miden el log.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestAdminPasswordReadsTheCredentialFromStandardInput: el argumento lleva solo el tenant; la
// contraseña entra por la entrada estándar y llega al alta sin el salto de línea.
func TestAdminPasswordReadsTheCredentialFromStandardInput(t *testing.T) {
	setter := &recordingSetter{}

	err := runAdminPassword(context.Background(), discardLogger(), []string{"--tenant", commandTenantID},
		strings.NewReader("clave-maestra\r\n"), setter.set)

	if err != nil {
		t.Fatalf("runAdminPassword: %v", err)
	}
	if len(setter.calls) != 1 {
		t.Fatalf("llamadas al alta = %d, want 1", len(setter.calls))
	}
	if got := setter.calls[0].tenantID.String(); got != commandTenantID {
		t.Errorf("tenant = %s, want %s", got, commandTenantID)
	}
	if got := setter.calls[0].password; got != "clave-maestra" {
		t.Errorf("password = %q, want %q sin el salto de línea", got, "clave-maestra")
	}
}

// TestAdminPasswordRejectsAMissingTenantOrAnEmptyCredential: ni el tenant ni la credencial
// tienen valor tácito; un camino incompleto no escribe nada.
func TestAdminPasswordRejectsAMissingTenantOrAnEmptyCredential(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "sin --tenant", args: nil, stdin: "clave\n"},
		{name: "tenant que no es uuid", args: []string{"--tenant", "bodega"}, stdin: "clave\n"},
		{name: "credencial vacía", args: []string{"--tenant", commandTenantID}, stdin: "\n"},
		{name: "credencial en blanco", args: []string{"--tenant", commandTenantID}, stdin: "   \n"},
		{name: "sin entrada", args: []string{"--tenant", commandTenantID}, stdin: ""},
		{name: "argumento desconocido", args: []string{"--tenant", commandTenantID, "--password", "clave"}, stdin: "clave\n"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			setter := &recordingSetter{}

			err := runAdminPassword(context.Background(), discardLogger(), testCase.args,
				strings.NewReader(testCase.stdin), setter.set)

			if err == nil {
				t.Fatal("el subcomando aceptó un camino incompleto")
			}
			if len(setter.calls) != 0 {
				t.Errorf("el alta corrió %d vez(es) con un camino incompleto, want 0", len(setter.calls))
			}
		})
	}
}

// TestAdminPasswordNeverLogsTheCredential: el registro estructurado nombra el tenant y jamás la
// contraseña — esa línea la lee el operador y puede terminar en un agregador de logs (W8-BE-4).
func TestAdminPasswordNeverLogsTheCredential(t *testing.T) {
	const password = "clave-que-no-debe-aparecer"

	var log strings.Builder
	setter := &recordingSetter{}

	err := runAdminPassword(context.Background(), slog.New(slog.NewJSONHandler(&log, nil)),
		[]string{"--tenant", commandTenantID}, strings.NewReader(password+"\n"), setter.set)

	if err != nil {
		t.Fatalf("runAdminPassword: %v", err)
	}

	logged := log.String()
	if strings.Contains(logged, password) {
		t.Errorf("el registro contiene la contraseña: %q", logged)
	}
	if !strings.Contains(logged, commandTenantID) {
		t.Errorf("el registro no nombra el tenant: %q", logged)
	}
}

// TestAdminPasswordPropagatesTheSetterError: un fallo del alta llega al operador como error del
// comando —con la salida no-cero de run— y no como un éxito silencioso.
func TestAdminPasswordPropagatesTheSetterError(t *testing.T) {
	setter := &recordingSetter{err: errors.New("no such tenant")}

	err := runAdminPassword(context.Background(), discardLogger(), []string{"--tenant", commandTenantID},
		strings.NewReader("clave\n"), setter.set)

	if err == nil {
		t.Fatal("el fallo del alta no llegó al comando")
	}
	if !strings.Contains(err.Error(), "no such tenant") {
		t.Errorf("el error perdió la causa: %v", err)
	}
}

// TestRunDispatchesTheAdminPasswordSubcommand: el despacho del comando reconoce el subcomando y
// falla ANTES de abrir cualquier conexión cuando el camino está incompleto. Es la única prueba del
// cableado `run` → `runAdminPassword` que no necesita base de datos.
func TestRunDispatchesTheAdminPasswordSubcommand(t *testing.T) {
	err := run(context.Background(), []string{"admin-password"})
	if err == nil {
		t.Fatal("run admin-password sin --tenant no falló")
	}
	if !strings.Contains(err.Error(), "admin-password") {
		t.Errorf("el error no nombra el subcomando: %v", err)
	}

	if err := run(context.Background(), []string{"no-existe"}); err == nil {
		t.Fatal("run no rechazó un subcomando desconocido")
	}
}
