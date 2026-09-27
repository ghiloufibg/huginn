# M5 — Real GKE: a QA session with real Spring Boot workloads  (status: design)

**Goal:** connect Huginn to a real GKE cluster in the user's own GCP project
(`huginn-kube-tui`, the $300 / 90-day free trial, already selected as the
`gcloud` default) and run an end-to-end QA session against it, this time with
**real Spring Boot services emitting JSON logs by default**, covering
everything [`deploy/lab/QA-SESSION.md`](../../deploy/lab/QA-SESSION.md)
explicitly leaves out because it only has a local `kind` cluster: real GKE
IAM/RBAC, `namespace_from` through GCP KMS, and real Spring Boot structured
logging.

**Status: design only.** Nothing in this document has been provisioned. No
`gcloud`, `kubectl` or Kubernetes manifest below has been applied; every
command is illustrative, for a future execution session to run top to
bottom, the same way `QA-SESSION.md` itself was a design before it was run.

## 0. What this reuses, what is new

`deploy/lab` already proved the adapter against every Kubernetes *object
shape* Huginn shows (M4 §1, §7). Re-deploying that whole catalog on GKE
would cost more and prove nothing new. This session is scoped to what only a
real cluster can show:

| Already covered by the lab — **not** repeated here | New — needs a real cluster |
|---|---|
| CrashLoopBackOff/OOMKilled/ImagePullBackOff/Pending shapes, StatefulSet, DaemonSet, CronJob, standalone pod, orphaned ReplicaSet (`workloads.yaml`, `qa-fixtures.yaml`) | Real GKE auth: `unauthorized` (revoked token), `forbidden` per namespace via real RBAC bound to a real IAM principal |
| Sidecar hiding, `A`, columns, filters, pickers, resize matrix, flakiness injection, reserved keys (`QA-SESSION.md` §2–§3) | `namespace_from` through `sops` + **GCP KMS** (the lab only exercises age keys) |
| `formats/10-nginx.yaml` decoding (`storefront-web`, CI's `examples/config-nginx`) | `formats/20-spring-json.yaml` against **real** `logging.structured.format.console=logstash` output: real MDC fields, real multi-frame stack traces, real `/actuator/health` probes |
| `--demo` pass (Phase N, no cluster needed) | Real network latency/jitter vs. `kind`'s effectively-localhost API server |

## 1. Cost governance (read this first)

Confirmed from Google's own documentation before writing the rest of this
plan (per the project's infrastructure rule — official docs over assumption):

- The $300 credit is spent over 90 days and is a **hard cap**: a free-trial
  billing account is explicitly "not billed"; if the credit runs out or 90
  days pass, the account auto-closes and billing is disabled, with a 30-day
  grace period before resources are deleted. There is no path to an
  unexpected charge unless the account is manually upgraded to a paid
  billing account.
- GKE's Free Tier waives the cluster management fee for **one Autopilot or
  zonal Standard cluster per month**; this is separate from, and in addition
  to, the $300 credit. Only compute (nodes or Autopilot pod resources),
  Artifact Registry storage and egress draw from the credit.
- A free-trial account cannot request a quota increase, so this design stays
  inside default project quotas by construction (small cluster, three tiny
  services).
- Because overspend is architecturally impossible on the trial account, the
  budget controls below exist to avoid **wasting** credit needed for later
  milestones (more QA rounds, a demo), not to prevent a bill:
  1. `gcloud billing budgets create` with 50/80/95% email thresholds, scoped
     to the billing account backing `huginn-kube-tui`, created **before**
     the cluster (first step of §8).
  2. The cluster is created for one time-boxed session (a few hours: spin
     up → QA → teardown), never left standing between sessions.
  3. A single teardown command deletes the cluster, the Artifact Registry
     repository, the KMS key ring and any reserved static IPs — mirroring
     the shape of [`deploy/lab/up.sh`](../../deploy/lab/up.sh) but for
     GKE, and run at the end of every session, not just the last one.
- **Not independently confirmed**: exact current Autopilot per-vCPU/GiB
  rates and e2 on-demand prices (Google's pricing pages returned truncated
  content when fetched for this design). Re-check the [GKE pricing
  page](https://cloud.google.com/kubernetes-engine/pricing) or the Pricing
  Calculator right before §8 step 2 — this affects how much of the $300 is
  left for later milestones, not whether this session can overspend it. As
  an order-of-magnitude sanity check: three small pods (0.1–0.5 vCPU, a few
  hundred MiB each) for a few hours is a small fraction of a dollar under
  any e2/Autopilot rate published historically; treat that as an estimate
  to verify, not a commitment.

## 2. Cluster design

- **Mode: GKE Autopilot**, single region (pick the region at execution
  time; nothing below is region-specific). Reasons: scale-to-zero when no
  workload is scheduled, no node-pool sizing decisions to get wrong (a
  manually oversized Standard node pool left running is the realistic way
  to waste credit here, not a runaway bill), and the free cluster
  management fee applies the same as Standard's first zonal cluster.
- **Trade-off to watch in the QA session, not a blocker:** Autopilot
  schedules a real node to fit each pod's resource request. The lab's
  `email-dispatcher` Pending/Unschedulable fixture (§3) may report a
  different reason (no matching machine shape vs. "insufficient memory")
  for the same oversized request — record it as a wording difference to
  fold into `docs/CONFIG.md`/README if it disagrees with the lab's text,
  not as a bug (this fixture is not being re-deployed here per §0, but the
  same request shape is reused for §3's `Pending` fixture if one is added).
- Illustrative command (not run):
  ```sh
  gcloud services enable container.googleapis.com artifactregistry.googleapis.com cloudkms.googleapis.com --project huginn-kube-tui
  gcloud container clusters create-auto huginn-qa --region <region> --project huginn-kube-tui --release-channel regular
  ```

## 3. Real Spring Boot workloads — reusing only what needs to be real

Building a real Spring Boot image only pays off where JVM/Spring behavior is
what's under test; everything else stays a cheap `alpine`/`busybox` trick,
exactly like the lab:

| State | Real Spring Boot? | Why |
|---|---|---|
| Healthy, 2 replicas, JSON logs + stack traces (`payment-service`) | **Yes** | The point of this session: decode real `logstash`-format output, real MDC fields, real multi-frame stack traces at real verbosity |
| CrashLoopBackOff (`catalog-indexer`) | **Yes** — a bad `DataSource` URL fails startup | A real, long, multi-frame stack trace; the lab's one-line fake JSON (`workloads.yaml` line 83) cannot exercise stack folding realistically |
| OOMKilled (`order-orchestrator`) | **Yes** — small `-Xmx`, a scheduled task growing a list past the container limit | Real JVM OOM behavior differs from the lab's `head -c /dev/zero` trick (the JVM may log `OutOfMemoryError` before the kernel OOM-kills it) — worth observing both, not assumed |
| Sidecar hidden by default | No — `busybox` loop writing text lines, labelled `istio-proxy` like the lab | Sidecar-hiding is about container naming/labels, not the mesh implementation |
| ImagePullBackOff, Pending/Unschedulable | No — same tricks as the lab | No JVM behavior involved |
| Everything else (§0 table) | Not redeployed | Already proved on the lab |

- Build: Spring Boot 3.5.x, `spring-boot-starter-web` + `spring-boot-starter-actuator`
  (real `/actuator/health` liveness/readiness — another thing the lab's
  `alpine` fixtures don't exercise), and (confirmed against the Spring Boot
  3.5 reference docs):
  ```properties
  logging.structured.format.console=logstash
  ```
  No `logstash-logback-encoder` dependency needed — this is Spring Boot's
  own structured logging, "JSON by default" in the literal sense asked for.
  It ships exactly the fields `deploy/lab/config/formats/20-spring-json.yaml`
  already expects (`@timestamp`, `logger_name`, `thread_name`, `level`,
  `message`, and `stack_trace` on exceptions, tunable via
  `logging.structured.json.stacktrace.*`) — the format file was written
  against this shape (or the near-identical `logstash-logback-encoder`
  output) and needs no change.
- Optional, cut first if it complicates the build (YAGNI): `micrometer-tracing-bridge-otel`
  for a real `traceId`/`spanId` MDC pair, no exporter needed — exercises
  the format's `trace_id` mapping with a real value instead of the lab's
  hand-typed one.
- Image: `eclipse-temurin:21-jre-alpine`, multi-stage build, pushed to one
  Artifact Registry repository co-located with the cluster (avoids
  cross-region egress cost).
- Resource requests, explicit and small on all three:
  `requests: {cpu: 100m, memory: 192Mi}` — a real JVM needs more than the
  lab's shell-script loop, still trivial against Autopilot's per-pod
  billing and the budget in §1.

## 4. Namespaces

Two namespaces, to reproduce a **real** forbidden-namespace result (IAM +
RBAC), the one thing the lab could only fake with a local kubeconfig context
swap (`rbac.yaml`, `up.sh`'s `huginn-restricted` context):

- `qa-rec`: `payment-service`, `catalog-indexer`, `order-orchestrator`.
- `qa-restricted`: one trivial `alpine` fixture, readable by the cluster
  owner but **not** granted to the read-only identity of §5 — this is what
  turns into `forbidden` on the services screen.

## 5. Real read-only identity

The lab's `huginn-reader` (`rbac.yaml`) is a Kubernetes ServiceAccount with
a manually minted token — a stand-in for a real developer's access. On GKE
the realistic path (and the gap this session actually closes) is:

- A project-level IAM role limited to `roles/container.viewer` (lets
  `gcloud container clusters get-credentials` build the kubeconpath context,
  grants no Kubernetes permissions by itself — GKE's "IAM authenticates,
  RBAC authorizes" split).
- A namespace-scoped `Role`/`RoleBinding` in `qa-rec` only (same rules as
  `deploy/lab/rbac.yaml`: `get/list/watch` on pods, pods/log, events,
  deployments, replicasets, statefulsets, daemonsets), bound to that IAM
  principal — nothing in `qa-restricted`.
- One kube context, two `environments.yaml` entries (`rec`: `[qa-rec]`,
  `restricted`: `[qa-rec, qa-restricted]`), exactly the lab's `rec`/`restricted`
  split (`deploy/lab/config/environments.yaml`), now driven by real RBAC.
- `namespace_from` through **GCP KMS** (not age, which the lab already
  covers): a small KMS key ring in `huginn-kube-tui`, a `sops`-encrypted
  dotenv naming `qa-rec`, and the read-only principal's IAM needing
  `roles/cloudkms.cryptoKeyDecrypter` on that key. Named as an explicit
  out-of-scope gap in `QA-SESSION.md` §0.1; this closes it. Costs a few
  cents at most for the key ring's lifetime, deleted at teardown (§8).

## 6. Huginn config folder

A new `deploy/gke-qa/config/` (not created — design only), reusing
`deploy/lab/config/formats/20-spring-json.yaml` and
`deploy/lab/config/layouts/spring.yaml` unchanged (already CI-validated,
general-purpose), with a fresh `environments.yaml`, `services.yaml`
(`explicit`, three repos), and `containers.yaml`/`ui.yaml`/`huginn.yaml`
copied from the lab's defaults. Sketch, illustrative:

```yaml
version: 1
environments:
  rec:
    context: gke_huginn-kube-tui_<region>_huginn-qa
    namespaces: [qa-rec]
  restricted:
    context: gke_huginn-kube-tui_<region>_huginn-qa
    namespaces: [qa-rec, qa-restricted]
  kms:
    context: gke_huginn-kube-tui_<region>_huginn-qa
    namespace_from: sops:./kms-namespace.env#K8S_NAMESPACE
```

## 7. QA session — delta over `deploy/lab/QA-SESSION.md`

Reuse its methodology and structure unmodified (§2 observation methodology,
§5 phased order, §6 report template); this session only replaces its §0
(local prerequisites → real `gcloud`/GKE prerequisites) and adds:

- **New phase, auth:** fresh `gcloud auth login`; `unauthorized` after
  `gcloud auth revoke` (README's error table); `forbidden` on
  `qa-restricted` inside the `restricted` environment; `secrets unavailable`
  when the KMS key is briefly unbound from the read-only principal's IAM.
- **New phase, `namespace_from`:** the `kms` environment opens and resolves
  its namespace through `sops` + GCP KMS.
- **Phase K (soak) re-measured, not re-baselined:** compare resource numbers
  against D-032's baseline over a real network; record the delta from the
  lab's numbers, since `kind`'s API server is effectively localhost.
- Every other row of `QA-SESSION.md` §3 (screens, keys, pickers) runs
  unmodified against `payment-service`/`catalog-indexer`/`order-orchestrator`;
  the shapes already covered by the lab (§0 table) are **not** re-run, to
  keep the session short and near-zero cost.
- Report: append a "real vs. lab" section to the existing template (§6) —
  only new findings and confirmed-identical behavior, not a re-derivation.

## 8. Lifecycle (order of work, for a future execution session)

1. `gcloud billing budgets create …` (§1) — before anything else touches
   the project.
2. `gcloud container clusters create-auto …` (§2).
3. Artifact Registry repo; build and push the three Spring Boot images (§3).
4. Apply namespaces and workloads (§4); create the IAM principal, the
   namespaced RBAC, the KMS key ring and the sops-encrypted dotenv (§5).
5. Write `deploy/gke-qa/config/` (§6); `huginn --config deploy/gke-qa/config rec`.
6. Run the QA session (§7); fill in the report.
7. **Teardown, every session:** delete the cluster, the Artifact Registry
   repository, the KMS key ring and any reserved static IPs. Leave the
   billing budget in place for future sessions.
8. Record D-040 (§9); fold any real-GKE finding that the local lab cannot
   show into README's "Connecting to GKE" checklist.

## 9. Decisions to record (once run)

- **D-040 candidate:** GKE Autopilot for the QA cluster; real Spring Boot
  structured logging (`logging.structured.format.console=logstash`, no
  extra dependency) validated against `formats/20-spring-json.yaml`; real
  RBAC-driven forbidden namespace; GCP-KMS-backed `namespace_from`; cost
  and findings.

## 10. Risks / open questions

- Current Autopilot/e2 per-unit pricing was not independently confirmed
  (§1) — re-check immediately before §8 step 2; does not affect the $300
  hard cap, only how much credit later milestones have left.
- Autopilot's exact wording for an intentionally oversized
  Pending/Unschedulable request is unverified against the lab's text — a
  QA observation (§2), not a blocker.
- Whether the read-only identity should be the user's own (scoped down) IAM
  principal or a second, deliberately separate one is left as an
  execution-time choice; either demonstrates the forbidden-namespace path.

## 11. Done when (this design)

This document exists, its cost and pricing claims are backed by Google's
own documentation (cited in §1) rather than assumption, and it needs no
code change to review. Execution — actually running §8 — is a separate,
future session and out of scope here.
