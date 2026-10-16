package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Este archivo implementa la mitad pre-auth del login: la lectura de `users` por el rol
// dedicado `pos_auth`, que existe para UNA sola forma de consulta (docs/DATABASE.md §5.4,
// docs/BACKEND.md §4.2).
//
// El rol importa: `pos_auth` tiene `SELECT` sobre `users` y sobre ninguna otra tabla, y su
// política `FOR SELECT TO pos_auth USING (true)` es la que le permite leer por encima del
// aislamiento de tenant. No hay JWT todavía, así que no hay contexto de tenant que fijar, y
// esa ausencia es deliberada: es la única forma de encontrar el teléfono del que saldrá el
// tenant.
//
// Nada de acá registra el teléfono en un log: es un identificador de persona (§7.9).

// ErrUnknownPhone es el resultado de una búsqueda sin filas. El login lo traduce a `401`
// genérico junto con la contraseña incorrecta, sin distinguir las dos causas
// (docs/BACKEND.md §4.2).
var ErrUnknownPhone = errors.New("no user has that phone")

// Credentials es lo que el login necesita de `users`: la identidad para el token y el hash
// contra el que verificar. No lleva el teléfono ni ningún otro dato personal.
type Credentials struct {
	UserID       uuid.UUID
	TenantID     uuid.UUID
	PasswordHash string
}

// UserDirectory es la búsqueda pre-auth por teléfono. Es una interfaz para que el handler del
// login se pruebe sin base, y para que exista exactamente un camino de lectura: el que el rol
// pre-auth tiene concedido.
type UserDirectory interface {
	CredentialsByPhone(ctx context.Context, phone string) (Credentials, error)
}

// PostgresUserDirectory lee `users` con el pool del rol `pos_auth`.
type PostgresUserDirectory struct {
	pool *pgxpool.Pool
}

// NewPostgresUserDirectory construye el directorio sobre el pool pre-auth.
func NewPostgresUserDirectory(pool *pgxpool.Pool) *PostgresUserDirectory {
	return &PostgresUserDirectory{pool: pool}
}

// credentialsByPhoneSQL es la única consulta del rol pre-auth, literal: el teléfono es
// globalmente UNIQUE y su índice sostiene la búsqueda (docs/DATABASE.md §4 `users`).
const credentialsByPhoneSQL = `SELECT id, tenant_id, password_hash FROM users WHERE phone = $1`

// CredentialsByPhone resuelve el teléfono a la identidad y al hash de la contraseña.
//
// Un teléfono sin fila devuelve ErrUnknownPhone, y cualquier otra falla del driver se propaga
// envuelta SIN el teléfono: el mensaje llega a un log y un teléfono no pertenece ahí.
func (d *PostgresUserDirectory) CredentialsByPhone(ctx context.Context, phone string) (Credentials, error) {
	if d.pool == nil {
		return Credentials{}, errors.New("the user directory has no connection pool")
	}

	// Los dos identificadores se leen como el tipo UUID del driver y se convierten después:
	// el SQL queda exactamente el de la búsqueda del contrato, sin cast, y la conversión a
	// `uuid.UUID` es la misma representación de 16 bytes que usa la aplicación.
	var (
		id           pgtype.UUID
		tenantID     pgtype.UUID
		passwordHash string
	)

	err := d.pool.QueryRow(ctx, credentialsByPhoneSQL, phone).Scan(&id, &tenantID, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, ErrUnknownPhone
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("look up the user by phone: %w", err)
	}
	if !id.Valid || !tenantID.Valid {
		return Credentials{}, errors.New("the user row has a null id or tenant_id")
	}

	return Credentials{
		UserID:       uuid.UUID(id.Bytes),
		TenantID:     uuid.UUID(tenantID.Bytes),
		PasswordHash: passwordHash,
	}, nil
}
