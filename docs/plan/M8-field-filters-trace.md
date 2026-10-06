# M8 — Filter on a field, and the trace view  (status: done; as built: D-046, D-047)

Two navigation features answer the same question: **"show me every line about the same thing as this one"**.

| Milestone | Feature | What it gives |
|---|---|---|
| **M8.1** | Filter on a field from zoom | In zoom's FIELDS section, pick a field. **Keep** the lines whose field has **exactly** this value, or **exclude** them. Examples: `request_id=d04b1995`, `user_id=u-4821`, not `route=/actuator/health`. |
| **M8.2** | Trace view (`v`) | From a line, see every line of the same trace, on every pod of the service. Lines are in time order, with a **time delta** column (`+0 ms`, `+102 ms`, …). |

Each milestone ships on its own, passes `go test ./...` and `golangci-lint run`, and is usable by itself. M8.2 reuses the field matching of M8.1.

Both follow the rule of the project: **the TUI knows nothing about the application**.
- The fields are the ones the config folder produces: `fields`, `transform`, and JSON keys.
- The field that identifies a trace is the standard field `trace_id`, mapped by the config (`fields.trace_id`, or a transform such as `correlation-id=(?P<trace_id>\S*)`).
- The keys are remappable in `ui.keymap`.

---

## Common ground

### Why a new kind of filter

Text filters (`/`) search for a **substring** of the line's searchable text:
- `request_id=d04b1995` also finds `request_id=d04b19951` and `x_request_id=d04b1995`;
- the value is compared ignoring case.

Filtering on a field needs **equality on one field**. So M8.1 adds a *field filter* to the domain's filter engine:
- it matches when the entry's field has exactly this value, case-sensitive, since ids are case-sensitive;
- `Invert` excludes instead.

### What a field is

- The standard fields `logger`, `thread`, `trace_id`, `app` and `pid`.
- The **visible** extra fields (`LogEntry.Fields`): JSON keys, named groups, and fields extracted by a transform.
- **Not** hidden fields. Filtering on them would decode every buffered line again (see D-044 on cost). They are already out of text search, and zoom does not offer them for selection.

### Architecture

| Package | Change |
|---|---|
| `internal/core/domain` | `TextFilter` gains `Field string`. When set, the filter matches on equality of that field, with `Pattern` as the value. A helper `FieldValue(e, key)` reads a standard or visible field. M8.2 adds `Trace(buffer, id)`, which selects the entries of a trace (pure and testable). |
| `internal/adapters/driving/tui` | Zoom: a field cursor and the keep/exclude keys (M8.1). The logs screen: a trace state, a delta column and a status chip (M8.2). New actions in `keys.go`. |
| `internal/core/ports`, `app`, adapters, `config` | **No change.** Filtering already happens in the domain, on the buffer the TUI holds. The keymap overrides are validated by `tui.NewKeymap`, as today. |

---

## M8.1 — Filter on a field from zoom

### What the user sees

In zoom (`enter` on a line), the FIELDS section gets a cursor:

```
 FIELDS                                   tab/shift+tab field · = keep · ! exclude
   trace_id        7a27d24a6525815e…
   correlation-id  7a27d24a6525815e
 > request_id      d04b1995
   route           /v1/orders
   user_id         u-4821
```

- **`tab` / `shift+tab`** move the field cursor. There is no cursor until the first `tab`, so zoom stays as it is today for readers who never use it. `j`/`k` keep scrolling.
- **`=`** (keep) goes back to the logs with the filter `request_id = d04b1995` added.
- **`!`** (exclude) adds `request_id ≠ d04b1995`.
- **In the logs**, the filter stacks with the others (as with `ctrl+a`) and shows in the status bar as `request_id=d04b1995` or `request_id≠d04b1995`.
  - `esc` removes the last filter, as today.
  - `x` (filter/highlight), `n`/`N` and `X` (context) work as with text filters.
  - The cursor stays on the zoomed line if it still matches.
- **If the zoomed line has no visible field**, `tab` does nothing and the hint is not shown.

### Rules

| Topic | Rule |
|---|---|
| Match | Exact equality of the field's value, case-sensitive. A line without the field does not match `=`, and does match `≠`. |
| Several field filters | They all must match, like stacked text filters. |
| Highlight mode (`x`) | The matching lines are marked. Nothing is highlighted inside the line, since the value may not be drawn at all. |
| Values | Compared as `LogEntry` holds them (numbers as written in the line). |
| Typing a field filter in `/` | Not in M8.1. `/` stays a text search. It could come later as `field:route=/v1/orders`. |

### Keys (new actions, remappable)

| Action | Default | Why this key |
|---|---|---|
| `field_next` / `field_prev` | `tab` / `shift+tab` | Free in zoom; `tab` moves focus in most UIs. |
| `field_keep` | `=` | Reads as "equals"; unshifted on AZERTY. |
| `field_exclude` | `!` | Already means "invert" in the filter prompt; unshifted on AZERTY. |

### Cost

A field filter is one map lookup per line, cheaper than a text filter. There is no new per-line memory. The filter state is a few strings.

### Tests (M8.1)

- **`domain`**:
  - equality, a missing field, invert, standard fields, number values;
  - several filters stacked, and mixed with text filters;
  - `Active`, `Select` with context, highlight mode;
  - a benchmark of `Select` over 50 000 entries, with a text filter versus a field filter.
- **`tui`** (golden files, `-update` then review the diff):
  - zoom with the cursor on a field;
  - zoom without any field (no hint);
  - the logs after `=` and after `!`, with the status bar;
  - `esc` clearing the last filter.
