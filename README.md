# Huginn

> Odin's raven of thought: it flies out to your pods and brings back what they are saying.

Huginn is a keyboard-driven, **read-only** terminal UI for reading the logs of application pods on Kubernetes (GKE), from inside your IDE's terminal. It feels like k9s and kl, but does one thing: help a developer debug from logs.

**Status: prototype, milestone M4.2 done: works against real clusters (GKE, kind, minikube).** Everything Huginn knows about your applications comes from a [config folder](docs/CONFIG.md) you provide. Services screen (with a WHY column and a preview of the selected service) and logs screen (merged live logs of a repository's application containers, drawn with the layout of your config folder, time windows, follow/pause, pod scope, zoom), level and live text filters with highlight, and help on every screen (`?` / `F1`). Crash loops show as waiting and `P` reads the previous instance; `9` shows the first lines of each container (how it started), of the previous instance with `P`. A local lab ([`deploy/lab`](deploy/lab)) runs everything against a real cluster without GKE. See [`docs/plan/M0.md`](docs/plan/M0.md) and the design mockups linked from [`docs/DECISIONS.md`](docs/DECISIONS.md).

## Try it

Requires Go 1.26+.

```sh
make build            # or: CGO_ENABLED=0 go build -o bin/huginn ./cmd/huginn
./bin/huginn --demo   # synthetic cluster and the embedded examples/config folder;
                      # no credentials needed; q quits. A live rollout of
                      # payment-service plays ~90 s after start
./bin/huginn prd --demo
./bin/huginn --help
```

## Connecting to GKE

Huginn reads your kubeconfig (`KUBECONFIG`, else `~/.kube/config`) and never logs in by itself:

1. Install `gcloud` and the `gke-gcloud-auth-plugin`, then `gcloud auth login`.
2. `gcloud container clusters get-credentials <cluster> --region <region> --project <project>` creates the kube context.
3. Put that context and your namespaces in `environments.yaml` of your config folder (or `namespace_from: sops:<file>#<key>` to read the namespace from a sops-encrypted dotenv file, with the `sops` command installed — the encrypted file itself must sit **outside** the config folder, e.g. `sops:../namespace.env.enc#KEY`, since the config loader rejects any file in the folder that isn't one it recognizes).
4. `huginn --config <folder> <env>`.

It only gets, lists and watches pods, workloads and events, and reads pod logs, in the configured namespaces (never cluster-wide): namespaced read access (`pods`, `pods/log`, `events`, and `deployments`/`statefulsets`/`daemonsets`/`cronjobs` in `apps`/`batch`) is enough, and a workload kind you cannot read is skipped. `HTTPS_PROXY`/`NO_PROXY` and `SSL_CERT_FILE` work as for `kubectl`.

**IAM and RBAC are a union, not an intersection.** Don't grant the
read-only identity a project-level role like `roles/container.viewer`
alongside a namespaced `Role`/`RoleBinding` meant to restrict it — the
broader IAM role alone grants read access to every cluster and namespace
in the project regardless of what RBAC says, silently defeating the
namespace scoping. Grant only the namespaced `Role`/`RoleBinding`; IAM is
still needed to authenticate the principal and fetch cluster credentials,
but nothing above that.

When something is wrong the services screen says what: `unauthorized` (log in again: `gcloud auth login`), `forbidden` for one namespace (the others keep working), `configuration error` for an unknown context (not retried: fix `environments.yaml`), `secrets unavailable` when sops cannot decrypt.

**First run on GKE, checklist:** the services screen lists your repositories with their states; open a repository's logs; press `P` on a service that restarted; switch environment with `ctrl+e`; an environment with `namespace_from` opens.

## Usage

```
huginn [env] [flags]

  huginn --config ~/work/acme-huginn     default environment of the folder
  huginn prd             or: huginn -e prd / huginn --env prd
  huginn --repo payment-service --since 1h
  huginn --demo          synthetic cluster + the embedded example folder

Flags
  -e, --env string        environment: a name of environments.yaml
      --repo string       open this repository's logs directly
      --since string      initial window: a duration (15m, 1h, 2d), tail or head (tail:N, head:N)
      --containers string containers the logs open on: app or all
      --config string     config folder (default: $HUGINN_CONFIG, else <user config dir>/huginn)
      --demo              synthetic cluster
      --theme string      light, accessible, classic, none
      --log-level string  write a diagnostic log (debug, info, warn, error)
      --version
```

Exit codes: `0` success, `1` runtime error, `2` config folder missing or invalid (every problem is printed with its `file:line:column`), or no interactive terminal.

Environment variables: `HUGINN_ENV`, `HUGINN_CONFIG`, `HUGINN_THEME`, `NO_COLOR` (forces the `none` theme), `HUGINN_DEBUG=1` (diagnostic log in the user cache directory, never on screen), `HUGINN_CPUPROFILE=<file>` (CPU profile of the session for `go tool pprof`).

## Services screen

A service whose status changes is highlighted for a few seconds. One row per repository; the **WHY** column says why a service is unhealthy (last exit, OOM limit, image that cannot be pulled, scheduler message, rollout progress). The space the rows leave free shows a **preview** of the selected service — workloads, pods, recent warnings of its worst pod (events read only when the cursor rests on a service), hidden sidecars — on the right from 200 columns, below the rows when 8 lines are free. Sorted by status, rows are grouped (FAILING, DEGRADED/PENDING, ROLLING, HEALTHY) when the titles fit.

Pods that no known workload owns are listed too, grouped by owner: a bare pod shows as `debug-shell (Pod)`, a Job made by hand as `migrate (Job)`, pods of an unknown controller with its kind (`services.yaml`: `standalone_pods`).

## Keys (services screen)

| Key | Action |
|---|---|
| `j` `k` `↑` `↓` `pgup` `pgdn` `g` `G`, mouse wheel | move |
| `enter` | open the repository |
| `/` or `ctrl+f` | filter by name (enter keeps, esc clears) |
| `s` | sort: status, name, restarts, age |
| `p` | preview of the selected service on/off |
| `r` | resync the watches |
| `ctrl+e` | switch environment |
| `esc` | back |
| `q` `ctrl+c` | quit |

## Logs screen

**Head** (`9`, `--since head`): the first `windows.head_lines` lines (500) of each container, merged by time, read from the start of what the node keeps; the view opens on the first line and does not follow (`f` goes back to the default window, following). Nodes rotate container logs, so for a chatty container the start may be gone: the status bar then says `first line at 14:02, the container started at 09:10 (older lines rotated away on the node)`. With `P`, the head of the previous instance shows how the crashed instance started. A pod started during the session loads its own head.

The pod strip shows each pod's state: a container that is not running says `waiting: CrashLoopBackOff` (its logs come back when it restarts), an init container that keeps failing says `waiting: init migrate …` and its output is shown, and pods read `terminating` or `completed` when so. When a pod runs several application containers, the pod column names the container (`9d5px/worker`). Lines recovered after a network outage are placed by time; lines are ordered by when the cluster received them, and a line dated another day shows its date. The status bar shows the stream at a glance: `LIVE 42/s` (live lines per second; `PAUSED +n`, `NO PODS`, `REMOVED` when the repository is gone, `NOT LOADED` after an error, `HEAD` for a head), then the errors and warnings of the current view (`>` / `<` jump to them), the window, pod scope and filters. When a view is empty it says why and which key helps; after a read error, `r` reloads the logs.

## Keys (logs screen)

| Key | Action |
|---|---|
| `j` `k` `pgup` `pgdn` `g` `G`, mouse wheel | move (`G` returns to the live tail) |
| `>` `<` | next / previous ERROR |
| `enter` | zoom on the entry (`J`/`K` next/previous, `p` raw JSON, `enter` metadata) |
| `f` | follow on/off |
| `space` | pause / resume (the screen keeps its lines; new ones wait, up to the buffer size, and the lines dropped beyond are counted) |
| `P` | previous instance of the restarted containers (why it crashed, OOM, exit); again for the current logs |
| `t` / `T` / `1`…`7` / `0` / `9` | next window / window picker / 15m 30m 40m 45m 1h 1d 2d / tail / head (AZERTY: `&é"'(-è` / `à` / `ç`) |
| `tab` / `S` | cycle pod scope / choose pods and containers (the two lists combine: one container of every pod, or one pod's container) |
| `A` | all containers (sidecars and init) or application containers only (default: `default_mode` of `containers.yaml`) |
| `c` | hide the next column (time, level, thread, class); after the last one, show them again |
| `ctrl+t` | time format: local, UTC, relative (never hides the time) |
| `R` | reset the display (columns, pod id, time format, pan, wrap); filters, window and pods are kept |
| `o` `I` `W` | order, pod id (short, full, hidden), wrap |
| `C` | columns picker: `t` time, `f` time format, `p` pod, `l` level, `h` thread, `c` class, `z` message only, `r` reset |
| `z` | focus layout: hide pod, thread and class (again to restore) |
| `←` `→` `H` `L` | pan when not wrapped |
| `F` | fullscreen |
| `/` or `ctrl+f` | filter as you type (in the prompt: `ctrl+r` regex, `ctrl+x` filter/highlight, `!` prefix inverts, `ctrl+a` stacks another filter, `enter` keeps, `esc` cancels) |
| `x` | filter (hide non-matching) or highlight (keep all) |
| `n` `N` | next / previous match |
| `X` | context lines around matches: 0, 1, 3, 5 |
| `l` / `e` `w` `a` | level picker / errors only, warn+error, all levels |
| `?` `F1` | help for this screen (searchable with `/`) |
| `F2` `ctrl+k` | key bar at the bottom: compact, full, hidden |
| `esc` | exit fullscreen, clear the last filter, then back |

Every key can be remapped with `ui.keymap` in the configuration.

## Configuration: your config folder

Huginn holds no knowledge of any application. You give it a **config folder** with fixed file names:

```
acme-huginn/
├── huginn.yaml           default environment, time windows
├── environments.yaml     kube contexts and namespaces
├── services.yaml         how workloads map to repositories
├── containers.yaml       sidecars to hide              (optional)
├── ui.yaml               theme, keymap, key bar         (optional)
├── formats/<name>.yaml   how to read log lines: JSON fields, a regex for text, chosen per container
└── layouts/<name>.yaml   how to draw log lines: columns as templates such as "[{thread|last:15|right:15}]"
```

It is read from `--config`, `$HUGINN_CONFIG` or `<user config dir>/huginn/` (`~/.config/huginn` on Linux, `~/Library/Application Support/huginn` on macOS, `%AppData%\huginn` on Windows), and checked before the UI opens. If anything is wrong, every problem is listed with its position and Huginn exits.

- **Reference**: [`docs/CONFIG.md`](docs/CONFIG.md) covers every file and key, the template language, the errors and a 15-minute walkthrough.
- **Examples**, tested in CI:
  - [`examples/config/`](examples/config): Spring Boot + logstash JSON; also the `--demo` folder.
  - [`examples/config-node/`](examples/config-node): Node.js pino.
  - [`examples/config-nginx/`](examples/config-nginx): nginx access lines read with a regex, next to JSON logs.
- **Editor completion**: JSON schemas, one per file, in [`docs/schema/`](docs/schema).

## Development

```sh
make test    # go test ./...
make lint    # golangci-lint run (v2, built with Go 1.26)
make cross   # CGO-free builds for linux/darwin/windows x amd64/arm64
make schema  # regenerate docs/schema/*.json after changing internal/config structs
```

Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) before changing code: Huginn uses a hexagonal architecture whose rules are enforced by a test.
