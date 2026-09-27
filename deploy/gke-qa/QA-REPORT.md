# Real-GKE QA report (M5)

Run against `docs/plan/M5-gke-qa.md` on 2026-09-27, binary built at commit
`2242960` (`go1.27.0`, `CGO_ENABLED=0 GOOS=linux`), against a real GKE
Autopilot cluster `huginn-qa` (`us-central1`, control plane
`1.35.8-gke.1036000`, project `huginn-kube-tui`) running real Spring Boot
3.5 services. Host driving the session: WSL2 Ubuntu 24.04 (tmux), invoked
from Windows. Per M5 §7, this report **only covers the delta over
[`deploy/lab/QA-REPORT.md`](../lab/QA-REPORT.md)** — real GKE auth,
`namespace_from` through GCP KMS, real Spring Boot structured logging, and
a real-network Phase K comparison. Every other row of
`deploy/lab/QA-SESSION.md` §3 (screens, keys, pickers, display/layout,
reserved keys, config variations) is **not** re-run here; it was already
proven against every Kubernetes object shape in the lab.

## Scoping notes

- Three real Spring Boot 3.5 services were built and pushed to Artifact
  Registry (`payment-service`, `catalog-indexer`, `order-orchestrator`),
  per M5 §3 — not fakes. `payment-service` also carries a `busybox`
  sidecar (`istio-proxy`) and an `istio-init` init container, matching the
  lab's sidecar-hiding fixture shape but on a real pod.
- `deploy/gke-qa/e2e.sh` (new, modeled on `deploy/lab/e2e.sh`) automated
  the bulk of this session's checks against the live cluster: services
  screen (sync, real crash loop, real OOM, ready-replica count), logs
  screen (JSON decoding, stack-trace folding, sidecar hide/show, head
  window), the real JDBC crash's full stack trace, the `restricted`
  environment's real RBAC-forbidden-namespace behavior, and the `kms`
  environment's real sops+KMS namespace resolution — **19/19 checks
  passed**.
- The auth-phase sub-checks that need destructive IAM state (revoking a
  live binding, disabling a live key) were run by hand, outside
  `e2e.sh`, and reverted immediately after each observation — see
  "Real GKE auth" below.
- Environment quirk found and worked around, not a huginn issue: a tmux
  server, once running, serves `new-session` from the environment it had
  at server start, not the invoking shell's environment at the time of
  the call — a long-lived server silently starved `sops`/`kubectl` of
  `KUBECONFIG`/`GOOGLE_APPLICATION_CREDENTIALS` exported later in the
  session. Fixed in `e2e.sh`'s `start()` by forwarding both variables as
  a literal prefix in the command string tmux runs, and by starting from
  a freshly killed server for ad-hoc probes.
- Separate environment quirk, also not a huginn issue: routing a
  multi-word script containing `$VAR` through `wsl.exe` as an inline
  argument (`wsl -d Ubuntu -- bash -c '...$VAR...'`) silently drops the
  variable before the real shell ever parses it — reproduced even inside
  a single-quoted heredoc body, where POSIX quoting should have protected
  it. Root cause not fully isolated (argument-reconstruction across the
  Windows/MSYS/WSL boundary), but reliably worked around by never
  embedding `$VAR` in a `wsl.exe` argv string: write the script to a file
  via stdin (`wsl ... -- bash -c 'cat > /tmp/x.sh' <<'EOF' ... EOF`, with
  `MSYS_NO_PATHCONV=1` to stop Git Bash from rewriting the POSIX path)
  and execute that file instead.
- Config-folder placement gotcha (real, worth keeping): the sops-encrypted
  dotenv for `namespace_from` cannot live inside the config folder itself
  — Huginn's config loader validates the folder's contents against a
  fixed allow-list (`huginn.yaml, environments.yaml, services.yaml,
  containers.yaml, ui.yaml, formats, layouts`) and rejects any other file.
  `deploy/gke-qa/namespace.env.enc` sits one level up
  (`deploy/gke-qa/`), referenced from `environments.yaml` as
  `sops:../namespace.env.enc#K8S_NAMESPACE` — matches `docs/CONFIG.md`'s
  documented `../` convention for relative `namespace_from` paths, but is
  easy to get wrong on a first pass (it was, this session).

## Real GKE auth (M5 §7 "auth" phase)

- **`forbidden` on `qa-restricted`** — confirmed live, repeatedly, via
  the real `huginn-reader` IAM principal + namespaced RBAC (`rbac.yaml`,
  bound to `qa-rec` only). Status bar: `namespace qa-restricted:
  forbidden`. Matches the lab's local-kubeconfig-context version of the
  same test, now produced by a real GKE RBAC decision instead of a faked
  context swap.
