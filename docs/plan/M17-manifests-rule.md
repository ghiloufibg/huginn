# M17 — The `manifests` rule of `services.yaml`: a workload belongs to the repository whose overlay declares it  (status: design, awaiting owner decisions §10)

**Goal:** implement the third rule of `services.yaml` `resolve`, accepted and validated since M3.4 but skipped at run time (`bootstrap/catalog.go`: "manifest resolver not available yet"). With it, a workload of the cluster is attached to the repository **whose Kustomize overlay for that environment declares it**, read from the clones under `repos_root`. This needs no label discipline in the cluster, and it makes the owner's assumption true by construction: a repository is listed only if its clone is on disk, so the COMMIT line of M16 always has a clone to read (`docs/plan/M16-deployed-commit.md` §2).

This is a design only. Nothing below is implemented. Facts come from the code (cited) or the Kustomize documentation; anything assumed is marked **to verify**.

## 1. What exists, and what is missing

| Fact | Where | Consequence |
|---|---|---|
| `services.yaml` already has `resolve: [… manifests]`, `manifests.overlay_glob` (with `{env}`) and `manifests.files`; validation requires `overlay_glob` and `repos_root` with the rule | `config/config.go` `Manifests`, `config/validate.go` | No config change for the basic rule; documented in `docs/CONFIG.md` §5 |
| The rule is skipped with a debug log | `bootstrap/catalog.go` `resolverChain` | The only wiring to add |
| The driven port exists: `ManifestScanner.Scan(ctx, repoDir, env) ([]DiscoveredWorkload, error)`, with `DiscoveredWorkload{Ref, Labels}`; no adapter implements it | `core/ports/manifests.go` | A new adapter, no new port |
| Rules run in order in `app.ChainResolver`; the first that names a repository wins; `MappingResolver` matches on environment and name, and on namespace and kind when set | `core/app/resolver.go` | A `ManifestResolver` slots in like the two others |
| The resolver runs **on every catalog snapshot**, and an error fails the whole snapshot | `core/app/catalog.go:312` | The scan must be cached, and a bad file must never be an error: it degrades to a warning |
| A snapshot carries `Warnings`, shown in the status bar | `ports.CatalogSnapshot` | Where unreadable files are reported |
| Files are read through `ports.LocalFiles` (bounded reads, a glob with `**` that skips hidden folders and is bounded to 100 000 entries) | `adapters/driven/localfiles` | Reused for the glob of overlays |
| D-017 planned a Kustomize scanner first, Helm or raw YAML later "without touching callers" | `docs/DECISIONS.md` D-017 | The port is per technology; this is the Kustomize one |

## 2. What the rule means

For environment `env`, a workload `(namespace, kind, name)` of the cluster belongs to repository `R` when `R`'s clone (`repos_root/R`) has an overlay folder matching `overlay_glob` with `{env}` replaced, and **building that overlay** would produce an object of that kind and name, in that namespace when the build sets one.

Only the **identity** of the objects is needed (kind, name, namespace), not their content. So Huginn does not run a full Kustomize build; it reads what decides identity (§4).

## 3. Design by layer

### 3.1 App: `ManifestResolver` (`core/app/resolver.go`)

```go
// ManifestResolver attributes a workload to the repository whose overlay
// for the environment declares it (services.yaml manifests rule).
type ManifestResolver struct {
    Scanner   ports.ManifestScanner
    Files     ports.LocalFiles  // lists the repositories under ReposRoot
    ReposRoot string
    Clock     ports.Clock
    TTL       time.Duration     // how long an environment's index is reused
    Log       *slog.Logger
}

func (r *ManifestResolver) Resolve(ctx context.Context, env domain.Env, ws []domain.Workload) ([]domain.Repo, []domain.Workload, error)
func (r *ManifestResolver) Warnings(env domain.Env) []string   // unreadable files, conflicts
```

- **Index per environment:** `(kind, name) → [(namespace or "", repo)]`, built by scanning every repository folder under `repos_root` (one level, hidden folders skipped). A cluster workload matches an entry of its kind and name whose namespace is empty or equal to its own.
- **Cache:** the index is reused for `TTL` (default 1 minute, §10.4) and rebuilt in the background when it expires (the snapshot keeps using the old index meanwhile: **stale-while-revalidate**), so a snapshot never waits on the disk after the first build. The services screen's resync key (`r`) also rebuilds it. **To measure:** the first build for the owner's `repos_root` (§8).
- **Never an error:** an unreadable file, a YAML error or an unsupported feature (§4.3) skips that file and is recorded as a warning; `Resolve` returns an error only when its context is cancelled.
- **Conflicts:** two repositories declaring the same `(namespace, kind, name)`: the workload is **not** attributed by this rule (the next rule may), with a warning naming both repositories. Silently picking one would hide a real misconfiguration (§10.3).
- **Warnings reach the status bar** through an optional interface the catalog checks (`interface{ Warnings(domain.Env) []string }`), so `RepoResolver` is unchanged for the other rules.

