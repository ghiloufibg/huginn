# M5 — Kafka topics, read only  (status: done: K0 to K4)

## 0. In one paragraph
Some services are debugged today with a separate script: it reads the repository's `config.env` files (one encrypted with sops), connects to Kafka with the application's SASL account and truststore, assigns partitions by hand (no consumer group) and prints the last records of a topic. This milestone does **the same thing inside Huginn**: on a service that has Kafka settings, `M` opens a screen listing its topics and their records, read only, with no effect on the cluster or the pods. Nothing else. Every application detail (paths, key names, topics) is in the user's config folder; the code holds the mechanism only (D-030).

**Scope rule.** The first version does what the script does, made safe, bounded and tested. Anything beyond is listed in §10 and added only when a real need appears.

## 1. What the user sees

### Services screen
- A repository with Kafka settings in the current environment shows a `K` marker. `M` (action `kafka`, remappable in `ui.yaml`) opens its Kafka screen; on other rows the key does nothing and is not in the key bar.
- Nothing is read, decrypted or connected before `M`. Leaving the screen (`esc`), switching environment or quitting closes every connection.

### Kafka screen

```
 <repo> · rec · Kafka (read only)                                   brokers 3/3  uncommitted
┌ TOPICS ─────────────────────┐┌ <topic-a> · 12 partitions · last 100 per partition ──────────┐
│ CONSUMES                    ││ 10:42:01.113  p3  #884213  key=k-7781  {"id":"k-7781","st…  │
│ ▸ <topic-a>             12p ││ 10:42:01.540  p7  #120044  key=k-7782  {"id":"k-7782","st…  │
│ PRODUCES                    ││ 10:42:02.002  p3  #884214  key=∅       binary 88 B          │
│   <topic-b>              6p ││                                                             │
│ TOPICS                      ││                                                             │
│   <topic-c>              3p ││                                                             │
└─────────────────────────────┘└─────────────────────────────────────────────────────────────┘
 enter zoom  / filter  1-7 window  0 tail  f follow  i isolation  esc back
```

- **Topics**: `CONSUMES` / `PRODUCES` when the config gives a direction, `TOPICS` otherwise (directions are optional). Each shows its partition count, or a short reason it cannot be read (§6).
- **Records**, merged across partitions by timestamp: time, partition, offset, key, one-line value preview.
- **Zoom** (`enter`): headers, key, timestamp and its type, partition, offset, value pretty-printed (JSON indented, text wrapped, hex dump for binary).
- **Same keys as the logs screen**: `0` tail (last N records per partition, the default), `1`…`7` windows (records since that time), `f` follow live, `space` pause, `/` filter (text, `key=…`, `partition=…`, `header.<name>=…`), `o` order, `ctrl+y` copy of the value. No `n`/`N`: the filter hides the other records, there is no match to jump to. Plus `i`: isolation `read_uncommitted` (default, like the script and Spring Kafka) ↔ `read_committed`.
- **Status bar**: records loaded, dropped by the buffer limit, truncated values, live/paused/stopped.
- **Production environment**: the red banner, as on every screen.

## 2. Safety: as if Huginn were not there

### 2.1 No consumer group, ever
- Partitions are assigned by hand (franz-go `ConsumePartitions`), never with a group id: no JoinGroup / SyncGroup / Heartbeat / LeaveGroup, so **no rebalance** of the pods' group; no OffsetCommit, so **their offsets never move**. Positions live in Huginn's memory only.
- No config key can set a group id; the field does not exist.
- Reading does not remove records from Kafka: other readers are unaffected.

### 2.2 Allow-list of Kafka requests, enforced twice
1. **Static**: `forbidigo` and `archtest` forbid, in `adapters/driven/kafka`, `kgo.ConsumerGroup`, `CommitOffsets`, `CommitRecords`, `Produce*`, transactions, `AllowAutoTopicCreation` and the `kadm` package.
2. **Runtime guard, before the bytes leave**: Huginn dials the brokers itself (`kgo.Dialer`); each connection is wrapped, above TLS, by a guard that parses request frames (4-byte size, int16 API key, tracking frames split or joined across writes) and accepts only:

   | Key | Request |
   |---|---|
   | 18 | ApiVersions |
   | 3 | Metadata (auto-creation off) |
   | 2 | ListOffsets |
   | 1 | Fetch |
   | 17, 36 | SaslHandshake, SaslAuthenticate |
   | 23 | OffsetForLeaderEpoch (read only: the client checks its position after a leader change) |

   Any other frame is not written; the guard closes the client and the screen shows `internal error: refused Kafka request <key>`. The diagnostic log records it.
