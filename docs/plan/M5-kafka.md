# M5 — Kafka topics, read only  (status: proposed, design only)

A service that consumes or produces Kafka events is often debugged with a separate script: find the connection settings somewhere in the repository, decrypt them, connect, print the records. Huginn already knows the repositories, the environments and sops; it can show the topics of a service next to its logs.

This milestone adds a **Kafka screen**: the records of the topics a service reads and writes, **read only, with no effect on the cluster or on the deployed pods**, as if Huginn were not there.

Like every other feature, it knows nothing about your applications. **Where the connection settings live, how their keys are named, which topics a service uses**: all of it comes from the config folder, in a new optional folder `kafka/`, built like `formats/` (one profile per file, `match` rules, first match wins). Without that folder, no Kafka code runs.

## 1. What the user sees

### Services screen
- A repository that a Kafka profile matches in the environment Huginn runs on (§3.2) shows a `K` marker in the name column.
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
- Caps from `huginn.yaml` `kafka:` (§3.10). A recognisable client id.

### 2.4 Shared quotas
When the credentials are the application's own, per-user broker quotas are shared with its pods. The intended use (a few minutes of debugging, low default caps, follow on demand) keeps the effect negligible; the notice of §1 makes it visible.

### 2.5 Nothing on the cluster side, nothing on disk
- No pod, no port-forward, no change to any manifest. Brokers are reached directly from the workstation (or through the user's SOCKS proxy).
- Files are read in place; encrypted ones are decrypted by `sops` into memory. Commands (`cmd:`, `command` sources) are the user's own, run without a shell, their output kept in memory. Certificates are converted in memory. Nothing decrypted, no record and no credential is written to disk or to the diagnostic log; credentials stay `domain.Secret` until the Kafka adapter.

### 2.6 Absent means absent
Without `kafka/`, bootstrap builds no Kafka component and the TUI has no Kafka action. A bootstrap test checks it.

## 3. Configuration: the `kafka/` folder (optional)

```
acme-huginn/
├── huginn.yaml        + optional kafka: section (limits, §3.10)
└── kafka/             optional   one connection profile per file
    └── <name>.yaml
```

Same rules as the rest of the folder: `version: 1`, strict decoding, the profile is named after its file, files are tried **in file name order** and the first whose `match` accepts the repository wins (as `formats/`). Huginn has **no default** path, file name, key name, mechanism or topic.

**Rule for options**: an option is in this design only if it is cheap when unused. None of them adds work to the services or logs screens, a dependency that weighs on the binary, or a widget the Kafka screen draws when the option is off. Options that fail this test are listed in §8 for later, if a real need appears.

### 3.1 A profile at a glance

```yaml
# yaml-language-server: $schema=../../docs/schema/kafka.schema.json
version: 1

match:                                   # which repositories (§3.2)
  repos: ["*"]
  files: ["{repo_dir}/<settings-dir>/{env}/*"]

sources:                                 # where values come from, merged in order (§3.4)
  - { file: "{repo_dir}/<settings-dir>/common.env" }
  - { file: "{repo_dir}/<settings-dir>/{env}/app.env", optional: true }
  - { file: "{repo_dir}/<settings-dir>/{env}/secrets.env", sops: true }

vars: { account: <DEFAULT_ACCOUNT> }     # free variables for the templates (§3.6)

connection:                              # how to reach the brokers (§3.5)
  bootstrap: ${<BOOTSTRAP_KEY>}
  security: ${<PROTOCOL_KEY>:-sasl_ssl}
  sasl:
    mechanism: scram-sha-512
    username: ${{account}_<USER_SUFFIX>}
    password: ${{account}_<PASSWORD_SUFFIX>}
  tls:
    ca: "{repo_dir}/**/<truststore-name>.p12"
    ca_password: ${{account}_<TRUSTSTORE_PASSWORD_SUFFIX>:-<default>}
  shared_credentials: true

topics:                                  # what to show (§3.7)
  discover: ["<TOPIC_KEY_PREFIX>*"]

repos:                                   # per repository additions and overrides (§3.9)
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
| `exclude` | list of globs | Repositories it never applies to. |
| `files` | list of path globs | The profile applies only if each glob matches at least one existing file. Checked without reading or decrypting anything, once per environment and repository, then cached; so the `K` marker appears only on repositories that really carry Kafka settings, at no cost on the services screen's refreshes. |

There is no environment list: the environment is the one Huginn runs on (`huginn rec`, `-e`, `HUGINN_ENV`, `ctrl+e`), written `{env}` in paths and values. A repository without Kafka settings for that environment has no matching `files`, so it gets no `K` marker there.

A profile with no `match` accepts everything; name it to sort last (`zz-default.yaml`). A repository listed under `repos:` (§3.9) is accepted by `match.repos` implicitly.

### 3.3 Placeholders and values
Two substitutions, always in this order:

| Written | Replaced by |
|---|---|
| `{env}`, `{repo}`, `{repo_dir}`, `{config_dir}`, `{<var>}` | Huginn placeholders and the profile's `vars` (§3.6). `{repo_dir}` is `repos_root/<repo>` from `huginn.yaml`, or the repository's `path` (§3.9). An unknown name is an error at load time. |
| `${KEY}`, `${KEY:-default}` | The value of `KEY` in the merged sources (§3.4); the default when absent or empty. |

Every string value is one of:
- a **literal**: `scram-sha-512`, `broker-1:9093,broker-2:9093`;
- `${KEY}`: from the sources;
- `env:VAR`: from the process environment (personal credentials kept nowhere);
- `file:<path>`: the content of a file (a token, a PEM certificate), trailing newline removed;
- `cmd:<command>`: the standard output of a command run without a shell (`cmd:gcloud auth print-access-token`), when the screen opens, kept in memory;
- for paths: absolute, `~/…`, relative to the config folder, usually built from `{repo_dir}`; globs allowed where noted, and they must match exactly one file.

A value that cannot be resolved stops only what depends on it (one topic, or the connection) and the screen names the missing key or file.

### 3.4 `sources`: where values come from
An ordered list; values are merged and a key in a later source replaces an earlier one. Each source has one of `file`, `command`, `values`.

| Key | Meaning |
|---|---|
| `file` | Path (glob allowed, one match). |
| `command` | A command whose standard output is read like a file (a secret manager CLI). Run without a shell, output kept in memory. |
| `values` | Inline key/value map (handy for overrides in `repos:`). |
| `format` | `dotenv`, `properties`, `yaml`, `json`. Default: from the extension, `dotenv` for commands. YAML and JSON are flattened to dotted keys (`spring.kafka.bootstrap-servers`, `data.PASSWORD`). All four are read with the standard library and the YAML library Huginn already uses. |
| `prefix` | Read only this sub-tree or the keys starting with it, and strip it: `prefix: data.` turns `data.PASSWORD` into `PASSWORD`. |
| `base64` | Decode every value (a Kubernetes `Secret` manifest). |
| `sops` | Decrypt with sops first, in memory (the existing adapter, extended to the four formats). |
| `optional` | A missing file or failing command is skipped instead of being an error. |

No source at all is valid: every value can be a literal, `env:`, `file:` or `cmd:`.

### 3.5 `connection`
| Key | Req. | Meaning |
|---|---|---|
| `bootstrap` | yes | `host:port` list, comma separated. |
| `security` | yes | `plaintext`, `ssl`, `sasl_plaintext`, `sasl_ssl` (case ignored, `-` or `_`). |
| `sasl.mechanism` | with `sasl_*` | `plain`, `scram-sha-256`, `scram-sha-512`, `oauthbearer`. |
| `sasl.username`, `sasl.password` | plain, scram | Values (§3.3). |
| `sasl.token` | oauthbearer | The token: usually `cmd:…` (any CLI printing a token, such as a cloud SDK) or `env:…`; read again when the broker rejects it as expired. |
| `tls.ca` | | CA certificates: a file (PEM, PKCS12 `.p12`/`.pfx`, Java `.jks`) or a value holding PEM text. Without it, the system roots. |
| `tls.ca_password` | | Password of a PKCS12 or JKS truststore. |
| `tls.cert`, `tls.key` | | Client certificate (mTLS), PEM files or values. |
| `tls.keystore`, `tls.keystore_password` | | Client certificate from a PKCS12 or JKS keystore instead. |
| `tls.server_name` | | TLS server name when it differs from the broker host. |
| `tls.insecure_skip_verify` | | Do not verify the brokers' certificates. Default `false`; when `true`, the header says `TLS NOT VERIFIED`. |
| `address_map` | | Map `advertised host:port → reachable host:port`, for brokers that advertise names the workstation cannot resolve. Applied in Huginn's own dialer, no extra component. |
| `proxy` | | `socks5://[user:pass@]host:port`: broker connections go through it (`golang.org/x/net/proxy`, already a dependency). |
| `shared_credentials` | | `true` when the credentials are the application's own: shows the quota notice (§2.4). Default `false`. |

### 3.6 `vars`
Free variables, written `{name}` in any string of the profile. They exist so one profile can serve accounts or files whose names follow a pattern: `${{account}_PASSWORD}` reads `ORDERS_PASSWORD` when `account: ORDERS`. Defined on the profile, overridden per repository (§3.9) and per topic (§3.7). Names: lower-case letters, digits, `_`; the placeholder names of §3.3 are reserved.

### 3.7 `topics`
| Key | Meaning |
|---|---|
| `consume`, `produce` | Topics with a direction. |
| `list` | Topics without a direction. |
| `discover` | Key globs over the merged sources; every value found is a topic without a direction (values with commas are split). |
| `exclude` | Topic name globs never shown, applied last. |

A topic is a string (literal or `${KEY}`) or an object:

| Key | Meaning |
|---|---|
| `name` | Required. Literal or `${KEY}`. |
| `vars`, `connection` | Override the profile's for this topic only (another account, another cluster). |
| `start` | Window when the topic opens: `tail`, `tail:N` or a duration (`15m`). Default: `huginn.yaml` `kafka.default_start`. |
| `value`, `key` | Display format: `auto` (default: JSON, then text, then hex), `json`, `text`, `hex`. |
| `redact` | JSON paths (`card.number`, `*.password`) or header names whose values are shown as `[redacted]`, in zoom and in copies too, through the existing domain `Redactor`. |

`value`, `key` and `redact` can also be set once for all topics of the profile under `topics.defaults`. Huginn opens **one client per distinct connection** of the screen and closes them all on leaving.

### 3.8 What is not configurable, on purpose
- The group id, offset commits, producing: they do not exist (§2.1).
- Colours and keys of the Kafka screen: `ui.yaml`, as for every screen (actions `kafka`, `kafka_partitions`, `kafka_isolation`).

### 3.9 `repos`: per repository
A map from repository name (or glob) to:
- `path`: the repository folder when it is not `repos_root/<repo>`;
- `vars`, `connection`, `sources`: merged over the profile's (`sources` are appended after the profile's);
- `topics`: added to the profile's;
- `enabled: false`: no Kafka screen for this repository.

