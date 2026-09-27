# TUI end-to-end QA report

Run against `deploy/lab/QA-SESSION.md` on 2026-09-27, minikube driver (Docker
Desktop backend via WSL2 on Windows), binary built at commit `5814a66`
(`go1.27.1`, `CGO_ENABLED=0`). Host: WSL2 Ubuntu, 200×50 tmux session unless
noted.

## Scoping notes (read before the findings)

- **Phase K (soak)**: ran a shortened ~4.5 min sample instead of the full 15
  min window, given the interactive session's time budget; resource
  sampling pattern and RSS-drop-on-close were still verified. Flagged
  explicitly per finding, not silently substituted.
- **Phase J/§2.3 `tc` network fault injection**: best-effort; recorded as
  untested if `tc` is unavailable in the minikube node image, per §2.3's own
  allowance. Attempted on this host: the `tc` binary is present in the
  `minikube` node container, but `tc qdisc add dev eth0 root netem delay
  300ms loss 5%` fails with `Error: Specified qdisc kind is unknown` —
  the `sch_netem` kernel module is not present in this host's kernel
  (`modprobe sch_netem` → `FATAL: Module sch_netem not found in
  directory /lib/modules/5.15.167.4-microsoft-standard-WSL2`, the WSL2
  kernel). This is a host/environment limitation (WSL2's kernel doesn't
  ship `netem`), not something fixable from inside the container.
  **Untested** — latency/packet-loss resilience was not exercised in
  this run.
- **`deploy/lab/qa-fixtures.yaml`** was added (Job `migrate-once` created
  live, `orphan-source` Deployment added then orphaned live) — see the Job
  finding below regarding whether the orphaned-ReplicaSet fixture as
  specified actually exercises the intended code path.
- Environment: `deploy/lab/config` is written for `kind-huginn`; this
  session used minikube, so a scratch copy of the config with
  `environments.yaml`'s context changed to `minikube` was used (per
  README's "with minikube, replace kind-huginn by minikube").
- The WSL2 tmux server was lost partway through Phase J/I (session and
  all panes gone, `docker`/`minikube` containers unaffected and still
  running) — an environment artifact of the WSL2 host, not a huginn
  issue. The tmux session and huginn were restarted against the same
  live cluster; no test state depended on the lost pane beyond what
  had already been captured into this report.

## Fix pass (2026-09-27)

All four items below were investigated against the code and docs and
resolved. One turned out not to be a bug at all on closer reading of
`docs/DECISIONS.md`; the other three were genuine docs/test-plan gaps,
now fixed. No behavior-changing code was modified — see the
"not a bug" writeup for why.

- **`f` follow-toggle "full reopen"**: reclassified, not a bug. See below.
- **No-TTY exit code doc mismatch**: fixed in `deploy/lab/QA-SESSION.md`
  §5 Phase A row (`exit 1` → `exit 2`, matching the actual behavior and
  README.md:59).
- **Orphaned-Deployment fixture not producing a WITHOUT-REPO row**: fixed.
  `deploy/lab/qa-fixtures.yaml` now also applies a bare `ReplicaSet`
  (`qa-standalone-rs`, never owned by a Deployment, no resolver label
  keys) which cannot go through the `pod-template-hash`-based
  Deployment-name inference in `ownerOf()`
  (`internal/adapters/driven/kubernetes/convert.go:105-123`) the way an
  orphaned Deployment's ReplicaSet always does. Re-verified live against
  the lab cluster: it now renders as `qa-standalone-rs (ReplicaSet)`
  under **WITHOUT REPO**, matching `docs/CONFIG.md`'s documented
  behavior. `deploy/lab/QA-SESSION.md`'s §0.1 fixture table row was
  updated to describe the corrected recipe and explain why the
  Deployment-orphaning approach doesn't exercise this path (kept as a
  separate, still-useful fixture for Phase J resilience testing).
- **`standalone_pods` docs wording**: fixed. `docs/CONFIG.md:178` now
  reads "**When shown this way**, they go through the rules above..."
  and adds an explicit sentence that turning `standalone_pods` off means
  a labelled orphaned pod does not join its repository either, since it
  is never turned into a row to begin with.

