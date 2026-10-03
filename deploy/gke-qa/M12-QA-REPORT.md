# Real-GKE QA report (M12) — Kafka topics against a real broker

Run against [`docs/plan/M12-gke-qa.md`](../../docs/plan/M12-gke-qa.md) on
2026-10-03, binary built at commit `afe236b` (`go1.27.0`,
`CGO_ENABLED=0`), against a fresh real GKE Autopilot cluster `huginn-qa`
(`us-central1`, project `huginn-kube-tui`), with a real single-broker
Kafka (KRaft mode, `apache/kafka:3.9.0`) deployed to it. Host driving the
session: native WSL2 Ubuntu Google Cloud CLI + tmux (TUI), the native
Windows binary (CLI checks only, where paths allowed).

This session's scope was Kafka-only per M12 §3.1's own stated option — the
M10 trio (`payment-service`, `catalog-indexer`, `order-orchestrator`) was
not redeployed this round, to keep the session focused and time-boxed.

## Headline result

**No huginn runtime bugs were found.** Every issue hit this session was
either this session's own test-environment setup (path conventions,
missing local tools, Kubernetes manifest details) or expected real-world
Kafka/GKE platform behavior that Huginn handled correctly. The security-
critical path this session exists to prove — real TLS, real SASL/SCRAM,
real sops+age decryption, a real PKCS12 truststore, real ACL enforcement —
worked end to end on the first fully-configured attempt.

## Setup findings (environment, not huginn)

- **Local Kafka validated in Docker before touching GKE** (de-risking step,
  not in the original design): caught a missing broker-side JAAS config
  for SCRAM (`listener.name.sasl_ssl.scram-sha-512.sasl.jaas.config`) and
  a `kafka-console-producer.sh` quirk (it decodes stdin as UTF-8, so
  genuinely invalid bytes become `U+FFFD` — confirmed by producing one
  record through it and one through a tiny `franz-go` script instead;
  only the latter preserved raw bytes). Both are Kafka/tooling behavior,
  not huginn's.
- **GKE Autopilot + GCE PD**: a fresh PVC's `lost+found` directory (ext4
  default) inside `log.dirs` is fatal to Kafka's `LogManager` — fixed by
  pointing `log.dirs` at a subdirectory of the mount, not the mount root.
  Also needed `securityContext.fsGroup: 1000` to match the image's
  non-root `appuser`, confirmed via `docker run ... id`.
- **Kafka's `allow.everyone.if.no.acl.found` is per-resource, not
  per-principal**: adding a DENY ACL for `qa-user` on `topic-restricted`
  silently also hid it from the unauthenticated admin connection, because
  the resource now has *an* ACL and the "no ACL found" default no longer
  applies to anyone. Fixed with an explicit ALLOW for `User:ANONYMOUS`.
  Confirmed by direct experimentation, not assumed.
- **Autopilot pod rescheduling** moved the broker to a new pod ~1 minute
  after initial deployment (bin-packing optimization) — confirmed the
  PVC-backed data survived intact (ACLs, topics) on the new pod, and that
  `kubectl port-forward` to a `Service` does **not** auto-follow a pod
  change; it must be restarted. Treated this the same way as the later,
  deliberate chaos test (§ below), since it was a free real instance of
  exactly that scenario.
