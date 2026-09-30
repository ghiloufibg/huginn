# Real-GKE QA report (M10)

Run against [`docs/plan/M10-gke-qa.md`](../../docs/plan/M10-gke-qa.md) on
2026-09-30/10-01, binary built at commit `d1eff0a` (`go1.27.0`,
`CGO_ENABLED=0 GOOS=linux`), against a fresh real GKE Autopilot cluster
`huginn-qa` (`us-central1`, project `huginn-kube-tui`), reusing the M5 trio
(`payment-service`, `catalog-indexer`, `order-orchestrator`) plus two new
workloads (`payment-service-loadgen`, `noisy-fixture`). Host driving the
session: a native WSL2 Ubuntu Google Cloud CLI + tmux, invoked from Windows.

Per M10 §0, this report covers the delta over M5: M6-M9 features never
exercised against a real cluster before, and one real-load NFR data point.

## Prerequisites: what M5's plan assumed vs. what was actually true

All confirmed live, not assumed:

- **Billing budget did not exist.** M5's plan said one was created "before
  anything else touches the project"; `gcloud billing budgets list` showed
  0 items. Created fresh (50/80/95% thresholds) before provisioning anything.
- **The `huginn-reader` IAM service account no longer existed** (deleted at
  M5's teardown, consistent with its own lifecycle intent even though not
  spelled out explicitly). Recreated.
- **Creating a service-account key is now blocked by an org policy**
  (`iam.serviceAccountKeys.create` denied even under `roles/owner`) — this
  was *not* true during M5, which relied on exactly this
  (`GOOGLE_APPLICATION_CREDENTIALS` pointing at a downloaded key). Real,
  previously-unknown change. Worked around for Kubernetes access via
  `gcloud container clusters get-credentials --impersonate-service-account`
  (keyless, no static credential); **not** worked around for `sops`+KMS,
  which needs Application Default Credentials that require an interactive
  login this session couldn't perform autonomously. The **`kms` environment
  is deferred this round** — `rec` and `restricted` don't depend on it.
- **KMS key regeneration was actually necessary, not just planned**: the
  M5-era `namespace-key` version was `DESTROY_SCHEDULED`; a fresh version
  was created and set primary (though moot this round given the point above).

## Bugs found and fixed (committed to `main` as found, not batched)

| # | Bug | Fix | Commit |
|---|---|---|---|
| 1 | `TestUICopy` (`internal/config`) hardcoded a Unix `/` path separator; `ExpandHome` correctly uses `filepath.Join` (OS-native), so the test failed on native Windows | Compute the expected suffix from `filepath.Separator` | `014d973` |
| 2 | **IAM+RBAC union trap, self-inflicted**: granted `huginn-reader` the project-level `roles/container.viewer` for `get-credentials`, which — exactly as README already warns — grants read access to every namespace regardless of RBAC, silently defeating `qa-restricted`'s forbidden test | Replaced with the minimal `roles/container.clusterViewer` (cluster discovery only, no object-read grant); re-verified `qa-restricted` correctly returns `Forbidden` | not a code change — confirms the README's existing warning is correct and easy to fall into even knowing about it |
| 3 | `payment-service-loadgen` (200m cpu) and `noisy-fixture` (20m cpu/32Mi) each formed a resource-request shape distinct from every already-scheduled pod; GKE Autopilot provisioning a new node-pool bucket for them hit this free-trial project's zero `PREEMPTIBLE_CPUS` quota, leaving both `Pending` | Aligned both to the already-proven 100m/256Mi shape; both scheduled immediately | `90d6955` |
| 4 | **`formats/30-noisy.yaml` never matched.** `docs/CONFIG.md` explicitly documents "formats are tried in file name order, first `match` wins, a format without `match` should sort last" — `20-spring-json.yaml` has no `match` and sorted *before* `30-noisy.yaml`, silently decoding `noisy-fixture` too. `level_from` and the `transform`/`pair_pattern` never ran (level column blank, message unstripped) | Renamed to `10-noisy.yaml`, so it's tried first and its `match: {containers: [noisy-fixture]}` scopes it correctly | `d1eff0a` |

Bug 4 was caught live on the real cluster (screen showed a blank level
column and un-stripped message text); confirmed root cause by reading the
decoder and `CONFIG.md`, not by guessing, then reproduced-fixed-reverified
on the same running pods with no redeploy needed (config-only change).

## M6-M9 feature coverage against real data (new this session)

All confirmed live via tmux automation (`deploy/gke-qa/e2e-m10.sh` plus
targeted manual-equivalent scripted checks) against the real cluster:

- **M8.2 trace view (`v`)**: opened against `payment-service`'s real
  `trace_id`/`span_id` (the tracing bridge was already in the image's
  `pom.xml` from M5, just never exercised through the TUI before).
