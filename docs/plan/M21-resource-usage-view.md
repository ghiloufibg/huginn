# M21 — Resource usage view: CPU and memory per namespace and per service

Status: **implemented as M21.1 to M21.5 on `feat/m21-resource-usage`; not yet run against a real cluster** ([M21-gke-qa.md](M21-gke-qa.md)). Where the code departs from this design, [D-076](../DECISIONS.md) says so (in-place tree instead of screen levels, `SharedCatalog`, `usage.enabled` instead of a `metrics:` selector, no `sparkline` switch, no share on a namespace with uncatalogued pods). The sections below are the design as written on 2026-11-15 on the owner's request: take inspiration from the open-source Kubernetes TUIs and add a view that shows resource utilisation per namespace and per service (Deployment) of the services Huginn lists. Nothing here is decided until the owner answers section 11.

## 1. Goal and non-goals

**Goal.** From the services screen, one key opens a screen that answers, for the current environment: *which namespace and which service uses how much CPU and memory, how close is it to its requests and limits, and is it getting worse?* The rows are the ones the services screen already lists (same repository → workload mapping, same namespaces, same environment), so the user never has to map `kubectl top` output back to services by hand.

**Non-goals (v1).**

| Not in v1 | Why |
|---|---|
| Node or cluster-wide capacity views | Needs cluster-scoped `nodes` read; Huginn watches namespaced objects only (D-004, D-034) because narrow roles are the norm. |
| Disk, network, GPU, ephemeral storage | The Metrics API serves CPU and memory only. |
| Cost in money | Needs a price source; not a log/state concern. |
| Right-sizing recommendations (VPA, Goldilocks-style) | Needs days of history. v1 shows facts and a plain over/under-provisioned marker, no advice engine. |
| Persisting history, alerts | Read-only prototype; history lives in memory for the session only. |
| Any write, `exec`, `port-forward`, `kubectl top` shelling out | Read-only by construction (D-034). |

## 2. Prior art and what is taken from it

Recalled from memory of these projects, **not re-checked against their current versions today**; none of it is copied, only the ideas.

| Tool | Idea worth taking | Rejected |
|---|---|---|
| `kubectl top` | The data source: `metrics.k8s.io` (metrics-server). Usage, nothing else. | No requests/limits, no grouping. |
| k9s | Pods/nodes tables with usage next to **%/request and %/limit** columns; column sort; colour by threshold; "pulse" overview. | Cluster-wide and node views; free-form resource commands. |
| ktop | A dashboard feel: gauges and **sparklines** over a short in-memory history; namespace filter. | Node-first layout. |
| kube-capacity / view-utilization plugins | **Roll-ups**: sum of usage, requests and limits per namespace and per workload, in one table. | Cluster-scoped node totals. |
| Lens, Headlamp | Drill from a total into its pods and containers. | GUI-only features. |
| Goldilocks (VPA) | The *question* (over or under provisioned?). | The recommender. |

The combination Huginn adds is the join with its own service catalog: rows are *services* (repositories), not just workloads.

## 3. Platform facts that constrain the design

| Fact | Consequence |
|---|---|
| Usage comes from the **Metrics API** (`metrics.k8s.io/v1beta1`, `PodMetrics`): per container CPU (rate) and memory (working set), a `timestamp` and a `window`. | One new data source; Huginn has none today. Values are averages over a window of tens of seconds, not instants: a spike between two scrapes is invisible, and an OOM kill can happen at a peak this view never shows. The screen must say so. |
| The Metrics API supports `get` and `list` per namespace, **no `watch`**. | The adapter polls; nothing like the informers of D-004. |
| Requires metrics-server (present by default on GKE; absent or unready elsewhere) and an RBAC grant on `pods.metrics.k8s.io`. | Three distinct failures to explain: not served (404), forbidden (403), not ready (503). The view degrades per namespace, like `NamespaceErrs` does for the services screen. |
| `list` is a read verb. | Passes the D-034 verb test unchanged; the test is extended to cover the new group, not loosened. |
| Requests and limits are already read: `domain.Container.Resources` holds them, but as **strings** (`"500m"`, `"1Gi"`) for display. | Ratios need numbers. Add numeric fields next to the strings (section 4.1). |
| Pods of a repository are known to `app.Catalog` (`ServiceSummary.Pods`, namespace on each pod). | The service/namespace join reuses the catalog; no second mapping logic. |
| A repository may run in several namespaces (D-030 allows namespace per environment or per repo). | A service can have partial rows under several namespaces (section 6.3). |

