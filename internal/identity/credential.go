package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Este archivo implementa UN primitivo de credenciales, no dos: `users.password_hash` y
// `devices.pin_hash` comparten algoritmo, formato y verificación (docs/DATABASE.md §2.10).
// El PIN de 4 dígitos es una credencial de baja entropía y podría querer parámetros más
// caros o un pepper; el formato los lleva escritos, así que ese cambio no invalida lo ya
// guardado y no obliga a tocar la verificación.
//
// El formato es el del contrato PHC de argon2:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<base64(sal)>$<base64(hash)>
//
// La sal y los parámetros viajan DENTRO de la cadena guardada. Es lo que permite verificar
// sin compilar ningún valor: subir el coste mañana no rompe las credenciales de ayer.

const (
	// argon2ID es el identificador del algoritmo en la cadena guardada. Se usa argon2id y
	// no argon2i ni argon2d: es el que combina resistencia a canal lateral y a memoria
	// (docs/DATABASE.md §2.10).
	argon2ID = "argon2id"

	// argon2Version es la versión del algoritmo que la biblioteca implementa (0x13).
	argon2Version = 19
)

// ErrInvalidCredentialHash es el error de una cadena guardada que no es argon2id en el
// formato esperado. Un verificador no puede tratarla como «credencial incorrecta» en
// silencio: significa que la fila está corrupta o que alguien tocó la base.
var ErrInvalidCredentialHash = errors.New("the stored credential hash is not an argon2id string")

// argon2Params son los parámetros de coste de argon2id. En la verificación nunca salen de
// acá: se leen de la cadena guardada, y los valores de abajo solo los usa la generación.
type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

// credentialParams son los parámetros con los que se generan credenciales nuevas: 64 MiB de
// memoria, 3 pasadas y 2 hilos, con sal de 16 bytes y hash de 32, según la recomendación de
// argon2id para secretos de servidor. No se suben sin medir: cada intento de login paga este
// costo, y el parámetro queda escrito en cada credencial para poder cambiarlo después.
var credentialParams = argon2Params{
	memory:      64 * 1024,
	iterations:  3,
	parallelism: 2,
	saltLength:  16,
	keyLength:   32,
}

// Límites superiores de los parámetros que se aceptan al LEER una cadena guardada. Sin ellos,
// una fila con `m=4294967295` haría que cada intento de login reserve memoria sin límite: el
// parámetro es entrada no confiable en el momento de verificar, aunque la escriba el servidor.
const (
	maxArgon2Memory      = 256 * 1024 // 256 MiB
	maxArgon2Iterations  = 10
	maxArgon2Parallelism = 16
	maxArgon2SaltLength  = 64
	maxArgon2KeyLength   = 128
)

// HashCredential genera una cadena argon2id verificable para la credencial en claro. La
// credencial no se guarda ni se registra en ningún lado: solo vuelve su hash.
func HashCredential(credential string) (string, error) {
	return hashCredentialWith(credential, credentialParams)
}

