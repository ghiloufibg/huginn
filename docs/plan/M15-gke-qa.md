# M15 QA — Real GKE: absolute time range (functional, non-functional, TUI non-regression)  (status: design, not executed)

**Goal:** run the M15 feature ([`M15-absolute-time-range.md`](M15-absolute-time-range.md), [D-071](../DECISIONS.md)) against a real GKE Autopilot cluster and a real Kafka broker, the way M5/M10/M12/M14 did for earlier features, and prove three things the unit tests, the in-memory `kfake` broker and `--demo` cannot:

1. **Functional.** A range returns exactly the lines (records) of `[From, To)`, in order, and paging through a long range loses nothing and repeats nothing, against a real kubelet, a real API server and a real broker.
2. **Non-functional.** Memory stays bounded by one page whatever the range, a full page really stops the read, a page far into a log costs what it costs (**the one measurement D-071 left open**), and nothing leaks, hangs or crashes when the network, a pod or a broker disappears mid-read.
3. **Non-regression of the TUI.** Everything that worked on the real cluster in M5–M14 still works, checked three ways: the existing real-cluster e2e scripts unchanged, a re-run of the README key table on the paths M15 touched, and a differential run of the **pre-M15 binary against the M15 binary** on the same cluster.

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

## 1. Preconditions and cost governance

**Checked today (2026-11-01), read only, nothing created:**

| Check | Result |
|---|---|
| `gcloud` | 586.0.0, logged in as `bg.ghiloufi@gmail.com` (active); `huginn-reader@huginn-kube-tui.iam.gserviceaccount.com` also has credentials on this machine |
| Project | `huginn-kube-tui`, billing enabled (`billingAccounts/010F0D-062D60-7836F6`) |
| Clusters | none (`gcloud container clusters list` is empty): the M14 teardown held |
| Artifact Registry | no repository: the trio's images must be rebuilt and pushed again (as M14 did) |

**Not checkable from the CLI, to do by hand in the Console before anything is created** (same as M14 §1): remaining credit and expiry. Last known: 257 EUR, expiring 2026-12-26, confirmed 2026-10-07. That leaves **55 days from today** at most, and an unknown balance after M14's own spending. Re-verify current Autopilot pricing on the GKE pricing page; no cost figure is estimated here because none has been checked.

**What this session adds over M14's set:** two tiny `busybox` workloads (the emitter, §2.1; the flood, §2.2), one Kafka topic and one ephemeral Job to fill it (§2.3). Nothing stateful, nothing with a public address (Kafka and Karapace stay behind `kubectl port-forward`, M11 §2.5).

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

The trio, `payment-service-loadgen`, `noisy-fixture`, `kafka-qa`, `karapace`, the two marker pods, `named-pod-fixture` ×2. Redeployed as they were so M10/M12/M14 numbers stay comparable.

### 2.5 Config folders

- `deploy/gke-qa/config` and `config-kafka`: **unchanged**, used for the regression suite and for the NFR runs at the default `logs.buffer_lines` (50 000).
- `deploy/gke-qa/config-m15` (new, a small override): `logs.buffer_lines: 1000` (the minimum the config accepts), so a few thousand lines already make several pages, plus the `mute` block of M14 pointed at `qa.range.noisy`.

## 3. Getting Huginn's answer as data, and the oracle

There is no non-interactive "print this range" command for logs, so the design uses what exists, with **an oracle that does not use Huginn's code**:

| What | Huginn's answer | Oracle (independent) |
|---|---|---|
| Logs, one page | `ctrl+s` saves the displayed lines to a file (D-050), one file per page | `kubectl logs --timestamps` of the same containers, captured after the range's end has passed, then cut by an `awk` script to `[From, To)` and ordered by time, pod |
| Logs, whole range | the concatenated page files | the same cut, without page size |
| Kafka | `huginn kafka read … --from … --to … --raw` (prints every page, scriptable) | a consumer in the broker pod (`kafka-console-consumer` with `print.partition`, `print.offset`, `print.timestamp`) over the whole topic, cut by timestamp |
| UI state (chips, status bar, popup) | `tmux capture-pane` as in the existing scripts | the expected strings of §4 |

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

## 5. Non-functional test plan

