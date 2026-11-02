# M15 / M15.1 QA Report — Real GKE, round 5

Executed 2026-11-02 against a real GKE Autopilot cluster (`huginn-qa`, `us-east1`, release channel `regular`, master 1.36.4-gke.2046003) in project `huginn-kube-tui`. Design: [`docs/plan/M15-gke-qa.md`](../../docs/plan/M15-gke-qa.md). Binary under test: commit `3ab8d65` (`huginn v0.4.1-0.20261101224109-3ab8d652834a`). Pre-change binary for the differential run: commit `e7a44f1` (`v0.4.0`). Full teardown confirmed at the end (§7).

Credit at the start, as read by the owner in the Console: 256 EUR, 55 days to expiry. `gcloud` cannot read it (§6). The cost of this session was **not measured**; read the Console credit after this report.

**This was a partial run.** The cases below marked *not run* were not run, and the report says so rather than filling them in (§5).

## 1. Verdict

| Feature | Verdict |
|---|---|
| **M15, absolute time range** (logs) | The core guarantee held on a real kubelet: a closed range over two pods, read in four pages, equals the `kubectl logs --timestamps` oracle exactly (3 893 of 3 893 lines; 0 missing, 0 extra, 0 duplicated; per-pod sequence consecutive across pages). Time zones, invalid input, an open range with live follow, two same-named pods, and a range older than what the node keeps behaved as designed. |
| **M15, Kafka range** | Works against a real broker. The out-of-order edge was **quantified** (FR-8c): 9 of 1 500 in-range records missed (0.6 %), all late-stamped, all at the `To` edge. Not a bug against the documented "approximate at the edges"; the owner decides whether that is acceptable (§4, decision 1). |
| **M15.1, last deployment time** | **V-2, the assumption the design rests on, holds**: a Deployment's `Progressing.lastUpdateTime` did not move for 30 minutes, nor for scaling, pod deletion, an autoscaler or scale-from-zero, and did move, to the second, for an undo and a restart. The scale-up defect fixed in `3ab8d65` did not appear on the real cluster. |
| **TUI non-regression** | Only partly covered: a differential run of the services screen against the pre-change binary at three sizes shows only the added `DEPLOYED` lines. The real-cluster e2e scripts of §6.2 of the plan were **not run** (they need the Spring trio, which was not built). |

No defect was found in the code under test. Five observations need an owner decision (§4). One concerns a cost leak from earlier sessions (§4, item 7).

## 2. What ran

**Cluster and fixtures.** `create-auto` succeeded on the first attempt in `us-east1` (clusters listed afterwards: only `huginn-qa`). Namespaces `qa-rec` and `qa-restricted`; `rbac.yaml`; `m15-range-fixtures.yaml` (`range-emitter`), `m15-deploy-fixtures.yaml` (12 objects), `named-pod-fixture.yaml`, and a single-broker Kafka (`kafka-workloads.yaml`) reached by `kubectl port-forward`. The IAM identity `huginn-reader` was recreated (it had been deleted by the M14 teardown) with an impersonated kubeconfig entry under distinct names, and `kubectl --context huginn-reader` was checked to be Forbidden in `qa-restricted` before it was used.

**Not built or deployed:** the Spring trio, `payment-service-loadgen`, `noisy-fixture`, Karapace, the Artifact Registry repository, `range-flood`.

**The oracle for M15.1** was `deploy/gke-qa/record-rollouts.sh`: every action with its exact command and time, and the Deployment/StatefulSet state polled every 2 s (76 lines in the ledger). A second poller read the preview every 200 ms and logged each change.

**Autopilot unknowns (§1 of the plan), both resolved:** a DaemonSet of the requested shape is accepted and ran one pod; the CPU HorizontalPodAutoscaler got metrics and scaled `deploy-auto` 1 → 3 → 1 by itself.

## 3. Results

### 3.1 M15 — logs