## 4. Architecture

Follows `docs/ARCHITECTURE.md` strictly. Nothing application-specific enters code.

```
 tui usageScreen ──▶ ports.ResourceUsage (driving) ──▶ app.Usage ──┬──▶ ports.ServiceCatalog  (existing, pod → service)
                                                                    ├──▶ ports.MetricsSource   (new, driven)
                                                                    └──▶ ports.Clock
 adapters/driven/kubernetes  implements MetricsSource (metrics.k8s.io, same clientset cache)
 adapters/driven/demo        implements MetricsSource (deterministic, scripted)
```

### 4.1 Domain (`internal/core/domain`, stdlib only)

```go
// Quantities are integers: CPU in millicores, memory in bytes.
type Amount struct{ CPUMilli, MemBytes int64 }

// Resources gains numeric twins of its display strings, filled by the adapter.
// (-1 = not set; 0 is a real value for a request.)
type ResourceAmounts struct{ Request, Limit Amount }   // on Container

type ContainerUsage struct{ Name string; Use Amount }
type PodUsage struct {
    Namespace, Name string
    SampledAt       time.Time      // metric's own timestamp
    Window          time.Duration
    Containers      []ContainerUsage
}

// Rollup is the sum over a set of pods; it is the unit of display.
type Rollup struct {
    Pods, Reporting int            // pods counted / pods with a metric
    Use, Request, Limit Amount
    LimitComplete   bool           // every counted container has a limit (else %limit is undefined)
    HottestPod      string         // pod with the highest use/limit (memory), the one that OOMs
    HottestMemPct   float64
}
```

Pure functions, table-tested: `RollupPods(pods []Pod, usage map[PodKey]PodUsage, f ContainerFilter) Rollup`, `Pct(use, ref int64) (float64, bool)`, `Severity(pct, thresholds)`, `Provisioning(r Rollup) Over|Under|Ok|Unknown`.

Rules baked into the rollup (each is a place where a naive sum lies):

- **Not reporting ≠ zero.** A pod younger than the metrics window, or one that is not Running, has no sample. It is excluded from sums and from `Reporting`; the UI shows `3/4 pods`.
- **Requests and limits are summed over application and sidecar containers that run**, init containers are left out (they do not run alongside); the effective-request subtlety of init containers is a documented approximation.
- **`%limit` is defined only when `LimitComplete`.** One container without a limit makes the ratio of sums meaningless (it would hide an unbounded consumer): show `no limit` instead of a number.
- **A ratio of sums hides the hot pod.** Hence `HottestPod` and its percentage: five pods at 40 % and one at 98 % of the memory limit must not read as 52 %.
- **Containers filter reuses `domain.ContainerFilter`** and the roles of `containers.yaml`, toggled with the existing `A` (application containers only / all). Sidecars are never named in code.

### 4.2 Ports (`internal/core/ports`)

```go
// Driven. Read-only. One call returns the current samples of one scope.
type MetricsSource interface {
    PodUsage(ctx context.Context, scope Scope) ([]domain.PodUsage, error)
}

// Driving: what the usage screen consumes, same shape as ServiceCatalog.
type UsageSnapshot struct {
    Env        domain.Env
    Namespaces []domain.NamespaceUsage     // each: Rollup + per-service Rollups + series
    SampledAt  time.Time                   // oldest metric timestamp used
    Window     time.Duration
    Synced     bool
    NamespaceErrs map[string]error         // forbidden, unavailable, unreachable, per namespace
    Err        error                       // metrics cannot be read at all (not served)
    StaleSince time.Time
}
type ResourceUsage interface {
    Watch(ctx context.Context, env domain.Env) (<-chan UsageSnapshot, error)
}
```

`MetricsSource` is **separate from `ClusterClient`** on purpose: metrics are optional (a cluster may have none), so the screen can say "no metrics here" without the services screen being affected, and a second backend (Prometheus, Cloud Monitoring) is "new adapter + `Register`", exactly the extension-point table of the architecture document. New error kind `domain.ErrMetricsUnavailable` (wrapping the 404/503 causes) next to `ErrForbidden`; the adapter classifies, the UI never inspects client errors. Fake and a contract suite (`RunMetricsSourceContract`) go to `portstest` (rule 7).

### 4.3 Application (`internal/core/app/usage.go`)

`app.Usage` implements `ResourceUsage`:

1. Subscribes to `ServiceCatalog.Watch` (already implemented in the same package; no second pod watch) to know *pod → service → namespace*.
2. Runs one poller per namespace of the scope, started only while a `Watch` is open, stopped on context cancel. Interval `usage.refresh` (default 15 s, never below 5 s), jittered, bounded concurrency (4), backs off on 429/503. Pods of the catalog without a metric are "not reporting".
3. On each metrics tick or catalog change, recomputes rollups and emits a snapshot, coalesced to at most two per second; a slow reader skips intermediate snapshots (same contract as the catalog).
4. Keeps a **ring buffer of rollup samples** (`usage.history`, default 15 min, so ~60 points at 15 s) per namespace and per service for sparklines. Memory bound: `(namespaces + services) × points × 2 × 8 B`, a few hundred KB for hundreds of services. Per-pod history is not kept; the pod level shows the current value only. The buffer belongs to the `Watch` session: no global state (rule 2.3), no persistence.
5. Time comes from the injected `Clock` (rule 8), so tests and `--demo` are deterministic.

Cost on the API server: one `list` per namespace per interval, only while the screen is open. 20 namespaces at 15 s is about 1.3 requests/s. State this in `docs/CONFIG.md`; the default is conservative and `usage.refresh` can raise it.

### 4.4 Adapter `kubernetes` (no new package)

`metrics.go` in `internal/adapters/driven/kubernetes`, sharing the per-context clientset cache:

- Recommended: typed client `k8s.io/metrics` (`MetricsV1beta1().PodMetricses(ns).List`), added to the adapter's `API` interface so client-go's unused groups stay out of the binary. It is a new module dependency (same release train as client-go, v0.37.x expected; **to verify it resolves with go 1.26 and passes `govulncheck`** before accepting). Alternative with no new module: `RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/namespaces/<ns>/pods")` with 10 lines of local structs; decided in section 11.
- Converts `resource.Quantity` to `Amount` at the boundary (`MilliValue()`, `Value()`); the same conversion fills `ResourceAmounts` in `convert.go`'s `resources()`, so strings and numbers come from one source.
- Maps 404 to `ErrMetricsUnavailable` ("metrics.k8s.io is not served"), 503 to `ErrMetricsUnavailable` ("metrics-server not ready"), 403 to `ErrForbidden`, as the existing adapter does for workloads.
- Verb test extended: the fake clientset must see only `list` on `pods` in `metrics.k8s.io`.
- **RBAC to document** (`docs/CONFIG.md`, and `deploy/lab` read-only identity): `get, list` on `pods.metrics.k8s.io`, namespaced.

### 4.5 Adapter `demo`

Deterministic generator from the clock and the existing world: one service close to its memory limit (the OOM scenario already in `world.go`), one badly over-provisioned, one without limits, one pod not reporting yet, one namespace answering forbidden. This is also what the golden tests render.

### 4.6 Config (`ui.yaml`, additive, validated in one place, `make schema`, `docs/CONFIG.md`)

```yaml
usage:
  refresh: 15s          # poll interval, min 5s
  history: 15m          # sparkline memory, 0 disables history
  thresholds:           # % of limit (or of request when there is no limit)
    warn: 80
    crit: 95
  sparkline: true
keymap:
  usage: [u]            # new action; u is free on the services screen
```

Plus adapter selection by name like the other registries: `metrics: kubernetes | demo | none` (where cluster adapters are selected today; `none` hides the key). Thresholds are generic numbers, not application knowledge; no namespace, label or container name appears anywhere.

## 6. Screen design

### 6.1 Entry and navigation

`u` on the services screen pushes `usageScreen` (same stack mechanism as logs and Kafka). If the cursor is on a service, that service is selected and its namespace expanded on arrival. `esc` goes back; `ctrl+e` switches environment as everywhere.

| Key (action) | Effect |
|---|---|
| `j`/`k`, `g`/`G`, `pgup`/`pgdown` | move |
| `space` or `left`/`right` | collapse / expand a namespace |
| `enter` | drill down: service → workloads and pods → containers (breadcrumb `services › usage › app-prd › payment-service`) |
| `s` (sort) | cycle: CPU use, memory use, %limit, %request, name; the sort applies inside each namespace and orders namespaces by the same total |
| `/` | filter by name (same input as the services screen) |
| `A` | application containers only / all (existing action, same meaning) |
| `p` | preview panel: per container usage, request, limit, last termination (reuses the `Explain` text, so "97 % of the memory limit, OOMKilled 38 min ago" appears next to the numbers) |
| `r` | poll now |
| `L` on a service | open its logs (reuses the open action) |

