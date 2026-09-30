# M6 — Field transforms: context baked into a JSON field's text  (status: design)

Some logging stacks write their request context **inside the text** of a JSON field instead of in separate keys. A typical case is an MDC pattern of the form `<prefix> - <message> - <suffix>`:

```json
{"time":"2026-01-01T00:00:00Z","severity":"INFO","logger":"com.example.widgets.WidgetService",
 "message":"route=FAKE_ROUTE method=GET correlation-id=FAKE_CORR business_id= - Widget created - user_id=FAKE_USER x-forwarded-for= request_id=FAKE_REQ http_status= result= status_code= error_code= activity_id= activity_name= process_instance_id="}
```

`decoder: json` maps `message` to the whole string. The stream line is dense: up to 13 `key=` tokens around the real text, most of them empty. `hidden:` cannot help, because it works on JSON keys and not on part of a value. `decoder: regex` cannot help either: it would parse the whole line with one fragile pattern and lose the JSON extraction of the other fields.

This milestone adds a **field transform** to JSON formats. It is a regular expression applied to the value of one standard field after the JSON is decoded. It can:
- **A. strip** the value: the group named after the field becomes the new value;
- **B. extract** fields: other named groups become fields of the line, and `pairs` groups are split into `key=value` fields **without naming any key in the config**.

Both options use one key and one mechanism. Option A is a transform with only the field's own group.

## 1. What the user writes

```yaml
# formats/30-mdc.yaml
version: 1
decoder: json
fields:
  time:    time
  level:   severity
  logger:  logger
  thread:  thread
  message: message
transform:
  message:
    pattern: '^(?P<before>(?:[\w.-]+=\S*\s+)*)-\s+(?P<message>.*?)\s+-\s+(?P<after>(?:[\w.-]+=\S*\s*)*)$'
    pairs: [before, after]
layout: spring
```

The stream line becomes `… INFO  WidgetService : Widget created`. The context values become fields of the line:
- zoom shows them in its FIELDS section;
- text filters search them (`correlation-id=fake_corr`);
- layouts can draw them (`{field:route}`).

Empty values (`business_id=`) are dropped. The zoom raw view (`p`) keeps the original line untouched.

### Option A: strip only

```yaml
transform:
  message:
    pattern: '^(?:[\w.-]+=\S*\s+)*-\s+(?P<message>.*?)\s+-\s+(?:[\w.-]+=\S*\s*)*$'
```

Only the `message` group exists, so the prefix and suffix are removed from the line and from text search. They are still in the raw view. This is documented as a trade-off: use `pairs` to keep them searchable.

### Option B1: explicit groups

```yaml
transform:
  message:
    pattern: '^route=(?P<route>\S*)\s+method=(?P<method>\S*)\s+correlation-id=(?P<trace_id>\S*).*?-\s+(?P<message>.*?)\s+-\s+.*$'
```

A named group becomes a field. A group named like a **standard field** (`trace_id`, `logger`, `thread`, `app`, `pid`, `level`) fills that field, as in `decoder: regex`. For example, the correlation id can feed the trace view. The group should capture the **value** (`route=(?P<route>\S*)`), not `route=…`.

### Option B2: `pairs`, generic key=value

Each group listed in `pairs` is split on white space. Every token `key=value` becomes the field `key`, spelled as written (`correlation-id`, `x-forwarded-for`). This answers the "non-exhaustive key list" question: a service that adds an MDC key gets a new field without any config change.

## 2. Rules