| ID | Result | Evidence |
|---|---|---|
| FR-1, FR-1b | **pass** | `--repo range-emitter --from "2026-11-02 13:28:00" --to "2026-11-02 13:29:00"`; chip `RANGE Nov 2 13:28 → 13:29`; opens on the first line (13:28:00.022). 3 893 lines (oracle: lines whose kubelet timestamp is in `[12:28:00Z, 12:29:00Z)` from both pods). Missing 0, extra 0, duplicated 0. No line exactly at `From` or at `To` exists on this node, so the two bound lines themselves were not exercised. |
| FR-3 | **pass** | 1 000 + 1 000 + 1 000 + 893 lines over pages 1–4 (`config-m15`, `buffer_lines 1000`), each saved with `ctrl+s`; concatenation equals the oracle; per pod 1 951 and 1 942 lines with no break in the `seq` sequence. Chip `page N · more after hh:mm:ss`, then `page 4 (last)`. |
| FR-2, FR-3d | **pass** | `--from now-25s`: the saved pages hold 1 000 lines per pod with consecutive `seq` across the page boundary and the history-to-live junction; no gap, no duplicate. |
| FR-4 | **pass** | `named-pod-fixture-0` in `qa-rec` and `qa-restricted`, one range: 48 of 48 lines (24 per namespace in the oracle), merged by time, alternating `from qa-restricted` / `from qa-rec`. |
| FR-5a | **pass** | By luck: the kubelet rotated `range-emitter-0` during the session (oldest line served after about 45 minutes of output: 12:58:04Z, `seq=073052`; 42 379 lines kept). A range wholly before it shows `no line … pods no longer exist, logs rotated away on node`, not an error. A range starting earlier shows `logs of range-emitter-0 available from Nov 2 13:58 only`, and the first line shown (13:58:04.435) is the node's first. |
| FR-7a | **pass** | `TZ=Asia/Tokyo` with `21:28–21:29`, `TZ=America/New_York` with `07:28–07:29`, and `TZ=America/New_York` with `2026-11-02T12:28:00Z`: the same first lines (`seq=003692` / `003337`) each time; the displayed hour follows the zone. |
| FR-7b | **pass (CLI only)** | `garbage`, a future date, `from` after `to`, `--since` with `--from`, `--to` alone, and an RFC 3339 start equal to the end each give a one-line reason and exit. The popup's own preview was not exercised. |
| FR-3b | **not reproducible on this node** | In `burst` mode, 10 lines written by one `printf` carried 10 distinct nanosecond timestamps (0 identical in 400 lines). The same-instant page boundary is therefore covered by the unit tests only. |
| FR-5b | **weak** | A pod was deleted while a range was open, but the range was already empty (rotated away) at that point, so only "no crash" was shown. Redo it. |
| FR-1c, FR-3c, FR-5c, FR-6, FR-7c, FR-9 | **not run** | FR-9 was run only for the services screen under `restricted` (§3.3), not for a range under `huginn-reader`. |

### 3.2 M15 — Kafka (`range-topic`, 6 partitions, 6 000 records, 2 % stamped 90 s late; `range-topic-lat`, `LogAppendTime`)

| ID | Result | Evidence |
|---|---|---|
| FR-8a | **pass** | `kafka read catalog range-topic --from "2026-11-02 13:25:00" --to "2026-11-02 13:30:00" --raw`: 1 491 records, 0 duplicates, **0 records outside the range by their own timestamp**. |
| FR-8b | **pass** | The 1 491 records exceed `kafka.max_records` (1 200), so the CLI read at least two pages; the union has no duplicate. |
| **FR-8c** | **quantified** | 1 500 records are stamped in the range; 1 491 returned; **9 missed, all 9 late-stamped** (`i` = 3099, 3149, … 3499). They sit after the broker's end offset for `To` because they were written after records stamped later. 0.6 % here, with 2 % of records 90 s late; the miss is bounded by the lateness (90 s) at the `To` edge. No record was missed at the `From` edge. Owner decision 1. |
| FR-8d | **pass** | On `range-topic-lat`, `[13:50,13:55)` = 1 343 and `[13:55,14:00)` = 1 310 records; `[13:50,14:00)` = 2 653 with an identical set. A range before the earliest data returns nothing and exit 0. |
| FR-8e, FR-8f, FR-8g | **not run** | |

### 3.3 M15.1 — DEPLOYED

All dates below are the preview's, in the machine's zone (UTC+1) against the ledger's UTC (`13:27` = `12:27:13Z`).