Overrides are merged key by key: a repository changing `tls.ca` keeps the profile's `sasl`.

### 3.10 `huginn.yaml`: `kafka:` section (optional, neutral technical defaults)
| Key | Default | Meaning |
|---|---|---|
| `kafka.tail_records` | `100` | Records per partition loaded by the tail (`0`). |
| `kafka.default_start` | `tail` | Window when a topic opens: `tail`, `tail:N` or a duration. |
| `kafka.max_records` | `20000` | Records kept in memory per screen. |
| `kafka.fetch_max_bytes` | `1MiB` | Bytes per fetch response. |
| `kafka.partition_fetch_max_bytes` | `256KiB` | Bytes per partition per fetch. |
| `kafka.max_value_bytes` | `1MiB` | Larger values are kept truncated, with their real size shown. |
| `kafka.client_id` | `huginn` | Kafka client id. |
| `kafka.connect_timeout` | `10s` | Time to reach the brokers and authenticate before the screen says they are unreachable. |
| `kafka.request_timeout` | `30s` | Time allowed for one Metadata, ListOffsets or Fetch request. |
| `kafka.isolation` | `read_uncommitted` | Isolation when a screen opens. |

### 3.11 Validation (in `internal/config`, with file, line and column)
- Structure: strict keys, known enum values, positive sizes and durations, placeholder names known, `${…}`, `cmd:` and globs well formed, `{repo_dir}` needs `repos_root` or a `path`, `sasl` keys matching the mechanism, a topic object has a `name`, one source kind per source.
- What depends on files or commands (sources, keys, certificates) is checked when the screen opens, since it depends on the repository and environment.
- No application value in code or as a default (D-030); `archtest` keeps checking it.