// hashCredentialWith separa la elección de parámetros de la generación para que una prueba
// pueda usar un coste mínimo sin volver lento el caso de uso real.
func hashCredentialWith(credential string, params argon2Params) (string, error) {
	salt := make([]byte, params.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate the credential salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(credential),
		salt,
		params.iterations,
		params.memory,
		params.parallelism,
		params.keyLength,
	)

	// Se codifica sin relleno (`RawStdEncoding`) para que la cadena no lleve `=` y se pueda
	// partir por `$` sin ambigüedad.
	return fmt.Sprintf(
		"$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2ID,
		argon2Version,
		params.memory,
		params.iterations,
		params.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyCredential comprueba la credencial contra la cadena guardada. Devuelve (false, nil)
// cuando la credencial simplemente no coincide y un error cuando la cadena guardada no es
// verificable: son dos cosas distintas y el llamador necesita distinguirlas.
//
// La comparación es de tiempo constante (`subtle.ConstantTimeCompare`): comparar con `==`
// corta en el primer byte distinto y filtra, byte a byte, cuánto del hash acertó quien
// intenta entrar.
func VerifyCredential(storedHash, credential string) (bool, error) {
	params, salt, want, err := parseCredentialHash(storedHash)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey(
		[]byte(credential),
		salt,
		params.iterations,
		params.memory,
		params.parallelism,
		uint32(len(want)),
	)

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// parseCredentialHash lee parámetros, sal y hash de la cadena guardada. Los parámetros salen
// de acá y no de una constante, que es lo que permite verificar credenciales generadas con
// cualquier conjunto de parámetros anterior.
func parseCredentialHash(storedHash string) (argon2Params, []byte, []byte, error) {
	// El formato tiene exactamente seis campos separados por `$`, y el primero vacío porque
	// la cadena empieza con el separador.
	fields := strings.Split(storedHash, "$")
	if len(fields) != 6 || fields[0] != "" {
		return argon2Params{}, nil, nil, ErrInvalidCredentialHash
	}
	if fields[1] != argon2ID {
		return argon2Params{}, nil, nil, fmt.Errorf("%w: algorithm %q", ErrInvalidCredentialHash, fields[1])
	}

	version, err := parseParameter(fields[2], "v")
	if err != nil {
		return argon2Params{}, nil, nil, err
	}
	if version != argon2Version {
		return argon2Params{}, nil, nil, fmt.Errorf("%w: version %d", ErrInvalidCredentialHash, version)
	}

	// El orden de los parámetros es parte del formato: `m`, `t`, `p`. Un orden distinto es
	// una cadena que no escribió este primitivo.
	names := []string{"m", "t", "p"}
	values := strings.Split(fields[3], ",")
	if len(values) != len(names) {
		return argon2Params{}, nil, nil, ErrInvalidCredentialHash
	}

	parsed := make([]uint64, len(names))
	for index, value := range values {
		parsed[index], err = parseParameter(value, names[index])
		if err != nil {
			return argon2Params{}, nil, nil, err
		}
	}

	params := argon2Params{
		memory:      uint32(parsed[0]),
		iterations:  uint32(parsed[1]),
		parallelism: uint8(parsed[2]),
	}
	if params.memory == 0 || params.memory > maxArgon2Memory ||
		params.iterations == 0 || params.iterations > maxArgon2Iterations ||
		params.parallelism == 0 || params.parallelism > maxArgon2Parallelism {
		return argon2Params{}, nil, nil, fmt.Errorf("%w: parameters m=%d,t=%d,p=%d are out of range",
			ErrInvalidCredentialHash, params.memory, params.iterations, params.parallelism)
	}

	salt, err := decodeField(fields[4])
	if err != nil {
		return argon2Params{}, nil, nil, err
	}
	if len(salt) == 0 || len(salt) > maxArgon2SaltLength {
		return argon2Params{}, nil, nil, fmt.Errorf("%w: salt length %d", ErrInvalidCredentialHash, len(salt))
	}

	hash, err := decodeField(fields[5])
	if err != nil {
		return argon2Params{}, nil, nil, err
	}
	if len(hash) == 0 || len(hash) > maxArgon2KeyLength {
		return argon2Params{}, nil, nil, fmt.Errorf("%w: hash length %d", ErrInvalidCredentialHash, len(hash))
	}

	return params, salt, hash, nil
}

// parseParameter lee un campo `nombre=valor` y exige que el nombre sea el esperado.
func parseParameter(field, name string) (uint64, error) {
	prefix := name + "="
	if !strings.HasPrefix(field, prefix) {
		return 0, fmt.Errorf("%w: expected a %q parameter, got %q", ErrInvalidCredentialHash, name, field)
	}

	value, err := strconv.ParseUint(strings.TrimPrefix(field, prefix), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a number", ErrInvalidCredentialHash, field)
	}
	return value, nil
}

// decodeField decodifica un campo base64 sin relleno. Se usa la variante estricta: una cadena
// que no es base64 legítimo no puede tratarse como «casi» válida.
func decodeField(field string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(field)
	if err != nil {
		return nil, fmt.Errorf("%w: a field is not base64", ErrInvalidCredentialHash)
	}
	return decoded, nil
}
