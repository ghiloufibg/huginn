# M22 QA — Real GKE: the whole TUI, the usage screen, and the non-functional requirements (design)

Status: **a design. Nothing in this file has been run**, and no `gcloud`, `kubectl` or cluster call was made to write it. Figures quoted from earlier sessions are marked with their source; every threshold marked *proposed* needs the owner's agreement before execution (§11).

**Goal.** One session on a real GKE Autopilot cluster that covers, with evidence:

1. **The whole TUI surface**, every screen, key, overlay, state, flag and theme, not only what a milestone touched (M5–M15 each covered their own change).
2. **The resource usage screen** (M21, [D-075](../DECISIONS.md)), which `--demo` and the unit tests cannot validate: real numbers, real errors, real API load. It absorbs [M21-gke-qa.md](M21-gke-qa.md) (its checks 1–11 become `U-1…U-11` here and are not repeated).
3. **The non-functional requirements**: memory and CPU of the Huginn process, API-server load, latency, redraw stability (flicker), leaks over time, behaviour under failure, read-only, and cost.

What "100%" means here is defined in §1. It is not "every statement executes".

## 0. What this reuses, what is new

| Already built — reused, not rebuilt | New in this session |
|---|---|
| GKE Autopilot, `us-east1`, `huginn-reader`, impersonated kubeconfig, WSL + tmux drivers, teardown ([M15-gke-qa.md](M15-gke-qa.md) §1, §9; M14 §2.2) | A **coverage ledger** that proves which actions, screens and states were reached (§1) |
| Fixtures: Spring trio, crash loop, OOM, loadgen, sidecars, `range-emitter`, `deploy-*`, Kafka + Karapace, `named-pod-fixture` (`deploy/gke-qa/*.yaml`) | Fixtures for usage (M21-gke-qa §1) and a bounded scale fixture (§3) |
| `e2e.sh`, `e2e-m10.sh`, `e2e-m12.sh`, `mouse-test.sh`, `record-rollouts.sh` (`deploy/gke-qa/`) | A driver library with per-keystroke timing, and PTY-byte capture (§5) |
| `ps`, `HUGINN_CPUPROFILE`, `/proc/<pid>/io` sampling (M10, M14, M15) | `/proc` sampling of threads and file descriptors, and a **leak test** by repeated open/close of every screen (N-4) |
| A/B of a pre-change binary against the new one (M15 §6.3) | The baseline is the binary **before M21**, because M21 changed the services screen's data path (`SharedCatalog`) |
| Benchmarks of the usage screen (`usage_perf_test.go`: frame 3–4 ms, snapshot+frame 5 ms at 2 000 services, measured on a laptop) | The same costs **on real data**, plus what benchmarks cannot see: the API, the network, the terminal |

## 1. Coverage model: what "100% of the TUI" means and how it is proved

**Inventory (the denominator), generated, never typed by hand:**

| Set | Source | Size at the time of writing |
|---|---|---|
| Actions | `Act…` constants in `internal/adapters/driving/tui/keys.go` | about 85 (counted by the ledger tool at run time) |
| Screens | types implementing `screen` in `model.go`: services, logs, zoom, trace view, Kafka topics, Kafka records, Kafka zoom, usage, help, pod selector | 10 |
| Overlays and pickers | environment (`ctrl+e`), find service (`ctrl+p`), window (`T`), range (`b`), level (`l`), pod selector (`S`), error groups (`E`), diagnostics (`d`), bug report (`B`) | 9 |
| States and chips | every status chip, empty state and error panel the code can print (grep of `errpanel.go`, `keybar.go`, `livestats.go`, empty-state strings) | listed by the ledger tool |
| CLI | every flag and subcommand in `--help`, exit codes 0/1/2 | README "Usage" |
| Themes and sizes | `auto light dark accessible classic none` × 80×24, 120×30, 220×50 | 18 |
| Config variations | `ui.yaml` keymap override, `usage.*`, `redact`, `mute`, `containers.yaml`, layouts | `docs/CONFIG.md` |

**Ledger.** `deploy/gke-qa/coverage.tsv`, one row per inventory item, filled during the session: `item, test id, evidence file, result`. A tool (written in the execution session, `go run ./scripts/qa-inventory` or similar) regenerates the inventory from the code and **fails if any row has no test id and no written reason**. Every test case in §4 names the inventory items it exercises.