### 3.12 Shapes the same folder supports
1. **Everything literal**, for a shared dev cluster: `bootstrap: broker:9092`, `security: plaintext`, `topics.list: [orders]`, no source.
2. **Personal read-only account**: `username: env:KAFKA_USER`, `password: env:KAFKA_PASSWORD`, bootstrap from the repository.
3. **Everything from the repository**: dotenv overlays decrypted with sops, per-topic accounts through `vars`, PKCS12 truststore found by glob, topics by `discover`. The shape of the original debug script; its profile goes in `examples/config/kafka/` with neutral names.
4. **Spring Boot properties**: `application-{env}.yaml` as a `yaml` source, `spring.kafka.*` read as dotted keys.
5. **Cloud Kafka with tokens**: `oauthbearer` with `sasl.token: cmd:<cloud CLI printing a token>`.

## 4. Opening the screen, step by step
1. Pick the first profile matching the repository (its `files` checked for the current environment); merge the repository's `repos:` entry over it.
2. Replace `{…}` placeholders.
3. Read the sources in order (files, sops, commands), merge.
4. Build the topic list: listed, then discovered; resolve names.
5. Per topic: apply its `vars` and `connection`, resolve credentials, certificates and display options; group topics by identical connection.
6. Per group: one client (through the proxy if any), Metadata for its topics only.
7. On selecting a topic: ListOffsets (latest, or by timestamp), Fetch from `end - tail_records` or the window's offsets, merge partitions by timestamp, deliver batches at most ~30 times a second.
8. Each failure stops only what depends on it and says why on the screen.

