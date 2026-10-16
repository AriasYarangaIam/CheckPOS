package identity

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// unknownPhoneDirectory es un directorio que siempre dice que el teléfono no existe.
type unknownPhoneDirectory struct{}

func (unknownPhoneDirectory) CredentialsByPhone(_ context.Context, _ string) (Credentials, error) {
	return Credentials{}, ErrUnknownPhone
}

// TestLoginVerifiesEvenWhenThePhoneIsUnknown fija, desde adentro del paquete, la propiedad de
// paridad de tiempo que el contrato pide: el camino «teléfono desconocido» NO se saltea la
// verificación. Se prueba así, y no midiendo tiempos, porque una comparación de duraciones es
// inestable en la máquina de integración: lo observable y determinista es que el hash señuelo
// sea un argon2id real y que, incluso conociéndolo, el camino no autentique.
//
// La consecuencia de contrato es la de arriba: quien mida la respuesta no puede deducir si el
// teléfono está registrado (docs/BACKEND.md §4.2, §5.7).
func TestLoginVerifiesEvenWhenThePhoneIsUnknown(t *testing.T) {
	issuer, err := NewTokenIssuer("test-only-signing-key-0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := NewLoginHandler(unknownPhoneDirectory{}, issuer, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)))
	if err != nil {
		t.Fatalf("NewLoginHandler: %v", err)
	}

	// El hash señuelo es un argon2id verificable, no un marcador que haga fallar la
	// verificación por estar mal formado: si lo fuera, el camino «desconocido» volvería antes
	// de calcular argon2 y delataría la diferencia.
	if handler.dummyHash == "" {
		t.Fatal("el handler no tiene hash señuelo: el camino «teléfono desconocido» no verificaría nada")
	}
	if !strings.HasPrefix(handler.dummyHash, "$argon2id$v=19$m=") {
		t.Errorf("el hash señuelo = %q, want una cadena argon2id", handler.dummyHash)
	}

	matches, err := VerifyCredential(handler.dummyHash, dummyCredential)
	if err != nil {
		t.Fatalf("el hash señuelo no es verificable: %v", err)
	}
	if !matches {
		t.Fatal("el hash señuelo no verifica contra su propia credencial: sería un error de formato, no una verificación")
	}

	// Y la prueba de que el señuelo no es una puerta: aun presentando la credencial que el
	// señuelo verifica, un teléfono desconocido no obtiene token.
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"phone":"+51900009999","password":"`+dummyCredential+`"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "token") {
		t.Errorf("la respuesta de un teléfono desconocido lleva un token: %q", recorder.Body.String())
	}
}
