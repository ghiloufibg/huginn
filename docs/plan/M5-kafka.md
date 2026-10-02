# M5 — Kafka topics, read only  (status: proposed, design only)

A service that consumes or produces Kafka events is often debugged with a separate script: find the connection settings somewhere in the repository, decrypt them, connect, print the records. Huginn already knows the repositories, the environments and sops; it can show the topics of a service next to its logs.

This milestone adds a **Kafka screen**: the records of the topics a service reads and writes, **read only, with no effect on the cluster or on the deployed pods**, as if Huginn were not there.

Like every other feature, it knows nothing about your applications. **Where the connection settings live, how their keys are named, which topics a service uses**: all of it comes from the config folder, in a new optional folder `kafka/`, built like `formats/` (one profile per file, `match` rules, first match wins). Without that folder, no Kafka code runs.

## 1. What the user sees

### Services screen
- A repository that a Kafka profile matches in the current environment (§3.2) shows a `K` marker in the name column.
- `M` (new action `kafka`, remappable in `ui.yaml`) opens its Kafka screen. The key bar shows it only on those rows; on the other rows it does nothing.
- Nothing is read, decrypted or connected before `M`. Leaving the screen closes every connection.

### Kafka screen

```
 <repo> · rec · Kafka (read only)                                   brokers 3/3  uncommitted
┌ TOPICS ─────────────────────┐┌ <topic-a> · 12 partitions · last 100 per partition ──────────┐
│ CONSUMES                    ││ 10:42:01.113  p3  #884213  key=k-7781  {"id":"k-7781","st…  │
│ ▸ <topic-a>             12p ││ 10:42:01.540  p7  #120044  key=k-7782  {"id":"k-7782","st…  │
│ PRODUCES                    ││ 10:42:02.002  p3  #884214  key=∅       binary 88 B (0x00…)  │
│   <topic-b>              6p ││                                                             │
│ TOPICS                      ││                                                             │
│   <topic-c>              3p ││                                                             │
└─────────────────────────────┘└─────────────────────────────────────────────────────────────┘
 enter zoom  / filter  1-7 window  0 tail  f follow  S partitions  i isolation  esc back
```

- **Topic list**, in up to three groups:
  - `CONSUMES` and `PRODUCES`: topics given a direction in the config. A topic in both is shown once, marked `⇄`.
  - `TOPICS`: topics without a direction (listed without one, or found by `discover`, §3.7). Directions are optional.
  - Each topic shows its partition count, or why it cannot be read (`not authorized`, `unknown topic`, `no credentials`, `<KEY> not found`).
- **Records**, merged across partitions by timestamp: time, partition, offset, key, one-line preview of the value (§6).
- **Zoom** (`enter`): headers, key, timestamp and its type (CreateTime or LogAppendTime), partition and offset, the value pretty-printed (JSON indented, text wrapped, hex dump for binary).
- **Reused from the logs screen** (same keys, same code where possible): windows `1`…`7` (start at the offsets for that time), `0` tail, `t`/`T`, `f` follow, `space` pause, `o` order, `n`/`N`, the filter engine (`key=…`, `header.<name>=…`, `partition=…`, free text), `ctrl+y` copy, the bounded buffer, the production banner.
- **Kafka specific keys**: `S` picks partitions, `i` switches isolation (`read_uncommitted` ↔ `read_committed`).
- **Empty view**: `no record in <topic> for the last 15m · 0 last records · esc back`.
- **Shared quota notice**, once per screen, when the profile says its credentials are the application's (`shared_credentials: true`, §3.5).

## 2. "As if Huginn were not there": the safety rules

### 2.1 No consumer group, ever
- Partitions are **assigned by hand** (franz-go `ConsumePartitions`), with no group id: no JoinGroup, SyncGroup, Heartbeat, LeaveGroup, so **no rebalance** of any group, and no offset commit. Positions live in Huginn's memory only.
- No config key can set a group id: the field does not exist.
- Reading a record does not remove it from the log; a pod only loses records if someone joins **its** group or moves **its** offsets. Both are made impossible, not just avoided.

