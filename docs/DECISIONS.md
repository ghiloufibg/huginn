# Huginn — Design Decisions

Short, dated records of non-critical choices made without waiting for input.
Format: **Decision** — trade-off considered — status. Revisit by adding a new entry that supersedes an old one.

## D-001 Go module path and layout
`github.com/ghiloufibg/huginn`, single binary under `cmd/huginn`, all code under `internal/` (no public Go API promised).
Status: accepted.

## D-002 Config library: koanf over Viper
Viper lowercases keys, pulls many transitive deps and makes strict decoding awkward. koanf is small, composable (file → env → flags) and supports strict unmarshalling, which we need for friendly "unknown key `enviroments`" errors. Validation runs as a Go pass; a JSON Schema is generated for editor autocompletion.
Status: accepted.

## D-003 SOPS: shell out to the `sops` binary only
The sops Go library drags in AWS/Azure/GCP-KMS/Vault SDKs (large binary, cgo-free but heavy) and couples us to its internal API. Shelling out to `sops -d --output-type json` with stdout captured into memory keeps the binary small and reuses the user's existing key setup. The library fallback is deferred behind a build tag if ever needed.
Status: accepted.

## D-004 Status data via namespaced informers; events on demand
Pods, Deployments, ReplicaSets and StatefulSets are watched with namespaced shared informers (one factory per configured namespace) — cluster-wide watches would require broader RBAC. Events are listed on demand when the diagnostics panel opens (field selector on `involvedObject.name`), not watched continuously.
Status: accepted.

## D-005 Multi-pod merge ordering
Historical load: k-way merge by the RFC3339Nano timestamp that the API prefixes when `timestamps=true`. Live (follow): a small reorder window (default 250 ms) before lines are committed, trading a tiny display latency for correct interleaving across pods. Lines are never reordered after being committed to the buffer.
Status: accepted.

## D-006 Ring buffer + filtered index
One bounded ring buffer (default 50 000 entries) per logs view stores parsed entries. Filters/levels/search produce an index slice over the buffer, recomputed incrementally for appended lines and fully only when a filter changes. The viewport renders only visible rows. UI flushes are batched on a 33 ms tick (~30 fps).
Status: accepted.

## D-007 `Ctrl+I` cannot be distinguished from `Tab`
Terminals send the same byte (0x09) for both. `Tab` keeps "switch pod scope"; invert is `!` as a filter prefix plus the `i` key in filter-edit mode. The same constraint applies to `Ctrl+M` (= Enter) and `Ctrl+[` (= Esc); none are used as bindings.
Status: accepted.

## D-008 Numeric time-window presets and non-QWERTY layouts
`1`..`7` → 15m, 30m, 40m, 45m, 1h, 1d, 2d; `0` → tail; `8`/`9` reserved for user-defined presets from config. On AZERTY the top row emits `& é " ' ( - è _ ç à` without Shift, so these runes are accepted as aliases for 1..0; numpad digits also work. `t`/`T` remain the layout-independent path.
Status: accepted.

## D-009 Horizontal pan keys
`l` is the level picker, so vim-style `h/l` panning is unavailable. Pan with `←/→` (and `H`/`L` for half-screen jumps).
Status: accepted.

## D-010 stdout/stderr level fallback is limited
The core `pods/log` API merges stdout and stderr and does not report which stream a line came from. Splitting streams (`PodLogOptions.stream`, KEP-3288) is alpha behind a feature gate and not assumed to be enabled on GKE. Therefore the stderr→"probably error" fallback only applies to the Cloud Logging source (V2), where GKE sets severity from the stream. On the Kubernetes source, lines without a detectable level are marked `unknown`. To verify on the target clusters' version.
Status: accepted, verify.

## D-011 Default theme: `light`, built on the 16 ANSI colors
Owner preference is a light theme (matching the web prototype). The default `light` theme uses only base-16 ANSI colors chosen to stay readable on a light background (no yellow/bright-white/bright-cyan foregrounds; errors in red + bold, warnings in magenta, info in blue, debug in dim black) and is colorblind-safe because every status/level also carries a symbol and label (`✔ Healthy`, `▲ Progressing`, `✖ CrashLoop`…).
Trade-off: we do not force the terminal background by default (painting every cell breaks transparency and looks wrong in a dark terminal); `ui.paint_background: true` opts into painting a light background explicitly. Other themes: `accessible` (dark-terminal variant), `classic`, `none`. ASCII fallbacks when the locale is not UTF-8. `NO_COLOR` forces `none`.
Status: accepted (supersedes the original "accessible" default).