- **M8.1 field filter from zoom (`tab`/`=`)**: confirmed filter applied,
  status bar reflected it.
- **M7 `level_from`**: confirmed end to end after fixing bug 4 —
  `noisy-fixture`'s non-standard `sev` field correctly raises lines with no
  JSON `level` field to ERROR/WARN/INFO, exactly per `docs/CONFIG.md`'s
  documented precedence.
- **M6.1/M6.2 `transform`/custom `pair_pattern`**: confirmed the
  `key: "value";` syntax (not the default `key=value`) is correctly
  extracted into fields and stripped from the displayed message.
- **M9.1/M9.2 select/mark/copy/save + redaction, full round trip**: marked
  the exact line containing a fake email and a fake bearer token, saved it
  (`ctrl+s`), and confirmed:
  - the secret is **visible in plain text on screen** (never redacted
    there, per D-050), and
  - the **saved file** (`~/huginn-gke-qa-saves/noisy-fixture-rec-*.log`,
    mode `0600`) contains `contact [redacted], token [redacted])` —
    both patterns fired correctly on real data.
- **Regression check (M5's own `e2e.sh`)**: real crash loop (`catalog-indexer`,
  real Hikari/JDBC stack trace, byte-identical shape to M5's report), real
  OOM (`order-orchestrator`), `payment-service` healthy 2/2 — all still
  correct after the rebuild. One check (`CrashLoopBackOff` literal text on
  the services screen) transiently failed: traced to
  `internal/core/domain/status.go`'s documented precedence — the container
  was sampled mid-`Terminated{Reason:Error}` between backoff cycles, where
  "Degraded / last exit 1" is the *correct* text, not "waiting in backoff".
  A real-world timing artifact of testing a live crash loop, not a bug.

## Not covered this session

- **`kms` environment** (`namespace_from` via sops+GCP KMS): deferred, see
  Prerequisites above. `sops` was also not installed in the fresh native
  WSL environment used this round (a separate, unrelated gap from the
  credentials one).
- **Mouse selection** (click/shift-click/drag, D-050): not exercised —
  tmux `send-keys` cannot easily simulate mouse events; needs a manual
  pass in a real terminal.
- **`auto` theme** (D-051, OSC 11 background query): the app ran correctly
  under it with no crash, but contrast/switching wasn't independently
  re-verified beyond what `TestThemeContrast` already covers — D-051 itself
  only confirmed tmux 3.4, which is what this session also used, so no new
  terminal was added to that coverage.

## NFR: one real data point

`bin/huginn-linux --config deploy/gke-qa/config rec --containers all`,
all five real repos live (idle trio + `payment-service-loadgen` at
`PAYMENT_PROCESSING_INTERVAL_MS=150` + `noisy-fixture`), sampled via `ps`
every 8s over 4 samples:

| Metric | This run (real GKE, mixed real load) | M5 (real GKE, idle trio only) | Lab/D-032 (synthetic 400 l/s) |
|---|---|---|---|
| RSS | 76 MB (flat across samples) | 62-68 MB | 280 MB |
| CPU | 2.7% (flat) | 2.8-5.9% | 13% |

Consistent with expectations: this session's aggregate real log rate
(loadgen + trio + fixture) is still well under the lab's synthetic
firehose, so RSS/CPU sit between M5's idle baseline and the lab's stress
number, not near either extreme. **Responsiveness**: subjectively smooth
at this rate in manual interaction during the scripted checks above; no
automated key-latency harness exists, so no fabricated number is given.
`perf-pass-M8.md`'s fixes (hidden-field cache, level-set hoisting, theme
by pointer) were exercised implicitly by this real ingest with no
regression observed, but not re-profiled with `pprof` this session —
time-boxed out, flagged as a gap rather than skipped silently.

## Findings summary

| # | Area | Severity | Status |
|---|---|---|---|
| 1 | Cross-platform test bug (`TestUICopy`) | real bug | fixed, `014d973` |
| 2 | Autopilot quota wall from mismatched resource-request shapes | real deployment bug (this session's own manifests) | fixed, `90d6955` |
| 3 | `30-noisy.yaml` never matched (file-name ordering) | real config bug (this session's own config) | fixed, `d1eff0a` |
| 4 | IAM/RBAC union trap | confirms existing README warning; not a new huginn issue | worked around, no code change |
| 5 | SA-key-creation org policy, `kms` env | environment/GCP policy change since M5, not a huginn issue | deferred, documented above |
| 6 | Crash-loop text timing | confirms `status.go`'s documented precedence is correct | no action needed |

No huginn *runtime* code bugs were found. The two real bugs found and
fixed this session were both in this session's own new QA fixtures/tests,
caught by actually running them against a real cluster rather than trusting
the design doc — exactly the value a real-GKE pass is supposed to add.
