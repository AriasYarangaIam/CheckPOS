// Package migrations embeds the versioned SQL schema migrations so that the
// migration runner is a plain Go program: no external goose binary, no `make`.
//
// The files are goose SQL migrations ("-- +goose Up" / "-- +goose Down") and are
// applied in filename order. See docs/DATABASE.md for the schema contract they
// translate.
package migrations

import "embed"

// FS holds every SQL migration in this directory. The embedded filesystem is
// rooted at this package's directory, so goose is pointed at ".".
//
//go:embed *.sql
var FS embed.FS
