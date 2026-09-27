# M4 — second end-to-end pass on the lab cluster (2026-09-27)

**Status: all bugs below are fixed** (commits `b5db773`, `44b7a71`, `e447388`, `706387b`; decisions in D-037). Each fix was checked on the lab again, and `deploy/lab/e2e.sh` passes. The one part left out is choosing a container in the pod scope: the pod column names the container instead (E7).

**Setup:** kind lab (`deploy/lab`, Kubernetes 1.33), Huginn at `daeb6ad`, run in tmux against `kind-huginn` and the restricted identity. On top of `workloads.yaml`, `deploy/lab/extra.yaml` adds:
- a high-rate emitter (about 690 lines/s over 2 pods);
- edge-case lines: ANSI codes, unicode, a 40 KB line, a 1.1 MB line, broken JSON, a bad timestamp, a 2020 timestamp, CRLF;
- two app containers in one pod;
- a CronJob, a DaemonSet, a Deployment scaled to 0;
- a failing init container, a never-ready pod, a silent pod;
- a pod draining for 40 s on SIGTERM;
- a workload without the repository label;
- terminal escape injection.

Failures were injected with `docker pause`, `iptables` DROP on the API port, a bad token, an unknown context, a missing kubeconfig, a namespace deletion and a namespace typo.

## Bugs

### High
| # | What happens | Reproduce |
|---|---|---|
| E1 | **Logs are unusable in an environment with one forbidden namespace.** In `restricted` (`app-rec` readable, `app-dev` forbidden), opening the logs of *any* service fails with `Cannot read the logs of payment-service: forbidden`. The services screen lists the same services. `LogSessions.workloadsOf` calls `ListWorkloads` over the whole scope, and the adapter fails the call on the first forbidden namespace. | `huginn --config deploy/lab/config restricted`, open any service |
| E2 | **A network loss after start is invisible.** With the API port dropped (iptables), the header keeps `kubernetes · watching` for 90 s+ and the services screen keeps showing the last state as live. The informers retry silently and no error reaches the catalog. On the logs screen the pods say `no logs: error`, then `unreachable`, but the header stays unchanged. | start, then `iptables -I OUTPUT -p tcp --dport <api port> -j DROP` |

### Medium
| # | What happens |
|---|---|
| E3 | **Failing init container.** A pod in `Init:Error` is shown `Pending — PodInitializing` for the first minutes, then `CrashLoopBackOff`. RST stays `0` (init restarts are not counted) and LAST shows `-`. The preview's pod row says `CrashLoopBackOff` while the table says `Pending`. The logs are empty, and the hint says "t longer window", which cannot help. The init container's output ("migration failed…"), which explains the failure, cannot be reached: `show_init: false` hides it, and `P` says "no previous instance". |
| E4 | **Completed Job pods (CronJob) are counted as failures.** The services row shows `Degraded — 2 of 2 pods not ready` next to `PODS 0/0`, the preview shows the pod as `Degraded`, and the pod strip shows `waiting: Completed`. The CronJob row says `1/1 ready 1/1 updated` (active jobs). |
| E5 | **Backfill after a reconnect is out of time order.** After a 2.5 min outage, one pod's backfilled lines (08:28:57–08:31:00) come after the other pods' lines committed at 08:31:01, so the view goes `08:31:01` → `08:30:26`. No line is lost. |
| E6 | **A pause longer than the buffer blanks the screen.** At 690 lines/s, after 80 s of pause the lines the paused view showed are evicted. The screen goes blank, `pgup` shows nothing, `PAUSED +50000` stops counting, and nothing says lines were dropped. |
| E7 | **Two app containers in one pod cannot be told apart.** There is no container column; only zoom shows the container. The pod scope (`tab`, `S`) selects pods, not containers. |
| E8 | **Workload kinds the identity cannot read are skipped silently.** With the restricted identity, the CronJob `nightly-export` disappears from the list. Nothing says the list is incomplete; `partial` in the header comes only from `app-dev`. |
| E9 | **Stream errors cannot be diagnosed.** During the outage the strip said `no logs: error`: `http2: client connection lost` was not mapped to `unreachable`. The error text appears nowhere, neither in the UI nor in the diagnostic log (tailer errors are not logged). |
| E10 | **A namespace that does not exist looks empty.** A typo (`app-rce`) or a deleted namespace gives `no services in tmp` under `watching`, with no hint that the namespace does not exist. |
| E11 | **Megabyte lines are slow to filter and zoom.** A filter matching everywhere in ten 1.1 MB lines costs about 350 ms per keystroke. Zooming one takes several seconds and RSS goes from 110 to 200+ MB, because the whole line is wrapped. Keys queue up meanwhile. |