- **Keymap**: new actions remappable, unknown ones rejected (existing test pattern).

### Docs (M8.1)

- `README.md` key table (zoom keys).
- `docs/CONFIG.md` `ui.yaml` action list.
- Help screen (`?`).
- **D-046**: equality instead of substring; hidden fields excluded; keys.

### Delivery (M8.1)

1. The domain field filter, with tests and a benchmark.
2. The zoom cursor and keys, with golden files.
3. The logs screen: stacking, the status bar and `esc`.
4. Help, README, CONFIG.md and D-046.

---

## M8.2 — Trace view (`v`)

### What the user sees

On a line of the logs screen, or in zoom, **`v`** opens the trace of that line:

```
 TRACE 7a27d24a…  ·  4 lines  ·  2 pods  ·  839 ms                      esc back
   +0 ms    9n2cx 17:16:42.061  INFO OrderController   : request received POST /v1/orders
 +102 ms    9n2cx 17:16:42.163  WARN DownstreamClient  : downstream latency above threshold
 +720 ms    ts262 17:16:42.781  INFO PaymentClient     : retry 1/3
 +839 ms    9n2cx 17:16:42.900 ERROR OrderSagaService  : Request processing failed
```

- Every line of the logs screen's buffer with the **same `trace_id`**, on **all pods** of the service, in time order.
- A **delta column**, owned by Huginn like the pod id, shows the time since the first line of the trace: `+0 ms`, `+1.2 s`, `+3 m 04 s`.
- **The header** shows the trace id, the number of lines and pods, and the duration from the first line to the last.
- **The trace view ignores the level, text and field filters and the pod scope** of the logs screen: a trace is only useful whole. They are kept, and `esc` goes back to the logs exactly as they were, with the cursor on the line `v` was pressed on.
- **Live:** new lines of the same trace are appended as they arrive.
- Inside the trace, `enter` (zoom), `/` (search, highlight only), `>`/`<` (errors) and `c`/`C` (columns) work as usual.
- **A line without `trace_id`**: `v` flashes `this line has no trace_id (fields.trace_id in formats/…)`.

### Rules

| Topic | Rule | Why |
|---|---|---|
| Identity | The standard field `trace_id`, exact equality (the M8.1 field filter). | The config decides which JSON key or text holds it. |
| Scope | The buffer of the logs screen: the loaded window and the pods of the service. | Huginn reads one service; searching across services is a V2 feature (Cloud Logging, D-010). |
| Partial traces | When the first line of the trace is the first line of the buffer, or the window started after the trace, the header says `may start before the window (t to widen)`. | Lines outside the window are not loaded, and the user should know. |
| Order | By the time of the entry (`Time`), since the delta is between application timestamps. | Delta between the application's own steps. |
| Size | Capped by the buffer (`logs.buffer_lines`). No extra copy: the view holds sequence numbers. | Memory. |

### Keys

- `view_trace` (`v`), already reserved in `keys.go` (D-020): in the logs screen and in zoom.
- `esc` leaves the trace.

### Architecture (M8.2)

- **`domain.Trace(buf, id) []uint64`**: the sequence numbers of the trace, in time order. It uses the M8.1 field filter and is tested on its own.
- **TUI**: a `trace` state of the logs screen, holding the id and the sequence numbers.
  - The delta is a Huginn-owned segment, drawn before the layout columns like the pod id.
  - Appended entries are checked one by one (a map lookup each).
  - Layouts are not changed: the delta column is not a config column.
- **Nothing else changes.**

### Cost

Opening a trace scans the buffer once: about 1 ms for 50 000 lines (to measure). Each new line costs one map lookup while the trace is open. There is no new goroutine.

### Tests (M8.2)

- **`domain`**: `Trace` (order, pods, missing ids, a trace of one line).
- **`tui`** golden files:
  - the trace view;
  - the delta formats (ms, s, min);
  - a partial-trace notice;
  - `v` on a line without `trace_id`;
  - `esc` restoring filters, pod scope and the cursor;
  - live append.
- **`bootstrap`**, demo end to end: `v` on an error line of `order-orchestrator` shows lines of several pods sharing its `trace_id`.
- **Benchmark**: opening a trace on a full buffer.

### Docs, demo (M8.2)

- README keys and help.
- `docs/CONFIG.md`: a short "trace view" note under `fields.trace_id` and `transform`.
- **D-047**: the scope is the buffer; filters are ignored and kept; order by entry time.
- Demo: the example format fills `trace_id` from `correlation-id` for `order-orchestrator`. The demo's requests already share their trace id across lines.

### Delivery (M8.2)

1. `domain.Trace`, with tests and a benchmark.
2. The trace state, the delta column and the header, with golden files.
3. Live append, `esc` restore and the notices.
4. Demo end to end, then docs and D-047.

---

## Decisions to confirm

1. **Hidden fields cannot be filtered** (recommended, for cost and consistency with search). The alternative, decoding them for every line, costs about 12 µs per line with transforms.
2. **Keys:** `tab`/`shift+tab` to pick a field, `=` keep, `!` exclude, `v` trace. All remappable.
3. **The trace view ignores filters and pod scope, and restores them on `esc`** (recommended). The alternative is to apply them inside the trace, which would show broken traces.

## Non-goals

- Searching a trace across services, or before the loaded window (V2, Cloud Logging).
- Typing field filters in the `/` prompt (possible later: `field:key=value`).
- Comparisons (`>=`, `<`) on field values: that is the "structured filters" idea, a separate milestone.
