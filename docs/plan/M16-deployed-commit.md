# M16 — The commit running in an environment, and its message, in the services preview  (status: design; owner decisions of 2026-11-19 applied, two still open §11)

**Goal:** when a microservice is selected on the services screen, its preview shows **which commit is running** in the current environment and, when it can be found, **that commit's message** (subject, author, date). With trunk-based development every deploy comes from the same branch, so a version like `v2.14.3`, or a timestamp, does not say which code is live; the commit does. Optionally, how far that commit is **behind the trunk** ("12 commits not deployed").

This is a design only. Nothing below is implemented. Facts come from the code (cited) or from the Kubernetes / OCI / git documentation; anything assumed is marked **to verify** and goes into the real-GKE QA (§10).

## 1. What exists today, and what is missing

| Fact | Where | Consequence |
|---|---|---|
| The preview has WORKLOADS, DEPLOYED, PODS, WARNINGS, SIDECARS; WORKLOADS ends with the image tag, DEPLOYED with the date of the last rollout (M15.1) | `tui/preview.go` `workloads()`, `deployed()` | The commit fits as a section next to DEPLOYED: same per-workload shape, same room allocation. |
| `domain.WorkloadVersion` already finds the version of a workload from its pods, grouped by template revision, and says `old→new` during a rollout | `domain/status.go:159` | The commit is found the same way, so a rollout shows `a41c9e2→9f03b11`. |
| `Container.Image` is the image reference of the spec (`…/payment-service:v2.14.3`); `ImageTag()` parses it | `domain/container.go:94`, `kubernetes/convert.go:46` | A commit embedded in the tag can be read with no new request. |
| The **digest** actually running (`status.containerStatuses[].imageID`) is **not** converted | `kubernetes/convert.go` `setStatus` | Needed only for a registry source (phase 2, §6). |
| `Pod` carries `Labels`, but **no annotations**; `Workload` carries `Labels` and `Annotations` | `domain/pod.go:22`, `domain/workload.go` | A pipeline-written commit in pod annotations needs the adapter to copy the configured keys (and only those). |
| `huginn.yaml` `repos_root` points at the user's local clones; `{repo_dir}` = `repos_root/<repo>` or a `path` override (Kafka profiles only today) | `config/config.go:51`, `core/app/kafka.go:142` | Each clone gives the **remote URL** (GitLab); the remote gives a fresh commit message and trunk, with the user's own git access, no token in Huginn. |
| Huginn already runs one external CLI behind a port (`sops`) with a context and a timeout | `adapters/driven/sops/sops.go:97` | A `git` CLI adapter follows an existing, reviewed precedent. |
| RBAC: pods, events, deployments, statefulsets, daemonsets — get/list/watch | `deploy/gke-qa/rbac.yaml` | Every cluster source below works with no new permission. |

## 2. Two questions, two different answers

1. **Which commit is running?** Only the cluster (or the image) knows; but **Kubernetes itself does not record a commit**. Something in the build or deploy pipeline must have put it somewhere: the image tag, a label or annotation, an environment variable, or an OCI label inside the image. *Where* is application knowledge, so it is **configured**, never guessed in code (`ARCHITECTURE.md` rule 4).
2. **What is that commit?** (message, author, date, how far behind the trunk.) Only git knows. The **remote** (GitLab) is read with the `git` CLI, read only, found through the user's local clone (already configured through `repos_root`), since the clone itself may be stale (§4).

They are kept apart on purpose: question 1 is pure and synchronous (it reads what is already watched, on every frame, like `LastDeployment`); question 2 is I/O, asynchronous, cached.

## 3. Question 1 — where the running commit comes from

All cluster sources read objects already watched: **no new permission, informer or request.** The user lists the ones their pipeline uses, in order; the first that yields a commit wins (like `resolve` in `services.yaml`).

