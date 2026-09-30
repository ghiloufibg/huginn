# M6 — Field transforms: context baked into a JSON field's text  (status: done)

Some logging stacks write their request context **inside the text** of a JSON field instead of in separate keys. A typical case is an MDC pattern of the form `<prefix> - <message> - <suffix>`:

```json
{"time":"2026-01-01T00:00:00Z","severity":"INFO","logger":"com.example.widgets.WidgetService",
 "message":"route=FAKE_ROUTE method=GET correlation-id=FAKE_CORR business_id= - Widget created - user_id=FAKE_USER x-forwarded-for= request_id=FAKE_REQ http_status= result= status_code= error_code= activity_id= activity_name= process_instance_id="}
```

`decoder: json` maps `message` to the whole string. The stream line is dense: up to 13 `key=` tokens around the real text, most of them empty. `hidden:` cannot help, because it works on JSON keys and not on part of a value. `decoder: regex` cannot help either: it would parse the whole line with one fragile pattern and lose the JSON extraction of the other fields.

The feature is delivered in **two milestones**. Each one ships on its own, passes `go test ./...` and `golangci-lint run`, and is usable by itself:

| Milestone | Option | What it gives |
|---|---|---|
| **M6.1** | A: strip | A regular expression keeps one part of a field's value. The stream line becomes compact. |
| **M6.2** | B: extract | The other parts become fields of the line: searchable, drawable in layouts, shown in zoom. `pairs` splits `key=value` text without listing any key. |

M6.2 **extends** M6.1's config key. It does not replace it: a folder written for M6.1 behaves the same after M6.2.

---

## Common ground (both milestones)

### Config surface

A new optional key in `formats/*.yaml`, for the `json` decoder only:

```yaml
transform:
  <standard field>:
    pattern: '<Go regular expression>'
    pairs: [<group>, …]        # M6.2 only
```

- Field keys: `message`, `logger`, `thread`, `trace_id`, `app`, `pid`. `time` and `level` are left out because they are parsed values; `stack` is left out because it is multi-line and large.
- The pattern **must** contain a group named after the field: its match becomes the field's new value.
- **Forward-compatibility rule:** in M6.1, any other named group, or `pairs`, is a validation error. M6.2 then gives them a meaning without changing the behaviour of any folder that was valid in M6.1.

### Where it lives (architecture)

This is a decoding concern, so it lives in the JSON decoder adapter. Nothing crosses the hexagon, and `internal/core` does not change.

| Package | Role |
|---|---|
| `internal/config` | `Format.Transform map[string]Transform` (`keys:"message,logger,thread,trace_id,app,pid"`), `Transform` struct (plain data), validation |
| `internal/bootstrap/logs.go` | The `json` factory compiles each pattern into `logformat.Profile.Transforms`. The patterns are already validated. |
| `internal/adapters/driven/logformat` | New `transform.go`, called by `JSONDecoder.Decode` after the standard fields are read |
| `internal/archtest` | No new package, so no new rule. Keys and patterns stay in `examples/`, never in code. |

### Shared rules

- **No match:** the field is left unchanged and no error is raised ("lines a format cannot parse keep their text").
- **Nothing is lost:** zoom's raw view (`p`) always shows the original line.
- **Several transforms** apply in a fixed order: message, logger, thread, trace_id, app, pid. Each one reads the JSON value of its own field, so there are no chains.
- **Only JSON lines** are transformed. Non-JSON lines (plain fallback) are not.
- **Performance:** Go's `regexp` runs in linear time, so there is no catastrophic backtracking. Use `FindStringSubmatchIndex` and take substrings of the value, with no copy per group.

---

## M6.1 — Option A: strip

### What the user writes and sees

```yaml
transform:
  message:
    pattern: '^(?:[\w.-]+=\S*\s+)*-\s+(?P<message>.*?)\s+-\s+(?:[\w.-]+=\S*\s*)*$'
```

- Stream: `… INFO  WidgetService : Widget created`.
- Zoom: the transformed message on the first line; `p` shows the raw JSON with the full text.
- Text search now works on the **transformed** value, so the prefix and suffix are no longer found by `/`. This trade-off is documented, and M6.2 lifts it.
- A message containing ` - ` is kept whole thanks to the lazy group: `Widget created - successfully` stays intact.

