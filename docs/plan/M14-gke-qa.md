# M14 — Real GKE, round 4: muted loggers, Schema Registry, and the D-061 fixes under load  (status: done)

**Goal:** a fourth real-GKE QA session, same `huginn-kube-tui` project as M5/M10/M12,
closing the gaps that have opened up **since M12** (the last real-GKE session):
everything built after it has only ever run against unit tests, benchmarks and
`--demo` — never a real cluster, a real broker, or a real registry.

1. **Muted loggers (D-062–D-066).** `mute.loggers`, the counted-not-silent
   status bar line, and the perf work that makes the whole live/history path
   cheaper under load have never seen a real pod's real logger names or a
   real crash loop. Needs real JSON with a real `logger_name` field — the
   three Spring apps already in `deploy/gke-qa` have exactly that.
2. **Schema Registry (M13, D-067–D-068).** Avro and JSON Schema decoding,
   `schema_registry`'s `basic_auth`, the failure-TTL and retry-once paths —
   all built and unit-tested (`kfake` plus an in-memory `http.RoundTripper`
   for `--demo`) but **never run against a real registry**. M12 explicitly
   could not cover this: Schema Registry support did not exist yet (M12 §3.2:
   "huginn explicitly does not decode Avro/Protobuf, only labels the
   framing").
3. **D-061's own fixes, on real infrastructure.** The pod-name-collision fix,
   the concurrent namespace/workload fetch, the Kafka panic recovery and the
   concurrent offset lookups were verified by unit tests and `-race` only
   (D-061's own text says so). None of the four has run against a real API
   server, a real crash-looping pod across two real namespaces, or a real
   broker.

**Status: done.** Executed 2026-10-07 against a real GKE Autopilot cluster
(region changed to `us-east1`, §1/§2 — see the report); see
[`deploy/gke-qa/M14-QA-REPORT.md`](../../deploy/gke-qa/M14-QA-REPORT.md) for
the report and [D-069](../DECISIONS.md) for the decision record. This
document is kept as-written for its design rationale; the report is
authoritative on what was actually run.

## 0. What this reuses, what is new

| Already covered — **not** repeated here | New — needs this session |
|---|---|
| Real GKE auth, RBAC, `namespace_from` via GCP KMS and `sops`+`age` (M5, M10, D-060) | Muted loggers against a real crash-looping pod's real logger name (`catalog-indexer`'s HikariCP pool) and against real sustained load (`payment-service-loadgen`) |
| Real Spring Boot JSON logging, stack-trace folding, OOM, crash loop, trace view (M5, M10) | A real Schema Registry (Avro + JSON Schema), its `basic_auth`, its failure/retry paths, against a real broker |
| Real Kafka (SASL/SCRAM, TLS, plaintext tiers, request guard, real broker loss/reconnect) (M12, D-057–D-060) | Pods of the same name in two real namespaces of the same repository, read together (D-061) |
| The M9/M10 select/copy/save, redaction, mouse, `auto` theme pass (M10) | The §6 NFR comparison extended with D-063/D-064's own "measured before" numbers against a real, not synthetic, stream |
| 100%-TUI-feature coverage matrix method (M10 §7) — reused, not redesigned | Filling in the matrix's two rows that are still `lab/unit only`: `M` (mute) and `D` (Schema Registry decode) on the Kafka screen |

### 0.1 Why not PetClinic, again

Still dropped, for the same reason M10 §3.2 gave: nothing in this session's
scope needs a fourth real app. Muted loggers need a real logger name — the
three apps already in `deploy/gke-qa` have one (HikariCP's pool logger,
already producing real multi-frame output per M10 §3.2). Schema Registry
needs a real *registry*, not a real *producer* — Huginn only ever reads it;
what writes the Avro/JSON-Schema-framed bytes can be (and in M12 already was,
for the one binary-payload case) a small one-off script, not a JVM app.
Adding PetClinic would cost a new image, a new Artifact Registry push and a
new app's own quirks to characterize, for zero new coverage.

## 1. Cost governance — re-verify before anything else

Same project, same discipline as M5 §1/M10 §1: **do not re-derive, but do
re-check these two things first, this time**, since real time has passed
since M5's session:

- **Remaining balance and expiration, both confirmed 2026-10-07 via the
  Console: 257 EUR, expiring in 80 days (2026-12-26).** The trial is open,
  has real room for this session's small addition (§1 table below), and a
  known hard deadline. `gcloud` itself cannot report either figure
  (confirmed by checking `gcloud billing accounts describe`/`list`: no
  credit or expiration field exists on the resource) — both are
  Console-UI-only, which is why they had to be checked by hand rather than
  scripted. **This session, and any further one in this project, must run
  and tear down before 2026-12-26.**
- **Re-verify current Autopilot/e2 pricing** against the [GKE pricing
  page](https://cloud.google.com/kubernetes-engine/pricing), same caveat as
  M5/M10 (not independently confirmed here either).

**What's new to size, once the above is confirmed:** this session adds, over
M12's already-running set (the M5/M10 trio + loadgen + noisy-fixture + the
single-broker Kafka + the two Kafka marker pods):

| Addition | Size | Why it's cheap |
|---|---|---|
| Karapace (Schema Registry) | 1 pod, ~128–256 MiB, a few hundred mCPU | Python, no JVM; uses the **existing** Kafka broker as its own backing store (a compacted `_schemas` topic) — no new PVC, no new stateful service |
| `named-pod-fixture` ×2 (one per namespace) | 2 tiny `busybox` pods, default requests | Same shape as `noisy-fixture` (M10 §3.3) |
| One ephemeral Kubernetes `Job` (schema registration + framed-record production) | Runs to completion, then exits; no standing cost | One-off, like M12's `franz-go` binary-payload script (§3.2) |
| `payment-service-loadgen`'s existing `logging.level.*` override | No new pod | Env var on an already-running Deployment |

Order-of-magnitude: a handful of small/tiny pods and one job, for the same
few-hour, time-boxed window as M10/M12, then torn down — comparable to M10's
own "smaller delta than adding PetClinic" conclusion, probably smaller in
absolute terms than M12's own broker addition. Still: re-verify against
current pricing, don't assume, same discipline as every prior session.

## 2. Cluster design

Unchanged from M5 §2 / M10 §2 / M12 §2: GKE Autopilot, single region, same
`create-auto huginn-qa` shape. Reuse the same cluster if a prior session's
teardown didn't run (verify first, same note as M10 §8 step 2); otherwise
fresh, region-independent.

## 3. Workloads

### 3.1 Reused, unchanged

The M5/M10 trio (`payment-service`, `catalog-indexer`, `order-orchestrator`),
`payment-service-loadgen`, `noisy-fixture`, the single-broker Kafka
(`kafka-qa`) and the `catalog`/`orders` marker pods — redeployed as-is so
every prior session's baseline stays comparable (same reasoning as M10 §3.1).

### 3.2 New: `named-pod-fixture`, same name in two real namespaces (D-061)

A StatefulSet, not a Deployment — Deployment pod names carry a random
suffix per ReplicaSet and would not collide even deployed identically twice;
a StatefulSet's pod name is deterministic (`named-pod-fixture-0`), which is
exactly the real-world shape D-061's fix targets (its own code comment cites
"a StatefulSet's deterministic `api-1`"). Deployed twice, identically except
for one literal in its log line, once into `qa-rec` and once into
`qa-restricted`, both carrying `app.kubernetes.io/part-of: named-pod-fixture`
so they resolve as one repository spanning two namespaces:

```yaml
containers:
  - name: named-pod-fixture
    image: busybox:1.36
    command: ["sh", "-c", "while true; do echo \"{\\\"level\\\":\\\"info\\\",\\\"message\\\":\\\"from $NS\\\"}\"; sleep 5; done"]
    env: [{name: NS, value: "qa-rec"}]   # "qa-restricted" in the second copy
```

`qa-rec`/`qa-restricted` are already both real namespaces of this cluster
(M5 §4); no new RBAC — the `rec` environment's context is the real `gcloud`
identity used to provision the cluster, not the namespace-scoped
`huginn-reader` principal, so it already has standing access to both. The
one config change needed is widening `rec`'s own namespace list (§5).

**What this proves, that the unit test (`TestSameNamedPodAcrossNamespaces`)
cannot:** a real API server actually returning two distinct `Pod` objects
with the same `metadata.name` from two `List` calls scoped to different
namespaces, and Huginn's real `ListWorkloads`/`ListPods` adapter path
(not a fake) feeding them through the fixed `podKey{namespace, name}` map
without one silently overwriting the other's tailer.

### 3.3 New: Karapace (Schema Registry)

[Karapace](https://github.com/Aiven-Open/karapace) (Apache-2.0, Python,
Confluent-REST-API-compatible) — chosen over Confluent's own Schema Registry
because Huginn's `SchemaDecoder` only ever speaks the (shared, open) REST
protocol, so the real behavior under test does not depend on which
implementation serves it, and Karapace is lighter and has no separate
licensing question to resolve for a QA session. It stores schemas in Kafka
itself (a compacted topic it creates on first use) — **no new PVC, reuses
the existing broker**:

```yaml
containers:
  - name: karapace
    image: ghcr.io/aiven-open/karapace:latest   # pin the digest at execution time
    env:
      - {name: KARAPACE_BOOTSTRAP_URI, value: "kafka-qa:9092"}
      - {name: KARAPACE_PORT, value: "8081"}
      - {name: KARAPACE_HOST, value: "0.0.0.0"}
      - {name: KARAPACE_REST_AUTHORIZATION, value: "true"}   # exercises schema_registry.basic_auth for real
    resources:
      requests: {cpu: 100m, memory: 256Mi}
      limits: {memory: 384Mi}
```

Reached the same way as the broker: `kubectl port-forward`, never a public
LoadBalancer (same reasoning as M11 §2.5, repeated in
`kafka-workloads.yaml`'s own header comment). Basic-auth credentials go into
the same `sops`+`age`-encrypted
source file pattern as the SASL tier (M12 §3.2 tier 2), not a new secrets
mechanism.

### 3.4 New: one ephemeral Job registers schemas and produces framed records

A small Go program, built and run once as a Kubernetes `Job` (same
"ephemeral, cheap trick, no new app" pattern as M12's one-off `franz-go`
script for the invalid-UTF-8 binary payload, §3.2). It reuses the exact same
libraries Huginn's own decoder uses — `hamba/avro` to encode, `franz-go` to
produce — so there is no new toolchain, and what it writes is guaranteed to
be real Confluent wire format, not a hand-rolled approximation:

1. Register one Avro schema (a few fields, at least one `logical type` —
   decimal or timestamp — to exercise D-067's "made readable" formatting)
   and one JSON Schema, against Karapace's REST API (`POST
   /subjects/{subject}/versions`).
2. Produce, onto the `catalog` repo's existing topics (no new topic):
   - a handful of Avro-framed records on `topic-a` (catalog's `produce`
     topic),
   - a handful of JSON-Schema-framed records on `topic-c` (catalog's `list`
     topic, already used for no-direction records in M12),
   - **one record with a bogus schema id** (e.g. `999999`) — the "registry
     names it but cannot use it" error path (D-067),
   - **one record with a plain big-endian numeric key** (no framing) to
     confirm the default `decode: [value]` leaves it alone, per D-068's own
     reasoning about keys that merely start with a `0` byte.
3. Exit. No standing process, no new Deployment.

### 3.5 New: a real, volume-correlated noisy logger, for the NFR pass

No app code change. `payment-service-loadgen` already overrides one
property via env var for its tuned rate (M10 §3.4); it gains one more:

```yaml
env:
  - {name: PAYMENT_PROCESSING_INTERVAL_MS, value: "100"}   # unchanged from M10
  - {name: LOGGING_LEVEL_ORG_SPRINGFRAMEWORK_WEB, value: "DEBUG"}   # new
```

Spring's own `DispatcherServlet`/web logging at `DEBUG` produces one real
extra line per request, so its volume is tied to the loadgen's already-tuned
rate — giving §6 a real "noisy logger, muted" comparison whose load is
exactly as real as the rest of the NFR pass, with zero new application code.

### 3.6 Summary table

| Workload | Real JVM? | Purpose |
|---|---|---|
| M5/M10 trio, loadgen, `noisy-fixture`, `kafka-qa`, marker pods | Yes (reused, unchanged) | Continuity baseline across all four sessions |
| `named-pod-fixture` ×2 | No — shell script | Real same-name-different-namespace pod collision (D-061, §3.2) |
| `karapace` | No — Python, not JVM | Real Schema Registry for D-067/D-068 |
| schema-registration Job | Yes — reuses `hamba/avro`/`franz-go`, Huginn's own libraries | Real Avro/JSON-Schema-framed records, zero new toolchain |
| `payment-service-loadgen` (env var addition only) | Yes (reused) | Real, volume-correlated noisy logger for §6 |

## 4. Namespaces and identity

Unchanged from M5 §4/§5, **except**: `rec`'s namespace list widens from
`[qa-rec]` to `[qa-rec, qa-restricted]` (§5) so the real `gcloud` identity
used there — not the namespace-scoped `huginn-reader` — can see
`named-pod-fixture` in both namespaces at once. This does not touch
`restricted`'s or `kms`'s RBAC (`rbac.yaml`, `huginn-reader`'s role stays
scoped to `qa-rec` only); the existing "qa-restricted is forbidden under
`restricted`" test (M5) is unaffected since that environment's own
namespace list is unchanged.

## 5. Huginn config folder

Extends `deploy/gke-qa/config/` and `config-kafka/` (not a new folder):

- `environments.yaml`: `rec.namespaces: [qa-rec, qa-restricted]` (§4).
- `services.yaml`: no change — `named-pod-fixture`'s
  `app.kubernetes.io/part-of` label resolves it like any other repository.
- `formats/20-spring-json.yaml` (already has `fields.logger: [logger_name,
  logger, log.logger]`, so it already works for muting without
  modification): gains a `mute` section —
  ```yaml
  mute:
    loggers: ["com.zaxxer.hikari"]        # exact logger names, confirmed at execution time
    keep: [error]                          # D-062's opt-in safety valve, exercised for real
  ```
  Exact HikariCP logger name(s) confirmed from `catalog-indexer`'s real
  `logger_name` field at execution time (M10 §3.2 already captured the real
  crash-loop text; this session reads the field itself rather than guessing
  the class name in advance).
- `config-kafka/kafka/20-catalog.yaml` (the `catalog` repo's plaintext
  profile): gains
  ```yaml
  schema_registry:
    url: ${KARAPACE_URL}
    basic_auth:
      username: ${KARAPACE_USERNAME}
      password: ${KARAPACE_PASSWORD}
    decode: [value]
  ```
  sourced from the same `kafka-secrets/` pattern as the existing tiers, not
  a new secrets mechanism.

## 6. NFR test plan

Same methodology as M5/M10 (`ps` sampling, `HUGINN_CPUPROFILE`), this time
comparable to D-063/D-064's own "measured before" numbers instead of only
M10's lab/real split:

| Scenario | Baseline to compare against | What's measured |
|---|---|---|
| Loaded, mute off | M10's loaded number (this session's own continuity baseline) | Confirms no regression from D-061–D-068 at rest |
| Loaded + `LOGGING_LEVEL_ORG_SPRINGFRAMEWORK_WEB=DEBUG`, mute off | Same loaded number | The real cost of a chatty logger reaching the full pipeline (decode, reorder, buffer, filter) — this is what D-063/D-064 made cheaper; a real number against their synthetic `BenchmarkSessionLoad`/`BenchmarkHistory20Pods` figures |
| Same, mute on (`mute.loggers`) | Previous row | Confirms D-062's "dropped before the buffer" claim for real: RSS/CPU should track the *muted* line rate, not the *emitted* one, and the status bar's `muted N` should move |
| Kafka screen open on `catalog`, Schema Registry decoding Avro + JSON Schema live | No prior real baseline (new feature) | First real number for D-067/D-068's decode cost outside `BenchmarkDecodeCached*`; confirms the registry's connection-pool reuse (D-068: "one decoder per registry… for the whole run") by reopening the screen and checking no new registry request is made |
| Registry made unreachable mid-session (scale Karapace to 0) | D-068's documented fail-fast/retry-once behavior | Real chaos test, same spirit as M12's `kubectl delete pod` on the broker: confirm every pending record fails at once with the documented message, not a per-id timeout stall |
| Responsiveness, loaded + mute + Schema Registry all active at once | Subjective, flagged as such (no key-latency harness exists, same note as M10 §6) | Key-press-to-redraw feel under the combined worst case this session can produce |

## 7. Coverage matrix (the "100%" deliverable)

Same method as M10 §7: one row per README key-table entry, filled in during
execution, not here. This session is expected to move exactly these rows
from `unit/demo only` to `real GKE`:

- Logs screen: `M` (show/hide muted lines).
- Kafka screen: `D` (Schema Registry decode toggle), and the record-list
  `avro N · {…}` / `json N · {…}` display itself.
- Nothing else — every other row was already moved by M10 or M12 and is not
  re-litigated here, same convention as M10 §7's own closing note.

## 8. Lifecycle

Same order as M5/M10/M12, with this session's additions folded into steps
3–5:

1. **Re-verify §1**: billing-account age/remaining credit, current pricing.
2. Confirm the M5 billing budget is still in place (do not recreate).
3. `gcloud container clusters create-auto …` — or reuse a still-up cluster
   (verify; it shouldn't be, per every prior session's teardown discipline).
4. Artifact Registry: rebuild/push the trio and loadgen unchanged (no image
   changes this session); pull Karapace's public image (pin the digest).
5. Apply namespaces/workloads: the trio, loadgen (with its new env var),
   `noisy-fixture`, `kafka-qa`, the two marker pods, **plus** `karapace` and
   `named-pod-fixture` ×2 (§3); widen `rec`'s namespace list (§4).
6. Run the schema-registration Job once (§3.4); confirm it exited 0 before
   proceeding.
7. Extend the config folder (§5): `mute` block, `schema_registry` block,
   `environments.yaml` change.
8. Run the QA session: feature pass against §7's matrix, D-061's real
   same-name-pod check (§3.2), then the NFR pass (§6).
9. **Teardown**, unconditionally, same as every prior session.
10. Record decisions (§9); fold any real-GKE-only finding into
    README/`docs/CONFIG.md`.

## 9. Decisions to record (once run)

- **D-06x/D-07x candidate:** the filled-in §7 coverage matrix; whether the
  real same-name-pod scenario (§3.2) behaves exactly as
  `TestSameNamedPodAcrossNamespaces` predicts; the §6 NFR numbers, especially
  whether muting tracks the *emitted* or *muted* rate as D-062/D-063 claim;
  Karapace's suitability as a stand-in registry if any Confluent-specific
  behavior is found to differ; any new finding.

## 10. Risks / open questions

- **§1's cost-governance precondition is now fully resolved**: 257 EUR
  remaining, expiring 2026-12-26 (confirmed 2026-10-07) — both checked by
  hand, since neither is available via `gcloud`. The only remaining
  scheduling constraint is running this session (§8) well before that date,
  with enough margin for a second attempt if the first hits a snag.
- Karapace's exact REST-auth configuration surface may differ in detail from
  what's sketched in §3.3 (`KARAPACE_REST_AUTHORIZATION` plus a users file,
  confirmed at execution time against its own docs) — the design intent
  (real basic-auth, no new secrets mechanism) is what matters, not the exact
  env var names.
- The exact HikariCP logger name(s) to mute (§5) are confirmed from a real
  captured log line at execution time, not guessed here.
- Whether one ephemeral Job is enough to produce a *bearer_token*-authenticated
  registry case too (§3.3 only designs for `basic_auth`) is left open —
  Karapace's own auth model is basic-auth-shaped; a bearer-token case may
  need a small reverse-proxy in front of it, judged not worth the added
  moving parts unless execution time shows otherwise.

## 11. Done when (this design)

This document exists, cites M5/M10/M12's already-confirmed facts rather than
re-deriving them, and needs no code change to review. Execution — §8 — is a
separate, future session and out of scope here, same convention as
M10-gke-qa.md §11 / M12-gke-qa.md.