Methods of M10/M14 (`ps` sampling, `HUGINN_CPUPROFILE`) plus per-process bytes read (`/proc/<pid>/io`, `rchar`, from WSL; to be validated that it counts the TLS socket reads before relying on it). **Correctness is binary; performance rows give numbers and "investigate" triggers.** The thresholds marked *proposed* are tolerances for noisy sampling, not derived from a measurement, and need the owner's agreement before execution.

| ID | What | How | Baseline / criterion |
|---|---|---|---|
| NFR-1 | **Memory is one page, not the range** | RSS (≥ 3 samples per step) while paging through ≥ 10 pages of a long range at `buffer_lines` 1 000, then 50 000 | RSS at page 10 ≈ RSS at page 1 (*proposed*: within 15%), and independent of range length (the same page 1 for a 10× longer range) |
| NFR-2 | **No growth over time** | 20 cycles of `]`/`[`, then 20 range changes; RSS and goroutine-visible resources (open connections to the API server via `ss`) | No upward trend (*proposed*: mean of the last 5 samples ≤ first 5 + 10%); open connections fall back to the number of live streams after leaving the screen |
| NFR-3 | **A full page stops the read** (D-071) | `rchar` delta for page 1 of two ranges with the same start, one 10× longer | Roughly equal bytes for both, and in the order of the page's bytes plus a few MiB of in-flight buffer; compared with `kubectl logs --since-time … \| wc -c` for the whole range, which should be far larger |
| NFR-4 | **Time to first page by depth** (the open measurement) | Time from `enter` to the page on screen, `From` near the start and far from the start of a log filled to its rotation limit, pages 1 and N; the same reads with raw `kubectl logs --since-time` as a floor | Report the numbers. **Investigate** if page N is more than about 3× page 1 for the same bytes, or if Huginn adds a large constant over `kubectl` (*proposed triggers*) |
| NFR-5 | **No regression of existing windows** | A/B (§6.3): pre-M15 binary against the M15 binary, same cluster, same minute: idle, loaded (trio + loadgen, `--containers all`), loaded + mute, with CPU profile | RSS and CPU within sampling noise of each other (*proposed*: 10%); M10/M14 numbers (62–68 MB idle, 76 MB loaded; M14: 42→72 MiB over ~4 min) as sanity context only, since cluster and load differ |
| NFR-6 | **Live range under load** | Open range on `payment-service-loadgen` (tens of lines/s) for several minutes | `LIVE n/s` plausible; RSS flat; no `dropped` growth beyond what the buffer explains |
| NFR-7 | **Kafka**: cost of a range and of pages on 6 partitions | Time to first page; broker-side: no consumer group created (`kafka-consumer-groups.sh --list` before and after), no offsets committed | Zero new groups; read-only held (the guard of D-057) |
| NFR-8 | **Read-only on the cluster** | RBAC proof: all of §4 under `huginn-reader`; Kafka under a Describe+Read ACL user | Nothing is `Forbidden` except what is meant to be; no write verb ever needed |
| NFR-9 | **Node and API server impact** | `kubectl top node` before/during/after a deep page; repeat page loads in a loop of 20 | No sustained node CPU rise attributable to repeated seeks; if seeks are costly (NFR-4), quantify per load |
| NFR-10 | **Cost** | Teardown within the time box; list clusters after | Zero clusters, repos, service accounts, instances at the end |

## 6. TUI non-regression

### 6.1 Automated gate (before touching GCP)

Run on the M15 branch, all must pass unchanged: `go test ./...`, `go test -race` on the core, Kafka, Kubernetes and demo packages, `golangci-lint run`. The **golden diff must be exactly the intended one**: new `b range` in the `T` picker hints and the Kafka hints, the three help lines, the extra row of the picker, `help_logs_120x80` renamed to `120x84`; nothing else. Any other golden change is a regression until explained.

### 6.2 The existing real-cluster scripts, unchanged

`e2e.sh`, `e2e-m10.sh` and `e2e-m12.sh` (tmux-driven, they exercise the services screen, logs, head, filters, trace, select/copy/save, redaction, Kafka screens) run **as they are** against the M15 binary on the new cluster. They are the real-world regression suite; they are not edited to make them pass. A script that fails because the screen text legitimately changed is a finding to triage (is the change intended?), not a script to patch silently.

