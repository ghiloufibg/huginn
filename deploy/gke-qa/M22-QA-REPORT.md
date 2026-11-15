# M22 QA Report — Real GKE, usage screen and non-functional pass (partial, in progress)

Design: [`docs/plan/M22-gke-full-qa.md`](../../docs/plan/M22-gke-full-qa.md). Executed 2026-11-15 against `huginn-qa` (GKE Autopilot, `us-east1`, master 1.36.4-gke.2377000), project `huginn-kube-tui`. Binary under test: `huginn v0.5.1-0.20261115194452-7b50d0bc26f9` (commit `7b50d0b`), Linux amd64, run from WSL2 inside tmux (200×50).

**This is a partial run, written while it runs.** Anything not listed under "Results" was not run. The whole-TUI ledger and statement-coverage merge of the design (§1) have **not** been done.

## 0. Session facts

| Item | Value |
|---|---|
| Cluster created | 2026-11-15T19:57:17Z, `RUNNING` at 20:01:32Z (4 min 15 s) |
| Budget cap agreed | 30 EUR, about 8 h of cluster time (owner reply "OK, go"); internal stop at 25 EUR. **Cost not measured**: `gcloud` cannot read the credit; read the Console after teardown |
| Pricing checked | cluster fee $0.10/h covered by the free tier ($74.40/month); pod rates found only for `us-west8`, **not** `us-east1` (cited from the GKE pricing page search, unverified for this region) |
| metrics-server | present but **not serving for about 5 minutes after cluster creation** (`v1beta1.metrics.k8s.io` absent from `kubectl get apiservices` until after the first workloads were up) |
| `-race` binary | **not built**: `gcc` missing in WSL (owner to install) |
| `mitmproxy`, `jq`, `kcat` | missing in WSL: API-load evidence by proxy (N-7) not available; Kafka and the Spring trio **not deployed** this session |
| RBAC boundary | proven with raw `kubectl` under `huginn-reader` before any Huginn run: `qa-rec` reads and `top` work; `qa-restricted` `Forbidden`; `qa-prd` pods readable but `pods.metrics.k8s.io` `Forbidden`; delete and `get nodes` `Forbidden` |

## 1. Results

### Usage screen (U-1…U-20), against the real Metrics API

| ID | Result | Evidence |
|---|---|---|
| U-1 agreement with `kubectl top` | **pass** (single comparison, not the 3-run median the plan asks for) | `usage-cpu`: 999m on screen, 999m in `kubectl top`; `usage-mem`: 770Mi = 3 × 256Mi; `usage-sidecar`: 129Mi = 2 × 64Mi sidecar |
| U-2 shares | **pass** | `usage-mem` 50 %/50 % (256Mi of 512Mi); `usage-cpu` 199–200 % of request, 100 % of limit (request 500m, limit 1) |
| U-3 no limit never reads 0 % | **pass** | Autopilot sets requests but no CPU/memory limits (`limits` holds only `ephemeral-storage`): rows show `%LIM —` and `no limit` |
| U-4 `A` toggle | **pass** | namespace memory 910Mi → 782Mi (−128Mi, two 64Mi sidecars); status line says `application only` |
| U-5 drill-down | **pass** | `enter` opens a service on its pods and a pod on its containers; `p` shows requests and limits |
| U-6 permission failures | **pass** | under `huginn-reader`: `qa-rec` full data; `qa-prd` and `qa-restricted` each marked `no access: pods.metrics.k8s.io` on their own row |
| U-12 keys | **pass** for `enter space p s / A r L esc ?`; `ctrl+e` **not tested** (driver sent the literal text, a script error) | captures 02–19 |
| U-13 sorts | **pass** | memory → cpu → memory-limit → name; no-limit rows sort last under the limit sort |

### Findings