3. **Tests** (§8): the guard is unit-tested and fuzzed; the contract suite against `kfake` asserts the broker received nothing outside the list, in every scenario.

### 2.3 Bounded load on the brokers
- Never from the beginning of a topic: tail (`kafka.tail_records` per partition) or a window decides the start.
- Live fetching only after `f`; `space` pauses fetching (`PauseFetchPartitions`), not just the view; history done + not following = no more fetch requests.
- Caps: `fetch_max_bytes`, `partition_fetch_max_bytes` (§4.5). Client id `huginn` (configurable) so the Kafka team can identify it.
- The credentials are usually the application's: per-user broker quotas are shared with its pods. With the caps above and short debugging sessions the effect is negligible; it is documented in `docs/CONFIG.md`.

### 2.4 Secrets
- Decrypted files and every credential stay in memory as `domain.Secret`, revealed only inside the Kafka adapter and the truststore loader. Never written to disk, never in the diagnostic log, never in an error message (errors name the **key** and the **file**, never a value).
- sops runs as today (D-003): `sops --decrypt` with output captured in memory, once per file per screen; the buffer is cleared after parsing.
- Truststores are converted in memory (no `openssl`, no temporary file).
- Copy (`ctrl+y`) and save (`ctrl+s`) are explicit actions on visible records only.

### 2.5 Nothing on the cluster side
No pod, no port-forward, no Kubernetes call for this feature, no change to any file of the repository. Brokers are reached directly from the workstation (VPN), as the script does.

### 2.6 Absent means absent
Without `kafka/` in the config folder, bootstrap builds no Kafka component, the TUI binds no Kafka action, no file is checked. A bootstrap test enforces it.

## 3. How the screen opens
1. **Match** (cheap, at services screen time): the first profile of `kafka/` whose `match` accepts the repository; its `match.files` checked with `stat` only, once per environment and repository, cached. Result: `K` marker or not.
2. **Resolve** (on `M`, off the UI goroutine): replace `{env}`, `{repo}`, `{repo_dir}`, `{vars}`; read and merge `sources` (sops for encrypted ones); build the topic list (listed, then discovered); resolve each topic's credentials and truststore; group topics by identical connection.
3. **Connect**: one franz-go client per connection group, through the guarded dialer; Metadata for its topics only; partition counts shown.
4. **Read** (on topic selection): ListOffsets (latest, earliest, or by timestamp for a window), start = `max(earliest, latest − tail_records)` per partition or the window's offsets; Fetch until the end offsets seen at start (history done), then stop or follow.
5. **Deliver**: records merged by timestamp, batches at most ~30 per second to the TUI, as log batches.
6. **Close**: on `esc`, environment switch, quit or a different topic, the context is cancelled and clients are closed; the next topic reuses the connection group's client.

Each step that fails stops only what depends on it and says why (§6).

## 4. Configuration

```
acme-huginn/
├── huginn.yaml        + optional kafka: section (§4.5)
└── kafka/             optional, one profile per file
    └── <name>.yaml
```

Same rules as the rest of the folder: `version: 1`, strict decoding, positions in errors, profile named after its file, files tried **in name order**, first match wins (as `formats/`). Huginn has **no default** path, file name, key name, mechanism or topic.

### 4.1 A complete profile