### 6.2 Layout (mock, 110 columns)

```
huginn PRD gke_…main / app-prd                      services › usage                  sampled 12s ago · 60s window
NAMESPACE / SERVICE          PODS    CPU use      %REQ   %LIM   MEM use    %REQ   %LIM   PEAK POD     TREND(15m)  NOTE
▾ app-prd                    31/31   14.2 cores   71%    48%    38.1 Gi    92%    77%                 ▂▃▃▅▅▆▆▆▇    
  ▌ payment-service           4/4     1.9 cores   95%    62%     7.2 Gi    99%    96%    pay-7c9f-x2   ▅▅▆▆▇▇▇██   MEM 96% of limit
    order-orchestrator        6/6     2.4 cores   80%    40%     9.6 Gi    81%    58%    ord-5b8d-q1   ▃▃▃▄▄▄▄▄▄   
    pricing-engine            3/3     0.2 cores   14%     7%     2.1 Gi    35%    17%    pri-6f4a-c7   ▁▁▁▁▂▁▁▁▁   over-provisioned
    ledger-writer             2/3     0.4 cores   —       —      1.0 Gi     —     no limit led-77d1-a0 ▂▂▃▃▃▄▄▄▅   1 pod not reporting · no limit
    (not in the catalog)      3/3     0.1 cores                  0.4 Gi                                      other pods of the namespace
▸ app-rec                     9/9     1.1 cores   40%    22%     5.0 Gi    61%    44%                 ▂▂▃▃▃▃▃▃▃
▸ batch-prd                   no access: pods.metrics.k8s.io (forbidden)

 Usage is the metrics-server average over its window: peaks between samples are not shown.   enter drill  p preview  s sort  A containers
```

- Namespace rows are bold and carry the sum of everything in the namespace, including an explicit **`(not in the catalog)`** row (pods the services screen does not list, e.g. finished jobs, filtered pods), so the services add up to the namespace and nothing is silently dropped.
- A bar or numbers: wide terminals draw a thin gauge under `%LIM`; the numbers are always printed (colour is never the only carrier; the `accessible` and `none` themes keep working, and `internal/…/theme_contrast_test.go` gets the new styles).
- `NOTE` always uses words: `MEM 96% of limit`, `over-provisioned`, `no limit`, `1 pod not reporting`, `CPU throttled risk` is *not* claimed (throttling is not in the Metrics API).
- Narrowing order when the terminal is small: TREND, then gauges, then `%REQ`, then `PEAK POD`, then `NOTE` last; never the name or the two numbers CPU use and MEM use. Reuses the narrowing mechanics of `columns.go`/`table.go`.
- Over-provisioned means usage below a low fraction of request on both resources, under-provisioned means above the `crit` threshold of the limit; both come from `Provisioning()`, are shown as a word, and carry no recommendation.

### 6.3 Services that span namespaces

A service with pods in several namespaces appears under each of them with that namespace's share and a trailing `(part)`. A flat "all namespaces" lens (`tab`) sums them per service; open point 5 asks whether v1 needs it.

### 6.4 Failure states

| Situation | What the screen shows |
|---|---|
| `metrics.k8s.io` not served | Whole-screen message naming the cause and the check (`kubectl get apiservice v1beta1.metrics.k8s.io`), services screen unaffected |
| 403 on one namespace | That namespace row: `no access: pods.metrics.k8s.io`; others fine |
| Metrics older than 2 windows | Values kept, dimmed, header `sampled 3m ago`, namespace marked stale like `StaleSince` on the services screen |
| Pod without a sample | Counted in `PODS` as `n/m`, excluded from sums |
| No limit / no request | `—` or `no limit`, never `0 %` |
| Terminal too small | Falls back to the first two columns, as the services screen does |

## 7. Security

- Read-only: one new `list` on `metrics.k8s.io`, covered by the extended verb test.
- No secret, token or log content is involved; pod and namespace names already appear on screen. The diagnostic log (`internal/diag`) records poll errors with the existing redaction.
- Requests are namespaced; no cluster-scoped permission is added or asked for.

## 8. Test strategy

