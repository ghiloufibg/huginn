# M5 — Kafka topics, read only  (status: proposed, design only)

A service that consumes or produces Kafka events is debugged today with a separate script: decrypt the overlay's `config.env` with sops, convert the truststore, connect with the application's SASL account, `assign()` partitions by hand, print the values. Huginn already knows the repository, the environment, the overlay files and sops; it can show the topics of a service next to its logs.

This milestone adds a **Kafka screen**: the records of the topics a service consumes and produces, **read only, with no effect on the cluster or on the deployed pods**, as if Huginn were not there.

It exists only for the repositories declared in a new optional file, `kafka.yaml`. Without that file, no Kafka code runs: no key, no column, no sops call, no connection.

## 1. What the user sees

### Services screen
- A repository declared in `kafka.yaml` for the current environment shows a `K` marker in the name column.
- `M` (new action `kafka`, remappable in `ui.yaml`) opens its Kafka screen. The key bar shows it only on those rows; on the other rows it does nothing.
- Nothing is resolved or connected before `M`: the env files are read and decrypted, and the brokers contacted, when the screen opens. Leaving the screen closes every connection.

### Kafka screen

```
 <repo> · rec · Kafka (read only)                                   brokers 3/3  uncommitted
┌ TOPICS ─────────────────────┐┌ <topic-a> · 12 partitions · last 200 per partition ──────────┐
│ CONSUMES                    ││ 10:42:01.113  p3  #884213  key=k-7781  {"id":"k-7781","st…  │
│ ▸ <topic-a>             12p ││ 10:42:01.540  p7  #120044  key=k-7782  {"id":"k-7782","st…  │
│ PRODUCES                    ││ 10:42:02.002  p3  #884214  key=∅       binary 88 B (0x00…)  │
│   <topic-b>              6p ││                                                             │
│ OTHER (env files)           ││                                                             │
│   <topic-c>              3p ││                                                             │
└─────────────────────────────┘└─────────────────────────────────────────────────────────────┘
 enter zoom  / filter  1-7 window  0 tail  f follow  S partitions  i isolation  esc back
```

- **Topic list**, in three groups:
  - `CONSUMES` and `PRODUCES`: declared in `kafka.yaml`. A topic declared in both lists is shown once, marked `⇄`.
  - `OTHER (env files)`: topics found in the env files by `discover` (§3.6) but not classified. Huginn's source script does not tell consumed from produced topics, so classification is optional: a service works with `discover` alone.
  - Each topic shows its partition count, or why it cannot be read (`not authorized`, `unknown topic`, `no credentials`).
- **Records**, merged across partitions by timestamp: time, partition, offset, key, and a one-line preview of the value (§6).
- **Zoom** (`enter`): headers, key, timestamp and its type (CreateTime or LogAppendTime), partition and offset, the value pretty-printed (JSON indented, text wrapped, hex dump for binary).
- **Reused from the logs screen**, same keys and same code where possible:
  - windows `1`…`7` (start at the offsets for that time, from ListOffsets by timestamp), `0` tail (the last `limits.tail_records` records per partition), `t`/`T`;
  - `f` follow, `space` pause, `o` order, `n`/`N` matches;
  - the filter engine: free text on key and value, `key=…`, `header.<name>=…`, `partition=…`;
  - `ctrl+y` copy of the record under the cursor (explicit action only);
  - the bounded buffer and its "older records dropped" notice;
  - the production banner.
- **Kafka specific keys**: `S` picks partitions (like the pod selector), `i` switches isolation between `read_uncommitted` (default, what the script and Spring Kafka use) and `read_committed`.
- **Empty view**: `no record in <topic> for the last 15m · 0 last records · esc back`.