| ID | Result | Evidence |
|---|---|---|
| **V-2a** | **pass** | C1 = `12:27:13Z` (end of rollout R1, `deploy-probe` `REV=2`). At +12 min, +30 min: `lastUpdateTime` unchanged to the second; the ledger shows no state change in between; the preview read `Nov 2 13:27` with the age growing (`30m ago`). |
| V-2b | **pass** | 2 → 3 → 2, preview polled every 200 ms from 5 s before to 60 s after: one value, `Nov 2 13:27`, never `rolling out`. The ledger shows the exact window the fix was for: during the scale-up the controller reported `avail=2` of `spec=3` and `Available=False` while `Progressing=True/NewReplicaSetAvailable`. |
| V-2c | **pass** | One pod, then both pods of `deploy-probe` deleted. New pods were created at 13:00:37Z (before: 12:27:00Z and 12:27:07Z); `lastUpdateTime` stayed `12:27:13Z` and the line did not change. This is the evidence that the controller record, not the pods, is the source. |
| V-2d | **pass** | The HPA took `deploy-auto` 1 → 3 → 1; the line stayed `Nov 2 13:26` (`lastUpdateTime` 12:26:15Z). |
| V-2e | **pass** | `deploy-sts` 2 → 4 → 2: stayed `pods since Nov 2 13:24`, never `rolling out`. `deploy-probe` 2 → 0 → 2: unchanged. |
| V-3 | **pass** | `rollout undo`: `lastUpdateTime` moved to `13:05:08Z`; the line read `rolling out since Nov 2 14:04 … 1/2 updated`, then `Nov 2 14:05`. Counter-example recorded: the ReplicaSet the undo reused was created 12:24:03Z, so a ReplicaSet-based source would not have moved. |
| V-4 | **pass** | `rollout restart`: `13:05:38Z`; the `restartedAt` annotation said `13:05:23Z` (client clock), 15 s earlier. The line follows the server's value. |
| V-5 | **pass** | `deploy-probe` unchanged (V-2c). `deploy-sts`, all pods deleted: `pods since Nov 2 14:02` (the new pods' creation), the documented lower bound. See observation 4. |
| V-6 | **pass, with observation** | `busybox:does-not-exist`: `rolling out, no new pod yet`, then `rolling out since Nov 2 14:07 … 1/2 updated`, with the old completion time never shown as current. The condition became `ProgressDeadlineExceeded` (`Progressing=False`) at 13:09:19Z and **the line kept saying `rolling out since 14:07` for 4 min 11 s, until the fix** (`Nov 2 14:11`, `13:11:31Z`). As designed; see decision 3. |
| V-7 | **pass** | `deploy-cron`: `no rollout record`. `deploy-ds` (accepted by Autopilot): `pods since …`. `deploy-sts` template change: `rolling out since Nov 2 14:06 … 0/2 updated`, then `pods since Nov 2 14:06`. |
| V-8 | **pass (partly)** | Role reduced to deployments, statefulsets, daemonsets, pods, pods/log, events (the committed `rbac.yaml` also grants ReplicaSets). Under `restricted` the DEPLOYED lines were identical to the admin view. The debug log has 0 mentions of ReplicaSet or ControllerRevision; the only `forbidden` entries are for the deliberately restricted `qa-restricted` namespace. V-1…V-7 were not repeated under `huginn-reader`. |
| V-10 | **pass** | Differential run, services screen, old versus new binary, 220×50, 120×30, 80×24, with and without `p`: the table and its columns are identical; the only differences are the added `DEPLOYED` lines and the status-bar clock and ages. The WORKLOADS, PODS, WARNINGS and SIDECARS sections were not compared in detail beyond that. |
| V-11 | **pass** | `deploy-pair`: `deploy-pair-api  Nov 2 13:26` and `deploy-pair-db  pods since Nov 2 13:24`, two lines. |
| V-16 | **pass, with observation** | `deploy-pair-api` deleted while selected: no panic; the line became `no rollout record` for about 30 s (its pod terminating), then disappeared. |
| V-1, V-9, V-12, V-13, V-14, V-15 | **not run** | V-1 for the JVM `payment-service` was not run (trio not built); the pod-template path was covered with `deploy-probe` (V-3, V-4). V-13's SIDECARS squeeze at 80×24 was not observed because no fixture has a sidecar. |

## 4. Observations and decisions needed from the owner

1. **FR-8c, approximate edge (Kafka).** 0.6 % missed in this data set, all at the `To` edge, bounded by the producers' lateness. Accept as documented, or widen the end offset by a margin (the cost is reading some records past `To` and dropping them).
2. **Rotation notice threshold (logs).** The `available from … only` notice is suppressed when the first kept line is within 5 minutes of `From`, or within a tenth of the range. A 60 s range whose first 34 s were rotated away showed no notice. Keep, or lower the threshold for short ranges.
3. **A stuck rollout reads `rolling out since …` indefinitely (V-6).** Already listed in the plan as a risk; confirmed: 4 min 11 s past `ProgressDeadlineExceeded`. Mark it, or leave it.
4. **`rolling out, no new pod yet` when no pod exists (V-5).** With every pod of `deploy-sts` deleted, the line said so for about 31 s, and for about 32 s at the start of a StatefulSet template change. Correct for the second case; arguable for the first.
5. **Loading state of a page.** Once, a page being loaded showed `page 3 (last)` and `0/0 lines` for a few seconds before settling to `page 3 · more after 13:28:46`. A page of 1 000 lines took more than 4 s and under 9 s to appear; this was not measured further. Small, but "(last)" is wrong while loading.
6. **`rbac.yaml` grants ReplicaSets**; the feature does not need them (V-8). Drop it from the committed Role, or keep it for the other features.
7. **Orphaned disks from earlier sessions.** After this session's teardown, three unattached 5 GB persistent disks remain in the project: `pvc-24e1e853-…` (us-central1-b, created 2026-10-04), `pvc-df6ec99b-…` (us-central1-c, 2026-10-03) and `pvc-42d630f7-…` (us-east1-d, 2026-10-07). They carry the labels of the M12 and M14 `huginn-qa` clusters. They were not created in this session and were **not deleted**. The Kafka disk of this session was deleted (PVC first, then the cluster). Delete them after confirming they are not needed.

## 5. Not covered

Not run: the real-cluster regression scripts (`e2e.sh`, `e2e-m10.sh`, `e2e-m12.sh`, plan §6.2), the key-table re-run (§6.4), the non-functional plan (§5: bytes read by page depth, time to first page by depth, `deploy-many`, memory over time, broker or network loss mid-read), FR-5c, FR-6, FR-8e–g, and the V cases listed above. **D-071 left one thing to measure on a real cluster: whether a page far into a log costs more than page 1. It was not measured.** The only memory figure taken is a resident set of 45–55 MB for the TUI on an empty range, which says nothing about a full page. The automated gate (§6.1) was last run when the commits were made, not in this session.

## 6. Tooling notes (for the next session)

- `gcloud` cannot read the free-trial credit or its expiry; the Cloud Billing API is disabled on the project and would not expose credits anyway. Read it in the Console.
- WSL: background processes (tmux) die when the last `wsl.exe` call returns; keep one `wsl -- sleep` running. The Windows kubeconfig's `gke-gcloud-auth-plugin.exe` is not found from WSL: run `gcloud container clusters get-credentials` inside WSL. `$PATH` contains spaces ("Program Files"), so put the environment in a wrapper script rather than on a tmux command line.
- Git Bash: `MSYS_NO_PATHCONV=1` breaks the Windows `gcloud` when `CLOUDSDK_CONFIG` is a `/c/...` path; use it only for the commands that need it (WSL, `kubectl exec`).
- A new service account's role bindings took about a minute before impersonation worked (`IAM_PERMISSION_DENIED` until then).
- The record producer (`range-topic/`) writes with one synchronous produce per record through a port-forward: 12 000 records took 23 min. Batch them next time.
- `kubectl port-forward` and the ledger poller were stopped at teardown.

## 7. Teardown (confirmed)

Cluster `huginn-qa` deleted; `gcloud container clusters list` empty (all regions); `huginn-reader` service account and its `roles/container.clusterViewer` binding deleted; no Artifact Registry repository was created; no addresses, forwarding rules or instances; this session's PVC disk deleted before the cluster. The three disks in §4 item 7 remain. WSL: tmux server stopped, kubeconfig copies, saved log files and temporary scripts removed.

## 8. Files

`m15-range-fixtures.yaml`, `m15-flood.yaml` (not applied), `m15-deploy-fixtures.yaml`, `record-rollouts.sh`, `range-topic/` (producer), `config-m15/`. The ledger of this run and the raw outputs are in the session scratchpad, not in the repository.