### 2.2 An allow-list of Kafka requests, enforced twice
1. **Static**: in `adapters/driven/kafka`, `forbidigo` and `archtest` forbid `kgo.ConsumerGroup`, `CommitOffsets`, `CommitRecords`, every `Produce*`, transactions, `AllowAutoTopicCreation`, and the `kadm` package.
2. **At runtime, before the bytes leave**: Huginn dials the brokers itself (`kgo.Dialer`) and wraps each connection, above TLS, in a guard that reads the API key of each request frame and refuses anything outside:

   | Key | Request |
   |---|---|
   | 18 | ApiVersions |
   | 3 | Metadata (topic auto-creation off) |
   | 2 | ListOffsets |
   | 1 | Fetch |
   | 17, 36 | SaslHandshake, SaslAuthenticate |

   A refused frame is never written: the guard closes the client and the screen shows an internal error.
3. **Tests**: the contract suite runs against `kfake` (franz-go's in-memory broker) and asserts, for every scenario, that the broker never receives a request off the list. The guard has unit tests on split and joined frames.

### 2.3 Bounded load
- Never from the beginning of a topic: the tail or the window decides the start. Live fetching only after `f`.
- Caps from `huginn.yaml` `kafka:` (§3.9). A recognisable client id.

### 2.4 Shared quotas
When the credentials are the application's own, per-user broker quotas are shared with its pods. The intended use (a few minutes of debugging, low default caps, follow on demand) keeps the effect negligible; the notice of §1 makes it visible.

### 2.5 Nothing on the cluster side, nothing on disk
- No pod, no port-forward, no change to any manifest. Brokers are reached directly from the workstation.
- Files are read in place; encrypted ones are decrypted by `sops` into memory. Certificates are converted in memory. Nothing decrypted, no record and no credential is written to disk or to the diagnostic log; credentials stay `domain.Secret` until the Kafka adapter.

### 2.6 Absent means absent
Without `kafka/`, bootstrap builds no Kafka component and the TUI has no Kafka action. A bootstrap test checks it.

## 3. Configuration: the `kafka/` folder (optional)

```
acme-huginn/
├── huginn.yaml        + optional kafka: section (limits, §3.9)
└── kafka/             optional   one connection profile per file
    └── <name>.yaml
```

Same rules as the rest of the folder: `version: 1`, strict decoding, the profile is named after its file, files are tried **in file name order** and the first whose `match` accepts the repository and environment wins (as `formats/`). Huginn has **no default** path, file name, key name, mechanism or topic.

### 3.1 A profile at a glance

```yaml
# yaml-language-server: $schema=../../docs/schema/kafka.schema.json
version: 1

match:                                   # which repositories and environments (§3.2)
  repos: ["*"]
  envs: [rec, prd]
  files: ["{repo_dir}/<settings-dir>/{env}/*"]

sources:                                 # where values come from, merged in order (§3.4)
  - { file: "{repo_dir}/<settings-dir>/common.env" }
  - { file: "{repo_dir}/<settings-dir>/{env}/app.env", optional: true }
  - { file: "{repo_dir}/<settings-dir>/{env}/secrets.env", sops: true }

connection:                              # how to reach the brokers (§3.5)
  bootstrap: ${<BOOTSTRAP_KEY>}
  security: ${<PROTOCOL_KEY>:-sasl_ssl}
  sasl:
    mechanism: scram-sha-512
    username: ${{account}_<USER_SUFFIX>}
    password: ${{account}_<PASSWORD_SUFFIX>}
  tls:
    ca: "{repo_dir}/**/<truststore-name>.p12"
    ca_password: ${{account}_<TRUSTSTORE_PASSWORD_SUFFIX>}
  shared_credentials: true

vars: { account: <DEFAULT_ACCOUNT> }     # free variables for the templates (§3.6)

topics:                                  # what to show (§3.7)
  discover: ["<TOPIC_KEY_PREFIX>*"]

repos:                                   # per repository additions and overrides (§3.8)
  <repo-a>:
    vars: { account: <ACCOUNT_A> }
    topics:
      consume: [${<TOPIC_KEY_1>}]
      produce:
        - { name: ${<TOPIC_KEY_2>}, vars: { account: <ACCOUNT_B> } }
  <repo-b>:
    connection:
      tls: { ca: "{repo_dir}/<other-dir>/ca.pem" }
```

Every `<…>` above is yours to write. A profile can be as small as a literal bootstrap and one topic, or build everything from the repository's own files.

### 3.2 `match`
| Key | Type | Meaning |
|---|---|---|
| `repos` | list of globs | Repositories this profile applies to. Empty means any. |
| `envs` | list | Environments (keys of `environments.yaml`). Empty means any. |
| `files` | list of path globs | The profile applies only if each glob matches at least one existing file. Checked without reading or decrypting anything (a cached `stat`), so the `K` marker appears only on repositories that really carry Kafka settings. |

A profile with no `match` accepts everything; name it to sort last (`zz-default.yaml`). A repository listed under `repos:` (§3.8) is accepted by `match.repos` implicitly.

### 3.3 Placeholders and values
Two substitutions, always in this order:

| Written | Replaced by |
|---|---|
| `{env}`, `{repo}`, `{repo_dir}`, `{config_dir}`, `{<var>}` | Huginn placeholders and the profile's `vars` (§3.6). `{repo_dir}` is `repos_root/<repo>` from `huginn.yaml`, or the repository's `path` (§3.8). An unknown name is an error at load time. |
| `${KEY}`, `${KEY:-default}` | The value of `KEY` in the merged sources (§3.4); the default when absent or empty. |

Every string value is one of:
- a **literal**: `scram-sha-512`, `broker-1:9093,broker-2:9093`;
- `${KEY}`: from the sources;
- `env:VAR`: from the process environment (personal credentials kept nowhere);
- for paths: absolute, `~/…`, relative to the config folder, usually built from `{repo_dir}`; globs allowed where noted, and they must match exactly one file.

A value that cannot be resolved stops only what depends on it (one topic, or the connection) and the screen names the missing key or file.

### 3.4 `sources`: where values come from
An ordered list; values are merged and a key in a later source replaces an earlier one.

| Key | Meaning |
|---|---|
| `file` | Path (glob allowed, one match). |
| `format` | `dotenv`, `properties`, `yaml`, `json`. Default: from the extension (`.env`, `.properties`, `.yaml`/`.yml`, `.json`). YAML and JSON are flattened to dotted keys (`spring.kafka.bootstrap-servers`, `data.PASSWORD`). |
| `prefix` | Read only this sub-tree (YAML/JSON) or keys starting with it, and strip it: `prefix: data.` turns `data.PASSWORD` into `PASSWORD`. |
| `base64` | Decode every value (Kubernetes `Secret` `data`). |
| `sops` | Decrypt with sops first, in memory (the existing adapter, extended to the four formats). |
| `optional` | A missing file is skipped instead of being an error. |

No source at all is valid: every value can be a literal or `env:`.

### 3.5 `connection`
| Key | Req. | Meaning |
|---|---|---|
| `bootstrap` | yes | `host:port` list, comma separated. |
| `security` | yes | `plaintext`, `ssl`, `sasl_plaintext`, `sasl_ssl` (case ignored, `-` or `_`). |
| `sasl.mechanism` | with `sasl_*` | `plain`, `scram-sha-256`, `scram-sha-512`. |
| `sasl.username`, `sasl.password` | with `sasl_*` | Values (§3.3). |
| `tls.ca` | | CA certificates: a file (PEM, or PKCS12 `.p12`/`.pfx` by extension or `tls.ca_format`), or a value holding PEM text. Without it, the system roots. |
| `tls.ca_password` | | Password of a PKCS12 file. |
| `tls.server_name` | | TLS server name when it differs from the broker host. |
| `shared_credentials` | | `true` when the credentials are the application's own: shows the quota notice (§2.4). Default `false`. |

### 3.6 `vars`
Free variables, written `{name}` in any string of the profile. They exist so one profile can serve accounts or files whose names follow a pattern: `${{account}_PASSWORD}` reads `ORDERS_PASSWORD` when `account: ORDERS`. Defined on the profile, overridden per repository (§3.8) and per topic (§3.7). Names: lower-case letters, digits, `_`; `env`, `repo`, `repo_dir`, `config_dir` are reserved.

### 3.7 `topics`
| Key | Meaning |
|---|---|
| `consume`, `produce` | Topics with a direction. |
| `list` | Topics without a direction. |
| `discover` | Key globs over the merged sources; every value found is a topic without a direction (values with commas are split). Topics already listed are not repeated. |

A topic is a string (literal or `${KEY}`) or an object `{name, vars, connection}`: `vars` and `connection` override the profile's for that topic only (another account, another cluster). Huginn opens **one client per distinct connection** of the screen and closes them all on leaving.

### 3.8 `repos`: per repository
A map from repository name to:
- `path`: the repository folder when it is not `repos_root/<repo>`;
- `vars`, `connection`, `sources`: merged over the profile's (`sources` are appended after the profile's);
- `topics`: added to the profile's.