| # | Source (`from:` kind) | Example of what pipelines do | Strength | Weakness |
|---|---|---|---|---|
| 1 | **`image_tag`**: a regular expression on the tag of the workload's application container, with a named group `commit` | `:9f03b11`, `:main-9f03b11`, `:2.14.3-9f03b11`, `:20261119.1-9f03b11c4e…` | Zero setup for teams that already tag images by SHA (common in trunk-based CI); the tag is what the kubelet pulled | A tag can be moved (`:latest`, re-pushed); a version-only tag has no commit |
| 2 | **`label`**: a label or annotation key, read on the **pods** of the newest revision first, then on the workload | `commit: 9f03b11…` put by Kustomize `commonAnnotations`, Helm `--set`, or the CD job | Exact; per revision, so a rollout shows old→new | Needs the pipeline to write it; a value on the workload object only (not the pod template) can change without a rollout, so pods are read first |
| 3 | **`env`**: a literal environment variable of the application container's spec | `GIT_COMMIT=9f03b11…` | Often already there for the app's own `/info` endpoint | Only literal `value:`, never `valueFrom` (no Secret/ConfigMap read); the adapter copies **only the configured names**, so no other variable ever leaves it |
| 4 | **`oci_label`** *(phase 2)*: `org.opencontainers.image.revision` from the image config in the registry, looked up by the running digest | Set by `docker buildx --label`, Jib, ko, Buildpacks (**to verify** per tool) | Standard (OCI image-spec annotations); immune to moved tags | Needs registry network access and credentials (Artifact Registry): a new driven adapter, out of the first cut |

**The application container**: the one the existing `ContainerFilter` keeps (sidecars from `containers.yaml` are skipped); with several, the first one in the spec, unless `revision.container` names one. Same rule `WorkloadVersion` uses today.

**A short SHA is fine**: git resolves 7+ hex characters; an ambiguous one is reported as such (§5), never guessed.

**Rejected, with the reason:**

| Rejected | Why |
|---|---|
| `kubectl exec` into the pod to read a file or call `/info`/`/actuator/info` | Needs `pods/exec` or a port-forward: a write-capable permission and traffic into production. Huginn is read-only (D-057 spirit) |
| The image digest alone, shown as "the version" | Exact but meaningless to a human; kept as a fallback identity (§7) and as the key of the phase-2 registry source |
| ReplicaSet / ControllerRevision metadata | Same pod template data, plus a permission the adapter avoids (D-072) |
| Hard-coded conventions (`GIT_COMMIT`, `app.kubernetes.io/version`, "a 7–40 hex tag is a SHA") | Application knowledge in code, forbidden even as a default (`CLAUDE.md`, `archtest`). The example folders show them instead |

## 4. Question 2 — the commit message, from the remote, read only

**Owner's choice:** the `git` CLI (like `sops`), reading the **remote** (GitLab), because the user's local clone may be stale. The local clone is used only to learn **where the remote is**; it is never written to.

### 4.1 How the remote is read without touching the user's repository

`git` cannot read a commit message from a remote without downloading the commit object: `git ls-remote` only lists refs (branch → SHA), and GitLab, like most servers, does not serve `git archive`/single-object reads. So Huginn keeps **its own cache copy** of each repository, separate from the user's clone:

| Step | Command (all with `GIT_TERMINAL_PROMPT=0`, stdin closed, timeout) | Writes to |
|---|---|---|
| Find the remote | `git -C <user clone> remote get-url <remote>` | nothing |
| First time | `git init --bare <cache>/<id>.git` then `git -C <cache> remote add origin <url>` | Huginn's cache only |
| Refresh | `git -C <cache> fetch --filter=tree:0 --prune origin '+refs/heads/*:refs/remotes/origin/*'` then `git -C <cache> remote set-head origin --auto` | Huginn's cache only |
| Message | `git -C <cache> log -1 --no-walk --format=%H%x00%h%x00%an%x00%aI%x00%s --end-of-options <sha>` | nothing |
| Not deployed | `git -C <cache> rev-list --count --end-of-options <sha>..<compare_ref>` | nothing |

