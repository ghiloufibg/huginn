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

## D-011 Default theme uses the 16 ANSI colors
The `accessible` theme uses only the base 16 ANSI colors (so the user's terminal palette, dark or light, decides the exact shades) and every status/level has a symbol and label (`✔ Healthy`, `▲ Progressing`, `✖ CrashLoop`…). ASCII fallbacks are used when the locale is not UTF-8. `NO_COLOR` forces `none`.
Status: accepted.

## D-012 Clipboard
OSC 52 escape sequence first (works over SSH and in most modern terminals, including Windows Terminal), `atotto/clipboard` (pure Go, shells out to OS tools) as fallback.
Status: accepted.
