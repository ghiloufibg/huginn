# M15 QA — Real GKE: absolute time range and last deployment time (functional, non-functional, TUI non-regression)  (status: design, not executed)

**Goal:** run two features against a real GKE Autopilot cluster and a real Kafka broker, in one session, the way M5/M10/M12/M14 did for earlier features:

- **M15, absolute time range** ([`M15-absolute-time-range.md`](M15-absolute-time-range.md), [D-071](../DECISIONS.md)): `[From, To)` on the logs and Kafka screens, read in pages.
- **M15.1, last deployment time** ([`M15.1-last-deployment.md`](M15.1-last-deployment.md), [D-072](../DECISIONS.md)): a DEPLOYED section in the services preview.

One session, not two: both need a cluster that costs money and time to build, both are checked by the same differential run against the pre-change binary, and M15.1 has a **15-minute wait** (V-2) that the M15 range tests can fill (§9).

The tests prove three things the unit tests, the in-memory `kfake` broker and `--demo` cannot:

1. **Functional.** A range returns exactly the lines (records) of `[From, To)`, in order, and paging through a long range loses nothing and repeats nothing, against a real kubelet, a real API server and a real broker. **The preview's deployment time is the controller's real completion time, and moves for a deployment (new image, restart, rollback) and for nothing else** (scaling, a deleted pod, an autoscaler), against real Deployments, StatefulSets, a DaemonSet and a CronJob.
2. **Non-functional.** Memory stays bounded by one page whatever the range, a full page really stops the read, a page far into a log costs what it costs (**the one measurement D-071 left open**), and nothing leaks, hangs or crashes when the network, a pod or a broker disappears mid-read. The deployment time needs **no permission and no request the tool did not already use**, appears within seconds of a rollout finishing, and costs nothing measurable to render for a large service.
3. **Non-regression of the TUI.** Everything that worked on the real cluster in M5–M14 still works, checked three ways: the existing real-cluster e2e scripts unchanged, a re-run of the README key table on the paths M15 and M15.1 touched, and a differential run of the **pre-change binary against the new binary** on the same cluster.

This file is a design. Execution is a separate session (§9) and nothing here has been run, except the read-only checks of §1.

## 0. What this reuses, what is new

