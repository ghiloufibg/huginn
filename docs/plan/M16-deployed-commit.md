# M16 — The commit running in an environment, and its message, in the services preview  (status: design, awaiting owner decisions §11)

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
| `huginn.yaml` `repos_root` points at the user's local clones; `{repo_dir}` = `repos_root/<repo>` or a `path` override (Kafka profiles only today) | `config/config.go:51`, `core/app/kafka.go:142` | The commit message can be read **offline from the user's own clone**, no token, no network. |
| Huginn already runs one external CLI behind a port (`sops`) with a context and a timeout | `adapters/driven/sops/sops.go:97` | A `git` CLI adapter follows an existing, reviewed precedent. |
| RBAC: pods, events, deployments, statefulsets, daemonsets — get/list/watch | `deploy/gke-qa/rbac.yaml` | Every cluster source below works with no new permission. |

## 2. Two questions, two different answers

1. **Which commit is running?** Only the cluster (or the image) knows; but **Kubernetes itself does not record a commit**. Something in the build or deploy pipeline must have put it somewhere: the image tag, a label or annotation, an environment variable, or an OCI label inside the image. *Where* is application knowledge, so it is **configured**, never guessed in code (`ARCHITECTURE.md` rule 4).
2. **What is that commit?** (message, author, date, how far behind the trunk.) Only git knows. The cleanest source is the **user's local clone** (already configured through `repos_root`); a hosting API (GitHub, GitLab, Bitbucket) is a later adapter behind the same port.

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

## 4. Question 2 — the commit message, cleanly

| # | Source | Verdict |
|---|---|---|
| A | **Local clone, `git` CLI, read-only** (`repos_root/<repo>` or a `path`) | **Recommended first cut.** No token, no network, no new secret; works on a laptop with VPN down; honours the user's own git setup (worktrees, partial clones). |
| B | Hosting API (GitHub/GitLab/Bitbucket REST) | Phase 2, same port. Needs a token (via `SecretsProvider`), network, rate limits; URL and auth are enterprise-specific → config + adapter. |
| C | A second annotation written by the pipeline with the subject (`commit-subject: …`) | Possible as one more `label` key, but discouraged: stale-prone, size-limited, escaping issues. Not designed further unless the owner wants it. |
| D | OCI labels (`org.opencontainers.image.description`) | Rarely the commit message; no. |

**Freshness of a local clone.** A commit made on the trunk and deployed before the user's last `git fetch` is not in the clone. Huginn **never fetches by itself** (network, credential prompts, writing into the user's repository; Huginn is read-only). It shows `9f03b11  not in your clone — git fetch` instead, which is honest and actionable. Whether a key may run `git fetch` on demand is an owner decision (§11.4).

**Exact git calls** (all read-only, `GIT_TERMINAL_PROMPT=0`, `GIT_OPTIONAL_LOCKS=0`, timeout 2 s, the SHA validated as `^[0-9a-f]{7,40}$` before it reaches argv, plus `--end-of-options`):

```
git -C <dir> log -1 --no-walk --format=%H%x00%an%x00%aI%x00%s --end-of-options <sha>
git -C <dir> rev-list --count --end-of-options <sha>..<compare_ref>      # "N not deployed", optional
```

`%s` is the subject only (first line): that is the "user-friendly" part. The body is not read in the first cut (it can be long, and the preview has one line).

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
}
```

`Pod` gains `Annotations map[string]string` and `Container` gains `Env map[string]string`, **filled only with the configured keys** (nil otherwise: no memory cost for users who do not configure them).

### 5.2 Ports (`internal/core/ports`)

```go
// CommitLog reads commits from a source repository. Driven port.
type CommitLog interface {
    // Commit describes sha in the repository at dir. ErrNotFound when the
    // clone does not have it, ErrAmbiguous for an ambiguous short SHA.
    Commit(ctx context.Context, dir, sha, compareRef string) (domain.Commit, error)
}

