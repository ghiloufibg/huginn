# Huginn — agent instructions

Read-only Kubernetes (GKE) pod-log TUI in Go. It is a prototype meant to be adapted later to an enterprise codebase, so maintainability and adaptability come first.

- Follow `docs/ARCHITECTURE.md` strictly (hexagonal architecture: domain → ports → app; adapters on the outside; wiring only in `internal/bootstrap`). Never import adapters or third-party I/O libraries from `internal/core`.
- Anything specific to a company (JSON field names, manifest paths, namespaces, label keys, secret locations) must be config or an adapter, never hard-coded in the core.
- Record non-trivial choices in `docs/DECISIONS.md`.
- Before committing: `go test ./...`, `golangci-lint run`.
