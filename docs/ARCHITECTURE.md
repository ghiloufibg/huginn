# Huginn — Architecture Rules

Huginn is built first as a **prototype** and will then be adapted to an enterprise codebase (its own log JSON format, its own Kubernetes manifest layout, its own cluster topology, its own secret storage). These rules exist so that adaptation means **adding or configuring an adapter**, never rewriting the core. They are mandatory for every change, human- or agent-written.

## 1. Hexagonal architecture (ports & adapters)

```
            driving side                                   driven side
   ┌──────────────────────┐                        ┌──────────────────────────┐
   │ adapters/driving/tui │──┐                  ┌──│ adapters/driven/k8s      │
   │ adapters/driving/cli │  │   ┌──────────┐   │  │ adapters/driven/kustomize│
   └──────────────────────┘  └──▶│   core   │◀──┘  │ adapters/driven/sops     │
                                 │ app      │      │ adapters/driven/logformat│
                                 │ ports    │      │ adapters/driven/demo     │
                                 │ domain   │      │ adapters/driven/…        │
                                 └──────────┘      └──────────────────────────┘
                     wired together only in internal/bootstrap
```

| Layer | Package(s) | Contains | May import |
|---|---|---|---|
| Domain | `internal/core/domain` | Pure types and rules: Env, Repo, Workload, Pod, Container, Status aggregation, LogEntry, Level, TimeWindow, Filter, Fingerprint, redaction, filter engine | stdlib only |
| Ports | `internal/core/ports` | Interfaces only. **Driven**: `ClusterClient`, `LogSource`, `LogDecoder`, `LogRenderer`, `ManifestScanner`, `RepoResolver`, `SecretsProvider`, `Clock`, `Clipboard`, `Opener`. **Driving**: `ServiceCatalog`, `LogSession`, `Diagnostics` (use cases the UI calls) | domain |
| Application | `internal/core/app` | Use-case implementations: build the service list, open a log session, merge streams, apply filters, group errors. Orchestrates ports, no I/O of its own | domain, ports |
| Driven adapters | `internal/adapters/driven/<name>` | One technology each: client-go, sops CLI, Kustomize scanner, JSON log decoder, Spring Boot renderer, demo cluster, file system, OS clipboard | domain, ports, third-party libs |
| Driving adapters | `internal/adapters/driving/tui`, `…/cli` | Bubble Tea UI, Cobra CLI. Talk to the core **only through driving ports** | domain, ports |
| Bootstrap | `internal/bootstrap` | Composition root: reads config, picks adapters by name from registries, wires everything | everything |
| Config | `internal/config` | Load/validate YAML into plain structs | stdlib, domain, yaml |
| Support | `internal/diag`, `internal/buildinfo` | Diagnostic log file, version stamp | stdlib only |

## 2. Hard rules

1. **Dependencies point inward.** `core/*` never imports `adapters/*`, `bootstrap`, `config`, client-go, Bubble Tea, Lip Gloss, Cobra or any I/O library.
2. **Adapters never import each other.** The TUI does not know client-go exists; the k8s adapter does not know the TUI exists.
3. **Only `internal/bootstrap` wires concrete types.** No `New…Adapter()` calls anywhere else, no package-level singletons, no `init()` side effects; registries are built explicitly by bootstrap.
4. **Everything enterprise-specific is an adapter or config, never an `if` in the core.** No customer/team names, field names, namespace names, label keys or path conventions in `core/`.
5. **Ports are small and owned by the core.** An interface lives in `core/ports` and is shaped by what the core needs, not by what a library exposes. Adapters translate library types into domain types at the boundary; no `corev1.Pod` leaks past an adapter.
6. **Adapters are selected by name from config** through a registry (`ports.Registry[T]`): e.g. `log_format.decoder: json-fields`, `manifests.scanner: kustomize`, `secrets.provider: sops`, `cluster.client: kubernetes | demo`. Adding an implementation = new package + `Register("name", factory)` + config entry.
7. **Every port has a fake** in `internal/core/ports/portstest` and every use case is tested against fakes. Every adapter has contract tests (shared test suite run against each implementation of the same port).
8. **Time, randomness, environment and filesystem are injected** (`Clock`, `fs.FS`, env lookup) so behavior is deterministic in tests and in `--demo`.
9. **Config is data, not behavior.** Config structs are plain and validated in one place; adapters receive only their own sub-section.
10. **Secrets never cross the core as strings meant for display.** `SecretsProvider` returns values for wiring only; the domain `Redactor` runs before anything is rendered or exported.