### Warnings shown once per screen
- **Shared quota** (§2.4), when the credentials come from the application's env files: `reading with the application's account: its broker quota is shared with the pods`.
- **Production environment**: the red banner, as on the other screens.

## 2. "As if Huginn were not there": the safety rules

### 2.1 No consumer group, ever
- Partitions are **assigned by hand** (franz-go `ConsumePartitions`, the equivalent of the script's `assign()`), with no group id.
- So Huginn never talks to a group coordinator: no JoinGroup, SyncGroup, Heartbeat, LeaveGroup. It **cannot trigger a rebalance** of the pods' group.
- It never commits offsets: positions exist only in Huginn's memory.
- The `*_CONSUMER_GROUP_ID` keys of the env files are never used to consume.
- Reading a record does not remove it from the log; a pod only loses records if someone joins **its** group or moves **its** offsets. Both are made impossible below, not just avoided.

### 2.2 An allow-list of Kafka requests, enforced twice
1. **Static**: in `adapters/driven/kafka`, `forbidigo` and `archtest` forbid `kgo.ConsumerGroup`, `CommitOffsets`, `CommitRecords`, every `Produce*`, transactions, `AllowAutoTopicCreation`, and the `kadm` package.
2. **At runtime, before the bytes leave**: Huginn dials the brokers itself (`kgo.Dialer`) and wraps each connection, above TLS, in a **guard** that reads the API key of each request frame (4-byte size, then int16 key) and refuses anything outside:

   | Key | Request |
   |---|---|
   | 18 | ApiVersions |
   | 3 | Metadata (with topic auto-creation off) |
   | 2 | ListOffsets |
   | 1 | Fetch |
   | 17, 36 | SaslHandshake, SaslAuthenticate |

   A refused frame is never written: the guard closes the client and the screen shows an internal error. It should never fire; it turns a future library change or a coding mistake into a visible failure instead of a write to the cluster.
3. **Tests**: the contract suite runs against `kfake` (franz-go's in-memory broker) and asserts, for every scenario (tail, window, follow, partition change, reconnect, close), that the broker never receives a group, commit, produce, create-topics, init-producer-id or any other request off the list. The guard has its own unit tests on split and joined frames.

### 2.3 Bounded load on the brokers
- Never from the beginning of a topic: the tail or the window decides the start.
- Live fetching only after `f`.
- Caps from `limits` (§3.7): fetch bytes, partition fetch bytes, records in memory, records per second shown.
- A recognisable `client.id` (`limits.client_id`, default `huginn-readonly`) so the Kafka team can tell Huginn apart.

### 2.4 The one real risk: shared quotas
The SASL accounts are the application's (one per topic or per service). If the cluster sets byte-rate quotas **per user**, Huginn's fetches count against the same quota as the pods and could throttle them.
- Defaults are therefore low (§3.7) and follow is opt-in.
- The warning of §1 says it.
- A dedicated read-only account (ACL `READ` and `DESCRIBE` on the topics, nothing on groups) can be configured instead, through `env:` values (§3.2); the warning then disappears.

### 2.5 Nothing on the cluster side, nothing on disk
- No pod, no port-forward, no change to any manifest. The brokers are reached directly (VPN / private network), as the script does.
- Env files are read; the encrypted one is decrypted by `sops` into memory (the existing adapter, D-003). The truststore is converted in memory. Nothing decrypted, no record and no credential is written to disk or to the diagnostic log.
- Credentials stay `domain.Secret` and are revealed only inside the Kafka adapter.

### 2.6 Absent means absent
- No `kafka.yaml`: bootstrap builds no Kafka component, the TUI has no Kafka action, the franz-go code is never initialised. A bootstrap test checks it.
- A repository not declared, or declared for other environments only: no marker, no key.

## 3. Configuration: `kafka.yaml` (optional)

```yaml
# yaml-language-server: $schema=../../docs/schema/kafka.schema.json
version: 1

# Shared by every repository with the same layout.
profiles:
  standard:
    env_files:                       # merged in order, the last one wins
      - "{repo_dir}/k8s-manifests/base/config.env"
      - "{repo_dir}/k8s-manifests/overlays/{env}/config.env"
      - "sops:{repo_dir}/k8s-manifests/overlays/{env}/encrypted/config.env"
    bootstrap: ${KAFKA_BOOTSTRAP_SERVERS}
    security: ${KAFKA_SECURITY_PROTOCOL:-plaintext}
    sasl:
      mechanism: scram-sha-512
      username: ${{prefix}_USERNAME}
      password: ${{prefix}_PASSWORD}
    truststore:
      file: "{repo_dir}/src/main/resources/truststore.p12"
      password: ${{prefix}_SSL_TRUSTSTORE_PASSWORD:-changeit}
    discover: ["KAFKA_TOPIC*", "KAFKA_TOPICS*"]

services:
  - repo: <repo-a>
    profile: standard
    prefix: <P>                      # default credentials of this service
    topics:
      consume:
        - ${KAFKA_TOPIC_<X>}
        - { name: ${KAFKA_TOPIC_<Y>}, prefix: <P2> }   # other account for this topic
      produce:
        - ${KAFKA_TOPIC_<Z>}

  - repo: <repo-b>
    profile: standard
    prefix: <Q>
    truststore: { file: "{repo_dir}/config/kafka/truststore.p12" }   # override of the profile
    # no topics: everything comes from discover, under OTHER