## D-012 Clipboard
OSC 52 escape sequence first (works over SSH and in most modern terminals, including Windows Terminal), `atotto/clipboard` (pure Go, shells out to OS tools) as fallback.
Status: accepted.

## D-013 Personal tool: no license, lightweight distribution
Huginn is a personal tool: no LICENSE file, no Homebrew tap or Scoop bucket. goreleaser still builds static binaries for linux/darwin/windows × amd64/arm64 attached to GitHub releases, plus `go install` instructions.
Status: accepted.

## D-014 Logs are always rendered as Spring Boot 3 console lines
All backend services are Spring Boot 3 and emit JSON logs that carry Kubernetes metadata, which is noisy for a developer. Huginn parses each JSON line and **re-renders it in the default Spring Boot 3 console layout**:
`2026-09-26T19:12:40.104+02:00  INFO 18472 --- [app-name] [http-nio-8080-exec-1] i.g.payment.PaymentController : message`
(timestamp, 5-char right-aligned level, PID, `---`, application name, thread, abbreviated logger, `:` message), colored like Spring's ANSI output but adapted to the light theme. Stack traces from the JSON are printed under the line and are foldable. Kubernetes enrichment fields are hidden in the stream and visible only in zoom mode (`Enter`), under a collapsed "metadata" section. Non-JSON lines (e.g. startup banner, JVM crash output) are shown raw with level `unknown`. The field mapping (timestamp/level/logger/thread/message/stack/trace id) is configurable, with defaults to be set from a real sample line.
The `p` key toggles between the Spring view (default) and pretty-printed JSON, instead of toggling pretty-print on raw JSON.
Status: accepted; field defaults pending a sample.

## D-015 No authentication handling in the TUI (this version)
The user authenticates with gcloud before launching Huginn. Huginn only uses the current kubeconfig contexts as-is. If an API call fails with 401/403 or a credential-plugin error, the error panel shows the raw cause and a generic hint (`gcloud auth login` / `gcloud container clusters get-credentials …`) with a retry key; no auth detection or flow beyond that. Supersedes the auth part of M4.
Status: accepted.

## D-016 SOPS inputs are per-overlay `config.env` files
Secrets live in `config.env` files per Kustomize overlay, encrypted with sops in dotenv format; decryption uses `sops -d --input-type dotenv --output-type dotenv` with output kept in memory only. Values are never displayed or exported. Which keys (if any) Huginn needs from them is still open.
Status: accepted; scope pending.

D-010 is now mostly moot: level always comes from the JSON level field.

## D-017 Prototype first: every site-specific shape is a pluggable, config-driven profile
Huginn is built as a prototype to be tuned against the real environment later, so nothing specific to one JSON shape, manifest layout or cluster topology is hard-coded:
- **Log format profiles** (`log_formats:`): a named profile maps JSON paths to canonical fields (timestamp, level, logger, thread, message, stack trace, trace id, app name, pid), lists level aliases and the fields to hide. A `LogDecoder` interface sits behind it, so a new format is either a config entry or one small Go type. The Spring Boot 3 console renderer is a separate `Renderer` (a line template), so parsing and display change independently. Default profile: logstash-logback-encoder names.
- **Manifest layout profiles** (`manifests:`): glob patterns for the overlay directory per env (default `**/overlays/{env}`), the file(s) to read, and where namespace / workload names / labels come from. A `ManifestScanner` interface backs it; Kustomize is the first implementation, Helm or raw YAML can be added without touching callers.
- **Topology** (`environments:`): each env declares its own `context` and `namespaces`; several envs can point to the same cluster/context (current setup: one cluster, one namespace per env) or to different ones, with no code change.
- **Secrets**: sops is optional. `config.env` values are decrypted lazily and only when a config entry explicitly references a key (e.g. `namespace_from: sops:config.env#K8S_NAMESPACE`); otherwise sops is never invoked.
Each profile ships with a documented example and table-driven tests, so adapting to the real codebase means editing YAML first and code only if a genuinely new shape appears.
Status: accepted.
