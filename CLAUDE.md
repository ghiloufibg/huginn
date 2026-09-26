# Huginn — agent instructions

Read-only Kubernetes (GKE) pod-log TUI in Go. It is a prototype meant to be adapted later to an enterprise codebase, so maintainability and adaptability come first.

- Follow `docs/ARCHITECTURE.md` strictly (hexagonal architecture: domain → ports → app; adapters on the outside; wiring only in `internal/bootstrap`). Never import adapters or third-party I/O libraries from `internal/core`.
- Anything specific to an application or company (JSON field names, layouts, namespaces, label keys, sidecars, framework packages) belongs in the user's config folder (`docs/CONFIG.md`, examples in `examples/`), never in code — not even as a default. `internal/archtest` checks it.
- Record non-trivial choices in `docs/DECISIONS.md`.
- Before committing: `go test ./...` and `golangci-lint run` (golangci-lint v2 built with Go ≥ 1.26). After changing config structs: `make schema` and update `docs/CONFIG.md`. After changing a TUI view: `go test ./internal/adapters/driving/tui -update` and review the golden diff.
- Try the app without a cluster: `make build && ./bin/huginn --demo`.
- New packages must be covered by a rule in `internal/archtest` (the test fails otherwise).
