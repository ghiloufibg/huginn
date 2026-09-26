# Huginn

> Odin's raven of thought: it flies out to your pods and brings back what they are saying.

Huginn is a keyboard-driven, **read-only** terminal UI for reading the logs of application pods on Kubernetes (GKE), from inside your IDE's terminal. It feels like k9s and kl, but does one thing: help a developer debug from logs.

**Status: prototype, milestone M2 done.** Services screen and logs screen (merged live logs of a repository's application containers in the Spring Boot layout, time windows, follow/pause, pod scope, zoom). Level and text filters arrive in M3. See [`docs/plan/M0.md`](docs/plan/M0.md) and the design mockups linked from [`docs/DECISIONS.md`](docs/DECISIONS.md).

## Try it

Requires Go 1.26+.

```sh
make build            # or: CGO_ENABLED=0 go build -o bin/huginn ./cmd/huginn
./bin/huginn --demo   # synthetic cluster, no credentials needed; q quits
                      # a live rollout of payment-service plays ~90 s after start
./bin/huginn prd --demo
./bin/huginn --help
```

## Usage

```
huginn [env] [flags]

  huginn                 default environment (rec unless configured)
  huginn prd             or: huginn -e prd / huginn --env prd
  huginn --repo payment-service --since 1h
  huginn --demo          synthetic in-memory cluster

Flags
  -e, --env string        environment (dev, rec, prprd, prd, or any configured name)
      --repo string       open this repository's logs directly
      --since string      initial window: 15m, 30m, 40m, 45m, 1h, 1d, 2d, tail
      --config string     config file
      --demo              synthetic cluster
      --theme string      light (default), accessible, classic, none
      --log-level string  write a diagnostic log (debug, info, warn, error)
      --version

Commands
  huginn config example            print the documented configuration
  huginn config validate <file|->  check a configuration file
```

Environment variables: `HUGINN_ENV`, `HUGINN_CONFIG`, `HUGINN_THEME`, `NO_COLOR` (forces the `none` theme), `HUGINN_DEBUG=1` (diagnostic log in the user cache directory, never on screen).

## Keys (services screen)

| Key | Action |
|---|---|
| `j` `k` `↑` `↓` `pgup` `pgdn` `g` `G`, mouse wheel | move |
| `enter` | open the repository |
| `/` or `ctrl+f` | filter by name (enter keeps, esc clears) |
| `s` | sort: status, name, restarts, age |
| `r` | resync the watches |
| `ctrl+e` | switch environment |
| `esc` | back |
| `q` `ctrl+c` | quit |

## Keys (logs screen)

| Key | Action |
|---|---|
| `j` `k` `pgup` `pgdn` `g` `G`, mouse wheel | move (`G` returns to the live tail) |
| `>` `<` | next / previous ERROR |
| `enter` | zoom on the entry (`J`/`K` next/previous, `p` raw JSON, `enter` metadata) |
| `f` | follow on/off |
| `space` | pause / resume (lines keep buffering) |
| `t` / `T` / `1`…`7` / `0` | next window / window picker / 15m 30m 40m 45m 1h 1d 2d / tail (AZERTY: `&é"'(-è` / `à`) |
| `tab` / `S` | cycle pod scope / pod selector |
| `o` `c` `I` `W` | order, timestamps, pod id, wrap |
| `←` `→` `H` `L` | pan when not wrapped |
| `F` | fullscreen |
| `esc` | back |

Every key can be remapped with `ui.keymap` in the configuration.

## Configuration

Huginn reads `config.yaml` from `--config`, `$HUGINN_CONFIG`, the OS user config directory (`~/.config/huginn` on Linux, `~/Library/Application Support/huginn` on macOS, `%AppData%\huginn` on Windows) or `~/.config/huginn`. Everything is optional.

Start from the documented example: `huginn config example > ~/.config/huginn/config.yaml` (also in [`examples/config.yaml`](examples/config.yaml)). A JSON Schema for editor completion is in [`docs/config.schema.json`](docs/config.schema.json).

Everything specific to your company is configuration: kube contexts and namespaces per environment, the JSON field names of your log format, your manifest layout, the labels that name a repository, where secrets live.

## Development

```sh
make test    # go test ./...
make lint    # golangci-lint run (v2, built with Go 1.26)
make cross   # CGO-free builds for linux/darwin/windows x amd64/arm64
make schema  # regenerate docs/config.schema.json and examples/config.yaml
```

Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) before changing code: Huginn uses a hexagonal architecture whose rules are enforced by a test.
