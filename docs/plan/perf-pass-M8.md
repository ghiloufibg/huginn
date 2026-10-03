# Performance and production-readiness pass after M6–M8  (status: done)

This pass looks at the code paths M6–M8 added or touched: JSON decoding with transforms and `level_from`, field filters, the zoom, and the trace view. It also covers the shared paths they run through: filter selection, view rebuilds and frame drawing.

The method:
1. **Regression check:** the shared benchmarks, before M6 (`1ea6ac0`) against now, with `benchstat` over 6 runs.
2. **CPU profiles** (`pprof`) of the hot paths, to find the causes.
3. **Fixes** measured one at a time: every behaviour kept, every test green.
4. **Code review** of M6–M8 for edge cases, with a test for each finding.

## 1. Regression check (before M6 → before this pass)

There was no slowdown on the shared paths: decode, filter selection, frame, ingest. One cost was found: **+16 B allocated per decoded JSON line** (720 → 736 B). The lazy hidden-field loader captured one more value. It is kept for nearly every buffered line, since log agents add hidden Kubernetes metadata, so that is about 800 KB more for 50 000 lines.

## 2. Findings and fixes

| # | Finding (profile) | Fix | Result |
|---|---|---|---|
| 1 | **The `hidden` globs took about 25 % of every JSON line's decoding.** `path.Match` ran again for every key of every line, although keys repeat. The "whole object hidden" test also built a string per nested object. | A **cache of hidden decisions per decoder**, holding "hidden" and "hidden with everything below" per key. It uses a read lock (never waits once warm), is bounded to 4 096 keys against hostile or ever-changing keys, and has a race-tested concurrent test. A copy-on-write version was tried first: the benchmark showed a quadratic fill (10 000 keys: 12 → 25 ms), so it was replaced. | JSON decode **3.95 → 2.9 µs (−27 %)**. Parallel decode on 4 CPUs: 1.27 → 1.08 µs per line with the cache, against no cache. |
| 2 | The lazy loader closure captured an extra flag (+16 B per line). | Two closures, one per case. | Back to 720 B per line (1 666 B retained per buffered line, as before M6). |
| 3 | `splitPairs` took 14 % of a `pairs` line: `strings.TrimLeft`/`IndexAny` rebuilt their character set on every call. | A byte scan (`isSpace`). The fuzz test that compares it with `pair_pattern` passes. | `pairs` line **10.6 → 8.5 µs (−19 %)**. |
| 4 | **`LevelOK` took 27 % of a view rebuild**: a map lookup per buffered entry. | `SelectAppend` turns the level set into a 5-entry table once per call and computes `Active()` once, not per entry. The `LevelSet` type and its semantics are unchanged: an empty set still shows nothing, and a nil set shows everything. | Trace open + close **5.9 → 3.4 ms (−42 %)**, ingest with context −24 %. |
| 5 | The rebuild tested the pod/container scope of every entry, even with no scope chosen. | A fast path when no pod or container is selected. | Part of #4. |
| 6 | **`segmentInk` copied the whole `Theme`** (dozens of lipgloss styles) on every drawn segment, and built `"level:"+n` / `"pod:"+n` keys per row. Other per-row functions took the theme by value too. | The theme is passed by pointer everywhere, including the value receivers `levelStyle`, `statusStyle`, `podStyle`. The ink keys are built once. | Frame **622 → 523 µs (−16 %)**, −7 % allocations per frame. Live ingest −11 %, services frame −8 %. |

End to end (`HUGINN_PERF=1 go test -run TestPerfHistoryLoad ./internal/bootstrap`, 50 000 demo lines, 3 runs): about 1.71 s before M6 and 1.34 s now. Most of that time is the demo generating its lines. Retained memory is 1 666 B per line, the same as before M6.

What was not changed:
- **The regexp engine**, which is about 60 % of a transformed line: that is the user's pattern.
- **ANSI width and truncation in the frame**, from the charm libraries.
- **The demo generator's allocations.**

## 3. Production-readiness review (M6–M8)

| Finding | Fix | Test |
|---|---|---|
| Changing the window (or `P`, `A`) during a trace reloads the logs, so the entry `v` was pressed on is gone. `esc` then put the cursor on the **oldest** line. | When the saved entry is no longer held, `esc` follows the newest lines. | `TestTraceViewAfterReload` (fails without the fix) |
| `ctrl+r` inside a trace changed how the restored text filter was read on `esc`. | The regex mode is saved and restored with the filters. | `TestTraceViewRestoresRegexMode` |
| Checked, no change needed: sequence numbers never repeat after a reload (`LogBuffer.Reset` keeps counting), so a saved entry can never point to another line. Late lines renumber the saved entry. The level-set semantics are kept. The transform never panics, and `hiddenOf` recovers on the UI goroutine (D-044). | | existing tests |

## 4. Validation

- `go test ./...` passes, including `-race` on logformat, bootstrap, core and tui.
- The fuzz tests `FuzzTransform`, `FuzzPairPatternMatchesDefault` and `FuzzDecoders` passed 30 s each.
- `golangci-lint` v2.14 reports 0 issues, and `internal/archtest` passes.
- The real TUI was checked in tmux: the logs, zoom field filter and trace view work on the demo.
