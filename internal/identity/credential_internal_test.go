package identity

import "testing"

// TestVerifyCredentialUsesTheParametersFromTheStoredString es la prueba de que verificar NO
// tiene los parámetros compilados: genera una credencial con un coste distinto del vigente y
// la verifica. Un verificador que usara las constantes del paquete calcularía otro hash y
// diría que no coincide.
func TestVerifyCredentialUsesTheParametersFromTheStoredString(t *testing.T) {
	cheap := argon2Params{
		memory:      8,
		iterations:  1,
		parallelism: 1,
		saltLength:  8,
		keyLength:   16,
	}
	if cheap == credentialParams {
		t.Fatal("los parámetros de esta prueba coinciden con los vigentes: no probaría que se leen de la cadena")
	}

	const credential = "una-credencial"
	stored, err := hashCredentialWith(credential, cheap)
	if err != nil {
		t.Fatalf("hashCredentialWith: %v", err)
	}

	// La cadena lleva el coste con el que se generó, no el vigente.
	const prefix = "$argon2id$v=19$m=8,t=1,p=1$"
	if len(stored) < len(prefix) || stored[:len(prefix)] != prefix {
		t.Fatalf("la credencial guardada = %q, want el prefijo %q", stored, prefix)
	}

	ok, err := VerifyCredential(stored, credential)
	if err != nil {
		t.Fatalf("VerifyCredential: %v", err)
	}
	if !ok {
		t.Error("VerifyCredential = false sobre una credencial generada con otros parámetros, want true (los parámetros tienen que salir de la cadena guardada)")
	}

	wrong, err := VerifyCredential(stored, "otra-credencial")
	if err != nil {
		t.Fatalf("VerifyCredential con la credencial incorrecta: %v", err)
	}
	if wrong {
		t.Error("VerifyCredential = true con la credencial incorrecta, want false")
	}
}
