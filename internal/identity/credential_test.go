package identity_test

import (
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
)

// TestCredentialHashRoundTrip cubre el primitivo de credenciales: argon2id con sus
// parámetros y su sal DENTRO de la cadena guardada, de modo que verificar no dependa de
// ningún valor compilado. El mismo primitivo sirve para `users.password_hash` y, en el
// corte siguiente, para `devices.pin_hash` (docs/DATABASE.md §2.10, §4 `devices`).
func TestCredentialHashRoundTrip(t *testing.T) {
	const credential = "una-contraseña-de-prueba"

	stored, err := identity.HashCredential(credential)
	if err != nil {
		t.Fatalf("HashCredential: error inesperado: %v", err)
	}

	// La forma de la cadena es la del contrato PHC de argon2: identificador, versión y los
	// tres parámetros antes de la sal y el hash. Se comprueba el texto porque es lo que
	// permite migrar de parámetros sin invalidar las credenciales ya guardadas.
	const prefix = "$argon2id$v=19$m=65536,t=3,p=2$"
	if !strings.HasPrefix(stored, prefix) {
		t.Errorf("la credencial guardada = %q, want el prefijo %q", stored, prefix)
	}
	if parts := strings.Split(stored, "$"); len(parts) != 6 {
		t.Errorf("la credencial guardada tiene %d campos separados por '$', want 6 (identificador, versión, parámetros, sal, hash)", len(parts))
	}
	if strings.Contains(stored, credential) {
		t.Errorf("la credencial guardada contiene la credencial en claro: %q", stored)
	}

	ok, err := identity.VerifyCredential(stored, credential)
	if err != nil {
		t.Fatalf("VerifyCredential sobre una credencial correcta: error inesperado: %v", err)
	}
	if !ok {
		t.Error("VerifyCredential = false con la credencial correcta, want true")
	}
}

// TestCredentialHashSaltedPerHash fija que la sal sea aleatoria: dos credenciales iguales
// no pueden producir la misma cadena guardada, o el hash sería una tabla de búsqueda
// precalculada para todo el que lea la base.
func TestCredentialHashSaltedPerHash(t *testing.T) {
	const credential = "la-misma-credencial"

	first, err := identity.HashCredential(credential)
	if err != nil {
		t.Fatalf("HashCredential (primera): %v", err)
	}
	second, err := identity.HashCredential(credential)
	if err != nil {
		t.Fatalf("HashCredential (segunda): %v", err)
	}

	if first == second {
		t.Errorf("dos hashes de la misma credencial salieron idénticos (%q): la sal no es aleatoria", first)
	}
	for _, stored := range []string{first, second} {
		ok, err := identity.VerifyCredential(stored, credential)
		if err != nil || !ok {
			t.Errorf("VerifyCredential(%q) = (%v, %v), want (true, nil)", stored, ok, err)
		}
	}
}

func TestVerifyCredentialRejectsTheWrongCredential(t *testing.T) {
	stored, err := identity.HashCredential("la-correcta")
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}

	cases := []struct {
		name       string
		credential string
	}{
		{name: "another credential", credential: "la-incorrecta"},
		{name: "the empty credential", credential: ""},
		{name: "a prefix of the credential", credential: "la-correct"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := identity.VerifyCredential(stored, tc.credential)
			if err != nil {
				t.Fatalf("VerifyCredential con una credencial incorrecta: error inesperado: %v (una credencial que no coincide es «no», no un error)", err)
			}
			if ok {
				t.Error("VerifyCredential = true con una credencial incorrecta, want false")
			}
		})
	}
}

// TestVerifyCredentialRejectsAMalformedStoredHash cubre la credencial guardada que no es una
// cadena argon2id: la verificación tiene que fallar con un error y NUNCA entrar en pánico ni
// aceptar. Es el caso de una fila corrupta o de un hash de otro algoritmo.
func TestVerifyCredentialRejectsAMalformedStoredHash(t *testing.T) {
	valid, err := identity.HashCredential("una-credencial")
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}
	parts := strings.Split(valid, "$")

	cases := []struct {
		name   string
		stored string
	}{
		{name: "empty", stored: ""},
		{name: "plain text", stored: "not-a-hash-at-all"},
		{name: "another algorithm", stored: strings.Replace(valid, "$argon2id$", "$argon2i$", 1)},
		{name: "another version", stored: "$argon2id$v=13$m=65536,t=3,p=2$" + parts[4] + "$" + parts[5]},
		{name: "missing the hash field", stored: strings.Join(parts[:5], "$")},
		{name: "an extra field", stored: valid + "$extra"},
		{name: "params in another order", stored: "$argon2id$v=19$t=3,m=65536,p=2$" + parts[4] + "$" + parts[5]},
		{name: "a non numeric parameter", stored: "$argon2id$v=19$m=big,t=3,p=2$" + parts[4] + "$" + parts[5]},
		{name: "a salt that is not base64", stored: "$argon2id$v=19$m=65536,t=3,p=2$!!!!$" + parts[5]},
		{name: "a hash that is not base64", stored: "$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$!!!!"},
		{name: "an empty salt", stored: "$argon2id$v=19$m=65536,t=3,p=2$$" + parts[5]},
		{name: "an empty hash", stored: "$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$"},
		// Un coste absurdo en la cadena guardada no puede convertirse en una reserva de
		// memoria arbitraria cada vez que alguien intenta entrar: los parámetros se leen de
		// la cadena, así que sus límites también tienen que estar acotados.
		{name: "an absurd memory cost", stored: "$argon2id$v=19$m=4294967295,t=3,p=2$" + parts[4] + "$" + parts[5]},
		{name: "an absurd iteration count", stored: "$argon2id$v=19$m=65536,t=4294967295,p=2$" + parts[4] + "$" + parts[5]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := identity.VerifyCredential(tc.stored, "una-credencial")
			if err == nil {
				t.Fatalf("VerifyCredential(%q) = (%v, nil), want un error", tc.stored, ok)
			}
			if ok {
				t.Error("VerifyCredential = true sobre una cadena malformada, want false")
			}
		})
	}
}

// TestVerifyCredentialRejectsATamperedHash deja el resto de la cadena intacto y cambia el
// hash: es el ataque directo —editar la fila para que coincida con una credencial conocida—
// y tiene que dar «no» sin error.
func TestVerifyCredentialRejectsATamperedHash(t *testing.T) {
	const credential = "la-credencial"
	stored, err := identity.HashCredential(credential)
	if err != nil {
		t.Fatalf("HashCredential: %v", err)
	}

	parts := strings.Split(stored, "$")
	hash := []byte(parts[5])
	if hash[0] == 'A' {
		hash[0] = 'B'
	} else {
		hash[0] = 'A'
	}
	tampered := strings.Join([]string{parts[0], parts[1], parts[2], parts[3], parts[4], string(hash)}, "$")

	ok, err := identity.VerifyCredential(tampered, credential)
	if err != nil {
		t.Fatalf("VerifyCredential sobre un hash alterado: error inesperado: %v", err)
	}
	if ok {
		t.Error("VerifyCredential = true sobre un hash alterado, want false")
	}
}
