# AGENTS.md — pos-system-saas

## Testing (authoritative)

- **Run:** `go test ./...` from the repo root.
- **HTTP layer is tested in-process by default.** Use `httptest.NewRecorder` + the
  in-process router (see `internal/identity/devicebind_test.go` for the established
  pattern). `go test` covers the HTTP surface without spawning a process or opening
  a port.
- **Integration tests need PostgreSQL via Docker only.** Run `docker compose up -d`
  before suites that touch the database. A container is expected; a spawned server
  process is not.
- **Throwaway databases only.** Integration tests create and drop databases with the
  `pos_test_` prefix (`internal/identity/testdb_test.go`). Never point tests at the
  developer database.
- **Process-level smoke tests are permitted.** When a smoke test of the real binary
  is needed, build and run it. Every spawned process MUST pass `-WindowStyle Hidden`
  (or `-NoNewWindow` when redirecting output) and MUST be stopped before reporting
  results. A visible console window is a defect.

## Rationale

The previous habit of building the binary and starting it in the background produced
visible console windows on Windows (each `powershell.EXE` spawn without window
suppression allocates a new console) and duplicated coverage that `go test` already
provides in-process. Keep tests in-process; keep the terminal free of flashes.