| Layer | Tests |
|---|---|
| domain | Table tests of `RollupPods` for every rule in 4.1 (not reporting, missing limit, init containers, hottest pod, zero request), `Pct`, `Severity`, `Provisioning`; fuzz on quantity conversion |
| ports | `RunMetricsSourceContract` run against the fake, the demo and the Kubernetes adapter on a fake clientset |
| app | `Usage` against fakes and a fake clock: join with catalog, coalescing, backoff on 429, per-namespace errors, ring buffer bound, no goroutine left after context cancel |
| adapter | Quantity conversion, 404/403/503 classification, verb whitelist |
| tui | Golden files (`-update`, reviewed) for 80, 110 and 160 columns, collapsed and expanded, stale, forbidden namespace, no metrics-server, `accessible` theme; key handling tests like `tui_test.go`; benchmark of the view for 500 services |
| archtest | No new package. Rule check that `usage*` files in `core` import nothing outside domain and ports; "no application data" test already covers the demo strings |
| real GKE | A QA plan like `M15-gke-qa.md`: compare the view with `kubectl top pods -n <ns>` for the same instant (tolerance: metrics window), a restricted role without the metrics grant, a cluster without metrics-server |

## 9. Risks and open technical points

| Risk | Handling |
|---|---|
| The numbers are window averages and may be read as exact | Footnote always visible, `window` shown in the header; no `%` presented beyond whole percent |
| API server load from polling | Only while open, per-namespace `list`, jitter, backoff, min 5 s; documented |
| Ratio of sums misleads | `PEAK POD` column and `LimitComplete` rule |
| Sidecars distort a service's usage | Same container roles and `A` toggle as the logs screen |
| New dependency `k8s.io/metrics` | Verify module resolution, size and `govulncheck`; fallback is a raw REST call with local structs |
| Services screen row key (`repo`) vs several namespaces | 6.3 |
| Metrics-server resolution on some clusters (60 s) | Header shows the real window from the metric, so a slow cluster looks slow rather than broken |
| Wide `NamespaceErrs`-style partial states multiply the UI cases | One reusable "scope state" cell shared with the services screen |

## 10. Phases (each ends with `go test ./...`, `golangci-lint run`, schema and docs updated)

| Phase | Content | Visible result |
|---|---|---|
| M21.1 | Domain types and rollup rules, `MetricsSource`, `ResourceUsage`, fake, contract suite, `app.Usage` with history | Tests only |
| M21.2 | Kubernetes adapter (`metrics.go`, numeric `ResourceAmounts`, RBAC doc, lab identity), demo scenarios | `--demo` data, adapter tests |
| M21.3 | `usageScreen` level 1: namespaces and services, columns, sort, filter, failure states, golden tests | `u` works in `--demo` and on a cluster |
| M21.4 | Drill-down to workloads, pods and containers, preview panel with `Explain`, sparklines | Full view |
| M21.5 | `ui.yaml` keys, `make schema`, `docs/CONFIG.md`, `docs/DECISIONS.md`, real-GKE QA report | Release candidate |
| Later | `ResourceQuota` row per namespace (`get/list resourcequotas`: used vs hard); Prometheus / Cloud Monitoring `MetricsSource` for real history | Separate decisions |

No time estimates are given: none can be derived from the code.

## 11. Decisions for the owner

1. **Data source.** Metrics API only in v1 (recommended; zero extra infrastructure) or also plan a Prometheus/Cloud Monitoring adapter first (real history, peaks)?
2. **Client library.** Typed `k8s.io/metrics` module (recommended, fake client available) or a raw REST call with local structs (no new module)?
3. **Numeric resources.** Add `ResourceAmounts` beside the strings (recommended, additive) or switch `Resources` to numbers and format on display (cleaner, touches `Explain` and the preview)?
4. **Containers in the sums.** Default all containers (what the pod really costs) with `A` to restrict, or default to application containers only, matching the logs screen?
5. **Multi-namespace services.** Per-namespace rows only (recommended for v1) or add the flat per-service lens (`tab`) now?
6. **Key.** `u` for the new screen (free today), or another?
7. **Polling default.** 15 s with a 5 s floor, or slower to be gentler on shared API servers?
8. **Thresholds.** 80 / 95 % of limit as defaults acceptable, or none until configured?
9. **ResourceQuota** in M21.5 or deferred (needs one more read permission)?

## 12. If accepted

Record the choices in `docs/DECISIONS.md` under the next free number (D-074 is already used by the M20 design), extend `docs/ARCHITECTURE.md` (port lists, extension-point row "another metrics backend → new `MetricsSource` adapter", package map) and start M21.1.