- **Path conventions are OS-specific and that's correct, not a bug**:
  `kafka/` profile paths written `C:/Users/...` resolve fine for the
  native Windows binary and not at all for `bin/huginn-linux` under WSL
  (Go's `filepath.IsAbs` is platform-aware by design); `/mnt/c/...` is the
  reverse. Settled on `/mnt/c/...` throughout `config-kafka/kafka/*.yaml`
  since the TUI can only run under WSL/tmux in this environment (no native
  Windows tmux) — consistent, not a workaround.
- **`sops` was not installed in the fresh native WSL environment** used
  this round (the same gap M10 found and noted, now hit a second time
  because this was a different/reset WSL install) — installed from the
  official release, not from the registry, and should probably just be
  added to whatever base setup future sessions start from.

## Kafka feature coverage, confirmed against real data

### CLI (`huginn kafka check`/`read`) — no TUI, no tmux needed

- `kafka check orders` (SASL_SSL/SCRAM/PKCS12/sops+age): `topic-a`,
  `topic-b`, `topic-c` → `ready`; `topic-restricted` →
  `not authorized: forbidden` — **verbatim match** to M11-kafka.md §6's
  documented text.
- `kafka check catalog` (PLAINTEXT, no secrets): all three topics `ready`.
- `kafka read orders topic-a`: every payload shape in M11-kafka.md §5
  rendered correctly — `key=∅` (null key), `tombstone` (null value),
  `binary 9 B` (genuinely invalid UTF-8, produced via a one-off `franz-go`
  script since the console producer mangles it), `schema 7, 12 B`
  (Confluent framing, schema id extracted correctly, not decoded).
- Wrong SASL password → `credentials rejected by the brokers (SASL
  scram-sha-512, user from connection.sasl.username): unauthorized` —
  verbatim match to §6. (Test fixture deleted after use — see below.)

### TUI (tmux, WSL)

- Services screen: `K` marker present on both `orders` and `catalog`.
- `M` opens the Kafka screen: `CONSUMES`/`PRODUCES`/`TOPICS` grouping
  matches M11-kafka.md §1's own mockup exactly, including
  `topic-restricted` showing its forbidden reason inline in the topic
  list rather than only on open.
- Records screen: all five payload shapes render identically to the CLI.
- Zoom (`enter`): binary → hex dump with ASCII sidebar; schema-framed →
  `schema registry framing, schema id 7: not decoded` followed by the hex
  dump showing the readable payload inside it — exact match to spec.
- Filters: `key=k-1` and `partition=2` both correctly narrowed the view.
- `o` (order): correctly reversed to newest-first.
- `i` (isolation): toggled to `read_committed`; all 8 non-transactional
  records remained visible (correct — isolation only affects aborted
  transactional records, none of which exist in this dataset).
- `ctrl+y`/`y` (copy): confirmed via status-bar message
  (`copied p2 #4 (71 bytes, value)`).

### Chaos test: broker lost mid-follow (real, not simulated)

`kubectl delete pod --force` on the broker while `kafka read --follow`
was active:
- Status progression matched §6 verbatim: `brokers unreachable,
  retrying…` → (after `kubectl port-forward` was restarted against the
  new pod) → `reconnected`.
- Confirmed **no duplicate record**: produced one new record after
  reconnection and it appeared exactly once, correctly resuming from the
  last offset read, exactly as D-057/§6 describe.

### Not covered this session

- `--committed`/`--raw` CLI flags specifically (isolation and raw-copy
  were exercised via the TUI's `i`/`Y`-equivalent instead; the CLI flags
  themselves weren't separately invoked) — low risk, same code path.
- A reusable `e2e-m12.sh` script was not written; this session's TUI/CLI
  checks were ad hoc WSL/tmux commands rather than a committed automation
  script (unlike M10's `e2e-m10.sh`). Worth doing before a round 4, not
  blocking this report.
- OAUTHBEARER/mTLS/JKS and other M11 §10 "not in this version" items are
  correctly out of scope — nothing to test.

## NFR

One real data point: `bin/huginn-linux kafka read orders topic-a --follow`
while a real `kafka-producer-perf-test.sh` burst (20,000 records, 200 B
each, ~2000 rec/s) ran against the same topic:

| Metric | This run (real broker, real burst) | M11 §8 (`--demo`, 20,000 records) |
|---|---|---|
| RSS | 38 MB | 52 MB |
| CPU | 0.9-1.5% | ~3% of a core |

Lower than the demo benchmark on both counts — plausible given different
record size/content and that this measured the CLI path (no TUI frame
redraw cost), not a regression signal either way. No `pprof` profile
captured this round (time-boxed out, noted rather than silently skipped).
Responsiveness: the TUI felt immediately interactive at this record
volume in manual use; no automated key-latency harness exists, so no
fabricated number is given.

## Findings summary

| # | Area | Severity | Status |
|---|---|---|---|
| 1 | GCE PD `lost+found` fatal to Kafka's `LogManager` | environment (GKE+ext4), not huginn | worked around (`log.dirs` subdirectory) |
| 2 | Non-root container needs `fsGroup` for a PVC | environment (Kubernetes), not huginn | fixed in `kafka-workloads.yaml` |
| 3 | `allow.everyone.if.no.acl.found` is per-resource | confirms real Kafka behavior, not a huginn issue | worked around (explicit ANONYMOUS ALLOW) |
| 4 | `kubectl port-forward` to a Service doesn't follow pod rescheduling | environment (kubectl), not huginn | restarted as needed; doubled as a free chaos-test instance |
| 5 | `sops` missing in fresh WSL (second occurrence, different install) | environment, recurring | installed; worth fixing at the base-image/setup level before round 4 |
| 6 | Console producer mangles invalid UTF-8 | Kafka tooling, not huginn | worked around (`franz-go` one-off script) |

No code changes to `internal/` were made or needed this session.
