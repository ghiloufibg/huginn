# M14 QA Report — Real GKE, round 4

Executed 2026-10-07 against a real GKE Autopilot cluster in the
`huginn-kube-tui` project. Design: [`docs/plan/M14-gke-qa.md`](../plan/M14-gke-qa.md).
Full teardown confirmed at the end (§7).

## 1. What actually ran

- **Region changed from the design's `us-central1` to `us-east1`**, live,
  mid-session: `us-central1` returned a real `GCE_STOCKOUT` on the very
  first `create-auto` attempt. The partially-created cluster (3 nodes,
  `ERROR` state) was real, running, billable compute and was deleted
  immediately on discovery, before anything else. All manifests and
  `environments.yaml` contexts were updated to `us-east1` for this session
  and left that way (§8).
- The M5/M10 trio (`payment-service`, `catalog-indexer`, `order-orchestrator`),
  `payment-service-loadgen`, `noisy-fixture`, the single-broker Kafka, and
  the `catalog`/`orders` marker pods — rebuilt and redeployed (Artifact
  Registry had been fully torn down since M12; nothing was reusable).
- **New this session**: `named-pod-fixture` (a `StatefulSet`, not a
  `Deployment`, deployed identically into `qa-rec` and `qa-restricted` so
  both pods are named `named-pod-fixture-0`); Karapace (Schema Registry);
  one ephemeral Go tool
  ([`deploy/gke-qa/schema-registration/main.go`](schema-registration/main.go))
  that registered a real Avro schema (with `decimal` and `timestamp-millis`
  logical types) and a real JSON Schema with Karapace, then produced real
  Confluent-wire-format records onto the `catalog` repo's existing topics.
- `huginn-reader` (IAM service account + its RBAC) was recreated — deleted
  at M12's teardown, same as M10 found it deleted after M5's.

## 2. Findings

### 2.1 huginn issues

None found. Every behavior under test (muted loggers, Schema Registry
decoding, the pod-key fix, RBAC) matched its documented design exactly on
first real exercise, with zero code changes needed during this session.

### 2.2 Real infrastructure/tooling issues, not huginn