### Changes

| Package | Change |
|---|---|
| `config/config.go` | `Transform{Pattern string}` and `Format.Transform` |
| `config/validate.go` | Errors listed in "Validation (M6.1)" below |
| `bootstrap/logs.go` | Compiles the patterns into `Profile.Transforms []logformat.FieldTransform{Field, Pattern}` |
| `logformat/transform.go` | `strip(e *domain.LogEntry, t FieldTransform)`: sets the field to its group when the pattern matches. It uses a small `field(e, name) *string` accessor. |
| `logformat/json.go` | `Decode` calls the transforms after the standard fields, before `rest()` |

### Validation (M6.1)

Each error is reported with its file and position:
- `transform` on a non-json decoder: `transform is for the json decoder`.
- Key not in the list: handled by the `keys` tag.
- `transform.<f>` whose field has no `fields.<f>` mapping: `fields.<f> is not mapped`.
- Empty or invalid pattern: `invalid regular expression: …`.
- No group named after the field: `missing group (?P<message>…)`.
- Any other named group: `named group "x": only (?P<message>…) is allowed; use (?:…) for other parts`.
- `pairs` is present: this falls out of the unknown-key check, since the struct does not have it yet.

### Tests (M6.1)

- `logformat`, table-driven:
  - strip on `message`;
  - strip on `logger`;
  - no match;
  - message containing ` - `;
  - empty group, which gives an empty message (allowed, since the pattern says so);
  - invalid UTF-8;
  - a line without the field;
  - a non-JSON line.
- `config`: every error above, with its position, plus a valid folder.
- `bootstrap`: a folder with a transform yields a decoder that applies it.
- Benchmark `BenchmarkJSONTransform` on the 13-key line, with and without a transform. Measured: 2.3 µs → 7.6 µs per line, all in the regexp engine (D-041). The initial 2× target is not reachable with Go's `regexp` on this pattern.
- Fuzz `FuzzTransform`: no panic, and the output is valid UTF-8.

### Docs, demo, decisions (M6.1)

- `docs/CONFIG.md` §8: a new "`transform`: keep part of a field" subsection, including the search trade-off.
- `make schema`.
- Demo: a repository whose messages carry the `prefix - message - suffix` context, with a format in `examples/config/formats/` matched on it.
- `docs/DECISIONS.md` **D-041 Field transforms (strip)**:
  - json only;
  - the list of fields;
  - the forward-compatibility rule;
  - no match leaves the value unchanged.

### Delivery order (M6.1)

1. Config, validation and schema, with tests.
2. The `logformat` strip, with tests, benchmark and fuzz.
3. Bootstrap wiring.
4. Demo, example, CONFIG.md and D-041.

---

## M6.2 — Option B: extract

### What the user writes and sees

**B1: explicit groups.** Named groups other than the field's own become fields:

```yaml
transform:
  message:
    pattern: '^route=(?P<route>\S*)\s+method=(?P<method>\S*)\s+correlation-id=(?P<trace_id>\S*).*?-\s+(?P<message>.*?)\s+-\s+.*$'
```

**B2: `pairs`, generic key=value.** Each listed group is split on white space, and each `key=value` token becomes the field `key`, spelled as written:

```yaml
transform:
  message:
    pattern: '^(?P<before>(?:[\w.-]+=\S*\s+)*)-\s+(?P<message>.*?)\s+-\s+(?P<after>(?:[\w.-]+=\S*\s*)*)$'
    pairs: [before, after]
```

The result:
- The stream stays compact. Extra fields are **never drawn** unless a layout column names them (`{field:route}`).
- Zoom's FIELDS section lists `route`, `correlation-id`, `user_id`, …
- Text search finds them: `correlation-id=fake_corr`. This lifts the M6.1 trade-off.
- A new MDC key in the logs becomes a field **without any config change**. This answers the request's "non-exhaustive key list" question.

### Rules

