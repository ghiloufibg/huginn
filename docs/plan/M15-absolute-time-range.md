# M15 — Absolute time range `[from, to]` (design)

Status: **implemented** (see [D-071](../DECISIONS.md)); real-GKE measurements of section 4a.4 are still to do. Where the code differs from the text below, the code and D-071 win: the page cursor counts lines per container at the boundary instant instead of comparing their text (so section 4a.3's "known limit" does not exist), Kafka pages are per-partition slices (section 8b), and the `b`/`[`/`]` keys are not in the full key bar.

## 1. Goal

Today the logs screen loads a *relative* window: `15m … 2d`, `tail`, `head` (`domain.TimeWindow`, keys `1…7 0 9`, picker `T`). Add an *absolute* range, as in Grafana, Kibana or Datadog: the user picks `[start date-time, end date-time]` and sees the lines of that interval, for example "the incident between 14:30 and 14:35 yesterday".

The range applies to **both** the logs screen and the Kafka records screen (§8b), and **no line of the range is lost**: a range larger than the in-memory buffer is read in contiguous pages, one page in memory at a time (§4a).

Out of scope: a calendar widget, saved ranges, live zoom by mouse drag, time shifting (see §10).

## 2. What the code does today (facts that shape the design)

| Fact | Where | Consequence |
|---|---|---|
| `TimeWindow` is `{Since, Tail, Head}`, exactly one set, compared with `==` | `domain/timewindow.go`, `setWindow`, `windowPicker` | A range is a fourth variant; `==` must keep working or be replaced by `Equal`. |
| The Kubernetes log API has `sinceTime` (**second precision**) but **no upper bound** | `kubernetes/logs.go:49` | The end of a range cannot be asked of the cluster: it is enforced by stopping the stream. |
| `Limit` becomes `tailLines`: the API returns the **newest** N lines since `sinceTime` | `tailer.request`, `logOptions` | With an end in the past, `tailLines` would return lines after `to` and push the range out. A range must not send `tailLines`. |
| A **head** already reads forward from the start, decodes as it reads, stops after N lines and never follows | `tailer.history`, `logsession.go:68`, D-039 | A range is "a head that starts at `from` and ends at `to`": the machinery exists. |
| A `SinceTime` in a request replaces the window (used to resume a followed stream) | `ports/logs.go`, `tailer.run` | An open-ended range resumes like any followed window. |
| `popup` overlays are modal; the only free-text input today is the filter prompt | `model.go:343`, `logfilter.go` | A text-field popup will not trigger the `1…7` keys. No text-input library is in use. |
| Pods come from the live cluster; the API serves only logs still on the node | `session.podsOf` | A past range only covers pods that still exist and logs not yet rotated. Inherent to a viewer without a log store; the UI must say so (§7). |
| The Kafka screen reuses `TimeWindow` (durations only) | `kafkarecords.go`, `kafkacli.go:145` | A new variant must not leak there by accident (§8). |

## 3. Domain (`internal/core/domain`)

New file `timerange.go`; `timewindow.go` changes minimally.

```go
type TimeWindow struct {
    Since time.Duration
    Tail  int
    Head  int
    // From and To select an absolute range. From is inclusive, To exclusive
    // ([From, To), so adjacent ranges never share a line). To zero means
    // "until now": the range then follows like a relative window.
    From, To time.Time
}
func (w TimeWindow) IsRange() bool { return !w.From.IsZero() }
func (w TimeWindow) Closed() bool  { return w.IsRange() && !w.To.IsZero() }   // never follows
func (w TimeWindow) FromStart() bool { return w.IsHead() || w.IsRange() }     // read forward, keep the oldest
func (w TimeWindow) Equal(o TimeWindow) bool
```

- **Invariant**: exactly one of `Since`, `Tail`, `Head`, `From` is set. `From`/`To` are stored in UTC without monotonic reading (`Round(0).UTC()`) by the constructor, so `==` stays valid; call sites that compare windows move to `Equal` anyway.
- `String()` for a range: `2026-11-01T14:30:00Z..2026-11-01T14:35:00Z` (or `..` open), round-trippable. `Label()` returns `"range"` like `"tail"` and `"head"`: local-time formatting needs a location, so it lives in the TUI (§6), not in the domain.
- `SelectWindow` gains a range branch (`From <= t < To`; lines without a timestamp are kept, as for `Since`).
- **Parsing** is a separate function so config and the `ParseTimeWindow` grammar are untouched (no regression risk for `windows.presets`, `windows.default`, `--since`, Kafka):

```go
func ParseTimeRange(from, to string, now time.Time, loc *time.Location) (TimeWindow, error)
```

Accepted forms for each field (`now` and `loc` are injected, rule 8):

| Input | Meaning |
|---|---|
| `2026-11-01 14:30`, `2026-11-01 14:30:05`, `2026-11-01T14:30:05` | local time in `loc` |
| `2026-11-01T14:30:05Z`, `…+02:00` | explicit zone wins |
| `14:30` | most recent occurrence not after `now` (today, else yesterday) |
| `now`, `now-90m`, `now-2h`, `now-1d` | relative to `now` (Grafana syntax, reuses `parseDuration`) |
| empty `to` | open end: follows |

Validation errors (shown inline in the popup, wrapped for the CLI): unparsable value, `from >= to`, span under 1 s, `from` or `to` after `now`, with the hint "clear *To* to follow".

DST: an ambiguous or non-existent local time resolves the way `time.Date` does; the popup always shows the resolved value with its UTC offset so the user sees what will be used.

## 4. Ports and use case

- `ports.LogRequest` and `ports.LogQuery`: **no new field**; they already carry `Window`. Doc comments change:
  - *LogSource contract for a range*: lines delivered are those with `From <= Time < To`; a source **may** stop reading once a line reaches `To` (efficiency) and **may** return up to one second before `From` (API precision); consumers skip both. This mirrors the existing `SinceTime` wording.
- `LogSessions.Open`: `if q.Previous || q.Window.IsHead() || q.Window.Closed() { q.Follow = false }`.
- `previous` (`p`) ignores the range, like it ignores durations: the whole previous instance.
- `tailer.request()` for a range: `Window` passed through, **`Limit = 0`** (no `tailLines`), memory bounded the way a head is: decode as read, stop after `min(limit, buffer)` kept lines (× `muteBudget` when the format mutes). Reuses the head branch (`window.FromStart()`).
- `newHistoryCollector(limit, window.FromStart() && !q.Previous)`: when a range holds more lines than the buffer, the **oldest** are kept (one *page*) and the batch says where the page ended, so the screen can offer the next page (§4a). Nothing is dropped silently.
- `tailer.history`: exact filter on top of the source's: drop `l.Time < From` (boundary second) and stop at the first `l.Time >= To`, whatever the adapter did. Correctness lives in the core so every adapter (Kubernetes, demo, a future Loki) is right even if it ignores `To`.
- `retentionNotice`: for a range, the reference start is `From` (not `opened - Since`): *"logs of pod-x available from Nov 1 14:41 only"* reuses the existing wording when a container returned nothing for the first part of the range. Pods that started after `To` can be skipped without a request *(optional optimisation, not required)*.
- Open-ended range (`To` zero): history from `From`, then follows. After the history, resume uses `SinceTime = last` as today.

## 4a. No lost lines: how other tools do it, and the paging design

### What existing tools do (researched 2026-11-01)

| Tool | Behaviour | Source |
|---|---|---|
| **k9s** | Fixed in-memory buffer (`logger.buffer`, default 5000, capped at 5000) and an initial `tail` (default 100). `sinceSeconds` is a relative window only; **no absolute start/end**. Scrolling up does **not** fetch older lines: users report they stop at the last `tail` lines unless the view stayed open. The maintainer's answer to "show all logs" is that it is "really tough" for chatty services and to use an external log plugin (`kubectl logs … | less`). Its time keys are also reported unreliable (issue open as "not planned"). | [issue #1228](https://github.com/derailed/k9s/issues/1228), [config package](https://pkg.go.dev/github.com/derailed/k9s/internal/config) |
| **Lens** | Loads 500 lines; **scrolling to the top fetches more** on demand. | [Lens forum](https://forums.k8slens.dev/t/pod-log-history-size/595) |
| **stern / kubectl** | Relative `--since` or absolute `--since-time` only; **no end time**. Bounded windows are done by filtering the output afterwards. | [stern](https://github.com/stern/stern), [kubectl logs](https://uptrace.dev/blog/kubectl-logs) |
| **Kubernetes API** | `sinceTime` (cut to the second), `tailLines`, `limitBytes`; **no upper bound**. A tool needing "until" requests timestamps and cuts itself. Forward paging = start the next call at the last line's time. `tailLines` is applied *after* the since filter, so it takes the newest lines of the span. | [MEHO #5071](https://github.com/evoila/meho/issues/5071), [metalk8s #5155](https://github.com/scality/metalk8s/pull/5155) |

Not verified: k9s's internal flush/batch behaviour (the page describing it returned HTTP 429), and Headlamp's behaviour (nothing found). Neither changes this design.

### What follows from it

1. **Nobody holds an unbounded history in memory**; everyone bounds a view and loads more on demand (Lens) or gives up (k9s). Huginn goes further than k9s: nothing is silently unreachable. This is the same bounded-memory principle as D-063/D-064.
2. **Memory is O(one page), not O(range).** Only the page on screen is held. Moving to another page drops the current one and re-reads from the source. Cost is paid only when the user asks for it.
3. **The server is asked for as little as possible.** A page is a forward read from the page start that is *closed as soon as the page is full* (the stream already does this for a head: returning closes the body and ends the kubelet's read, `kubernetes/logs.go:87`). No `limitBytes` (it cuts lines), no `tailLines` (wrong end).
4. **Page size.** One page = `logs.buffer_lines` lines (default 50 000), the size whose memory and decode cost were already measured in D-063/D-064. Not a new tuning knob.

### Design

1. **Pages.** A page is the first `buffer_lines` lines (merged by time across pods) of what remains of the range. If the range has more, the status bar shows `page 1 · more after 14:32:10` instead of a "not loaded" warning. `]` loads the next page, `[` the previous one; pages are contiguous and do not overlap. A hint appears in the status bar when the cursor reaches the last line of a page that has a successor.
2. **How the next page starts.** It reuses the resume mechanism the followed streams already use: `SinceTime = time of the last line shown`, and lines already shown at exactly that time are skipped by comparing their text (`skipResumed`, `tailer.go`). The session keeps the list of page start times (a few timestamps), so `[` re-queries the previous start; nothing is cached for pages that are not shown.
3. **No known limit (changed while implementing).** The first design skipped lines already shown by comparing their text, which would collapse byte-identical lines sharing a timestamp at a boundary. The cursor instead counts, per container, the lines already delivered at the boundary instant, and the next page skips that many: exact, whatever the text. Kafka pages by offset.
4. **Cost to measure, not assume** (GKE QA round): the kubelet scans the container log file to find `sinceTime`, so a page far from the start of a long file costs more than the first. Measure page 1 vs page N on a large log, and decide from data whether a smaller page or a time hint is needed. If it is too slow, the fallback is narrowing the range, which the UI already suggests.
5. **Raising `logs.buffer_lines`** gives bigger pages for modest ranges, at a memory cost already benchmarked in D-063/D-064.
6. **Later, if wanted:** load the next page automatically when scrolling past the end (the Lens behaviour) instead of on `]`. Not in this milestone: explicit pages are simpler to reason about and to test.

Saving a whole range to a file is **out of scope** (owner decision: not a quick way to debug). Pages plus the existing `ctrl+s` (current page) stay as they are.

## 5. Adapters

| Adapter | Change |
|---|---|
| `driven/kubernetes` | `logOptions`: range → `SinceTime = From`, no `TailLines`, `Follow` only if open-ended. `stream.read`: for a closed range, return (closing the body) at the first line with `Time >= To`, as it already does after `Head` lines. |
| `driven/demo` | `history()` gets a `w.IsRange()` branch generating `[From, To)` bounded by retention and the instance's life. Keeps `--demo` and the tests deterministic. |
| `portstest` | `RunLogSourceContract`: new cases "range returns only lines in `[From,To)` (±1 s at From)", "closed range ends without Follow", "open range then follows". Run against fake, demo and (when a cluster is present) Kubernetes. |

Open question for the GKE QA round *(verify)*: how much the kubelet pays to find `sinceTime` in a large log file when `From` is far back. Measure, do not assume.

## 6. TUI (`internal/adapters/driving/tui`)

### Entry points
- New action `ActWindowRange`, default key **`b`** ("between"). Checked against `defaultKeys` in `keys.go`: `b` is unused (`R` is taken by reset-display). Free single letters at the time of writing: `b h u O Q U Z`. `[` and `]` (pages, §4a) are also unused. No non-QWERTY alias is needed: `b` is a letter and the popup itself is text entry. The keymap can still override it, and a duplicate-key test must be added.
- A last row **"Custom range…"** in the existing `T` picker opens the same popup.
- CLI: `--from` and `--to` (see §8).

### Popup `rangePicker` (new file `rangepicker.go`, implements the same overlay interface as `windowPicker`)

```
┌ Time range ──────────────────────────────┐
│ From  2026-11-01 14:30:00               │
│ To    2026-11-01 14:35:00               │
│                                          │
│ 14:30:00 → 14:35:00 CET (UTC+01:00) · 5m │   ← live preview of what will be loaded
│ ✗ from must be before to                 │   ← only when invalid (Theme error ink, no icon, D-020)
│                                          │
│ tab field · enter load · ctrl+u clear · esc cancel │
└──────────────────────────────────────────┘
```

- **Prefilled** with the bounds of the current window (a `15m` window shows `now-15m → now` resolved), so the user edits instead of typing from scratch. `To` left empty = follow.
- Live validation on every key through `domain.ParseTimeRange` (pure, cheap). `enter` is inert while invalid; the error line says why.
- Editing: reuse the line editing of the filter prompt if it is separable (`logfilter.go`: `startEditing`, `filterKey`), otherwise a ~40-line field type. **No new dependency** *(verify which)*.
- The `1…7` keys do not fire inside the popup (popups are modal).

### After loading
- `logsScreen.window` is the range; `setWindow` already reopens the session. Replace `w == l.window` by `w.Equal(l.window)`.
- Status bar field (where `head` prints `l.window.String()`): `Nov 1 14:30 → 14:35` same day, `Nov 1 14:30 → Nov 2 09:00` across days, `Nov 1 14:30 → now` open; local zone abbreviation when `Location` is not local.
- `f` (follow) on a **closed** range behaves as on a head (D-039): goes back to the default window and follows, so no gap is left silently.
- Empty state, new wording that includes the real limit: *"no line between 14:30 and 14:35. Pods that no longer exist and logs rotated on the node are not available."* with hints `T` other range, `0` last lines.
- Help overlay and `actions.go` table get the new action (section "Time and stream").

### Time zone
**Detected automatically, no option.** Go's `time.Local` is the machine's zone (the `TZ` variable, else the operating system's setting, Windows included), and the code already shows times in it. `Options.Location *time.Location` is injected only so tests can pass UTC and goldens do not depend on the machine; bootstrap passes `time.Local`. Anyone who wants another zone for a run can set `TZ`. The popup and status bar always show the zone in use, and an explicit `Z` or `+02:00` in the input overrides it.

## 7. Behaviours and edge cases

| Case | Behaviour |
|---|---|
| Range larger than the buffer | Pages (§4a): oldest page first, `]` for the next, `[` for the previous. |
| `to` in the future / `from >= to` / span < 1 s | Inline error, nothing loaded. |
| Pod deleted or rotated logs | Empty or partial result with the retention notice; never an error. |
| Second-precision `sinceTime` | Duplicated boundary lines removed exactly in the tailer. |
| Muted loggers, levels, text filters, pod scope | Unchanged: they filter the loaded range, not the request. |
| Closed range + `p` (previous) | Previous instance, whole; the range label is replaced by "whole instance" as for durations. |
| Resize / narrow terminal | Popup follows D-054: it is clipped, not spilled; one size test. |

## 8. CLI, config, bootstrap

- `huginn --from <t> [--to <t>]`, same grammar as the popup, parsed in `bootstrap` with `ParseTimeRange`; `--since` together with `--from/--to` fails: *"--since and --from: give one of them"* (same style as `kafka read`).
- `config/validate.go:149`: presets and `default` already reject `tail`/`head`; add `IsRange()` for defence (cannot occur: `ParseTimeWindow` never returns a range).
- `kafkacli.go:145` already requires `tw.Since > 0`: unaffected.
- No new config key (timezone is automatic), so `make schema` and `docs/CONFIG.md` do not change; the key table in the README and the help overlay do.
- No new package: `archtest` needs no new rule.

## 8b. Kafka records screen

Same popup, same grammar, same key `b`, same pages (`]` / `[`, offsets instead of times).

- `ports.TopicRead` gains `From, To time.Time` next to `Since` and `Tail`; the use case converts a range `TimeWindow` into them (the Kafka screen and `huginn kafka read` take `--from/--to` too). `kafkacli.go:145` stops requiring `Since > 0` for ranges.
- `kafka.go` `offsets()`: start per partition = `ListOffsets(From)`, end = `ListOffsets(To)` clamped to the end offset taken at that moment (`-1` from the broker, no record at or after the time, means "end"). A closed range never follows; an open one reads to the end offset then follows.
- Kafka timestamps are producer-set (`CreateTime`) unless the topic uses `LogAppendTime`, so they can be out of order: the broker's offset lookup is then approximate at the edges. The core therefore **also filters records by timestamp** `[From, To)`, as for logs, and the contract test covers an out-of-order fixture.
- Paging: the next page starts at the offset after the last record shown, per partition. Exact, with no text comparison, so Kafka has no duplicate-at-boundary caveat.
- Reading stays behind the request guard (D-057, read only): only `ListOffsets`, which the guard already allows for the existing `Since`.
- `windowLabel()` and the empty state of `kafkarecords.go` gain a range wording; the duration-only `windowKey` presets are unchanged.

## 9. Alternatives considered

| Option | Why not |
|---|---|
| Extend `ParseTimeWindow` with `14:30..14:35` typed in a prompt | Hard to discover, and mixes a pure-duration grammar used by config and Kafka with absolute times. Kept the two parsers separate. |
| A calendar / date picker widget | Heavy in a terminal, slower than typing for the target user. |
| Filter the already loaded buffer by time | The buffer holds the newest N lines only; an old range would be empty. The cluster must be asked with `sinceTime`. |
| Keep the **newest** lines when the range exceeds the buffer | Needs `tailLines`, which counts from *now*, not from `to`. Wrong lines for any range ending in the past. Oldest-first also allows stopping the read early. |
| New `TimeRange` type and a new `LogRequest` field | Cleaner types, but `LogQuery`, `NextWindow`, presets, the picker and the Kafka query all take `TimeWindow`: much more churn for no behavioural gain. Trade-off recorded; a sealed-union refactor of `TimeWindow` is a separate piece of work. |
| Enforce `to` in the adapter only | Each future adapter would have to be right; enforce in the core, let adapters stop early as an optimisation. |

## 10. Later (not in this milestone)

- Time shift keys (move the range back/forward by its width) and zoom out (×2), the Grafana habit, cheap once `Range` exists. `[` / `]` now page, so they need other keys.
- Skip containers whose pod started after `to`.

## 11. Test plan

- **Domain** (table-driven): every input form, DST gap and overlap, `from >= to`, future bounds, `now-…`, open end, `String()` round trip, `Equal`, `SelectWindow` boundaries (`[From,To)`).
- **Contract** (`portstest`): the three range cases against fake and demo; Kubernetes against a fake body (`logOptions` has no `TailLines`; `read` stops at `To`).
- **App** (fakes): closed range never follows; open range follows and resumes; oldest kept with the notice when over the buffer; boundary second de-duplicated; retention notice uses `From`; `Previous` ignores the range.
- **TUI**: popup states (prefilled, valid preview, each error), status bar for same-day, multi-day and open ranges, empty state, help. Goldens via `go test ./internal/adapters/driving/tui -update`, diff reviewed, tests run with a UTC `Location`.
- **Paging**: a range of 2.5 × buffer lines over two pods yields three contiguous pages with no gap and no duplicate (including lines sharing a timestamp at a boundary); `[` after `]` returns the same first page; memory stays bounded to one page while paging through the whole range.
- **Kafka**: contract cases for both bounds, a partition with no record at or after `To`, an out-of-order timestamp fixture, a closed range that does not follow, page continuation by offset.
- **CLI**: flag conflicts and parsing, for `huginn` and `huginn kafka read`.
- **Real GKE (M15 QA round)**: a range in the past on a live pod, a range spanning a rotation, cost of a far `sinceTime`, a closed range on a busy container for memory.
- Gate before committing: `go test ./...`, `golangci-lint run`, `make schema`.

## 12. Decision to record when implemented (D-071)

"Absolute time range": `TimeWindow` gains `From/To`; a range is read forward like a head, in pages of the buffer's size, so no line is skipped; the end is enforced in the core and, as an optimisation, by stopping the stream; `ParseTimeWindow` is untouched and ranges have their own parser; local time detected automatically (`time.Local`) with an injected `Location` for tests; logs and Kafka share the range.

## 13. Decisions taken

- Open end (`To` empty, then follow): kept (owner).
- Kafka included (§8b); key `b` (§6); timezone automatic, no config key (§6).
- No lost lines through pages (§4a); **no** whole-range save (owner).
- Page keys `[` and `]` are unused in `defaultKeys` (checked); no other key needed.

Open: none blocking. Measurements listed in §4a.4 and §5 belong to the QA round.