## 5. Architecture

| Layer | Addition |
|---|---|
| `core/domain` | `KafkaTopic{Name, Direction}` (Consume, Produce, Both, None), `KafkaRecord{Topic, Partition, Offset, Time, TimeType, Key, Value, ValueSize, Headers}`, `KafkaStart`, `KafkaIsolation`, `KafkaConnection` (credentials as `Secret`, certificate bytes), placeholder expansion and `${…}` resolution (pure functions), a bounded `RecordBuffer`, record filtering on the existing filter engine. |
| `core/ports` driven | `TopicSource` and `TopicSourceFactory` (connection → source); `ValueSources` (read, decrypt and flatten a source into keys); `CertificateLoader` (CA pool and client certificate from PEM, PKCS12, JKS); `PayloadPreview`. |
| `core/ports` driving | `KafkaCatalog` (does a profile match this repository in this environment; cached) and `TopicSession` (resolve, list topics, open a topic). |
| `core/app` | `KafkaServices`: the steps of §4, limits, merging, batching. No I/O of its own. |
| `adapters/driven/kafka` | The only package importing franz-go: client options, SASL plain/scram/oauthbearer, dialer with address map, SOCKS proxy and the request guard (§2.2), offsets and fetch, error classification (`ErrUnauthorized`, `ErrUnreachable`, `ErrUnknownTopic`). |
| `adapters/driven/valuesource` | file (dotenv, properties, YAML, JSON), command and inline sources; flattening, `prefix`, `base64`; `sops: true` goes through the `SecretsProvider`. |
| `adapters/driven/sops` | Extended from dotenv to the four formats (`--input-type` from the source format). |
| `adapters/driven/certs` | PEM, PKCS12 (`software.sslmate.com/src/go-pkcs12`) and JKS (`github.com/pavlo-v-chernykh/keystore-go`): two small pure-Go libraries without dependencies. |
| `adapters/driven/kafkapayload` | auto, json, text, hex; Confluent framing labelled; redaction. |
| `adapters/driven/demo` | Synthetic topics and records for `--demo`. |
| `driving/tui` | `kafka.go`, `kafkazoom.go`, `partitionpicker.go`; actions `kafka`, `kafka_partitions`, `kafka_isolation`; golden files. The services screen only gains the `K` marker, read from the cached `KafkaCatalog`. |
| `config` | `KafkaProfile` structs and `Huginn.Kafka`, validation, `make schema` → `docs/schema/kafka.schema.json`, `kafka` added to the folder's known names, a `docs/CONFIG.md` section. |
| `bootstrap` | Builds the Kafka graph only when `kafka/` has at least one profile. |
| `archtest` | Rules for the new packages; each third-party library confined to its package; the `forbidigo` list of §2.2. |
| `portstest` | Fakes for the new ports; `RunTopicSourceContract` on the demo adapter and on `kfake`, with the allow-list assertion. |