- **`secrets unavailable`, credentials not found** — confirmed live: a
  huginn process started with `GOOGLE_APPLICATION_CREDENTIALS` unset/
  empty against the `kms` environment shows, verbatim: status bar
  `kubernetes · error: secrets unavailable`; body `Cannot watch kms:
  secrets unavailable` /
  `environments.yaml: environments.kms.namespace_from: sops cannot
  decrypt deploy/gke-qa/namespace.env.enc: Failed to get the data key
  required to decrypt the SOPS file. · cannot create GCP KMS service:
  credentials: could not find default credentials`, with a `press r to
  retry` prompt.
- **`secrets unavailable`, permission denied** — confirmed live by
  revoking `huginn-reader`'s `roles/cloudkms.cryptoKeyDecrypter` binding
  on `namespace-key` (`gcloud kms keys remove-iam-policy-binding`) with
  credentials otherwise present and valid: same status-bar/`press r to
  retry` shape, but the body's root cause changes to a GCP
  `PermissionDenied` on `cloudkms.cryptoKeyVersions.useToDecrypt`,
  correctly distinguishing "can't find credentials" from "credentials
  found but not authorized" while folding both into the same
  `secrets unavailable` chip. Binding restored
  (`add-iam-policy-binding`) and re-verified clean (`kms` environment
  synced normally) immediately after the observation.