### 3.2 Driven adapter: `adapters/driven/kustomize` (new)

Implements `ports.ManifestScanner` for Kustomize, configured by bootstrap with `overlay_glob` and `files`:

1. Overlay folders: `Glob(repoDir/<overlay_glob with {env}>/<file>)` for each entry file name (`files`, default `[kustomization.yaml, kustomization.yml, Kustomization]`).
2. For each overlay, read the kustomization and follow it (§4), collecting the workload objects.
3. Return them as `DiscoveredWorkload{Ref: {Env, Namespace, Kind, Name}, Labels}`.

YAML is parsed with `go.yaml.in/yaml/v3` (already a dependency), multi-document files included. Files are read through `LocalFiles`-style bounded reads (a size limit per file, 4 MiB like `localfiles`). No network, no process, no plugin: **remote resources are never fetched** (§4.3).

### 3.3 Wiring

`bootstrap/catalog.go` `resolverChain`: `case "manifests":` builds the `ManifestResolver` with the `kustomize` scanner (the demo uses the same adapter on a fixture folder, §7). `archtest`: the new package is covered by the existing `adapters/driven/` rule and added to the packages that must hold no application data.

## 4. What is read in a kustomization

### 4.1 Followed

| Field | Effect on identity |
|---|---|
| `resources:` (files and folders) | Files: their objects. Folders: the kustomization inside, recursively (a base, a component) |
| `components:` | Read like a folder resource (they can add resources) |
| `namespace:` | Sets the namespace of every object below it; the outermost one wins, as in Kustomize |
| `namePrefix:` / `nameSuffix:` | Applied to every object name below, prefixes accumulating from the base outwards, as in Kustomize |
| `apiVersion`/`kind` of the objects | Only `Deployment`, `StatefulSet`, `DaemonSet`, `CronJob` (apps/v1, batch/v1) are kept: the kinds Huginn watches |
| `metadata.name`, `metadata.namespace`, `metadata.labels` of the objects | The identity, and the labels for `DiscoveredWorkload` |

Paths are relative to the kustomization's folder; a path leaving `repos_root/R` (`../../other-repo`) is refused with a warning, so one repository cannot claim another's objects. Cycles and depth are bounded (depth 16, each folder read once per overlay).

### 4.2 Ignored, because it does not change a workload's identity

`patches`, `patchesStrategicMerge`, `patchesJson6902` (except a patch that renames, §4.3), `images`, `replicas`, `labels`/`commonLabels`, `commonAnnotations`, `configMapGenerator`, `secretGenerator` (their hash suffixes apply to ConfigMaps and Secrets only), `generatorOptions`, `vars`, `replacements` (except on `metadata.name`, §4.3), `openapi`, `crds`.

### 4.3 Not supported: reported as a warning, never guessed

| Feature | What happens |
|---|---|
| Remote resources (`https://…`, `github.com/…?ref=`) | Skipped with a warning: fetching would need the network and credentials, and Huginn reads only the disk |
| `helmCharts:` / `helmGlobals:` | Skipped with a warning: a Helm scanner is a separate adapter (D-017) |
| A patch or a replacement that changes `metadata.name` or `metadata.namespace` | **To verify** whether the owner's overlays do this; if they do, the case is handled (a JSON6902 `replace` on `/metadata/name` is simple to apply), else it stays a warning |
| `transformers:` / `generators:` (plugins) | Skipped with a warning: plugins run code |

The design choice behind this table is in §9 (why not `kustomize build`).

## 5. Configuration

No new key is needed for the basic rule:

```yaml
# services.yaml
resolve: [explicit, manifests, labels]      # manifests before labels: the disk is the source of truth
manifests:
  overlay_glob: "deploy/overlays/{env}"     # a fixed path is much cheaper than "**/overlays/{env}" (§8)
  files: [kustomization.yaml]               # optional
```

Possible additions, each an owner decision (§10):

- `manifests.overlay_names: {prd: production}`: when a repository's overlay folder is not named like the Huginn environment.
- `manifests.ttl: 1m`: how long an index is reused.
- `manifests.service_from: <glob part>`: in a monorepo, name the row after the service folder instead of the repository (§6, §10.5).

`docs/CONFIG.md` §5 loses the "not read yet" sentence and gains §4's lists; the example folders get a commented `manifests` rule; `docs/prompts/copilot-config-folder.md` drops its rule "do not use manifests" and learns to choose `overlay_glob` from the repositories.