```yaml
# yaml-language-server: $schema=../../docs/schema/kafka.schema.json
version: 1

match:
  repos: ["*"]
  files: ["{repo_dir}/<manifests-dir>/overlays/{env}/<secret-dir>/<file>.env"]

sources:                                   # merged in order, the last one wins
  - { file: "{repo_dir}/<manifests-dir>/base/<file>.env" }
  - { file: "{repo_dir}/<manifests-dir>/overlays/{env}/<file>.env", optional: true }
  - { file: "{repo_dir}/<manifests-dir>/overlays/{env}/<secret-dir>/<file>.env", sops: true }

vars: { account: <DEFAULT_ACCOUNT> }

connection:
  bootstrap: ${<BOOTSTRAP_KEY>}
  security: ${<PROTOCOL_KEY>:-plaintext}
  sasl:
    mechanism: scram-sha-512
    username: ${{account}_<USER_SUFFIX>}
    password: ${{account}_<PASSWORD_SUFFIX>}
  tls:
    ca: "{repo_dir}/<resources-dir>/<truststore>.p12"
    ca_password: ${{account}_<TRUSTSTORE_PASSWORD_SUFFIX>:-<default>}

topics:
  discover: ["<TOPIC_KEY_PREFIX>*"]

repos:
  <repo-a>:
    vars: { account: <ACCOUNT_A> }
    topics:
      consume: [${<TOPIC_KEY_1>}]
      produce:
        - { name: ${<TOPIC_KEY_2>}, vars: { account: <ACCOUNT_B> } }
  <repo-b>:
    connection:
      tls: { ca: "{repo_dir}/<other-dir>/<truststore>.p12" }
```

Every `<…>` is the user's. The smallest valid profile is a literal `bootstrap`, `security: plaintext` and one topic.

### 4.2 Keys

**`match`**
| Key | Meaning |
|---|---|
| `repos` | Repository globs. Empty: any. Repositories under `repos:` are accepted implicitly. |
| `files` | Path globs that must each match an existing file (stat only). This is what limits the marker to repositories with Kafka settings in the current environment. |

The environment is the one Huginn runs on (`huginn rec`, `-e`, `HUGINN_ENV`, `ctrl+e`), written `{env}`.

**`sources`** (optional list, merged in order, a later key replaces an earlier one)
| Key | Meaning |
|---|---|
| `file` | Path to a dotenv file (`KEY=VALUE`, comments and blank lines ignored, optional `export`, single or double quotes removed). Glob allowed, exactly one match. |
| `sops` | Decrypt with sops in memory first. |
| `optional` | A missing file is skipped (an overlay that does not exist); otherwise it is an error. |

**`vars`**: free variables written `{name}` anywhere in the profile, overridden per repository and per topic. Lower-case letters, digits, `_`; `env`, `repo`, `repo_dir` are reserved.

**`connection`**
| Key | Req. | Meaning |
|---|---|---|
| `bootstrap` | yes | `host:port` list, comma separated. |
| `security` | yes | `plaintext`, `ssl`, `sasl_plaintext`, `sasl_ssl` (case ignored). |
| `sasl.mechanism` | with `sasl_*` | `plain`, `scram-sha-256`, `scram-sha-512`. |
| `sasl.username`, `sasl.password` | with `sasl_*` | |
| `tls.ca` | | Truststore: PEM (`.pem`, `.crt`, `.cer`) or PKCS12 (`.p12`, `.pfx`), by extension. Glob allowed, one match. Without it, system roots. PKCS12: Java truststores and keystores; a certificate-only file made by openssl without Java's trust attribute is refused with the command converting it to PEM. |
| `tls.ca_password` | | Password of a PKCS12 truststore. |

**`topics`**
| Key | Meaning |
|---|---|
| `consume`, `produce` | Topics with a direction. |
| `list` | Topics without a direction. |
| `discover` | Key globs over the merged sources: each value is a topic (comma-separated values are split). Already listed topics are not repeated. |

A topic is a name or `{name, vars}`; `vars` override the profile's for that topic (another SASL account). Each distinct resolved connection gets its own client.

**`repos`**: map from repository name to `path` (folder when not `repos_root/<repo>`), `vars`, `connection`, `topics` (added), `sources` (appended), `enabled: false`. Merged key by key over the profile.

### 4.3 Values
- Literal, `${KEY}` or `${KEY:-default}` (from the merged sources), `env:VAR` (from the process environment, for personal credentials).
- `{…}` placeholders are replaced first, then `${…}`: `${{account}_PASSWORD}` reads `ORDERS_PASSWORD` when `account: ORDERS`.
- Paths: absolute, `~/…`, or relative to the config folder; usually built from `{repo_dir}` (`huginn.yaml` `repos_root` + repository name, or `repos.<repo>.path`).
- YAML: a value starting with `{` and any reference inside `[ ]` or `{ }` must be quoted (`consume: ["${TOPIC}"]`); `$$` is a literal `$`.

