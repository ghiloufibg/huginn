# TUI end-to-end QA session — design (minikube)

**Status: design only.** This is a prompt/spec for a future session; nothing
in this file has been executed. It defines an interactive, end-user-style QA
pass over the whole TUI on a real (local) cluster, distinct from
[`e2e.sh`](e2e.sh) (a fast, scripted regression check of a handful of
screens). This session is exhaustive: every screen, every bound key, every
`ui.yaml`/`containers.yaml`/`services.yaml` variant that changes TUI
behavior, plus responsiveness, smoothness, flakiness and resource
observations that a scripted `grep`-based check does not surface.

Hand this file to whoever (human or agent) runs the session. They execute it
top to bottom, filling in `## Findings` as they go, and produce the report
described in [§6](#6-bug--performance-report-template).

## 0. Assumptions and prerequisites

- POSIX shell with `tmux`, `docker`, `kubectl`, `minikube` installed (WSL2 on
  Windows, or Linux/macOS directly — the same shape of environment as CI's
  `lab` job). Native `cmd.exe`/PowerShell cannot run [`up.sh`](up.sh) or
  [`e2e.sh`](e2e.sh).
- `make build` succeeded; `bin/huginn` exists.
- The huginn process itself runs on whatever host runs the shell above (it
  only needs a kubeconfig reaching minikube's API server); resource sampling
  commands differ if that host is Windows — see the platform note in
  [§2.2](#22-resource-utilization).
- No GKE credentials needed. `sops`-encrypted namespaces, `gke-gcloud-auth-plugin`
  flows and cluster-wide RBAC are **out of scope** (not reproducible with
  minikube) — record them as known gaps in the final report, not as bugs.

### 0.1 Cluster and fixtures

```sh
deploy/lab/up.sh minikube
kubectl --context minikube apply -f deploy/lab/extra.yaml
kubectl --context minikube apply -f deploy/lab/qa-fixtures.yaml   # new, see below
```

`workloads.yaml` and `extra.yaml` already cover most states (see
[`README.md`](README.md) and the header comment of `extra.yaml`). Two states
documented in [`docs/CONFIG.md`](../../docs/CONFIG.md) (`standalone_pods`)
have no fixture yet and must be added as `deploy/lab/qa-fixtures.yaml`
before this session runs:

| Fixture to add | Shows | How |
|---|---|---|
| A Job run by hand (not a CronJob) | `migrate (Job)` standalone row | `kubectl -n app-rec create job migrate-once --image=alpine:3.20 -- sh -c 'echo migrating; sleep 5'` |
| A ReplicaSet with no owning Deployment | a standalone row grouped by an owner kind Huginn does not special-case (`domain.Workload.Owns`, D-038) | apply a bare `ReplicaSet` directly (never created by a Deployment, and without the resolver's label keys or a `pod-template-hash` label — see `qa-fixtures.yaml`'s note on why orphaning a *Deployment*'s ReplicaSet does not exercise this path: `ownerOf()` in `internal/adapters/driven/kubernetes/convert.go` infers `Deployment: <name>` from the pod's own `pod-template-hash` label regardless of whether the Deployment object still exists) |
| A Deployment created *after* the session starts | a repository appearing live on the services screen without restart | apply mid-session, see Phase J |

Also record whether `metrics-server` is enabled (`minikube addons enable
metrics-server`); it is optional and only used to sanity-check that fixture
load itself isn't skewing host resource numbers, never as a stand-in for
sampling the huginn process.

## 1. Non-goals

- Re-deriving what `e2e.sh` already asserts mechanically — this session
  *includes* everything `e2e.sh` checks (services load, sidecars hidden by
  default and shown with `A`, standalone pod, previous instance, rollout
  seen live, restricted identity, no-TTY exit) but through manual/observed
  interaction, watching for jank and resource cost along the way, not just
  pass/fail text matching.
- Testing `examples/config-node` (pino) and `examples/config-nginx`
  decoding correctness — already covered by CI per `README.md`; this
  session only touches decoding indirectly through what's already in the
  lab config (`formats/10-nginx.yaml` via `storefront-web`,
  `formats/20-spring-json.yaml` via the JSON emitters).
- Auth flows requiring GKE (`unauthorized`, `secrets unavailable`).

## 2. Observation methodology

### 2.1 Responsiveness and smoothness

Reference baseline: [`docs/DECISIONS.md`](../../docs/DECISIONS.md) D-031
(frame budget) — 0.6 ms/frame at 50 000 lines, 2.5 ms/frame at 500
repositories, 30 fps cap. There is no automated way to measure perceived
input-to-redraw latency from outside the process, so:

- Drive the session through `tmux` (as `e2e.sh` does:
  `tmux send-keys -t qa <key>`), and after each keystroke poll
  `tmux capture-pane -p -t qa` in a tight loop, timestamping the first
  poll where the expected change appears. Record outliers (anything
  subjectively "held" for more than a couple of frames, i.e. > ~150 ms)
  rather than every sample — this is about catching regressions, not
  building a benchmark suite.
- Watch specifically for: partial/torn redraws, flicker on toggles
  (`A`, `c`, `z`, `R`, theme change), stutter while `bulk-emitter` streams
  at its default rate, and any visible delay opening `zoom` on the
  1.1 MB line from `edge-lines`.
- Resize the terminal (`tmux resize-window -t qa <cols> <rows>`) across a
  matrix while both screens are open: `20x5` (must not crash — extreme
  minimum), `80x24`, `120x30`, `200x50` (the preview-pane and 8-line
  thresholds from `README.md`'s services screen section), `300x60`. Confirm
  the WHY column drops first, the preview pane moves from right (≥200 cols)
  to below (≥8 free lines) to hidden, and the key bar/help screen reflow
  without leftover garbage columns.
- Run once with `HUGINN_CPUPROFILE=qa.pprof` for the busiest phase (K,
  below) and inspect it afterwards with `go tool pprof -top qa.pprof` for
  any function dominating outside rendering/decoding as expected.

### 2.2 Resource utilization

Baseline table (D-032, 200×50 terminal, demo streaming 400 lines/s over 4
containers, 15 min window, 50 000-line buffer): idle services screen
0.4–0.6% CPU; steady streaming ≈13% CPU, 280 MB RSS; history load brief
35–45% CPU. Use these as the "does this look right" reference, not a strict
gate — the lab's real cluster and dummy workloads won't reproduce the demo's
exact rates.

- **Linux/macOS/WSL2 host:** sample every 30 s during each phase:
  `ps -o pid,%cpu,%mem,rss,vsz -p $(pgrep -f 'bin/huginn')`, or
  `pidstat -r -u 30 -p $(pgrep -f 'bin/huginn')` for a running log.
- **Windows host (if the huginn process itself runs outside WSL):**
  `Get-Process huginn | Select-Object Id,CPU,WorkingSet64,PrivateMemorySize64`
  sampled the same way (`! ...` from the session to run PowerShell inline),
  or Task Manager for a quick visual check.
- Record RSS trend over the soak test (Phase K) specifically — a
  monotonically rising RSS that doesn't plateau after the buffer fills is a
  leak candidate (D-032 says freed memory is returned on logs-screen close
  and via a background `debug.FreeOSMemory`; verify RSS drops after
  `esc` back to services and staying there ~1 min).

### 2.3 Flakiness injection

The docker driver's minikube node is a container named `minikube`:

- **Total outage:** `docker pause minikube` … wait 15–30 s … `docker unpause minikube`.
  Watch the services and logs screens: expect a resync/reconnect indication
  (the spinner from D-031, "the session's watches" resyncing), not a crash,
  not a silent freeze, and no duplicated/missing lines once it recovers.
- **Single pod disruption:** `kubectl -n app-rec delete pod <payment-service pod>`
  while its logs are open and followed; confirm the pod strip updates,
  the new pod's lines arrive, and no stale "waiting" state lingers.
- **Node-level latency/loss (optional, best-effort):**
  `docker exec minikube tc qdisc add dev eth0 root netem delay 300ms loss 5%`,
  observe the logs screen's live rate and status bar under degraded
  network, then `docker exec minikube tc qdisc del dev eth0 root`. Skip if
  `tc` isn't available in the node image — note it as untested rather than
  forcing it.
- **Repeat, don't trust once:** anything that looks flaky (an intermittent
  extra blank line, a status flicker, a reconnect that sometimes takes one
  resync and sometimes three) gets repeated at least 3 times before being
  logged as a bug, with the observed variability noted.

## 3. Screen-by-screen coverage

Every row must be exercised at least once; "expected" is summarized from
`README.md` and `internal/adapters/driving/tui/actions.go`/`keys.go` — treat
those files as the source of truth if this table and the running code
disagree (and log the disagreement as a docs bug either way).

### 3.1 Services screen

| Key(s) | Expected | Also watch |
|---|---|---|
| `j`/`k`/`↑`/`↓`/`pgup`/`pgdn`/`g`/`G`, mouse wheel | move selection | scroll smoothness with ~15+ rows (workloads + extra + qa-fixtures) |
| `enter` | opens the repository's logs | transition latency |
| `/` `ctrl+f` | filter by name, `enter` keeps, `esc` clears | filtering while a status changes underneath |
| `s` | cycle sort: status, name, restarts, age | re-sort doesn't jump the cursor to an unrelated row |
| `p` | preview pane on/off | layout thresholds, §2.1 |
| `r` | resync watches | spinner appears/disappears correctly |
| `ctrl+e` | opens env picker | see §3.6 |
| `esc` | back (top-level: confirm it does nothing harmful, not silently mapped to quit) | |
| `q` `ctrl+c` | quit | clean terminal restore (no leftover alt-screen, no dropped cursor) |

Status/grouping to visually confirm present and correctly bucketed:
Healthy (`payment-service`, `payment-worker`), FAILING (`catalog-indexer`
CrashLoopBackOff, `document-renderer` ImagePullBackOff), DEGRADED/PENDING
(`order-orchestrator` OOMKilled, `email-dispatcher` Pending/Unschedulable,
`never-ready`), ROLLING (trigger via `kubectl rollout restart`), two
workloads under one repository (`payment-service`/`payment-worker` sharing
`part-of`), a StatefulSet in another env (`ledger-writer`, only visible
under `dev`), a DaemonSet (`node-agent`), a CronJob (`nightly-export`, watch
a completed run age out per its history limits), a scaled-to-zero
Deployment (`scaled-zero`), an unlabelled Deployment (`unlabelled` — confirm
the fallback naming rule), an init-container failure distinct from a crash
loop (`init-fail`), a slow-stopping pod mid-`SIGTERM` (`slow-stop`, trigger
a rollout/delete and watch the WHY column during the grace period), the
standalone rows (`debug-shell (Pod)`, `migrate (Job)`, the orphaned
ReplicaSet fixture), and the 5-second reverse-video status-change highlight
firing on a real transition (not on first snapshot of an environment, per
D-031).

### 3.2 Logs screen — navigation & inspection

| Key(s) | Expected | Also watch |
|---|---|---|
| `j`/`k`/`pgup`/`pgdn`/`g`/`G`, mouse wheel | move; `G` returns to live tail | scrolling smoothness at `bulk-emitter` volumes |
| `>` `<` | next/previous ERROR | wraps or stops sensibly at buffer ends |
| `enter` | zoom the entry | see zoomScreen table below |
| `f` | follow on/off | |
| `space` | pause/resume, buffers up to buffer size, counts drops | pause during `bulk-emitter`, confirm drop count is accurate and shown |
| `P` | previous instance of restarted containers, again for current | only meaningful on `catalog-indexer`/`init-fail`; confirm no-op message elsewhere |

### 3.3 Logs screen — time & stream

| Key(s) | Expected |
|---|---|
| `t` / `T` | next window / window picker (§3.7) |
| `1`…`7` / `0` and AZERTY `& é " ' ( - è` / `à` | 15m/30m/40m/45m/1h/1d/2d / tail — test at least the QWERTY digits and two AZERTY equivalents via `tmux send-keys -l` with the literal character |
| `tab` | cycle pod scope (all → each pod) |
| `S` | pod selector overlay (§3.7) |
| `A` | all containers vs application-only; confirm sidecar lines (`istio-proxy`'s `via_upstream`) appear/disappear and the container name shows (`9d5px/istio-proxy`) |

### 3.4 Logs screen — filter & search

| Key(s) | Expected |
|---|---|
| `/` `ctrl+f` | enter filter prompt |
| (typing) | live filtering as you type |
| `ctrl+r` | regex on/off — test both a valid and a deliberately invalid regex (must not crash, must report the problem) |
| `ctrl+x` | filter (hide) vs highlight (keep all) toggle **inside the prompt** |
| `!` prefix | inverts; `\!` types a literal `!` |
| `ctrl+a` | stacks another filter (AND) |
| `enter` / `esc` | keep / cancel |
| `x` | filter/highlight toggle **after** commit |
| `n` `N` | next/previous match, wrap-around at buffer ends |
| `X` | context lines: 0, 1, 3, 5 |
| `l` | level picker (§3.7); `e` `w` `a` shortcuts (errors only / warn+error / all) |

### 3.5 Logs screen — display & layout

| Key(s) | Expected |
|---|---|
| `c` | hides next column in order (time, level, thread, class), cycles back |
| `ctrl+t` | time format: local, UTC, relative |
| `R` | resets columns/pod id/time format/pan/wrap; **keeps** filters, window, pods — verify the "keeps" part explicitly |
| `o` | order (newest/oldest first) |
| `I` | pod id: short, full, hidden |
| `W` | wrap; interacts with pan keys below |
| `C` | columns picker sub-keys: `t` `f` `p` `l` `h` `c` `z` `r` (§3.7) |
| `z` | focus layout (hide pod/thread/class), toggles back |
| `←` `→` `H` `L` | pan (only meaningful when not wrapped) |
| `F` | fullscreen |
| `esc` | precedence: exit fullscreen → clear last filter → back, one level per press — test this chain explicitly, it's easy to get wrong |
| `F2` `ctrl+k` | key bar: compact/full/hidden |

Use `edge-lines` for this phase specifically: a 40 000-char field, ANSI
color/bold codes inside a plain line (should render as literal text, not
control the terminal — see Phase L), unicode (`é à ü 日本語 🚀`), a line
missing its timestamp field, an unparseable timestamp, a truncated/invalid
JSON line, tab-separated text, and a >1 MB line. Confirm each renders
without crashing, without hanging, and wraps/truncates sensibly.

### 3.6 zoomScreen

| Key(s) | Expected |
|---|---|
| `enter` (from logs) | opens the entry |
| `J` `K` | next/previous entry, stop sensibly at buffer ends |
| `p` | raw JSON view |
| `enter` (in zoom) | metadata, if the README's claim of a second `enter` level holds — confirm and log a docs bug if it doesn't |
| `esc` | back to logs |

### 3.7 Pickers/overlays

| Overlay | Open with | Behavior to confirm |
|---|---|---|
| Help | `?` `F1` on any screen | per-screen key list differs (services vs logs vs zoom vs pod selector); `/` searches within help; empty search result shows the "no key matches" message; scrolling with `j`/`k`/pgup/pgdn |
| Env picker | `ctrl+e` | lists `rec`, `dev`, `restricted`; `enter` switches; `esc` cancels; switching while a logs screen is open behaves sensibly (back to services, or reopens) |
| Window picker | `T` | lists configured windows, cursor tracks the current one, `enter` applies |
| Level picker | `l` | multi-select with `space`; `e`/`w`/`a` shortcuts; `enter` applies, `esc`/`l` cancels |
| Columns picker | `C` | `t` `f` `p` `l` `h` `c` toggle individual columns, `z` focus layout, `r` reset |
| Pod selector | `S` | fullscreen; the two lists (pods, containers) combine per D-038 ("one container of every pod" or "one pod's container"); picking a sidecar container while in `app` mode switches to all containers automatically — confirm this specific rule |

### 3.8 Reserved/unimplemented keys

`v` (`view_trace`), `d` (`diagnostics`), `E` (`error_groups`), `m` (`mark`),
`ctrl+y` (`copy`), `ctrl+s` (`save`), `B` (`bug_report`) are declared in
`keys.go`/`Action` but have no handler and no `actionInfo` entry — they are
reserved, not yet wired up. Press each on the logs screen and confirm it is
a safe no-op (no crash, no accidental fallthrough to an unrelated action, no
visible side effect). If one of them *does* something, that's a bug (dead
code silently active) — log it.

## 4. Config-driven variations

Run a short pass of §3.2–3.5's core keys under each variant, not the full
matrix — the goal is confirming the variant takes effect, not re-testing
every key:

- **Theme** (`--theme` or `ui.yaml: theme`): `light`, `accessible`,
  `classic`, `none`, plus `NO_COLOR=1` (must force `none` regardless of
  `--theme`). Confirm status colors, level colors and the reverse-video
  status-change highlight remain legible/absent-of-color as appropriate in
  each.
- **Key bar** (`ui.yaml: key_bar`): `compact`, `full`, `hidden`, and cycling
  through all three with `F2`/`ctrl+k` at runtime.
- **Keymap override** (`ui.yaml: keymap`): rebind one action (e.g.
  `follow: [f, ctrl+l]`) in a scratch copy of `deploy/lab/config`, confirm
  the old key still works (multi-key binding) and the new key works, and
  that the help screen/key bar reflect the remap (README's "every key can
  be remapped" claim).
- **`containers.yaml` `default_mode: all`**: logs open with sidecars shown
  by default; `A` now returns to application-only.
- **`services.yaml` `standalone_pods: false`**: `debug-shell`/`migrate` rows
  disappear from the services screen.
- **`--containers app|all`** flag: overrides `containers.yaml` at startup.

## 5. Phased execution order

| Phase | Focus | Depends on |
|---|---|---|
| A | CLI flags & startup: every flag in `README.md`'s Usage block, every env var, `--version`, malformed config folder (exit 2, `file:line:column`), no-TTY exit (exit 2, "interactive terminal") | §0 |
| B | Services screen, all states and keys (§3.1) | A |
| C | Logs screen navigation & inspection (§3.2), zoomScreen (§3.6) | B |
| D | Logs screen time/stream controls (§3.3) | C |
| E | Logs screen filter/search (§3.4) | C |
| F | Logs screen display/layout (§3.5), including the resize matrix (§2.1) | C |
| G | Pod/container scope specifics (`A`, `S`, D-038 rule) | C |
| H | Pickers/overlays (§3.7), help search | B, C |
| I | Config-driven variations (§4) | A–H done once on defaults |
| J | Resilience: flakiness injection (§2.3), rollout while paused/filtered/non-tail window, scale 2→0→2, delete a pod, delete a whole Deployment, apply a brand-new Deployment mid-session and confirm it appears without restart | B, C |
| K | Performance/resource soak: open `bulk-emitter` (100 events/s × 2 replicas) at default window, run ≥15 min, sample resources every 30 s (§2.2), pause/resume mid-soak, then `esc` back and confirm RSS drops | D, F |
| L | Security/robustness: `esc-inject` workload — confirm ANSI/OSC escapes (screen clear, title change, fake clipboard write, cursor jumps, fake hyperlink, backspace tricks) render as inert text, never affect the surrounding terminal or persist after scrolling past | C |
| M | Reserved keys sanity (§3.8) | C |
| N | `--demo` pass: no cluster needed; open `--demo` and `<env> --demo`; watch for the scripted payment-service rollout ~90 s after start; confirm it's smooth and doesn't spike resources unexpectedly | (standalone, can run anytime) |
| O | Report compilation | all |

## 6. Bug & performance report template

One entry per finding, appended to the session's report:

```
### [severity] short title

- Screen/feature: 
- Steps to reproduce: 
- Expected (cite README.md line / docs/DECISIONS.md Dxxx / docs/CONFIG.md section): 
- Actual: 
- Repro reliability: always | intermittent (n/n attempts) | once, unreproduced
- Evidence: tmux capture-pane snippet / screenshot / ps or Get-Process sample / pprof top / diagnostic log excerpt
- Suspected area (optional, file:line): 
```

Severity scale: **crash/data-loss** (process exits, terminal left broken,
log lines silently lost or corrupted) > **functional break** (a documented
key/behavior doesn't work) > **performance/resource** (exceeds D-031/D-032
baselines without explanation) > **visual/cosmetic** (flicker, misaligned
layout, wrong color) > **minor/nit** (docs mismatch, awkward wording).

Close the report with: total keys/screens exercised vs. the checklist in
§3, resource numbers vs. the D-032 baseline table, and the list of
out-of-scope items from §1/§0 that were not covered.
