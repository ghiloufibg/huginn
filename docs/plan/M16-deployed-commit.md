# M16 — The release running in an environment, and its commit, in the services preview  (status: implemented 2026-11-19, decision [D-074](../DECISIONS.md); **§6 and the git parts of §7–§10 superseded by [M16.1](M16.1-commits-from-gitlab-api.md), D-075: the commit details now come from GitLab's API, git is removed**; real-environment QA pending: [`M16-qa.md`](M16-qa.md))

**Goal:** when a microservice is selected on the services screen, its preview shows **which commit is running** in the current environment, with its **message** (subject), who deployed it and when, and **how many trunk commits are not deployed yet**. With trunk-based development every deploy comes from the same branch, so a version or a date does not say which code is live; the commit does.

This file was written as the design and **updated after the implementation to describe the code as built** (commits `ebe99c5` and `aed19ca`). Where the implementation departed from the first text, the reason is given in place and in D-074. Facts come from the code (cited), the GitLab and Kubernetes documentation, or the owner's own check; what is still assumed is marked **to verify** and is checked by the QA plan [`M16-qa.md`](M16-qa.md).

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

## 2. What existed, and what was added

| Fact | Where | Consequence |
|---|---|---|
| The preview had WORKLOADS, DEPLOYED, PODS, WARNINGS, SIDECARS | `tui/preview.go` | The commit is a COMMIT section after DEPLOYED (owner's choice: the preview, since messages can be long). |
| `Container.Image` (spec reference) was converted; the running **digest** (`status.containerStatuses[].imageID`) was **not** | `kubernetes/convert.go` `setStatus` | `Container.ImageID` added; no new request or permission. |
| `domain.WorkloadVersion` finds a workload's version from its pods, with `old→new` during a rollout | `domain/status.go` | `domain.RunningImages` picks the digests the same way, so a rollout shows both releases. |
| The owner keeps every clone at `repos_root/<repo>` | `config/config.go` | The clone's remote URL gives the GitLab host and project path: nothing configured per repository. **Found while implementing:** the `manifests` rule of `services.yaml` is validated but not read yet (`bootstrap/catalog.go` skips it; `docs/CONFIG.md` §5 says so), so a service resolved by `labels` can be listed without a clone; its COMMIT line then says `no clone of this repository …`. |
| Hidden files of the config folder are ignored by the fixed-names check | `config/load.go` `ignored` | A `.env` next to the config files is allowed as is. |
| A dotenv parser and a `domain.Secret` type (never printed) existed | `domain/dotenv.go`, `domain/secret.go` | The token is read and carried with existing, reviewed code. |
| `sops` was already run behind a port with a context and timeout | `adapters/driven/sops` | The `git` adapter (§6) follows the same pattern. |

## 3. Configuration (all application knowledge here, none in code)

### 3.1 `environments.yaml`: the GitLab names of each Huginn environment

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

**Names that differ per service** (owner: the job name must be configurable; monorepos exist): both values accept the placeholders `{repo}` (the Huginn repository) and `{workload}` (the Deployment, StatefulSet… name), so a convention is written once, for example `job: promote-{workload}-prd` or `environment: prd/{workload}`. Exceptions go in `releases.yaml` `repos:` (§3.2). Any other placeholder is a load error with its position.

### 3.2 `releases.yaml` (new, optional; absent = feature off)

```yaml
version: 1
provider: gitlab
url: https://gitlab.acme.com      # optional; default: https:// and the host of each clone's remote
token:
  file: .env                      # a file of the config folder
  key: GITLAB_TOKEN
remote: origin                    # optional; default origin: the clone's remote naming the GitLab project
per_page: 5                       # optional, default 5 (1 to 100): deployments read per request (owner: "a few")
max_deployments: 20               # optional, default 20: how far back before saying "not a recent release"
max_log_bytes: 20MiB              # optional, default 20MiB: a job log is read up to this size
compare_ref: origin/HEAD          # optional; default origin/HEAD (the remote's default branch)
refresh: 1m                       # optional, default 1m, at least 10s: how old the git copy, and a "not recent" answer, may get
repos:                            # optional per-repository exceptions
  billing-gateway:
    compare_ref: origin/release
    job: { prd: release-billing-prd }          # per Huginn environment; same placeholders as §3.1
  platform-monorepo:                           # a GitLab project holding several services
    max_deployments: 60
    workloads:
      payment-service:
        paths: [services/payment/, libs/common/]   # what counts as this service's code, for "not deployed" (§6)
        job: { prd: promote-payment-prd }
        environment: { prd: prd/payment }
```

Validated at load, with positions (`config/releases.go`): `releases.yaml` needs `repos_root`; `url` starts with `https://` or `http://`; `token.file` stays in the config folder; `remote` and `compare_ref` are plain names (no leading `-`, no `..`); the limits are in range; per-environment keys name environments of `environments.yaml`; `paths` are relative folders. Reference: `docs/CONFIG.md` §11.

### 3.3 The token: `.env` in the config folder

```
# <config folder>/.env
GITLAB_TOKEN=glpat-…
```

- A personal or project access token with scope **`read_api`** and at least the **Reporter** role (needed for job logs). Huginn only sends `GET` requests.
- Read by `bootstrap/releases.go` with `domain.ParseDotenv` **on each request** (at most 64 KiB), so a token added or renewed while Huginn runs is used at once; wrapped in `domain.Secret` immediately; never logged and never in an error message, so it cannot reach the diagnostic log or a bug report (`B`).
- **Checks at start-up, as a warning in the status bar (`Options.Notice`), not errors:** the file is readable by others (mode not `0600`, Unix only) → the `chmod` to run; the config folder is a git work tree (`docs/CONFIG.md` §13 suggests sharing it) and the file is **not** git-ignored (`git check-ignore`) → the `.gitignore` to edit. A token committed by accident is the main risk of this choice.
- Missing file or key → the COMMIT line says `no GitLab token (.env GITLAB_TOKEN)`; the rest of Huginn is unaffected.

## 4. GitLab, exactly

The project is the clone's remote URL path (`git@gitlab.acme.com:payments/payment-service.git` → `payments/payment-service`, URL-encoded as the project id; `domain.ParseGitRemote` reads scp-like, `ssh://`, `https://` and `http://` URLs). The API base is `releases.yaml` `url`, else `https://` and the remote's host. Calls (REST v4, header `PRIVATE-TOKEN`, `GET` only):

| Step | Request | Reads |
|---|---|---|
| Deployments | `GET /projects/:id/deployments?environment=<env>&status=success&order_by=id&sort=desc&per_page=<n>&page=<p>` | per deployment: `id`, `sha`, `ref`, `finished_at` (else `updated_at`), `user.username`, `deployable` (the job: `id`, `name`, `pipeline.id`, and the commit's `title`, `author_name`, `created_at`) |
| Promote job | if the deployment's own job (`deployable.name`) is a wanted job name, it is used with **no request**; else `GET /projects/:id/pipelines/:pipeline_id/jobs?per_page=100` (up to 10 pages; GitLab returns the last run of each job), keeping the jobs with a wanted name | job ids |
| Log | `GET /projects/:id/jobs/:job_id/trace`, **streamed** in 32 KiB reads, searched for the full `sha256:<64 hex>` of every digest still wanted for that job, stopped when all are found or at `max_log_bytes` | which digests it prints |

- **Ordered by `id`, not `finished_at`** (changed from the first text): some GitLab versions refuse `order_by=finished_at` without a `finished_after`/`finished_before` range; ids grow with creation, which is the order wanted.
- The search keeps the last `len(digest)-1` bytes of each read, so a digest split across two reads is still found; the log is never kept in memory or on disk. JSON answers are read to the end, so HTTP connections are reused.
- **No "in progress" case on the GitLab side** (owner: the promote job does not touch the cluster; Flux applies the change afterwards). When the pod runs a new digest, the GitLab deployment that printed it has already succeeded, so only `status=success` is read. A rollout in the cluster (two digests) is still shown, from the pods (§7.5).
- **Monorepos** (owner: some GitLab projects hold several services). Deployments of one environment then mix services, and one pipeline may promote several. Matching by digest already tells them apart, so no deployment is mistaken for another; but the "few newest" may all belong to other services. So: (1) when the job name names the service (`{workload}` in §3.1), deployments whose job does not match are skipped **without reading their log**; (2) each log read is searched for **every digest Huginn is looking for in that project at once** (all workloads of the selected repository), so one pass answers several services; (3) `max_deployments` should be larger for a monorepo (per-repository override).
- **Errors are classified** at the adapter boundary into the existing domain kinds, with the message the COMMIT line shows: 401 → `ErrUnauthorized` `GitLab refused the token (expired or revoked?)`; 403 → `ErrForbidden` `the token cannot read <project> (read_api scope and Reporter role needed)`; 404 → `ErrNotFound` `GitLab has no project <project> visible to the token`; 429 → `ErrUnreachable` `GitLab rate limit, retry after Ns`; network → `ErrUnreachable` `GitLab unreachable`; a redirect to another host → `ErrConfig`, refused (it would carry the token header). A missing token is `ErrUnauthorized` `no GitLab token (.env GITLAB_TOKEN)`, decided before any request.

**Cost** (only for the service the cursor rests on, in the background, one search at a time per project and environment):

| Case | Requests |
|---|---|
| Running the latest release, the deployment's own job is the promote job | 1 list + 1 log, then **none** for the rest of the session (a digest never changes release) |
| Same, the deployment's job is another job | 1 list + 1 pipeline job list + 1 log |
| Found *k* deployments back | ⌈*k*/`per_page`⌉ lists + up to *k* logs (+ up to *k* job lists) |
| Not found, first time (worst case) | ⌈`max_deployments`/`per_page`⌉ lists + up to `max_deployments` logs |
| Not found, re-checked after `refresh` (every minute while the service stays selected) | **1 list**: the search stops at the newest deployment already searched, a finished job's log is never searched twice for the same digest, and a pipeline's jobs are listed once per session. (Changed from the first text, which re-read every log each minute; D-074 "Cost".) |

## 5. When no deployment matches: the fallback

Shown with what was tried, never as an error:

| Situation | Line |
|---|---|
| No match in the last `max_deployments` | `not a recent prd release` in the warning style, then the fallback |
| Fallback 1: a part of the image tag that is a commit id (`2.14.3-9f03b11` → `9f03b11`; 7 to 40 hex characters, not all digits, longest first) that git confirms | `not a recent prd release  · ≈ 9f03b11c  <subject>  · from the image tag  · <count>` |
| Fallback 2: the image tag is a git tag (`2.14.3`, then `v2.14.3`) | `… · ≈ 9f03b11c  <subject>  · from the git tag v2.14.3  · <count>` |
| Neither | `not a recent prd release  · commit unknown (tag 2.14.3)` |

A guess is shown only when **git confirms** it, so a version like `1.0.10` or a build date is never taken for a hash; the `≈` and the source say it is not proven by a deployment. The same fallback runs, without `not a recent … release`, for an environment without `gitlab:` and when GitLab fails (its reason is then appended).

## 6. Git: "N not deployed" and the fallback

GitLab's deployment already gives the subject, author and date, so git is needed for **the count**, for the fallback of §5, and for the subject when a deployment has none. The owner wants fresh data, not the possibly stale local clone, and nothing written to the user's repository. Git cannot read a commit from a remote without fetching it, so Huginn keeps **its own partial copy** of each remote (`adapters/driven/gitcli`):

| Step | Command (no stdin, `GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=never`, `GIT_OPTIONAL_LOCKS=0`, `GIT_NO_LAZY_FETCH=1`, `LC_ALL=C`; timeout 10 s, 90 s for the network) | Writes to |
|---|---|---|
| Find the remote | `git -C <clone> remote get-url <remote>`, **reused for 1 minute** | nothing |
| First time | `git init --bare <cache>`, then `git config` of `remote.origin.url`, the refspecs `+refs/heads/*:refs/remotes/origin/*` and `+refs/tags/*:refs/tags/*`, `remote.origin.promisor true`, `remote.origin.partialclonefilter <filter>`, `gc.auto 0` | Huginn's copy only: `<user cache dir>/huginn/git/<first 8 bytes of sha256(url), hex>.git` |
| Refresh (at most every `refresh`, only while a service of that remote is looked up) | `git -C <cache> fetch --quiet --prune --no-tags --filter=<filter> origin`; `git -C <cache> remote set-head origin --auto` **at most hourly** (a round trip; the default branch rarely changes) | Huginn's copy only |
| Count, and whether on the trunk | `git rev-list --count --left-right --end-of-options <sha>...<compare_ref>`: one walk gives the commits only `<sha>` has (0 = on the trunk) and those only the trunk has (not deployed). With `paths`, one more `rev-list --count <sha>..<compare_ref> -- <paths>` | nothing |
| Subject (fallback, or a deployment without one) | `git log -1 --no-walk --format=%H%x00%an%x00%aI%x00%s --end-of-options <sha>^{commit}` | nothing |
| Git tag (fallback 2) | `git rev-parse --verify --quiet refs/tags/<tag>^{commit}` | nothing |

`<filter>` is `tree:0` (commits only) or, when a workload of the remote has `paths`, `blob:none` (commits and trees, still no file contents); a commits-only copy is deleted and fetched again the first time trees are needed. Every read is tried in the copy first and, if the copy lacks the object or does not exist, in the user's clone, and the answer is then marked **stale**. SSH runs with `BatchMode=yes` unless the user set `GIT_SSH_COMMAND`, `GIT_SSH` or `core.sshCommand`. A SHA must be 7 to 40 lower-case hex characters, a reference or remote a plain name (no leading `-`, no `..`), a path must not start with `-`: nothing else reaches git's command line. A lookup costs **2 git processes** and a refresh within the hour 2 (counted by a test).

**Monorepos:** the count must only include commits that touch the service, else every commit of a neighbour service would read as "not deployed". With `paths` set for a workload (§3.2), the count is limited to them. A workload of a monorepo (a repository where some workload has `paths`) without `paths` shows `N commits on origin/HEAD since` instead of `not deployed`, so it is never misread.

`compare_ref` defaults to `origin/HEAD` (the remote's default branch: git's own convention, so not application knowledge) and is configurable globally or per repository. Results: `up to date with origin/HEAD`, `N not deployed on origin/HEAD`, or `not on origin/HEAD` (warning: a hotfix branch or a force-push); a stale count adds `(local, may be stale)`.

## 7. Design by layer

### 7.1 Domain (`internal/core/domain/release.go`, stdlib only)

```go
// Container gains ImageID: the image the kubelet runs, as the container
// status reports it ("…@sha256:…"); empty before the pull.

type Release struct {                 // a deployment of a commit, as the CI recorded it
    SHA, Ref, Subject, Author string
    CommittedAt, DeployedAt   time.Time
    DeployedBy                string
    Pipeline                  int64
}

type ReleaseState int                 // ReleaseUnknown | ReleaseFound | ReleaseGuessed

type TrunkCount struct {              // the comparison with compare_ref
    Known, OnTrunk, Scoped, WholeProject, Stale bool
    Ref                                         string
    Behind                                      int
}

type RunningRelease struct {
    State     ReleaseState
    Release   Release
    Guess     string                  // "image tag", "git tag v2.14.3"
    NotRecent bool                    // searched; no recent deployment printed the digest
    Count     TrunkCount
    Problem   error                   // why GitLab or git could not answer; shown, never fatal
}

type WorkloadImage struct{ Workload, Digest, Tag string }

// RunningImages: the image of the application container (ContainerFilter
// skips sidecars) of w's newest pods and, during a rollout, of the previous
// ones; told apart by digest, ordered by their first pod. Allocates nothing.
func RunningImages(w Workload, pods []Pod, f ContainerFilter) (current, previous WorkloadImage)

func ImageDigest(imageID string) string        // "sha256:<64 hex>" or ""
func IsDigest(s string) bool
func IsCommitID(s string) bool                 // 7 to 40 lower-case hex characters
func CommitCandidates(tag string) []string     // parts of a tag that may be a commit id, longest first
func ShortSHA(sha string) string               // 8 characters on screen
func ParseGitRemote(url string) (GitProject, bool)
```

Changed from the first text: `RunningDigests`/`MatchDigest` became `RunningImages` (the tag is needed for the fallback) and `IsDigest`; the states `ReleaseDeploying`/`ReleaseNotRecent` became the `NotRecent` flag (no deployment is ever "running", §4, and "not recent" combines with a guess); the count moved into `TrunkCount`; the short id is 8 characters, not 7 (fewer collisions in large repositories). `ContainerFilter.PrimaryApp` was rewritten to allocate nothing (it built a slice per pod), which also lightens the existing version column.

### 7.2 Ports (`internal/core/ports/releases.go`)

Shaped by what the core needs, not by GitLab's API (rule 5):

```go
type ReleaseProject struct{ BaseURL, Path string }

// ReleaseHistory reads the deployments a CI system recorded. Read only.
type ReleaseHistory interface {
    // Deployments lists the successful deployments of p to env, newest
    // first, one page (1-based) of perPage at a time.
    Deployments(ctx context.Context, p ReleaseProject, env string, page, perPage int) ([]Deployment, error)
    // PipelineJobs lists the jobs of a pipeline (the last run of each).
    PipelineJobs(ctx context.Context, p ReleaseProject, pipeline int64) ([]Job, error)
    // LogFind streams a job's log and returns which of needles it contains,
    // stopping once all are found or after limit bytes.
    LogFind(ctx context.Context, p ReleaseProject, job int64, needles []string, limit int64) ([]string, error)
}

type GitRepo struct{ Dir, Remote string }

// CommitLog reads commits with git, read only on the remote and the clone;
// stale says the clone answered because the copy of the remote could not.
type CommitLog interface {
    RemoteURL(ctx context.Context, r GitRepo) (string, error)
    Refresh(ctx context.Context, r GitRepo, trees bool) error
    Commit(ctx context.Context, r GitRepo, sha string) (c Commit, stale bool, err error)
    Tag(ctx context.Context, r GitRepo, tag string) (sha string, stale bool, err error)
    Count(ctx context.Context, r GitRepo, sha, ref string, paths []string) (behind int, onRef, stale bool, err error)
}

type ReleaseQuery struct {
    Env    domain.Env
    Repo   string
    Images []domain.WorkloadImage
}

// Releases is the driving port behind the COMMIT line; results by digest.
type Releases interface {
    Running(ctx context.Context, q ReleaseQuery) (map[string]domain.RunningRelease, error)
}
```

Changed from the first text: no `DeployStatus` (only successful deployments are read, §4); `PromoteJob` became `PipelineJobs`, since choosing the job by name is a rule of the core, not of GitLab; `TagCommit` became `Tag`; the git port takes a `GitRepo` and reports `stale`. Fakes `FakeReleaseHistory` and `FakeCommitLog` are in `portstest`, with a `RunReleaseHistoryContract` suite run against the fake and the GitLab adapter. The git port has no shared contract suite: its adapter is tested against real git (§10) and its only other implementation is the demo's.

### 7.3 Application (`internal/core/app/releases.go`)

The algorithm of §1 lives here, as a mechanism (no names): it reads the clone's remote, refreshes the git copy (at most every `refresh`), pages through `Deployments`, takes each deployment's own job or the pipeline's jobs with a wanted name (`PipelineJobs`), searches their logs with `LogFind`, stops when every wanted digest is found, then applies the fallback of §5 and the count of §6. Settings arrive as plain data (`ReleaseSettings`: names per environment with `{repo}`/`{workload}`, exceptions per repository and workload, limits). State, **in memory only** (owner: no disk cache for now), each map bounded to 4 096 entries:

| State | Kept | Why |
|---|---|---|
| `(project, env, digest) → deployment` | the session | a digest never changes release |
| `(project, env, digest) → not found, newest deployment searched` | re-checked after `refresh` | the next search reads only newer deployments |
| `(project, job, digest)` known absent from that job's log | the session | a finished job's log does not change |
| `(project, pipeline) → jobs` | the session | listed once |
| `remote → last refresh` | — | at most one refresh per `refresh` |

One search runs at a time per project and environment and collects all the wanted digests. The clock is injected.

### 7.4 Driven adapters and wiring

- **`adapters/driven/gitlab`** (new): `net/http` (the environment's proxy settings, a 2-minute client timeout, redirects to another host refused), `GET` only, token from a function given by bootstrap, streaming log search, error classification of §4.
- **`adapters/driven/gitcli`** (new): §6. It also offers `Ignored`, used by bootstrap for the `.gitignore` warning.
- **`kubernetes`**: `ImageID` copied from the container status.
- **`demo`**: synthetic digests on every demo container, a release history derived from the demo pods (one deployment per running version, its promote job printing that version's digests, plus older releases) and a synthetic git, so `--demo` shows a proven release, a rollout `old→new`, releases not recent with each fallback (`search-api`: git tag; `ledger-writer`: commit id in the tag), and the counts.
- **`bootstrap/releases.go`**: builds `ReleaseSettings` from the config, picks the demo or the real adapters, reads the token, computes the start-up notice.
- **`archtest`**: no new rule was needed (the `adapters/driven/` rule covers new adapters); `gitlab` and `gitcli` were added to the packages that must hold no application data, and `promote-` to the forbidden strings. `examples/config` has a `releases.yaml` and `gitlab:` keys with placeholder names.

### 7.5 TUI (`adapters/driving/tui/commit.go`): a COMMIT section after DEPLOYED

```
DEPLOYED   payment-service  Nov 18 16:02  3h ago
COMMIT     9f03b11c  fix(payment): retry 3DS callback on timeout  · jdoe, pipeline #48211  · 12 not deployed on origin/HEAD
```

| State | Line (dim = secondary) |
|---|---|
| Found | `9f03b11c  <subject>  · <deployer>, pipeline #N  · N not deployed on origin/HEAD` (cut at the panel's width, so the tail goes first) |
| Up to date | `… · up to date with origin/HEAD` (dim) |
| Not on the trunk | `… · not on origin/HEAD` (warning) |
| Searching | `searching GitLab releases…` (dim) |
| Rollout in the cluster (two digests) | `4c1a77e0→9f03b11c  <new subject>` (the ids in the warning style) |
| Not a recent release | `not a recent prd release` (warning) `· ≈ 9f03b11c  <subject>  · from the image tag` |
| Image not pulled yet | `waiting for the image` (dim) |
| No token / token refused / role too low | the messages of §4 (dim), after the fallback's commit when git confirms one |
| GitLab unreachable | the fallback, then `· … GitLab unreachable` (dim) |
| No clone at `repos_root/<repo>` | `no clone of this repository in <repos_root>: …` (dim) |
| Environment without `gitlab:` | the fallback only |
| `(no repo)` rows | no COMMIT section |
| No `releases.yaml` | no COMMIT section (no change for existing users) |

Several workloads (a monorepo, or a service with a worker): one line when their lines are identical (the usual case: one pipeline promotes them together, with different digests but the same commit), else one line per workload prefixed by its name, as DEPLOYED does. No new key: the SHA is copied with the terminal's selection (owner's decision). Commit subjects are sanitized (`safeText`) before they are drawn.

**Loading:** once the cursor rests on a service for 300 ms (the pod-events delay), one `Running` call runs in the background (timeout 2 minutes) for all the digests of its workloads, current and previous. The answer is kept a minute (`releasesTTL`) and asked again only while the service stays selected; answers older than 10 minutes are dropped. Render never waits: before the answer the line reads `searching GitLab releases…`. The images of the selected service are computed **once per cluster snapshot**, not per frame (`relMemo`): measured on a 2 000-pod service, the COMMIT line adds about 2 KB and no measurable time to a 3.3 MB, 15 ms frame.

### 7.6 What does not change

The services table, the logs and Kafka screens, the cluster RBAC, existing config files (`gitlab:` and `releases.yaml` are optional). No new dependency.

## 8. Edge cases (each a unit test; the starred ones also a real case, [`M16-qa.md`](M16-qa.md))

| Case | Expected |
|---|---|
| ★ Pod runs the latest prd release | Found on the first page, first log |
| ★ Pod runs a release 3 deployments old (others failed or were reverted) | Found; `N not deployed` counts the newer commits |
| ★ GitLab "re-deploy" of an old deployment (rollback) | A new deployment with the old sha: found |
| ★ `kubectl set image` by hand / rollback older than `max_deployments` | `not a recent prd release` + fallback |
| ★ Flux applies a release while the preview is open | Two digests during the rollout (`old→new`), the new one resolved as soon as the pods run it |
| ★ Monorepo: the newest prd deployments belong to other services | Skipped by job name without reading logs; the service's own release found further back |
| ★ Monorepo: one pipeline promotes three services | One log pass resolves all three; one COMMIT line if same commit |
| Monorepo workload without `paths` | Count labelled `N commits on origin/HEAD since`, never `not deployed` |
| Two deployments of the same digest (re-run) | The newest one |
| Promote job retried (two jobs with the same name in a pipeline) | The last run: GitLab's pipeline job list returns only the last run of each job |
| Digest split across two read buffers | Still matched (tested at every split position) |
| Log over `max_log_bytes` | Read up to the limit; no match → next deployment |
| The same digest searched again after `refresh` | Only deployments newer than the last search are read; no log is read twice |
| Pod not started yet (empty `ImageID`) | `waiting for the image` (dim) |
| Sidecar images | Skipped (ContainerFilter) |
| Token missing / expired / Guest role | The matching dim line; Huginn otherwise unaffected |
| `.env` world-readable or not git-ignored | Start-up warning in the status bar |
| GitLab 429 | The search stops; the line says `GitLab rate limit, retry after Ns`; asked again with the next lookup (a minute later), not in a loop |
| Remote unreachable | Counts and guesses from the user's clone, marked `(local, may be stale)` |
| SSH key not loaded / no stored credential | No prompt; the copy is not refreshed and the clone answers (stale) |

## 9. Non-functional (as built)

| Aspect | As built |
|---|---|
| Permissions | Cluster: none added. GitLab: `read_api`, Reporter, `GET` only. Git: read only on the remote (fetch), nothing written to the user's clone; writes only in Huginn's cache folder |
| Per frame | Images computed once per snapshot; `RunningImages` and `PrimaryApp` allocate nothing (0.35 ms for 2 000 pods, per snapshot); the COMMIT line adds ~2 KB to a frame |
| Latency | Typical lookup: 1 list request + 1 streamed log (+ the first fetch of the git copy, once); shown as `searching GitLab releases…` |
| Load on GitLab | Only the selected service, sequential; once per digest per session when found; 1 request per minute while a "not recent" service stays selected |
| Git | 2 processes per lookup, 2 per refresh within the hour; one fetch per remote per `refresh` |
| Memory | Logs streamed, never kept; session maps bounded to 4 096 entries each; the TUI drops answers older than 10 minutes |
| Disk | Huginn's copies: commits only (or commits and trees for monorepos with `paths`); **to measure** on the largest repository (QA); safe to delete |
| Security | Token as `domain.Secret`, never logged, read from a file checked at start (mode, `.gitignore`); no redirect to another host; every SHA, ref, remote and path validated before argv; commit subjects sanitized before render |
| Failure | Every failure is a dim reason on the COMMIT line and, where possible, the fallback |

## 10. Tests (as written)

- **Domain** (`release_test.go`): `ImageDigest`, `CommitCandidates`, `IsCommitID`, `RunningImages` (settled, rollout, rollback, a pod still pulling, a restart, other workloads and deleted pods), `ParseGitRemote`, zero allocations, a benchmark.
- **App** (`releases_test.go`): found on the first log; found three deployments back through pipeline jobs; not recent with the tag fallback, and its re-check reading only new deployments; git-tag fallback; an unconfirmed tag not shown; monorepo (other services skipped without reading their log, counts by paths, `WholeProject`); one log pass for several digests; no log read twice for a digest and pipeline jobs listed once; problems (refused token with the guess kept, no clone, not a hosted remote); an environment without names; per repository and workload settings; images without digest.
- **GitLab adapter**: the shared contract suite against an `httptest` GitLab (paging, jobs, a long log, the limit), a digest split at every position across reads, 401/403/404/429/500, unreachable, no token, the token never in an error, a redirect to another host refused without sending the token.
- **Git adapter**, against real git with a local bare "remote": a commit pushed after the clone found through the copy, counts, tags, counts by paths (the copy replaced by one with trees), a commit not on the trunk, the remote gone (stale fallback), options and bad input refused, batch-mode SSH unless the user chose a command, the number of git processes per lookup and refresh, and **the user's clone unchanged** (refs, objects, no `FETCH_HEAD`).
- **Config**: defaults, every validation error with its position, the root-file suggestion; schemas regenerated.
- **Bootstrap**: the token read on each use, the start-up notice (mode, `.gitignore`), the demo wired end to end.
- **TUI**: every line state, the loading on rest and the TTL, one line versus one per workload, the side panel, hidden without `releases.yaml`, the notice; golden `services_preview_commit_160x34`; a frame benchmark with and without the line. No existing golden changed.

## 11. Real verification

Moved to its own plan, [`M16-qa.md`](M16-qa.md): the checks against the owner's GitLab, cluster and Flux, the three assumptions still open (whether a deployment's own job is the promote job, git without a prompt over SSH and HTTPS, the first fetch's time and disk), and the non-functional measurements.

## 12. Decisions

**Decided by the owner (2026-11-19):** the algorithm of §1; the promote log prints the pod's digest (checked); GitLab environment and job names configured per environment (§3.1); the token in `.env` in the config folder (§3.3); the commit in the preview, subject only; no new key; "not deployed" against `origin/HEAD` by default, configurable; git CLI, read only, never writing to the user's clone; clones at `repos_root/<repo>`.

Also decided on 2026-11-19: the job name (and the GitLab environment) configurable per environment with `{repo}`/`{workload}` placeholders, and per repository and workload for exceptions; the promote job does not change the cluster (Flux does, afterwards), so no "running deployment" lookup; monorepos are supported (§4, §6); no disk cache for now, in memory only; the implementation made performant and resource-efficient before any real use (commit `aed19ca`).

Recorded as [D-074](../DECISIONS.md), with the departures from the first text of this file.

## 13. Done when

Design: done (this file). Implementation: done, `go test ./...` and `golangci-lint run` clean, `--demo` showing every state. **Verified:** when the P0 cases of [`M16-qa.md`](M16-qa.md) pass on the owner's environment; D-074's "Not verified" line is then replaced by the findings.
