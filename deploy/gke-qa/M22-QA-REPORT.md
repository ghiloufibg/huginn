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

## 3. Second batch (same day): CLI, latency, error states, Kafka, live changes

### Command line and config (C-1…C-8), run without a TTY against `huginn-plain`

| Case | Result |
|---|---|
| C-1 unknown environment | exit **1**, message lists the known names. The plan expected 2; README documents 2 only for a missing/invalid config folder or no TTY, so 1 is within the documented rules (the README does not say which code a bad flag gets) |
| C-2/C-3 bad `--since`, `--from`, `--to` without `--from`, `--theme`, `--containers` | exit 1 each, one-line reason that names the accepted forms |
| C-4 invalid config | exit 2, every problem as `file:line:column`, with a "did you mean" for a misspelt key; the usage settings are validated (`usage.refresh: 1s` below the 5 s floor, `warn >= crit`) |
| C-5 no TTY | exit 2, one-line message, no escape sequences |
| C-8 `--version`, `--help` | exit 0 |
| missing config folder | exit 2 and the expected folder structure |

Not run: C-6 (debug log scan), C-7 for the shell `kafka read` flags other than `--tail` (`--since --from --to -f --committed --raw --no-decode`), C-9 (token expiry, wrong context).

### Latency and startup (N-11, N-12), 3 to 5 runs, polling every 50 ms (resolution about 60 ms), 200×50

| What | Runs (ms) |
|---|---|
| start to services synced | 1 787, 1 887, 2 029 |
| `u` to the usage table with data (cold) | 526–584 |
| usage: `s` (sort), 3 × `s`, `p` (detail) | 62–69, 60–68, 61–69 (at the measuring floor) |
| usage: `esc` back to services | 73–129 |

Three rows of the script (`usage-expand`, `usage-filter-open`, `services-down`, 4 to 13 ms) were **invalid**: their expected text was already on screen. Discarded. Nothing exceeded the proposed 150 ms. This measures the screen update after a key, not what a person perceives.

### Error and size states

| Case | Result |
|---|---|
| P-2 API unreachable (`HTTPS_PROXY` to a closed port) | services and usage screens both show the cause with the API URL, a fix line ("Check network or VPN access cluster"), `retrying automatically · press r retry now`; the process stays up |
| T-2 resize storm | 200 random resizes (10–250 columns, 3–62 rows) on the usage screen, then 20×6, 10×3 and 200×50: process alive, no crash. **Only liveness was checked**; the captures at 20×6 and 10×3 were not read |
| T-1 themes × sizes | `light dark accessible classic none auto` × 80×24, 120×30, 220×50, on the services and usage screens: all 18 alive. The captures (`m22/ui/`) were **not reviewed** for legibility |

### Kafka, tier 1 (plaintext, `kubectl port-forward`), one broker, topics created with `kafka-topics.sh`

Oracle: counts produced and `kafka-get-offsets.sh`. Karapace, SASL/TLS and the Avro cases were **not run**.

| Case | Expected | Observed |
|---|---|---|
| `huginn kafka check catalog` | 5 topics with their partitions | 5 topics, partitions 3/1/2/6/1, all `ready` |
| `kafka read topic-b --tail 2` | last 2 per partition | 6 records, 2 per partition |
| TUI `topic-b`, default view (200 records, 3 partitions) | 200 | **200** |
| filter `key=k1` | 40 (keys `k(i mod 5)`, 200 records) | **40** |
| filter `partition=2` | 80 (end offset of p2) | **80** |
| text filter `50` | 2 (`n=50`, `n=150`) | **2** |
| `range-topic` (6 partitions, 3 000 records), `0` last 100 per partition | 600 | **600** |
| `range-topic`, `6` (1 day) | 3 000 | **3 000** |
| consumer groups on the broker after all of the above | none | **none** (`kafka-consumer-groups.sh --list` empty) |

Not compared: record order inside partitions, the `]`/`[` page keys (the topic fits one page, so they changed nothing), follow with a live producer, pause with "no broker read while paused", isolation `i`, raw `D`, range `b` beyond opening the popup, copy `y` content.

