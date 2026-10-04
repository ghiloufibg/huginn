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

## D-026 Filters and help (M3)
- **What a text filter searches**: message, logger, thread, trace id, stack trace and visible fields (`key=value`); the raw line for unstructured entries; never the hidden Kubernetes metadata. Matching is case-insensitive.
- **Invert is the `!` prefix only** (`\!` for a literal `!`): `ctrl+i` is `tab` for terminals and `i` is text inside the prompt. **Context lines use `X`** (cycles 0, 1, 3, 5): `-`/`+` collide with the AZERTY number-row window shortcuts. **Filter ⇄ highlight** is `x` on the stream and `ctrl+x` inside the prompt (where `x` is text).
- **`esc` on the logs screen** exits fullscreen, then clears the last filter, then goes back — the most local thing first.
- **Performance**: each entry caches its lower-cased search text; substring filtering of 50 000 entries takes ~9 ms. Regexes get a literal prefilter from their syntax tree; a plain word alternation (`gateway|redis`) never runs the regex engine (~17 ms). A regex whose literal appears on every line still costs ~325 ms on 50 000 entries (Go's RE2 engine), so filter recomputation is debounced: 30 ms for plain filters, 300 ms after the last keystroke when a regex is involved. Filtering stays on the UI goroutine (the ring buffer is not safe to share); moving it to a background snapshot is the next step if real logs show hitches.
- **Highlight style** is background + underline so matches remain visible without color (`NO_COLOR`).
- **Mode-change confirmations** show for 2 s at the start of the status bar (before the state), so they are never cut on narrow terminals.
- **One action table** (`internal/adapters/driving/tui/actions.go`) drives help, status-bar hints and the README tables; help is generated from the live keymap, so remapped keys are shown as remapped.
Status: accepted.

## D-027 Optional columns and key bar (M3.1)
- **Columns**: time, pod, level, thread and class (logger) are optional; the message never is. `C` opens a picker where one letter toggles each column (`t p l h c`, `z` message only, `r` reset) — one global key instead of one per column keeps the keyspace free for M5 (`d`, `m`, `v`, `P`, `E`, `B`) and shows the state of every column. `z` on the stream is the **focus layout** (hides pod, thread and class, restores them on the second press). `I` and `c` keep cycling pod id and timestamp format, and reach "hidden" through the same column set.
- **Automatic narrowing** until the user chooses (or configures `ui.logs.columns`): thread hidden below 140 cells, class below 110. Zoom always shows every field, and filters keep searching hidden columns.
- **Key bar at the bottom**, one line under the status bar; the status bar keeps state only and the header keeps context only. Rationale: keys belong next to where input happens (the filter prompt is at the bottom), k9s-style header key blocks cost 5–6 lines of logs, and separating hints from state stops hints from being the first thing truncated. `f2` / `ctrl+k` cycles compact (1 line) → full (2 lines) → hidden (0 lines, status shows `f2 keys ? help`); default `ui.key_bar: compact`. It follows the context (stream, prompt, paused, zoom, popups, services, help), is built from the live keymap, drops low-priority keys first and always keeps `? help` last.
- **Not done**: `ui.logs.focus_columns` from the plan (what `z` keeps is fixed to time + level + message for now); per-column widths and column order.
Status: accepted.

## D-028 A services screen that uses all the space (M3.2)
- **WHY column**: computed in the domain (`domain.Explain`) from the watched pods and workloads only — last termination, OOM limit, waiting message, scheduler message, rollout progress. No event watch (D-004). It is the last column, takes every cell REPO leaves (REPO shrinks to its content) and is the first dropped on narrow terminals; titles are shortened (`WL`, `RST`, `LAST`) to leave it room.
- **Preview placement**: side panel (40% of the width) from 200 columns; else bottom panel when at least 8 lines are free under the rows; else none. It never pushes a row off screen unless the user forces it with `p` (50% split). `p` hides a visible preview and shows a hidden one; it reuses `p` of the zoom (raw JSON) since keys are per screen.
- **Events on demand**: a new driving port `ports.PodEvents` reads the worst pod's events once the cursor rests 300 ms on a service (a timer carrying a sequence number discards stale rests), cached 30 s per pod; only `Warning` events are listed. Errors show in the panel, never in place of the table. `Diagnostics` stays for the M5 diagnostics panel.
- **Status groups**: when sorted by status, thin titles separate FAILING, DEGRADED/PENDING, ROLLING, HEALTHY and WITHOUT REPO — only when every row and title fits and the titles do not take the lines the preview would use. Priority for free space is: why, then preview, then group titles.
Status: accepted.

## D-029 Peeling columns with one key (M3.3)
- **`c` hides one column per press** in a fixed order — time, level, thread, class — and the next press restores the layout from before the first press (not the defaults). Columns already hidden (width, `C`, config) are skipped so every press changes the line. The pod column is left to `I`: it tells replicas apart. `C`, `z`, `I` and `R` end a cycle; the next `c` starts from the current layout. Supersedes the `c` binding of D-027.
- **The time format moved to `ctrl+t`** (local → UTC → relative → local) and to `f` in the columns picker. It never hides the time; on a hidden time it shows it again in the next format.
- **`R` resets the display, never the data**: columns (configured ones, else automatic narrowing), pod id, time format, pan and wrap go back to their defaults; filters, levels, window, follow, pod scope and the selected line stay. `R` mirrors `r` inside the picker; `r` stays resync.
- **WARN messages take the warning color while the level column is hidden** (ERROR messages are always red), so hiding the level loses no information.
Status: accepted.

## D-030 The config folder is the only source of application knowledge (M3.4)
- **Open/closed**: supporting another company, cluster, log format or console look means writing files, never changing the code. Environments, repository mapping, sidecars, log formats and line layouts come from a **config folder** the user provides (`--config`, `HUGINN_CONFIG`, `<user config dir>/huginn/`). The code holds no application data, not even as defaults. Our Spring Boot setup became `examples/config/`, which `--demo` embeds, so the demo needs nothing a user could not write. `internal/archtest` fails the build if application strings reappear in the generic code.
- **Fixed file names** (`huginn.yaml`, `environments.yaml`, `services.yaml`, optional `containers.yaml` and `ui.yaml`, `formats/*.yaml`, `layouts/*.yaml`) rather than typed files merged by `kind`. Each file has one role and one documentation section, formats and layouts are named by their file, and unexpected files are errors with a suggestion. Every file carries `version: 1`.
- **Validation at startup, all problems at once, with positions**: strict decoding keeps the YAML node positions; required/enum/keys struct tags plus cross-file checks. The adapters' own checks (layout templates, keymap) are reported in the same list. The TUI never opens on an invalid folder, and the exit code is 2. **No tooling** (no `profile init/validate/show`, the `config` subcommands are removed): the folder is data, `docs/CONFIG.md` documents it, and startup is the validator. The JSON schemas in `docs/schema/` give editor completion.
- **Formats per container**: `match` globs on repository and container; formats are tried in file name order and the first match wins. Unmatched containers are plain text. A `regex` decoder with named groups and `level_from` covers text logs, and entries carry the name of their format so the view draws them with that format's layout.
- **Layouts are templates, columns are data**: a small template language (`{field|filter}`, filters `left right first last abbrev upper lower default`). A column whose fields are all empty is left out. Column name, picker key, colour role and `hide_below` come from the layout, and the picker, `c`, `z`, `ctrl+t` and the WARN colouring work on roles instead of fixed columns. The Spring layout renders byte for byte as the former Go implementation (tests kept).
- **Not done**: the manifests rule is validated but read only with the real cluster (M4). `namespace_from` is validated but not decrypted yet. The single-file `config.yaml` was removed without a migration path, because this is a prototype.
Supersedes the file layout of D-022 (strict YAML decoding and did-you-mean suggestions are kept).
Status: accepted.

## D-031 Performance budget and a livelier UI
- **Frame budget**: a frame must stay far below the 33 ms of a 30 Hz stream. With 50 000 lines at 200×60, a logs frame now takes 0.6 ms (it was 4.4 ms) and the services screen with 500 repositories 2.5 ms (it was 6.4 ms). Benchmarks in `internal/adapters/driving/tui/bench_test.go` guard these numbers.
  - **Viewport**: the logs viewport never scans more than one screen of entries, however far the cursor jumps. Before, it re-summed heights one offset at a time and a jump of 40 000 lines hung.
  - **Rendering**: each entry is rendered once per frame. Log segments are painted with cached escape sequences (`ink`) instead of `lipgloss.Style.Render`, which is kept for blocks.
  - **Width**: widths of ASCII text are counted without grapheme segmentation.
  - **Services table**: it builds cells for the visible rows only and caches its sorted rows per snapshot, sort and filter.
  - **Rebuilds with context lines**: they reuse their buffers (3 MB → 0.23 MB per batch).
- **The logs status bar shows the stream's pulse**:
  - the LIVE chip carries the live rate over the last 5 s (history is not counted);
  - error and warning counts for the current view appear first, coloured, and are maintained incrementally, eviction included;
  - fields are ordered by usefulness so a narrow terminal truncates the diagnostics (order, line and buffer counts) first.
- **Motion only when it means something**:
  - a spinner runs while connecting, resyncing, loading history or reading events, driven by a 100 ms timer that exists only while something is awaited;
  - a 1 s clock runs only while a stream is followed (rate, relative times) or a highlight must end;
  - a service whose status changes is shown in reverse video for 5 s, but not on the first snapshot of an environment.
- **No dead ends**:
  - empty views say why (window, filters, pod scope) and name the keys that help;
  - a logs error offers `r` to reload the logs (on that screen `r` otherwise keeps resyncing the watches).
Status: accepted.

## D-032 Resource budget and robustness (production readiness)
Measured on the real binary in a 200×50 terminal, demo streaming 400 lines/s over 4 containers, 15 min window, 50 000-line buffer:

| | Before | After |
|---|---|---|
| History load CPU / time (headless) | 167% for ~8 s / 4.4 s | 35–45% briefly / 1.0 s |
| RSS while streaming | 1.37 GB | 280 MB |
| Steady streaming CPU | 14% | 13% (≈ 8% terminal diffing, most of the rest the demo's own line generator) |
| Idle CPU (services screen) | 1.0% | 0.4–0.6% |
| Default demo rate, logs open | 60 MB, 3% | 50 MB, 2.5% |

- **History is cut before decoding**: every container may return up to `buffer_lines` lines, so a repository with n containers decoded n times what the view keeps. Tailers now return raw lines. The session keeps the newest `buffer_lines` of all containers by source time and decodes only those, in parallel (chunks of 4 096 lines, one goroutine per CPU). A notice says how many older lines were skipped. Without source timestamps nothing is cut.
- **Hidden metadata on demand**: the JSON decoder no longer flattens hidden fields (the Kubernetes enrichment) for every line. Whole hidden objects are skipped, and `LogEntry.LoadHidden` re-reads them from the raw line when zoom shows them. Decoding is 33% faster (18.8 → 12.7 µs, 6.5 → 4.1 KB allocated per line). The live heap for 50 000 entries is about 87 MB.
- **Memory is returned**: after a history load and when a logs screen closes, freed memory is given back to the system (`debug.FreeOSMemory` in the background). Otherwise the process kept its peak size.
- **Frame rate 30 fps** (Bubble Tea's default is 60): it matches the sessions' 33 ms batches and halves idle redraw work.
- **Bounded growth**: the preview's events cache drops expired pods. A wrapped entry takes at most 12 rows ("… n more rows, enter to open"). Popped screens are released.
- **No crash leaves the terminal broken**: the use-case goroutines (catalog, log session, tailers, pod watches) recover panics, log them with their stack to the diagnostic log and report an error through their normal channel. A decoder panicking on an unexpected line yields the raw line. Bubble Tea already recovers panics in `Update` and `View`.
- **Guards**:
  - fuzz tests for decoders, templates and the config loader (no crash in 75 s of fuzzing);
  - a test that closed sessions leave no goroutines;
  - a test that a 40 000-line jump stays fast;
  - benchmarks for frames and ingestion.
- **Diagnosis in the field**: `HUGINN_CPUPROFILE=<file>` writes a CPU profile of the session (`go tool pprof`), next to the existing `HUGINN_DEBUG` log.
- **Not done**: the terminal renderer's diffing cost belongs to Bubble Tea and is only limited by the frame rate. Replacing `encoding/json` with a streaming parser could halve decode time again; this is not needed at the target rates.
Status: accepted.

## D-033 fastjson for the JSON log decoder
- **Why**: decoding is the main per-line cost on the client, since in production nothing generates lines. `encoding/json` into `map[string]any` uses reflection and allocates a map for every object. `github.com/valyala/fastjson` (MIT, no dependencies) parses into a reusable tree, from a `ParserPool` safe across goroutines. The profile's paths are read from that tree and hidden subtrees are skipped without being built. Strings are copied out before the parser is reused. The standard library's `encoding/json/jsontext` needs `GOEXPERIMENT=jsonv2` in Go 1.26, so it is not an option yet.
- **Gain** (logstash line with Kubernetes metadata): 12.7 → 3.5 µs, 4.1 KB → 0.7 KB, 89 → 23 allocations per line, about 280 000 lines/s per core (43 000 at M2). Retained memory for 50 000 entries: 87 → 77 MB.
- **Same results**: the former implementation is kept in `oracle_test.go`, and a differential fuzz test compares both on arbitrary input. Four minutes of fuzzing pass. The differences it found are deliberate and documented in docs/CONFIG.md:
  - **numbers**: they keep the text written in the line (a 19-digit id is no longer rounded through float64);
  - **invalid UTF-8**: each bad byte becomes U+FFFD, as with encoding/json;
  - **repeated keys**: when an object repeats a key, the first occurrence wins, consistently in lookups and extra fields (encoding/json kept the last);
  - **hidden objects**: a hidden key holding an object hides the whole object (its children used to leak as fields);
  - **literal dotted keys**: a key with a literal dot next to a consumed field (`level.x` beside `level`) stays visible (it was wrongly dropped).
  - Keys that flatten to the same dotted path (`"."` and an empty key under an empty key) remain ambiguous in both implementations and are not compared.
Status: accepted.

## D-034 Kubernetes adapter: client-go, informers per namespace, events on demand
- **client-go**, typed clients for the three API groups Huginn reads (core, apps, batch) instead of the full clientset: the binary grows from 8.8 MB to 42 MB (51 MB with the full clientset). `kubectl` is about 50 MB. Accepted: it is the reference client (exec auth plugins such as `gke-gcloud-auth-plugin`, proxies, retries), and a hand-written REST client would re-implement watches badly.
- **Read-only by construction**: only get, list, watch and `pods/log`. A test drives every method on a fake clientset and fails on any other verb.
- **One clientset per kube context**, from the standard loading rules; an unknown context is `ErrConfig`, a permanent error (D-035).
- **Informers per namespace** for pods and for each workload kind, never cluster-wide (D-004). Before a watch starts, each kind is listed once (limit 1): kinds the user cannot read are skipped (a narrow role often lacks CronJobs), and a namespace where none is readable is `ErrForbidden` for that namespace only.
- **Owners without extra reads**: a Deployment's pod is owned by a ReplicaSet named `<deployment>-<pod-template-hash>` and a CronJob's by a Job named `<cronjob>-<scheduled time>`; the names are derived from the pod, so no permission on ReplicaSets or Jobs is needed. Log sessions also drop pods owned by another workload, because selectors can overlap.
- **Logs**: `timestamps=true`, the kubelet's RFC 3339 prefix becomes the line time. Lines are read with a `bufio.Reader` and capped at 1 MiB (then marked truncated), never a reason to stop the stream.
- **Events on demand** (field selector on the pod), normalized for new-style events (no `count`/`lastTimestamp`: `eventTime`, `series`), and merged when the same event repeats in a new series (after a node restart).
- **client-go's own logs** (klog, runtime error handlers) go to Huginn's diagnostic log: writing to stderr would draw over the screen.
- **Not done**: the manifests rule of `services.yaml` (M4 plan §3.8, optional) is still validated only; the ReplicaSet revision for restart-only rollouts (B7) is left for later.
Status: accepted.

## D-035 Resume, waiting containers and permanent errors
- **Resume**: the API honours `sinceTime` to the second. After a reconnect the tailer drops lines strictly before the last delivered time and, at that exact time, lines already seen. The contract suite checks every LogSource in a second-precision mode.
- **Waiting containers**: a follow stream that ends while its container is not running (crash loop, terminating pod) and a stream refused with "waiting to start" (`ErrNotStarted`) are not connection problems. The tailer waits for the pod watch to show a running instance (or one minute) instead of reconnecting with back-off, and the pod strip says `waiting: CrashLoopBackOff`.
- **Previous instance** (`P`): reads the whole previous instance of the restarted application containers (up to the history limit, the window does not apply), without following.
- **Permanent errors** (`ErrConfig`, `ErrNotImplemented`) are not retried; the screen says what to fix. Without a terminal Huginn says so and exits with 2.
- **namespace_from**: `sops --decrypt` of a dotenv file, run the first time the environment is opened, output kept in memory; a failure is reported on the services screen against `environments.yaml`.
Status: accepted.

## D-036 The lab and the end-to-end job
- `deploy/lab` starts kind (default) or minikube in Docker with dummy workloads for every state Huginn shows, and a read-only identity limited to one namespace (context `huginn-restricted`).
- `kind.yaml` carries two patches for sandboxed hosts (`restrict_oom_score_adj`, `failCgroupV1: false`); harmless elsewhere. Images are imported with `ctr` because `kind load` fails with Docker ≥ 29 multi-platform images.
- CI job `lab`: starts kind, runs the adapter contract suites (`HUGINN_LAB=1`) and `deploy/lab/e2e.sh`, which drives the binary in tmux and checks the screens. The script found one bug on its first run (a terminating pod showed "reconnecting").
Status: accepted.

## D-037 Fixes of the second end-to-end pass (docs/plan/M4-e2e-pass2.md)
- **A broken cluster connection ends the watch.** client-go informers retry a failing list forever, silently. When a list fails because the cluster is unreachable or refuses the credentials, the adapter ends the watch (closes its channel); the catalog then reconnects with its own backoff and shows the error over the last known services. A 10 s dial timeout bounds the first failure.
- **Errors carry their kind without repeating it** (`domain.KindError`): the screen shows the kind as a title and the message below.
- **Incomplete watches are said**: kinds skipped for lack of permission come as `WorkloadEvent.Warning` and reach the status bar; an empty namespace that does not exist is `not found` when namespaces can be read.
- **Order by reception**: entries are ordered by `LogEntry.Received` (the kubelet's timestamp), displayed with the time the application wrote. Application clocks and fields can be wrong; the kubelet's clock is the same for all lines of a node.
- **Late lines are inserted, not appended**: after an outage a stream delivers what it missed; the session marks those lines `Late` and the view merges them into its buffer (O(n), rare). Live lines are never held back to wait for a slow stream.
- **Pause holds new lines outside the buffer**, so the paused screen cannot be evicted; beyond a buffer's worth, held lines are dropped and counted.
- **Init containers that block a pod are streamed** (`ContainerFilter.LogContainers`): their output is why the pod cannot start. A failed init container is a crash loop; a finished Job pod is healthy and its stream ends for good.
- **Pod revisions** (`pod-template-hash`, `controller-revision-hash`, set by the adapter) make a restart-only rollout visible: `3.20 (restart)`.
- **Log text is untrusted**: only colour sequences (SGR) reach the terminal; others (OSC 8 hyperlinks, titles, clipboard) are removed.
- **Megabyte lines cost what is visible**: a segment is cut to the bytes that can reach the screen before highlighting; zoom wraps at most 64 KB of a line.
- **Not done**: choosing a container in the pod scope (`tab`/`S` select pods); the container is shown instead. Bare pods (without a workload) stay out of the catalog.
Status: accepted.

## D-038 Containers on demand, standalone pods (M4.1)
- **Containers.** The logs screen opens on the application containers (`containers.yaml` `default_mode: app`, or `--containers`). `A` follows all containers, sidecars and init containers included, by reopening the session (same window, filters, pod scope). `S` selects pods × containers: the two lists combine, which covers "one container of every pod" and "one pod's container" without a tree. Choosing a container that is not followed (a sidecar in app mode) switches to all containers. `hide` now means "sidecar" (not followed in app mode, not counted), no longer "never shown".
- **Why not follow every container and filter in the view:** sidecars are often the chattiest containers (mesh access logs); they would share the bounded buffer with the application and evict its lines, and double the log streams to the API server for nothing in the common case.
- **Standalone pods.** Pods no known workload owns become synthetic workloads (`domain.StandaloneWorkloads`), grouped by owner (kind and name) or by pod name when bare, carrying their oldest pod's labels. Every screen then works on them unchanged: resolvers (a labelled debug pod joins its repository), status and WHY, logs, `P`, preview and events. Shown by default; `services.yaml` `standalone_pods: false` hides them.
- **One claim rule.** `domain.Workload.Owns` tells which pods belong to a workload (owner name and kind, or selector for pods without an owner, or the standalone key), shared by the catalog and the log sessions instead of two slightly different rules.
- **Owner kind without extra permission**: the adapter derives it from the pod's controller reference (ReplicaSet with a pod-template-hash → Deployment, Job with a scheduled-time suffix → CronJob).
Status: accepted.

## D-039 Head window (M4.2)
- **A window kind, not a flag.** `TimeWindow.Head` sits next to `Since` and `Tail`: one key (`9`, AZERTY `ç`), one picker row, one `--since head[:N]` value, `windows.default: head`, and `t` cycles through it. It never combines with a duration.
- **Per container.** The logs API has no head, and a repository has no single start: each container gives its first `windows.head_lines` lines, merged by time, so a Deployment shows each pod's startup at its time. When containers × head exceed the buffer, the **oldest** lines are kept (`newer lines of the heads not loaded`).
- **Read from the start, stop by count.** No `tailLines`/`since`/`follow`: the API streams from the start of the current (or previous) log file; the adapter stops after N lines and closes the body, which stops the kubelet. The tailer cuts at N too, so a source that ignores the head costs no more. `limitBytes` is not used: it counts bytes and cuts the last line.
- **No follow.** Appending live lines after line N would leave a gap until now. `f` in a head goes back to the default window (the tail when the default is a head), following. The view opens on the oldest line, unpinned; `space` works as in `STOPPED`. Pods started during the session load their own head.
- **Rotation is shown, not worked around.** The API serves the current log file only; reading rotated files needs node access Huginn never has. The first line more than a minute after the container's start (`Container.Started`, from `state.running.startedAt`) gives a notice. Cloud Logging (the V2 source of D-010) could serve older lines later through the same port.
- **With `P`**, the head reads the first lines of the previous instance: how the crashed instance started, for N lines instead of the whole instance.
Status: accepted.

## D-040 Real GKE QA (M5)

- **GKE Autopilot** for the QA cluster (`huginn-qa`, `us-central1`,
  project `huginn-kube-tui`): scale-to-zero, no node-pool sizing
  decisions, free cluster-management fee for one cluster/month — the
  realistic way to waste the $300 free-trial credit here is a
  forgotten-standing cluster, not a runaway bill (the free-trial
  billing account cannot be charged past its balance; see
  `docs/plan/M5-gke-qa.md` §1).
- **Real Spring Boot 3.5 structured logging**
  (`logging.structured.format.console=logstash`, no extra dependency)
  validated end to end against `deploy/lab/config/formats/20-spring-json.yaml`/
  `layouts/spring.yaml` unchanged: real `@timestamp`/`logger_name`/
  `thread_name`/MDC fields, real multi-frame `stack_trace`s on a genuine
  Hibernate/Hikari startup failure and JVM OOM, real logger-name
  abbreviation at real package depth, real `/actuator/health`-backed
  pods. No format-file change was needed — it decoded real output on the
  first try.
- **Real RBAC-driven `forbidden`**: `huginn-reader`'s IAM principal maps
  straight to a Kubernetes `User` on GKE (IAM authenticates, RBAC
  authorizes); a namespaced `Role`/`RoleBinding` scoped to `qa-rec` only
  reproduces the lab's local-kubeconfig-context `forbidden` test with a
  real cluster decision instead of a faked context swap.
- **GCP-KMS-backed `namespace_from`**: closes the one gap
  `deploy/lab/QA-SESSION.md` §0.1 left explicit (the lab only exercises
  `age` keys) — `sops` decrypts a dotenv via a GCP KMS key, the read-only
  principal needing `roles/cloudkms.cryptoKeyDecrypter` on it. The
  sops-encrypted file cannot live inside the config folder itself: the
  config loader validates the folder against a fixed allow-list and
  rejects any other file, so it sits one level up
  (`deploy/gke-qa/namespace.env.enc`, referenced as `../namespace.env.enc`
  per `docs/CONFIG.md`'s documented relative-path convention).
- **IAM/RBAC is a union, not an intersection — do not grant
  `roles/container.viewer` at the project level.** That role alone grants
  read access to every cluster and namespace in the project regardless of
  a principal's namespaced `Role`/`RoleBinding`; GKE authorization is the
  union of whatever IAM and RBAC each separately allow, not their
  intersection. `rbac.yaml`'s namespaced Role/RoleBinding is therefore
  the read-only principal's **only** grant — no project-level IAM role at
  all — otherwise the `qa-restricted` "forbidden" test would silently
  pass for the wrong reason (IAM-level access, not an RBAC gap).
- **`gcloud billing budgets create` does not accept a free-trial billing
  account.** The command returned `INVALID_ARGUMENT` against
  `huginn-kube-tui`'s billing account; free-trial accounts cannot be
  budgeted through this API path (confirmed against Google's own
  documentation, not assumed). Since a free-trial account cannot be
  overspent by construction (§1), this blocks a nice-to-have guardrail,
  not the session itself — flagged as a non-blocking platform
  limitation, not retried further.
- **Cost**: three small real Spring Boot pods (100m CPU / 192Mi request
  each) plus a KMS key ring for a few hours; well under the free-trial
  credit, consistent with the order-of-magnitude estimate in
  `docs/plan/M5-gke-qa.md` §1.
- **Findings, none code bugs**: see `deploy/gke-qa/QA-REPORT.md` in full —
  a tmux-server stale-environment gotcha and a `wsl.exe` argument-mangling
  gotcha (both host/tooling, worked around in `deploy/gke-qa/e2e.sh`), and
  an inconclusive `unauthorized` repro (disabling the read-only
  principal's IAM key did not retroactively invalidate an already-issued
  access token within the session's time budget).
Status: accepted.

## D-041 Field transforms: strip (M6.1)
- **A regular expression on a decoded field, in the JSON decoder.** Context written into a field's text (a Logback MDC pattern such as `key=value… - message - key=value…`) is a decoding concern: `transform.<field>.pattern` runs in `logformat` after the standard fields are read. The core does not change. `decoder: regex` was rejected, because it would re-parse the whole JSON line with one pattern.
- **One group named after the field** is the new value. No match leaves the value unchanged, as "lines a format cannot parse keep their text". Only text fields can be transformed (`message`, `logger`, `thread`, `trace_id`, `app`, `pid`): `time` and `level` are parsed values, and `stack` is multi-line and large.
- **Other named groups were rejected in M6.1**, so M6.2 could give them a meaning without changing the folders written for M6.1 (D-042).
- **Fixed order** (message, logger, thread, trace_id, app, pid), whatever the order of the file.
- **Search follows the shown value**: the stripped parts leave text search and stay in the raw view. M6.2 brings them back as fields.
- **Cost**: Go's `regexp` runs in linear time, with no backtracking blow-up. The 13-key MDC line costs about +5 µs per line (2.3 µs → 7.6 µs to decode), all of it in the regexp engine. This is acceptable for a TUI buffer (50 000 lines ≈ 0.25 s), and a `match` keeps other containers free of it.
Status: accepted.

## D-042 Field transforms: extract (M6.2)
- **`pairs` instead of a key list.** A group listed in `pairs` is split into `key=value` fields, with keys spelled as written. The MDC keys of a logging stack are open-ended (a service adds one, another drops one), so neither the code nor the config lists them. A group that is not entirely `key=value` is kept whole as one field named after the group, so nothing is dropped. Values containing spaces need a named group.
- **Other named groups become fields**, as in `decoder: regex`. A group named like a standard field fills it **only when the JSON left it empty**, since an explicit key is more reliable than text. `time` and `stack` groups are rejected, as are duplicate group names.
- **Empty values are left out.** A context of 13 mostly empty keys would otherwise add 13 empty rows to zoom and `key=` noise to search. The raw view keeps them.
- **A JSON key wins** over an extracted field of the same name, like "repeated keys: the first occurrence wins". Between extracted fields, named groups come first, then `pairs` in list order.
- **Extracted fields are ordinary fields**: searchable, drawable, and subject to `hidden`. The lazy hidden-field loader runs the transforms again. They are never drawn on the stream unless a layout names them, so hiding them is never needed for a compact line.
- **The zoom section "KUBERNETES METADATA" is now "HIDDEN FIELDS"** (key bar: `enter hidden fields`). It always held whatever `hidden` matched, and with transforms that includes fields that do not come from Kubernetes.
- **Cost**: `pairs` on the 13-key line decodes in about 10.5 µs instead of 2.3 µs. The JSON decode path without transforms is unchanged, with the same allocations.
Status: accepted.

## D-043 Pair syntax in the config; the trace id named by its standard field
- **`pair_pattern`**: the `pairs` of D-042 assumed one convention (`key=value` separated by white space), so a stack writing `key: value;` or quoted values would have needed a code change. A transform now takes an optional `pair_pattern`, a regular expression with exactly the groups `key` and `value`, read with `FindAll`. The pairs it reads must cover the group except white space, so the separator belongs to the pattern. Otherwise the group is kept whole, as with the default syntax.
- **The default stays hand-written**, not a regular expression: `key=value` split on white space is the common case and costs less without a regexp. A fuzz test checks that it agrees with the same syntax written as `pair_pattern` (`(?P<key>[^\s=]+)=(?P<value>\S*)`). That test found and fixed one difference (a group of white space only).
- **Zoom labels the trace id `trace_id`**, the standard field's name, instead of `traceId`, the key of one encoder, whatever path the format reads it from. `internal/archtest` now forbids `traceid` in generic code.
Status: accepted.

## D-044 Field transforms: limits and hardening (M6 QA)
- **Limits per transform, in the config** (`max_bytes`, default 16 KiB; `max_fields`, default 64). With submatches, Go's `regexp` reads about 20 MB/s, so a 1 MiB value took 45 to 60 ms per line and could stall ingestion. Past `max_bytes` the value is shown as it is, and a 1 MiB line costs 0.8 ms, its JSON parse. `max_fields` bounds what one line adds to the buffer. Both are resource limits, not application knowledge, so neutral defaults are allowed (`config/defaults.go`).
- **Linear de-duplication**: extracted keys were checked by scanning, which is quadratic (10 000 pairs took 162 ms). A set now takes over past 16 keys, and the same line takes 12 ms, all of it regexp time.
- **The adapter never panics**, whatever `FieldTransform` it is given (missing groups, a `pair_pattern` without `key`, fields that cannot be transformed). It does not rely on validation, and a test feeds it transforms the validation rejects. `hiddenOf` also recovers, since it runs on the UI goroutine, from the zoom view and layout columns.
- **Hidden fields are decoded again without the transforms** unless one of them extracted a hidden field. A layout column naming a hidden field costs 2.6 µs per drawn row, as before M6, instead of 12 µs.
- Measured end to end (15 min demo window, 50 000 lines): the transforms add no measurable time over line generation. Retained memory goes from 2 103 to 2 110 B/line with 6.1 extracted fields per line, because extracted values are substrings of the message and small maps share one group. See `docs/plan/M6-qa.md`.
Status: accepted.

## D-045 `level_from` for JSON; it only raises the level (M7)
- **Same key for `json` as for `regex`.** A JSON line's severity can live elsewhere than its level key, for example in an HTTP status that is a JSON key or a field extracted by a transform (M6). `level_from.field` is read as a JSON path, like `fields` and including hidden keys, since hiding is about display. Otherwise it is read as an extracted field. The rules are shared with `regex` in `logformat/levelfrom.go`. The core and the TUI do not change: the level is a plain `LogEntry.Level`.
- **Only raises.** The line takes the more severe of its level and the matching rule's level. Override was rejected because a `*` rule would turn an ERROR logged during a request that answered 200 into INFO, which hides real errors from `e`, `>` and the counts. A value that matches no rule, or a missing field, keeps the level. `regex` did override, and gave UNKNOWN when no rule matched. It now follows the same rule, so one key has one meaning. The example folders are unchanged: nginx has no level group and uses a `*` rule, which a test checks.
- **Cost**: about +0.2 µs and 2 allocations per JSON line with `level_from` (stringifying the value).
- The plain decoder rejects `level_from`, and `field` and `map` must be set together.
Status: accepted.

## D-046 Filter on a field from zoom (M8.1)
- **Equality, not a substring.** A text filter searches a substring of the searchable text, so `request_id=d04b1995` also matched `request_id=d04b19951` and `x_request_id=…`. A field filter (`domain.FieldFilter`, a `TextFilter` with `Field` set) matches when the field is exactly the value, case-sensitive, since ids are. `≠` keeps the lines without the field. It stacks with text filters in the same list, so `esc`, `x`, `n`/`N` and `X` work unchanged. It highlights nothing inside the line, since the value may not be drawn.
- **The fields are those zoom lists**: `trace_id`, then the visible fields. `domain.FieldValue` also reads `logger`, `thread`, `app` and `pid` for later uses. **Hidden fields cannot be filtered**, because that would decode every buffered line again (about 12 µs per line with transforms, D-044), and they are already out of text search. A visible field named like a standard one is shadowed by the standard field.
- **Keys** (remappable): `tab`/`shift+tab` select a field. There is no cursor until then, so zoom reads as before. `=` keeps and `!` excludes. Both then go back to the logs, on the zoomed entry when it still shows. `enter` keeps toggling the hidden fields.
- **Cost**: one map lookup per line. `Select` over 50 000 lines takes 3.0 ms with a field filter, against 9.3 ms with a text filter.
Status: accepted.

## D-047 Trace view (M8.2)
- **The trace is a field filter on `trace_id`** (D-046), over the buffer of the logs screen. The loaded window and the pods of the service are its scope. The config decides what `trace_id` is (a JSON key or a transform group), so the TUI knows no application field. Searching across services or before the window remains V2 (Cloud Logging, D-010).
- **Filters, levels and pod scope are set aside, not applied**, because a trace is only useful whole. They are saved with the cursor entry and the scroll position, and restored on `esc`.
  - Inside the trace, `/` adds filters that narrow it, and `esc` removes them first.
  - `x` (highlight) is refused, since it would mix every line into the trace.
  - Changing the window reloads, and the trace stays.
- **Ordered by the entries' own time**, not by arrival (the buffer order), since the delta column is the time between the application's steps. Trace rows are sorted after a rebuild and after live lines arrive (only when out of order). Eviction drops them wherever they are. Late lines renumber the saved cursor entry.
- **Delta column** owned by Huginn, like the pod id (`+0 ms`, `+102 ms`, `+1.2 s`, `+3 m 04 s`). Layouts are unchanged. The status bar shows `TRACE`, the id, the lines, the pods and the duration. It says `may start before the loaded lines` when the trace contains the buffer's first line.
- **Deviations from the M8 plan**: there is no separate `domain.Trace`, since the M8.1 field filter already selects the lines. `/` in a trace narrows it instead of only highlighting, for one behaviour of `/` everywhere.
- **Cost**: `v` then `esc` on a full buffer of 50 000 lines take 5.9 ms together, two rebuilds with a field filter (`BenchmarkTraceView`). A live line costs one map lookup, plus a sort of the trace rows only when it arrives out of order. There is no new goroutine and no copy of entries: the view holds sequence numbers.
- The demo shares a trace id between neighbouring requests of every `order-orchestrator` pod, so `--demo` shows real multi-pod traces.
Status: accepted.

## D-048 Performance pass after M6–M8
- **Hidden decisions are cached per JSON decoder.** `path.Match` over the `hidden` globs was about a quarter of every JSON line, for keys that repeat on every line. The cache holds "hidden" and "hidden with everything below" per key, behind a read lock, and is bounded to 4 096 keys. A copy-on-write map was measured and rejected: filling it is quadratic. Decoding went from 3.95 to 2.9 µs per line.
- **Filter selection hoists the level set into a table.** The `LevelSet` map type and its semantics are unchanged, since an empty set must keep showing nothing. Only the loop over the buffer stops hashing.
- **The theme is passed by pointer.** Copying it per drawn segment was a tenth of a frame.
- Also: splitting pairs scans bytes, the lazy hidden loader captures nothing extra, and rebuilds skip the scope test when no pod or container is chosen.
- The full report, with before/after numbers and the review findings (trace after a reload, regex mode), is `docs/plan/perf-pass-M8.md`.
Status: accepted.

## D-049 Select and copy log lines (M9.1)
- **Selection by entries, not screen rows.** A range (`V`) and marks (`m`) hold sequence numbers, so wrapping, panning and folded stack traces do not matter. **Only displayed lines are copied.** Outside a trace, the range is every displayed entry between its two ends, found by binary search since rows are in sequence order, even when a filter hides an end. Inside a trace (time order), both ends must be displayed. Evicted entries leave the selection. `esc` clears a selection before anything else.
- **Two forms.** `y` copies **as shown**: the pod id and the columns the user did not hide, uncolored, never truncated or wrapped, with whole stack traces, plus the delta in a trace. Columns hidden only because the terminal is narrow (`hide_below`) are copied, since a copy is not bound by the width. `Y` copies **raw**: the line as received, for `jq`. With no selection, both copy the cursor line; in zoom, the zoomed entry.
- **Control characters are removed** from copies, except tab and new line: log text is untrusted (D-037), and a pasted escape sequence could act on the terminal it is pasted into.
- **Clipboards (D-012):** the terminal's, through OSC 52 (`tea.SetClipboard`, written by the TUI, which owns the terminal). The system's goes through the new driven port `ports.Clipboard` and the adapter `adapters/driven/clipboard`, which runs the first command found. `ui.yaml clipboard: auto | osc52 | system | off`: `auto` sends OSC 52 and uses the system command only when one is installed, so SSH and containers get no warning on every copy. `copy.max_bytes` (1 MiB) refuses larger copies with a message, since terminals cap OSC 52; M9.2's save will take those.
- **Cost:** a frame with a range over the screen and 1 000 marks takes 0.61 ms against 0.52 ms (`BenchmarkLogsFrameSelecting`). A 10 000-line copy is built in about 15 ms, only on `y`/`Y` (`BenchmarkCopy10000`).
- **Deviation from the M9 plan:** selected rows get the gutter only, not a background, which would fight the level colors (D-020's `*` gutter).
Status: accepted.

## D-050 Save lines, redaction, mouse (M9.2, M9.3)
- **`ctrl+s` saves the selection**, or every displayed line (not only the screen), in the form of the last copy: as shown, or raw after `Y`. There is no picker: the copy keys already choose the form. The file is `<repo>-<env>-<yyyymmdd-hhmmss>.log`, or `.raw.log` rather than the planned `.ndjson`, since raw lines are JSON only when the logs are.
- **The lines are copied on the UI goroutine, then written away from it**, streamed. The buffer reuses its slots for new lines, so the writer never reads it: a copy of the entries and of the display settings (`lineWriter`) goes to the command. For 50 000 lines the UI waits about 11 ms for the copy; the write takes about 75 ms in the background (`BenchmarkSave50000`).
- **The `filesink` adapter** writes in `ui.yaml save.dir`, which must exist and is never created behind the user's back. Files use `O_EXCL` (`-1`, `-2` suffixes, never overwritten) and mode `0600`. A failed or cancelled write leaves no half file. The name is sanitized.
- **`domain.Redactor`** (ARCHITECTURE rule 10) replaces matches of `ui.yaml redact` with `[redacted]` in copies and saves, never on screen. No pattern is built in; the examples live in `examples/`. It does nothing when no pattern is set.
- **Mouse:** a click moves the cursor, `shift`+click selects from the cursor, a drag selects. Each frame records the entry drawn on every screen row, so wrapped lines and folded stacks map back to their entry. `ui.yaml mouse: false` stops capturing the mouse (`MouseModeNone`), which gives the terminal's own selection back. Clicks are ignored under a popup.
Status: accepted.

## D-051 Light and dark themes, chosen from the terminal's background
- **`light` and `dark` use fixed 256-color shades, not the 16 base ANSI colors.** Screenshots of every screen in real terminal palettes showed the base colors failing. ANSI "white", the old bar background, is `#e5e5e5` in xterm but `#555555` in VS Code's light terminal: dark bars there, with the crumbs and error counts almost invisible. On dark palettes (Darcula), ANSI bright black made dim text unreadable, the black chip vanished into the background, and the light theme's magenta warnings and white bars were wrong. The shades of the 256-color palette are the same in every terminal, and lipgloss maps them down on 16-color terminals. `accessible` keeps the 16 base colors, so a user can still get their own palette; `classic` and `none` are unchanged.
- **Contrast is measured, not eyeballed.** `TestThemeContrast` computes WCAG ratios: at least 4.5 for every text color on several real backgrounds of its kind (white, One Half Light, Solarized light; black, VS Code dark, Darcula, Solarized dark), 7 for bar, chip, cursor and match text, and 4.5 or 3 for bold words on bars and chips. Light chips on the dark theme carry black text.
- **`auto` is the default.** Bootstrap guesses from `COLORFGBG` (a background of 7 or 15 means light), else dark. The TUI then asks the terminal for its background color (`tea.RequestBackgroundColor`, OSC 11) and switches when it answers, dropping the painted inks. A theme named explicitly never switches. Checked in tmux 3.4: a white pane gets `light`, a black one `dark`.
- **`paint_background`** paints the palette's own background (white or near black) for both themes.
Status: accepted; supersedes the 16-color rule of D-020 for `light`.

## D-052 Real GKE, round 2: M6-M9 coverage and NFRs under real load (M10)
- Full report: [`deploy/gke-qa/M10-QA-REPORT.md`](../deploy/gke-qa/M10-QA-REPORT.md), design: [`docs/plan/M10-gke-qa.md`](plan/M10-gke-qa.md).
- **No huginn runtime bugs found.** The two real bugs found and fixed were both in this session's own new QA fixtures: `internal/config`'s `TestUICopy` hardcoded a Unix path separator (`ExpandHome` correctly uses OS-native `filepath.Join`), and `formats/30-noisy.yaml` never matched because a format without `match` (`20-spring-json.yaml`) sorted before it — `docs/CONFIG.md` already documents "tried in file name order, no-match files should sort last," confirmed the hard way.
- **Reusing M5's project surfaced real prerequisite drift, not assumptions**: the billing budget M5's plan said should already exist did not; the `huginn-reader` IAM principal had been deleted at M5's teardown; creating a service-account key is now blocked by an org policy that did not block it during M5, so the `kms` environment (sops+KMS) is deferred this round — `rec`/`restricted` do not depend on it. Kubernetes access without a static key works via `gcloud container clusters get-credentials --impersonate-service-account`.
- **Granting the QA identity a project-level `roles/container.viewer` (for `get-credentials`) silently defeated the `qa-restricted` RBAC test** — a live reproduction of the IAM/RBAC union warning already in the README. Fixed by using the minimal `roles/container.clusterViewer` instead.
- **M6-M9 features, never run against a real cluster before, all confirmed working on real data**: trace view against a real `trace_id` spanning two pods, field filter from zoom, `level_from` on a non-standard field, a custom `pair_pattern`, and select+mark+save with redaction confirmed end to end (a real fake email/token visible on screen, `[redacted]` in the saved file).
- **NFR**: one real data point, 76 MB RSS / 2.7% CPU under mixed real load (idle trio + a tuned `payment-service-loadgen` + `noisy-fixture`, `--containers all`) — between M5's idle baseline (62-68 MB/2.8-5.9%) and the lab's synthetic-400 l/s number (280 MB/13%), consistent with this session's real aggregate rate sitting between the two. No `pprof` re-profile this round (time-boxed out, not silently skipped).
- Not covered: mouse selection (hard to script via tmux), independent re-verification of `auto` theme beyond tmux 3.4 (same terminal D-051 itself used).
Status: accepted.

## D-053 Not logged in: credential plugins kept off the screen
- **The bug:** with an expired gcloud session, `gke-gcloud-auth-plugin` fails and prints a dozen lines of advice to stderr. client-go runs exec plugins with the process's own stderr (and stdin, in interactive mode), with no option to change it, so that text was painted over the TUI on every retry. The failure itself comes back as `getting credentials: …` inside a `*url.Error`, a `net.Error`, so it read as "unreachable" and the screen never said to log in.
- **Stderr:** `kubernetes.quietPlugin` points `os.Stderr` at a pipe while the clientsets of an exec kubeconfig are built: client-go captures it then, in the plugin's authenticator, which it caches for the life of the process. The pipe's lines go to the log (`source=auth-plugin`). The swap is short and serialized; anything else written to stderr in that window goes to the log too, which is where it belongs under the TUI. Rejected: running the plugins ourselves (re-implementing the exec credential protocol, caching and certificates), and redirecting stderr for the whole run in bootstrap (it would also hide output meant for after the TUI exits).
- **Stdin:** the plugin is made non-interactive (`interactiveMode: Never`), whatever the kubeconfig says: under the TUI it cannot prompt, and reading stdin would steal keystrokes.
- **Kind:** `getting credentials:` maps to `ErrUnauthorized`, shown as "not logged in". A plugin that is not installed (`executable X not found`, or the system's error for a path) maps to `ErrConfig` instead: retrying cannot help, and the message keeps the kubeconfig's `installHint` rather than client-go's generic help. A broken watch now also ends on `ErrConfig`, so a plugin removed mid-session is reported, not retried silently by the informers. The request URL is dropped from the message, and the plugin's path is shortened to its name.
- **The reason is shown, not only logged.** Diagnostic logging is off by default, so the plugin's stderr would be lost and the screen would only say "exit code 1". The pipe's reader keeps the lines of the latest run (lines more than 500 ms apart start a new run); when a plugin fails, the error gets its reason: the text after the last `ERROR: ` (plugins wrap the error of the CLI they run, `gcloud`), else the first line, at most 300 characters. The plugin has exited when client-go returns, so its output is already in the pipe; `reason` waits until the reader has been idle for 20 ms (150 ms at most), and ignores output older than 2 s.
- **The log is not flooded.** A failing plugin prints the same advice on every retry, for each namespace. Lines are logged without their klog header, and a line already logged is not logged again for 10 minutes.
- **Error screens:** one layout, `tui.errorPanel`, for the services and logs error screens: a headline in the error's terms ("Not logged in to rec"), the message, what to do, the keys. Each part is wrapped to at most 76 columns; on a short terminal the message is cut first, so the advice and keys stay visible, and nothing goes past the screen.
Status: accepted.

## D-054 Narrow terminals: the environment always shows; one size test for every screen
- **The header never drops the environment.** Below about 50 columns the connection state on the right took the room first, and `fill` cut the left side: at 40 columns `PRD` and the breadcrumb were gone, only the red bar was left. Now, as the width shrinks: the context goes (as before), the connection state loses its source and the word "synced", the brand goes, then the connection state is cut, then dropped. The environment tag and the breadcrumb stay.
- **Popups wider than the terminal are clipped** (`placeOver`) instead of pushing the lines past the screen: the environment picker was 75 columns wide whatever the terminal.
- **`TestLayoutFitsEverySize`** renders every screen and popup, and the loading and error states, from 20×5 to 220×60, and checks that each fills the terminal exactly, that no line is wider, and that the header names the environment. It found both bugs above. `TestLayoutScreensOpen` checks that its table still opens what it says.
- **Waiting on a cluster that does not answer** shows the elapsed time after 2 s (`connecting to dev · 7s`): the dial times out after 10 s, which looked like a hang. The unreachable error screen says to check the network or VPN.
Status: accepted.

## D-055 Release pipeline, pinned CI tools
- **goreleaser, as planned in M0 and D-013, was never added.** It is now: `.goreleaser.yaml` builds the same six CGO-free targets as `make cross`, stamps `buildinfo.Version` with the tag (`v` included, like `make build` from a tag), archives each binary with the README, `docs/CONFIG.md` and the examples (zip on Windows), and writes `checksums.txt`. Release notes come from the conventional commits since the previous tag, grouped into features and fixes; `docs`, `test` and `chore` commits are left out.
- **`release.yml` runs on `v*` tags only**, with `contents: write`, and runs vet and the tests again before goreleaser: a tag must not publish what CI would reject. Every push runs `goreleaser check`, so the config cannot rot between releases. The procedure, the version policy (semver, `v0.1.0` first; a config change that needs users to edit their folder bumps MINOR before 1.0) and the manual checks CI cannot do (a real expired gcloud session, Windows) are in `docs/RELEASING.md`.
- **CI tools are pinned:** golangci-lint `v2.14.0`, govulncheck `v1.8.0`, goreleaser `v2.18.2`, and exact action versions. `latest` let a new linter release turn `main` red with no change of ours. The actions move to their Node 24 majors (checkout and setup-go v7, golangci-lint-action v9, goreleaser-action v7): GitHub warned on every run that Node 20 is deprecated. Bumping a tool is a deliberate commit.
Status: accepted.

## D-056 Stale services are marked; the status bar puts failures first
- **The catalog keeps the services of a namespace that lost its watch**, as last seen, so a session that expires does not empty the screen. They were shown as if live: the header said "error: not logged in", but a row could be 20 minutes old without a sign of it. `CatalogSnapshot.StaleSince` now says since when (the first failure of the oldest failing namespace; retries do not move it, a reconnection clears it). It is computed in the core, which knows when a watch failed; the UI only shows it.
- **The services screen** starts the status bar with `stale since 18:54 (20m)`, ahead of everything so a narrow terminal keeps it, and dims the rows of the failing namespaces with `stale ·` at the start of the WHY column: dimming alone is invisible with the `none` theme.
- **The status counts start with the status groups**, from the most urgent: at 80 columns, `14 repos + 1 without repo · 3 failin…` hid the one number that matters; it now reads `3 failing · 2 degraded/pending · …` and the repository count is what gets cut.
Status: accepted.

## D-057 Kafka topics, read only (M11)
- **Scope: replace the debug script, nothing more.** Dotenv sources (sops for encrypted ones), SASL plain/scram, PEM or PKCS12 truststore, topics listed or discovered in the sources, tail/windows/follow. Other formats, auth methods, decoders and group lag wait for a real need (docs/plan/M11-kafka.md §10).
- **No consumer group.** Partitions are assigned by hand (franz-go `ConsumePartitions`), never with a group id: no coordinator traffic, no rebalance of the pods' group, no offset commit. No config key can set a group id.
- **Request allow-list, enforced before the bytes leave.** Huginn dials the brokers itself and a guard above TLS refuses any request other than ApiVersions, Metadata, ListOffsets, Fetch, SaslHandshake, SaslAuthenticate and OffsetForLeaderEpoch (read only; the client asks it after a leader change). Backed by `forbidigo`/`archtest` rules and a `kfake` contract test asserting the broker never receives anything else.
- **franz-go** over sarama (heavier, consumer-group oriented) and confluent-kafka-go (cgo, librdkafka): pure Go, custom dialer, in-memory test broker. **go-pkcs12** for truststores, no `openssl`.
- **Generic `kafka/` profiles, like `formats/`.** One profile per file, `match` on repositories and on the existence of files for the running environment, first match wins; ordered sources referenced as `${KEY}`; `{env}`, `{repo_dir}` and free `vars` replaced first; per-repository and per-topic overrides. No path, key name, mechanism or topic in code or as a default (D-030).
- **Bounded by count and by bytes.** The record buffer has both a record limit and a byte limit; values above `max_value_bytes` are truncated with their size shown; fetching stops when history is loaded unless following, and pausing pauses the fetch.
- **Absent means absent.** Without a `kafka/` folder, bootstrap builds no Kafka component and the TUI has no Kafka action.
- **K0 choices.** References are pure domain functions (`domain.ExpandVars`, `ResolveKeys`, `ParseDotenv`) shared by validation and, later, the use case. One `localfiles` adapter reads every local file a profile names (sources, truststore, existence checks) instead of two adapters. Encrypted sources go through a new `ports.SecretFiles` (`sops.Provider.Decrypt`, not cached, so a profile reread sees the file as it is) rather than widening `SecretsProvider`. Dotenv values in quotes are unquoted and `#` after a value is kept, since a password may hold one. PKCS12: Java truststores and keystores only; go-pkcs12 cannot decode openssl's certificate-only files without the Java trust attribute, and the error gives the conversion command instead of shelling out to openssl. Third-party libraries are confined to one package each by `archtest.Confined`.
- **K1 choices.** The Kafka screen is three stacked screens (topics → records → record) like services → logs → zoom, not two panes. The use case sorts the history by timestamp once it is complete and trims it to what the view keeps (count and bytes) before sending it, then passes live batches sorted; the adapter only keeps each partition in offset order. Values are truncated to `max_value_bytes` by the use case whatever the adapter does. The services screen asks which repositories have Kafka once per change of environment or repository names, in the background; the answer only depends on file existence and is remembered. Payload previews are computed once per record and cached with the record. Without a `kafka/` folder, or without a topic source for the run (a real cluster until K2), `Options.Kafka` is nil: no marker, no key, no help line.
- **K2 choices.** The guard holds back the size and API key of each frame until the key is checked, so a refused request is never written, not even partly; the connection is closed, the violation recorded on the source, and every later read of that source fails. TLS is done inside Huginn's dialer, under the guard, so requests are still readable there. franz-go's client metrics (KIP-714, a write) are disabled. The history ends when each partition reaches the end offset seen at the start, or after a poll waits `IdleEnd` (2s) without a record, since a partition ending with a transaction marker has no record at its last offset. A broker that closes the connection during SASL is reported as rejected credentials or a protocol mismatch, as real brokers often do that. `forbidigo` forbids franz-go's produce, commit, group and transaction calls and the write requests of kmsg, outside tests.
- **Hardening.** Every franz-go poll is bounded (`IdleEnd`), so a read reports brokers lost and back (from the client's connect hook) while following, and a lost broker never ends the end-of-history detection early. The one-line preview reads only the first 16 KiB of a value and compacts JSON without validating it: a large value costs what a small one does; zoom still lays out the whole value. Retention gaps get no notice, since compaction makes the same gaps. After a code review: the history ends early only for partitions that already delivered records and stay silent for two polls (a partition still waiting for its first record waits the connect and request timeouts, so a slow VPN never shortens it); "unreachable" means every real broker failed, not one stale seed; connections of a profile are opened together; a session opened for a screen already closed is closed; records held while paused are bounded by bytes as well as count; newest-first keeps the records being read in place; finished reads leave nothing in the session; zoom lays a value out once.
- **Resources.** Pause is backpressure: the screen stops taking batches and the blocked channels stop franz-go's polling, so nothing is fetched while paused and nothing piles up in memory (the one batch already on its way is held, bounded by count and bytes). Records copy their key, value and headers, cut to `max_value_bytes`, instead of pointing into franz-go's decompressed batch. A read keeps its own franz-go client rather than reusing one per source: reuse would save a handshake (about a second over a VPN) when changing window, but a purged topic's buffered fetches could leak into the next read, and correctness wins for a debugging tool.
- **K4: a CLI next to the screens.** `huginn kafka check|read` reuse `app.KafkaService` through the composition root: the services screen needs a Kubernetes cluster, the Kafka settings do not, so a profile can be checked from a shell first. `--raw` writes values as received (line breaks as `\n`, a tombstone as `null`) into a pipe, and escapes control characters only on a terminal, where record data must not drive the screen. An environment named `kafka` would now be read as the subcommand; `-e kafka` still works.
- **K3 choices.** `y` copies the value only, as received when it is text or JSON (the exact payload, for replaying it elsewhere by hand), as a hex dump otherwise. It goes through the same path as log copies (D-049): `ui.yaml clipboard`, `redact` and control-character removal; a value above `copy.max_bytes` is cut there, with a message, rather than refused, since a Kafka screen has no save. `n`/`N` are not offered: the Kafka filter hides non-matching records.
- **Screens follow the rest of the TUI.** Errors use the shared error panel with Kafka advice (credentials, ACLs, brokers and truststore, the profile); every Kafka screen is in the size test (D-054). The records list has a column header, one pod colour per partition, the date when the oldest record shown is from another day, the live rate in the `LIVE` chip and the count of newer records off screen; the zoom colours JSON member names. All are computed per frame from what is on screen, or once per record, so following costs the same.
Status: accepted (docs/plan/M11-kafka.md).

## D-058 Real GKE, round 3: Kafka against a real broker (M12)
- Full report: [`deploy/gke-qa/M12-QA-REPORT.md`](../deploy/gke-qa/M12-QA-REPORT.md), design: [`docs/plan/M12-gke-qa.md`](plan/M12-gke-qa.md).
- **No huginn runtime bugs found.** Every issue was this session's own environment setup or confirmed real Kafka/GKE platform behavior. The security-critical path — real TLS, real SASL/SCRAM, real sops+age decryption, a real PKCS12 truststore, real ACL enforcement — worked end to end.
- **`sops`+`age` (not GCP KMS) closes the gap M10's D-052 left open**: credentials for the Kafka profile were encrypted with a local `age` keypair, sidestepping the org policy that blocks service-account key creation (which only ever affected GCP-KMS-backed `namespace_from`, never `sops`'s own file-based decryption, per D-003).
- **Real failure modes confirmed verbatim against M11-kafka.md §6's documented text**: `not authorized` (ACL-denied topic), `credentials rejected` (wrong SASL password), and the full `brokers unreachable, retrying… → reconnected` cycle from a real `kubectl delete pod` on the broker mid-`--follow`, with no record shown twice on resume.
- **All five payload shapes** (JSON, null key, tombstone, binary, Confluent-schema-framed) confirmed rendered correctly in both the CLI and the TUI (list and zoom), including the hex dump and the `schema <id>, N B` label.
- **Operational findings, not huginn issues**: a GCE PD's `lost+found` directory is fatal to Kafka's `LogManager` unless `log.dirs` points at a subdirectory; a non-root container needs `fsGroup` for a PVC to be writable; `allow.everyone.if.no.acl.found` is evaluated per-resource, not per-principal (a DENY ACL for one principal silently also removes the default-allow for everyone else on that resource).
- **NFR**: one real data point, 38 MB RSS / 0.9-1.5% CPU following a real 20,000-record burst (200 B each, ~2000 rec/s) — lower than M11 §8's own `--demo` benchmark (52 MB / ~3%), not treated as a regression signal either way given the different record shape and no TUI redraw cost on the CLI path.
- Not covered: a committed `e2e-m12.sh` automation script (this session's checks were ad hoc); `--committed`/`--raw` CLI flags specifically (exercised via TUI equivalents instead).
Status: accepted.

## D-059 Mouse selection, tested without a real cluster (closes an M10 gap)
- **tmux `send-keys -H` can inject raw SGR mouse escape sequences as literal bytes** against `--demo`, no real cluster needed. bubbletea/ultraviolet decode them exactly as a real mouse driver would (`parseMouseButton` in `charmbracelet/ultraviolet`'s decoder), so click, shift+click, drag and wheel are all genuinely testable this way. Script: [`deploy/gke-qa/mouse-test.sh`](../deploy/gke-qa/mouse-test.sh). All four confirmed correct against real demo data (cursor moves to the clicked line, shift+click and drag both show the `▌` range gutter, wheel scrolls).
- **`ui.yaml mouse: false` cannot be validated by injecting raw bytes** — a real methodological trap, not a product issue. It works by never emitting the terminal's mouse-report-enable sequences (`CSI ?1002h`, `?1006h`) in `View()` (`model.go`: `if m.opts.Mouse { v.MouseMode = tea.MouseModeCellMotion }`), so a real mouse simply never produces SGR bytes once disabled — but a synthetic test that injects those bytes directly bypasses that gate entirely and will show a "click" succeeding regardless of the config. The valid test captures Huginn's own raw output (`tmux pipe-pane`) and checks the enable sequences are absent when `mouse: false`. Confirmed correct.
Status: accepted.

## D-060 The remaining infrastructure-dependent gaps, closed
- Full reports: [`deploy/gke-qa/M10-QA-REPORT.md`](../deploy/gke-qa/M10-QA-REPORT.md)'s follow-up and [`M12-QA-REPORT.md`](../deploy/gke-qa/M12-QA-REPORT.md)'s follow-up.
- **The `kms` environment works via Application Default Credentials, no static key.** Once `gcloud auth application-default login` was run once (an unavoidable one-time interactive step — ADC fundamentally requires it, by Google's own design, to establish the base identity), the SA-key-creation org policy that blocked M10 no longer matters: GCP KMS calls through `sops` just need ADC, not a downloaded key. Tested as the actual `huginn-reader` identity, not just the admin account, by hand-building an `impersonated_service_account` ADC file with the admin's own `authorized_user` ADC as `source_credentials` — fully non-interactive after the one login, reusable for future sessions. The `kms` environment synced correctly end to end through the real TUI.
- **`e2e-m12.sh`'s first live run found three bugs — all in the script, none in huginn.** Missing `CLOUDSDK_CONFIG` forwarding (the real `gke-gcloud-auth-plugin` couldn't find the logged-in account), an extra `Enter` that opened the wrong screen entirely (every downstream check then ran against it), and a copy-confirmation check that raced the 2-second flash-message TTL. This is exactly what running a never-executed test script against real infrastructure is for.
- **`--committed`/`--raw` CLI flags** confirmed matching README's documented semantics exactly (raw tombstone as literal `null`, real bytes for binary/schema-framed content, `--committed` showing all non-transactional records unaffected).
Status: accepted.