### 4.4 Validation
At load time (exit 2 with every problem and its position, like the rest of the folder): strict keys, enums, placeholder names known, `${…}` syntax, globs, `sasl` present with `sasl_*` and absent otherwise, `{repo_dir}` used without `repos_root` nor `path`, a topic object without `name`, sizes and durations positive.
At open time, on the screen: missing files, sops failures, missing keys, unreadable truststore, unknown topics.

### 4.5 `huginn.yaml` `kafka:` section
| Key | Default | Meaning |
|---|---|---|
| `tail_records` | `100` | Records per partition loaded by the tail. At most 10 000. |
| `max_records` | `20000` | Records kept per screen; the oldest are dropped first. |
| `max_buffer_bytes` | `64MiB` | Total bytes of keys, values and headers kept per screen; the oldest records are dropped first. Bounds memory whatever the record size. |
| `max_value_bytes` | `256KiB` | A larger key or value is kept truncated, with its real size shown. |
| `fetch_max_bytes` | `1MiB` | Bytes per fetch response. |
| `partition_fetch_max_bytes` | `256KiB` | Bytes per partition per fetch. |
| `connect_timeout` | `10s` | Dial + TLS + SASL before `unreachable`. |
| `request_timeout` | `30s` | One Metadata, ListOffsets or Fetch. |
| `client_id` | `huginn` | Kafka client id. |
| `isolation` | `read_uncommitted` | Initial isolation. |

## 5. Records
- **Preview** (stream) and **zoom**: JSON (compact / indented), valid UTF-8 text, otherwise `binary N B` and a hex dump. Control characters go through the existing safe-text rendering, so a record cannot move the cursor or change colours; invalid UTF-8 inside text is shown as `�`.
- **Confluent framing** (`0x00` + 4-byte schema id) is labelled `schema <id>, N B`, not decoded.
- **Null key** `∅`; **tombstone** (null value) `tombstone`; empty value `""`.
- **Timestamps**: CreateTime or LogAppendTime shown; a negative timestamp (old producers) shows `-` and sorts by offset within its partition.
- **Transactions**: control records are never shown; with `read_committed`, aborted records are not shown either.
- **Compacted topics**: gaps in offsets are normal and not reported as errors.
- **Batch compression** (gzip, snappy, lz4, zstd) is handled by the client.

## 6. Failure modes (each one tested, each one says what to do)

| Situation | Shown | Effect |
|---|---|---|
| sops not installed / cannot decrypt | `sops: <its one-line reason> (<file>)` | Nothing connects; other screens unaffected. |
| Source file missing (not `optional`) | `<file> not found` | Same. |
| `${KEY}` missing, no default | `<KEY> not found in the sources`, on the topic or in the header for the connection | Only what uses it. |
| Glob matches 0 or several files | `<glob>: no file` / `<glob>: 3 files: a, b, c` | Same. |
| Truststore unreadable / wrong password | `truststore <file>: wrong password or not PKCS12/PEM` | Connection not attempted. |
| DNS fails / VPN down / timeout when the screen opens | `brokers unreachable (<host>): check the network or VPN` on the topics | `r` on the topics screen retries. |
| TLS: unknown authority / name mismatch | `TLS: broker certificate not trusted by <tls.ca>` / `… issued for <name>` | Not retried. |
| SASL rejected | `credentials rejected for <username key>` (the key name, not the value) | Not retried. |
| Topic does not exist | `unknown topic` | Never created (auto-creation off). |
| Not authorized on topic | `not authorized` | Other topics unaffected. |
| Brokers lost during a read (restart, VPN drop) | `brokers unreachable, retrying…` then `reconnected` in the status bar | The client retries by itself and resumes from the last offset read; no record shown twice. Leader moves are invisible. |
| Offset out of range (retention deleted data during the session) | nothing: a compacted topic has offset gaps too, so a notice would be wrong as often as right | The client restarts at the earliest offset. |
| Empty topic / empty window | `no record in <topic> for <window>` | — |
| Record larger than the caps | `value truncated (N MB)` in zoom | Kept truncated. |
| Buffer full | `older records dropped` in the status bar | Oldest records evicted. |
| Refused request (guard) | `internal error: refused Kafka request <key>` | Client closed; `esc` still works. |
| Panic in a Kafka goroutine | recovered, reported like the guard | The TUI keeps running. |

