# M9 — Select and copy log lines  (status: done; as built: D-049, D-050)

Today, getting lines out of Huginn into a ticket, a chat or an editor is awkward:
- **Huginn captures the mouse** (wheel scrolling, `MouseModeCellMotion`), so the terminal's own selection needs `shift`+drag in most terminals, and many users do not know that.
- **A native selection copies the screen, not the logs.** Wrapped lines are cut, panned lines are truncated, folded stack traces stay folded, and colors and the pod column come along.
- **Nothing copies the raw line** (the JSON), or a whole stack trace.

This milestone adds a **keyboard selection on the logs screen** and **copy to the clipboard**, then **save to a file**. The actions `mark` (`m`), `copy` (`ctrl+y`) and `save` (`ctrl+s`) are already reserved for this in `keys.go` (D-019, D-020), and the clipboard strategy is D-012.

| Milestone | Feature |
|---|---|
| **M9.1** | Select lines (a range with `V`, single lines with `m`) and copy them (`y` as shown, `Y` raw). The current line is copied when nothing is selected. |
| **M9.2** | Save the selection, or the whole filtered view, to a file (`ctrl+s`). Optional redaction patterns for everything copied or saved. |
| **M9.3** | Mouse: click selects a line, `shift`+click extends the range, and a setting releases the mouse to the terminal. |

Each milestone ships on its own and passes `go test ./...` and `golangci-lint run`. As for M6–M8, the TUI stays generic: formats, redaction patterns and paths come from the config folder, and the keys are remappable in `ui.keymap`.

---

## Common ground

### What is copied

| Form | Key | Content |
|---|---|---|
| **as shown** (default) | `y` | Each line as the layout draws it, **without colors**: pod id, then the columns, the separator and the message, **never truncated or wrapped**. A folded stack trace is copied **whole**, below its line. The pod id follows the pod-id setting (`I`), and hidden columns (`c`, `C`) are left out, as on screen. |
| **raw** | `Y` | The original line as received: the JSON object, or the text for unstructured lines. It is untouched by `transform`, `hidden` and layouts, so it can be pasted into `jq`. |

- Lines are copied in **display order**, oldest first unless `o` shows newest first. They are separated by `\n`, with a trailing `\n`.
- The **trace view** copies with its delta column, since that is what it shows.

### Selection model

- The selection is a set of **entries** (sequence numbers), never screen rows. Wrapping, panning and folded stacks therefore do not matter.
- **Only what is displayed can be selected.** A range covers the rows shown between its two ends; lines hidden by filters or pod scope are not included. Changing a filter keeps the marks: marked lines that become hidden are not copied, and come back when shown again.
- **Evicted lines** (older than the buffer) leave the selection silently.

### Clipboard (D-012)

The new driven port `ports.Clipboard` has one method, `Copy(ctx, text) error`. It is used in two ways:
1. **OSC 52**, through Bubble Tea's `tea.SetClipboard`. This is written by the TUI itself, since it owns the terminal. It works over SSH, in tmux (with `set-clipboard on`), in Windows Terminal, iTerm2, kitty, WezTerm and VS Code/IntelliJ terminals.
2. **System clipboard**, through the new adapter `adapters/driven/clipboard`. It runs `pbcopy`, `wl-copy`, `xclip`/`xsel` or `clip.exe`, whichever exists, with no cgo and no new dependency. The tool comes from the environment, never from code aimed at one OS version.

A new `ui.yaml` key, `clipboard: auto | osc52 | system | off` (default `auto`), chooses between them:
- **`auto`** writes OSC 52 and also uses the system tool when one exists. Neither can be detected reliably, and a second write is harmless.
- **`off`** disables copying, for locked-down setups.

**Result shown:** a flash such as `copied 12 lines (3.4 KB, as shown)`. When the system tool fails and only OSC 52 was sent, the flash says `copied via the terminal (OSC 52)`.

### Limits

- **`copy.max_bytes`** (`ui.yaml`, default 1 MiB) bounds one copy. Terminals cap OSC 52 payloads, and a 50 000-line selection would be tens of MB.
- Past the limit, **nothing is truncated silently**. The flash says `selection too large (38 MB): ctrl+s saves it to a file`.
- **Building the text is O(bytes copied)**, and only happens on `y`/`Y`. There is no cost per frame except a set lookup per drawn row for the markers.

### Architecture

| Package | Change |
|---|---|
| `internal/core/ports` | `Clipboard` interface (driven), with a fake in `portstest`. M9.2 adds `FileSink` (`Save(ctx, name string, r io.Reader) (path string, err error)`, driven). |
| `internal/core/domain` | M9.2 adds **`Redactor`**, already named by ARCHITECTURE rule 10: a list of compiled patterns that replace matches with `[redacted]`, applied to everything copied or saved. |
| `internal/adapters/driven/clipboard` | New: the system-tool clipboard, with the tool injected so tests use a fake. It needs a new `internal/archtest` rule (adapters: domain, ports, stdlib). |
| `internal/adapters/driven/filesink` | New in M9.2: writes files in the configured directory, with no overwrite and `0600` permissions. It gets an `archtest` rule too. |
| `internal/adapters/driving/tui` | Selection state, markers, keys, the "as shown" and "raw" text, OSC 52, flashes. |
| `internal/config` | `ui.yaml`: `clipboard`, `copy.max_bytes`; `save.dir` (M9.2); `redact` (M9.2); `mouse` (M9.3). Schema and CONFIG.md. |
| `internal/bootstrap` | Wires the adapters by config. |

---

## M9.1 — Select and copy

### What the user sees