- **Read only on the remote:** a fetch only *reads* the server (git's `upload-pack`); nothing is ever pushed. Same credentials the user already has (SSH agent or credential helper); no token in Huginn's config.
- **The user's clone is never modified:** no fetch, no ref, no `FETCH_HEAD`, no lock in it.
- **Small:** `--filter=tree:0` (a *treeless* partial clone) downloads commits only, no files and no trees; the message and the count need nothing else. A large history is tens of MB, once. **To measure** on the biggest real repository (§10).
- **Where:** `<user cache dir>/huginn/git/<hash of the URL>.git` (`~/.cache/huginn` on Linux). Shared by every repository with the same remote (a monorepo is fetched once). Deleting it is always safe; Huginn rebuilds it.
- **When:** the first time a service's COMMIT line is needed, then again when the selected service's copy is older than `revision.refresh` (default 1 minute), in the background; one fetch per remote at a time. The line shows the last known result meanwhile, so the panel never waits on the network.
- **Fallback (VPN down, remote unreachable):** the message is read from the user's local clone instead, if it has the commit, and the count is marked `(local, may be stale)`. Never an error screen.
- **Never prompts:** a prompt would break the TUI. `GIT_TERMINAL_PROMPT=0`, no stdin, and SSH in batch mode (`-c core.sshCommand="ssh -o BatchMode=yes"` unless the user configured their own `core.sshCommand`, **to verify** with the real GitLab). An authentication failure reads `cannot reach the remote (authentication)`.

### 4.2 "Not deployed", defaulting to `origin/HEAD`

`origin/HEAD` is the remote's default branch (set by `remote set-head --auto`), so the default works for any repository whatever its trunk is called; it is git's own convention, not application knowledge. `revision.compare_ref` changes it (`origin/main`, `origin/release/2026.11`, …), globally or per repository. The count is `git rev-list --count <deployed>..<compare_ref>`: commits on the trunk that are not in production.

| Result | Line |
|---|---|
| 0 | `up to date with origin/HEAD` (dim) |
| N > 0 | `N not deployed` |
| The deployed commit is not on the trunk (hotfix branch, reverted, force-pushed) | `not on origin/HEAD` (warning style): an important thing to know in trunk-based development |

Only the subject (first line of the message) is read: the body can be long and the preview gives the commit one line.

### 4.3 Rejected

| Rejected | Why |
|---|---|
| `git fetch` into the user's clone | Writes to the user's repository (refs, objects, `FETCH_HEAD`, can conflict with their own git commands); the owner wants read-only |
| Reading only the user's local clone | Stale: a commit deployed after the last `git pull` is unknown, and the count would be wrong (owner's point) |
| `git ls-remote` alone | Gives the trunk's SHA but no message and no count |
| GitLab REST API | Needs a token in the config and an HTTP adapter; the git CLI reuses the user's existing access. Can be added later behind the same port |
| A full clone, or `--filter=blob:none` | Downloads trees or files that are never read |

## 5. Design

### 5.1 Domain (`internal/core/domain`, stdlib only)

```go
// RevisionRule is one configured way to find the commit of a workload.
type RevisionRule struct {
    Kind    RevisionKind   // RevImageTag | RevLabel | RevEnv
    Key     string         // label/annotation key or env name
    Pattern *regexp.Regexp // RevImageTag only; has a "commit" group
}

type RevisionInfo struct {
    Commit, Previous string        // Previous set during a rollout (old→new)
    Source           RevisionKind  // where it came from, shown dim when not exact
}

// DeployedRevision is pure, like LastDeployment: it reads the workload and
// its pods as watched, calls nothing, and allocates nothing in the common case.
func DeployedRevision(w Workload, pods []Pod, f ContainerFilter, rules []RevisionRule) RevisionInfo

// Commit is what git says about one commit.
type Commit struct {
    SHA, Author, Subject string
    At                   time.Time
    Behind               int  // commits on compare_ref not in SHA; -1 unknown
    OnTrunk              bool // SHA is an ancestor of compare_ref
    Stale                bool // read from the user's local clone, the remote being unreachable
}
```

`Pod` gains `Annotations map[string]string` and `Container` gains `Env map[string]string`, **filled only with the configured keys** (nil otherwise: no memory cost for users who do not configure them).

### 5.2 Ports (`internal/core/ports`)

```go
// CommitLog reads commits from a source repository. Driven port.
type CommitLog interface {
    // Refresh brings Huginn's cache copy of the remote of the clone at dir
    // up to date (read only on the remote, never writes to dir).
    Refresh(ctx context.Context, dir, remote string) error
    // Commit describes sha from the cache copy, else from the clone at dir.
    // ErrNotFound when neither has it, ErrAmbiguous for an ambiguous short SHA.
    Commit(ctx context.Context, dir, remote, sha, compareRef string) (domain.Commit, error)
}

// Revisions is the use case the TUI calls (driving port).
type Revisions interface {
    Describe(ctx context.Context, repo, sha string) (domain.Commit, error)
}
```

Fake in `portstest` plus a contract suite (`RunCommitLogContract`) that builds a temporary git repository in the test.