| # | P | Finding | Evidence | Owner decision |
|---|---|---|---|---|
| F-1 | P1 | **Completed Job and CronJob pods are shown as "not reporting"** and counted in `PODS`: `usage-done 0/1 · 1 pod not reporting`, `deploy-cron 0/2`, and the namespace reads `20/22 · 2 pods not reporting`. The QA plan (M21-gke-qa §1) expected a completed Job pod to be "not in the usage, not an error". A finished pod is not a reporting gap | `out/02`, `out/09` | exclude pods in phase `Succeeded` from the total, or keep and word it `completed`. Not changed here |
| F-2 | P2 | Under a namespace marked `no access`, its services still list `prd-api 0/1 · 1 pod not reporting`. Accurate but reads as a pod fault | `out-r/01` | wording |
| F-3 | info | The Metrics API was not served for about 5 minutes after cluster creation. What the usage screen shows during that window was **not observed** (it was not open yet); worth one check with the screen open at cluster creation | session log | none |

### Non-functional, Huginn process (WSL2, tmux 200×50, `plain` build, one run each unless stated)

Sampled once a second from `/proc` (RSS, threads, file descriptors, CPU ticks) and the raw terminal bytes of the pane (`tmux pipe-pane`). Cluster: 20 pods in `qa-rec`.

| ID | Result | Numbers |
|---|---|---|
| N-1 idle footprint | **pass** (proposed idle CPU under 1 %: services idle read **1.1 %**, usage 1.15 %, i.e. marginally over; a one-core reading of a 1 Hz sample, not conclusive) | services idle 43 MiB / 17 threads / 15 fds; usage screen open 150 s: 43→46 MiB, 17 threads, 16 fds |
| N-6 redraw bytes (flicker proxy) | **pass** | idle services: 3 461 bytes in 116 s; usage screen: 7 672 bytes in 146 s (about 53 B/s); no continuous repainting |
| N-3 usage screen cost | **pass for the cycle**, long run not done | 100 open/close cycles of `u`: RSS 50–52 MiB throughout, threads 18–19, fds 15–16 |
| N-4 leak by cycling, isolated, current binary vs pre-M21 baseline (`01a1533`), settled 4 min | **pass** (proposed ±15 %; threads +1 to +2 in both binaries, so not from M21) | 20 environment round trips: 46→47 MiB (baseline 45→48); 30 logs open/close of `range-emitter`: 50→51 MiB (baseline 48→65); fds 15 before and after in every run |
| N-4b follow then leave | **no regression**, one case slightly over | 2 min following `range-emitter` logs: peak 175 MiB (baseline 166); 4 min after leaving: **54 MiB** (46 at start, +17 %; baseline 80 MiB) |
| N-2 loaded footprint | observed | following `range-emitter` for 2 min: 143–189 MiB, CPU about 13 % of one core. The emitter's line rate was **not measured**, so this is not comparable with the M10/M14 figures (76 MiB loaded) |

**Method problems in this session, none of them in the numbers above:** (1) a first leak run matched processes by command line and sampled the wrong process; (2) the first "baseline" binary came from an old merge-base (`2242960`) and rejected the config, so it was rebuilt from `01a1533`; (3) `BIN` was not passed into the tmux session, so the "baseline" ran the current binary; (4) a `pkill -f` pattern matched its own shell. All four were found before any of their data was used. The first NFR run (single session, default binary) was not affected and is the source of the idle and usage-cycle rows.

### Environment facts found on the cluster

| # | Finding |
|---|---|
| E-1 | Project quota `CPUS_ALL_REGIONS` = **16 vCPU, 8 in use** with about 25 small pods: Autopilot reported `FailedScaleUp … GCE quota exceeded` for system pods in `us-east1-d` (they scheduled anyway). The scale fixture (N-3 at 20 namespaces × 3 services) is capped by this, not by cost |
| E-2 | Pods with **no resources set get 500m CPU and 2 Gi memory each** from Autopilot (`usage-nolimit`); this is the quickest way to reach the quota |
| E-3 | `/metrics` of the API server is not exposed on Autopilot (`NotFound`): API load needs Cloud Monitoring (control-plane metrics) or audit logs |
| E-4 | `mitmproxy`, `jq`, `kcat`, `gcc` missing in WSL: no proxy evidence (N-7), no `-race` pass (R-1), no Kafka oracle |