Errors are classified at the adapter boundary into the existing domain kinds (`ErrUnreachable`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrSecretsAccess`, `ErrConfig`); the TUI never inspects library errors.

## 7. Architecture

| Layer | Addition |
|---|---|
| `core/domain` | `KafkaTopic`, `KafkaRecord`, `KafkaStart`, `KafkaIsolation`, `KafkaConnection` (credentials as `Secret`), placeholder and `${…}` expansion (pure), `RecordBuffer` bounded by count and bytes, record filters on the existing engine, payload classification (pure: JSON / text / binary / framed). |
| `core/ports` | Driven: `TopicSourceFactory` → `TopicSource` (`Partitions`, `Read(ctx, query) <-chan RecordBatch`); `LocalFiles` (`Glob`, `ReadEnv` plain or sops, `ReadTrustStore` → DER certificates; done in K0); `SecretFiles` (`Decrypt` a whole file, implemented by the sops adapter; done in K0). Driving: `KafkaCatalog` (has this repository Kafka settings here; cached) and `TopicSession` (resolve, list topics, open a topic). The merge of sources, `optional`, and the one-match rule of globs are use-case logic in `core/app`. |
| `core/app` | `KafkaServices`: steps of §3, limits, merge by timestamp (heap), batching. No I/O. |
| `adapters/driven/kafka` | The only franz-go importer: options, SASL, guarded dialer, offsets and fetch, retry/backoff, error classification. |
| `adapters/driven/localfiles` | `LocalFiles`: globs with `**` (bounded, no hidden folders, no link loops), dotenv files read with a size limit and parsed by `domain.ParseDotenv`, sops through `SecretFiles`, truststores PEM and PKCS12 (`software.sslmate.com/src/go-pkcs12`: Java truststores and keystores). One package for the user's local files rather than two, since both read files named by the same profile. |
| `adapters/driven/demo` | Synthetic topics and records for `--demo`. |
| `driving/tui` | `kafka.go`, `kafkazoom.go`; actions `kafka`, `kafka_isolation`; goldens. The services screen only gains the `K` marker. |
| `config` | `KafkaProfile`, `Huginn.Kafka`, validation, schema, `kafka` in the known names, `docs/CONFIG.md` section. |
| `bootstrap` | Builds the Kafka graph only when `kafka/` has a profile. |
| `archtest` | Rules for the new packages; third-party libraries confined to one package each (`archtest.Confined`: go-pkcs12 → `localfiles`, later franz-go → `kafka`); the `forbidigo` list. |

New dependencies: `github.com/twmb/franz-go` (+ `kfake` in tests) and `software.sslmate.com/src/go-pkcs12`. Both pure Go.

**Concurrency and lifetime**: all Kafka work runs in goroutines owned by the session context; the TUI only receives batches and never blocks on I/O. Closing the screen cancels the context, closes the clients and waits for the goroutines; a test checks that none survive, under `-race`.

## 8. Tests (definition of done)
- **Config**: valid and invalid profiles with positions; schema regenerated; examples load.
- **Pure functions**: placeholder and `${…}` expansion (nested `{var}` inside `${…}`, defaults, missing keys), dotenv merge order, topic list building and dedup, payload classification, record buffer limits (count and bytes).
- **Fuzz**: dotenv parser, guard frame parser, payload classification.
- **Truststore**: PEM, PKCS12 made by Java keytool (trusted cert entries), wrong password.
- **Contract suite** (`RunTopicSourceContract`) on the demo adapter and on `kfake`: tail, window by timestamp, follow, pause, offset out of range, empty topic, unknown topic, SCRAM success and failure, broker restart, close; **after each scenario, the broker saw only allowed request keys, and no group or commit request**.
- **Bootstrap**: no `kafka/` → no Kafka component, no action; with a profile → marker only on matching repositories.
- **TUI**: goldens for the topic list, records, zoom, each error state, a narrow terminal.
- **Secrets**: sentinel secret values never appear in errors, the diagnostic log or goldens.
- **Benchmarks** (done): sort and trim of 12 partitions × 10 000 records 46 ms (once per read); one frame of 20 000 records 0.4 ms; the filter over 20 000 records 1.2 ms per keystroke; the preview of a 1 MiB JSON value 0.11 ms (only its first 16 KiB are read; it was 10 ms with a full validation); a buffer append 48 ns without allocation. All within D-031.
- **Lifecycle** (done): no goroutine left after open/close cycles of sessions and of franz-go reads; brokers lost while following reported and not fatal; `go test -race ./...`; `golangci-lint run`.
- **Secrets** (done): the end-to-end test checks that passwords, right or wrong, appear neither in topic errors nor in the debug log.
- **Manual check** before calling it done: `--demo`, then a real `rec` topic compared with the original script on the same records.

## 9. Steps
1. **K0 — config** (done): structs, validation, schema, `docs/CONFIG.md` §10, `examples/config-kafka/`, `domain` references (`{var}`, `${KEY}`) and dotenv parser, `ports.LocalFiles` / `ports.SecretFiles`, `localfiles` adapter, `sops.Provider.Decrypt`.
2. **K1 — core and demo** (done): domain (record buffer by count and bytes, payload detection and rendering with control characters escaped, record filter), ports (`TopicSource`, `Kafka`, `KafkaSession`), `app.KafkaService`, fakes and `RunTopicSourceContract`, demo source and `examples/config/kafka/demo.yaml`, TUI screens and goldens, bootstrap wiring. The screen is three stacked screens (topics, records of a topic, one record) rather than two panes, like the rest of the TUI.
3. **K2 — real brokers** (done): `adapters/driven/kafka` (franz-go, partitions assigned by hand, client metrics disabled), the guard holding back each frame's header until its key is checked, `forbidigo` rules and `archtest.Confined`, the contract suite on `kfake` with a recorder of the frames on the wire, a test proving a produce request never reaches the broker and stops the source, SASL, TLS and network failures, and an end-to-end test of a real run (dotenv overlays, per-topic accounts, discovery) in `internal/bootstrap`.

4. **K3 — what §1 promised and K1 left out** (done): `o` newest first, `ctrl+y` copies the value of the record under the cursor (in the list and in zoom) with OSC 52, as D-012 chose: text and JSON as received, a hex dump for binary data, at most 64 KiB, with a confirmation naming the record and the size.

5. **K4 — Kafka from a shell** (done): `huginn kafka check <repo>` and `huginn kafka read <repo> <topic> [--tail N|--since D] [--follow] [--committed] [--raw]`, built on the same use case and adapters as the screens, needing neither the TUI nor a Kubernetes cluster. `check` fails when a topic cannot be read; `read` prints a line per record (values on one line, control characters escaped) or raw values for `jq`. It lets a profile be checked against the real brokers before opening the TUI, and replaces the original script on the command line too.

6. **Resources and responsiveness** (done): records own copies of their bytes, cut to `max_value_bytes` in the adapter, so a kept record no longer holds franz-go's whole batch buffer; pausing stops reading the channel, which blocks the read back to franz-go, so the brokers are not read while paused; live batches already waiting are merged into one redraw; the `K` marker checks repositories eight at a time; the history is sorted on compact keys (120 000 records: 44 ms → 31 ms); `huginn kafka read | head` ends quietly. Measured with `--demo`: following 20 000 records costs about 3 % of a core and 52 MB, paused under 1 %; five 2-day loads use 0.86 s of CPU (2.02 s before, most of it the demo's random seeding, now a PCG).

K0 → K2 replace the original script. Each step ends green (`go test -race ./...`, `golangci-lint run`) and is its own commit series.

## 10. Not in this version (added only on a real need)
Other source formats (properties, YAML, JSON) and command sources; `file:` and `cmd:` values; OAUTHBEARER, mTLS, JKS; SOCKS proxy and broker address mapping; partition picker; per-topic display format and redaction; schema decoding (Avro, Protobuf); consumer group lag; Kerberos, AWS IAM. The structure above (a list of sources, value forms, adapters behind ports) lets each one be added without changing what exists.
