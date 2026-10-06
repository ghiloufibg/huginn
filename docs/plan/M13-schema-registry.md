# M13 — Schema Registry records decoded (Avro, JSON Schema)  (status: done)

## 0. In one paragraph
Records written by Confluent serializers start with `0x00` and a 4-byte schema id. Today the Kafka screens recognise this framing and show `schema <id>, N B` with a hex dump (M11 §5). This milestone **decodes them**, as Confluent's Java `KafkaAvroDeserializer` and `KafkaJsonSchemaDeserializer` do: the writer schema is read from the Schema Registry by id, and the record is shown as JSON. Huginn stays read only: there is no serializer, the registry is only ever read (GET), and nothing about it exists unless a Kafka profile names a registry.

**Scope rule.** Avro and JSON Schema values and keys, generic decoding with the writer schema. Everything else is in §9.

## 1. The Java deserializer, mapped

| Java (Confluent) | Huginn |
|---|---|
| Wire format: magic `0x00`, schema id (4 bytes, big endian), payload | Same; detected by `domain.ClassifyPayload` (`PayloadFramed`) |
| Writer schema by id: `GET /schemas/ids/{id}`, cached forever | Same: ids are immutable; bounded LRU cache, one fetch per id at a time |
| Schema references: `GET /subjects/{s}/versions/{v}` | Same, recursively (depth ≤ 10, cycles refused) |
| `schemaType` `AVRO` (default), `JSON`, `PROTOBUF` | Avro and JSON decoded; Protobuf shown as today, "not decoded" (§9) |
| `specific.avro.reader` (generated classes, reader schema) | Not applicable: generic decoding with the writer schema |
| `key.deserializer` / `value.deserializer` | `decode: [key, value]` (both by default) |
| `basic.auth.user.info`, `bearer.auth.token`, `schema.registry.ssl.truststore.*` | `basic_auth`, `bearer_token`, `tls.ca` / `ca_password` (the Kafka truststore loader) |
| Serializer: `auto.register.schemas`, `use.latest.version`, subject name strategies | Not applicable: Huginn never produces, and the registry client refuses any method but GET |

## 2. Settled choices
1. **Readable logical types** (not the exact `kafka-avro-console-consumer` text): `decimal` → exact decimal string (`"12.50"`); `timestamp-millis`/`-micros` and `local-timestamp-*` → RFC 3339 (`"2026-10-06T19:14:02.113Z"`); `date` → `"2026-10-06"`; `time-millis`/`-micros` → `"19:14:02.113"`; `uuid` → its string; `duration` → `{"months":1,"days":2,"millis":3}`. Other `bytes` and `fixed` → `"0x…"` hex. Unions keep Avro's JSON encoding (`{"string":"x"}`, `null` plain) so the branch taken stays visible. Fields keep the schema's order.
2. **JSON Schema in the same version**: its payload after the 5-byte header is already JSON; the schema is fetched only to name it (title or `$id`) and is not used to validate.

## 3. Configuration (`kafka/<name>.yaml`)

```yaml
schema_registry:
  url: ${SCHEMA_REGISTRY_URL}
  basic_auth:                        # optional
    username: ${SR_USERNAME}
    password: ${SR_PASSWORD}
  # bearer_token: env:SR_TOKEN       # optional, instead of basic_auth
  tls:                               # optional, as connection.tls
    ca: "{repo_dir}/src/main/resources/truststore.p12"
    ca_password: ${SR_TRUSTSTORE_PASSWORD}
  decode: [key, value]               # default both
  timeout: 10s                       # one registry request
```

- Same references as `connection` (`${KEY}`, `{repo_dir}`, `env:VAR`), overridable under `repos.<name>`. Credentials stay `domain.Secret` until the adapter.
- Validation: `url` is `http(s)://…`; `basic_auth` and `bearer_token` exclude each other; `decode` holds `key` and/or `value`; `timeout` is a duration.
- Without the section, everything works as today. `make schema`, `docs/CONFIG.md` §10 (keys, errors table), example in `examples/config/kafka/demo.yaml`.

## 4. Structure

```
core/domain    SchemaRef{ID, Format ("avro"|"json"), Name, Err} on KafkaRecord, for key and value
               SchemaRegistryConn (url, auth, CA certs, timeout), resolved from the profile
core/ports     SchemaDecoder: Decode(ctx, framed []byte) ([]byte, domain.SchemaRef, error)
               (the topic and key/value flag only choose subjects, a serializer's concern)
               SchemaDecoderFactory: Open(ctx, domain.SchemaRegistryConn) (SchemaDecoder, error)
               portstest: fake registry decoder + contract suite
core/app       kafkaSession: decode framed keys/values, then truncate (max_value_bytes)
adapters/driven/schemaregistry   HTTP client (GET only), cache, references; selects the decoder by schemaType
adapters/driven/avrojson         writer schema + Avro binary → ordered JSON (§5)
bootstrap      wires them only when a profile has schema_registry
archtest       rules for the two packages
```

