# M16 QA — The COMMIT line against a real GitLab, cluster and Flux (functional, failure, non-functional, non-regression)  (status: not run)

**Goal:** check the COMMIT line of the services preview ([`M16-deployed-commit.md`](M16-deployed-commit.md), [D-074](../DECISIONS.md)) where the unit tests, the `httptest` GitLab, the local bare git "remote" and `--demo` cannot: the owner's own GitLab, GKE cluster and Flux, from the owner's laptop. It proves four things:

1. **Functional.** The commit shown is the one GitLab deployed and the cluster runs, for the latest release, an older one, a deploy seen live, a rollback and a monorepo service; "N not deployed" equals an independent `git` count.
2. **Failure.** No token, a refused token, a role too low, the VPN off and an SSH key not loaded each give a clear line, never a prompt, a hang or a crash, and the token never leaks.
3. **The three assumptions the implementation could not check** (D-074 "Not verified"): A-1 whether a deployment's own job is the promote job, A-2 git without a prompt over SSH and HTTPS, A-3 the first fetch's time and disk.
4. **Non-functional and non-regression.** The cost stays what D-074 measured on fakes (2 git processes per lookup, 1 GitLab request a minute for a "not recent" service, nothing per frame), and Huginn without `releases.yaml` is unchanged.

Unlike the GKE-lab QAs (M5–M15), **nothing is created**: no cluster, no fixture, no image. Everything is read only, except the deploys of §4 that the team does anyway, and only in `rec`.

## 0. Rules for this session

- **Read only on `prd`.** In `prd`, Huginn is only looked at. Deploys, rollbacks and manual rollouts are done in `rec` only, on a service the owner picks as safe, and only as the team would do them anyway.
- **The token never leaves `.env`.** It is not pasted in a terminal, a chat, a ticket or this report. The `curl` oracle (§2) reads it from a file made for the session and deleted after. The report names services, projects and people by placeholders (`svc-A`, `mono/svc-B`, `dev-1`).
- **Huginn's copies of the remotes** (`~/.cache/huginn/git`) are part of what is measured (A-3); delete them before starting so the first fetch is measured.

## 1. Preconditions

| # | Check | How |
|---|---|---|
| P-1 | A build of `main` at `aed19ca` or later | `make build && ./bin/huginn --version` |
| P-2 | The automated gate passes | `go test ./...`, `golangci-lint run` (v2.14, built with Go ≥ 1.26), `./bin/huginn --demo` shows the COMMIT line |
| P-3 | Config folder: `repos_root` set; `gitlab: {environment, job}` on `rec` and `prd`; `releases.yaml` from `examples/config/releases.yaml`; for one monorepo, `repos.<repo>.workloads.<workload>.paths` and a larger `max_deployments` | `huginn --config <folder>` starts without a config error |
| P-4 | `.env` with `GITLAB_TOKEN=…` (scope `read_api`, role Reporter or above), mode `0600`, ignored by the folder's `.gitignore` | `ls -l .env`; `git -C <folder> check-ignore .env` prints `.env` |
| P-5 | Clones under `repos_root` for the services tested, each with an `origin` pointing at GitLab (one over SSH, one over HTTPS if both are used) | `git -C <repos_root>/<repo> remote get-url origin` |
| P-6 | VPN on, `kubectl` on the `rec` and `prd` contexts | `kubectl --context <ctx> get pods -n <ns>` |
| P-7 | No copy yet | `rm -rf ~/.cache/huginn/git` |

**Choose the services** (write them in the ledger, §3): `svc-A` running its latest `prd` release; `svc-B` whose `prd` is a few releases behind the trunk; `svc-C` in `rec`, safe to deploy and roll back; `mono/svc-D`, a service of a monorepo, with neighbours deployed after it; `svc-E`, the repository with the largest history (for A-3).

## 2. Oracles (independent of Huginn)

| What | Huginn's answer | Oracle |
|---|---|---|
| Running digest | — | `kubectl get pod <pod> -n <ns> -o jsonpath='{.status.containerStatuses[*].imageID}'` |
| Running commit | the COMMIT line (`tmux capture-pane`, or a screenshot) | GitLab's environment page (Operate → Environments → `<env>`): the deployment whose promote job log contains that digest (search the job log page for the digest), and its commit |
| Deployment details | `· <deployer>, pipeline #N` | the same deployment on GitLab |
| Not deployed | `N not deployed on origin/HEAD` | in a **throwaway** clone, fresh: `git rev-list --count <sha>..origin/HEAD` (with `-- <paths>` for a monorepo service) |
| On the trunk | `not on origin/HEAD` or not | `git merge-base --is-ancestor <sha> origin/HEAD; echo $?` in the throwaway clone |
| GitLab's own answers (A-1) | — | `curl` with the token read from a file made for the session: |