Known touch points found by reading them today: `e2e.sh` asserts the `HEAD` chip (the new `RANGE`/`PAGE` chips come after it in the chip order, so `HEAD` must be unaffected); `lab/e2e.sh` types the word "debug" into the filter prompt (the `b` inside a prompt must stay text, which the unit tests cover and this run confirms for real); none uses `[`, `]` or a bare `b` as a command.

### 6.3 Differential run: before and after

Build the pre-M15 binary from `main` (`e7a44f1`) and the M15 binary from the branch. On the **same cluster, in the same minutes**, drive both through the same scripted session (services, logs 15m, `1…7`, `0`, `9`, `t`, `T`, `f`, `space`, `P`, filters, `A`, `M`, zoom, trace, select/copy/save, `ctrl+e` env switch, Kafka topics and records with every window key) and compare: `tmux capture-pane` text at each step (the only expected differences are the ones listed in §6.1), RSS/CPU (NFR-5), and the saved files byte for byte for the same window. This is the strongest regression evidence the design has, because it does not depend on anyone having listed the right assertions.

### 6.4 Re-run of the README key table on the paths M15 changed

Same method as M10 §7: one row per README key-table entry, filled in during execution, with three columns: *unit/demo only*, *real GKE before M15*, *this session*. The rows below are those whose code M15 touched; every other row stays as M10/M12/M14 left it and is **not** re-litigated.

| Changed code | Rows to re-run on the real cluster |
|---|---|
| `logs.open/apply`, window handling, `setWindow` (now `Equal`), `toOldest` (now for ranges too) | `1`…`7`, `0`, `9`, `t`, `T`, AZERTY aliases `&é"'(-è`, `à`, `ç`; `9` stays a non-following head; `f` from a head returns to the default window; `P` with `9`; env switch `ctrl+e` while on each window kind |
| Status bar chips and fields | `LIVE n/s`, `PAUSED +n`, `STOPPED` (`f` off), `HEAD`, `PREVIOUS INSTANCE`, `NO PODS`, `REMOVED`, `NOT LOADED`, `muted N`, `dropped N` |
| Key bar, picker hints, help, empty states | `?`/`F1` help on each screen, `f2`/`ctrl+k` key bar sizes, the `T` picker, every empty message (no line in window, head, tail, muted) |
| Keymap | A `ui.yaml` override of `window_range`, `page_next`, `page_prev` works; an unknown action is still rejected with a suggestion; a user who already mapped `b`, `[` or `]` to another action gets a defined behaviour (to observe: the keymap allows shared keys across actions) |
| Kafka records screen | `1…7`, `0`, `f`, `space`, `i`, `D`, `/`, `o`, `y`, `enter` (zoom), status line text, empty state |
| Popup modality | Mouse click, drag, wheel and the `1…7` keys do nothing while the range popup is open (`mouse-test.sh`); `esc` always closes it |

### 6.5 Look on a real terminal

Sizes 80×24, 120×30, 220×50 (the popup is clipped, not spilled, D-054) × themes `auto`, `light`, `dark`, `accessible`, `classic`, `none`; the range popup, an error in it, page chips, and the status bar at 80 columns where fields are truncated from the end ("most useful first"). Screenshots or `capture-pane` text kept with the report.

## 7. Robustness and chaos