## 6. Monorepos

A monorepo with one overlay per service (`services/payment/overlays/prd`, `services/ledger/overlays/prd`) declares all its services' workloads, so they all join **one row**: the monorepo, with several workloads. That is consistent with M16 (one GitLab project, one clone, the COMMIT line per workload with `paths`). If the owner prefers **one row per service**, the row must carry both a display name (the service folder) and the repository it lives in, because `repos_root/<row name>` would no longer be the clone; that touches `domain.Repo`, the catalog, M16's `Releases` and Kafka's `{repo_dir}`. It is a separate decision (§10.5), not needed for the rule itself.

## 7. Demo

The demo has no clones. A small Kustomize fixture under `examples/demo-repos/` (two repositories with `base/` and `overlays/{rec,prd}`, a `namePrefix`, a component, a conflict, an unreadable file) and a demo config pointing `repos_root` at it, used by tests; `--demo` itself keeps `resolve: [labels]` so its screen does not change.

## 8. Non-functional

| Aspect | Design |
|---|---|
| Disk reads | Only the overlay glob, then the files the kustomizations name; nothing else in a repository is opened |
| Glob cost | A fixed `overlay_glob` (`deploy/overlays/{env}`) costs one stat per repository; a `**` glob walks each repository (hidden folders skipped, bounded to 100 000 entries by `LocalFiles`): **to measure** on the owner's `repos_root`, and the docs recommend a fixed path |
| Frequency | One index build per environment per `TTL`, in the background after the first; the catalog's snapshots read the cached index (a map lookup per workload) |
| Memory | One entry per declared workload |
| Failure | A bad file is a warning in the status bar and the rule skips it; the next rule in `resolve` still applies |
| Permissions | None on the cluster; read only on the disk |
| Security | No network, no process, no plugin; paths confined to the repository |

## 9. Rejected alternatives

| Rejected | Why |
|---|---|
| Run `kustomize build` / `kubectl kustomize` per overlay (a CLI adapter, like `sops`) | Exact, but one process per repository and environment (100 repositories × 3 environments = 300 builds per refresh, each 100 ms or more: **to measure**), needs the tool installed, may fetch remote bases over the network, and may run plugins. Kept as a possible second scanner (`manifests.engine: build`) if §4's subset proves too small (§10.1) |
| The Kustomize Go library (`sigs.k8s.io/kustomize/api`, krusty) | Exact, no process, but a large dependency tree for identity only, and the same remote-base and plugin questions |
| Match on the overlay folder name only (repository = owner of any workload named like it) | Wrong as soon as a repository deploys a worker with another name, or a prefix is added |
| Read every YAML file of the repository | Slow on large repositories, and would attribute objects that no overlay of the environment deploys |

## 10. Decisions needed from the owner

1. **Engine:** the built-in identity reader of §4 (recommended: fast, no tool, no network), or `kubectl kustomize` per overlay (exact, slower, needs the tool)?
2. **Order** in the example and the docs: `[explicit, manifests, labels]` (recommended: the disk decides; labels catch workloads with no overlay), or labels first?
3. **Conflicts** (two repositories declare the same workload): not attributed, with a warning (recommended), or the first repository by name wins?
4. **Refresh:** index reused 1 minute and rebuilt in the background, plus `r` to force (recommended), or read once at start only?
5. **Monorepos:** one row per repository (recommended now, consistent with M16), or one row per service folder (a larger change, §6)?
6. **Overlay names:** are the overlay folders named exactly like the Huginn environments, or is `overlay_names` needed?
7. **Your overlays:** do any patches or replacements rename workloads or change their namespace (§4.3)? Do any use remote bases or `helmCharts`?
8. **Numbering:** M17, decision D-075.

## 11. Test plan

- **Adapter** (table tests on fixture folders): plain resources; nested bases; `namespace` override (outermost wins); `namePrefix`/`nameSuffix` accumulation; components; multi-document files; the four kinds kept, others ignored; remote resource, `helmCharts`, plugin, path leaving the repository, cycle, YAML error: each a warning, never an error; an optional oracle test comparing names and kinds with `kubectl kustomize` output when the tool is installed.
- **App**: attribution by kind and name with and without a namespace; conflict; stale-while-revalidate (old index used, new one swapped in); `r` forces a rebuild; warnings exposed; chain order with `explicit` and `labels`.
- **Bootstrap**: the rule wired; the "not available yet" log gone.
- **Real check**: on the owner's `repos_root` and cluster, every workload attributed as with `labels` today (a diff of the two runs' rows), the first index build timed, `**` versus a fixed `overlay_glob` timed.

## 12. Done when (this design)

This file exists, its claims are cited or marked to verify, and §10 is answered. Implementation is a separate step.
