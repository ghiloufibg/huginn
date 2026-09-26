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

## D-018 Hexagonal architecture with enforced layer rules
The package layout follows ports & adapters as specified in `docs/ARCHITECTURE.md` (`internal/core/{domain,ports,app}`, `internal/adapters/{driving,driven}/…`, `internal/bootstrap`). Adapters are chosen by name from config through registries, so enterprise-specific behavior is added as a new adapter or config entry, never inside the core. Layer rules are enforced by `depguard` and an import-graph test. `CLAUDE.md` points future agents to these rules.
Trade-off: more packages and interfaces than a prototype strictly needs, accepted because the explicit goal is to port Huginn onto an enterprise codebase later. Supersedes the flat layout proposed initially.
Status: accepted.

## D-019 Keys added or changed while drafting the mockups
`[`/`]` need AltGr on AZERTY, so: next/previous error = `>` / `<`; next/previous entry in zoom = `J` / `K`. Unassigned toggles from the spec get: `c` cycle timestamp format (local/UTC/relative/none), `I` cycle pod identifier (short/full/none), `B` copy as bug report. Mockups: https://claude.ai/artifact/4RuyPRpu8V6BvcaYx8bcqq
Status: accepted, pending design review.

## D-020 Visual language: no icons, maximum log density
Supersedes the symbol part of D-011 and the keys of D-019 where they differ.
- **No icons or pictograms.** Status and level are spelled out (`CrashLoopBackOff`, `ERROR`) and colored; color is never the only signal because the word is always there. Only data glyphs remain: sparklines (`▁▂▃▅█`) and the scrollbar rail.
- **Chrome is two lines**: one header (app, env tag, context/namespace, breadcrumb, auth/sync) and one status bar (mode chip such as LIVE / PAUSED / TRACE / PRODUCTION, then window, scope, levels, filters, order, counts; key hints on the right). Key hints for everything else live in `?`. The logs screen adds one pod strip (pods, scope, errors-per-minute sparkline). Everything else is log lines.
- **Compact Spring view by default**: `pod time LEVEL [thread] logger : message`. PID, `---` and app name are hidden in the stream (same service on every line) and shown in zoom; a `spring-full` renderer is available in config.
- **Scan aids**: ERROR rows get a light red background, WARN rows a light amber one; a right-hand rail shows the viewport and marks error positions in the whole buffer; marked lines get a `*` in the gutter; matches are yellow and underlined.
- **Debugging views**: zoom shows fields, stack trace with own frames bold and framework frames dimmed/folded, and 3 lines of same-pod context; `v` (view trace) filters every pod of the service on the entry's traceId and shows a time delta column; error groups show a per-group trend sparkline and correlate onset with rollouts; the error state keeps showing the last cached data greyed out.
- **Keys**: `c` timestamps, `I` pod id, `v` view trace, `ctrl+a` stack another filter, `a` all levels, `ctrl+t` search trace across repos (V2).
Status: accepted, pending design review.

## D-021 Toolchain: Go 1.26, Bubble Tea v2, client-go v0.37
Current client-go (v0.37.x) and Bubble Tea v2 (`charm.land/bubbletea/v2`) both require Go ≥ 1.26, so the module targets Go 1.26. Bubble Tea v2 over v1: current major line and richer key events (can tell `ctrl+i` from `tab` on terminals supporting keyboard enhancements; D-007 fallback stays for others). client-go is only imported from M4, keeping M0–M3 builds light.
Status: accepted.

## D-022 Config: strict YAML decoding instead of koanf
Supersedes D-002. The configuration is one YAML file plus a handful of flags and `HUGINN_*` variables handled by the CLI, so a layered config library adds little. `go.yaml.in/yaml/v3` (the maintained yaml.v3) decodes into typed structs; a small walker compares the YAML tree with the struct tags to report every unknown key with its line and a "did you mean" suggestion. Defaults fill only zero values (lists and maps in the file replace defaults wholesale). Allowed values are declared once in `enum` struct tags and used by both validation and the generated JSON Schema (`docs/config.schema.json`), which a test keeps in sync, as it does `examples/config.yaml`.
Status: accepted.