```bash
umask 077; printf 'PRIVATE-TOKEN: %s\n' "$(sed -n 's/^GITLAB_TOKEN=//p' <folder>/.env)" > /tmp/qa-gitlab-header
```
```bash
curl -sS -H @/tmp/qa-gitlab-header "https://<gitlab>/api/v4/projects/<group%2Fproject>/deployments?environment=prd&status=success&order_by=id&sort=desc&per_page=5" | jq '.[] | {id, sha, job: .deployable.name, pipeline: .deployable.pipeline.id}'
```
```bash
rm -f /tmp/qa-gitlab-header
```

**Counting git processes** (N-2): put a `git` wrapper first on `PATH` for the Huginn process only; it appends each invocation to a file and runs the real git:

```bash
mkdir -p ~/qa-shim && printf '#!/bin/sh\necho "$(date +%%T) $*" >> ~/qa-shim/calls\nexec /usr/bin/git "$@"\n' > ~/qa-shim/git && chmod +x ~/qa-shim/git
```
```bash
PATH=~/qa-shim:$PATH ./bin/huginn --config <folder> prd
```

**Counting GitLab requests** (N-3) has no oracle that does not need a proxy: Huginn does not log its requests. Either run Huginn behind a local logging proxy (`HTTPS_PROXY` and `SSL_CERT_FILE` pointing at its CA), or rely on A-1 plus the unit tests. The owner decided against a request log in Huginn (§7).

## 3. The ledger

One row per action, written as it happens: time, environment, service, action (look, deploy, re-deploy, VPN off…), the digest (oracle), the deployment and sha (oracle), the COMMIT line (captured), the time from cursor rest to the final line, and any difference. It is the report's appendix.

## 4. Functional cases

`P0` = must pass for M16 to be called verified.

| ID | Pri | Scenario | Expected |
|---|---|---|---|
| Q-1 | P0 | Open `prd`, rest on `svc-A` | `searching GitLab releases…`, then `<sha 8>  <subject>  · <deployer>, pipeline #N  · up to date with origin/HEAD` (or the right count). sha, subject, deployer and pipeline equal the oracle's deployment |
| Q-2 | P0 | Rest on `svc-B` | Found (not `≈`); `N not deployed on origin/HEAD` equals the throwaway clone's count |
| Q-3 | P0 | `rec`: the team deploys a new commit of `svc-C`; keep its preview open from the pipeline's start to the end of Flux's rollout | The old commit until the pods change; `old→new` while both digests run; then the new commit, found, within seconds of the pods changing (a new digest is looked up at once; the one-minute cache applies only to unchanged images), without pressing a key |
| Q-4 | P0 | `rec`: GitLab "re-deploy" of an older deployment of `svc-C` (the rollback as the team does it) | The older commit, found through the new deployment record |
| Q-5 | P1 | `rec`: a rollback older than `max_deployments`, or an image set by hand that Flux has not yet reverted (only if the team allows it) | `not a recent rec release`, then `≈ … from the image tag` / `from the git tag …` when the tag allows, else `commit unknown (tag …)` |
| Q-6 | P0 | `prd`: rest on `mono/svc-D`, whose newest deployments are its neighbours' | Found further back; the count equals `git rev-list --count <sha>..origin/HEAD -- <paths>` |
| Q-7 | P1 | A workload of that monorepo without `paths` | `N commits on origin/HEAD since`, never `not deployed` |
| Q-8 | P1 | A service whose workloads run the same commit (a service and its worker) | One COMMIT line; a service whose workloads differ: one line per workload, prefixed by its name |
| Q-9 | P2 | A service running a commit not on the trunk (a hotfix), if one exists | `not on origin/HEAD` (warning) |
| Q-10 | P1 | An environment without `gitlab:` (remove it for one run) | Only the fallback (`≈ …` or `commit unknown`), no `not a recent … release` |

## 5. Failure cases and the three assumptions

