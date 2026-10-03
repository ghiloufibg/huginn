# M6 — QA of field transforms  (status: done)

This QA pass covers the functional, non-functional and architecture aspects of `transform` (M6.1 strip, M6.2 extract, `pair_pattern`). It was run on the demo cluster, on a 4-core container, with Go 1.26.0. Every number below comes from a test or benchmark that is in the repository and can be run again.

## 1. Functional

| Area | Covered by | Result |
|---|---|---|
| Strip (A): context around the message, empty context, message containing ` - `, no match, empty group | `TestTransformStripsMessage` | pass |
| Order of transforms; transform on `logger` | `TestTransformOtherFieldsAndOrder` | pass |
| Lines without the field, non-JSON lines left alone | `TestTransformLeavesOtherLinesAlone` | pass |
| Invalid UTF-8 in a transformed value | `TestTransformKeepsValidUTF8`, fuzz | pass |
| `pairs` (B2): split, empty values dropped, searchable | `TestTransformExtractsPairs` | pass |
| Text that is not pairs kept whole; empty key; empty group | `TestTransformPairsKeepTextThatIsNotPairs` | pass |
| `pair_pattern`: other syntaxes, quoted values, leftovers | `TestTransformPairPattern` | pass |
| Default syntax equals the same syntax written as `pair_pattern` | `FuzzPairPatternMatchesDefault`, 2.9 M inputs | pass, after one fix |
| Named groups (B1): extra fields; standard fields filled only when empty; `level` | `TestTransformNamedGroups` | pass |
| JSON keys win; first extracted value wins | `TestTransformJSONKeysWinAndFirstExtractedWins` | pass |
| `hidden` on extracted fields: out of `Fields` and search, in zoom | `TestTransformHiddenExtractedFields` | pass |
| `max_bytes`, `max_fields` | `TestTransformLimits`, config tests | pass |
| Validation errors with file:line:column; defaults; forward compatibility | `internal/config` `TestTransform` | pass |
| Config → decoder wiring (every setting, fixed order) | `TestTransformsCarryEverySetting` | pass |
| Example folder draws a transformed line | `TestExampleFoldersReadTheirLogs` | pass |
| Demo end to end: compact message, context fields, no empty ones | `TestDemoTransformExtractsContext` | pass |
| Real TUI (`--demo`, tmux): stream line, zoom FIELDS, raw view `p`, `/correlation-id=` filter | manual | pass |

## 2. Robustness

- **No panic, whatever the transform.** `TestTransformNeverPanics` feeds `Decode` and the hidden-field loader transforms that validation rejects: missing groups, a `pair_pattern` without `key`, fields that cannot be transformed, non-string values. `hiddenOf` runs on the UI goroutine and also recovers. Decoding in the log sessions was already guarded by `safeDecode`.
- **Fuzzing, 30 s each:** `FuzzTransform`, `FuzzPairPatternMatchesDefault` and `FuzzDecoders` found nothing. One earlier finding was fixed, and its input is kept in `testdata/fuzz` as a regression case.
- **Concurrency:** one decoder is shared by the session goroutines, and compiled regexps are safe to share. `TestTransformConcurrentDecode` and `go test -race` on logformat, bootstrap, core and config are clean.

## 3. Performance and resources

### Per line (`BenchmarkJSONTransform`, 13-key MDC line, about 330 B message)

| Variant | Time | Allocations |
|---|---|---|
| no transform | 2.3 µs | 744 B, 15 allocs |
| strip | 7.6 µs | 780 B, 16 allocs |
| pairs | 10.5 µs | 1 037 B, 19 allocs |

The added time is all spent in Go's `regexp`, which reads about 20 MB/s with submatches.

### Value size (`BenchmarkTransformSize`)

| Message | No limit | Default limits (`max_bytes` 16 KiB) |
|---|---|---|
| 1 KiB | 30 to 55 µs | same (under the limit) |
| 64 KiB | 3.1 ms | **27 µs** |
| 1 MiB | 45 to 60 ms | **0.8 ms** (JSON parse only) |

Without the limit, a single large line, such as a dumped payload, stalled ingestion for tens of milliseconds. This is fixed with `max_bytes` (D-044).

### Many pairs (`BenchmarkTransformManyPairs`)

| Pairs in a line | Before | After |
|---|---|---|
| 1 000 | 2.1 ms | 0.85 ms |
| 10 000 | 162 ms (quadratic) | 12 ms (linear) |

`max_fields` (64) also bounds what such a line keeps in memory.

### End to end (`HUGINN_PERF=1 go test -run TestPerfTransform ./internal/bootstrap`)

This loads a 15 min window of `order-orchestrator` into the 50 000-line buffer, twice per variant:

| Variant | Time per line | Retained per line | Fields per line |
|---|---|---|---|
| none | 22.5 to 27.7 µs | 2 103 B | 0.9 |
| strip | 25.4 to 26.3 µs | 2 107 B | 0.9 |
| pairs | 27.0 to 27.8 µs | 2 110 B | 6.1 |

The differences are within the noise of line generation. Memory does not grow because extracted values are substrings of the message (no copy), and a small Go map holds up to 8 keys in one group.

### Real process (`--demo --repo order-orchestrator --since 1d`, about 18 290 lines)

| Config | RSS after load | RSS after 15 s live | CPU |
|---|---|---|---|
| without transform | 104 MB | 118 MB | 1 s |
| with `pairs` | 104 MB | 116 MB | 1 s |

### UI-side paths

- **Text filter**, first search of 1 000 lines (`BenchmarkSearchTransformed`): 1.3 ms without transform, 1.1 ms with `pairs`. The shorter message compensates for the added fields.
- **Layout column naming a hidden field**, which re-decodes the line on each drawn row (`BenchmarkHiddenFieldsTransformed`): 2.6 µs per row with or without transform. The transforms run again only when they extracted a hidden field, at about 12 µs per row. That is about 0.7 ms for a 60-row frame, well under a 16 ms frame.
- **The UI loop never decodes lines.** Decoding happens in the log-session goroutines of `core/app`.

## 4. Architecture

- **`internal/core` is unchanged** (`git diff origin/main -- internal/core` is empty). Extracted values are plain `LogEntry` fields.
- **The mechanism lives in the JSON decoder adapter** (`internal/adapters/driven/logformat`). It imports only `domain`, `ports`, stdlib and fastjson, which was already a dependency.
- **The config is plain data**, validated in one place, with neutral resource defaults in `config/defaults.go`.
- **Only `internal/bootstrap` imports the adapter** and compiles the patterns.
- **No application knowledge in code.** Keys, patterns and separators are in `examples/`. `internal/archtest` passes and now also forbids `traceid`. The zoom's trace label and hidden-fields section no longer assume one encoder or Kubernetes.
- **`golangci-lint` v2.14 (Go 1.26), including depguard: 0 issues.**

## 5. Not covered

- **Real GKE logs of the target stack.** The demo reproduces the shape described in the feature request. Before relying on it, check a real namespace with `--config` and the team's pattern.
- **Colours:** the TUI was checked as text in tmux, not with the colour theme.
