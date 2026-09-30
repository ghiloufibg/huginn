# M10 — Real GKE, round 2: full TUI coverage and NFRs under real load  (status: design)

**Goal:** a second real-GKE QA session, in the same `huginn-kube-tui` project as
[M5](M5-gke-qa.md), that closes two gaps M5 explicitly left open:

1. **Feature coverage.** M6–M9 (field transforms, `level_from`, field filters,
   trace view, select/copy/save, redaction, mouse selection) and the M10-era
   auto light/dark theme (D-051) were built *after* M5 and have **never been
   run against a real cluster** — `deploy/lab/QA-SESSION.md` does not mention
   any of them either (confirmed by grep), so most of them have not even had
   a full scripted lab pass, only unit/bench coverage (D-041–D-051,
   `perf-pass-M8.md`). This session's primary deliverable is a **coverage
   matrix** (§7) mapping every row of the README's two key tables to
   lab-only / M5-real-GKE / this-session, so "100% of the TUI" is an
   auditable claim, not an assertion.
2. **NFRs under real conditions.** M5's soak comparison (§"Real vs. lab:
   Phase K") explicitly could not be compared against D-032's lab baseline
   (280 MB RSS / 13% CPU) because M5's three services idle at ~1.2 lines/s —
   "a real network introduce visible instability" check, not a throughput
   one. This session adds one real, HTTP-driven, sustained-log-volume
   workload so performance, resource usage and responsiveness have a real
   apples-to-apples number, not just a synthetic `bulk-emitter` (lab) or an
   in-process benchmark (`perf-pass-M8.md`).

**Status: design only.** Nothing below has been provisioned — no `gcloud`,
`kubectl`, Kubernetes manifest or `huginn.yaml` change has been applied. Same
convention as [M5-gke-qa.md](M5-gke-qa.md): illustrative commands, for a
future execution session.

## 0. What this reuses, what is new

M5 already proved real GKE auth/RBAC, `namespace_from` via GCP KMS, and real
Spring Boot JSON logging end to end. Re-deriving any of that here would cost
credit and prove nothing new.

| Already covered — **not** repeated here | New — needs this session |
|---|---|
| Real GKE auth: `unauthorized`, `forbidden`, `secrets unavailable` (both causes), IAM/RBAC union behavior (M5, `QA-REPORT.md` "Real GKE auth") | M6 field transforms (`strip`/`extract`) and M7 `level_from` against **real** JSON logs, not just `FuzzTransform`/`FuzzDecoders` |
| `namespace_from` through `sops` + GCP KMS (M5) | M8 field filters from zoom (`tab`/`=`/`!`) and trace view (`v`) across **multiple real pods and services**, not the demo generator |
| Real Spring Boot structured logging, stack-trace folding, head window, sidecar hiding, real OOM (M5) | M9 select/copy/save (`V`/`m`/`y`/`Y`/`ctrl+s`), redaction (`ui.yaml redact`) and mouse selection against real log volume and a real terminal (OSC 52 clipboard, real `save.dir`) |
| Everything `deploy/lab/QA-SESSION.md` §3 already proved (screens, keys, pickers, resize matrix, reserved keys) | D-051 `auto` theme (`COLORFGBG` + OSC 11 background query) against the real terminal(s) driving this session — untested outside tmux 3.4 per D-051's own note |
| Cost governance, GKE Free Tier terms, Autopilot choice, teardown discipline (M5 §1, §2, §8) — **unchanged, cite not re-derive** | One real, sustained-log-volume workload for a real RSS/CPU/responsiveness number, comparable to D-032's lab baseline and `perf-pass-M8.md`'s in-process numbers |
| `payment-service`'s tracing (`micrometer-tracing-bridge-otel`, `management.tracing.sampling.probability=1.0`) — **already in the pom.xml/properties checked into `deploy/gke-qa/services/payment-service`**, not something this session adds | Actually exercising that already-real `trace_id`/`span_id` output through the trace view (`v`, M8.2) — M5 never opened it |

## 1. Cost governance