| ID | Pri | Scenario | Expected |
|---|---|---|---|
| F-1 | P0 | `.env` mode `0644`; start | Status bar: `.env is readable by others (chmod 600 …)`; `chmod 600`, restart: gone |
| F-2 | P0 | Remove `.env` from `.gitignore` of the config folder (if it is a git work tree); start | Status bar: `.env is not git-ignored (add it to …/.gitignore)`; put it back |
| F-3 | P0 | Rename `.env`; rest on `svc-A` | `no GitLab token (.env GITLAB_TOKEN)` (with the fallback's commit when git confirms one); rename it back **while Huginn runs**: the next lookup (≤ 1 minute) works without a restart |
| F-4 | P0 | A revoked or expired token | `GitLab refused the token (expired or revoked?)` |
| F-5 | P1 | A token of a user with the Guest role on the project | `the token cannot read <project> (read_api scope and Reporter role needed)` |
| F-6 | P0 | VPN off, then rest on a service not yet looked up, and on one already found | Not yet looked up: the fallback, then `GitLab unreachable`; the count `(local, may be stale)`. Already found: the same commit (kept for the session). No hang beyond the timeouts; Huginn stays responsive |
| F-7 | P0 | **Token never leaks**: after the whole session | `grep -rcF -f <(sed -n 's/^GITLAB_TOKEN=//p' <folder>/.env) ~/.cache/huginn` reports 0 everywhere (diagnostic log at `--log-level debug`, git copies) |
| **A-1** | P0 | For `svc-A`, `svc-B` and `mono/svc-D`: the oracle's `curl` (§2) | Record whether `.deployable.name` is the configured promote job. If yes: 1 request saved per deployment (as designed). If not: the pipeline's job list is read once per pipeline; note which job the deployment points to. **Either answer is correct behavior**; it decides the cost line of D-074 |
| **A-2a** | P0 | SSH remote, key in the agent | The count appears (the copy is fetched); no prompt |
| **A-2b** | P0 | SSH remote, agent emptied (`ssh-add -D`) or a passphrase-protected key not loaded | **No prompt ever appears in the terminal**; the count comes from the clone, `(local, may be stale)` |
| **A-2c** | P1 | HTTPS remote with a stored credential (credential helper) | Works, no prompt; without the stored credential: no prompt, stale |
| **A-3** | P0 | Delete `~/.cache/huginn/git`; rest on `svc-E` (largest history), time to the count appearing; then `du -sh ~/.cache/huginn/git/*`; repeat for the monorepo with `paths` (trees fetched) | Record time and size for both, next to the size of a full clone (`du -sh <repos_root>/<repo>/.git`). Pass: the preview stays responsive meanwhile, and the copy is much smaller than a full clone. The numbers go into D-074 |

## 6. Non-functional and non-regression

| ID | Pri | Measure | Expected |
|---|---|---|---|
| N-1 | P0 | Time from the cursor resting to the final line, `svc-A`, with the copy already fetched | Under 2 s on the office network (recorded, not a hard limit) |
| N-2 | P0 | Git processes (the §2 wrapper): 5 minutes resting on one found service, then 5 minutes on a "not recent" one | After the first minute: about 4 per minute (2 for the refresh, 2 for the lookup); never a burst; `set-head` at most once in the session's first hour |
| N-3 | P2 | GitLab requests (proxy, if used) for the same 10 minutes | Found service: none after the first lookup. "Not recent" service: 1 per minute |
| N-4 | P1 | `ps -o rss,pcpu -p <pid>` every minute during 30 minutes of normal use across 20+ services, against the same run without `releases.yaml` | RSS within a few MB of the run without it; CPU at rest unchanged |
| N-5 | P1 | Scroll the services list fast with a 1 000+ pod service in it | No visible lag compared with the run without `releases.yaml` (optionally `HUGINN_CPUPROFILE`) |
| NR-1 | P0 | The same config folder without `releases.yaml` | No COMMIT section, nothing else changed, no request to GitLab, no git process |
| NR-2 | P0 | Logs screen, Kafka screen, environment switch (`ctrl+e`) while a lookup runs | Unaffected; the line of the new environment is looked up afresh |

## 7. Decided by the owner (2026-11-21)

- **No request log** in Huginn: N-3 is done with a proxy, or not claimed.
- **`max_log_bytes` stays 20 MiB.**
- **Not planned:** a disk cache of results, other CI systems, a list of the commits not deployed.

## 8. Pass and fail

- **M16 verified** when every P0 case of §4, §5 and §6 passes. A-1 and A-3 always "pass" once recorded: they are measurements that complete D-074.
- **Findings** are classified as in the earlier QAs: *Huginn* (code or docs change, then the affected rows are re-run), *environment* (not Huginn), *measured limit* (documented, no change).
- **Not claimed** unless done: Q-5 if the team does not allow a manual rollout; Q-9 if no hotfix exists; N-3 without a proxy.

## 9. Report

Write `deploy/gke-qa/M16-QA-REPORT.md` with placeholders for every company name (§0): the filled tables of §4–§6, the ledger, A-1/A-3 numbers, the findings. Then replace D-074's "Not verified on a real GitLab yet" line with the outcome, set this file's status, and fold any finding into `docs/CONFIG.md`.
