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