### Low
| # | What happens |
|---|---|
| E12 | A terminating pod (40 s grace) stays `Healthy` in the table and the strip until it is gone; there is no "terminating" state. |
| E13 | A repository deleted while its logs are open: its pods become `terminated` but the chip stays `LIVE`, and nothing says the workload is gone. |
| E14 | A line whose own timestamp is years old (2020) shows `00:00:00.000` with no date. The history puts such lines at the top, out of source order, while live they stay in place. |
| E15 | OSC 8 hyperlinks in log output reach the terminal: the text can hide another URL. SGR colours pass on purpose. Clear screen, title, OSC 52 clipboard, cursor moves and backspace are stripped (checked). |
| E16 | StatefulSet pods are labelled `0`, `1` in the preview and the strip, which is ambiguous when a repository has two StatefulSets. |
| E17 | Each informer first tries the watch-list mode (`sendInitialEvents`), which a server without that feature refuses, then falls back: 30 extra failed requests at start. |
| E18 | Starting with the API unreachable shows the spinner for about 30 s (TCP dial timeout) before `unreachable`. |
| E19 | Permanent errors say "fix … and restart huginn", but `r` works once the kubeconfig is fixed. Messages repeat the kind: `Unauthorized: unauthorized`, `…does not exist: configuration`. |
| E20 | With `KUBECONFIG` pointing to a missing file, the message says the context does not exist, not that no kubeconfig was found. |
| E21 | The logs error view cuts long errors instead of wrapping them, and its chip still says `LIVE`. |
| E22 | The columns picker of a JSON (spring layout) view lists `status`, `bytes` and `client` from the nginx access layout. |
| E24 | **(fixed)** Found by the CI lab job (Kubernetes 1.37): when a container's log file is gone, the kubelet answers `200` with the text `unable to retrieve container logs for containerd://…`, which Huginn showed as a log line. The adapter now turns it into `not found`. |
| E23 | Known from the plan: B7, a restart-only rollout, is still invisible in the version column. |

### Not bugs, or out of scope (noted)
- Bare pods (no workload) are not listed: Huginn lists workloads.
- The nginx error lines of the storefront container (a second format in the same container) get no level: a format has one pattern. This is a limitation of the config model.
- The warning "Unhealthy (x307) 4m ago" while the probe still fails: Kubernetes rate-limits the updates of repeated events, and Huginn shows `lastTimestamp` faithfully.
- After the ServiceAccount was deleted, existing watches kept working for 60 s+: the token is checked on new connections only.

## Verified working
- **States:** CrashLoopBackOff, OOMKilled, ImagePullBackOff, Pending (Unschedulable), never ready, scaled to 0, DaemonSet, StatefulSet (other env), unlabelled workload ("without repo").
- **Logs:**
  - JSON decoding and folded stacks, sidecars hidden, text (nginx) format;
  - unicode, tabs, CRLF, broken JSON, empty lines;
  - a 40 KB line, and a 1.1 MB line cut at 1 MiB.
- **Live changes:**
  - rollout with a 40 s drain ("drained" line received, then `terminated`);
  - scale up (new pods appear);
  - deletion;
  - a new Job pod of the CronJob appears.
- **Failure handling:**
  - crash loop: `waiting`, the restart is picked up without duplicates, `P` shows the previous instance;
  - network loss: streams resume within 10 s, and the lines of the outage are backfilled (none lost);
  - API server paused and resumed: recovers.
- **Kubelet log rotation:** 122 901 lines followed over 6 min and at least two rotations, **no gap**.
- **Errors:** bad token → `unauthorized`; unknown context → `configuration error`, not retried, and `r` works after the fix; forbidden namespace reported next to a working one.
- **Terminal:** escape sequences are sanitized (see E15); resizing down to 20×5 is handled on both screens; `ctrl+e` switches environment; `--repo`, `--since` and an unknown `--repo` all work.
- **Performance:**
  - 690 lines/s: about 19% CPU, 110 MB;
  - 2d window on the emitter: 92 k lines loaded in 2.2 s, cut to the 50 000-line buffer with a notice;
  - a 30 min session: 63 MB, 11 threads.
