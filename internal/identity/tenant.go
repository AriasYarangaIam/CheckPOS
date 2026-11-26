package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Este archivo implementa la segunda credencial del sistema (W8-DB-1 y W8-BE-1;
// docs/BACKEND.md §4.4): la contraseña MAESTRA del tenant, que no pertenece a ningún cajero y
// que solo el dueño conoce.
//
// Dos roles, dos caminos, y ninguno de los dos se mezcla con el login de empleado:
//
//   - la LECTURA la hace `pos_auth` sobre `tenants`, con una concesión por columna —`id`,
//     `name`, `master_password_hash`— porque el login de administrador corre antes de que
//     exista un token (migrations/20261031000001_admin_credential.sql);
//   - la ESCRITURA la hace el rol de migración desde `cmd/maintenance admin-password`, porque
//     la superficie HTTP no tiene un camino para fijarla (decisión D-B = B1 de
//     odd/tasks/sprint-8-admin-hardening.md).
//
// Ningún camino devuelve, registra ni transporta la contraseña en claro: solo su hash.

// ErrUnknownTenant es el resultado de un alta sobre un tenant que no existe. El login de
// administrador lo traduce a su `401` genérico junto con la contraseña incorrecta, sin
// distinguir las dos causas (docs/BACKEND.md §4.4).
var ErrUnknownTenant = errors.New("no tenant has that id")

// TenantCredential es lo que el login de administrador necesita del registro de tenants: el
// nombre para la respuesta y el hash contra el que verificar. No lleva el plan ni ningún otro
// dato del registro, porque el rol pre-auth no puede leerlos.
type TenantCredential struct {
	Name               string
	MasterPasswordHash string
}

// TenantDirectory es la lectura pre-auth del registro de tenants. Es una interfaz para que el
// handler se pruebe sin base y para que exista exactamente un camino de lectura.
type TenantDirectory interface {
	MasterCredential(ctx context.Context, tenantID uuid.UUID) (TenantCredential, error)
}

// PostgresTenantDirectory lee `tenants` con el pool del rol `pos_auth`.
type PostgresTenantDirectory struct {
	pool *pgxpool.Pool
}

// NewPostgresTenantDirectory construye el directorio sobre el pool pre-auth.
func NewPostgresTenantDirectory(pool *pgxpool.Pool) *PostgresTenantDirectory {
	return &PostgresTenantDirectory{pool: pool}
}

// masterCredentialSQL es literalmente la superficie que la concesión por columna habilita: las
// tres columnas del login y ninguna otra. Pedir `plan` o `created_at` acá fallaría con 42501,
// que es exactamente la propiedad que la migración busca.
const masterCredentialSQL = `SELECT name, master_password_hash FROM tenants WHERE id = $1`

// MasterCredential devuelve el nombre del tenant y su hash maestro.
func (d *PostgresTenantDirectory) MasterCredential(ctx context.Context, tenantID uuid.UUID) (TenantCredential, error) {
	if d == nil || d.pool == nil {
		return TenantCredential{}, errors.New("the tenant directory has no connection pool")
	}
	if tenantID == uuid.Nil {
		return TenantCredential{}, errors.New("the tenant directory needs a tenant id")
	}

	var credential TenantCredential
	err := d.pool.QueryRow(ctx, masterCredentialSQL, tenantID).Scan(&credential.Name, &credential.MasterPasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantCredential{}, ErrUnknownTenant
	}
	if err != nil {
		return TenantCredential{}, fmt.Errorf("read the master credential of the tenant %s: %w", tenantID, err)
	}
	return credential, nil
}

// credentialExecer es el mínimo que el alta necesita de una conexión: `*sql.DB` en el comando y
// en las pruebas, y cualquier otro ejecutor que implemente el mismo método.
type credentialExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// updateMasterPasswordSQL escribe SOLO el hash: la contraseña en claro no toca la base ni el
// registro del operador.
const updateMasterPasswordSQL = `UPDATE tenants SET master_password_hash = $2 WHERE id = $1`

// SetMasterPassword fija la contraseña maestra de UN tenant y devuelve un error si el tenant no
// existe.
//
// La credencial se hashea tal como llegó —solo se recortan los saltos de línea del canal—: un
// espacio al final es parte de la contraseña del dueño, y recortarlo en silencio haría que una
// contraseña correcta fallara en el login. Lo que sí se rechaza es una credencial vacía o en
// blanco: no es una contraseña, y aceptarla dejaría al dueño con una credencial trivial.
func SetMasterPassword(ctx context.Context, db credentialExecer, tenantID uuid.UUID, password string) error {
	if db == nil {
		return errors.New("setting the master password needs a database handle")
	}
	if tenantID == uuid.Nil {
		return errors.New("setting the master password needs the tenant id")
	}

	credential := strings.TrimRight(password, "\r\n")
	if strings.TrimSpace(credential) == "" {
		return errors.New("the master password cannot be empty")
	}

	hash, err := HashCredential(credential)
	if err != nil {
		return fmt.Errorf("hash the master password: %w", err)
	}

	result, err := db.ExecContext(ctx, updateMasterPasswordSQL, tenantID, hash)
	if err != nil {
		return fmt.Errorf("set the master password of the tenant %s: %w", tenantID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count the tenants updated by the master password: %w", err)
	}
	if affected == 0 {
		return ErrUnknownTenant
	}
	return nil
}