// Revisions is the use case the TUI calls (driving port).
type Revisions interface {
    Describe(ctx context.Context, repo, sha string) (domain.Commit, error)
}
```

Fake in `portstest` plus a contract suite (`RunCommitLogContract`) that builds a temporary git repository in the test.

### 5.3 Application (`internal/core/app/revisions.go`)

- Maps a repository to its folder: `path` override, else `repos_root/<repo>`; no folder → `ErrNoClone` (shown, not an error screen).
- **Cache**: a commit is immutable, so `(dir, full SHA) → Commit` is kept for the run; `Behind` changes after a fetch, so it is re-read after 30 s. Misses (`not in your clone`) are retried after 30 s, so a `git fetch` in another terminal shows up without restarting. One call in flight per key.
- No I/O of its own; the clock is injected.

### 5.4 Driven adapters

- **`adapters/driven/gitcli`** (new): runs the two commands of §4, parses NUL-separated output, classifies errors (`unknown revision` → not found, `ambiguous argument`/`short object ID … is ambiguous` → ambiguous, missing binary → `ErrUnavailable`). Registered as `git` in bootstrap; covered by a new `archtest` rule.
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
  compare_ref: origin/main                    # optional; enables "N not deployed"
  commit_url: https://git.example.com/acme/{repo}/commit/{sha}   # optional; for the open/copy key
repos:                                        # optional; when the clone is not repos_root/<repo>
  payment-service: { path: ~/work/monorepo }
```

Validation at load, with positions: the pattern compiles and has a `commit` group; `{repo}`/`{sha}` are the only placeholders; `revision` without `repos_root` and without any `path` is allowed (the commit is shown, the message says `no clone configured`). No default for any key. The examples (`examples/config*`) show the common conventions.

### 5.6 TUI (`adapters/driving/tui/preview.go`)

A new section **COMMIT**, right after DEPLOYED:

```
WORKLOADS  Deployment payment-service  2/2 ready  2/2 updated  2.14.3-9f03b11
DEPLOYED   payment-service  Nov 1 14:32  3h ago
COMMIT     9f03b11  fix(payment): retry 3DS callback on timeout  · Jane Doe, Oct 31  · 12 not deployed
PODS       …
```

| State | Line (dim = secondary) |
|---|---|
| Found, message read | `9f03b11  <subject>  · <author>, <date>  · N not deployed` (the tail is dropped first when narrow; the subject is cut with `…`) |
| Up to date with the trunk | `… · up to date with origin/main` (dim) |
| Rollout in progress | `9f03b11→c27e4d0  <new subject>` in the warning style, like WORKLOADS' `old→new` |
| Loading | `9f03b11  …` |
| Not in the clone | `9f03b11  not in your clone — git fetch` (dim) |
| Ambiguous short SHA | `9f03b11  ambiguous in your clone` (dim) |
| No clone / git missing | `9f03b11  no clone configured` / `git not found` (dim) |
| No rule matched | `no commit found (tag 2.14.3)` (dim): tells the user *why*, and the doc says how to configure it |
| No `revision` section | the section is not shown at all (zero change for existing users) |

- **Several workloads**: one line when they all run the same commit (the usual case for one repository), else one line per workload prefixed with its name, as DEPLOYED does.
- **Room**: one line, given after DEPLOYED and before PODS (same reasoning as D-072: it is why the panel is opened).
- **Keys** (services screen): `y` copies the full SHA (existing `Clipboard` port, existing `ActCopy` binding); `o` opens `commit_url` if configured (**needs an `Opener` port**, which `ARCHITECTURE.md` lists but does not exist yet — owner decision §11.6).
- Loaded asynchronously with the same pattern as pod events (`preview.watch` / `eventsTTL`): render never waits for git.

### 5.7 What does not change

The services table (VERSION and AGE columns), the logs and Kafka screens, existing RBAC, existing config files (the section is optional).