### 5.3 Application (`internal/core/app/revisions.go`)

- Maps a repository to its folder: `path` override, else `repos_root/<repo>`; no folder → `ErrNoClone` (shown, not an error screen).
- **Refresh schedule**: asks `Refresh` when the selected service's remote copy is older than `revision.refresh` (default 1 minute); one refresh per remote at a time; a failure is retried after the same delay, not in a loop.
- **Cache**: a commit's subject, author and date are immutable, so `(remote, full SHA)` is kept for the run; `Behind`/`OnTrunk` are re-read after each refresh. A miss is re-read after the next refresh.
- No I/O of its own; the clock is injected.

### 5.4 Driven adapters

- **`adapters/driven/gitcli`** (new): runs the commands of §4.1, owns the cache folder (path injected by bootstrap from `os.UserCacheDir`), parses NUL-separated output, classifies errors (`unknown revision` → not found, `ambiguous` → ambiguous, `Permission denied`/`Authentication failed` → unauthorized, `Could not resolve host`/timeout → unreachable, missing binary → unavailable). Registered as `git` in bootstrap; covered by a new `archtest` rule.
- **`kubernetes`**: copies the configured pod annotation keys and literal env values; nothing else changes.
- **`demo`**: a fake `CommitLog` with deterministic commits for the demo services (one ahead/behind, one not in the clone, one with no rule match) so `--demo` and the goldens show every state.

### 5.5 Config (`services.yaml`, plus `make schema` and `docs/CONFIG.md`)

```yaml
revision:
  # Tried in order; the first that yields a commit wins.
  from:
    - image_tag: '^(?:.+-)?(?P<commit>[0-9a-f]{7,40})$'
    - label: deploy.example.com/commit       # pod first, then workload; label or annotation
    - env: GIT_COMMIT                         # literal values only
  container: app                              # optional; default: first non-sidecar container
  remote: origin                              # optional; default origin: the remote of the local clone to read
  compare_ref: origin/HEAD                    # optional; default origin/HEAD (the remote's default branch)
  refresh: 1m                                 # optional; default 1m: how old the cache copy may be
  repos:                                      # optional per-repository overrides
    billing-gateway: { compare_ref: origin/release }
repos:                                        # optional; open point §11.2
  payment-service: { path: ~/work/monorepo }
```

Validation at load, with positions: the pattern compiles and has a `commit` group; `compare_ref` is a valid ref name; `refresh` is at least 10 s; `revision` without `repos_root` and without any `path` is allowed (the commit is shown, the message says `no clone configured`). No default for any key. The examples (`examples/config*`) show the common conventions. `origin` and `origin/HEAD` are git's own conventions, not application knowledge, so they may be defaults (to record in D-074).

### 5.6 TUI (`adapters/driving/tui/preview.go`)

A new section **COMMIT**, right after DEPLOYED:

```
WORKLOADS  Deployment payment-service  2/2 ready  2/2 updated  2.14.3-9f03b11
DEPLOYED   payment-service  Nov 1 14:32  3h ago
COMMIT     9f03b11  fix(payment): retry 3DS callback on timeout  · Jane Doe, Oct 31  · 12 not deployed on origin/HEAD
PODS       …
```

| State | Line (dim = secondary) |
|---|---|
| Found, message read | `9f03b11  <subject>  · <author>, <date>  · N not deployed` (the tail is dropped first when narrow; the subject is cut with `…`) |
| Up to date with the trunk | `… · up to date with origin/HEAD` (dim) |
| Not on the trunk | `… · not on origin/HEAD` (warning style) |
| First fetch of the remote running | `9f03b11  fetching from the remote…` (dim) |
| Remote unreachable, local clone used | `… · 12 not deployed (local, may be stale)` (dim tail) |
| Remote refuses the credentials | `9f03b11  <subject from the local clone, if any>  · cannot reach the remote (authentication)` (dim) |
| Rollout in progress | `9f03b11→c27e4d0  <new subject>` in the warning style, like WORKLOADS' `old→new` |
| Loading | `9f03b11  …` |
| Not on the remote nor in the clone | `9f03b11  unknown to the remote` (dim): an image built from an unpushed or deleted commit |
| Ambiguous short SHA | `9f03b11  ambiguous in your clone` (dim) |
| No local clone (so no remote URL) / git missing | `9f03b11  no clone of this repository` / `git not found` (dim) |
| No rule matched | `no commit found (tag 2.14.3)` (dim): tells the user *why*, and the doc says how to configure it |
| No `revision` section | the section is not shown at all (zero change for existing users) |