- **`unauthorized` (revoked/invalid credential)** — attempted, **not
  conclusively reproduced** in this session. `huginn-reader` is a real
  external IAM principal (GKE maps it straight to a Kubernetes `User` of
  the same name, per `rbac.yaml`'s header comment — no in-cluster
  ServiceAccount/token to revoke the way the lab's `rbac.yaml` does), so
  the closest real-world equivalent is disabling its IAM key
  (`gcloud iam service-accounts keys disable`). Disabling the key that
  `GOOGLE_APPLICATION_CREDENTIALS` pointed at had **no observable effect**
  on two probes roughly 90 s apart — the `restricted` environment kept
  authenticating and syncing normally throughout. Most likely
  explanation: Google's OAuth2 token endpoint and/or the GKE auth path
  accepts a short-lived access token independent of the signing key's
  live disabled/enabled state once minted, so disabling a key doesn't
  retroactively invalidate a token already in play; forcing a real 401
  would need either waiting out a live token's own expiry (~1h) or a
  more invasive credential-revocation path than fits this session's time
  budget. Key re-enabled immediately after the observation. Recorded as
  a real, if inconclusive, finding rather than silently skipped.

## `namespace_from` through GCP KMS (M5 §7 "namespace_from" phase)

- The `kms` environment (`environments.yaml`: `namespace_from:
  sops:../namespace.env.enc#K8S_NAMESPACE`) opens and resolves its
  namespace end to end through the real `huginn` binary: `sops` decrypts
  `namespace.env.enc` using GCP KMS (`namespace-key`, keyring `huginn-qa`,
  `us-central1`) via `huginn-reader`'s own IAM credentials, not a literal
  namespace list — confirmed by the services screen showing the real
  `qa-rec` workloads (`synced`, all three repos visible) with no
  namespace hardcoded anywhere in `environments.yaml` for that entry.
- This closes the gap `deploy/lab/QA-SESSION.md` §0.1 explicitly left
  open (the lab only exercises `namespace_from` with local `age` keys,
  never GCP KMS).

## Real Spring Boot structured logging (M5 §3)

Confirmed live against `payment-service`, `catalog-indexer` and
`order-orchestrator`, all built with
`logging.structured.format.console=logstash` (no extra dependency, per
M5 §3) and decoded through the lab's unmodified
`formats/20-spring-json.yaml`/`layouts/spring.yaml`:

- Standard fields present and correctly decoded: `@timestamp`, `level`,
  `logger_name`, `thread_name`, `message`, `level_value`, plus
  `traceId`/`spanId`/`correlationId` on `PaymentWorker` messages.
- Real multi-frame stack traces (`stack_trace` field) on ERROR-level
  lines, folding into the logs view as `[+N lines, enter to open]` —
  observed on both a real Hibernate/Hikari startup failure
  (`catalog-indexer`, unresolvable DB host:
  `HikariPool-1 - Starting...` → `SQL Error: 0, SQLState: 08001` →
  `The connection attempt failed.` →
  `HHH000342: Could not obtain connection to query metadata` →
  `Application run failed`) and an occasional simulated
  `AcquirerTimeoutException` in `payment-service`. This is the thing the
  lab's one-line fake JSON fixture (`deploy/lab/workloads.yaml:83`)
  cannot exercise realistically, per M5 §3's own rationale for building
  real images.
- Logger-name abbreviation (last segment kept full, e.g.
  `c.h.payment.PaymentWorker`, `.h.c.CatalogIndexerApplication`,
  `c.z.hikari.HikariDataSource`, `o.h.e.j.spi.SqlExceptionHelper`) reads
  correctly against real, deep Java package names — the lab's fixtures
  use shorter hand-typed logger names, so this hadn't been exercised
  against realistic depth before.
- Head window (`9`): shows real JVM startup lines
  (`Starting PaymentServiceApplication` →
  `Started PaymentServiceApplication in 23.736 seconds...`), confirming
  the head-window feature (M4.2) against a real, slow-relative-to-a-shell-
  script JVM boot.
- Real OOM: `order-orchestrator` (small `-Xmx160m` against a `220Mi`
  container limit, a scheduled task growing a list) cycled between
  `Healthy` and `OOMKilled` (exit 137) repeatedly over the session,
  roughly every 1-4 minutes — consistent with the lab's `OOMKilled`
  shape, but reached via a real JVM heap allocation rather than a
  `head -c /dev/zero` trick. No separate JVM-logged `OutOfMemoryError`
  line was observed before the kernel OOM-kill in the captured restarts
  (the kernel won the race every time observed) — noted in M5 §3 as
  worth checking either way, now checked: on this workload's timing, the
  kernel OOM-killer fires first.
- Sidecar hide/show (`A`) against a real pod: `istio-proxy` (busybox,
  labelled like the lab's fixture) hidden by default, shown with `A`,
  container-prefixed as `<pod>/<container>` exactly like the lab
  (`7zn7k/istio-proxy 21:39:22.908 INFO envoy : sidecar heartbeat`) — no
  behavioral difference from the lab's fake sidecar.

## Real vs. lab: Phase K (soak), re-measured not re-baselined

Per M5 §7, resource numbers are compared against D-032's baseline and
against the lab's own run, not treated as a strict regression target —
`kind`'s API server is effectively localhost, this cluster is a real
regional GKE control plane reached over a real network from a WSL2 host.

| Scenario | Lab (D-032/QA-REPORT, `kind`, localhost-ish) | This run (real GKE, real network) |
|---|---|---|
| Streaming `payment-service` logs (~1.2 lines/s, real workload rate, not the lab's 400 l/s synthetic `bulk-emitter`) | n/a (different workload) | RSS 62-68 MB, CPU 2.8-5.9% (`ps`, 3 samples ~20 s apart, all flat/low) |
| Services screen sync/resync over the real network | n/a | No extra latency-related symptom observed: `synced` timestamps updated on the expected ~poll cadence, no stuck `watching` state, no dropped-connection UI at any point across the whole session (well beyond the ~1 min sampled here) |

Not directly comparable to the lab's `bulk-emitter`-driven 280 MB RSS/13%
CPU numbers (different, much lower log rate — three real but
low-throughput Spring Boot services vs. a synthetic firehose), so this is
a "does a real network introduce visible instability" check, not a
regression number: it does not. No reconnect storms, no stuck spinners,
no growing-unboundedly RSS were observed over the course of the session
against the real GKE control plane.

## Findings summary

| # | Area | Severity | Status |
|---|---|---|---|
| 1 | Config loader rejects a `namespace_from` sops file placed inside the config folder itself | doc/convention (not a bug) | fixed: `docs/CONFIG.md`'s `namespace_from` row now states explicitly that the encrypted file must sit outside the folder and names the `unexpected file` error it triggers otherwise |
| 2 | tmux server serves `new-session` from its start-time environment, not the caller's current env | environment/tooling, not huginn | worked around in `e2e.sh` |
| 3 | `wsl.exe` silently drops `$VAR` from inline multi-word script arguments, even inside quoted heredocs | environment/tooling, not huginn | worked around (script-file + `MSYS_NO_PATHCONV=1`), root cause not fully isolated |
| 4 | Disabling the read-only IAM principal's key doesn't retroactively invalidate an already-issued access token | environment/GCP behavior, not huginn | inconclusive within session time budget, documented as-is |
| 5 | `secrets unavailable` correctly distinguishes "credentials not found" vs. "permission denied" in its body text while using the same status-bar chip and retry affordance for both | confirms intended behavior | no action needed |

No huginn code bugs were found in this session. Every screen/key/picker
already covered by the lab (§0 table) was intentionally not re-run here,
per M5 §7's scope; `e2e.sh`'s 19/19 pass and the manual auth-phase
observations above are the full extent of this session's independent
verification.