### Merged statement coverage of the two instrumented real-cluster walks (services, usage, logs, Kafka)

| Package | Before Kafka | After Kafka |
|---|---|---|
| `adapters/driving/tui` | 59.1 % (121 functions at 0 %) | **69.5 %** (60 functions at 0 %) |
| `adapters/driven/kafka` | 0 % | 65.6 % |
| `adapters/driven/kubernetes` | 67.8 % | 70.8 % |
| `core/app` | 47.4 % | 63.2 % |
| `core/domain` | 42.1 % | 56.9 % |
| `config` | 47.1 % | 56.4 % |
| `bootstrap` | 43.2 % | 52.2 % |

The 60 remaining TUI functions at 0 % include the trace view (`trace.go`: `range-emitter` carries no `trace_id`), the mouse handlers (`mouse.go`: `tmux send-keys` cannot send mouse events), the error panels (the proxy test ran on the **uninstrumented** binary), the range popup, `logfilter.go` context and match helpers, and the usage screen's error and narrow-terminal branches (`usage.go:491, 647, 748, 807, 835`).

### Live changes under the usage screen (R-3, R-4, U-18), `huginn-reader`

| Action | Observed |
|---|---|
| remove the `metrics.k8s.io` rule from the Role (23:28:07) | within 45 s the status line reads `stale since 23:28 (52s)`; rows keep their values; no error panel |
| restore the Role (23:29:03) | **still `stale since 23:28 (1m) · sampled 2m ago` 45 s later**; fresh at the next capture (about 2 min after the restore). The use case backs off after errors (D-075); the screen gives no hint that `r` retries at once. **Finding F-4 (P2)** |
| scale `usage-mem` 3 → 0 | row shows `0/3`, then pods drop out; no share is computed for pods without a sample |
| scale back 0 → 3, 25 s later | `0/3 · 3 pods not reporting` (never 0 %), then `3/3` once the metrics server has a sample (38m CPU while the new pods start) |
| delete `range-emitter-0` while its logs are followed | pod strip `emitter-0 terminated`, `emitter-1 Healthy`; lines keep arriving (`LIVE 39/s`); the buffer line says `older lines of the window not loaded: 50000 lines kept (buffer size), 21751 older skipped` |

**Rate of the fixture, measured late:** consecutive lines of one pod are 25 ms apart, about 40 lines/s per pod and 80 lines/s for the two. The 143–189 MiB peaks of N-2 and N-4b are a **full 50 000-line buffer at 80 lines/s**, not a leak; they are not comparable with the 76 MiB of M10/M14 (a lower rate).

### Findings, additions

| # | P | Finding |
|---|---|---|
| F-4 | P2 | After the cause of a usage error is fixed, the stale marker stays for up to about 2 minutes (backoff); the screen says nothing about `r`. Measured once |
| F-5 | info | `usage.refresh` of 15 s with the metrics-server window of about 40–50 s means the displayed sample is 30–60 s old (`sampled 57s ago`). The screen says so; it is the metrics API, not Huginn |

## 4. Third batch: soak, fixes verified on the cluster, log content against `kubectl`

### Soak (N-5): 11 cycles of about 5.5 minutes (60 minutes), `plain` build, alone on the cluster

Each cycle: services idle 60 s, usage open 120 s, logs follow of `range-emitter` 90 s (80 lines/s into a 50 000-line buffer), Kafka topics and records 35 s. Sampled every second (`m22-soak-summary.sh`).

| Cycle | RSS at the end of the idle minute | Peak of the cycle | Threads | Fds |
|---|---|---|---|---|
| 1 | 46.6 MiB | 175 MiB | 16 | 15 |
| 2–5 | 53.8, 64.7, 58.7, 62.9 | 155–199 MiB | 18 | 15 |
| 6–8 | 58.0, 57.4, 57.1 | 163–183 MiB | 18–19 | 15 |
| 9–11 | 59.1, 55.5, 55.8 | 171–211 MiB | 19 | 15 |