- **Several workloads**: one line when they all run the same commit (the usual case for one repository), else one line per workload prefixed with its name, as DEPLOYED does.
- **Room**: one line, given after DEPLOYED and before PODS (same reasoning as D-072: it is why the panel is opened).
- **No new key** (owner's decision): the SHA is selected and copied with the terminal's own selection. The short SHA shown is git's `%h`, which git keeps unambiguous in that repository, so it is enough to paste into `git show`.
- **Only the subject** of the message, on the one line, cut with `…` at the panel's width (owner's decision: the preview, not a table column, because messages can be long).
- Loaded asynchronously with the same pattern as pod events (`preview.watch` / `eventsTTL`): render never waits for git.

### 5.7 What does not change

The services table (VERSION and AGE columns), the logs and Kafka screens, existing RBAC, existing config files (the section is optional).

## 6. Phase 2 (designed so it is an adapter, not a redesign)

- **Registry source** (`oci_label`): convert `imageID` (digest), new `ImageMetadata` port, Artifact Registry adapter; credentials through `SecretsProvider` or ADC.
- **GitLab API** `CommitLog` adapter, selected by name (`revision.log: git | gitlab`), if the git CLI turns out not to be enough.
- **"What is not deployed"**: a small list (`git log --oneline <sha>..<compare_ref>`, max 20) on a key, answering "will my merged fix be in the next deploy?".
- **Compare environments**: `rec 9f03b11 · prd 4c1a77e (8 behind rec)` — needs the catalog of another environment; a later milestone.

## 7. Edge cases (each a unit test; the first five also a real-cluster case)

| Case | Expected |
|---|---|
| Tag `2.14.3-9f03b11`, pattern with `commit` group | `9f03b11` |
| Tag `latest` / `2.14.3` only, no other rule | `no commit found (tag 2.14.3)` |
| Image pinned by digest (`@sha256:…`) | Tag rules do not match; label/env rules still apply |
| Rollout in progress | `old→new`, new subject; settles to the new one |
| Rollback | The old commit again (pods of the old template are the newest revision) |
| Label on the workload differs from the pods' (stamp updated without a rollout) | The pods' value wins |
| Two containers both non-sidecar | First in spec, unless `container:` is set |
| Commit pushed and deployed after the user's last `git pull` | Found through the cache copy (the reason for reading the remote) |
| Remote unreachable (VPN down) | Message from the local clone if it has it; count marked `(local, may be stale)` |
| SSH key not loaded / token expired | `cannot reach the remote (authentication)`; no prompt ever appears in the terminal |
| Trunk renamed (`master` → `main`) | `origin/HEAD` follows it after the next refresh (`remote set-head --auto`) |
| Deployed commit not on the trunk (hotfix branch, force-push) | `not on origin/HEAD` |
| Ambiguous 7-char SHA | `ambiguous in your clone` |
| Clone missing / not a git repo / no such remote / git not installed | Dim reason, never an error screen |
| Monorepo: several repositories share one clone | `repos.<name>.path` → same dir; cache shared |
| Subject with control characters or wide runes | Sanitized (existing `ansi` width handling) |
| A crafted label value (`--upload-pack=…`) | Rejected by the hex check before argv; `--end-of-options` as a second guard |

## 8. Non-functional

| Aspect | Design |
|---|---|
| Permissions | None added on the cluster. The remote is only read (fetch); the user's clone is only read (`remote get-url`, and `log` as a fallback, with `GIT_OPTIONAL_LOCKS=0`); writes go only to Huginn's own cache folder |
| Latency | First fetch: seconds to tens of seconds for a large history (**to measure**), shown as `fetching…`; later fetches are incremental (a few KB). `log`/`rev-list` on the cache copy: milliseconds. All asynchronous; timeouts 60 s for a fetch, 2 s for a read |
| Network | One incremental fetch per remote per `refresh` (1 min) and only for the selected service: negligible for GitLab |
| Disk | Commits only (treeless); **to measure** on the largest repository; the folder is documented and safe to delete |
| CPU per frame | `DeployedRevision` is pure and runs for the selected service only, like `LastDeployment`; the regexp runs on the tag of the newest revision only, with the result memoized per tag |
| Memory | Configured annotation/env keys only; one cache entry (~200 B) per commit seen |
| Security | The SHA is validated before reaching argv; env values other than the configured names are never copied; the subject is user data and is sanitized before render; nothing goes to the diagnostic log beyond the SHA |
| Failure | Every failure degrades to a dim reason on the COMMIT line |