New dependencies: franz-go (with its `kfake` for tests), go-pkcs12, keystore-go. Nothing else.

## 6. Records without a declared format
Nothing has to be declared: each value is shown in the best readable form detected per record (JSON, text, hex), or the format a topic sets (§3.7). Avro/Protobuf framing is recognised and labelled (`schema <id>, N B`), not decoded.

## 7. Steps
1. **K0 — config**: structs, validation, schema, `docs/CONFIG.md`, examples (the shapes of §3.12); `valuesource` and `certs` adapters; sops formats.
2. **K1 — core and demo**: domain, ports, app, fakes, demo adapter, TUI screen and goldens. Works with `--demo`.
3. **K2 — real brokers**: franz-go adapter (plain, scram, oauthbearer, TLS, mTLS, address map, proxy), request guard, `kfake` contract suite, error classification.
4. **K3 — comfort**: windows by timestamp, partition picker, isolation switch, header filters, copy, redaction.

K0 → K2 cover the original script entirely (dotenv + sops, SCRAM, PKCS12 truststore, discover, tail and windows).

## 8. Out of scope
- **Never**, whatever the config says: producing, replaying, resetting offsets, deleting or creating anything; joining a consumer group or committing an offset (§2.1); writing records or secrets to disk (the existing `ctrl+s` saves only the visible records, redacted, on explicit request).
- **Left out because they fail the rule of §3** (a heavy dependency or work on the TUI for a need nobody has yet). Each would be one new adapter package or one new option, without changing the rest of the design:
  - Kerberos (GSSAPI) and AWS MSK IAM authentication, used by on-premise and AWS clusters;
  - Avro, Protobuf and JSON Schema decoding, local schemas, schema registry;
  - value fields as columns, time or level taken from the value;
  - consumer group lag (would add OffsetFetch to the allow-list);
  - Kubernetes Secret or ConfigMap sources, port-forward tunnel, DNS overrides;
  - topic discovery from the brokers' topic list.

## 9. Cost when unused
- **No `kafka/` folder**: no Kafka component is built, no action is bound, no file is checked; the binary still carries the three libraries, nothing runs.
- **Repositories without Kafka settings**: one cached file check per environment, no marker, no key.
- **Options left off**: no request, no widget, no column; the Kafka screen draws only what the profile enables.
- **`--demo`**: the demo replaces the `TopicSourceFactory` and the `ValueSources` with synthetic ones; `examples/config/kafka/` holds a profile with literal values and no `match.files`, so the screen works without any file or broker.