Overrides are merged key by key: a repository changing `tls.ca` keeps the profile's `sasl`.

### 3.9 `huginn.yaml`: `kafka:` limits (optional, neutral technical defaults)
| Key | Default | Meaning |
|---|---|---|
| `kafka.tail_records` | `100` | Records per partition loaded by the tail (`0`). |
| `kafka.max_records` | `20000` | Records kept in memory per screen. |
| `kafka.fetch_max_bytes` | `1MiB` | Bytes per fetch response. |
| `kafka.partition_fetch_max_bytes` | `256KiB` | Bytes per partition per fetch. |
| `kafka.max_value_bytes` | `1MiB` | Larger values are kept truncated, with their real size shown. |
| `kafka.client_id` | `huginn` | Kafka client id. |
| `kafka.isolation` | `read_uncommitted` | Isolation when a screen opens. |

### 3.10 Validation (in `internal/config`, with file, line and column)
- Structure: strict keys, known enum values, positive sizes, placeholder names known, `${…}` and globs well formed, `{repo_dir}` needs `repos_root` or a `path`, `sasl` required with `sasl_*`, a topic object has a `name`.
- What depends on files (sources, keys, certificates) is checked when the screen opens, since it depends on the repository and environment.
- No application value in code or as a default (D-030); `archtest` keeps checking it.

