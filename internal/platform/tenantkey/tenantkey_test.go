package tenantkey_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/tenantkey"
)

// tenantA y tenantB son dos ids válidos y distintos, ambos UUIDv7 como los que genera la
// aplicación (docs/DATABASE.md §2.2). El segundo comparte el prefijo textual del primero a
// propósito: `...01` contra `...02`.
var (
	tenantA = uuid.MustParse("019a0000-0000-7000-8000-000000000001")
	tenantB = uuid.MustParse("019a0000-0000-7000-8000-000000000002")
)

// TestKeyStartsWithTheTenantPrefixForEveryPartCount cubre la forma de la clave para uno, dos y
// tres segmentos: el prefijo `tenant:{id}:` es obligatorio y las partes van detrás en orden.
func TestKeyStartsWithTheTenantPrefixForEveryPartCount(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{
			name:  "one part",
			parts: []string{"catalog"},
			want:  "tenant:" + tenantA.String() + ":catalog",
		},
		{
			name:  "two parts",
			parts: []string{"catalog", "page"},
			want:  "tenant:" + tenantA.String() + ":catalog:page",
		},
		{
			name:  "three parts",
			parts: []string{"catalog", "global-product", "7501234567890"},
			want:  "tenant:" + tenantA.String() + ":catalog:global-product:7501234567890",
		},
		{
			name:  "a part that carries its own separator",
			parts: []string{"sync:cursor", "device"},
			want:  "tenant:" + tenantA.String() + ":sync:cursor:device",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := tenantkey.Key(tenantA, tc.parts...)
			if err != nil {
				t.Fatalf("Key devolvió error: %v", err)
			}
			if key != tc.want {
				t.Errorf("Key = %q, want %q", key, tc.want)
			}
			if !strings.HasPrefix(key, "tenant:"+tenantA.String()+":") {
				t.Errorf("Key = %q, want el prefijo tenant:{id}: (ARCHITECTURE.md:20)", key)
			}
		})
	}
}

// TestKeyRejectsTheNilTenant: el UUID nulo no es el id de ninguna fila, así que una clave
// construida con él no pertenece a ningún tenant.
func TestKeyRejectsTheNilTenant(t *testing.T) {
	for _, zero := range []uuid.UUID{uuid.Nil, {}} {
		key, err := tenantkey.Key(zero, "catalog")
		if !errors.Is(err, tenantkey.ErrNilTenant) {
			t.Errorf("Key(%s, \"catalog\") error = %v, want ErrNilTenant", zero, err)
		}
		if key != "" {
			t.Errorf("Key(%s, \"catalog\") = %q, want la cadena vacía junto al error", zero, key)
		}
	}
}

// TestKeyRejectsAnEmptyPartSet: sin partes la clave no nombra nada, y una parte en blanco deja un
// segmento que no se puede leer de vuelta.
func TestKeyRejectsAnEmptyPartSet(t *testing.T) {
	cases := map[string][]string{
		"no parts at all":       {},
		"one empty part":        {""},
		"one blank part":        {"   "},
		"a blank part in front": {"", "catalog"},
		"a blank part behind":   {"catalog", "\t"},
		"a blank part between":  {"catalog", "", "page"},
	}

	for name, parts := range cases {
		t.Run(name, func(t *testing.T) {
			key, err := tenantkey.Key(tenantA, parts...)
			if !errors.Is(err, tenantkey.ErrEmptyPart) {
				t.Errorf("Key(%s, %#v) error = %v, want ErrEmptyPart", tenantA, parts, err)
			}
			if key != "" {
				t.Errorf("Key(%s, %#v) = %q, want la cadena vacía junto al error", tenantA, parts, key)
			}
		})
	}
}

// TestTwoTenantsNeverShareAKey es la propiedad que la regla del prefijo protege: las mismas
// partes bajo dos tenants distintos no pueden producir la misma clave.
func TestTwoTenantsNeverShareAKey(t *testing.T) {
	parts := []string{"catalog", "global-product", "7501234567890"}

	keyA, err := tenantkey.Key(tenantA, parts...)
	if err != nil {
		t.Fatalf("Key(tenantA): %v", err)
	}
	keyB, err := tenantkey.Key(tenantB, parts...)
	if err != nil {
		t.Fatalf("Key(tenantB): %v", err)
	}

	if keyA == keyB {
		t.Fatalf("los dos tenants comparten la clave %q: el aislamiento del caché depende del prefijo", keyA)
	}
	// Y no alcanza con que difieran: cada clave tiene que llevar SU tenant, o una lectura por
	// prefijo devolvería las dos.
	if !strings.HasPrefix(keyA, "tenant:"+tenantA.String()+":") {
		t.Errorf("Key(tenantA) = %q, want el tenant A en el prefijo", keyA)
	}
	if !strings.HasPrefix(keyB, "tenant:"+tenantB.String()+":") {
		t.Errorf("Key(tenantB) = %q, want el tenant B en el prefijo", keyB)
	}
}