Unchanged from [M5-gke-qa.md §1](M5-gke-qa.md#1-cost-governance-read-this-first):
same `huginn-kube-tui` project, same $300/90-day free-trial hard cap (a
free-trial billing account cannot be charged past the credit; it auto-closes
instead), same GKE Free Tier cluster-management-fee waiver, same billing
budget (`gcloud billing budgets create`, 50/80/95% thresholds) already
created in M5 — **do not recreate it**, it is project-scoped, not
session-scoped. Re-verify current Autopilot/e2 pricing against the [GKE
pricing page](https://cloud.google.com/kubernetes-engine/pricing) before
provisioning, same caveat as M5 §1 (not independently confirmed there
either).

**What's new to size:** this session runs more pods than M5 (M5's 3 + one
`payment-service-loadgen` replica set + one synthetic fixture, §3) for a
similar few-hour, time-boxed window, then tears down — no new external app,
so this is a smaller delta over M5 than an earlier draft that included
PetClinic. Order-of-magnitude estimate: 5-6 small pods (0.1-0.5 vCPU, a few
hundred MiB each, the loadgen replica being the largest at perhaps
0.3-0.5 vCPU under its tuned rate) for 2-4 hours is still a small fraction
of a dollar under any published Autopilot rate — re-verify, don't assume,
same discipline as M5.

## 2. Cluster design

Unchanged from M5 §2: GKE Autopilot, single region, same
`gcloud container clusters create-auto huginn-qa` shape. **Reuse the same
cluster name and region as M5** if run soon after it (avoids re-learning a
new region's quota/latency characteristics); otherwise pick fresh, nothing
below is region-specific.

## 3. Workloads

Two groups: workloads that **prove M6-M9 features need real JSON**, and one
workload that **produces real sustained load** for the NFR pass. Kept to the
same "real JVM only where JVM behavior is under test, cheap tricks
everywhere else" discipline as M5 §3.

### 3.1 Reused from M5, unchanged

`payment-service`, `catalog-indexer`, `order-orchestrator` — redeployed as-is
(same images, same `qa-rec`/`qa-restricted` split) so the M5 soak numbers
remain a same-cluster-class baseline for §6's comparison table, and so `P`
(previous instance), OOM and crash-loop behavior stay available without
re-deriving them.

`payment-service`'s `pom.xml` and `application.properties` already carry
`micrometer-tracing-bridge-otel` and
`management.tracing.sampling.probability=1.0` (checked, not assumed — read
directly from `deploy/gke-qa/services/payment-service`) — no exporter, no
tracing backend, just real `trace_id`/`span_id` generation. **M5 never
opened the trace view against it.** This session is the first to actually
exercise trace view (`v`, M8.2) against a real, multi-line, same-trace-id
correlation across `payment-service`'s two replicas, with zero new code.

### 3.2 Dropped: an external app (PetClinic)

An earlier draft of this design proposed adding Spring PetClinic for
realism and load. **Dropped, per explicit instruction, in favor of
reusing only what's already in `deploy/gke-qa`** — lower risk (no new
app's Spring Boot version or container-build toolchain to verify), no new
Artifact Registry image, nothing outside this project's existing QA
assets. Its two jobs are absorbed elsewhere:

- **Verbose, real, nested JSON for `strip`/`extract` realism** (M6):
  `catalog-indexer` already produces a real, deep, multi-frame
  Hibernate/Hikari stack trace on every crash-loop restart (`QA-REPORT.md`
  "Real Spring Boot structured logging" — `HikariPool-1 - Starting...` →
  `SQL Error: 0, SQLState: 08001` → `Could not obtain connection to query
  metadata` → `Application run failed`), already checked into
  `deploy/gke-qa/services/catalog-indexer`. That nested `stack_trace`
  structure is a genuine, already-real candidate for a `strip` rule (e.g.
  dropping internal Hikari pool diagnostic frames); no new workload needed.
  `extract` (pulling a field from a `key=value` pairs-style string) has no
  natural source in any of the three real apps — kept in the synthetic
  fixture below, which never depended on PetClinic either.
- **A real HTTP target and a real sustained log rate for §6's NFR pass**:
  replaced by tuning `payment-service` itself — see §3.3.

### 3.3 One synthetic fixture for what no real app here naturally produces

Kept cheap and small (`busybox`/`alpine` script, per M5's and the lab's own
"no JVM where JVM behavior isn't under test" rule):

- A non-standard level field (e.g. `"severity": "warn"` instead of
  `level`/`levelname`) to exercise M7's `level_from` end to end against a
  real (if synthetic) log stream — PetClinic and the M5 trio both already
  use Spring Boot's own `level` field, so neither exercises the *remapping*
  path.
- One or two lines shaped like they carry a secret (a fake,
  obviously-synthetic email and a fake bearer-token-shaped string) so
  `ui.yaml redact` (D-050) has something real to redact in a copy/save and
  the QA session can confirm — as D-050 specifies — it never shows on
  screen, only in the copied/saved text.
- A `pairs`-style line (`key=value key2=value2`) to exercise the M6.1
  configurable `pair_pattern` extraction path, which none of the three real
  apps produce naturally.

### 3.4 Real sustained load, from `payment-service` itself

No new app, no new tooling. `PaymentController.processPayment()`
(`deploy/gke-qa/services/payment-service`) already runs on a hardcoded
`@Scheduled(fixedDelay = 4000, initialDelay = 5000)` — the source of M5's
measured ~1.2 l/s aggregate. Externalize that one constant to a property
(`payment.processing.interval-ms=4000`, one line in
`application.properties`, read via `@Value`/`@Scheduled(fixedDelayString=
"${payment.processing.interval-ms}")`) and deploy a **second Deployment**,
`payment-service-loadgen`, from the same image, same `qa-rec` namespace,
with that property overridden much lower via env var
(`PAYMENT_PROCESSING_INTERVAL_MS=100`, tuned at execution time to land in
the tens-of-lines/s range) — enough for a real, non-trivial, sustained rate
to compare against D-032's lab baseline (280 MB RSS/13% CPU at the lab's
synthetic 400 l/s) and M5's idle 1.2 l/s.

This is the one source change this design makes to a QA-only fixture we
already own (not new application logic, an existing hardcoded value made
configurable) — smaller and lower-risk than adding a whole second app, and
it reuses the same image/build/push step already in M5's lifecycle, so §8
gains no new step.

### 3.5 Summary table

| Workload | Real JVM? | Purpose |
|---|---|---|
| `payment-service`, `catalog-indexer`, `order-orchestrator` | Yes (reused from M5, unchanged) | Continuity baseline; trace view now exercised (tracing already present, §3.1); `catalog-indexer`'s stack trace doubles as the `strip` realism source (§3.2) |
| `payment-service-loadgen` | Yes — same image, tunable interval (§3.4) | Sustained real log volume for §6, no new app |
| `noisy-fixture` | No — shell script | `level_from` remap, redaction, `pairs` extraction — cheaply, precisely shaped |

## 4. Namespaces and identity

Unchanged from M5 §4/§5: `qa-rec` (now also holding
`payment-service-loadgen`, `noisy-fixture`) and `qa-restricted`, same `huginn-reader`
IAM principal, same namespaced RBAC, same `kms` environment. **Reuse, do not
recreate**, if this session follows M5 in the same project without a full
teardown in between; recreate identically (§5-6 of M5's plan) if starting
fresh after a prior teardown.

## 5. Huginn config folder

Extends `deploy/gke-qa/config/` (existing, from M5) rather than a new
folder — same `environments.yaml`/`rbac` split, `services.yaml` gains two
entries (`payment-service-loadgen`, grouped with `payment-service` or as its
own repo — execution-time choice; `noisy-fixture` under a `debug`-style
repo per `standalone_pods`), and `ui.yaml` gains:

```yaml
redact:
  - '\b[\w.+-]+@[\w-]+\.[\w.-]+\b'      # fake emails in noisy-fixture
  - '\bBearer [A-Za-z0-9._-]+\b'         # fake tokens in noisy-fixture
save:
  dir: ~/huginn-gke-qa-saves            # must pre-exist per D-050
clipboard: auto
```

A `formats/30-noisy.yaml` (new, small) for the fixture's `severity`-keyed
JSON, using `level_from`; the M5 trio and its loadgen sibling keep
`formats/20-spring-json.yaml` unchanged.

## 6. NFR test plan

Same methodology as M5's soak table and D-032's lab baseline (`ps` sampling,
`HUGINN_CPUPROFILE`), extended to real load:

| Scenario | Baseline to compare against | What's measured |
|---|---|---|
| Idle: M5 trio only, no loadgen | M5's own number (62-68 MB RSS, 2.8-5.9% CPU) | Confirms this cluster/session is the same class as M5 — sanity check before trusting the loaded number |
| Loaded: M5 trio + `payment-service-loadgen` at its tuned tens-of-lines/s rate, `--containers all`, no filters | D-032 lab baseline (280 MB RSS/13% CPU @ 400 l/s synthetic) and `perf-pass-M8.md`'s in-process numbers (e.g. frame 523 µs, trace open+close 3.4 ms) | RSS/CPU via `ps` (3+ samples over several minutes); `HUGINN_CPUPROFILE` for a real `pprof` profile under real (not demo-generated) ingest, diffed against `perf-pass-M8.md`'s profile-driven findings — confirms those fixes hold under a real, not synthetic, decode/filter/frame path |
| Loaded + a real filter/trace-view/select active | Same loaded baseline, no filter | Isolates the cost of D-046/D-047/D-049's features under real concurrent ingest, not just their own benchmarks (`BenchmarkLogsFrameSelecting`, trace-view benches) |
| Responsiveness | Subjective, explicitly flagged as such (no automated key-latency harness exists) | Key-press-to-redraw feel at the loaded rate, both plain and with `F` fullscreen / wrap on; report as an honest observation ("felt responsive at N l/s, visible lag at M l/s"), not a fabricated number — consistent with the project's "no fake metrics" rule |
| Environment switch under load (`ctrl+e`) between `rec`/`restricted`/`kms` | M5 (idle) | Confirms switching and resync (`r`) stay snappy with a live high-volume watch already running, which M5's idle session couldn't show |

## 7. Coverage matrix (the "100%" deliverable)

A table, one row per README key-table entry (both screens), three columns:
`Lab only` / `M5 real-GKE` / `This session`, filled in during execution —
sketched here as the **method**, not pre-filled (it depends on what actually
gets exercised). Rows expected to move from "lab only" to "this session" based
on §0's gap analysis: `V`/`m`/`y`/`Y`/`ctrl+s` (M9), `v` trace view (M8.2),
`tab`/`=`/`!` field filter (M8.1), mouse click/drag/wheel select, `auto`
theme. Everything already in M5's "not re-run, already proved by the lab"
list (M5 §0) stays `Lab only` here too, by the same reasoning — this session
does not re-litigate it, only extends the matrix's two new columns.

The report (§8 step 6) **must** include this filled-in table; a session that
skips it hasn't actually delivered "100% coverage," just a narrative claim of
it.

## 8. Lifecycle

Same order as M5 §8, with the new workloads folded into steps 3-5:

1. Confirm the M5 billing budget is still in place (do not recreate).
2. `gcloud container clusters create-auto …` (§2) — or reuse M5's if still
   up (it shouldn't be, per M5's own teardown discipline; verify).
3. Artifact Registry: rebuild/push the M5 trio, unchanged, plus the
   `payment-service` image again as `payment-service-loadgen` with the
   interval property overridden (§3.4) — same build step as M5, no new
   registry repo.
4. Apply namespaces, all workloads including `noisy-fixture` and
   `payment-service-loadgen` (§3); recreate IAM/RBAC/KMS identically to
   M5 (§4) if starting fresh.
5. Extend `deploy/gke-qa/config/` (§5): new `services.yaml` entries,
   `ui.yaml` redact/save/clipboard, `formats/30-noisy.yaml`.
6. Run the QA session: feature pass against §7's matrix, then the NFR
   pass (§6) with `payment-service-loadgen` running; fill in both.
7. **Teardown**, unconditionally, same as M5 §8 step 7.
8. Record a decision (§9) and fold any real-GKE-only finding into
   README/`docs/CONFIG.md`, same as M5 §8 step 8.

## 9. Decisions to record (once run)

- **D-05x candidate:** `payment-service`'s processing interval externalized
  to a property for QA load generation (no new app; §3.4); the filled-in §7
  coverage matrix; the §6 loaded-vs-lab NFR comparison and whether
  `perf-pass-M8.md`'s fixes hold under real ingest; trace view (M8.2)
  exercised for the first time against `payment-service`'s already-real
  tracing; any new finding.

## 10. Risks / open questions

- The externalized interval property (§3.4) is the one source change this
  design makes to a real app already in `deploy/gke-qa` — small
  (`fixedDelay` → `fixedDelayString` reading one property) but still worth
  its own quick review/test before the image is rebuilt, not assumed
  correct by analogy to the constant it replaces.
- The loadgen replica's tuned rate (§3.4, "tens of lines/s") is a starting
  point, not a spec — adjust the env var at execution time so the loaded
  scenario is meaningfully above M5's 1.2 l/s without approaching the lab's
  synthetic 400 l/s (the point is a real, JVM-driven, moderate rate, not a
  stress test).
- Whether `auto` theme (D-051) needs checking against more than one real
  terminal this session (D-051 only confirmed tmux 3.4) is left to
  execution time — cheap to check, not worth a dedicated workload.

## 11. Done when (this design)

This document exists, cites M5's already-confirmed cost/pricing facts rather
than re-deriving them, and needs no code change to review. Execution — §8 —
is a separate, future session and out of scope here, same convention as
M5-gke-qa.md §11.