### 3.11 Three shapes the same folder supports
1. **Everything literal**, for a shared dev cluster: `bootstrap: broker:9092`, `security: plaintext`, `topics.list: [orders]`, no source.
2. **Personal read-only account**: `username: env:KAFKA_USER`, `password: env:KAFKA_PASSWORD`, bootstrap from the repository.
3. **Everything from the repository**: dotenv overlays decrypted with sops, per-topic accounts through `vars`, truststore found by glob, topics by `discover`. This is the shape of the original debug script; its profile goes in `examples/config/kafka/` with neutral names.

## 4. Opening the screen, step by step
1. Pick the first profile matching the repository and environment; merge the repository's `repos:` entry over it.
2. Replace `{…}` placeholders.
3. Read the sources in order (sops for encrypted ones), merge.
4. Build the topic list: listed, then discovered; resolve names.
5. Per topic: apply its `vars` and `connection`, resolve credentials and CA; group topics by identical connection.
6. Per group: one client, Metadata for its topics only.
7. On selecting a topic: ListOffsets (latest, or by timestamp), Fetch from `end - tail_records` or the window's offsets, merge partitions by timestamp, deliver batches at most ~30 times a second.
8. Each failure stops only what depends on it and says why on the screen.

## 5. Architecture

| Layer | Addition |
|---|---|
| `core/domain` | `KafkaTopic{Name, Direction}` (Consume, Produce, Both, None), `KafkaRecord{Topic, Partition, Offset, Time, TimeType, Key, Value, ValueSize, Headers}`, `KafkaStart`, `KafkaIsolation`, `KafkaConnection` (credentials as `Secret`, CA bytes), placeholder expansion and `${…}` resolution (pure functions), a bounded `RecordBuffer`, record filtering on the existing filter engine. |
| `core/ports` driven | `TopicSource` and `TopicSourceFactory` (connection → source); `ValueSources` (read, decrypt and flatten a source into keys); `CertificateLoader` (file or text → CA pool); `PayloadPreview`. |
| `core/ports` driving | `KafkaCatalog` (does a profile match this repository and environment, cheap) and `TopicSession` (resolve, list topics, open a topic). |
| `core/app` | `KafkaServices`: the steps of §4, limits, merging, batching. No I/O of its own. |
| `adapters/driven/kafka` | The only package importing franz-go: client options, request guard (§2.2), offsets and fetch, error classification (`ErrUnauthorized`, `ErrUnreachable`, `ErrUnknownTopic`). |
| `adapters/driven/valuesource` | dotenv, properties, YAML, JSON readers; flattening, `prefix`, `base64`; `sops: true` goes through the `SecretsProvider`. |
| `adapters/driven/sops` | Extended from dotenv to the four formats (`--input-type` from the source format). |
| `adapters/driven/certs` | PEM and PKCS12 (`software.sslmate.com/src/go-pkcs12`, `DecodeTrustStore`). |
| `adapters/driven/kafkapayload` | Preview: JSON, UTF-8 text, otherwise `binary N B` and hex; `0x00` + 4-byte id shown as `schema <id>, N B`. |
| `adapters/driven/demo` | Synthetic topics and records for `--demo`. |
| `driving/tui` | `kafka.go`, `kafkazoom.go`, `partitionpicker.go`; actions `kafka`, `kafka_partitions`, `kafka_isolation`; golden files. |
| `config` | `KafkaProfile` structs and `Huginn.Kafka`, validation, `make schema` → `docs/schema/kafka.schema.json`, `kafka` added to the folder's known names, `docs/CONFIG.md` section 10. |
| `bootstrap` | Builds the Kafka graph only when `kafka/` has at least one profile. |
| `archtest` | Rules for the new packages; franz-go and go-pkcs12 confined to their package; the `forbidigo` list of §2.2. |
| `portstest` | Fakes for the new ports; `RunTopicSourceContract` on the demo adapter and on `kfake`, with the allow-list assertion. |

## 6. Records without a declared format
No schema is declared. Each value is shown in the best readable form detected per record (JSON, text, hex). Avro/Protobuf framing is recognised and labelled, not decoded; a schema registry is out of scope.

## 7. Steps
1. **K0 — config**: profile structs, validation, schema, `docs/CONFIG.md`, examples (the three shapes of §3.11), `valuesource` and `certs` adapters, sops formats.
2. **K1 — core and demo**: domain, ports, app, fakes, demo adapter, TUI screen and goldens. Works with `--demo`.
3. **K2 — real brokers**: franz-go adapter, request guard, `kfake` contract suite, error classification.
4. **K3 — comfort**: windows by timestamp, partition picker, isolation switch, header filters, copy.

## 8. Out of scope
- Producing, replaying, resetting or deleting anything (never).
- Consumer group lag (possible later with OffsetFetch, read only; needs an allow-list change recorded in a decision).
- Schema registry decoding; OAUTHBEARER and mTLS client certificates; brokers reachable only from inside the cluster; skipping TLS verification.