```

### 3.1 Two kinds of substitution
| Written | Replaced by | When |
|---|---|---|
| `{env}`, `{repo}`, `{repo_dir}`, `{prefix}` | Huginn placeholders: current environment, repository name, its folder, the prefix of the service or topic | first |
| `${KEY}`, `${KEY:-default}` | the value of `KEY` in the merged env files (§3.3); the default when the key is absent or empty | then |

`{repo_dir}` is `repos_root/<repo>` (`huginn.yaml`), or the service's `path` when the folder is named differently. A `${KEY}` absent without default is an error on that topic only (`<topic>: KAFKA_TOPIC_X not found in the env files`), shown in the topic list.

### 3.2 Values
Every string value is one of:
- a literal: `scram-sha-512`;
- `${KEY}` / `${KEY:-default}`: from the env files;
- `env:VAR`: from the process environment, for personal credentials kept nowhere (a dedicated read-only account);
- paths only: a path, relative to the config folder, `~/` to the home folder, usually built from `{repo_dir}`.

### 3.3 `env_files`
- Read in order and merged; a key in a later file replaces the earlier value (base, then overlay, then encrypted overlay).
- `sops:<file>` is decrypted with the existing sops adapter (dotenv, once per file, in memory). Other files are read as plain dotenv.
- A missing plain file is skipped (an overlay may not exist); a missing or undecryptable `sops:` file is an error shown on the screen with sops' reason, as for `namespace_from`.

### 3.4 Connection
| Key | Meaning |
|---|---|
| `bootstrap` | `host:port` list, comma separated. One per service. |
| `security` | `plaintext`, `ssl`, `sasl_plaintext`, `sasl_ssl` (case ignored, so `SASL_SSL` from an env file works). |
| `sasl.mechanism` | `plain`, `scram-sha-256`, `scram-sha-512`. |
| `sasl.username`, `sasl.password` | Usually built from `{prefix}` (§3.5). |
| `truststore.file` | PKCS12 truststore (`.p12`), or PEM (`.pem`, `.crt`) by extension. Converted in memory to the CA pool; no `openssl`. Without it, the system roots are used. |
| `truststore.password` | Password of the PKCS12 file. |

**Truststore location**: the profile gives the usual path (the repository's resources folder); a service overrides it with its own `truststore.file` when its repository differs. A glob is accepted (`{repo_dir}/**/truststore.p12`) and must match exactly one file; zero or several matches is an error naming the candidates.

### 3.5 Credentials and `prefix`
The application has one SASL account per topic or per service, named by a key prefix (`<P>_USERNAME`, `<P>_PASSWORD`).
- `prefix` on the service is the default for all its topics; `prefix` on a topic overrides it.
- `{prefix}` is replaced in the profile's `sasl` and `truststore` templates before `${…}` is read.
- Huginn opens **one client per distinct set of credentials** of the screen, and closes them all on leaving.
- A topic whose credentials cannot be resolved is listed with `no credentials` and the missing key.

### 3.6 Topics and `discover`
- `topics.consume` and `topics.produce` are lists of names (a literal or `${KEY}`), or `{name, prefix}` objects. Both optional.
- `discover` is a list of key globs over the merged env files. Each value found is a topic (a value with commas is split). Topics already declared are not repeated; the others go under `OTHER (env files)` with the service's default credentials.
- A service must end up with at least one topic, declared or discovered.

### 3.7 `limits` (top level, all optional, neutral technical defaults)
| Key | Default | Meaning |
|---|---|---|
| `tail_records` | `100` | Records per partition loaded by the tail (`0`). |
| `max_records` | `20000` | Records kept in memory per screen. |
| `fetch_max_bytes` | `1MiB` | Bytes per fetch response. |
| `partition_fetch_max_bytes` | `256KiB` | Bytes per partition per fetch. |
| `max_value_bytes` | `1MiB` | Larger values are kept truncated, with their real size shown. |
| `client_id` | `huginn-readonly` | Kafka client id. |
| `isolation` | `read_uncommitted` | Initial isolation (`i` switches it). |

### 3.8 Validation (in `internal/config`, with file, line and column)
- `version: 1`; strict keys; `profile` exists; `repo` is a repository name the resolvers can produce (warning only, the catalog is dynamic); `{repo_dir}` needs `repos_root` or `path`; `security`, `mechanism`, `isolation` are known values; sizes and counts are positive.
- `${…}` and globs are checked for syntax at load time; their values only when the screen opens (the files depend on the environment).
- No application value in code: names of keys, paths, prefixes and the truststore location live in the user's folder and in `examples/config/kafka.yaml`, never as defaults (D-030).

## 4. Opening the screen, step by step
1. Pick the service entry of the repository and its profile; apply the service overrides.
2. Replace `{env}`, `{repo}`, `{repo_dir}`.
3. Read and merge `env_files` (sops for the encrypted one).
4. Build the topic list: declared topics, then discovered ones; resolve names.
5. Per topic: replace `{prefix}`, resolve credentials and truststore; group topics by identical connection settings.
6. Per group: one client, Metadata for its topics only (partition counts, leaders).
7. On selecting a topic: ListOffsets (latest, or by timestamp for a window), Fetch from `end - tail_records` or from the window's offsets, merge partitions by timestamp, deliver batches at most ~30 times a second (as log batches).
8. Each step that fails stops only what depends on it and says why on the screen.

## 5. Architecture

| Layer | Addition |
|---|---|
| `core/domain` | `KafkaTopic{Name, Direction}` (Consume, Produce, Both, Other), `KafkaRecord{Topic, Partition, Offset, Time, TimeType, Key, Value, ValueSize, Headers}`, `KafkaStart` (tail N, since time), `KafkaIsolation`, `KafkaConnection` (bootstrap, security, mechanism, credentials as `Secret`, CA pool bytes), a bounded `RecordBuffer`, record filtering on the existing filter engine. |
| `core/ports` driven | `TopicSource` (`Topics(ctx) → partitions or error per topic`, `Read(ctx, TopicQuery) <-chan RecordBatch`), `TopicSourceFactory` (`Open(KafkaConnection) TopicSource`), `EnvFiles` (read and merge dotenv files, sops for the encrypted ones), `TrustStore` (file → CA pool), `PayloadPreview` (bytes → preview and zoom text). |
| `core/ports` driving | `KafkaCatalog` (`Topics(env, repo)`: is Kafka declared, which topics, resolution errors) and `TopicSession` (`Open(ctx, TopicQuery) <-chan RecordBatch`). |
| `core/app` | `KafkaServices`: resolution of §4 (placeholders, env files, credentials, grouping), limits, merge by timestamp, batching. No I/O of its own. |
| `adapters/driven/kafka` | The only package importing franz-go: client options, the request guard (§2.2), offsets and fetch, error classification into domain kinds (`ErrUnauthorized`, `ErrUnreachable`, `ErrUnknownTopic`). |
| `adapters/driven/envfile` | `EnvFiles`: plain dotenv reader + the existing `SecretsProvider` for `sops:` files. |
| `adapters/driven/truststore` | PKCS12 (`software.sslmate.com/src/go-pkcs12`, `DecodeTrustStore`) and PEM. |
| `adapters/driven/kafkapayload` | Preview: JSON (compact on the stream, indented in zoom), UTF-8 text, otherwise `binary N B` and hex; values starting with `0x00` + 4-byte id are shown as `schema <id>, N B` (Avro/Protobuf framing, not decoded). |
| `adapters/driven/demo` | Synthetic topics and records for `--demo`. |
| `driving/tui` | `kafka.go` (screen), `kafkazoom.go`, `partitionpicker.go`; actions `kafka`, `kafka_partitions`, `kafka_isolation` in `keys.go`; golden files. |
| `config` | `KafkaFile` structs, validation, `make schema` → `docs/schema/kafka.schema.json`, `kafka.yaml` added to the fixed file names. |
| `bootstrap` | Builds the Kafka graph only when `kafka.yaml` is loaded; passes `nil` driving ports otherwise, and the TUI hides the feature. |
| `archtest` | Rules for the four new adapter packages; franz-go and go-pkcs12 allowed only in their package; `forbidigo` list of §2.2. |
| `portstest` | Fakes for the new ports; `RunTopicSourceContract` run on the demo adapter and on `kfake`, including the allow-list assertion. |

## 6. What "without a declared format" means
Huginn declares no schema for records. The value is shown as it is, with the best readable form detected per record (§5 `kafkapayload`). Decoding Avro or Protobuf through a schema registry is out of scope; the framing is recognised so the user knows why a value is binary.

## 7. Steps
1. **K0 — config**: structs, validation, schema, `docs/CONFIG.md` section, `examples/config/kafka.yaml`, `envfile` and `truststore` adapters with tests.
2. **K1 — core and demo**: domain, ports, app, fakes, demo adapter, TUI screen and goldens. Everything works with `--demo`, no cluster.
3. **K2 — real brokers**: franz-go adapter, request guard, `kfake` contract suite with the allow-list assertion, error classification, quota warning.
4. **K3 — comfort**: windows by timestamp, partition picker, isolation switch, filters on headers, copy.

## 8. Out of scope
- Producing, replaying, resetting or deleting anything (never).
- Consumer group lag. It could later read the application's group offsets with OffsetFetch (read only, no membership), off by default; it needs the group `DESCRIBE` ACL and an allow-list change recorded in a decision.
- Schema registry decoding; OAUTHBEARER and mTLS; brokers reachable only from inside the cluster.

## 9. Decisions to record when implemented
D-040 (see `docs/DECISIONS.md`): manual assignment and request allow-list, franz-go, env file layering and placeholders, quota warning.