| Topic | Rule | Why |
|---|---|---|
| Fields that can be transformed | `message`, `logger`, `thread`, `trace_id`, `app`, `pid`. Not `time`, `level` (parsed values) or `stack` (multi-line, large). | Keeps the pass on short text. |
| Required group | The pattern needs a group named after the transformed field. To keep the value, write `(?P<thread>.*)`. | One way to say "the new value". |
| No match | The field is left as it is and no field is added. This is never an error. | "Lines a format cannot parse keep their text." |
| Empty group or pair value | Not added. For a standard field, an empty group keeps the JSON value. | 13 empty `key=` would come back as 13 empty rows in zoom. The raw view keeps them. |
| Tokens of a `pairs` group without `=` | The whole group is kept as one field named after the group. | Nothing is dropped. |
| Standard field already set by the JSON | Other groups (not the transformed field's own) **fill a standard field only when it is empty**. | An explicit JSON key is more reliable than text. |
| Name clash with a JSON key | The JSON key wins, and the extracted value is in the raw view only. | Same as "repeated keys: the first occurrence wins". |
| Clash between groups or pairs | Named groups first, then `pairs` groups in list order. The first one wins. | Deterministic. |
| Several transforms | Applied in the order message, logger, thread, trace_id, app, pid. One transform does not see another's fields. | Deterministic, no chains. |
| `hidden` | Extracted fields go through `hidden` like any other field. A hidden one leaves the search and moves to zoom metadata. | One rule for every field. See §4 note. |
| Decoders | `json` only. `regex` already has named groups, and `plain` reads nothing. | Validation error otherwise. |

**Note on the feature request's `hidden:` list.** Extra fields are **never drawn on the stream** unless a layout column names them, so the extracted fields do not need to be hidden to get a compact line. Hiding them would *remove* them from text search. The documentation will say so.

## 3. Where it lives (architecture)

Nothing crosses the hexagon: this is a decoding concern, in the JSON decoder adapter.

| Package | Change |
|---|---|
| `internal/config` | `Format.Transform map[string]Transform` with `keys:"message,logger,thread,trace_id,app,pid"`. `Transform{Pattern string; Pairs []string}` (plain data). Validation in `validate.go` (§5). |
| `internal/bootstrap/logs.go` | The `json` factory compiles each pattern (`regexp.MustCompile`, already validated) into `logformat.Profile.Transforms`. |
| `internal/adapters/driven/logformat` | New `transform.go`: `type FieldTransform struct{ Field string; Pattern *regexp.Regexp; Pairs []string }` and `apply(e *domain.LogEntry, …)`. `JSONDecoder.Decode` calls it after the standard fields are read and before `rest()`. The group assignment of `RegexDecoder.Decode` moves to a shared helper (`setGroup`) used by both. |
| `internal/core/*` | **No change**: extracted values are plain `LogEntry.Fields` and standard fields. |
| `internal/adapters/driving/tui` | Rename the zoom section `KUBERNETES METADATA` to `HIDDEN FIELDS`. It already holds any hidden field, and with transforms it can hold non-Kubernetes ones. Golden files are updated. |
| `internal/archtest` | No new package, so no new rule. No application string in code: keys, patterns and examples stay in `examples/`. |

Decode order in `JSONDecoder.Decode`:
1. Parse and map the standard fields (as today).
2. **Transforms**: for each one, `FindStringSubmatchIndex` on the field value; set the field, the standard fields and the extra fields per §2. The extra fields go to a small map merged into `e.Fields` after `rest()`, and the JSON keys win.
3. `rest()` flattens the remaining JSON keys (as today), then the extracted fields are merged, with `hidden` applied to them.
4. `hiddenOf(raw)` (lazy zoom metadata) runs the transforms again, so hidden extracted fields appear in zoom. `hasHidden` accounts for them.

## 4. Performance

The transform runs on every line of the containers the format matches, on the ingestion hot path.
- Go's `regexp` runs in linear time: there is no catastrophic backtracking.
- Use `FindStringSubmatchIndex` and substrings of the value, with no copy per group. The `pairs` split uses `strings.Cut` on a loop, not `strings.Fields`.
- New benchmark `BenchmarkJSONTransform` next to the existing decoder benchmarks, with and without a transform, on the 13-key line. Target: less than 2× the plain JSON decode.
- The fuzz target `FuzzFastjsonMatchesEncodingJSON` is unchanged. A new fuzz target `FuzzTransform` checks for no panic and that the output is valid UTF-8.

## 5. Validation (errors with file and position)

- `transform` on a format whose decoder is not `json`: `transform is for the json decoder`.
- Key not in the list: handled by the `keys` tag (schema and validator).
- `transform.<f>` whose field has no `fields.<f>` mapping: `transform.<f>: fields.<f> is not mapped`.
- `pattern` empty or invalid: `invalid regular expression: …`.
- Missing group named after the field: `missing group (?P<message>…)`.
- `pairs` entry that is not a group of `pattern`: `"x" is not a group of pattern`.
- A group named `time` or `stack`: `time and stack cannot be set by a transform`.

## 6. Documentation and schema

- `docs/CONFIG.md` §8, under `decoder: json`: a "`transform`: context inside a field" part with the table of §2, options A, B1 and B2, and the search trade-off of option A.
- `make schema` regenerates `docs/schema/format.schema.json`.
- `examples/config-mdc/formats/…yaml`, or a second file in `examples/config/formats/` matched on a demo repo, shows B2.
- `docs/DECISIONS.md`: **D-041 Field transforms**, which records the choices of §2. The main ones are one key for strip and extract, `pairs` instead of a key list, empty values dropped, JSON keys win, and json decoder only.

## 7. Demo

The demo generator (`adapters/driven/demo`) gets one repository whose messages carry the `prefix - message - suffix` context, with mostly empty values and a few set. `examples/config` gets a format matched on that repo with the B2 transform. `./bin/huginn --demo` then shows the compact line, the fields in zoom and `correlation-id=` search.

## 8. Tests

| Level | Tests |
|---|---|
| `logformat` (table-driven) | Strip (A); explicit groups (B1), including a standard field filled only when empty; `pairs` (B2) with empty values and tokens without `=`; no match; name clash with a JSON key; transform on `logger`; message containing ` - `; hidden extracted field (present in `hiddenOf`, absent from `Fields`); invalid UTF-8. |
| `config` | Every validation error of §5, with positions; a valid transform loads. |
| `bootstrap` | A format folder with a transform produces a decoder applying it. |
| `domain` (existing) | `searchText` finds an extracted pair (`correlation-id=…`). |
| `tui` golden | The zoom section rename, and a zoom of a transformed entry (`-update`, then review the diff). |
| Benchmark / fuzz | See §4. |

## 9. Delivery order

1. Config struct, validation, schema, and tests.
2. `logformat`: transform, shared group helper, and tests, benchmark and fuzz.
3. Bootstrap wiring.
4. Zoom rename, and golden files.
5. Demo repo, example format, CONFIG.md, and D-041.

Each step passes `go test ./...` and `golangci-lint run`.

## 10. Non-goals

- Message classification, or collapsing noisy periodic loggers.
- Any change to `hidden` or `levels` for top-level JSON keys.
- Transforms on `decoder: regex` or `plain`, or on `time`, `level` and `stack`.
- Chained transforms, or transforms on extra (non-standard) fields. Both are possible later by extending the `keys` list, and are not needed now.
- Values containing spaces inside `pairs` (`key=a b`). Use explicit groups (B1) for those.
