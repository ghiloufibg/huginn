# Huginn lab: a local Kubernetes cluster with dummy workloads

Used to build and test milestone M4 (the real cluster connection) without GKE.

```sh
deploy/lab/up.sh            # kind (default)
deploy/lab/up.sh minikube   # or minikube
huginn --config deploy/lab/config rec
```

## What runs

| Namespace | Workload | State Huginn must show |
|---|---|---|
| app-rec | `payment-service` (Deployment, 2 replicas) | Healthy; a sidecar (`istio-proxy`) and an init container to hide; Spring-style JSON logs with stack traces |
| app-rec | `payment-worker` | Second workload of the same repository (label `app.kubernetes.io/part-of`) |
| app-rec | `catalog-indexer` | CrashLoopBackOff (exit 1) |
| app-rec | `order-orchestrator` | OOMKilled (exit 137, limit 32Mi) |
| app-rec | `document-renderer` | ImagePullBackOff (image does not exist) |
| app-rec | `email-dispatcher` | Pending (Unschedulable: insufficient memory) |
| app-rec | `storefront-web` | nginx access logs (text, `formats/10-nginx.yaml`) next to a traffic generator |
| app-dev | `ledger-writer` (StatefulSet) | Another environment |

- **Rollout:** `kubectl -n app-rec rollout restart deploy/payment-service` plays one.
- **Read-only identity:** `rbac.yaml` creates `huginn-reader`, limited to `app-rec`; `up.sh` adds its kubeconfig context `huginn-restricted` (a 24 h token: run `up.sh` again to renew it). The environment `restricted` of the lab config uses it: `app-dev` is forbidden, `app-rec` works.
- **Tests:** `HUGINN_LAB=1 go test ./internal/adapters/driven/kubernetes` runs the adapter's contract suites against the lab; `deploy/lab/e2e.sh` drives the real binary in a terminal (tmux) and checks the screens. CI runs both (job `lab`).

## Sandboxes and CI runners

`kind.yaml` carries two patches that are harmless elsewhere and needed on
some sandboxed hosts:
- **OOM scores:** `restrict_oom_score_adj` lets pods start where the host forbids lowering OOM scores.
- **cgroup v1:** `failCgroupV1: false` lets Kubernetes ≥ 1.35 run on cgroup v1 hosts.

On a cgroup v1 host without the `hugetlb` controller, the kubelet also needs
`/sys/fs/cgroup/<controller>/kubelet.slice` to exist. Create it inside the
node while the cluster starts:

```sh
docker exec huginn-control-plane sh -c 'for c in /sys/fs/cgroup/*/; do mkdir -p "$c/kubelet.slice/kubelet-kubepods.slice"; done'
```

The node images come from Docker Hub (`kindest/node`), so no access to
`registry.k8s.io` is needed. minikube pulls its control plane from
`registry.k8s.io` unless its preload tarball matches the runtime.
