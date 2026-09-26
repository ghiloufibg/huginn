# Huginn — agent instructions

Read-only Kubernetes (GKE) pod-log TUI in Go. It is a prototype meant to be adapted later to an enterprise codebase, so maintainability and adaptability come first.

- Follow `docs/ARCHITECTURE.md` strictly (hexagonal architecture: domain → ports → app; adapters on the outside; wiring only in `internal/bootstrap`). Never import adapters or third-party I/O libraries from `internal/core`.
- Anything specific to a company (JSON field names, manifest paths, namespaces, label keys, secret locations) must be config or an adapter, never hard-coded in the core.
- Record non-trivial choices in `docs/DECISIONS.md`.
- Before committing: `go test ./...` and `golangci-lint run` (golangci-lint v2 built with Go ≥ 1.26). After changing config structs: `make schema`. After changing a TUI view: `go test ./internal/adapters/driving/tui -update` and review the golden diff.
- Try the app without a cluster: `make build && ./bin/huginn --demo`.
- New packages must be covered by a rule in `internal/archtest` (the test fails otherwise).