**Result: pass.** The idle-minute RSS rises over cycles 1–3 (mean 55.0 MiB) and then holds: cycles 9–11 average 56.8 MiB (+3 %, proposed limit +10 %). Descriptors constant at 15; threads 16 → 19 and flat from cycle 7. All 11 cycles ended with the process alive. The peaks are the full buffer. One run, one cluster, one terminal size.

### Findings fixed after the first run, each verified on the real cluster with the fixed binary

| # | Finding | Fix | On the cluster |
|---|---|---|---|
| F-1 | completed Job / CronJob pods counted as "not reporting" (`20/22 · 2 pods not reporting`) | a finished pod is counted in `Rollup.Done`, not in the pods | namespace `23/23`; `usage-done` and `deploy-cron` read `0/0 · 1 pod completed` |
| F-2 | a service under a `no access` namespace read `1 pod not reporting` | its row keeps the name only; the namespace row says why | `prd-api` bare under `qa-prd · no access: pods.metrics.k8s.io` |
| F-4 | after a permission was restored the stale mark stayed about 2 minutes, with no hint | retry wait capped at one minute (it was 8 ticks); mark reads `· r retries now` | stale 30 s after the revoke; recovered **29 s** after the restore on its own, **1 s** with `r` |
| F-6 | at 80 columns the NOTE column lost `no limit`, leaving `—` unexplained | `no limit` first | unit test (80 columns); not re-run on the cluster |
| F-7 (older than M21) | `f` (follow off) emptied the logs screen for about 300 ms and a save in that time said `nothing to save` | with the history loaded and nothing paused or paged, `f` stops the stream and keeps the lines; D-025 amended | before: blank for 313–330 ms in the current and the pre-M21 binary (once 1.3 s); after: **never blank** (3 runs) |

A new test fails with the old behaviour for F-1, F-2, F-4 (a mutation check with the old cap: still stale 2m20s) and F-7. Commits `eb6e560`, `355d241`, `a89591c`.

### Log content against `kubectl logs` (L-1, L-8, L-10), saved with `ctrl+s`

| Case | Result |
|---|---|
| `prd-api`, 15 min window, follow off, save | 180 lines, numbers 2502–2681, **0 duplicates, 0 missing between the first and last, 0 not present in `kubectl logs`**; 180 lines = 15 min at one line per 5 s |
| `range-emitter`, `--since 5m`, follow off, save (2.7 MB) | pod 0: 12 157 lines, `seq` 022213–034369; pod 1: 12 159 lines, `seq` 373569–385727; **0 duplicates, 0 missing, 0 extra against `kubectl logs --since=8m`** on both; about 40 lines/s × 300 s |
| trace view `v` on `trace-0000` (2 pods, 9 lines each) | `TRACE trace trace-0000 · 18 lines · 2 pods · +16.0 s`; `kubectl logs` of both pods: 9 + 9 = 18 lines with that id |

Not compared: the window edges to the second (the window slides while the test runs), `Y` raw copy content, `y` clipboard content, redaction in the saved file (the fixtures carry no pattern the `redact` list matches).

### Display review (T-1), 80×24 and tiny sizes, read by eye

At 20×6 the screen shows a truncated header, the first rows and `? help`, no crash; at 10×3 it prints `huginn: te`, cut mid-word, acceptable at that size. At 80×24 `none` and `accessible` show the same columns, words only (`—`, `no limit`, `CPU 100% of limit`): nothing depends on colour. The other themes at 120×30 and 220×50 were **not read**.

### Updated list of what was not run

Spring trio and Karapace (so Avro/JSON Schema decoding and the SASL/TLS Kafka tier); the `-race` pass; the secrets scan of artefacts; the clipboard; chaos beyond a revoked permission, a scale-to-zero, a deleted followed pod and an unreachable API (no network cut mid-session, no metrics-server removal, no throttling); 3-run medians for the performance numbers outside the latency table; the per-verb API count.