| Already covered — **not** repeated | New — needs this session |
|---|---|
| GKE Autopilot cluster, `create-auto`, Artifact Registry, RBAC, `huginn-reader`, kubeconfig handling (M5, M10, M14 §2.2 #2) | A fixture whose every line is known in advance, so a range can be checked against ground truth (§2.1) |
| Spring trio, crash loop, OOM, `payment-service-loadgen`, `noisy-fixture`, sidecars (M5, M10) | Ranges over crash-looping and restarted pods, over pods deleted mid-range, over rotated logs (§4, FR-5) |
| Single-broker Kafka, SASL/SCRAM, TLS, request guard, broker loss (M11, M12) | A Kafka topic with known timestamps, **out-of-order ones included**, many partitions, read by range and by page (§2.3, FR-8) |
| Karapace + Avro/JSON Schema decoding (M14) | Decoding inside a range and inside pages (FR-8.6) |
| Same-name pods in two namespaces (M14 `named-pod-fixture`) | The same fixture read through a range (FR-4) |
| `e2e.sh`, `e2e-m10.sh`, `e2e-m12.sh` (tmux-driven) | Run **unchanged** as the real-cluster regression suite (§6.2) |
| `ps`/`HUGINN_CPUPROFILE` sampling (M10 §6) | Per-process bytes read (`/proc/<pid>/io`) and time to first page by depth (§5) |
| Services preview: WORKLOADS, PODS, WARNINGS, SIDECARS, events read on rest (M5, D-028) | The DEPLOYED section against **real rollouts** (image change, restart, rollback, stuck rollout), which `--demo` cannot produce (§4b) |
| A pod's `Created`, `Revision` label and owner as the real API server returns them (M5, D-034) | The Deployment's `Progressing` condition: whether `lastUpdateTime` stays fixed after a rollout completes (**V-2, the one assumption the design could not check**) |
| `huginn-reader`'s namespace-scoped read-only RBAC (M5, M14) | That the preview needs nothing more than it already has: no ReplicaSet and no ControllerRevision right (V-8) |

## 1. Preconditions and cost governance

**Checked today (2026-11-01), read only, nothing created:**

| Check | Result |
|---|---|
| `gcloud` | 586.0.0, logged in as `bg.ghiloufi@gmail.com` (active); `huginn-reader@huginn-kube-tui.iam.gserviceaccount.com` also has credentials on this machine |
| Project | `huginn-kube-tui`, billing enabled (`billingAccounts/010F0D-062D60-7836F6`) |
| Clusters | none (`gcloud container clusters list` is empty): the M14 teardown held |
| Artifact Registry | no repository: the trio's images must be rebuilt and pushed again (as M14 did) |

**Not checkable from the CLI, to do by hand in the Console before anything is created** (same as M14 §1): remaining credit and expiry. Last known: 257 EUR, expiring 2026-12-26, confirmed 2026-10-07. That leaves **55 days from today** at most, and an unknown balance after M14's own spending. Re-verify current Autopilot pricing on the GKE pricing page; no cost figure is estimated here because none has been checked.

**What this session adds over M14's set:** two tiny `busybox` workloads for M15 (the emitter, §2.1; the flood, §2.2), one Kafka topic and one ephemeral Job to fill it (§2.3), and for M15.1 a handful of tiny workloads, all `busybox` with minimal requests: two Deployments, two StatefulSets, a DaemonSet, a CronJob and an optional HorizontalPodAutoscaler (§2.6). Nothing with a public address (Kafka and Karapace stay behind `kubectl port-forward`, M11 §2.5), nothing persistent.

**Two Autopilot unknowns that decide what M15.1 can cover, checked in step 1 of §9 and not assumed:** whether Autopilot accepts a DaemonSet of the requested shape (it runs one pod per node and is billed per pod), and whether a CPU-based HorizontalPodAutoscaler gets metrics there. If either is refused, the matching case is reported as *not exercised* (§8), not simulated. This session also keeps the cluster for roughly 40 minutes longer than a pure M15 session, because of V-2's wait; that wait overlaps the range tests, so the extra cost is the small fixtures running that long, not a longer session (§9).

**Rules carried over from M14 (each cost someone time):**
- After **any** failed `create-auto`, list clusters: M14 found a half-created billable cluster after `GCE_STOCKOUT`. Use `us-east1` (M14 moved there after a stockout in `us-central1`).
- Create the impersonated kubeconfig user with its own name **before** any plain `get-credentials`, then prove the boundary (`qa-restricted` → `Forbidden`) through raw `kubectl` first (M14 §2.2 #2).
- Run from WSL with `CLOUDSDK_CONFIG` pointing at the Windows gcloud config, `MSYS_NO_PATHCONV=1` for any Git Bash command (M12, M14 §2.2 #6).
- Teardown is unconditional (§9): cluster, Artifact Registry repo, `huginn-reader`; then list clusters in both regions again.

## 2. Fixtures

### 2.1 `range-emitter` — a log whose content is known in advance

A `StatefulSet` of 2 replicas in `qa-rec` (deterministic names `range-emitter-0/1`), `part-of: range-emitter`, plus one replica in `qa-restricted` (same name `range-emitter-0`, FR-4). A `busybox` shell loop; each line is one JSON object in the shape of `formats/20-spring-json.yaml`, so the existing layout and mute work:

```
{"level":"info","logger_name":"qa.range.steady","message":"pod=range-emitter-0 seq=000123 mode=steady"}
```

- `seq` is a per-pod counter that never repeats and never resets while the container lives; the **message carries it, the kubelet adds the time**. Lines are therefore checkable by `seq` alone, with no parsing of timestamps from Huginn's output.
- **Three modes, switched by an env var** (so one image covers every case): `steady` (a fixed rate, calibrated at execution to about 40 lines/s per pod, so two pods fill a 1 000-line page in about 12 s); `burst` (several lines written in one `printf`, to see whether the runtime stamps them with **identical instants**, the case the page cursor exists for); `quiet` (one line every 30 s, for empty and near-empty ranges).
- A second logger name, `qa.range.noisy`, every Nth line, to exercise `mute.loggers` inside a range.
- **Whether identical instants occur on a real node is unknown** and decided by measurement (count repeated timestamps in `kubectl logs --timestamps`). If they never occur, the same-instant page boundary stays proven by unit tests only, and the report says so.

### 2.2 `range-flood` — to force log rotation

One `busybox` pod writing on the order of 200 KB/s (calibrated), to make the kubelet rotate its container log within minutes. First step of the session, before relying on it: read the node's real rotation limits if Autopilot allows (`/configz` through the node proxy) and otherwise infer them from how the first line of `kubectl logs` moves. **Do not assume** the limits (GKE's defaults are not verified here).

### 2.3 `range-topic` — records whose time is known

An ephemeral Job (the pattern of M14 §3.4, extending `schema-registration/main.go` or a sibling program that uses the same `franz-go`) writes onto a new topic of the `catalog` profile (one added line in `kafka-catalog-fixture.yaml`):

| Property | Why |
|---|---|
| 6 partitions, about 6 000 records, timestamps **set explicitly** by the producer at a fixed pace | the oracle can compute the exact answer for any range |
| About 2% of records stamped 90 s **in the past** (a producer with a slow clock), spread over every partition | out-of-order timestamps, the case a broker's time lookup answers only approximately |
| One partition with **no record** inside the range under test | empty partition inside a range |
| A second topic with `message.timestamp.type=LogAppendTime` | broker-assigned time, the other path of `ListOffsets` |
| A tombstone, a record with a key and headers, a few Avro-framed records | rendering inside a range; Schema Registry inside pages |

`kafka.max_records` is set to the smallest value the config accepts (to confirm against `internal/config`), so 6 000 records make at least 3 pages.

### 2.4 Reused unchanged

The trio, `payment-service-loadgen`, `noisy-fixture`, `kafka-qa`, `karapace`, the two marker pods, `named-pod-fixture` ×2. Redeployed as they were so M10/M12/M14 numbers stay comparable. `payment-service` is also the **one realistic rollout** of M15.1 (V-1): a JVM with real readiness probes, as opposed to the cheap `busybox` fixtures below.

### 2.5 Config folders

- `deploy/gke-qa/config` and `config-kafka`: **unchanged**, used for the regression suite and for the NFR runs at the default `logs.buffer_lines` (50 000).
- `deploy/gke-qa/config-m15` (new, a small override): `logs.buffer_lines: 1000` (the minimum the config accepts), so a few thousand lines already make several pages, plus the `mute` block of M14 pointed at `qa.range.noisy`. The M15.1 feature needs no config: it has no key (the annotation source was left out of the first cut, D-072).

### 2.6 Fixtures for the last deployment time ([M15.1](M15.1-last-deployment.md))

Everything is `busybox:1.36` running a trivial loop, requests at the minimum the cluster accepts, in `qa-rec` (and one in `qa-restricted` for V-15). Each has its own `app.kubernetes.io/part-of` so it is its own row on the services screen, except `deploy-pair`.

| Workload | Kind | Why it exists |
|---|---|---|
| `deploy-probe` | Deployment, 2 replicas, `progressDeadlineSeconds: 120`, `minReadySeconds: 5` | The workhorse of V-1…V-6, V-11, V-14. A rollout is a change of an env var in the template (`REV=2`, `3`, …): a real template change, no registry push, done in seconds. The deadline is set low on purpose so a stuck rollout (V-6) reaches `ProgressDeadlineExceeded` in 2 minutes instead of the default 10 |
| `deploy-sts` | StatefulSet, 2 replicas, `RollingUpdate` | Pods-only source (`pods since …`), V-5 (deleting every pod moves it, the documented lower bound), V-7 |
| `deploy-ds` | DaemonSet | V-7. **Autopilot may refuse or constrain it** (see §1): if so, not exercised |
| `deploy-cron` | CronJob every 5 minutes, `successfulJobsHistoryLimit: 1` | V-7: `no rollout record`, whatever its jobs' pods say |
| `deploy-pair` | Deployment `deploy-pair-api` and StatefulSet `deploy-pair-db`, same `part-of: deploy-pair` | V-11: one microservice, two workloads, two lines in WORKLOADS order |
| `deploy-hpa` | HorizontalPodAutoscaler (CPU) on `deploy-probe`'s sibling `deploy-auto`, a Deployment with a CPU-burning container | V-2d: an autoscaler changes replicas by itself; nothing may move. **Optional**, depends on §1's second unknown |
| `deploy-many` | Deployment scaled from 2 to 40 replicas, minimal requests | NFR-11: the preview of a service with many pods. Brought up only for that test and removed after |
| `deploy-forbidden` | Deployment in `qa-restricted` | V-15: a namespace the `restricted` environment cannot read; the preview must not misbehave |

**The ledger.** A small script (`deploy/gke-qa/record-rollouts.sh`, to be written at execution) does two things for the whole session: it wraps every action of §4b (`apply`, `rollout restart`, `rollout undo`, `scale`, `delete pod`) so each is logged with its server-side time and the exact command, and it polls every 2 seconds the `Progressing` and `Available` conditions of each fixture (`kubectl get -o jsonpath`), logging each change of `reason`, `status` and `lastUpdateTime`. That timeline is the **oracle** of §3 for every V case, and it is also the evidence that decides V-2.

## 3. Getting Huginn's answer as data, and the oracle

There is no non-interactive "print this range" command for logs, so the design uses what exists, with **an oracle that does not use Huginn's code**:

| What | Huginn's answer | Oracle (independent) |
|---|---|---|
| Logs, one page | `ctrl+s` saves the displayed lines to a file (D-050), one file per page | `kubectl logs --timestamps` of the same containers, captured after the range's end has passed, then cut by an `awk` script to `[From, To)` and ordered by time, pod |
| Logs, whole range | the concatenated page files | the same cut, without page size |
| Kafka | `huginn kafka read … --from … --to … --raw` (prints every page, scriptable) | a consumer in the broker pod (`kafka-console-consumer` with `print.partition`, `print.offset`, `print.timestamp`) over the whole topic, cut by timestamp |
| UI state (chips, status bar, popup) | `tmux capture-pane` as in the existing scripts | the expected strings of §4 |
| Deployment time of a workload | The `DEPLOYED` line of that workload, from `tmux capture-pane` of the services screen (220×50: side panel; 120×30: panel under the rows), parsed to a date, an hour and a state | The ledger of §2.6 and the workload's own `Progressing` condition: `lastUpdateTime` and `reason`, read at the same moment, plus the end of `kubectl rollout status` |

**Comparing a deployment time.** The preview shows the time to the minute and the age as a moving number, so the check is on the **date and hour** part only, against the condition's `lastUpdateTime` formatted the same way in the same zone: they must be equal, and the raw `lastUpdateTime` is also compared before and after each action, to the second. Since Huginn reads that very field, an unchanged raw value together with an unchanged display is full evidence that nothing moved; a changed raw value with an unchanged display (or the reverse) is a Huginn finding. The age is checked separately: within 1 minute of `now - lastUpdateTime` at capture time. During a rollout the check is the state (`rolling out since`, `n/m updated`), and the time must be within 60 seconds of the first new pod's `creationTimestamp`.

Comparisons are **set and order comparisons on `seq` (logs) or `(partition, offset)` (Kafka)**: missing, extra, duplicated and misordered items are counted separately and printed. Ranges are always chosen to end at least 60 s in the past so the oracle's snapshot is final. A verifier script (`deploy/gke-qa/verify-m15.sh`, to be written at execution) takes the saved files and the oracle capture and exits non-zero on any difference. Whether a small non-interactive mode in Huginn itself would be worth adding is a question for the owner (§11), not a design assumption.

## 4. Functional test catalogue

`P0` = must pass for the feature to be called verified on a real cluster. Every case states its oracle.

| ID | Pri | Scenario | Expected |
|---|---|---|---|
| **FR-1** | P0 | Closed range on `range-emitter` (2 pods), `To` ≥ 60 s ago; typed in the popup | Exactly the oracle's lines; both bounds checked: a line at exactly `From` present, one at exactly `To` absent; no line before `From` although the API honors the start to the second |
| FR-1b | P0 | Same range via `--from/--to` at launch, with `--repo` | Same lines; chip `RANGE`; opens on the first line |
| FR-1c | P1 | A one-second range; a range with no line (`quiet` mode, between two lines) | Exact lines; empty range shows the "pods that no longer exist and logs rotated away…" message, not an error |
| **FR-2** | P0 | Open range (`To` empty) from 2 minutes ago | History equals the oracle up to now, then live lines arrive in order; no gap and no duplicate at the junction of history and live |
| **FR-3** | P0 | Range of ≥ 2.5 pages with `config-m15` (page 1 000 lines, 2 pods) | `page 1 · more after hh:mm:ss`; `]` ×N reaches `page N (last)`; the concatenation of page files equals the oracle exactly; `[` returns the identical first page; `]` on the last and `[` on the first do nothing |
| FR-3b | P0 | `burst` mode; a page boundary that falls between lines of the same instant, if the runtime produces them (§2.1) | No line lost and none repeated at the boundary. If no identical instants exist, record "not reproducible on this node" |
| FR-3c | P1 | Change window (`1…7`, `0`, `9`, `t`, `T`) or press `f` while on page 3 | Back to page 1 of the new window; `f` on a closed range returns to the default window following |
| FR-3d | P1 | Open range larger than a page | Pages 1…N−1 do not follow; the last page follows and receives live lines |
| **FR-4** | P0 | `named-pod-fixture-0` in `qa-rec` and in `qa-restricted` through one range (as M14 §3.2) | Both streams, merged by time, no loss, no overwrite, also across pages |
| **FR-5a** | P0 | Range older than what the node keeps (`range-flood` after ≥ 2 rotations) | Partial result plus the notice `logs of <pod> available from <time> only`; no error; the first line shown matches the oracle's first line |
| FR-5b | P0 | Pod deleted (rollout) while viewing a closed range | The range still shows lines read before; a reload shows only what the node still serves; no crash, no stale stream left |
| FR-5c | P1 | `catalog-indexer` (crash loop): a range across several restarts; `P` (previous instance) while a range is active | Range lines from the running instance; `P` ignores the range and shows the whole previous instance (documented) |
| **FR-6** | P0 | Combine a range with: level filter, text filter (`/`), pod scope (`tab`), `A` (all containers), mute (`mute.loggers`), `space` (pause) on a page, trace view `v`, zoom, select/copy/save | Filters narrow the loaded page only; muted count `muted N` correct; pause holds the page; `ctrl+s` saves the current page; no state leaks to the next page |
| FR-7a | P0 | Time zones: run with `TZ` = UTC, `Asia/Tokyo`, `America/New_York`; same wall-clock input | Different instants by the zone offsets; an input with an explicit `Z` gives the same lines whatever `TZ` is; the popup shows the zone |
| FR-7b | P1 | Input forms: `2026-…` with and without seconds, `HH:MM` (today/yesterday), `now-90m`, RFC 3339 with offset, invalid and future values | Each resolves as the popup previews it; invalid values show the reason and `enter` does nothing |
| FR-7c | P1 | Local clock versus node clock | Measure the offset (`date -u` in a pod versus local). A range ending "now" with a skewed local clock is reported, not corrected |
| **FR-8a** | P0 | Kafka, `huginn kafka read … --from --to --raw` on `range-topic` | Every in-range record present (by `(partition, offset)`); none outside by its own timestamp; **records stamped in range but located before the broker's start offset are counted** (out-of-order data, FR-8c) |
| FR-8b | P0 | Kafka screen pages (`]`/`[`) over 6 partitions | Union of pages equals the oracle; no record on two pages except those the byte/record trim re-reads (counted, reported) |
| FR-8c | P0 | The out-of-order 2% | The number of in-range records the broker's offset lookup misses is **quantified**. Expected to be non-zero by construction; the QA decides, with the owner, whether the documented "approximate at the edges" is acceptable |
| FR-8d | P1 | `LogAppendTime` topic; a partition empty in the range; a range before the earliest offset (retention) | Correct bounds; no error for the empty partition; start clamps to the earliest offset |
| FR-8e | P1 | Open range on Kafka, then produce a record | Follows after the last page |
| FR-8f | P1 | SASL/SCRAM tier and TLS tier (the `orders` repo) with a range; the read-only ACL user (Describe + Read only) | Works with those ACLs (`ListOffsets` by time needs `Describe`; a missing ACL would show as `Forbidden`, which would be a finding) |
| FR-8g | P1 | `i` (isolation), `D` (decode toggle), Avro/JSON Schema records inside a range and across pages | As M14, including `registry answered 404` for the bad id |
| **FR-9** | P0 | `huginn-reader` (get on `pods/log`, list/watch pods, nothing else) | Range and pages work; `qa-restricted` stays `Forbidden` under `restricted`. A range that needs a permission the plain tail does not would show up here |

### 4b. Last deployment time in the services preview ([M15.1](M15.1-last-deployment.md))

Tested in the same session and on the same cluster, because it needs real rollouts that `--demo` cannot produce. The design rests on **one assumption nobody has checked**: that a Deployment's `Progressing` condition keeps its `lastUpdateTime` once a rollout is complete. The Kubernetes documentation says only "the last time this condition was updated". So **V-2 is the first thing started and the last thing judged** (§9): its clock starts at the first completed rollout and runs while the M15 range tests are done. If V-2 fails, M15.1's design changes before any other V case is trusted: the pods become the first source, with their documented weakness, and the affected rows are re-run.

`P0` = must pass for the feature to be called verified on a real cluster. The oracle for all of them is the ledger of §2.6 and the method of §3.

| ID | Pri | Scenario | Expected |
|---|---|---|---|
| V-1 | P0 | Roll out a new template of `deploy-probe` (`REV=2`) and, once, a new image of the JVM `payment-service`, with the preview of that service open and untouched | The line flips to `rolling out since hh:mm` with `n/m updated` **by itself** within seconds (the watch, no key pressed), then to a plain date and hour **equal to the minute of the condition's `lastUpdateTime`** and within 60 s of the end of `kubectl rollout status`. Both without pressing a key |
| **V-2a** | P0 | After the V-1 rollout completes, **do nothing for 30 minutes** (15 is the minimum); read the raw condition and the line at +1, +5, +15, +30 | `lastUpdateTime` is unchanged to the second, and so is the displayed date and hour. The age keeps growing |
| V-2b | P0 | Scale `deploy-probe` 2→3→2, polling the preview every 200 ms from the command to 30 s after | Raw value and display unchanged **at every poll**. In particular the line must never read `rolling out since …` during the scale-up, when the controller's counts lag and the watched `Progressing` flag is true (a defect found by measurement and fixed; this is its real-cluster check). Record the longest time `Progressing` was true in the ledger, to know how long the window was |
| V-2c | P0 | `kubectl delete pod` of one replica, then of both | Raw value and display unchanged, **while the pods' creation times visibly changed** (note them: this is the evidence that the controller record, not the pods, is the source) |
| V-2d | P1 | The HPA scales `deploy-auto` up and down by itself | Unchanged. Not exercised if §1's second unknown says no HPA |
| V-2e | P0 | The same scale-up and scale-down on `deploy-sts` (a kind with no controller record), and a scale from 0 to 2 and back on `deploy-probe` | `deploy-sts`: `pods since …` stays, never `rolling out`. Scale from zero: the completion time is still shown for a Deployment that has one. A StatefulSet template change (V-7) is still `rolling out`, with two revisions among the pods |
| V-3 | P0 | `kubectl rollout undo` of `deploy-probe` | Moves to the rollback's completion: a new `lastUpdateTime`, line equal to it. (A ReplicaSet-based source would have stayed on the old ReplicaSet's creation time: that is why it was rejected; record the old ReplicaSet's `creationTimestamp` as the counter-example) |
| V-4 | P1 | `kubectl rollout restart` | Moves. Note that the `restartedAt` annotation uses the **client's** clock; the line follows the **server's** `lastUpdateTime`, which is the one to compare |
| V-5 | P1 | Delete every pod of `deploy-probe` and of `deploy-sts` | `deploy-probe`: unchanged. `deploy-sts`: moves to the new pods' creation, shown as `pods since …` (the documented lower bound). Both lines recorded |
| V-6 | P1 | Roll out `busybox:does-not-exist` on `deploy-probe` (deadline 120 s), watch for 4 minutes, then fix it | `rolling out since … 0/2 updated` (or `1/2`) during the stall; **the previous completion time is not shown as current**. After the deadline the condition is `ProgressDeadlineExceeded` and **the line still says `rolling out since`** (the design does not mark a stalled rollout): record exactly what is shown and for how long, as an observation for the owner (§11). After the fix, back to a dated line |
| V-7 | P1 | `deploy-sts` rolling update (template change), `deploy-ds`, `deploy-cron` | `pods since …` for the first two, `no rollout record` for the CronJob (also when its jobs' pods exist). DaemonSet only if the cluster accepts it |
| V-8 | P0 | The whole of V-1…V-7 under `huginn-reader` (Role: pods, pods/log, events, deployments, statefulsets, daemonsets; **no ReplicaSet and no ControllerRevision**) | Identical lines. No `forbidden` anywhere in the diagnostic log (`--log-level debug`): the feature asks for nothing new |
| V-9 | P1 | `TZ` = UTC, `Asia/Tokyo`, `America/New_York`; then the local clock set 5 minutes ahead (WSL) | The hour follows the zone, the date can change across midnight; with the clock ahead the age reads `just now`, never negative |
| V-10 | P1 | Non-regression of the rest of the panel and table: the `AGE` column, `WORKLOADS`, `PODS`, `WARNINGS`, `SIDECARS` sections, the `p` toggle, the 80/120/220-column layouts | Only the added `DEPLOYED` lines differ from the pre-change binary (differential run, §6.3) |
| V-11 | P1 | `deploy-pair` (a Deployment and a StatefulSet in one microservice) | Two lines, in the same order as its two WORKLOADS lines, each with its own source and wording |
| V-12 | P1 | A workload with no rollout yet completed (just created, replicas not ready) | `rolling out …` or `no rollout record`, never a blank or a wrong date; then dated once ready |
| V-13 | P1 | The panel at real sizes with real data: 220×50 (side), 120×30 (under the rows), 80×24 with `p` forced on | DEPLOYED present at each. **At 80×24 the SIDECARS lines are squeezed out** (known from the golden): record what the user sees and whether that matters (§11) |
| V-14 | P0 | While the preview is open, move the cursor to another service and back during a rollout; switch environment (`ctrl+e`) and back | Always the right workload's line; no stale `rolling out` after the rollout ended; no flash of a wrong date |
| V-15 | P1 | `restricted` environment (forbidden namespace row) and the `deploy-forbidden` workload; a service whose read failed | No DEPLOYED lines for a row with an error, no panic, no blank panel; the forbidden message as before |
| V-16 | P1 | A workload whose Deployment is deleted while selected | The panel follows the row's disappearance; no panic |

## 5. Non-functional test plan

Methods of M10/M14 (`ps` sampling, `HUGINN_CPUPROFILE`) plus per-process bytes read (`/proc/<pid>/io`, `rchar`, from WSL; to be validated that it counts the TLS socket reads before relying on it). **Correctness is binary; performance rows give numbers and "investigate" triggers.** The thresholds marked *proposed* are tolerances for noisy sampling, not derived from a measurement, and need the owner's agreement before execution.

| ID | What | How | Baseline / criterion |
|---|---|---|---|
| NFR-1 | **Memory is one page, not the range** | RSS (≥ 3 samples per step) while paging through ≥ 10 pages of a long range at `buffer_lines` 1 000, then 50 000 | RSS at page 10 ≈ RSS at page 1 (*proposed*: within 15%), and independent of range length (the same page 1 for a 10× longer range) |
| NFR-2 | **No growth over time** | 20 cycles of `]`/`[`, then 20 range changes; RSS and goroutine-visible resources (open connections to the API server via `ss`) | No upward trend (*proposed*: mean of the last 5 samples ≤ first 5 + 10%); open connections fall back to the number of live streams after leaving the screen |
| NFR-3 | **A full page stops the read** (D-071) | `rchar` delta for page 1 of two ranges with the same start, one 10× longer | Roughly equal bytes for both, and in the order of the page's bytes plus a few MiB of in-flight buffer; compared with `kubectl logs --since-time … \| wc -c` for the whole range, which should be far larger |
| NFR-4 | **Time to first page by depth** (the open measurement) | Time from `enter` to the page on screen, `From` near the start and far from the start of a log filled to its rotation limit, pages 1 and N; the same reads with raw `kubectl logs --since-time` as a floor | Report the numbers. **Investigate** if page N is more than about 3× page 1 for the same bytes, or if Huginn adds a large constant over `kubectl` (*proposed triggers*) |
| NFR-5 | **No regression of existing windows** | A/B (§6.3): pre-change binary against the new binary, same cluster, same minute: idle, loaded (trio + loadgen, `--containers all`), loaded + mute, **services screen with the preview open for 5 minutes while rollouts happen**, with CPU profile | RSS and CPU within sampling noise of each other (*proposed*: 10%); M10/M14 numbers (62–68 MB idle, 76 MB loaded; M14: 42→72 MiB over ~4 min) as sanity context only, since cluster and load differ |
| NFR-6 | **Live range under load** | Open range on `payment-service-loadgen` (tens of lines/s) for several minutes | `LIVE n/s` plausible; RSS flat; no `dropped` growth beyond what the buffer explains |
| NFR-7 | **Kafka**: cost of a range and of pages on 6 partitions | Time to first page; broker-side: no consumer group created (`kafka-consumer-groups.sh --list` before and after), no offsets committed | Zero new groups; read-only held (the guard of D-057) |
| NFR-8 | **Read-only on the cluster** | RBAC proof: all of §4 under `huginn-reader`; Kafka under a Describe+Read ACL user | Nothing is `Forbidden` except what is meant to be; no write verb ever needed |
| NFR-9 | **Node and API server impact** | `kubectl top node` before/during/after a deep page; repeat page loads in a loop of 20 | No sustained node CPU rise attributable to repeated seeks; if seeks are costly (NFR-4), quantify per load |
| NFR-10 | **Cost** | Teardown within the time box; list clusters after | Zero clusters, repos, service accounts, instances at the end |
| NFR-11 | **Rendering cost of DEPLOYED** (M15.1) | **Measured before the QA** (`BenchmarkLastDeployment`, `internal/core/domain`, kept): a settled Deployment is 8.8 ns and no allocation for 2 000 pods; a workload with no controller record scans its pods in 46 µs for 2 000, no allocation (it was 190 µs and 25 allocations for four workloads before the scale-up fix). On the cluster: `deploy-many` at 40 replicas, the preview open and the cursor moving for 2 minutes, RSS/CPU against the pre-change binary | The new binary's RSS and CPU within 10% of the pre-change binary (*proposed*, the noise floor of sampling). The preview's other sections are unchanged and not judged here (PODS costs 10.7 ms per frame for 2 000 pods, pre-existing; noted for the owner) |
| NFR-12 | **No new request, no new permission** (M15.1) | (a) V-8: the whole of §4b under `huginn-reader` with no ReplicaSet/ControllerRevision right; the diagnostic log at `debug` for `forbidden`. (b) If cheap: the same session behind a logging proxy (`proxy-url` in the kubeconfig, `mitmproxy` in WSL), list the resource kinds and verbs requested, pre- against post-change binary | (a) zero `forbidden`, identical lines. (b) the same set of kinds and verbs, with the same watches and no new list. If the proxy proves impractical (TLS interception on Autopilot), (a) alone is the evidence and the report says (b) was not done |
| NFR-13 | **Freshness** (M15.1) | For each rollout of V-1, time from the condition reaching `NewReplicaSetAvailable` (the ledger) to the line flipping from `rolling out` to a date, read by polling `capture-pane` every 200 ms | Report the numbers (min, median, max over at least 5 rollouts). **Investigate** if the max exceeds 10 s (*proposed*): that would point at a stale watch, not at the render |

## 6. TUI non-regression

### 6.1 Automated gate (before touching GCP)

Run on the branch (it carries both features), all must pass unchanged: `go test ./...`, `go test -race` on the core, Kafka, Kubernetes and demo packages, `golangci-lint run`. The **golden diff against `e7a44f1` must be exactly the intended one**, and nothing else:

- M15: new `b range` in the `T` picker hints and the Kafka hints, the three help lines, the extra row of the picker, `help_logs_120x80` renamed to `120x84`.
- M15.1: six existing services-screen goldens gain the DEPLOYED section (`services_rec_160x30`, `services_prd_env_picker_160x30`, `services_preview_160x30`, `services_preview_side_220x40`, `services_preview_forced_80x24`, `services_filter_editing_120x20`), plus two new `services_preview_deployed_*`. **At 80×24 forced, the SIDECARS section no longer fits** (the DEPLOYED line has priority): that diff is intended and is judged in V-13, not hidden.

Any other golden change is a regression until explained.

### 6.2 The existing real-cluster scripts, unchanged

`e2e.sh`, `e2e-m10.sh` and `e2e-m12.sh` (tmux-driven, they exercise the services screen, logs, head, filters, trace, select/copy/save, redaction, Kafka screens) run **as they are** against the M15 binary on the new cluster. They are the real-world regression suite; they are not edited to make them pass. A script that fails because the screen text legitimately changed is a finding to triage (is the change intended?), not a script to patch silently.

**Checked today for M15.1:** the three real-cluster scripts and `mouse-test.sh` run in a tmux of 220×50, so the services screen shows the preview as a side panel (from 200 columns); the lab script runs at 180×40, where the panel sits under the rows. Either way the new DEPLOYED lines are on screen during the services-screen steps. None of them asserts anything about the preview, and their `refuse` checks look for **log** text (`sidecar heartbeat`, `envoy`, `reconnecting`, `unable to retrieve container logs`) on the logs screen, which the new section cannot produce. An `expect` that waits for a workload name on the services screen can now also be satisfied by a DEPLOYED line (a possible false positive, never a false negative); the differential run catches any such change in outcome.

Known touch points found by reading them today: `e2e.sh` asserts the `HEAD` chip (the new `RANGE`/`PAGE` chips come after it in the chip order, so `HEAD` must be unaffected); `lab/e2e.sh` types the word "debug" into the filter prompt (the `b` inside a prompt must stay text, which the unit tests cover and this run confirms for real); none uses `[`, `]` or a bare `b` as a command.

### 6.3 Differential run: before and after

Build the pre-M15 binary from `main` (`e7a44f1`) and the M15 binary from the branch. On the **same cluster, in the same minutes**, drive both through the same scripted session (services, logs 15m, `1…7`, `0`, `9`, `t`, `T`, `f`, `space`, `P`, filters, `A`, `M`, zoom, trace, select/copy/save, `ctrl+e` env switch, Kafka topics and records with every window key) and compare: `tmux capture-pane` text at each step (the only expected differences are the ones listed in §6.1), RSS/CPU (NFR-5), and the saved files byte for byte for the same window. This is the strongest regression evidence the design has, because it does not depend on anyone having listed the right assertions.

**For M15.1 the differential run has one extra step.** Captures of the services screen differ by design (the DEPLOYED lines, and SIDECARS at 80×24). Each pair is therefore compared twice: raw, to list the differences, and after removing the DEPLOYED lines from the new capture, when the two must be identical. The DEPLOYED lines themselves are judged by §4b, not by this comparison. The pre-change binary is `e7a44f1`, which predates both features, so a single baseline serves both.

### 6.4 Re-run of the README key table on the paths M15 and M15.1 changed

Same method as M10 §7: one row per README key-table entry, filled in during execution, with three columns: *unit/demo only*, *real GKE before M15*, *this session*. The rows below are those whose code M15 or M15.1 touched; every other row stays as M10/M12/M14 left it and is **not** re-litigated.

| Changed code | Rows to re-run on the real cluster |
|---|---|
| `logs.open/apply`, window handling, `setWindow` (now `Equal`), `toOldest` (now for ranges too) | `1`…`7`, `0`, `9`, `t`, `T`, AZERTY aliases `&é"'(-è`, `à`, `ç`; `9` stays a non-following head; `f` from a head returns to the default window; `P` with `9`; env switch `ctrl+e` while on each window kind |
| Status bar chips and fields | `LIVE n/s`, `PAUSED +n`, `STOPPED` (`f` off), `HEAD`, `PREVIOUS INSTANCE`, `NO PODS`, `REMOVED`, `NOT LOADED`, `muted N`, `dropped N` |
| Key bar, picker hints, help, empty states | `?`/`F1` help on each screen, `f2`/`ctrl+k` key bar sizes, the `T` picker, every empty message (no line in window, head, tail, muted) |
| Keymap | A `ui.yaml` override of `window_range`, `page_next`, `page_prev` works; an unknown action is still rejected with a suggestion; a user who already mapped `b`, `[` or `]` to another action gets a defined behaviour (to observe: the keymap allows shared keys across actions) |
| Kafka records screen | `1…7`, `0`, `f`, `space`, `i`, `D`, `/`, `o`, `y`, `enter` (zoom), status line text, empty state |
| Popup modality | Mouse click, drag, wheel and the `1…7` keys do nothing while the range popup is open (`mouse-test.sh`); `esc` always closes it |
| `preview.render` (sections now five, allocation order changed: DEPLOYED first), `Model.location()`, the `Workload` conversion | The services screen: `p` (toggle, and the auto/on/off placement at 200 columns and 8 free lines), `j`/`k`, `g`/`G`, `s` (sort), `/` (filter), `enter` (open logs), `ctrl+e` (environment), `r` (resync), the `K` marker; every preview section (WORKLOADS, PODS, WARNINGS with the events read after the 300 ms rest, SIDECARS); a service with several workloads; a row with an error; the stale-services marker (D-056) |

### 6.5 Look on a real terminal

Sizes 80×24, 120×30, 220×50 (the popup is clipped, not spilled, D-054) × themes `auto`, `light`, `dark`, `accessible`, `classic`, `none`; the range popup, an error in it, page chips, and the status bar at 80 columns where fields are truncated from the end ("most useful first"). For M15.1, the four DEPLOYED states (dated, `pods since`, `rolling out since`, `no rollout record`) at the same sizes and themes: the rolling-out state uses the warning colour, so under `none` and `accessible` it must still read as different from a settled one **by its words**, which it does by construction (`rolling out since …`) and the capture confirms. Screenshots or `capture-pane` text kept with the report.

## 7. Robustness and chaos

| ID | Action | Expected |
|---|---|---|
| CH-1 | Cut the network to the API server in the middle of loading a deep page (an `iptables` DROP in WSL, then removed) | A visible error with `r` to reload; no crash; no goroutine or connection left after recovery |
| CH-2 | `kubectl delete pod` of one replica while paging | The page keeps its lines; the next page for that pod reports its state; no panic |
| CH-3 | `kubectl scale karapace --replicas=0` during an Avro range read (M14's chaos) | Pending records fail at once with the documented message, no per-id stall |
| CH-4 | Delete the Kafka broker pod during a range read (M12's chaos) | `brokers unreachable, retrying…` then recovery or a clear error; no hang |
| CH-5 | The `kubectl logs --since=15m` **hang** seen on one pod in M14 (§2.2 #7, never root-caused) | If it recurs, check whether a range on that pod hangs too and how the screen behaves (a `loading` spinner with no timeout would be a finding, pre-existing or not) |
| CH-6 | Quit (`q`) and `esc` out of the screen during a page load | Process exits promptly; no stream left open (`ss`) |
| CH-7 | Cut the connection to the API server while a rollout of `deploy-probe` is running and the preview is open, restore it after the rollout ended | The services screen shows its stale marker (D-056) rather than a confident `rolling out`; after recovery the line settles to the dated state. It must never stay on `rolling out since` once the rollout is over |
| CH-8 | `r` (resync) during a rollout; delete the selected Deployment while its preview is shown | Resync keeps the line coherent; the deletion removes the row without a panic or a blank panel |
| CH-9 | A GKE node auto-repair or upgrade happening during the session (not triggered: **noted if it occurs**, via `kubectl get events`) | It is natural V-5 evidence on real nodes: Deployments unchanged, StatefulSets and bare pods moved to `pods since …`. Recorded with its time, not planned |

## 8. Pass and fail

- **M15 verified** when every `P0` case of §4 passes and NFR-1/2/3/7/8/10 pass.
- **M15.1 verified** when every `P0` case of §4b passes (V-1, V-2a/b/c/e, V-3, V-8, V-14), NFR-12(a) passes, and V-2 specifically held for the full 30 minutes. **If V-2 fails**, M15.1 is *not* verified: D-072 is changed (pods first, wording adjusted), the code is changed, and the V cases are re-run; this is a *Huginn* finding that stops the run for a fix (below).
- **Both** require the differential run to show only the intended differences (§6.1, §6.3), the three e2e scripts to pass unchanged, and the key-table rows of §6.4 to be filled in.
- **Findings** are classified as in M14: *Huginn* (code or docs change), *infrastructure/tooling* (not Huginn), *measured limit* (documented, no change). A *Huginn* finding stops the run for a fix, then the affected rows are re-run.
- **Not claimed** unless measured: any figure for page depth (NFR-4) or node impact (NFR-9); the same-instant boundary on a real node (FR-3b) if the runtime never produces identical instants; bearer-token Schema Registry auth (still not exercised since M14); the DaemonSet case (V-7) and the autoscaler case (V-2d) if Autopilot refuses them; the request-set comparison (NFR-12b) if no proxy is practical; the render cost numbers (NFR-11a) if the benchmark was not written.

## 9. Lifecycle

The order is chosen so that V-2's 30-minute wait **overlaps** the M15 range work instead of adding to the session.

1. §1: Console check of credit and expiry; pricing; the budget alert still exists (do not recreate). **Autopilot unknowns** (DaemonSet accepted? HPA metrics?): checked with a dry run, or by applying the fixture and reading the answer, before relying on them.
2. Gate §6.1 on the branch; build both binaries (§6.3) and keep the checksums.
3. `create-auto` in `us-east1`; list clusters even if it failed. Kubeconfig user entries first (§1 rules).
4. Artifact Registry, push the trio and loadgen (unchanged images); apply namespaces, reused workloads, then the M15 fixtures (`range-emitter`, `range-flood` kept off until FR-5a, `range-topic` Job) and the M15.1 fixtures (§2.6); confirm the Job exited 0 and the emitter's rate/identical-instant measurements (§2.1) before testing.
5. **Start the ledger (§2.6) and perform rollout R1 of `deploy-probe`** (and the JVM `payment-service` rollout of V-1). Observe V-1 live, including NFR-13. When R1 completes, note `C1` and the raw `lastUpdateTime`: **the V-2a clock starts at C1** (checkpoints C1+1, +5, +15, +30). V-2b and V-2c are done inside the wait, at C1+5 and C1+10.
6. Config folders (§2.5).
7. **During the 30 minutes**: the M15 functional pass (§4), in priority order. Nothing in it touches the M15.1 fixtures.
8. At C1+30: judge V-2a. **If it failed, stop M15.1 here** (§8) and carry on with M15 only.
9. The rest of §4b (V-3…V-16, one action at a time, each followed by at least 60 s of observation), then the regression suite (§6.2), the differential run (§6.3), the key-table rows (§6.4), the terminal matrix (§6.5).
10. NFR pass (§5; NFR-11b brings up `deploy-many` and removes it), then chaos (§7).
11. Fill `deploy/gke-qa/M15-QA-REPORT.md` (findings, filled matrix, numbers, the ledger as an appendix, what was not covered).
12. **Teardown, unconditionally**; list clusters in both regions; delete the repo and `huginn-reader`.
13. Record **D-073** (the filled matrix, the measured numbers, FR-8c's verdict) and **update the status of D-071 and D-072** from "pending" to what was found; fold any real-GKE-only finding into the README and `docs/CONFIG.md`.

Time-box: like M10/M12/M14, hours and not days, with a hard stop that triggers teardown whatever has been done. The cluster is up about 40 minutes longer than for M15 alone only if the M15 pass is shorter than V-2's wait. The page-depth measurement (NFR-4) is the one that may need a second, short run once the first numbers are known.

## 10. Risks and open questions

- **Log rotation limits and retention on Autopilot are unverified.** If the kubelet keeps little, the "far from the start" case is limited by the node, not by Huginn; that is itself a result.
- **Identical instants may not exist** on the runtime (FR-3b): then the cursor's per-container counting is covered by unit tests only, and the report must say so rather than imply otherwise.
- **FR-8c is a design question, not only a test**: the broker's time index can miss in-range records stamped out of order. The plan quantifies it; whether to document it more loudly, offer a wider safety margin, or accept it is the owner's call.
- **`rchar` as a bytes-over-the-wire measure** is an approximation (it counts reads, including TLS framing); if it proves unusable, fall back to the adapter byte counters of the OS network interface on a quiet machine.
- **Local clock skew** can shift `now`-relative ranges against the node's timestamps; measured (FR-7c), not corrected.
- **The hang of M14 §2.2 #7** may recur on a restarted pod and would affect ranges and plain windows alike.
- **Keys `b`, `[`, `]`** could already be mapped by a user to something else through `ui.yaml`; the behaviour of shared keys is observed in §6.4, not assumed.
- **V-2 is a real risk to M15.1's design**, not a formality: `lastUpdateTime` is documented only as "the last time this condition was updated". If the Deployment controller refreshes it (for example when it re-syncs a complete rollout), the first source is wrong and the feature, as built, would show a date that creeps forward. The plan is built so that this is discovered in the first 30 minutes of the session, not at the end.
- **Minute resolution hides small moves** in the display; the plan compares the raw `lastUpdateTime` to the second (§3), which Huginn reads unchanged, so a creeping value cannot hide.
- **Autopilot constraints** (a DaemonSet's per-node cost or refusal; whether the HPA gets metrics; minimum requests) are unverified; each affected case is reported as not exercised if refused (§8).
- **A stuck rollout reads `rolling out since …` indefinitely** (V-6): the code does not mark `ProgressDeadlineExceeded`. It is correct (the rollout is not done) but may mislead after the deadline. This is recorded as an observation and a decision for the owner (§11), not as a defect found in advance.
- **The SIDECARS lines are squeezed out at 80×24** with the preview forced on, because DEPLOYED has priority (V-13). Intended, but it is a visible trade-off that deserves the owner's eye on a real screen.
- **`restartedAt` uses the client's clock**, the condition uses the server's: V-4 compares with the latter.
- **A GKE node event during the session** can replace pods and change the picture of V-5 (CH-9): noted if it happens, not relied upon.

## 11. Decisions needed from the owner before execution

1. Accept the *proposed* thresholds of §5 (15% / 10% / 3×), or give others.
2. FR-8c: is "approximate at the edges for out-of-order timestamps" acceptable once quantified, or should the design change (for example, widen the start offset by a margin)?
3. Is a small non-interactive way to print a logs range (a `huginn logs` read-only subcommand, like `kafka read`) worth adding, to make FR-1/3 a script instead of per-page `ctrl+s`? Not designed here because it would be new scope.
4. Run the differential comparison (§6.3) for the Kafka screens too, or logs only.
5. Budget: confirm the remaining credit after the Console check, and whether a second short run for NFR-4 is acceptable.
6. **M15.1, after V-6:** should a rollout past its progress deadline be marked (for example `stalled since …`) instead of `rolling out since …`? Decide on what V-6 shows; the QA does not change the code.
7. **M15.1, after V-13:** is it acceptable that SIDECARS disappears at 80×24 with the preview forced on, or should DEPLOYED be limited to one line there?
8. **M15.1 fixtures:** if Autopilot refuses the DaemonSet or the HPA, accept "not exercised" for V-7 (DaemonSet) and V-2d, or spend effort on a workaround.
9. ~~Whether to write `BenchmarkLastDeployment`~~ Done: it exists and is the figure in NFR-11. Remaining question: is the PODS section's cost for services of thousands of pods (10.7 ms per frame for 2 000) worth its own change? Not part of this QA.
10. If V-2 fails: confirm in advance that the fallback is the pods-first design of M15.1 §3 (the `pods since` wording kept), so the fix can follow immediately without a new design round.

## 12. Done when (this design)

This document covers both features end to end: fixtures (§2, §2.6), oracle (§3), functional cases (§4, §4b), non-functional cases (§5), non-regression (§6), chaos (§7), pass and fail (§8), the order of the session (§9), risks (§10) and the decisions it needs from the owner (§11). It cites the facts it relies on (the read-only checks of §1, the existing scripts and reports, the grep of the e2e scripts for M15.1), and can be reviewed with no code change. Execution (§9) is a separate session.