**Measured statement coverage as a cross-check, not as the claim.** A build with `go build -cover -coverpkg=./internal/...` is used for the functional pass; it writes coverage data to `GOCOVERDIR` on exit. `go tool covdata merge` + `textfmt` gives the statements reached **on a real cluster**. Report: coverage of `internal/adapters/driving/tui`, `…/driven/kubernetes`, `…/driven/kafka`, `internal/core/app`, and the **list of functions with zero coverage in both the unit tests and this session**, each with a verdict: reached by a case added now, dead code to delete, or unreachable without a failure we cannot produce (and then said so).

**Honest limits.** Not claimed: every statement (error branches need failures a managed cluster does not produce on demand), every state combination (filters × windows × pods × sizes is combinatorial; §4 covers each axis alone and a chosen set of crossings), other terminals or operating systems than the ones in §3. Coverage data is lost when the process is killed with `SIGKILL` or panics, so chaos cases (§7) that kill it use a second, uninstrumented run.

## 2. Preconditions and cost governance

- **Credit.** Last known: 256 EUR, expiring 2026-12-26 ([M15 report](../../deploy/gke-qa/M15-QA-REPORT.md), read by the owner in the Console on 2026-11-02; `gcloud` cannot read it). Today is 2026-11-15, so about 41 days remain. **Not rechecked for this design.** The balance after M15 is unknown; the owner reads it in the Console before anything is created. No cost estimate is made here: none has been verified against current GKE Autopilot pricing.
- **Rules carried over (each cost someone time before):** after any failed `create-auto`, list clusters (a half-created billable cluster survived `GCE_STOCKOUT` in M14); `us-east1`; create the impersonated kubeconfig user under its own name before a plain `get-credentials`; prove the `qa-restricted` boundary with raw `kubectl` first; WSL with `CLOUDSDK_CONFIG` pointing at the Windows gcloud config; `MSYS_NO_PATHCONV=1` in Git Bash; teardown is unconditional (§9).
- **Autopilot facts to confirm in step 1, not assumed:** metrics-server is present (the M21 check relies on it), `v1beta1.metrics.k8s.io` is served, and the three kinds of API-load evidence of §6 N-7 are obtainable (the managed control plane may refuse `/metrics`, and Data Access audit logs may be off and billable).
- **Cost-bounded by design:** all workloads are `busybox`/`pause`-class with minimal requests, no public address (Kafka and Karapace behind `kubectl port-forward`), nothing persistent. The pod count of the scale fixture is capped by the owner (§11, D-2).

## 3. Environment and fixtures

**Host.** WSL2 Ubuntu with tmux, driving Huginn in a PTY; the Windows gcloud config; Windows Terminal for the human look (§4 T-*). **Only this combination is covered.** macOS, native Linux terminals and other emulators are *not exercised* unless the owner provides them (§11, D-6); the report says so.

**Four binaries from one commit:**

| Binary | Used for | Why separate |
|---|---|---|
| `plain` (release flags) | every performance and resource number | instrumentation changes timings and memory |
| `cover` (`-cover`) | the functional pass and the ledger | statement coverage |
| `race` (`-race`) | one full functional pass (§7 R-1) | finds data races the unit tests did not; slow, never used for numbers |
| `baseline` (merge-base of M21, `claude/gallant-dijkstra-1lp7r0`) | A/B on services and logs (§6 N-9) | M21 rewired the catalog watch |

**Cluster.** One Autopilot cluster, namespaces `qa-rec` (full set), `qa-prd` (a second environment, so `ctrl+e` has somewhere to go), `qa-restricted` (no `metrics.k8s.io` grant, no secrets), `qa-usage` (M21 fixtures), `qa-scale` (below).

**Fixtures, all existing unless marked new:**
- Logs and rollouts: trio, `payment-service-loadgen`, `noisy-fixture`, crash loop, OOM, sidecars, `range-emitter`, `range-flood`, `deploy-*`, `named-pod-fixture`; the `edge-lines` fixture with a 1.1 MB line for zoom (from `deploy/lab`).
- Kafka: single broker with SASL/SCRAM and TLS, `range-topic`, orders and catalog fixtures, Karapace with Avro and JSON Schema.
- Usage (**new**, from M21-gke-qa §1): a 256 MiB memory holder with request = limit, a one-core burner, a no-limit Deployment, a sidecar Deployment, a freshly created pod, a Job that completes, a restricted-role namespace.
- **`qa-scale` (new):** K namespaces × S services × 1 replica of the smallest possible pod, K and S set by the owner's pod cap (D-2). Purpose: the real adapter cost per namespace list (requests, bytes, latency) and the services/usage screens over a few hundred rows of real data. **Scale beyond the cap is covered by the benchmarks only** (TUI and use case, not the API); the report says so.