### API load of the usage screen (N-7, N-8), indirect evidence only

**What could not be obtained:** a per-verb request count. `/metrics` of the API server is not exposed; Cloud Monitoring's `apiserver_request_total` (control-plane metrics enabled for this) has **no `metrics.k8s.io` series** (aggregated API calls are not counted there) and mixes every client for core `pods` LIST/WATCH (about 95 LIST per 15 min on an otherwise idle cluster); metrics-server's own counters sit behind a GKE Warden block (`managed-namespaces-limitation`); no `mitmproxy`. Data Access audit logs would work but change the project's IAM audit config: **not done, owner's decision**.

**What was measured instead:** bytes read and written by the Huginn process (`/proc/<pid>/io`, TLS bytes), per 15 s bucket, one run, 1 namespace, about 25 pods, `usage.refresh` 15 s.

| Phase | Read per 15 s | Notes |
|---|---|---|
| services screen only (before) | 3.7–5.4 KB | one spike of 14 KB (the CronJob firing) |
| usage screen open (5 min) | 10–13 KB steady | about **7 KB more per 15 s**, consistent with one `PodMetrics` list of about 25 pods per refresh (about 280 B per pod); written bytes +55 B/s, consistent with one small request per refresh |
| after leaving (3 min) | about 4.9 KB | back to the services level in the first full bucket; a 31 KB spike in the bucket where the CronJob fired (21:45:00Z) |

Opening the screen read about 80 KB in the first 45 s (a one-time cost, not traced). **This shows the rate and that polling stops on leaving. It does not prove which verbs were used**; the verb restriction rests on the RBAC proof (a write was `Forbidden`) and on the adapter's list-only unit test.

### Statement coverage reached on the real cluster (§1 cross-check)

One scripted key walk (`m22-drive-walk.sh`) with `go build -cover -coverpkg=github.com/ghiloufibg/huginn/...`, ending with `q` so the data is flushed: services, usage, logs of `range-emitter` (windows, filters, levels, display, pods, selection, copy, save, zoom, trace, diagnostics, previous instance, bug report), help, environment switch, service finder.

| Package | Statements covered |
|---|---|
| `adapters/driving/tui` | **59.1 %** (121 of 457 functions at 0 %) |
| `adapters/driven/kubernetes` | 67.8 % |
| `core/app` | 47.4 % |
| `core/domain` | 42.1 % |
| `config` | 47.1 % |
| `adapters/driving/cli` | 78.1 % |
| `adapters/driven/{kafka,schemaregistry,sops,localfiles,demo}` | 0 % (not deployed or not used in this session; `demo` by design) |

The 121 uncovered TUI functions are almost all (a) the three Kafka screens (`kafka.go`, `kafkarecords.go`, `kafkazoom.go`), because Kafka was **not deployed**, and (b) the error panels (`errpanel.go`, `errTitle`, `errFix`, `retryKeys`), because **no failure was injected in the walk**. Also uncovered: `columns.go` (`toggleColumn`, `togglePod`, `sameState`), `help.go` `mutedHelp`, `keys.go` `First`, `logfilter.go` `appendContext`. The walk only checks that the process stayed up after each step (a weak check: it greps the pane for the word `huginn`), so **this is reach, not correctness**.

The first instrumented build used `-coverpkg=./internal/...`, which matched nothing: the binary ran but wrote no data. Rebuilt with the module path.

## 2. Not run (so far)

Spring trio, Kafka and Karapace (and so every `K-*` case and the Kafka screens); the `-race` pass; the A/B of the services screen at the key level (only memory, cycling); latency measurement (N-12); startup time (N-11); the 60 minute soak (N-5); chaos (R-2…R-9, including the network cut, metrics-server removal and throttling); terminal matrix (themes × sizes) and resize storm; clipboard checks; the plan's 3-run median for any timing; the whole-TUI ledger (`coverage.tsv`) beyond the figures above; per-key checks of the logs screen against `kubectl logs` oracles (only reached, not compared); secrets scan of artefacts (N-17).