These rules are enforced mechanically: `golangci-lint` `depguard` rules give editor feedback for the core, and the architecture test `internal/archtest` (its `Rules` table is the executable form of this document) fails the build if any package imports outside its allowed set or is not covered by a rule.

## 3. Extension points (how to adapt Huginn to a new enterprise)

| You need to support… | Do this | Code change? |
|---|---|---|
| Another JSON log shape | Add a `log_formats` profile mapping JSON paths → canonical fields | No |
| A non-JSON or exotic log format | New `LogDecoder` adapter + register it | One package |
| A different console look (not Spring Boot) | New `LogRenderer` template in config, or new renderer adapter | Usually no |
| Another manifest layout (paths, overlay names) | Edit `manifests` globs / field locations | No |
| Helm charts, raw YAML, Jsonnet… | New `ManifestScanner` adapter | One package |
| Repo ↔ workload mapping by label/annotation | Edit `resolver.label_keys` | No |
| One cluster, many clusters, namespace per env/per repo | Edit `environments` (context + namespaces per env) | No |
| Secrets in Vault / GSM / another file | New `SecretsProvider` adapter | One package |
| Another log backend (Cloud Logging, Loki, Elastic) | New `LogSource` adapter | One package |
| Other sidecars to hide | Edit `containers.denylist` / `allowlist` | No |
| Different keys or theme | Edit `ui.keymap` / `ui.theme` | No |

## 4. Coding conventions

- Small functions, idiomatic Go, every exported symbol documented.
- Errors are wrapped with context (`fmt.Errorf("list pods in %s: %w", ns, err)`) and classified at adapter boundaries into domain error kinds (`ErrUnauthorized`, `ErrForbidden`, `ErrUnreachable`, …) so the UI never inspects library errors.
- No global state; constructors take their dependencies explicitly.
- Table-driven tests; golden files for rendering; benchmarks for hot paths (buffer, filter).
- When two designs compete, record the trade-off in `docs/DECISIONS.md`.

## 5. Package map

```
cmd/huginn/                     main: calls bootstrap.Main
internal/
  core/domain/                  Env, Repo, Workload, Pod, Container, Level, LogEntry,
                                TimeWindow (+ presets, window selection), Secret, ServiceStatus
  core/ports/                   ClusterClient, LogSource, LogDecoder, LogRenderer,
                                ManifestScanner, RepoResolver, SecretsProvider, Clock,
                                driving ports, Registry[F]
  core/ports/portstest/         fakes + RunClusterContract / RunLogSourceContract
  core/app/                     use cases: Catalog (ServiceCatalog), repo resolvers
  adapters/driven/demo/         synthetic cluster + log generator (--demo)
  adapters/driven/kubernetes/   client-go adapter (M4; placeholder until then)
  adapters/driven/clock/        system clock
  adapters/driving/cli/         cobra command tree -> cli.Options
  adapters/driving/tui/         Bubble Tea app: theme, keymap, screens
  bootstrap/                    composition root: config -> registries -> adapters -> tui
  config/                       YAML schema, defaults, validation, example, JSON schema
  diag/                         file-only diagnostic log with redaction
  buildinfo/                    version stamping
  archtest/                     layer rules as a test
```

**Adding an adapter** (for example a Loki `LogSource`): create `internal/adapters/driven/loki`, implement the port, run the port's contract suite from `portstest` in its tests, register it in the matching registry in `internal/bootstrap`, add its name to the `enum` tag of the config field that selects it, and regenerate the schema (`make schema`).
