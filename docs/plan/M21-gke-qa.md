# M21 QA — Real GKE: the resource usage screen

Status: **a plan, not run.** Nothing in this file has been executed against a real cluster. [D-076](../DECISIONS.md) stays "accepted, pending the real-cluster QA" until the checks below are done and reported here.

**What the unit tests and `--demo` cannot prove:** that the numbers match what the cluster says, that the failure states read right with the real API server's errors, and that polling stays light.

Reuses the M14/M15 session: same GKE Autopilot cluster, `huginn-reader`, kubeconfig rules and unconditional teardown ([M15-gke-qa.md](M15-gke-qa.md) §1, §9). Autopilot runs metrics-server by default.

## 1. Fixtures

| Fixture | Purpose |
|---|---|
| A Deployment of 3 pods whose container allocates a known amount of memory (for example a `busybox`/`stress` pod holding 256 MiB) with request 512Mi, limit 512Mi | Memory use near a known value and a known share of the limit |
| A pod burning about one core (`stress --cpu 1`), request 500m, limit 1 | CPU use and `%REQ` above 100 % |
| A Deployment whose container has **no** limit | `—` and `no limit`, never `0 %` |
| A Deployment with a sidecar (the existing Istio-like fixture of M5/M10) | The `A` toggle changes the sums by the sidecar's share |
| A pod created seconds before the check | Counted in `PODS` as `n/m`, not as zero |
| A crash-looping pod (existing) | Its container reads `not measured`; the pod does not lower the shares |
| A Job pod that completed | Not in the usage, not an error |
| Role `qa-restricted` without the `metrics.k8s.io` grant, in a second namespace | `no access: pods.metrics.k8s.io` for that namespace only |

## 2. Checks

**Functional**

1. **Agreement with the cluster.** For each fixture namespace, compare every row with `kubectl top pods -n <ns> --containers` taken in the same 15 s: CPU within 10 % or 20m, memory within 5 % (both are averages over the same window; do not compare to an instant). Record the differences.
2. **Shares.** `%LIM` of the memory fixture equals use ÷ limit computed by hand from `kubectl get pod -o yaml`; the no-limit fixture reads `—` for `%LIM` and `no limit` in NOTE.
3. **Hottest pod.** With one of three pods holding much more memory, `PEAK POD` names it and the namespace's share of the sum reads lower than that pod's.
4. **Containers toggle.** `A` changes the sums by the sidecar's use and request; the status bar says which mode.
5. **Drill-down.** `enter` on the service lists its pods (names as in `kubectl get pods`), on a pod its containers; `L` opens the logs of that service; `p` shows requests and limits equal to `kubectl describe`.
6. **Failure states.** Without the grant: only the restricted namespace is marked, the others work. On a cluster where `v1beta1.metrics.k8s.io` is unavailable (scale metrics-server to 0, put it back afterwards): the screen says `no metrics served` and the services screen is unaffected. Stop the network for 2 minutes: values stay, dated `stale since`; back online: they refresh without a restart.
7. **Switching environment** (`ctrl+e`) from the usage screen returns to the services screen of the new environment with no usage watch left running.

**Non-functional**

8. **API load.** Count the requests of one session with the API server's audit log or `kubectl get --raw /metrics` (`apiserver_request_total` for `metrics.k8s.io`, verb `list`): with N namespaces and the default `15s`, about N lists per 15 s while the screen is open, **zero** within 20 s after leaving it.
9. **No second informer set.** With the services screen and the usage screen opened one after the other, the pod and workload `watch` count stays that of one session (the shared catalog watch); compare with the audit log.
10. **Memory.** `/proc/<pid>/status` (`VmRSS`) before and after 30 minutes on the usage screen with the history at its default: no growth beyond the history bound.
11. **Read-only.** The audit log of the session shows no verb other than `get`, `list`, `watch` and `pods/log`, and for the metrics group only `list`.

**Non-regression:** run `deploy/lab/e2e.sh`, `e2e-m10.sh` and `e2e-m12.sh` unchanged; the services screen's `u` key must not change any existing binding (the help screen lists `u` once).

## 3. What to report

A table of the measured differences of check 1, the request counts of checks 8 and 9, and every check that failed or could not be run, with the reason. Findings that change a default (`usage.refresh`, thresholds) are decisions for the owner, not edited here.

## 4. Cost

The extra cost over the M15 session is a few small pods for the length of the session; tear everything down with the cluster (M15 §9).