### `f` follow-toggle: not a bug — confirmed working as designed (D-025)

Re-reading `docs/DECISIONS.md` D-025 (missed during the original QA
pass) settles this: *"Follow is on by default (like kl and `kubectl
logs -f` habits); `f` turns it off and reloads the window without
following (`STOPPED`)."* A full reconnect and history re-fetch on every
`f` press — in both directions — is the documented, intended design,
not an oversight; it mirrors `kubectl logs -f`'s own reload-on-toggle
habit, and a dedicated `STOPPED` status-bar chip (covered by
`tui_test.go:378`) exists precisely for the follow-off state. The
transient "0/0 lines" / widened-column display during the ~1-2s reload
is the same transient any reopen produces (window change, previous-
instance toggle, `A` all-containers toggle, manual `r` refresh all
share the same `l.open` path and reset `l.formats` too) — it is not
specific to, or worse for, the follow toggle. No code change made.

## Findings

### [minor/nit] QA-SESSION.md's no-TTY exit code disagrees with README.md — RESOLVED: doc fixed

- Screen/feature: Phase A, startup
- Steps to reproduce: `huginn --config <cfg> rec </dev/null`
- Expected (cite): QA-SESSION.md §5 Phase A says "no-TTY exit (exit 1,
  \"interactive terminal\")"; README.md:59 says exit code 2 covers "config
  folder missing or invalid... or no interactive terminal".
- Actual: exit code is `2` with message "needs an interactive terminal: run
  it in a terminal, not through a pipe or a redirection" — matches
  README.md, not QA-SESSION.md.
- Repro reliability: always
- Evidence: `EXIT=2` captured directly.
- Suspected area: deploy/lab/QA-SESSION.md §5 Phase A row (docs typo, not a
  code bug).

### [minor/nit] The qa-fixtures.yaml orphaned-Deployment recipe doesn't produce a standalone/WITHOUT-REPO row — RESOLVED: fixture and doc fixed

- Screen/feature: Services screen, standalone rows (§3.1), qa-fixtures §0.1
- Steps to reproduce: apply a labelled Deployment (`orphan-source`), then
  `kubectl delete deployment orphan-source --cascade=orphan`.
- Expected (cite QA-SESSION.md §0.1 fixture table): "a standalone row
  grouped by an owner kind Huginn does not special-case".
- Actual: the pod still shows as a normal HEALTHY repo row named
  `orphan-source`, not under "WITHOUT REPO". Two independent reasons: (1)
  `services.yaml`'s label resolution (`app.kubernetes.io/part-of:
  orphan-source`) resolves it to a repo regardless of ownership; (2) even
  disregarding labels, the orphaned ReplicaSet's name still matches the
  `<deployment>-<hash>` pattern D-034 uses to infer "owned by a Deployment
  named orphan-source" *from the pod's controller reference alone* — Huginn
  never checks whether that Deployment object still exists, so an
  `cascade=orphan` delete is invisible to it by design.
- Repro reliability: always
- Evidence: `kubectl -n app-rec get pod -l app=orphan-source -o
  jsonpath='{.items[0].metadata.ownerReferences}'` → owner kind
  `ReplicaSet`, name `orphan-source-5945b777c9` (hash-suffixed); services
  screen capture shows it under `HEALTHY`, not `WITHOUT REPO`.
- Suspected area: deploy/lab/QA-SESSION.md §0.1 fixture recipe (docs/test-plan
  gap, not a huginn code bug — D-034's naming-pattern inference is
  intentional and documented). To actually exercise "an owner kind Huginn
  does not special-case", the fixture would need a ReplicaSet whose name
  does *not* follow the pod-template-hash convention (e.g. created by
  `kubectl create replicaset` directly) or a non-standard controller kind.

### [performance/functional] `f` (follow toggle) tears down and re-opens the whole log session, unlike `space` (pause) — RESOLVED: not a bug, see "Fix pass" above (D-025)

- Screen/feature: Logs screen — time & stream (§3.3), navigation (§3.2)
- Steps to reproduce: open logs for a busy repo (`bulk-emitter`, 2 pods
  streaming ~300 lines/s), press `f` (follow off) then `f` again (follow
  on) in quick succession; capture the status bar immediately after the
  second press.
- Expected (cite README.md:94 "follow on/off"; contrast with `space`
  "pause/resume, buffers up to buffer size, counts drops" in
  QA-SESSION.md §3.2): a lightweight display toggle for auto-scroll,
  analogous to `space`'s non-destructive pause (which keeps the buffer
  and queues arrivals in `held`/`heldLate`, per `logs.go:584-604`, with no
  session reopen).
- Actual: `ActFollow`'s handler (`internal/adapters/driving/tui/logs.go:570-573`)
  unconditionally calls `l.open(m)`, which (`logs.go:149-168`) cancels the
  context, calls `l.buf.Reset()`, clears `l.rows`/`held`/`heldLate`/
  `heldLost`/`l.formats`, and issues a brand-new `sessions.Open` query
  (`Follow: l.follow`) against the backend — i.e. a full reconnect and
  history re-fetch, not a display-only toggle. Immediately after the
  second `f`, the status bar reliably (3/3 repeats) showed `0/0 lines` ·
  `buffer 0%` · `dropped 0` and a *different*, wider column set (`cols pod
  time status bytes level thread class msg` instead of the steady-state
  `cols pod time level thread class msg`) — the latter because
  `l.formats` (which drives automatic column narrowing) was also wiped
  and hadn't yet re-detected the stream's format. Buffer and columns
  both self-correct within roughly 1-2 s as history reloads.
- Repro reliability: always (3/3 attempts, ~0.3 s apart)
- Evidence: `tmux capture-pane` immediately after toggling `f` twice
  showed `0/0 lines  ·  buffer 0%  ·  dropped 0` with the wider column
  list, on all 3 attempts; a follow-up capture ~2 s later showed correct
  counts (`50000 lines kept... 40627 older skipped`) and the narrowed
  column list again.
- Suspected area: `internal/adapters/driving/tui/logs.go:570-573`
  (`ActFollow` case reuses `l.open`, the same full-reopen path as
  switching repos/pods/previous-instance, rather than a `space`-style
  in-place toggle). Functionally this means: (1) toggling follow briefly
  shows misleading buffer/column numbers, and (2) every follow toggle
  re-issues a Kubernetes log fetch for the whole configured window,
  which is unnecessary API load on a real cluster and, unlike `space`,
  provides no guarantee that lines buffered before the toggle survive
  the round-trip identically (they are re-fetched from the source, not
  retained in memory).

### [minor/nit] `standalone_pods: false` also hides labeled orphaned-controller pods, which docs/CONFIG.md's wording doesn't make obvious — RESOLVED: doc fixed

- Screen/feature: Services screen, `services.yaml` `standalone_pods` (§4
  config variations)
- Steps to reproduce: with the `orphan-source` fixture already orphaned
  (Deployment deleted with `--cascade=orphan`, labels resolve it to its
  own repo under default settings, per the finding above), set
  `standalone_pods: false` in `services.yaml` and restart huginn.
- Expected (cite docs/CONFIG.md:178): "...pods [whose] ReplicaSet[']s
  Deployment is gone... own rows, grouped by owner... go[es] through
  the rules above [labels], so a debug pod labelled like [an]
  application joins its repository." Read in isolation, this sentence
  could suggest a labeled orphaned pod keeps resolving to its labeled
  repo regardless of `standalone_pods`.
- Actual: with `standalone_pods: false`, `orphan-source` disappears
  from the services screen entirely (repos 18→17, confirmed stable
  after a manual `r` resync) rather than continuing to show as its own
  `HEALTHY` repo row.
- Repro reliability: always
- Evidence: `internal/core/app/catalog.go:298-300` —
  `if c.Standalone { ws = append(ws, domain.StandaloneWorkloads(ws,
  idx.all)...) }` — the synthetic "standalone workload" (which is what
  later gets checked against label rules) is only created at all when
  `standalone_pods` is true; when false, the pod is never even handed
  to the label resolver, so it can't "join its repository" by label
  either.
- Suspected area: docs/CONFIG.md:178 wording only (not a code bug) —
  the label-matching clause describes what happens to a standalone pod
  once synthesized (the default-true path), not a guarantee that
  persists when `standalone_pods` is turned off. Worth a doc tweak
  (e.g. "...when standalone pods are shown, they still go through the
  label rules above...") to avoid the reading above.

## Phase J — resilience (validated, no bugs found)

- **Single pod deletion** (`kubectl delete pod payment-service-...-2qrts
  --wait=false`, logs open & followed): pod strip correctly transitioned
  `terminating` → `terminated`, the replacement pod appeared marked
  `new`, and lines kept streaming without a stale "waiting" state.
  `2qrts terminated` stayed pinned in the strip indefinitely, even ~75 s
  after `kubectl get pods` confirmed it no longer exists in the API at
  all. This is intentional, not a bug: `internal/core/ports/usecases.go:63-64`
  documents `Terminated bool // marks a pod that disappeared; its lines
  are kept`, i.e. Huginn deliberately keeps a disappeared pod's row so
  its already-displayed log lines stay attributable to a real pod
  identity rather than being orphaned. (Worth knowing for a long-running
  session with heavy pod churn — e.g. frequent rolling restarts — the
  pod strip will grow unboundedly for the life of the log session, but
  this is a documented trade-off, not an oversight.)
- **Rollout while paused, filtered, on a non-tail window**: opened
  `payment-service` logs, switched off the default tail window, applied
  a text filter (`PaymentController`), then `space`-paused; triggered a
  rollout (`kubectl set env deployment/payment-service ROLLOUT_MARKER=...`).
  While paused, the status bar correctly showed `PAUSED +214` — the
  ~214 new lines produced by the rollout were counted and queued, never
  injected into the frozen view, and the active filter/window were left
  untouched. Resuming (`space`) integrated the new pods' lines
  seamlessly (new pods `4wl2k`/`xpxd6` appeared, filter stayed active
  throughout), and clearing the filter afterward showed the pod strip
  correctly reflecting the rollout (old pods `terminated`, new ones
  `Healthy ... new`).
- **Scale a Deployment 2→0→2** (`kubectl scale deployment payment-service
  --replicas=0` then `--replicas=2`): scaling to 0 correctly left all
  prior pods `terminated` (same retention rule as above); scaling back
  to 2 correctly showed the two new pods as `Healthy ... new` within
  ~8 s, no restart of huginn needed. While `payment-service` was at 0,
  the repo's pod strip still showed a `Healthy` pod (`6m99d`) — this
  belongs to a *different* Deployment, `payment-worker`, which
  `services.yaml`'s label-based resolution (`app.kubernetes.io/part-of:
  payment-service` shared by both Deployments' pod templates)
  intentionally groups into the same `payment-service` repo. Confirmed
  via `kubectl get deployment ... -o jsonpath='{...labels}'` on both
  Deployments — not a bug, just easy to misread at a glance.
- **Apply a brand-new Deployment mid-session / delete a whole
  Deployment**: applied a throwaway `qa-transient` Deployment (1
  replica) mid-session — it appeared live under SERVICES (repo count
  18→19) within ~6 s of `kubectl apply`, no restart needed. `kubectl
  delete deployment qa-transient` (normal cascade, not
  `--cascade=orphan`) removed all its pods, and the repo row itself
  disappeared from SERVICES (19→18) within ~30 s — unlike a disappeared
  *pod* (kept per the Terminated design above), a repo with zero
  current and zero terminated-but-remembered pods is correctly pruned
  entirely. This is consistent with, and further confirms, the
  already-logged "orphaned ReplicaSet" finding above: re-running
  `kubectl get deployment orphan-source` in this same test session
  still returned `NotFound` while its orphaned ReplicaSet/pod (from
  earlier in the session) was still `Running` and still rendered as a
  normal `HEALTHY` repo row — i.e. Huginn's repo pruning is driven by
  "does any pod (live or remembered-terminated) still exist for this
  repo," not by "does the owning Deployment object still exist."
- **Total outage** (`docker pause minikube` ~50 s, then `docker
  unpause`): services screen correctly showed `namespace app-rec:
  unreachable` in the status bar during the outage (no crash, no
  silent freeze), and recovered to `watching · synced <time>` within
  ~5 s of unpausing, with the top-level `kubernetes · watching ·
  synced` indicator confirming resync. Did not catch an explicit
  spinner frame between unreachable and synced at the 5 s sampling
  granularity used, but the end-to-end recovery (no crash, no stuck
  "unreachable") matches §2.3's expectation.
- **`tc netem` latency/loss injection**: untested on this host — see
  Scoping notes (WSL2 kernel lacks `sch_netem`).

## Phase K — performance/resource soak (abbreviated, validated, no bugs found)

Ran an abbreviated ~4.5 min soak against `bulk-emitter` (2 replicas)
instead of the full 15 min window (see Scoping notes), sampled every
20-30 s:

- `ps -o pid,%cpu,%mem,rss,vsz` samples while steadily streaming (buffer
  filled to 50000/50000 lines, LIVE mode, no filter): RSS flat at
  ~115 MB across 3 samples ~30 s apart (115364 → 115612 → 115612 KB) —
  no growth once the buffer plateaus, consistent with D-032's
  no-leak expectation. Note `ps`'s `%CPU` (≈29% throughout) is a
  lifetime average since process start, not instantaneous — see below.
- **Pause/resume mid-soak**: `space` correctly froze the view at
  `50000/50000` lines and counted queued arrivals (`PAUSED +575`);
  `space` again resumed cleanly with no visible gap or duplicate lines.
- **RSS drop on close**: `esc` back to services (with the log session
  fully closed) dropped RSS from ~115 MB to ~97 MB within the next
  sampling window and it stayed there (98832 → 97316 KB over ~40 s) —
  matches D-032's "freed memory is returned on logs-screen close"
  claim.
- **Idle CPU**: `ps %CPU` still read ~26-28% immediately after closing,
  which looked wrong at first — but that figure is `ps`'s lifetime
  average (cpu-time ÷ process-age), not live usage, and a few minutes
  of heavy streaming will keep that average elevated for a while after
  the fact. A `top -b -n1` snapshot (a true instantaneous sample) taken
  at the same moment read `0.0%`, matching D-032's 0.4-0.6% idle
  baseline. Not a bug — just a reminder that `ps %CPU` alone is
  misleading for pre/post comparisons on a long-lived process; `top
  -b -n1` or `pidstat` (unavailable on this host) is the correct tool
  for an instantaneous reading.
- **`HUGINN_CPUPROFILE` + `go tool pprof -top`**: top 15 functions by
  flat time are all rendering/decoding (`ultraviolet.RenderBuffer.SetCell`,
  `Cell.Equal`, `printString`, `ansi.stringWidth`, grapheme iteration,
  huginn's own `segmentInk` styling, etc.) plus `runtime.futex`/
  `syscall.Syscall6` — no unexpected function dominating outside the
  render/decode path.

## Phase L — security/robustness, `esc-inject` ANSI handling (validated, no bugs found)

Opened the `esc-inject` workload's logs, which emit lines containing raw
OSC/cursor/clear-screen/clipboard/hyperlink/backspace escape sequences
every 5 s. All observed as inert text, never affecting the surrounding
terminal:

- `clear screen` sequence: rendered literally as `beforeafter clear
  screen` — the "before"/"after" markers stayed concatenated on one
  line, proving the embedded clear-screen escape never actually cleared
  anything.
- `title change` (OSC), `clipboard write attempt` (OSC 52), `move
  cursor up` (CSI cursor movement), `click me hyperlink` (OSC 8): all
  rendered as their literal describing text, not interpreted.
- `backspace` trick: rendered as `backspaceXXXX` — the embedded
  backspace bytes did not erase the following `XXXX`, confirming
  backspace is treated as a literal/neutralized byte, not interpreted
  destructively.
- Scrolled past the injected lines (`j`/`k` repeatedly) and back: no
  leftover garbling, no persisted visual corruption at any point.
- SGR color passthrough (separate from the above): consistent with
  this session's earlier observation that legitimate SGR color codes in
  log output render as actual terminal colors (intentional passthrough,
  per D-037), while every other escape class above is neutralized to
  plain text — i.e. the neutralization is deliberately scoped to
  OSC/cursor/clear/backspace, not a blanket ANSI strip.

## Phase N — `--demo` pass (validated, no bugs found)

- `./bin/huginn --demo`: launches immediately into a rich scripted
  dataset (fake `gke_acme_europe-west1_main` context, `app-rec`
  namespace) with a realistic mix of failing/degraded/rolling/healthy
  repos. The scripted `payment-service` rollout (`v2.14.3→v2.14.4`)
  fired right around the documented ~90 s mark (ROLLING count went
  2→3, `payment-service` row showed `Progressing 5/4`), and completed
  cleanly back to `Healthy 4/4` roughly 25 s later (ROLLING 3→2).
- Resource usage stayed light and flat throughout, before/during/after
  the scripted rollout: RSS constant at 40376 KB, CPU 0.8-1.4% (`ps`
  samples) — no spike from the scripted rollout event.
- `./bin/huginn dev --demo` (env-qualified form): correctly launches
  into the demo's `DEV`/`app-dev` dataset variant instead of the
  default `REC`/`app-rec` one.

## Phase O — report compilation

### Coverage vs. §3 checklist

- §3.1 Services screen: sort/filter/preview toggle, resync, env switch,
  FAILING/DEGRADED-PENDING/HEALTHY/WITHOUT-REPO grouping, standalone
  rows (bare pod, Job, orphaned ReplicaSet) — all exercised.
- §3.2 Logs screen navigation: `j/k`, `g/G`, page up/down, `space`
  pause/resume, `enter`/`esc` in/out — exercised.
- §3.3 Time & stream: all AZERTY window-key aliases (`1`-`7`/`&`.../`è`,
  `0`/`à` tail), `tab` pod-scope cycling, `S` picker, `A` all-containers/
  sidecar toggle, D-038's sidecar-picks-all-containers rule — exercised.
- §3.4 Filter & search: `/`, live typing, `ctrl+r` regex (valid/invalid),
  `ctrl+x` mode toggle, `!`/`\!` invert, `ctrl+a` AND-stack, `x` toggle,
  `n`/`N` match nav, `X` context cycle, `l` level picker — exercised,
  including two initially-suspicious behaviors (level excluded from
  text search; highlight "match X of Y" semantics) traced to source and
  confirmed correct, not bugs.
- §3.5 Display & layout: `c`, `ctrl+t`, `o`, `I`, `R`, `W`+pan, `z`, `F`
  fullscreen, the full `esc` precedence chain, `F2`/`ctrl+k` key bar
  cycle — exercised.
- §3.6/§3.7 Pickers/overlays: help screen + search, columns picker `C`
  (`t f p l h c z r`) — exercised.
- §3.8 Reserved keys (`v`, `d`, `E`, `m`, `B`, `ctrl+y`, `ctrl+s`,
  `ctrl+p`): confirmed safe no-ops, no crashes.
- §4 Config variations: `theme` (light/classic/none/accessible +
  `NO_COLOR=1`), `key_bar` (compact/full/hidden), `keymap` override,
  `containers.yaml default_mode` (app/all), `--containers` flag
  (app/all), `services.yaml standalone_pods` (true/false) — all
  exercised; one doc-wording nit found (`standalone_pods` finding
  above), no functional bugs.
- §5 Phases A-N: all executed except the `tc netem` sub-item of Phase J
  (untested, host kernel limitation — see Scoping notes).

### Resource numbers vs. D-032 baseline

| Scenario | D-032 baseline (200×50, demo, 400 l/s, 4 containers, 15m window) | Observed this run |
|---|---|---|
| Idle services screen | 0.4-0.6% CPU | 0.0% (`top -b -n1`) |
| Steady streaming | ≈13% CPU, 280 MB RSS | ~115 MB RSS (`bulk-emitter`, 2 replicas, non-demo real cluster; not directly comparable — different workload/rate) |
| History load | 35-45% CPU brief | not isolated separately in this run |
| RSS after logs-screen close | drops, freed via `debug.FreeOSMemory` | ~115 MB → ~97 MB, confirmed |
| `--demo` idle/rollout | (n/a, demo-specific) | 40 MB RSS, 0.8-1.4% CPU, flat across the scripted rollout |

The lab's real cluster and dummy workloads don't reproduce the demo's
exact rates (as QA-SESSION.md §2.2 itself notes) — the CPU/RSS numbers
above are "does this look right" sanity checks, not strict regression
numbers against the baseline table.

### Out-of-scope items from §0/§1 not covered

- Full 15-minute Phase K soak (ran ~4.5 min instead).
- `tc netem` latency/packet-loss injection (host kernel lacks
  `sch_netem`; `docker pause`/`unpause` total-outage test was run
  instead and passed).
- Anything explicitly marked out-of-scope in QA-SESSION.md §0/§1 was
  not attempted (write/mutate operations beyond the lab's own fixture
  setup, non-Kubernetes backends, etc.).

### Transient states investigated and determined non-bugs

- Toast/reverse-video/history-reload transients (validated earlier in
  the session, prior to this report's Phase-by-phase write-up).
- Level excluded from text-filter search haystack (by design, per
  `filter.go`/`logentry.go` doc comments — a separate `l` level picker
  exists for that purpose).
- Highlight-mode "match X of Y" counter (X = current match position
  for `n`/`N` navigation, not "matches of total buffer lines" — X==Y
  simply means the cursor sits on the most recent match).
- Terminated pods staying pinned in the pod strip indefinitely after
  full deletion from the cluster (intentional retention, per
  `ports/usecases.go:63-64`).
- A different Deployment's pod (`payment-worker`) appearing inside
  `payment-service`'s pod strip while the latter was scaled to 0
  (intentional label-based repo grouping, both share
  `app.kubernetes.io/part-of: payment-service`).
- `standalone_pods: false` hiding a labeled orphaned pod entirely
  (intentional consequence of skipping standalone-workload synthesis
  when the flag is off — logged as a doc-wording nit, not a code bug).
- `ps %CPU` reading a high lifetime-average value right after a busy
  soak ended, despite true instantaneous CPU being ~0% (tooling
  artifact of `ps`, not a huginn issue).

### Summary

4 findings logged: 1 test-plan/fixture-recipe gap and 2 documentation
wording/consistency nits (all three fixed — see "Fix pass" above), and 1
initially-suspected functional/performance issue (`f` follow-toggle
causing a full session reopen) that turned out, on cross-checking
`docs/DECISIONS.md` D-025, to be documented, intentional behavior — not
a bug, no code change made. No crashes, hangs, data-loss, or
rendering-corruption bugs were found across the full A-N phase sweep.
The environment had one unrelated hiccup (WSL2 tmux server drop,
recovered without data loss) and one untested sub-item (`tc netem`,
host kernel limitation), both disclosed above rather than silently
worked around.

**Net result of the fix pass**: 0 code changes, 3 documentation/test-plan
fixes (`docs/CONFIG.md`, `deploy/lab/QA-SESSION.md`,
`deploy/lab/qa-fixtures.yaml`), 1 finding downgraded from "bug" to
"confirmed as designed" after reading the decision record that the
original QA pass had missed. The corrected `qa-standalone-rs` fixture
was re-verified live against the lab cluster (rendered correctly under
WITHOUT REPO) and then removed again to leave the cluster clean; the
fixture file itself is kept so a future session can reapply it.