## 4. Functional catalogue

Priority: P0 must pass for the session to count; P1 important; P2 cosmetic or rare. Each case lists the ledger items it exercises. "Oracle" is the independent source of truth (`kubectl`, a ledger of timed actions, the broker CLI).

Rule for every screen: **walk every row of its README key table and of its in-app help (`?`) once**, and add the cases below. The ledger tool cross-checks that the help text, the README table and `keys.go` agree (a key present in one and not the others is a finding).

### 4.1 C — Command line, config, exit codes

| ID | P | Case | Expected |
|---|---|---|---|
| C-1 | P0 | `huginn`, `huginn prd`, `-e prd`, `HUGINN_ENV` | opens the named environment; an unknown name fails with exit 2 and the list of known ones |
| C-2 | P0 | `--repo`, `--since 1h/tail:N/head:N`, `--from/--to` (every accepted form incl. `now-90m`, RFC 3339, a zone) | logs open directly on that window; a bad value exits 2 with the reason |
| C-3 | P0 | `--containers app|all`, `--theme` ×6, `NO_COLOR`, `HUGINN_THEME` | applied at start; `NO_COLOR` forces `none` |
| C-4 | P0 | A broken config folder (one error per file kind) | exit 2, every problem printed as `file:line:column`, nothing started |
| C-5 | P1 | No TTY (`huginn </dev/null`, piped) | exit 2 with a clear message, no escape sequences |
| C-6 | P1 | `--log-level debug`, `HUGINN_DEBUG=1` | log file in the user cache dir, never on screen; **scanned for tokens, kubeconfig contents, record values** (N-12) |
| C-7 | P0 | `huginn kafka check/read` with every flag (`--tail --since --from --to -f --committed --raw --no-decode`) | output as documented; `check` exits 1 on an unreadable topic; no consumer group created (E-8) |
| C-8 | P1 | `--version`, `--help` | correct, exit 0 |
| C-9 | P1 | Kubeconfig variants: exec plugin (gke-gcloud-auth-plugin), expired token mid-session, wrong context | clear error panel with a fix line, `r` recovers after the token is renewed |

### 4.2 S — Services screen (and shared across screens)

| ID | P | Case | Expected |
|---|---|---|---|
| S-1 | P0 | Open, wait for sync, `j k ↑ ↓ pgup pgdn g G`, mouse wheel, click | cursor moves; synced state shown; same row count as `kubectl` (oracle: deployments/statefulsets/daemonsets grouped by repo label) |
| S-2 | P0 | `s` through every sort (status, name, restarts, age) | order matches the oracle for each |
| S-3 | P0 | `/` and `ctrl+f` filter: type, `enter` keeps, `esc` clears; inside the prompt `b`, `u`, `?` are text | rows filtered; no action fires from prompt text |
| S-4 | P0 | `p` preview on/off at 80×24, 120×30, 220×50 (side/under/forced) | sections WORKLOADS, PODS, WARNINGS (events after the 300 ms rest), SIDECARS, DEPLOYED present or squeezed as documented |
| S-5 | P0 | `enter` opens logs; `M` opens Kafka on a `K` row only; `u` opens usage; `esc` back each time | correct screen, correct breadcrumb, cursor restored on return |
| S-6 | P0 | `ctrl+e` to `qa-prd`, back, from **each** screen | services of the new environment; **no watch of the old one left** (connections via `ss`, N-4); the usage screen restarts on the new environment |
| S-7 | P1 | `ctrl+p` find service: type, pick, cancel | jumps to the row, also in a long list |
| S-8 | P0 | `r` resync; forbidden namespace row; stale marker after a network cut (D-056); empty environment | each state shows words, not colour alone |
| S-9 | P1 | A service with several workloads, a row with an error, the `K` marker, a repo present in two namespaces | as in M5/M14 |
| S-10 | P0 | `?`/`F1` help, `F2`/`ctrl+k` key bar sizes, `q`, `ctrl+c`, `esc` at the root | help searchable; quit restores the terminal (cursor, mouse mode, alt screen) |

### 4.3 U — Usage screen (M21)

`U-1…U-11` are the checks 1–11 of [M21-gke-qa.md](M21-gke-qa.md), unchanged (agreement with `kubectl top`, shares, hottest pod, `A` toggle, drill-down, failure states, `ctrl+e`, API load, no second informer set, memory, read-only). Added here:

| ID | P | Case | Expected |
|---|---|---|---|
| U-12 | P0 | Every key of the screen: `enter`, `space`, `←`, `→`, `L`, `p`, `s`, `/`, `A`, `r`, `esc`, `pgup/pgdn/g/G`, wheel | each does what the help says; `L` opens the logs of the **service under the cursor**, `esc` returns with the cursor and folds as left |
| U-13 | P0 | Sorts (memory, CPU, memory-limit, name) while data changes | order matches the numbers on screen; **cursor stays on the same row across snapshots** (the unit tests cover this with fake data, here with live data) |
| U-14 | P1 | Colour thresholds: a row at 79/80/94/95 % of its limit (the 256 MiB holder tuned to those shares) | warn and crit appear at the configured thresholds and **also as words** under `none`/`accessible` |
| U-15 | P1 | `ui.yaml` `usage.refresh` 5s and 60s, `usage.history` 0s, `usage.enabled: false`, `warn >= crit` | refresh honoured (measured from the proxy log); `0s` removes TREND; `false` removes the screen and its key and **issues no metrics request**; the invalid value is rejected at start with the key path |
| U-16 | P1 | TREND sparkline against a known ramp (memory holder that grows each minute) | the line rises; shape agrees with `kubectl top` samples taken every 15 s |
| U-17 | P0 | A namespace added or deleted while the screen is open | row appears or disappears within one refresh; no stale row, no panic |
| U-18 | P1 | A deployment scaled 3 → 0 → 3 | `PODS` reads `reporting/total` correctly through the transitions; never a share computed from a pod without a sample |
| U-19 | P1 | metrics-server restarted (Autopilot: delete its pod if permitted) while the screen is open | values kept and dated `stale since`; recovery without a restart. If the platform forbids it, *not exercised* |
| U-20 | P2 | Narrow terminals: 80×16, 100×24 | columns drop in the documented order; TREND and NOTE go first; nothing wraps or overflows |

### 4.4 L — Logs screen

Walk the whole README logs key table. Cases that need a real source of truth:

| ID | P | Case | Expected |
|---|---|---|---|
| L-1 | P0 | Windows `t T 1…7 0 9` and AZERTY aliases, `f` follow, `space` pause (buffer fills, `PAUSED +n`, `dropped N` beyond the buffer), `o` order | line sets equal `kubectl logs --since/--tail` for the same window (count and order); chips as documented |
| L-2 | P0 | Absolute range `b`, `] [` paging: the M15 oracle (`range-emitter`) | 0 missing, 0 extra, 0 duplicated across pages |
| L-3 | P0 | `P` previous instance on the crash loop and the OOM pod; `P` again | the crash reason visible; no `unable to retrieve container logs` leak |
| L-4 | P0 | Filters: `/` text, `ctrl+r` regex, `!` invert, `ctrl+a` stack, `x` filter/highlight, `X` context 0/1/3/5, `n N` | matches equal `grep` over the same lines; invalid regex shows an error, not a crash |
| L-5 | P0 | Levels: `l` picker, `e w a`, `> <` next/previous error, `E` error groups, with JSON logs of `formats/20-spring-json.yaml` | counts equal a `jq` oracle over the same lines |
| L-6 | P0 | Pods and containers: `tab`, `S` selector (one container across pods / one pod's containers), `A`, `I` pod id, a pod deleted and recreated while followed | strip updates; new pod's lines arrive; no stale "waiting" |
| L-7 | P0 | Display: `c ctrl+t C z R W F ← → H L M`, `F2` | each toggles as documented; `R` resets display only, not filters or window; **no residue** after toggling (compare `capture-pane` before and after the round trip) |
| L-8 | P0 | Selection, copy, save: `V`, `m`, `y`, `Y`, `ctrl+s`, `esc` | clipboard content equals the lines **as shown** (`y`) and **raw** (`Y`); the saved file equals the screen's lines byte for byte; `redact` applied on copy and save; file mode private; `copy.max_bytes` honoured |
| L-9 | P0 | Zoom (`enter`, `J K`, `p`, `tab`, `shift+tab`, `=`, `!`) on a normal entry, a stack trace, and the 1.1 MB line | no hang; filter keep/exclude by field value equals a `jq` oracle |
| L-10 | P0 | Trace view `v` on a `trace_id` across the trio | all pods' lines of that id, ordered, with time since the first; `esc` restores the previous filters |
| L-11 | P1 | Diagnostics `d`, bug report `B` | content correct, **no secret** in the bundle (N-12) |
| L-12 | P1 | Mute (`mute.loggers`, `M`): counts `muted N` | equal the oracle |
| L-13 | P0 | `1.1 MB` line, 50 000-line buffer, binary or invalid UTF-8 lines, ANSI escape sequences inside a log line, `\r`, very long lines | **terminal never corrupted** (`safetext.go`): no escape sequence from log data reaches the terminal; checked on the raw PTY capture |
| L-14 | P1 | Environment switch from the logs screen while following | previous stream closed, the services screen of the new environment |

### 4.5 K — Kafka screens (`M`, `D`, `i`, `0 1…7`, `f`, `space`, `o`, `y`, `enter`, `r`)

`K-1…K-n` re-run M11/M12/M14/M15 on the final binary: topics list, records of the last N per partition and by window, range and paging (the 0.6 % out-of-order edge of M15 is an accepted, documented limit: re-measured, not re-litigated), follow, pause (**no broker read while paused**), isolation `i`, raw `D`, Avro/JSON-Schema decoding, Karapace down (fails at once with the documented message), SASL/TLS errors, broker loss and return. Oracle: `kafka-console-consumer`/`kcat` from WSL on the same offsets. Read-only: no consumer group, no committed offset (`kafka-consumer-groups.sh --list` before and after).

### 4.6 P — Pickers, overlays, help, errors

| ID | P | Case | Expected |
|---|---|---|---|
| P-1 | P0 | Open and close every overlay of §1 with `esc`; while open, `1…7`, mouse, wheel and screen keys do nothing behind it | modality holds; popup clipped, not spilled (D-054) at 80×24 |
| P-2 | P0 | Every error panel: forbidden, not found, no metrics, network down, expired credentials, config invalid after a live reload if supported | words + fix line + the retry key; the retry works |
| P-3 | P0 | Every empty state (no services, no lines in window, filter matches nothing, muted-all, no pods, no records) | a sentence, not a blank screen |
| P-4 | P1 | `ui.yaml` keymap override: remap one key per screen; an unknown action; a key mapped to two actions | override works on every screen and in help; unknown rejected with a suggestion; the duplicate has a defined behaviour (observed, recorded) |

### 4.7 T — Terminal, rendering, mouse (human + captured)

| ID | P | Case | Expected |
|---|---|---|---|
| T-1 | P0 | 6 themes × 3 sizes × (services, usage, logs, zoom, Kafka records, a picker) | readable; under `none` and `accessible` **every state is distinguishable by words**; the ledger records one capture per cell |
| T-2 | P0 | Resize storm: 200 resizes in 10 s, including below the minimum (e.g. 20×6) | no panic, no stuck layout, correct redraw at the end, a "too small" message below the minimum |
| T-3 | P0 | Mouse: wheel, click to move, drag to select, `shift`+click, `mouse: false` (`mouse-test.sh`) | as documented; terminal's own selection works with `mouse: false` |
| T-4 | P0 | Clipboard: in tmux and in Windows Terminal through WSL (OSC 52 and the configured command) | content arrives, or an explicit message says why not; no silent failure |
| T-5 | P0 | Exit paths: `q`, `ctrl+c`, `SIGTERM`, `SIGHUP` (terminal closed), panic injected in a test build | the terminal is restored (cursor, alt screen, mouse reporting off, echo on) every time |
| T-6 | P1 | Paste of 100 KB into a filter prompt; a held-down key (key repeat) | no hang; frame rate stays in budget; the prompt keeps its text |
| T-7 | P1 | Unicode: wide characters, combining marks, RTL, emoji in logs and in repo names | no misalignment of columns; no cut in the middle of a character |
| T-8 | P2 | **Human look**: 15 minutes each on services, usage, logs follow; the owner (or tester) notes flicker, tearing, cursor jumps, lost selection, lag. Complements §5 N-6, which measures the same thing mechanically | recorded in the report as an observation with the terminal and size |

## 5. Non-functional method

**Sampling, for every N-case.** A sampler script (written at execution time) records once per second for the Huginn PID: `VmRSS`, `VmHWM`, `Threads`, the open file-descriptor count (`/proc/<pid>/fd`), `%CPU`, `rchar`/`wchar` from `/proc/<pid>/io`, and the established connections to the API server and brokers (`ss -tnp`). `HUGINN_CPUPROFILE` for CPU profiles; `go tool pprof -top`. **There is no heap-profile hook in the binary**: RSS, threads and descriptors are the evidence unless the owner approves adding one (§11, D-8).

**Driver.** `tmux send-keys` with the time of each key, `capture-pane -p` polled every 50 ms, and the time of the first poll where the expected text appears is the **input-to-redraw latency**. Every case runs **3 times**; the report gives min/median/max, never one run.

**Mechanical flicker measure.** The PTY is recorded with `script -q -f` (or the equivalent) for each screen. Computed from the raw bytes: bytes written per second while idle, number of full-screen clears (`ESC[2J`) and of full repaints per minute, and bytes per snapshot on the usage screen. **Expectation:** an idle screen writes near zero bytes (Bubble Tea writes only the diff); the usage screen writes only when a snapshot changes a visible cell; no full clear outside a resize or a screen change. This is the objective proxy for "TUI flakiness" the unit tests cannot give.

## 6. Non-functional catalogue

Thresholds marked *proposed* are tolerances against sampling noise, not derived from a measurement. Reference numbers from earlier sessions are context only, since cluster and load differ: M10/M14 idle 62–68 MB RSS, loaded 76 MB, 42→72 MiB over about 4 minutes in M14; frame costs 0.6 ms at 50 000 lines and 2.5 ms at 500 repositories (D-031); usage screen on a laptop benchmark: frame 3.1–3.8 ms and snapshot+frame 2.8–5.0 ms from 50 to 2 000 services.

### Resource utilisation (Huginn process, API server, cluster)

| ID | What | How | Criterion |
|---|---|---|---|
| N-1 | **Idle footprint per screen** | 5 minutes each on services, usage (`qa-scale`), logs on a quiet pod, Kafka records, zoom | RSS, threads, fds, CPU reported; idle CPU near zero (*proposed*: under 1 % on a screen with nothing changing) |
| N-2 | **Loaded footprint** | logs on `payment-service-loadgen` with `--containers all`, 50 000-line buffer; `bulk-emitter`; trace view over the trio; `ctrl+s` of 50 000 lines | RSS bounded by the buffer; the save does not double RSS (*proposed*: peak under 2× the pre-save RSS); frame cost from the CPU profile |
| N-3 | **Usage screen cost** | `qa-scale` at default refresh for 10 minutes, then `usage.refresh: 5s` | CPU and RSS reported against N-1 services; **no growth of `Threads`/fds**; history memory bounded by `usage.history` |
| N-4 | **Leak test by cycling** | Open and close each screen and overlay 200 times (usage, logs, zoom, trace, Kafka, pickers); `ctrl+e` back and forth 50 times | after a settle period, RSS within *proposed* 15 % of the start, threads and fds **equal** to the start, connections to the API server equal to the number of live streams (none left from closed screens) |
| N-5 | **Soak** | 60 minutes alone on the cluster, cycling services → usage → logs (follow on the loadgen) → Kafka records every 5 minutes | RSS plateaus (*proposed*: mean of the last 10 samples ≤ first 10 + 10 %); no monotonic rise; D-032's free-on-close visible as a drop after `esc` to services |
| N-6 | **Redraw stability (flicker)** | The PTY measure of §5 on every screen, idle and under load, at 3 sizes | idle bytes/s near zero; no full clear outside resize/screen change; usage: bytes only on snapshot changes |
| N-7 | **API load** | Every request Huginn makes, by verb and resource, for each scenario: services only, usage open, logs follow, Kafka, idle after leaving each | **Primary:** a logging proxy (`proxy-url` in the kubeconfig, `mitmproxy` in WSL), as M15 NFR-12(b) attempted. **Secondary:** audit logs, only if Data Access logs can be enabled at a known cost. Expected: usage ≈ one `list pods.metrics.k8s.io` per namespace per refresh while open, zero within 20 s of leaving; one set of pod/workload watches whether or not usage was opened; **no verb but `get list watch` and `pods/log`**. If TLS interception is impractical on Autopilot the report says the evidence is the RBAC proof alone |
| N-8 | **Bytes per request** | From the proxy log: response size of the namespace metrics list for 1, 10, 100 pods | linear in pods; reported so an operator can size `usage.refresh` |
| N-9 | **A/B against the pre-M21 binary** | Same cluster, same minutes, services screen idle and logs loaded (the paths M21 rewired through `SharedCatalog`) | RSS and CPU within *proposed* 10 %; watch count equal or lower; captures identical except the new `u` entry in help and key bar |
| N-10 | **Node and control-plane impact** | `kubectl top node` and the API latency seen by `kubectl get --raw=/readyz?verbose` timing before, during, after a deep page and a 20-load loop | no sustained rise attributable to Huginn |

### Performance (time and throughput)

| ID | What | How | Criterion |
|---|---|---|---|
| N-11 | **Startup to usable** | `huginn` cold, services synced; with 1 and with `qa-scale` namespaces; usage first paint | times reported; first paint of the usage screen shows the spinner and **no blank frame** |
| N-12 | **Input-to-redraw latency** | The driver of §5 on `j`/`k`, `enter`, `/` typing, `s`, `A`, `u`, `esc`, zoom on the 1.1 MB line, `ctrl+e` | median and max per action; **investigate** any single case over *proposed* 150 ms (the M10 threshold for "held") |
| N-13 | **Time to first line** | `enter` on a service to the first log line, per window kind; deep page of a range (M15 NFR-4 re-measured) | reported against `kubectl logs` as a floor |
| N-14 | **Throughput** | `loadgen` at tens of lines/s and `bulk-emitter` at maximum | `LIVE n/s` plausible; `dropped N` explained by the buffer; frame rate holds the 30 fps cap |
| N-15 | **Kafka** | first page of a range on 6 partitions; follow under a producer at a steady rate | reported; no consumer group (E-8) |

### Security, privacy, read-only

| ID | What | How | Criterion |
|---|---|---|---|
| N-16 | **Read-only** | N-7's proxy log and the RBAC boundary (`huginn-reader` and `qa-restricted`) over the **whole** session | only `get list watch` and `pods/log`; for `metrics.k8s.io` only `list`; no write verb is ever attempted, even refused |
| N-17 | **No secrets in artefacts** | Scan the debug log, the saved files, the bundle of `B`, the clipboard and the PTY capture for the Kafka passwords, tokens, the kubeconfig and the redacted patterns of `redact` | none found; redaction applies to copy and save |
| N-18 | **File safety** | Mode and location of saved files, of the debug log; a hostile log line trying to write a path or inject escapes | private mode, only under `save.dir`; no escape reaches the terminal (L-13) |

## 7. Robustness, chaos, concurrency

| ID | Action | Expected |
|---|---|---|
| R-1 | A full functional pass with the `race` binary | no data race reported on stderr/log; any report is a P0 defect |
| R-2 | Cut the network to the API server (`iptables` DROP in WSL) on each screen for 2 minutes, then restore | values kept and marked stale with the age; reconnect without a restart; no goroutine/fd left after recovery (N-4 method) |
| R-3 | Delete the pod being followed; delete the namespace under the usage screen; scale a deployment to 0 and back | the screen follows; no panic; no stale row |
| R-4 | Metrics: scale metrics-server to 0 (if the platform allows), revoke the `metrics.k8s.io` grant mid-session, return both | `no metrics served` / `no access: pods.metrics.k8s.io` for the affected namespaces only; the services screen unaffected; recovery on its own |
| R-5 | API throttling: a burst of requests from a second client (loop of `kubectl get`) while Huginn runs | usage backs off (the `Usage` backoff in refresh ticks), no error storm on screen |
| R-6 | Key mashing: 2 000 random keys at 100/s on every screen | no panic; terminal restored on exit (T-5) |
| R-7 | Karapace down, broker killed and restored during follow (M12, M14) | documented messages; recovery |
| R-8 | `SIGKILL` of Huginn, then restart | the terminal needs `reset` (expected, documented) and the cluster shows no leftover object (Huginn creates none) |
| R-9 | Clock skew between host and cluster (set the WSL clock 5 minutes off) | relative ages and the `stale since` markers stay sensible; ranges use the documented clock |

## 8. Pass and fail

- **P0 failure** (wrong data, a crash, a corrupted terminal, a write verb, a secret leaked, a data race, a leak shown by N-4/N-5) **fails the session** and is fixed before the milestone is called done.
- **P1** failures are reported with a decision for the owner. **P2** are listed.
- A case that could not run is reported *not exercised* with the reason, never marked passed. A ledger row with neither a test id nor a reason fails the gate (§1).
- Findings that change a default (`usage.refresh`, thresholds, buffer sizes) are decisions for the owner, not edited during the session.
- D-075 moves from "accepted, pending the real-cluster QA" to "accepted" only when U-1…U-20 and N-3, N-7, N-8, N-9 pass or are explicitly waived by the owner.

## 9. Execution plan and lifecycle

Phases, in this order. Times are the owner's to set (§11, D-1); none is estimated here.

| Phase | What | Cost while running |
|---|---|---|
| 0 | Local gate, free: `go test ./...`, `-race` on core/adapters/tui, `golangci-lint run`, benchmarks, goldens; build the four binaries; write the inventory, ledger, sampler and driver scripts | none |
| 1 | Cluster up; list clusters; kubeconfig and RBAC boundary proven; Autopilot facts of §2 confirmed; proxy path validated (N-7) | cluster |
| 2 | Fixtures applied; `qa-scale` sized to the owner's cap | cluster |
| 3 | **Functional pass** with the `cover` binary: C, S, U, L, K, P, T (the human look T-8 here) | cluster |
| 4 | **Race pass** (R-1), same cases, quicker subset allowed if the owner agrees | cluster |
| 5 | **Performance and resources** with the `plain` binary: N-1…N-15, A/B with `baseline` | cluster |
| 6 | **Chaos** R-2…R-9 | cluster |
| 7 | **Soak** N-5, alone on the cluster; the report is written meanwhile | cluster |
| 8 | Coverage merge and the zero-coverage list; ledger completed; report | none |
| 9 | **Teardown, unconditional**: delete the cluster, the Artifact Registry repo and the service accounts created; list clusters in both regions; report the Console credit read by the owner | stops |

**Order rationale.** Functional first (it finds defects that make later numbers meaningless); performance before chaos (chaos leaves the cluster noisy); soak last and alone (any other client perturbs its numbers); teardown even if a phase is abandoned.

**Deliverables.** `deploy/gke-qa/M22-QA-REPORT.md` (verdict, the ledger summary, the coverage figures and the zero-coverage list, each N-case with numbers and the run count, every failure and every *not exercised* with its reason), `deploy/gke-qa/coverage.tsv`, raw evidence under `deploy/gke-qa/m22/` (captures, proxy log, samples, PTY recordings, pprof files), and D-075's status updated. Scripts written for the session go in `deploy/gke-qa/` beside the existing ones.

## 10. Risks and open questions

- **Cost leak.** M15's report found a leak from an earlier session. Teardown is a checklist item with a second `list` in both regions, and the owner reads the Console after.
- **API-load evidence may not be obtainable** on a managed control plane (no `/metrics`, TLS interception, audit logs billable). Step 1 of phase 1 decides which of the N-7 sources is used; the report states which.
- **`SharedCatalog` is shared state.** Its regression risk is on the services and logs screens, not on the usage screen; N-9 and N-4 target it.
- **Instrumented builds disturb timing.** Numbers come only from `plain`.
- **Coverage data is lost on `SIGKILL`/panic**; chaos is run with `plain`, not `cover`.
- **No heap profile.** A suspected leak shows in N-4/N-5 as RSS or threads/fds growth but cannot be attributed to an allocation site without the hook of D-8.
- **Human look is subjective.** T-8 is recorded as observation; the objective measure is N-6.
- **One terminal stack.** Findings do not transfer to other emulators or operating systems.
- **`ResourceQuota`, nodes, cluster-wide views** are out of M21's scope (D-075) and so out of this plan.

## 11. Decisions needed from the owner before execution

| # | Question | Default if unanswered |
|---|---|---|
| D-1 | Budget cap in EUR and the time box per phase | none: execution does not start |
| D-2 | Pod cap for `qa-scale` (namespaces × services) | 20 namespaces × 3 services, to be confirmed against the balance |
| D-3 | N-7 source: logging proxy, audit logs (if their cost is acceptable), or RBAC proof only | proxy first, then RBAC proof only |
| D-4 | Is it acceptable to run the functional pass on a `-cover` build and one pass on a `-race` build? | yes |
| D-5 | The *proposed* tolerances (10 % A/B, 15 % cycle drift, 150 ms latency, 1 % idle CPU) | as written, reported as proposals |
| D-6 | Which terminals and OS are in scope beyond WSL2 + Windows Terminal | WSL2 + Windows Terminal only |
| D-7 | Is Kafka in scope this time (broker and Karapace add a running cost) | in scope, as the TUI has Kafka screens |
| D-8 | Add a heap-profile hook (`HUGINN_MEMPROFILE`) before the session? It is a code change | no: RSS, threads and fds only |
| D-9 | Policy on a P0 found mid-session: stop and fix, or log it and finish the pass | log and finish the phase, fix before the next |

## 12. Done when (this design)

- The owner has answered §11 or accepted its defaults.
- The inventory tool, the ledger and the four-binary build are specified well enough to write in phase 0 (this file) and nothing in §4–§7 is left as "to be decided".
- No cluster has been created and no cost incurred by writing it.