| ID | Action | Expected |
|---|---|---|
| CH-1 | Cut the network to the API server in the middle of loading a deep page (an `iptables` DROP in WSL, then removed) | A visible error with `r` to reload; no crash; no goroutine or connection left after recovery |
| CH-2 | `kubectl delete pod` of one replica while paging | The page keeps its lines; the next page for that pod reports its state; no panic |
| CH-3 | `kubectl scale karapace --replicas=0` during an Avro range read (M14's chaos) | Pending records fail at once with the documented message, no per-id stall |
| CH-4 | Delete the Kafka broker pod during a range read (M12's chaos) | `brokers unreachable, retrying…` then recovery or a clear error; no hang |
| CH-5 | The `kubectl logs --since=15m` **hang** seen on one pod in M14 (§2.2 #7, never root-caused) | If it recurs, check whether a range on that pod hangs too and how the screen behaves (a `loading` spinner with no timeout would be a finding, pre-existing or not) |
| CH-6 | Quit (`q`) and `esc` out of the screen during a page load | Process exits promptly; no stream left open (`ss`) |

## 8. Pass and fail

- **Verified** when every `P0` case of §4 passes, NFR-1/2/3/7/8/10 pass, the differential run shows only the intended differences, the three e2e scripts pass unchanged, and the key-table rows of §6.4 are filled in.
- **Findings** are classified as in M14: *Huginn* (code or docs change), *infrastructure/tooling* (not Huginn), *measured limit* (documented, no change). A *Huginn* finding stops the run for a fix, then the affected rows are re-run.
- **Not claimed** unless measured: any figure for page depth (NFR-4) or node impact (NFR-9); the same-instant boundary on a real node (FR-3b) if the runtime never produces identical instants; bearer-token Schema Registry auth (still not exercised since M14).

## 9. Lifecycle

1. §1: Console check of credit and expiry; pricing; the budget alert still exists (do not recreate).
2. Gate §6.1 on the branch; build both binaries (§6.3) and keep the checksums.
3. `create-auto` in `us-east1`; list clusters even if it failed. Kubeconfig user entries first (§1 rules).
4. Artifact Registry, push the trio and loadgen (unchanged images); apply namespaces, reused workloads, then `range-emitter`, `range-flood` (kept off until FR-5a), `range-topic` Job; confirm the Job exited 0 and the emitter's rate/identical-instant measurements (§2.1) before testing.
5. Config folders (§2.5).
6. Functional pass (§4), in priority order, then the regression suite (§6.2), the differential run (§6.3), the key-table rows (§6.4), the terminal matrix (§6.5).
7. NFR pass (§5), then chaos (§7).
8. Fill `deploy/gke-qa/M15-QA-REPORT.md` (findings, filled matrix, numbers, what was not covered).
9. **Teardown, unconditionally**; list clusters in both regions; delete the repo and `huginn-reader`.
10. Record D-072 (the filled matrix, the measured numbers, FR-8c's verdict); fold any real-GKE-only finding into the README and `docs/CONFIG.md`.

Time-box: like M10/M12/M14, hours and not days, with a hard stop that triggers teardown whatever has been done. The page-depth measurement (NFR-4) is the one that may need a second, short run once the first numbers are known.

## 10. Risks and open questions

- **Log rotation limits and retention on Autopilot are unverified.** If the kubelet keeps little, the "far from the start" case is limited by the node, not by Huginn; that is itself a result.
- **Identical instants may not exist** on the runtime (FR-3b): then the cursor's per-container counting is covered by unit tests only, and the report must say so rather than imply otherwise.
- **FR-8c is a design question, not only a test**: the broker's time index can miss in-range records stamped out of order. The plan quantifies it; whether to document it more loudly, offer a wider safety margin, or accept it is the owner's call.
- **`rchar` as a bytes-over-the-wire measure** is an approximation (it counts reads, including TLS framing); if it proves unusable, fall back to the adapter byte counters of the OS network interface on a quiet machine.
- **Local clock skew** can shift `now`-relative ranges against the node's timestamps; measured (FR-7c), not corrected.
- **The hang of M14 §2.2 #7** may recur on a restarted pod and would affect ranges and plain windows alike.
- **Keys `b`, `[`, `]`** could already be mapped by a user to something else through `ui.yaml`; the behaviour of shared keys is observed in §6.4, not assumed.

## 11. Decisions needed from the owner before execution

1. Accept the *proposed* thresholds of §5 (15% / 10% / 3×), or give others.
2. FR-8c: is "approximate at the edges for out-of-order timestamps" acceptable once quantified, or should the design change (for example, widen the start offset by a margin)?
3. Is a small non-interactive way to print a logs range (a `huginn logs` read-only subcommand, like `kafka read`) worth adding, to make FR-1/3 a script instead of per-page `ctrl+s`? Not designed here because it would be new scope.
4. Run the differential comparison (§6.3) for the Kafka screens too, or logs only.
5. Budget: confirm the remaining credit after the Console check, and whether a second short run for NFR-4 is acceptable.

## 12. Done when (this design)

This document exists, cites the facts it relies on (the read-only checks of §1, the existing scripts and reports), and can be reviewed with no code change. Execution (§9) is a separate session.