```
 m8q7v 19:13:04.000 DEBUG [exec-1] c.z.hikari.pool.HikariPool : Pool stats …
▌m8q7v 19:13:12.000 ERROR [exec-1] i.g.p.PaymentService : Payment authorization failed …
▌    io.gimle.payment.PaymentGatewayException: upstream request timed out [+3 lines]
▌x4k2p 19:13:15.000 INFO  [exec-1] i.g.p.PaymentController : request completed …
*x4k2p 19:13:32.000 ERROR [exec-1] i.g.p.card.CardController : Card declined …
 SELECT 3 lines + 1 marked   ·  y copy · Y copy raw · esc clear
```

- **Gutter:** a column, owned by Huginn like the pod id, before the pod id. `▌` marks a line in the range and `*` a marked line (D-020). Selected rows also get the theme's selection background (`Theme.Selected`, which already exists).
- **`V`** starts a range at the cursor. Moving the cursor (`j`/`k`, `pgup`/`pgdn`, `g`/`G`, `n`/`N`, `>`/`<`) extends it, and `V` again ends it, keeping the lines selected.
- **`m`** marks or unmarks the cursor line, for lines that are far apart. Marks and the range add up.
- **`y`** copies the selection as shown and **`Y`** copies it raw. With **no selection**, both copy the **cursor line**, which is the common case. After copying, the selection stays, so the other form can be copied too.
- **`esc`** clears the selection first. The next `esc` behaves as today (fullscreen, filters, back).
- **Status bar:** the chip `SELECT` with `n lines + m marked`, and the key hints change while a selection exists.
- **In zoom:** `y`/`Y` copy the zoomed entry (as shown with its full stack, or raw).

### Rules

| Topic | Rule |
|---|---|
| Follow mode | Starting a range stops sticking to the tail (as moving the cursor does). New lines keep arriving below; they are not selected. |
| Pause | Works as in any state. |
| Newest first (`o`) | The range is the same set of entries; the copy uses the display order. |
| Trace view | Selection works inside the trace. Leaving the trace (`esc`) first clears a selection, then leaves. |
| Empty view | `y` flashes `nothing to copy`. |

### Keys (remappable)

| Action | Default | Note |
|---|---|---|
| `select` | `V` | New; like vim's visual line mode. |
| `mark` | `m` | Reserved already (D-020). |
| `copy` | `y`, `ctrl+y` | `ctrl+y` was reserved; `y` added since it is the common yank key. |
| `copy_raw` | `Y` | New. |

### Tests (M9.1)

- **`tui`**:
  - range and marks (display order, with filters, eviction, newest first, trace);
  - "as shown" text (no ANSI, not truncated, full stack, hidden columns, pod id modes);
  - "raw" text;
  - cursor-line copy;
  - the `max_bytes` refusal;
  - `esc` order;
  - golden files: the gutter, the `SELECT` status and zoom.
- **`clipboard` adapter**: tool selection with fake `LookPath`/exec, errors, `off`.
- **`config`**: `clipboard`, `copy.max_bytes` validation and schema.
- **Benchmarks**: a frame with 1 000 marked lines, which must stay within the frame budget (D-031); building a 10 000-line copy.

---

## M9.2 — Save to a file, and redaction

- **`ctrl+s`** saves the **selection**, or when nothing is selected the **whole filtered view** (every displayed row, not only the screen). The format follows the last copy form (as shown by default, raw with `ctrl+s` after `Y`), or a small picker (`s` shown / `r` raw NDJSON) to be decided.
- **Destination:** `ui.yaml save.dir`, a path, default the current directory. The file is named `<repo>-<env>-<yyyymmdd-hhmmss>.log` (or `.ndjson` for raw), never overwrites, and is written with mode `0600`. The flash gives the path.
- **Redaction:** `ui.yaml redact: [patterns]`, Go regular expressions whose matches become `[redacted]` in copies and saves. Examples in `examples/` only (tokens, emails); nothing is built in (CLAUDE.md). The screen itself is not redacted: the user sees the logs as they are.
- **Architecture:** `ports.FileSink` and the `adapters/driven/filesink` adapter, with the filesystem injected (rule 8) and a fake for tests. The `domain.Redactor` is pure and table-tested, with a benchmark.
- **Tests:**
  - the file name, no overwrite, permissions, and a directory that does not exist (a clear flash, no crash);
  - redaction on both forms;
  - a whole-view save of 50 000 lines, streamed without building one big string.

## M9.3 — Mouse

- **Click** on a row moves the cursor there. **`shift`+click** extends a range from the cursor. **Drag** selects rows.
- **`ui.yaml mouse: true | false`** (default `true`). With `false`, Huginn does not capture the mouse: the terminal's own selection works directly, and the wheel no longer scrolls Huginn.
- Tests: mouse messages mapped to rows, with wrapped rows and the pod strip offset.

---

## Decisions to confirm

1. **Keys:** `V` range, `m` mark, `y`/`ctrl+y` copy as shown, `Y` copy raw, `ctrl+s` save.
2. **Default copy form: as shown** (the lines as read on screen, uncolored and whole), with `Y` for raw.
3. **Clipboard `auto`:** OSC 52 always, plus the system tool when present.
4. **Selection covers displayed lines only** (filters apply to what is copied).
5. **Order:** M9.1 now; M9.2 and M9.3 after, each with its own go.

## Non-goals

- A bug-report bundle (`B`, reserved: lines plus environment and versions). It could build on M9.2 later.
- Selecting part of a line (characters). The terminal's own selection covers that, and M9.3 makes it easy to reach.
- Reading the clipboard (paste into filters).