## 9. Test plan

- **Domain**: table tests for every rule kind and order, rollout/rollback, sidecar skip, digest-pinned images, zero pods.
- **gitcli**: contract suite against a temporary bare repository used as the "remote" (`file://` URL) and a clone of it: first fetch, incremental fetch, a commit pushed to the remote after the clone (found), `origin/HEAD` after renaming the default branch, a commit not on the trunk, unreachable remote → local fallback, unknown SHA, not a repo, missing binary, timeout, and **the user's clone is byte-for-byte unchanged afterwards**; skipped with a message if `git` is absent.
- **App**: cache hit/miss/expiry with the fake clock; one call in flight per key.
- **Config**: schema, validation errors with positions, examples load.
- **TUI goldens**: each state of §5.6 at 80, 120, 200+ columns, bottom and side panel; existing goldens change only where a `revision` section is configured (the demo folder).
- **archtest**: new rule for `gitcli`; `examples/` hold every convention, the code none.

## 10. Real-GKE / real-repo verification

| ID | Action | Expected |
|---|---|---|
| C-1 | Deploy an image tagged `<version>-<sha>` built from a known commit | COMMIT shows that SHA and subject; equals `git log -1 <sha>` |
| C-2 | Same with a pipeline annotation on the pod template | Same, source `label` |
| C-3 | Roll out a new commit; watch during and after | `old→new`, then new |
| C-4 | `kubectl rollout undo` | Old commit again |
| C-5 | Deploy a commit pushed after the local clone's last pull | Subject shown from the remote; the local clone unchanged (`git status`, refs, `FETCH_HEAD` mtime) |
| C-6 | Default `origin/HEAD` with 3 newer commits merged on the trunk | `3 not deployed on origin/HEAD`; `up to date` after deploying the head; same with `compare_ref: origin/<other>` |
| C-6b | Real GitLab over SSH and over HTTPS (credential helper); SSH agent stopped | Works with the user's access; with no agent, `cannot reach the remote (authentication)` and no prompt |
| C-6c | VPN off | Local fallback, `(local, may be stale)` |
| C-7 | `huginn-reader` RBAC | Works unchanged |
| C-8 | Largest real repository | First treeless fetch: time and disk measured; preview never stalls |

## 11. Decisions

Answered by the owner on 2026-11-19:

| # | Question | Decision |
|---|---|---|
| 2 | Where the message comes from | The `git` CLI, like `sops`, reading the **remote** (the local clone may be stale), read only. Done through Huginn's own treeless cache copy (§4.1), since git cannot read a message from a remote without fetching it, and fetching into the user's clone would write to it |
| 3 | "Not deployed" | Shown; default `origin/HEAD`, changed by `revision.compare_ref` (globally or per repository) |
| 4 | `git fetch` | Never into the user's clone; only into Huginn's cache copy |
| 6 | Copy / open keys | None: the terminal's selection copies the SHA |
| 7 | Placement | The COMMIT line of the preview, subject only |

Still open (explained to the owner in plain terms):

1. **Where the running commit is written by the pipeline (§3).** Kubernetes only knows the image, for example `europe-docker.pkg.dev/acme/apps/payment-service:2.14.3-9f03b11`. Huginn needs one real example of a production image reference and, if its tag has no commit in it, whether the pipeline writes the commit elsewhere (a label or annotation, an environment variable like `GIT_COMMIT`). This decides which `from:` rules the example config shows and which the first cut must support.
2. **Finding each repository's local clone (§5.5).** Huginn reads the remote URL from the clone at `repos_root/<repository name>` (the setting already used by the `manifests` rule and Kafka). If every clone sits there under the service's name, nothing more is needed. If some do not (another folder name, a monorepo), a per-repository `path` is needed; today that setting only exists inside Kafka profiles, and the proposal is to move it to `services.yaml` so both features share it.
8. **Numbering:** M16, decision D-074.

## 12. Done when (this design)

This file exists, every claim is cited or marked to verify, and §11 is answered. Implementation is a separate step.
