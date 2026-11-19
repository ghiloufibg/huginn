# M16 — The release running in an environment, and its commit, in the services preview  (status: design; owner's algorithm adopted 2026-11-19; open points §12)

**Goal:** when a microservice is selected on the services screen, its preview shows **which commit is running** in the current environment, with its **message** (subject), who deployed it and when, and **how many trunk commits are not deployed yet**. With trunk-based development every deploy comes from the same branch, so a version or a date does not say which code is live; the commit does.

This is a design only. Nothing below is implemented. Facts come from the code (cited), the GitLab and Kubernetes documentation, or the owner's own check; anything assumed is marked **to verify** and goes into the QA (§11).

## 1. The algorithm (the owner's, 2026-11-19)

```
1. Running pod  → status.containerStatuses[].imageID  → digest  sha256:…            (Kubernetes, already watched)
2. Digest already resolved in this session?  → done                                   (a digest never changes release)
3. GitLab: deployments of the project to the environment, status success, newest first, a few per page
4. For each deployment: its pipeline's promote job (e.g. promote-prd) → its log
       the log prints the pod's digest?  → that deployment's sha is the running commit. Stop.
5. No match within the limit → the pod is not on a recent release (rollback beyond the limit, or a manual rollout)
       → say so, and fall back to the image tag checked by git (§5)
6. The commit → subject, author (from GitLab's deployment); "N not deployed" (git, §6)
```

**Why this is the right source:** GitLab records, for every deployment to an environment, the exact commit (`sha`) the pipeline deployed. Matching the digest the cluster actually runs against the digest the promote job printed **proves** which deployment is live, instead of trusting a tag that can be moved. **Checked by the owner on a real pod:** the `promote-prd` log prints the same `sha256:…` as the pod's `imageID` (so the multi-architecture "index vs platform digest" mismatch does not occur in this setup).

## 2. What exists today, and what is missing