| # | Issue | Real impact | Resolution |
|---|---|---|---|
| 1 | `us-central1` `GCE_STOCKOUT` on `create-auto`, leaving a real, billable, partially-created cluster behind despite the command reporting failure | Real compute cost until noticed | Deleted on discovery; retried in `us-east1`, which succeeded |
| 2 | **`gcloud container clusters get-credentials --impersonate-service-account` silently shares the same kubeconfig user-entry name as a plain (non-impersonated) `get-credentials` call for the same cluster.** Running the plain call *after* the impersonated one overwrote the impersonated credentials in place — the `huginn-reader` *context* still existed and still had the right name, but it silently pointed at the admin's own credentials. `kubectl --context=huginn-reader get pods -n qa-restricted` returned real data instead of `Forbidden`. | A real, previously-undocumented footgun: easy to believe an RBAC boundary is verified when it silently is not, with no error at any point | Extracted the impersonated `exec` config (`kubectl config view --raw`) into a distinctly-named user entry (`huginn-reader-user`) via `kubectl config set-credentials`, then pointed the `huginn-reader` context at that name before ever running the plain call again. Re-verified: `qa-rec` allowed, `qa-restricted` → real `Forbidden`, through both raw `kubectl` and the real TUI (`restricted` environment, status bar: `namespace qa-restricted: forbidden`) |
| 3 | Karapace's own `karapace` console script is broken across every published image tag tried (`latest`=6.2.4, `6.2.3`, `5.0.1`): `ImportError: cannot import name 'main' from 'karapace.__main__'` — a real upstream packaging bug, not a config error | Schema Registry container would `CrashLoopBackOff` indefinitely with any config | Found the real entrypoint by reading `__main__.py`'s own source inside the image: it is meant to be run as `python3 -m karapace` (its own `if __name__ == "__main__":` block), not through the stale console-script wrapper. Pinned to `5.0.1` (older, more conventional dependency set) with `command: ["python3", "-m", "karapace"]` |
| 4 | **The Kafka broker's `advertised.listeners=PLAINTEXT://localhost:9092`** (a deliberate M12 choice so an *external* client reaching the broker only via `kubectl port-forward` sees a consistently-resolvable address) **breaks any in-cluster client.** Karapace, running inside the cluster, received `localhost:9092` as the broker's advertised address from the initial bootstrap metadata response and then tried to reconnect to itself | Schema Registry could never actually read or write its backing Kafka topic | Added a fourth, dedicated listener (`INTERNAL://:9095`, advertised as `kafka-qa:9095` — the real in-cluster service DNS name) alongside the existing three, purely for in-cluster clients; the external, port-forwarded path (`PLAINTEXT`/`SASL_SSL`, still advertised as `localhost`) is unchanged. This is a real, generalizable lesson for any future in-cluster QA workload added to this broker |
| 5 | `config-kafka/kafka/*.yaml`'s `sources.file`/`tls.ca` paths were hardcoded WSL-absolute (`/mnt/c/Users/PC/...`), from a session that ran Huginn's CLI from inside WSL. Running the natively-built `huginn.exe` from Windows failed to resolve them | A config folder checked in from one session silently didn't work from a different, equally valid execution environment | Changed to paths relative to the config folder (`../kafka-secrets/...`) — portable across WSL and native Windows, and no longer tied to one user's absolute home path |
| 6 | `kubectl exec ... -- /opt/kafka/bin/kafka-topics.sh` failed under Git Bash: MSYS auto-converts leading `/opt/...` into a Windows path before it ever reaches `kubectl` | Topic creation inside the broker pod failed with a nonsensical `C:/Program Files/Git/opt/kafka/...` path | `MSYS_NO_PATHCONV=1` prefix; same fix applied everywhere a literal in-container absolute path was passed through this shell |
| 7 | `kubectl logs --since=15m` for one specific pod (`payment-service-loadgen`'s, 5 restarts) hung indefinitely (>60 s, never returned) | The TUI's own logs screen for that repository also hung on "loading 15m" — confirmed to be the same root cause, not a huginn bug, by reproducing the identical hang with raw `kubectl logs` against the same pod/window | Not resolved within this session's time box; noted as an open question (§5). Every other pod's history loaded normally, including `catalog-indexer` and `order-orchestrator`'s own continuous crash loops |

## 3. Feature coverage confirmed for real

- **D-061, pod-key collision (the primary target of this session)**:
  `named-pod-fixture-0` in `qa-rec` and `named-pod-fixture-0` in
  `qa-restricted` — same name, same repository (`app.kubernetes.io/part-of:
  named-pod-fixture`), different namespaces. The services screen correctly
  showed **2 workloads, 2/2 pods** (not 1, not a silent overwrite); opening
  its logs showed both real streams correctly merged by time, alternating
  `from qa-rec` / `from qa-restricted` every ~5s with no gap, no duplicate,
  no drop. Confirms `TestSameNamedPodAcrossNamespaces` against a real API
  server, not a fake.
- **D-062, muted loggers**: `catalog-indexer`'s real crash loop was checked
  first (`kubectl logs`) to confirm the *exact* real `logger_name` —
  `com.zaxxer.hikari.HikariDataSource` — rather than guessing one in
  advance (the design's own instruction); `com.zaxxer.hikari.pool.HikariPool`
  was *not* actually present in the real output, confirming the design's
  own caution not to assume. With it muted, the status bar read `muted 1`
  and the `HikariPool-1 - Starting...` line was absent from the default
  view; `M` revealed it, in its correct chronological position. The `keep:
  [error]` safety valve was not separately exercised, since the real muted
  line is `INFO`, not `ERROR` — correct, honest scope for what this session
  actually tested, not overclaimed.
- **D-067/D-068, Schema Registry**: `huginn kafka check catalog` reported
  `schema registry: ready` against the real Karapace. `huginn kafka read
  catalog topic-a` decoded three real Avro records with logical types
  rendered exactly as documented — `"total":"19.99"` (decimal),
  `"placedAt":"2026-10-07T20:07:11.273Z"` (timestamp-millis, ISO 8601) —
  and `huginn kafka read catalog topic-c` decoded two real JSON Schema
  records (`json 2 · {...}`). A record framed with a bogus schema id
  (999999) produced the exact documented error: `registry answered 404
  Schema not found`. A record with a plain big-endian-numeric key (no
  framing) was correctly left as `binary 8 B`, confirming the default
  `decode: [value]` does not touch keys. `--no-decode` correctly fell back
  to `schema <id>, N B` labels. `basic_auth`/`bearer_token` were **not**
  exercised — Karapace was deployed without REST auth in this session,
  given time already spent on the two real bugs in §2.2 (open item, §5).
- **RBAC boundary, for real, through the fixed kubeconfig (§2.2 #2)**: the
  `restricted` environment's status bar read `namespace qa-restricted:
  forbidden` — the genuine Kubernetes API server response under the
  properly-isolated `huginn-reader` identity, not the silently-wrong
  admin-credentials result this session initially got before finding and
  fixing the kubeconfig collision.
- **Continuity**: `catalog-indexer`'s Hikari/JDBC crash loop and
  `order-orchestrator`'s real `OOMKilled` (exit 137) both reproduced exactly
  as in M5/M10, confirming this fresh cluster is the same class as prior
  sessions.

## 4. NFR snapshot

One real data point, not a full soak (time-boxed session):

| Scenario | RSS | CPU time (wall) |
|---|---|---|
| ~2 min after connecting, services + a muted/unmuted logs pass on `catalog-indexer`, trio + loadgen + fixtures all live | 42 MiB | 8s |
| ~4 min in, after switching environments twice (`rec` → `restricted` → `rec`) and opening `payment-service`/`payment-service-loadgen` logs | 72 MiB | 17s |

Node-level, at the same point: `445m`/`23%` CPU and `3075Mi`/`51%` memory on
the node carrying the trio + fixtures; `308m`/`3%` and `3151Mi`/`11%` on the
node carrying Kafka/Karapace/the marker pods — no sign of resource pressure
during this session, consistent with the §2.2 #7 hang being pod/kubelet-side
rather than cluster-wide starvation. Responsiveness: subjective, felt
normal throughout (no automated key-latency harness, same honest caveat as
every prior session).

## 5. Not covered / deferred

- `schema_registry.basic_auth`/`bearer_token` (Karapace deployed without
  REST auth this round).
- The `kms` environment and the tier-2 SASL/SCRAM Kafka profile (`orders`
  repo) were not re-exercised this session — continuity with M10/M12's
  already-confirmed coverage was assumed, not re-verified, given time spent
  on the real bugs in §2.2.
- §2.2 #7's `kubectl logs` hang for one specific pod was reproduced but not
  root-caused within this session's time box.
- The §6 NFR comparison against D-063/D-064's own synthetic benchmark
  figures (the design's original ask) was not completed to that level of
  rigor — §4 above is a real but modest data point, not a load test at the
  rates those benchmarks used.

## 6. Decision

See [D-069](../../docs/DECISIONS.md) for the recorded decision.

## 7. Teardown

Confirmed clean: cluster deleted (`huginn-qa`, `us-east1`), Artifact
Registry repo deleted, `huginn-reader` service account deleted, no stray
Compute Engine instances, zero clusters/repos remaining in either region.