## D-023 M0 implementation choices
- **TUI tests render the model directly** (Update with a WindowSizeMsg, then View, ANSI stripped, compared to golden files under `testdata/`, refreshed with `-update`) instead of `teatest`: deterministic, no timing, no extra dependency. A pty smoke test of the real binary was run manually.
- **Registries are built explicitly** in `internal/bootstrap` (no `init()` self-registration, no globals), so reading bootstrap shows every adapter that exists.
- **Environment precedence**: `--env` > positional argument > `HUGINN_ENV` > `default_env` > `rec`; flag and argument disagreeing is an error. **Theme precedence**: `--theme` > `NO_COLOR` (forces `none`) > `HUGINN_THEME` > `ui.theme`.
- **Warnings are magenta in the light theme**: yellow is unreadable on light backgrounds in most 16-color palettes; the word WARN always accompanies the color.
- **Demo retention** defaults to 6 hours, so asking for 1d/2d in demo mode reproduces the real "logs available from HH:MM only" situation.
- **CI uses the latest Go 1.26 patch** while `go.mod` requires 1.26.0, so security fixes in the standard library are picked up without forcing users to upgrade.
Status: accepted.

## D-024 Services screen rules (M1)
- **Status precedence**, worst first: CrashLoopBackOff, OOMKilled, ImagePullBackOff, Degraded, Pending, Progressing, Unknown, Healthy. A crash loop whose last termination was an OOM kill is shown as **OOMKilled** (it names the cause). Sidecar containers count for status (a crashing proxy breaks the pod) but not for restarts. During a rollout, not-ready or pending pods of the rolling workload are **Progressing**, never Degraded; a crash stays a crash. "Fewer ready than desired" becomes **Degraded** only when no pod state already explains it (so a pending pod shows **Pending**). A workload scaled to 0 shows "scaled to 0".
- **Version** is the image tag of the primary workload's app container (the workload named like the repo), `old→new` while two versions run; digests show as `sha256:1234567`.
- **Unassigned workloads** (no resolver claims them) are listed after repositories as `name (no repo)`, dimmed, so nothing running is invisible. Default label keys are `app.kubernetes.io/part-of`, then `app.kubernetes.io/name`, then `app`, so in practice most workloads resolve.
- **Sort** cycles status (worst first) → name → restarts → age (newest first); ties by name. Unassigned rows always stay last.
- **Narrow terminals**: columns drop AGE, then LAST RESTART, WORKLOADS, VERSION; STATUS never. The header shortens the context before the breadcrumb; the status bar drops key hints before state.
- **Catalog**: one watch pair per namespace, reconnect backoff 1s/2s/5s/10s/30s, last known rows kept while a namespace is failing, snapshots coalesced to at most one per 100 ms and latest-wins. Snapshot cost is about 5 ms for 500 repos / 3 000 pods (benchmark in `internal/core/app`).
- **Deferred**: `ctrl+p` service finder (M5), help overlay (M3), last-known-data cache at startup (M4).
Status: accepted.

## D-025 Logs screen rules (M2)
- **Follow is on by default** (like kl and `kubectl logs -f` habits); `f` turns it off and reloads the window without following (`STOPPED`).
- **History, then live**: each container's window is read without following, merged across pods by time, then the stream is re-opened from the last line's time. Lines at that boundary are skipped by text, so nothing is shown twice. Live lines wait **250 ms** in a reorder window; once shown, a line never moves.
- **Decoding happens in the session** (one goroutine per container, so it scales with cores); the UI only renders. JSON decoding runs at ~43k lines/s per core for a Kubernetes-enriched logstash line, above the 10k lines/s target; flattening the hidden metadata is the main remaining cost and can be made lazy if needed.
- **Bounded history**: each container's history is requested with a line limit equal to the buffer size (`LogRequest.Limit`, Kubernetes `tailLines` with the window), so a 2d window over a chatty pod stays bounded. When the limit cuts the window, the status bar says so ("older lines not loaded"), which is distinct from the **retention notice** ("logs available from HH:MM only"), emitted only when a container running since before the window returned nothing for more than 5 minutes and 10% of the window. The notice reports what was received; it cannot tell rotation from silence, and says "available from", not "deleted".
- **Pause vs scroll**: `space` freezes the view (new lines keep buffering, `PAUSED +N`); scrolling up keeps the view live but stops auto-scroll (`LIVE +N below`); `G` returns to the tail.
- **Compact layout** hides PID, `---` and the application name (identical on every line of a service); zoom shows the full Spring Boot layout. Stack traces are folded to their first line plus `+N lines` in the stream and shown in full in zoom.
- **Own frames** in zoom are frames outside common framework packages (java, jakarta, org.springframework, org.apache, io.netty, reactor, …); the list is code today and will become configurable with the V1 stack folding work.
- **Pod identity**: each line starts with the pod's generated suffix (`m8q7v`) in a per-pod color; the text identifies the pod, the color only helps. `I` switches to the full name or nothing.
- **Screens and async data**: session and catalog messages reach every screen of the stack, so the stream keeps filling under a zoom; screens close their sessions when left.
Status: accepted.