| Fact | Where | Consequence |
|---|---|---|
| The preview has WORKLOADS, DEPLOYED, PODS, WARNINGS, SIDECARS | `tui/preview.go` | The commit becomes a section after DEPLOYED (owner's choice: the preview, since messages can be long). |
| `Container.Image` (spec reference) is converted; the running **digest** (`status.containerStatuses[].imageID`) is **not** | `kubernetes/convert.go:46`, `setStatus` | One field added to the adapter; no new request or permission. |
| `domain.WorkloadVersion` finds a workload's version from its pods by template revision, with `old→new` during a rollout | `domain/status.go:159` | The digest(s) are picked the same way, so a rollout shows both releases. |
| Every listed service has a local clone at `repos_root/<repo>` (the owner's `manifests` rule) | `config/config.go:51`, `docs/CONFIG.md` §5 | The clone's `origin` URL gives the GitLab host and project path: nothing configured per repository. |
| Hidden files of the config folder are ignored by the fixed-names check | `docs/CONFIG.md` §2 | A `.env` next to the config files is allowed as is. |
| A dotenv reader and a `domain.Secret` type (never printed) already exist | `adapters/driven/localfiles` `ReadEnv`, `domain/secret.go` | The token is read and carried with existing, reviewed code. |
| `sops` is already run behind a port with a context and timeout | `adapters/driven/sops` | The `git` CLI adapter (§6) follows the same pattern. |

## 3. Configuration (all application knowledge here, none in code)

### 3.1 `environments.yaml`: the GitLab environment of each Huginn environment

```yaml
environments:
  rec:
    context: gke_acme_europe-west1_main
    namespaces: [app-rec]
    gitlab:
      environment: rec          # GitLab environment name for this Huginn environment
      job: promote-rec          # the job whose log prints the deployed digest
  prd:
    context: gke_acme_europe-west1_main
    namespace_from: sops:../infra/overlays/prd/config.env#K8S_NAMESPACE
    production: true
    gitlab:
      environment: prd
      job: promote-prd
```

An environment without `gitlab` shows the commit from the fallback only (§5). Both keys are required when `gitlab` is present; no default (they are the company's names).

### 3.2 `releases.yaml` (new, optional; absent = feature off)

```yaml
version: 1
provider: gitlab
url: https://gitlab.acme.com      # optional; default: the host of each clone's origin URL, over https
token:
  file: .env                      # relative to the config folder
  key: GITLAB_TOKEN
remote: origin                    # optional; default origin: the clone's remote naming the GitLab project
per_page: 5                       # deployments read per request (owner: "a few")
max_deployments: 20               # how far back before saying "not a recent release"
max_log_bytes: 20MB               # a job log is read up to this size
compare_ref: origin/HEAD          # optional; default origin/HEAD (the remote's default branch)
refresh: 1m                       # optional; how old the git cache copy may be (§6)
repos:                            # optional per-repository overrides
  billing-gateway: { compare_ref: origin/release }
```

### 3.3 The token: `.env` in the config folder

```
# <config folder>/.env
GITLAB_TOKEN=glpat-…
```

- A personal or project access token with scope **`read_api`** and at least the **Reporter** role (needed for job logs). Huginn only sends `GET` requests.
- Read with the existing dotenv reader, wrapped in `domain.Secret` immediately; never logged, never in an error message, never in a bug report (`B`); the diagnostic log's redactor knows it.
- **Checks at start-up, as warnings, not errors:** the file is readable by others (mode not `0600` on Unix) → warning with the `chmod` to run; the config folder is a git repository (`docs/CONFIG.md` §12 suggests sharing it) and `.env` is **not** git-ignored (`git check-ignore`) → warning with the line to add to `.gitignore`. A token committed by accident is the main risk of this choice.
- Missing file or key → the COMMIT line says `no GitLab token (.env GITLAB_TOKEN)`; the rest of Huginn is unaffected.

## 4. GitLab, exactly

The project is the clone's `origin` URL path (`git@gitlab.acme.com:payments/payment-service.git` → `payments/payment-service`, URL-encoded as the project id). Calls (REST v4, header `PRIVATE-TOKEN`):

| Step | Request | Reads |
|---|---|---|
| Deployments | `GET /projects/:id/deployments?environment=<env>&status=success&order_by=finished_at&sort=desc&per_page=<n>&page=<p>` | per deployment: `sha`, `ref`, `finished_at`, `user`, `deployable` (the job, with its `pipeline` and the commit's `title`, `author_name`, `created_at`) |
| Promote job | if the deployment's own job (`deployable.name`) is the configured `job`, use it; else `GET /projects/:id/pipelines/:pipeline_id/jobs?per_page=100` and pick it by name | job id |
| Log | `GET /projects/:id/jobs/:job_id/trace`, **streamed**, searched for the full `sha256:<64 hex>`, stopped at the first match or `max_log_bytes` | match or not |

- The search is a streaming substring match that also catches a digest split across two read buffers; the log is never kept in memory or on disk.
- **A rollout from the pipeline still in progress:** the pod may already run the new digest while GitLab's deployment is still `running`. When no success matches, one more request with `status=running` (first page) lets the line say `being deployed by pipeline #48211` instead of "not a recent release". **To verify** with the owner's pipeline (§12.2).
- **Errors are classified** at the adapter boundary into the existing domain kinds: 401 → `ErrUnauthorized` (token invalid or expired), 403 → `ErrForbidden` (role too low for logs), 404 → project not found (the clone's remote is not this GitLab), 429 → rate limited (stop, retry after `Retry-After`), network → `ErrUnreachable` (VPN down).

**Cost:** only for the selected service, in the background, once per digest per session. Worst case for a digest not found: `max_deployments / per_page` list calls + up to `max_deployments` job lists + logs; typical case (running the latest release): 1 list call + 1 log. Requests are sequential per project, never in parallel bursts.

## 5. When no deployment matches: the fallback

Shown with what was tried, never as an error:

| Situation | Line |
|---|---|
| No match in the last `max_deployments` | `not a recent prd release (rollback or manual rollout)` in the warning style, then the fallback's commit if any |
| Fallback 1: a hash in the image tag (`2.14.3-9f03b11` → `9f03b11`) that git confirms is a commit of this repository | `≈ 9f03b11 <subject> (from the image tag)` |
| Fallback 2: the image tag is a git tag (`2.14.3`, then `v2.14.3`) | `≈ 9f03b11 <subject> (from git tag v2.14.3)` |
| Neither | `commit unknown (tag 2.14.3)` |

A guess is shown only when **git confirms** it, so a version like `1.0.10` is never taken for a hash; the `≈` and the source in brackets say it is not proven by a deployment.

## 6. Git: the message fallback and "N not deployed"

GitLab's deployment already gives the subject, author and date, so git is needed for **the count** and for the fallback of §5. The owner wants fresh data, not the possibly stale local clone, and nothing written to the user's repository, so Huginn keeps **its own copy** (decided earlier, unchanged):

| Step | Command (`GIT_TERMINAL_PROMPT=0`, stdin closed, SSH in batch mode, timeout) | Writes to |
|---|---|---|
| Find the remote | `git -C <clone> remote get-url <remote>` | nothing |
| First time | `git init --bare <cache>` + `git -C <cache> remote add origin <url>` | Huginn's cache only (`<user cache dir>/huginn/git/<hash of URL>.git`) |
| Refresh (when older than `refresh`) | `git -C <cache> fetch --filter=tree:0 --prune origin '+refs/heads/*:refs/remotes/origin/*'` + `remote set-head origin --auto` | Huginn's cache only; commits only, no files |
| Not deployed | `git -C <cache> rev-list --count --end-of-options <sha>..<compare_ref>` | nothing |
| Subject (fallback only) | `git -C <cache> log -1 --no-walk --format=%H%x00%h%x00%an%x00%aI%x00%s --end-of-options <sha>` | nothing |

`compare_ref` defaults to `origin/HEAD` (the remote's default branch) and is configurable globally or per repository. Results: `up to date with origin/HEAD`, `N not deployed on origin/HEAD`, or `not on origin/HEAD` (warning: a hotfix branch or a force-push). Remote unreachable → the user's clone is read instead and the count is marked `(local, may be stale)`. Every SHA is checked as `^[0-9a-f]{7,40}$` before it reaches argv.

## 7. Design by layer

### 7.1 Domain (`internal/core/domain`, stdlib only)

```go
// Container gains: ImageID string // the digest the kubelet runs, "sha256:…"; empty before the pull

// Release is a deployment of a commit to an environment, as the CI recorded it.
type Release struct {
    SHA, Ref, Subject, Author string
    CommittedAt, DeployedAt   time.Time
    DeployedBy                string
    Pipeline                  int    // pipeline number, for the line
}

type ReleaseState int // ReleaseFound | ReleaseDeploying | ReleaseNotRecent | ReleaseGuessed | ReleaseUnknown

type RunningRelease struct {
    State    ReleaseState
    Release  Release
    Guess    string        // where a guessed commit came from: "image tag", "git tag v2.14.3"
    Behind   int           // commits on compare_ref not deployed; -1 unknown
    OnTrunk  bool
    Stale    bool          // count read from the local clone
}

// RunningDigests is pure, like LastDeployment: the digests of the
// application container (ContainerFilter skips sidecars) of w's newest
// revision, and of the previous one during a rollout.
func RunningDigests(w Workload, pods []Pod, f ContainerFilter) (current, previous string)

// MatchDigest reports whether s is a full "sha256:<64 hex>" digest.
func MatchDigest(s string) bool
```

### 7.2 Ports (`internal/core/ports`)

Shaped by what the core needs, not by GitLab's API (rule 5):

```go
// ReleaseHistory reads the deployments a CI system recorded. Driven port;
// read only.
type ReleaseHistory interface {
    // Deployments lists successful (or running) deployments of project to
    // env, newest first, one page at a time.
    Deployments(ctx context.Context, project, env string, status DeployStatus, page, perPage int) ([]Deployment, error)
    // PromoteJob returns the id of the job named name in the deployment's pipeline.
    PromoteJob(ctx context.Context, project string, d Deployment, name string) (int64, error)
    // LogContains streams a job's log and reports whether it contains needle,
    // reading at most limit bytes.
    LogContains(ctx context.Context, project string, job int64, needle string, limit int64) (bool, error)
}

// CommitLog: the git cache copy (§6). Driven port.
type CommitLog interface {
    Refresh(ctx context.Context, dir, remote string) error
    Commit(ctx context.Context, dir, remote, sha, compareRef string) (domain.Commit, error)
    TagCommit(ctx context.Context, dir, remote, tag string) (string, error)
}

// Releases is the use case the TUI calls (driving port).
type Releases interface {
    Running(ctx context.Context, repo string, env domain.Env, digest, imageTag string) (domain.RunningRelease, error)
}
```

Fakes in `portstest`; `RunReleaseHistoryContract` and `RunCommitLogContract` suites.

### 7.3 Application (`internal/core/app/releases.go`)

The algorithm of §1 lives here, as a mechanism (no names): pages through `Deployments`, asks `PromoteJob` and `LogContains`, stops at the first match, then the fallback of §5 and the count of §6. Caches: `(project, env, digest) → Release` for the session (immutable); a "not recent" result re-checked after `refresh` (a release may finish meanwhile); one lookup in flight per digest. Clock injected.

### 7.4 Driven adapters

- **`adapters/driven/gitlab`** (new): `net/http` with the proxy and CA settings of the environment, `GET` only, token from bootstrap as a `domain.Secret`, streaming log search, error classification of §4. Base URL from `releases.yaml` `url` or the remote's host.
- **`adapters/driven/gitcli`** (new): §6.
- **`kubernetes`**: `ImageID` copied from the container status.
- **`demo`**: fakes for both ports with deterministic releases (latest, two behind, being deployed, not recent with a tag fallback, no token) so `--demo` and the goldens show every state.
- Two new `archtest` rules; `examples/config` gets a `releases.yaml` and `gitlab:` keys with placeholder names; nothing GitLab-specific about the company in code.

### 7.5 TUI (`adapters/driving/tui/preview.go`): a COMMIT section after DEPLOYED

```
DEPLOYED   payment-service  Nov 18 16:02  3h ago
COMMIT     9f03b11  fix(payment): retry 3DS callback on timeout  · jdoe, pipeline #48211  · 12 not deployed on origin/HEAD
```

| State | Line (dim = secondary) |
|---|---|
| Found | `9f03b11  <subject>  · <deployer>, pipeline #N  · N not deployed on origin/HEAD` (tail dropped first when narrow; subject cut with `…`) |
| Up to date | `… · up to date with origin/HEAD` (dim) |
| Not on the trunk | `… · not on origin/HEAD` (warning) |
| Searching | `searching GitLab releases…` (dim) |
| Being deployed | `being deployed by pipeline #48211` (warning) |
| Rollout in the cluster (two digests) | `9f03b11→c27e4d0  <new subject>` (warning) |
| Not a recent release | `not a recent prd release` (warning) + the fallback (`≈ … (from the image tag)`) |
| No token / token refused / role too low | `no GitLab token (.env GITLAB_TOKEN)` / `GitLab refused the token` / `the token cannot read job logs (Reporter needed)` (dim) |
| GitLab unreachable | `GitLab unreachable` + the fallback (dim) |
| Environment without `gitlab:` | the fallback only |
| No `releases.yaml` | no COMMIT section (no change for existing users) |

Several workloads: one line when they run the same digest (the usual case), else one per workload prefixed by its name. No new key: the SHA is copied with the terminal's selection (owner's decision). Loaded asynchronously like pod events; render never waits.

### 7.6 What does not change

The services table, the logs and Kafka screens, the cluster RBAC, existing config files (`gitlab:` and `releases.yaml` are optional).

## 8. Edge cases (each a unit test; the starred ones also a real case)

| Case | Expected |
|---|---|
| ★ Pod runs the latest prd release | Found on the first page, first log |
| ★ Pod runs a release 3 deployments old (others failed or were reverted) | Found; `N not deployed` counts the newer commits |
| ★ GitLab "re-deploy" of an old deployment (rollback) | A new deployment with the old sha: found |
| ★ `kubectl set image` by hand / rollback older than `max_deployments` | `not a recent prd release` + fallback |
| ★ Pipeline still running, pod already on the new digest | `being deployed by pipeline #N` |
| Two deployments of the same digest (re-run) | The newest one |
| Promote job retried (two jobs with the same name in a pipeline) | The last one that succeeded |
| Digest split across two read buffers | Still matched |
| Log over `max_log_bytes` | Read up to the limit; no match → next deployment; noted in the diagnostic log |
| Pod not started yet (empty `ImageID`) | `waiting for the image` (dim) |
| Sidecar images | Skipped (ContainerFilter) |
| Token missing / expired / Guest role | The matching dim line; Huginn otherwise unaffected |
| `.env` world-readable or not git-ignored | Start-up warning |
| GitLab 429 | Stops, retries after `Retry-After`, says `GitLab rate limit` meanwhile |
| Monorepo (several services, one project) | Same project; deployments filtered by environment only — **to verify** (§12.3) |

## 9. Non-functional

| Aspect | Design |
|---|---|
| Permissions | Cluster: none added. GitLab: `read_api`, Reporter, `GET` only. Git: read only on the remote, nothing written to the user's clone |
| Latency | Typical lookup: 2 requests (list + one streamed log); shown as `searching…`; cached per digest for the session |
| Load on GitLab | Selected service only, sequential, once per digest; the git cache fetch once per `refresh` per remote |
| Memory | Logs streamed, never kept; one cache entry per digest |
| Security | Token as `domain.Secret`, redacted everywhere, file-mode and `.gitignore` warnings; SHAs validated before argv; commit subjects sanitized before render |
| Failure | Every failure is a dim reason on the COMMIT line and, where possible, the fallback |

## 10. Test plan

- **Domain**: `RunningDigests` (rollout, sidecars, not started), `MatchDigest`.
- **App**: the algorithm against fakes: match on page 1, page 3, none; running deployment; retried job; cache and re-check; fallback order; errors per kind.
- **gitlab adapter**: contract suite against an `httptest` server reproducing GitLab's responses (pagination headers, 401/403/404/429, a 30 MB log, a digest across a buffer boundary); the token never appears in errors or logs.
- **gitcli adapter**: contract suite on a temporary bare "remote" and a clone; the user's clone unchanged afterwards.
- **Config**: schema (`make schema`), validation with positions, `.env` reading and the two warnings.
- **TUI goldens**: each state of §7.5 at 80, 120, 200+ columns, bottom and side panel.

## 11. Real verification

| ID | Action | Expected |
|---|---|---|
| R-1 | Select a prd service on its latest release | COMMIT shows the deployment's sha, subject, deployer, pipeline; equals the GitLab environment page |
| R-2 | A service whose prd is a few releases behind | Found; `N not deployed` equals `git rev-list --count` on a fresh clone |
| R-3 | Watch a prd deploy end to end | `being deployed…` (if §4's running case holds), then found |
| R-4 | GitLab re-deploy of an older deployment | Old sha found |
| R-5 | Token removed, expired, Guest role | The three dim lines; no crash, no token in `~/.cache`/diag logs |
| R-6 | VPN off | `GitLab unreachable` + fallback; count `(local, may be stale)` |
| R-7 | Requests made | Measured: number per lookup, time to first result |

## 12. Decisions

**Decided by the owner (2026-11-19):** the algorithm of §1; the promote log prints the pod's digest (checked); GitLab environment and job names configured per environment (§3.1); the token in `.env` in the config folder (§3.3); the commit in the preview, subject only; no new key; "not deployed" against `origin/HEAD` by default, configurable; git CLI, read only, never writing to the user's clone; clones at `repos_root/<repo>`.

**Still open:**
1. **Job name:** is it `promote-<env>` in every repository, or does it differ per repository? (If it differs, a per-repository override in `releases.yaml`.)
2. **Deploys in progress:** does the pod start running the new digest while GitLab's deployment is still `running` (deploy job = promote job), or is the cluster changed by Flux after the job succeeded? This decides whether the `status=running` request of §4 is useful.
3. **Monorepos:** does any GitLab project hold several services? If yes, deployments of the same environment would mix services, and the matching job must be told apart (by name, or by searching the digest only, which already disambiguates).
4. **Persisting the digest→release cache on disk** across runs (`~/.cache/huginn`), to avoid re-reading logs at each start: recommended later, not in the first cut.
5. **Numbering:** M16, decision D-074.

## 13. Done when (this design)

This file exists, every claim is cited, checked by the owner or marked to verify, and §12's open points are answered. Implementation is a separate step.
