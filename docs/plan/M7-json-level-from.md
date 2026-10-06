# M7 — `level_from` for JSON formats  (status: done)

A JSON line can carry its real severity somewhere other than its level key. A request logged at `INFO` can say `http_status=500` in its MDC context, or in a JSON key. Huginn then shows it as INFO:
- `e` (errors only) does not keep it;
- `>` (next error) skips it;
- the status bar does not count it among the errors.

`decoder: regex` already solves this with `level_from` (a field → glob → level table). This milestone opens the same key to `decoder: json`. It reads JSON keys and the fields extracted by a `transform` (M6).

Everything is in the config folder. The code gains a mechanism only: no field name, status code or convention is written in it.

## 1. What the user writes

```yaml
# formats/mdc-json.yaml
decoder: json
fields: { level: level, message: message, … }
transform:
  message: { pattern: '…', pairs: [before, after] }
level_from:
  field: http_status                   # a JSON path, or a field extracted by a transform
  map: { "5*": error, "4*": warn }     # no "*": other values keep the line's level
```

- A line `INFO … http_status=503` shows as **ERROR**. `e`, `>` and the error count see it.
- A line `ERROR … http_status=200` stays **ERROR** (see §2).
- A line without `http_status` keeps its level.

## 2. Rules

| Topic | Rule | Why |
|---|---|---|
| Where `field` is read | First as a JSON path, as in `fields` (literal key, then dotted walk; hidden keys included); otherwise as a field extracted by a transform. | The status is either a JSON key or inside the text. Hidden keys still count, because hiding is about display. |
| Map | Globs on the value, longest first, as for `regex`. | Same syntax as today. |
| No rule matches, or the field is absent | **The line keeps its level.** | A missing status must not erase a known level. |
| A rule matches | **The level can only go up**: the result is the more severe of the JSON level and the mapped level. An unknown level takes the mapped one. | Lowering would hide real errors: an ERROR logged during a request that answered 200 must stay visible to `e`. |
| Value types | Numbers and strings are compared as written (`500`, `"500"`). | Same as `stringify` today. |

**Decided (D-045):** "only raise" versus "override". Override is what `regex` does today, and with a `*` rule it can lower an ERROR to INFO. I recommend **only raise** for JSON. §4 covers what happens to `regex`.

## 3. Where it lives (architecture)

| Package | Change |
|---|---|
| `internal/config` | No new struct: `LevelFrom` already exists. Validation allows it with `decoder: json`: `field` is not empty, `map` has at least one rule, the globs are valid and the levels are known. The message `pattern and level_from are for the regex decoder` becomes `pattern is for the regex decoder`. |
| `internal/adapters/driven/logformat` | `Profile` gains `LevelField string` and `LevelRules []LevelRule` (existing type). The rule matching of `regex.go` moves to a shared `levelRules` helper. `JSONDecoder.Decode` applies it last, after the transforms, reading the value from `root` or from the extracted fields. |
| `internal/bootstrap` | The code building `LevelRules` from `level_from.map` is shared by the `json` and `regex` factories. |
| `internal/core`, TUI | **No change.** The level is a plain `LogEntry.Level`, so filters, `>` and the counts already work. |

The core does not change, the adapter stays generic, and the wiring stays in bootstrap.

## 4. Consistency with `decoder: regex`

Today, in `regex`, a matching rule **replaces** the level, and a value that matches no rule gives **UNKNOWN**. Two options:
- **(a) Leave `regex` as it is.** It has no real level group in practice (access logs), so both behaviours give the same result there. The docs explain the difference.
- **(b) Align `regex` on the JSON rules** (no match keeps the level; only raise). This is the same result for the example folders, since nginx has no level group and a `*` rule. Only a regex format with both a `level` group and `level_from` would change.

Recommendation: **(b)**, one rule for one key. It is recorded in `docs/DECISIONS.md`, and a test fixes the nginx example unchanged.

## 5. Cost

This is one `lookup` plus a few `path.Match` calls per line: well under 1 µs next to the 2.3 µs of the JSON decode. A benchmark in `logformat` measures it. No memory is kept per line.

## 6. Decisions to take

1. **Only raise** (recommended), or **override** like `regex` today.
2. **`regex`:** (b) align it (recommended), or (a) leave it.

## 7. Tests

- **`logformat`**:
  - INFO + 503 gives ERROR;
  - ERROR + 200 stays ERROR;
  - an unknown level gets the mapped one;
  - a field that is absent, or that matches no rule, keeps the level;
  - a value read from a JSON key, a nested path, a hidden key and an extracted field;
  - a numeric value;
  - a benchmark.
  - `regex`, if (b): the new rule, and the nginx cases unchanged.
- **`config`**: `level_from` accepted with json; errors (empty field, empty map, invalid glob, unknown level) with file:line:column.
- **`bootstrap`**: example folder test, where an INFO line of the MDC format with `http_status=503` draws as ERROR.
- **Demo**: some `order-orchestrator` requests answer 5xx while their line is logged at INFO or WARN. The end-to-end test checks that they are counted as errors.

## 8. Docs

- `docs/CONFIG.md` §8: `level_from` moves to the common keys of formats, with the rules of §2.
- `make schema`: the doc of `level_from` changes.
- `docs/DECISIONS.md` D-045.
- The example `mdc-json.yaml` gets `level_from` on `http_status`.

## 9. Delivery

1. The shared `levelRules` helper, and `regex` per decision 2, with tests.
2. Config validation and schema.
3. The JSON decoder, with tests and a benchmark.
4. Bootstrap wiring and the example folder.
5. The demo end to end, then docs and D-045.

Each step passes `go test ./...` and `golangci-lint run`.
