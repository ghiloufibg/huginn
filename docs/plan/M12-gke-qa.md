# M12 — Real GKE, round 3: Kafka topics against a real cluster  (status: done)

**Goal:** a third real-GKE QA session, extending [M10](M10-gke-qa.md) (itself
extending [M5](M5-gke-qa.md)), to close the one gap neither could have
closed: **the Kafka feature** ([M11](M11-kafka.md), D-057) — read-only
topic screens, the request guard, sops-encrypted credentials, TLS
truststores and the `huginn kafka check`/`read` CLI — **has never been run
against a real broker on a real cluster.** M11 §8's own definition of done
only asks for "a real `rec` topic compared with the original script," not
a full real-infrastructure pass; this session is that pass, in the same
spirit M5 was for real Spring Boot logging and M10 was for M6-M9.

**Status: done.** Executed against a real GKE Autopilot cluster with a
real single-broker Kafka; see
[`deploy/gke-qa/M12-QA-REPORT.md`](../../deploy/gke-qa/M12-QA-REPORT.md)
for the report and [D-058](../DECISIONS.md) for the decision record. This
document is kept as-written for its design rationale; the report is
authoritative on what was actually run, including the Kafka manifests
(`deploy/gke-qa/kafka-workloads.yaml`, `kafka-orders-fixture.yaml`,
`kafka-catalog-fixture.yaml`) and config
(`deploy/gke-qa/config-kafka/kafka/`), which superseded this document's
illustrative sketches where execution needed something more specific
(e.g. `bitnami/kafka` → `apache/kafka:3.9.0`, per §10's own flagged risk).
Supersedes nothing in M10 (which stays "done," a historical record of
what it actually ran) — this was the next round, reusing M10's
cluster/workload baseline where sensible and adding Kafka specifically.
PetClinic was not used, per the explicit decision already made for M10
(lower risk, no new external app, no new Artifact Registry image) and
reaffirmed for this
round — no new external application here either, beyond the Kafka broker
itself (self-hosted, not an "application" in the PetClinic sense).

## 0. What this reuses, what is new

| Already covered — **not** repeated here | New — needs this session |
|---|---|
| Real GKE auth, RBAC forbidden-namespace, GCP-KMS `namespace_from` (M5) | A real, self-hosted Kafka broker on GKE — GKE has no managed Kafka product, so this is new ground for every prior round |
| M6-M9 (field transforms, trace view, field filters, select/copy/save/redact), real NFR baseline (M10) | The Kafka screen end to end: `K` marker, `M` key, topics/records/zoom, `/` filter forms, `0`/`1`-`7`/`f`/`space`, `o`, `ctrl+y`, `i` isolation |
| Cost governance, Autopilot choice, billing budget, teardown discipline (M5 §1, reconfirmed live in M10) | `kafka/` profile resolution against **real** sops-encrypted dotenv sources and a **real** PKCS12 truststore — the local-files/SASL/TLS path has only ever run against `kfake` (in-memory fake broker) in unit/contract tests |
| — | The wire-level request guard (D-057) behaving correctly against a **real** broker's actual request/response traffic, not a fake one |
| — | `huginn kafka check`/`read` CLI against a real broker |
| — | A real NFR number for following a live Kafka topic, comparable to M11 §8's `--demo`-only benchmarks (3% CPU/52MB following 20,000 records) |

## 1. Cost governance

Unchanged facts from M5 §1/M10 §1: same `huginn-kube-tui` project, same
$300/90-day free-trial hard cap (architecturally impossible to overspend
on a trial billing account), same billing budget (already created live
during M10 — **do not recreate**, confirm it's still there). Re-verify
current Autopilot/e2 pricing before provisioning, same standing caveat.

**What's new to size:** a single-broker Kafka deployment needs real memory
(JVM heap + page cache) — realistically 1-2 GiB request, a genuinely
different resource shape from every pod in M5/M10 (100m cpu/256Mi-384Mi).
This **will** trigger a new Autopilot node-pool bucket, unlike M10's
fixable case. The lesson from M10 isn't "never use a new shape" — it's
"know which quota bucket it lands in before it's Pending." M10's failure
was specifically `PREEMPTIBLE_CPUS` (limit 0); general on-demand quotas
(`E2_CPUS` limit 24, `N2_CPUS` limit 200, both at 0 usage after M10's
teardown) had headroom. Re-check `gcloud compute regions describe
us-central1` for exactly these metrics before provisioning this round,
and if Autopilot defaults any part of this workload to Spot, pin it to
on-demand explicitly (Autopilot compute class / node selector) rather
than discovering a second quota wall live.

## 2. Cluster design

Unchanged from M10 §2 (itself unchanged from M5 §2): GKE Autopilot,
`us-central1`. Reuse the region; a fresh cluster (M10's was torn down per
its own lifecycle discipline).

## 3. Workloads

### 3.1 Reused from M10, unchanged

`payment-service`, `catalog-indexer`, `order-orchestrator`,
`payment-service-loadgen`, `noisy-fixture` — redeployed as-is if a broad
regression pass is wanted alongside the Kafka-specific work; **optional**
for this round, since M10 already proved them and this session's scope is
Kafka. Cheapest: skip them entirely and keep this session Kafka-only,
deciding at execution time based on remaining budget/time.

### 3.2 New: a single-broker Kafka, KRaft mode, two security tiers

One broker, not a cluster of brokers — multi-broker replication/leader
election is not what this feature reads (it only ever does `Fetch` and
`ListOffsets` against whichever broker is the leader; D-057 does not touch
replication). KRaft mode (no ZooKeeper) is the current default deployment
shape, with its own leaner resource footprint.

- **Image**: `bitnami/kafka` (public, Apache-2.0, widely used for exactly
  this single-node test-cluster shape) — env-var-driven SASL/SCRAM and TLS
  configuration, avoiding hand-written `server.properties` and a custom
  keystore-generation script. Real behavior under test is the *client*
  side (Huginn's dialer, guard, SASL exchange, truststore loading), not
  the broker's own implementation, so the broker image's provenance
  matters less than the protocol it actually speaks on the wire — which
  is real Kafka wire protocol either way.
- **Two topics tiers, matching M11-kafka.md §1's own screen mockup**
  (`topic-a` consume, `topic-b` produce, `topic-c` no direction) so the
  CONSUMES/PRODUCES/TOPICS grouping is exercised exactly as designed:
  - **Tier 1 — `PLAINTEXT`**, one broker listener, no SASL: covers the
    bulk of the feature (topics, records, zoom, filters, tail/window/
    follow/pause, order, copy, isolation) at minimum setup complexity —
    the "cheap trick where the real behavior isn't under test" half of
    this design, consistent with M5/M10's own discipline.
  - **Tier 2 — `SASL_SSL`, SCRAM-SHA-512, a self-signed CA** on a second
    listener/topic: this is where real infrastructure genuinely matters —
    real TLS negotiation, a real SASL exchange on the wire, a real PKCS12
    truststore Huginn must parse, and real sops-encrypted credentials
    Huginn must decrypt. Everything Tier 1 already covers at the UI level
    doesn't need repeating here; Tier 2 exists specifically to prove the
    security-critical path the unit/contract tests (against `kfake`,
    never a real TLS stack) cannot.
- **Record variety** (produced via the broker image's own
  `kafka-console-producer.sh`/`kafka-producer-perf-test.sh`, run as a
  one-off Job — no new image, no new app): plain JSON values across
  multiple partitions (merged-by-timestamp rendering), a null key (`∅`),
  a null value (`tombstone`), a non-UTF8/binary value (hex dump path), and
  one Confluent-framed value (`0x00` + 4-byte schema id, to exercise the
  `schema <id>, N B` label without decoding it — M11-kafka.md §5
  explicitly does not decode Avro/Protobuf, only labels the framing).
- **One ACL-restricted topic** (Kafka's built-in simple authorizer),
  denied to the QA account, mirroring `qa-restricted`'s real-RBAC-forbidden
  shape already used for Kubernetes namespaces (M5 §4) — now for a Kafka
  topic's `not authorized` failure mode (M11-kafka.md §6).

### 3.3 Why no PetClinic, still

Confirmed unnecessary here too: Kafka's own test surface is about the
*client-side* protocol handling (guard, TLS, SASL, truststore, sops), not
about having a "real, well-known public application" producing the
records — a controlled producer that deliberately emits every payload
shape §5 of M11-kafka.md describes (JSON, binary, tombstone, null key,
schema-framed) is a **more thorough** test of the rendering path than
whatever JSON a real Spring Boot app happens to produce, and it costs
nothing extra to construct.

## 4. Namespace, identity, and secrets — and the M10 `kms` lesson applied

- **Namespace**: `qa-rec` reused, or a new `qa-kafka` namespace if run as
  a standalone Kafka-only session (§3.1) — execution-time choice.
- **No Kubernetes RBAC change needed for Kafka itself.** Per D-057/M11 §2.5,
  "nothing on the cluster side": Huginn reaches the broker directly
  (a `Service`/port-forward or a `LoadBalancer`/`NodePort` for the
  workstation to dial), not through the Kubernetes API, so this is not
  gated by `huginn-reader`'s namespaced RBAC at all.
- **Credentials: `sops` + `age`, not GCP KMS.** This is the direct fix for
  M10's deferred `kms` environment, which was blocked by an org policy
  against service-account key creation. D-003 already establishes that
  `sops` is shelled out and reuses "the user's existing key setup" —
  nothing about the Kafka feature requires GCP KMS specifically. An
  `age` keypair is local, asymmetric, needs no GCP IAM grant, no service
  account, and no key-creation permission at all — it sidesteps the
  blocked policy entirely rather than working around it. The SASL
  username/password and truststore password for Tier 2 go in a
  `kafka.env` dotenv, sops-encrypted with the `age` public key, decrypted
  locally by Huginn exactly as D-057/K0 already implements.
- **Truststore**: a self-signed CA generated for Tier 2, converted to
  PKCS12 (`openssl` + `keytool`, or `go-pkcs12` directly, execution-time
  choice) — a few commands, no new infrastructure.

## 5. Huginn config folder

New `deploy/gke-qa/config/kafka/` (or a fresh folder if run standalone,
§3.1), one profile per tier, named so a more specific one is tried first
(`docs/CONFIG.md`'s file-name-order rule — the exact mistake M10 made and
fixed for `formats/`, not repeated here):

```yaml
# kafka/10-qa.yaml — sketch, illustrative
version: 1
match:
  files: ["{repo_dir}/kafka-tier2.env.sops"]   # or a simpler match.repos for a standalone session
sources:
  - { file: "{repo_dir}/kafka-tier2.env.sops", sops: true }
connection:
  bootstrap: ${KAFKA_BOOTSTRAP}
  security: sasl_ssl
  sasl: { mechanism: scram-sha-512, username: ${KAFKA_USER}, password: ${KAFKA_PASSWORD} }
  tls: { ca: "{repo_dir}/truststore.p12", ca_password: ${KAFKA_TRUSTSTORE_PASSWORD} }
topics:
  consume: [topic-a]
  produce: [topic-b]
  list: [topic-c, topic-restricted]
```

```yaml
# kafka/20-plaintext.yaml — Tier 1, no secrets at all
version: 1
match: { repos: ["*"] }
connection: { bootstrap: ${KAFKA_PLAINTEXT_BOOTSTRAP}, security: plaintext }
topics:
  consume: [topic-a]
  produce: [topic-b]
  list: [topic-c]
```

`huginn.yaml` gains the optional `kafka:` block only if any default
(`tail_records`, `max_records`, etc., M11-kafka.md §4.5) needs overriding
for this session — otherwise the shipped defaults are exactly what should
be exercised.

## 6. Coverage matrix (the "100%" deliverable, extended)

M10 §7 already established the method: a table of every README key,
`Lab only` / `M5` / `M10` / `This session`. This session adds the Kafka
rows that did not exist before M11 shipped: `K` marker, `M`, `enter` zoom,
`/` and its three forms (text, `key=`, `partition=`, `header.<name>=`),
`0`, `1`-`7`, `f`, `space`, `o`, `ctrl+y`, `i`, `esc`, and the CLI's
`huginn kafka check`/`read` with `--tail`, `--since`, `--follow`,
`--committed`, `--raw`. The CLI is the cheapest of these to automate (no
tmux keystroke choreography — run it, capture stdout and exit code), and
should carry most of this session's scripted coverage; the TUI-specific
rows (the `K` marker's presence, screen navigation, zoom rendering) need
the tmux approach M10 already established.

Failure modes from M11-kafka.md §6 worth reproducing for real, cheaply:
wrong truststore password, a nonexistent topic, SASL rejected (a
deliberately wrong password), `not authorized` (the ACL-restricted topic,
§3.2), and brokers lost mid-read (`kubectl delete pod` on the broker while
following — the same "break it for real" approach M5 already used for
OOM and crash loops, D-057's retry/reconnect behavior gets a real test
instead of only `kfake`'s simulated disconnect).

## 7. NFR

Same methodology as M10 §6 (`ps` sampling, `HUGINN_CPUPROFILE`), now
against a real broker: follow a real topic under sustained production
(`kafka-producer-perf-test.sh` at a tuned rate) and compare against M11
§8's own `--demo` numbers (3% CPU / 52 MB following 20,000 records, paused
<1%) — the same "real vs. synthetic, not a regression target" framing
M10 used against the lab's `bulk-emitter` baseline. Responsiveness:
same honest, explicitly-subjective note as M10 §6 (no automated
key-latency harness exists).

## 8. Lifecycle

1. Confirm the M10 billing budget is still in place (do not recreate).
2. `gcloud container clusters create-auto …` — check `E2_CPUS`/`N2_CPUS`
   quota first (§1), pin on-demand if Autopilot would default to Spot.
3. Generate the `age` keypair, the self-signed CA + PKCS12 truststore, and
   the sops-encrypted `kafka.env` (§4) — all local, no GCP IAM involved.
4. Deploy the broker (Tier 1 + Tier 2 listeners), create the topics
   (including the ACL-restricted one), produce the record variety (§3.2).
5. Write `kafka/` profiles (§5); `huginn --config … kafka` screens and
   `huginn kafka check`/`read` CLI against both tiers.
6. Run the QA session: coverage matrix (§6) via CLI-first automation,
   then the TUI-specific rows via tmux; the NFR pass (§7); fill in both.
7. **Teardown**, unconditionally: delete the cluster; the `age` key and
   truststore are local files, delete them too (no GCP KMS key to
   schedule-destroy this time — one fewer cleanup step than M5/M10).
8. Record a decision and fold any real-broker-only finding into
   `docs/CONFIG.md`'s Kafka section or README.

## 9. Decisions to record (once run)

- **D-05x candidate:** Bitnami Kafka (KRaft, single broker) as the
  real-broker QA fixture; `sops`+`age` as the concrete, non-GCP-KMS fix
  for credential-file testing (closing the gap M10's D-052 left open);
  the filled-in Kafka coverage matrix; real NFR numbers against M11 §8's
  `--demo` baseline; any new finding.

## 10. Risks / open questions

- The broker's resource shape is new (§1) — verify quota before
  provisioning, not after a `Pending` pod repeats M10's exact mistake.
- Producing a genuinely raw Confluent-framed byte sequence (§3.2) may need
  a small one-off script (`kcat -P` or a few lines of `printf`/`nc`)
  rather than the console producer, which is text-line-oriented — an
  execution-time detail, not a blocker.
- Whether to also redeploy the M10 trio (§3.1) for a combined regression
  + Kafka session, or keep this session Kafka-only, is left open — purely
  a cost/time trade-off, not a design dependency.
- ACL configuration on `bitnami/kafka` (SASL + authorizer together) is
  unverified in detail — confirm the exact env vars against the image's
  current documentation before relying on it for the `not authorized`
  failure-mode check, same "verify, don't assume" discipline as every
  prior round's cost/pricing claims.

## 11. Done when (this design)

This document exists, reuses M5/M10's already-confirmed facts rather than
re-deriving them, and needs no code change to review. Execution is a
separate, future session and out of scope here, same convention as
M5-gke-qa.md §11 and M10-gke-qa.md §11.