- **Decode before truncating**: in the app's read path, right before `domain.TruncateRecord` (`internal/core/app/kafka.go`): a truncated payload cannot be decoded. `max_value_bytes` then applies to the JSON, and `RecordBuffer` counts its bytes.
- **The decoded JSON replaces the bytes**; `SchemaRef` says what they were. The screens classify the value as JSON, so preview, zoom, filters (`key=…`, text) and copy work unchanged. The original bytes are not kept (memory); a key re-reads the topic undecoded (§6).
- **Failures never hide a record**: the record keeps its bytes and `SchemaRef.Err` says why (`registry unreachable`, `unauthorized`, `unknown schema id`, `invalid avro at byte 42`, `protobuf not decoded`); one session notice per distinct registry error, not one per record.

## 5. The adapters
- **schemaregistry**: `GET /schemas/ids/{id}` (+ references); LRU of 1 000 schemas; one fetch per id at a time (others wait for it); failures cached 30 s (no hammering on 401/404); responses capped at 1 MiB; a `RoundTripper` that refuses any method but GET (tested, like the Kafka request guard of D-057); never logs payloads or credentials.
- **avrojson**: schemas parsed with `hamba/avro/v2` (pure Go, schema cache resolves references). A small transcoder walks the parsed schema and reads the binary with hamba's reader, writing JSON directly: field order kept, no `map[string]any`, logical types as in §2. Every length prefix is capped by the bytes left and nesting depth by 64, so a malformed or hostile record cannot allocate gigabytes or recurse without end; it is fuzzed and must never panic. `linkedin/goavro` was set aside: no schema references.

## 6. Screens and CLI
- **Records list**: `avro 7 · {"orderId":"ord_8f91a2","amount":"12.50"}`; JSON Schema `json 9 · {…}`.
- **Zoom**: `value: avro, schema 7 (com.acme.OrderCreated)` above the indented JSON; on failure `schema 7: registry unreachable` above the hex dump.
- **Key** (new, Kafka records screen): show undecoded / decoded, re-reading the topic, as `M` does for muted loggers (D-062).
- **Help** lists the registry state: reachable, schemas cached, failures.
- **CLI**: `huginn kafka read` prints decoded JSON by default (works with `jq`); `--no-decode` keeps the bytes. `huginn kafka check` also checks the registry (`GET /schemas/types`) and its credentials.

## 7. Performance
- Cache hit: no I/O; Avro decoding about 1–5 µs per record.
- History (up to 120 000 records): decoded in parallel chunks (`GOMAXPROCS` workers) on the read goroutine, never the UI's; live batches decoded inline.
- Cache miss: one GET per new id, bounded by `timeout`; the batch waits, the screen does not.
- New benchmark `BenchmarkKafkaDecodeAvro` (100 000 framed records, 20 schemas): target < 5 µs and a fixed number of allocations per record.

## 8. Steps (each green: `go test -race ./...`, `golangci-lint run`)
1. **S0 — config and ports** (done): `schema_registry` in profiles, validation, schema, CONFIG.md; domain types, ports, fakes, contract suite.
2. **S1 — avrojson** (done: `schemaregistry/avrojson`, a sub-package of the registry adapter since adapters do not import each other; hamba/avro confined to it by archtest; a record using every type decodes in about 4.5 µs, 12 allocations; 2.8 million fuzzed inputs without a failure): transcoder, golden fixtures (every type, unions, logical types, nesting, arrays, maps, references), fuzzing, benchmark.
3. **S2 — schemaregistry** (done; it passes the S0 contract suite over HTTP): HTTP client against `httptest.Server`: cache, one fetch per id, negative cache, GET guard, basic/bearer auth, TLS, references; JSON Schema.
4. **S3 — app and screens** (done; the undecoded key is `D`; help lists the key, the registry state is in the status bar notices): decode before truncation, failures as `SchemaRef.Err`, notices, list/zoom/help, the undecoded key, golden files.
5. **S4 — CLI and demo** (done; `--raw` already meant "values only, for jq" and now prints the decoded JSON, so the undecoded bytes are `--no-decode`; the demo registry is the real client over an in-memory transport): `kafka read`/`check`; the demo's framed records become real Avro and JSON Schema with a fake registry, so `--demo` shows the feature; README; D-067.

## 9. Not in this version (added only on a real need)
Protobuf (message indexes after the header, descriptor decoding); validating JSON Schema payloads; a reader schema or schema evolution views; subject/version browsing; registry contexts and multi-registry profiles; OAuth for the registry; showing the schema text in zoom.