## 6. Phase 2 (designed so it is an adapter, not a redesign)

- **Registry source** (`oci_label`): convert `imageID` (digest), new `ImageMetadata` port, Artifact Registry adapter; credentials through `SecretsProvider` or ADC.
- **Hosting API** `CommitLog` adapter (GitHub/GitLab) selected by name: `revision.log: git | github | gitlab`.
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
| SHA not in the clone, then `git fetch` elsewhere | Shown within 30 s without restarting |
| Ambiguous 7-char SHA | `ambiguous in your clone` |
| Clone missing / not a git repo / git not installed | Dim reason, never an error screen |
| Monorepo: several repositories share one clone | `repos.<name>.path` → same dir; cache shared |
| Subject with control characters or wide runes | Sanitized (existing `ansi` width handling) |
| A crafted label value (`--upload-pack=…`) | Rejected by the hex check before argv; `--end-of-options` as a second guard |

## 8. Non-functional

| Aspect | Design |
|---|---|
| Permissions | None added on the cluster. Locally, read-only git commands only; no fetch, no write, no lock (`GIT_OPTIONAL_LOCKS=0`) |
| Latency | `git log -1` on a local clone is a few ms (**to measure** on a large monorepo); async, cached, timeout 2 s |
| CPU per frame | `DeployedRevision` is pure and runs for the selected service only, like `LastDeployment`; the regexp runs on the tag of the newest revision only, with the result memoized per tag |
| Memory | Configured annotation/env keys only; one cache entry (~200 B) per commit seen |
| Security | The SHA is validated before reaching argv; env values other than the configured names are never copied; the subject is user data and is sanitized before render; nothing goes to the diagnostic log beyond the SHA |
| Failure | Every failure degrades to a dim reason on the COMMIT line |

## 9. Test plan

- **Domain**: table tests for every rule kind and order, rollout/rollback, sidecar skip, digest-pinned images, zero pods.
- **gitcli**: contract suite on a temporary repository (commit, short SHA, ambiguous SHA via a crafted object set or a mocked runner, unknown SHA, not a repo, missing binary, timeout); skipped with a message if `git` is absent.
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
| C-5 | Deploy a commit, do not fetch locally | `not in your clone — git fetch`; after a fetch, the subject within 30 s |
| C-6 | `compare_ref: origin/main` with 3 newer commits merged | `3 not deployed`; `up to date` after deploying the head |
| C-7 | `huginn-reader` RBAC | Works unchanged |
| C-8 | Large monorepo clone | `git log -1` latency measured; preview never stalls |

## 11. Decisions needed from the owner

1. **First cut sources:** `image_tag` + `label` + `env` (recommended), or fewer. Registry (`oci_label`) in phase 2 (recommended) or now.
2. **Commit message source:** local clone via `git` CLI (recommended) vs go-git library (no external binary, but a sizeable dependency and different behaviour from the user's git config) vs hosting API first.
3. **"N not deployed":** include with a configured `compare_ref` (recommended), or leave for later. No default ref (rule 4), or allow git's own `origin/HEAD` as a generic default?
4. **`git fetch`:** never (recommended), or on an explicit key with a confirmation.
5. **Repository folders:** move `repos.<name>.path` from Kafka profiles to a single place (`services.yaml` `repos:`) shared by Kafka and this feature (recommended, with Kafka's key kept as an alias), or duplicate it.
6. **Keys:** `y` copy SHA (recommended); `o` open in browser — needs a new `Opener` port (small, but a new adapter per OS). Include now or later?
7. **Placement:** a COMMIT section (recommended) vs a column of the services table (useful for scanning all services; not designed here — the table's VERSION column could show the short SHA when a rule matches).
8. **Numbering:** this is M16; its decision would be D-074.

## 12. Done when (this design)

This file exists, every claim is cited or marked to verify, and §11 is answered. Implementation is a separate step.