| Topic | Rule | Why |
|---|---|---|
| Group capture | The group captures the **value**, `route=(?P<route>\S*)`, not `route=…`. | Documented, with an example. |
| Group named like a standard field (`trace_id`, `logger`, `thread`, `app`, `pid`, `level`) | Fills that field **only when the JSON left it empty**; `level` is parsed with `levels`. `time` and `stack` groups are rejected. | An explicit JSON key is more reliable than text. The correlation id can feed the trace view. |
| Empty group or pair value (`user_id=`) | Not added. | Otherwise 13 empty rows would appear in zoom. The raw view keeps the text. |
| `pairs` group with a token without `=` | The whole group is kept as one field named after the group. | Nothing is dropped. |
| `pairs` group itself | Not added as a field when it splits cleanly. | Avoids duplicating its pairs. |
| Name clash with a JSON key | The JSON key wins. | Same as "repeated keys: the first occurrence wins". |
| Clash between groups and pairs | Named groups first, then `pairs` groups in list order. The first one wins. | Deterministic. |
| `hidden` | Extracted fields go through `hidden` like any other field. | One rule. The docs say that hiding them removes them from search, and that it is not needed for a compact stream line. |

### Changes

| Package | Change |
|---|---|
| `config` | `Transform.Pairs []string`. Validation: other named groups are now allowed; `time`/`stack` groups are rejected; each `pairs` entry must be a group of `pattern` and must not be the field's own group. |
| `logformat/transform.go` | `strip` becomes `apply`. It returns the extra fields and fills the empty standard fields. It adds `splitPairs(group string) (map[string]string, ok bool)`, a `strings.Cut` loop with no `strings.Fields` allocation. |
| `logformat/regex.go` | The group → standard field switch moves into a helper, `setGroup(e, name, v, levelAliases)`, shared with the transform. The regex decoder's behaviour is unchanged. |
| `logformat/json.go` | Extracted fields are merged into `e.Fields` after `rest()`, with JSON keys winning and `hidden` applied. `hiddenOf(raw)`, the lazy zoom metadata, runs the transforms again so hidden extracted fields appear. `hasHidden` accounts for them. |
| `tui/zoom.go` | The zoom section `KUBERNETES METADATA` becomes `HIDDEN FIELDS`, since it can now hold non-Kubernetes fields. Golden files are updated with `-update`, then the diff is reviewed. |
| `core/*` | No change. Extracted values are ordinary `LogEntry.Fields` and standard fields, so `searchText`, layouts and zoom already handle them. |

### Tests (M6.2)

- `logformat`:
  - B1 groups;
  - a standard field filled only when empty;
  - `level` from a group;
  - B2 pairs with empty values;
  - tokens without `=`;
  - a clash with a JSON key;
  - a clash between groups and pairs;
  - a hidden extracted field (in `hiddenOf`, not in `Fields`);
  - the regex decoder's existing tests, unchanged after the helper refactor.
- `config`: the new errors, with positions. A folder valid in M6.1 still loads identically.
- `domain`, existing `searchText`: an extracted pair is found.
- `tui` golden: the section rename, and the zoom of an entry with extracted fields.
- The benchmark and fuzz tests are extended to `pairs`.

### Docs, demo, decisions (M6.2)

- `docs/CONFIG.md` §8: the "`transform`" subsection gains B1, B2, the rules table, and the note on `hidden`.
- `make schema`.
- Demo: the M6.1 example format switches to B2, so `--demo` shows the fields in zoom and `correlation-id=` search.
- `docs/DECISIONS.md` **D-042 Field transforms (extract)**:
  - `pairs` instead of a key list;
  - empty values dropped;
  - JSON keys win;
  - standard fields filled only when empty;
  - the zoom section rename.

### Delivery order (M6.2)

1. The `setGroup` helper refactor. The regex decoder's tests must be green, with no behaviour change.
2. Config `pairs` and the relaxed validation, then `make schema`.
3. `logformat` extract: B1, then B2, then hidden handling.
4. The zoom rename and golden files.
5. Demo, CONFIG.md and D-042.

---

## Non-goals (both milestones)

- Message classification, or collapsing noisy periodic loggers.
- Any change to `hidden` or `levels` for top-level JSON keys.
- Transforms on `decoder: regex` or `plain`; on `time`, `level` or `stack`; or on extra (non-standard) fields.
- Chained transforms.
- Values containing spaces inside `pairs` (`key=a b`). Use explicit groups (B1) for those.
