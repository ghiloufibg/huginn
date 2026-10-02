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
- **Optional**, when the profile enables them: decoded Avro/Protobuf values (§3.9, §3.10), value fields as columns, the application's consumer lag per partition (§3.12), `via port-forward` or `TLS NOT VERIFIED` in the header (§3.5, §3.8).
- **Empty view**: `no record in <topic> for the last 15m · 0 last records · esc back`.
- **Shared quota notice**, once per screen, when the profile says its credentials are the application's (`shared_credentials: true`, §3.5).

## 2. "As if Huginn were not there": the safety rules

### 2.1 No consumer group, ever
- Partitions are **assigned by hand** (franz-go `ConsumePartitions`), with no group id: no JoinGroup, SyncGroup, Heartbeat, LeaveGroup, so **no rebalance** of any group, and no offset commit. Positions live in Huginn's memory only.
- No config key makes Huginn consume as a group: there is no group id setting. The optional `groups` section (§3.12) only reads the application's committed offsets to show its lag.
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
   | 9, 10 | OffsetFetch, FindCoordinator — only when the profile enables `groups` (§3.12) |

   A refused frame is never written: the guard closes the client and the screen shows an internal error.
3. **Tests**: the contract suite runs against `kfake` (franz-go's in-memory broker) and asserts, for every scenario, that the broker never receives a request off the list. The guard has unit tests on split and joined frames.

### 2.3 Bounded load
- Never from the beginning of a topic: the tail or the window decides the start. Live fetching only after `f`.
- Caps from `huginn.yaml` `kafka:` (§3.13). A recognisable client id. Optional fetching from the closest replica (`rack`).

### 2.4 Shared quotas
When the credentials are the application's own, per-user broker quotas are shared with its pods. The intended use (a few minutes of debugging, low default caps, follow on demand) keeps the effect negligible; the notice of §1 makes it visible.

### 2.5 Nothing on the cluster side, nothing on disk
- No pod, no change to any manifest. Brokers are reached directly from the workstation by default; a port-forward (§3.8) and Kubernetes Secret or ConfigMap sources (§3.4) are opt-in, read-only, and create nothing in the cluster.
- Files are read in place; encrypted ones are decrypted by `sops` into memory. Commands (`cmd:`, `command` sources, OAuth token commands) are the user's own, run without a shell, their output kept in memory. Certificates are converted in memory. Nothing decrypted, no record and no credential is written to disk or to the diagnostic log; credentials stay `domain.Secret` until the Kafka adapter.

### 2.6 Absent means absent
Without `kafka/`, bootstrap builds no Kafka component and the TUI has no Kafka action. A bootstrap test checks it.

## 3. Configuration: the `kafka/` folder (optional)

```
acme-huginn/
├── huginn.yaml        + optional kafka: section (limits and client defaults, §3.13)
└── kafka/             optional   one connection profile per file
    └── <name>.yaml
```

Same rules as the rest of the folder: `version: 1`, strict decoding, the profile is named after its file, files are tried **in file name order** and the first whose `match` accepts the repository wins (as `formats/`). Huginn has **no default** path, file name, key name, mechanism or topic. Every option below is optional unless marked required; the principle is "more options than fewer", each one off or neutral by default.

### 3.1 A profile at a glance

```yaml
# yaml-language-server: $schema=../../docs/schema/kafka.schema.json
version: 1
description: <free text shown in the screen header>
enabled: true

match:                                   # which repositories (§3.2)
  repos: ["*"]
  exclude: ["<repo-without-kafka>"]
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

network: {}                              # proxy, address mapping, tunnel (§3.8)
schema_registry: {}                      # optional decoding of Avro / Protobuf / JSON Schema (§3.10)

topics:                                  # what to show and how (§3.7, §3.9)
  discover: ["<TOPIC_KEY_PREFIX>*"]
  defaults:
    value: { format: auto }
    redact: ["<json.path.to.hide>"]

repos:                                   # per repository additions and overrides (§3.11)
  <repo-a>:
    vars: { account: <ACCOUNT_A> }
    topics:
      consume: [${<TOPIC_KEY_1>}]
      produce:
        - { name: ${<TOPIC_KEY_2>}, vars: { account: <ACCOUNT_B> }, value: { format: json } }
  <repo-b>:
    connection:
      tls: { ca: "{repo_dir}/<other-dir>/ca.pem" }
```

Every `<…>` above is yours to write. A profile can be as small as a literal bootstrap and one topic, or build everything from the repository's own files.

### 3.2 `match`
| Key | Type | Meaning |
|---|---|---|
| `repos` | list of globs | Repositories this profile applies to. Empty means any. |
| `exclude` | list of globs | Repositories it never applies to, even if `repos` accepts them. |
| `files` | list of path globs | The profile applies only if each glob matches at least one existing file. Checked without reading or decrypting anything (a cached `stat`), so the `K` marker appears only on repositories that really carry Kafka settings. |
| `files_any` | list of path globs | Same, but one match among them is enough. |

There is no environment list: the environment is the one Huginn runs on (`huginn rec`, `-e`, `HUGINN_ENV`, `ctrl+e`), written `{env}` in paths and values. A repository without Kafka settings for that environment simply has no matching `files`, so it gets no `K` marker there.

A profile with no `match` accepts everything; name it to sort last (`zz-default.yaml`). A repository listed under `repos:` (§3.11) is accepted by `match.repos` implicitly. `enabled: false` turns a profile off without deleting it.

### 3.3 Placeholders and values
Two substitutions, always in this order:

| Written | Replaced by |
|---|---|
| `{env}`, `{repo}`, `{repo_dir}`, `{config_dir}`, `{namespace}`, `{<var>}` | Huginn placeholders and the profile's `vars` (§3.6). `{repo_dir}` is `repos_root/<repo>` from `huginn.yaml`, or the repository's `path` (§3.11); `{namespace}` the first namespace of the repository's workloads. An unknown name is an error at load time. |
| `${KEY}`, `${KEY:-default}` | The value of `KEY` in the merged sources (§3.4); the default when absent or empty. |

Every string value is one of:
- a **literal**: `scram-sha-512`, `broker-1:9093,broker-2:9093`;
- `${KEY}`: from the sources;
- `env:VAR`: from the process environment (personal credentials kept nowhere);
- `file:<path>`: the content of a file (a token, a PEM certificate), trailing newline removed;
- `sops:<file>#<key>`: one key of an encrypted file, as `namespace_from` already does;
- `cmd:<command>`: the standard output of a command run without a shell, split on spaces, quotes respected (`cmd:gcloud auth print-access-token`); it runs when the screen opens, its output stays in memory;
- for paths: absolute, `~/…`, relative to the config folder, usually built from `{repo_dir}`; globs allowed where noted, and they must match exactly one file.

A value that cannot be resolved stops only what depends on it (one topic, or the connection) and the screen names the missing key or file.

### 3.4 `sources`: where values come from
An ordered list; values are merged and a key in a later source replaces an earlier one. Each source has exactly one of `file`, `command`, `env`, `kubernetes_secret`, `kubernetes_configmap`, `values`.

| Key | Meaning |
|---|---|
| `file` | Path (glob allowed, one match). |
| `command` | A command whose standard output is read like a file (`vault kv get -format=json …`, `gcloud secrets versions access …`). Run without a shell, output kept in memory. |
| `env` | The process environment; with `prefix`, only the variables starting with it. |
| `kubernetes_secret` | `{name, namespace, context}`: a Secret read with the environment's kube context (a read-only `get`; needs that RBAC permission). `data` is base64-decoded. `namespace` defaults to `{namespace}`. |
| `kubernetes_configmap` | `{name, namespace, context}`: same for a ConfigMap. |
| `values` | Inline key/value map (handy for overrides in `repos:`). |
| `format` | `dotenv`, `properties`, `yaml`, `json`, `ini`, `raw` (the whole content as the key given by `key`). Default: from the extension (`.env`, `.properties`, `.yaml`/`.yml`, `.json`, `.ini`), `dotenv` for commands. YAML and JSON are flattened to dotted keys (`spring.kafka.bootstrap-servers`, `data.PASSWORD`, list items as `servers.0`). |
| `prefix` | Read only this sub-tree (YAML/JSON) or keys starting with it, and strip it: `prefix: data.` turns `data.PASSWORD` into `PASSWORD`. |
| `add_prefix` | Prepend this to every key read (to keep two sources apart). |
| `keys` | Only these keys (globs). |
| `rename` | Map `from → to` applied after reading. |
| `base64` | Decode every value. |
| `sops` | Decrypt with sops first, in memory (the existing adapter, extended to every format). |
| `sops_args` | Extra sops arguments (`--config`, `--age-key-file` …). |
| `optional` | A missing file, Secret or failing command is skipped instead of being an error. |
| `key` | With `format: raw`: the key that receives the content. |

No source at all is valid: every value can be a literal, `env:`, `file:`, `sops:` or `cmd:`.

### 3.5 `connection`
| Key | Req. | Meaning |
|---|---|---|
| `bootstrap` | yes | `host:port` list, comma separated (or a YAML list). |
| `security` | yes | `plaintext`, `ssl`, `sasl_plaintext`, `sasl_ssl` (case ignored, `-` or `_`). |
| `sasl.mechanism` | with `sasl_*` | `plain`, `scram-sha-256`, `scram-sha-512`, `oauthbearer`, `gssapi`, `aws_msk_iam`. |
| `sasl.username`, `sasl.password` | plain, scram | Values (§3.3). |
| `sasl.authzid` | | Authorization identity (plain). |
| `sasl.oauth.token` | oauthbearer | A static token value (`cmd:…`, `file:…`, `env:…`), re-read on expiry. |
| `sasl.oauth.token_url`, `client_id`, `client_secret`, `scope`, `audience`, `extensions` | oauthbearer | OAuth 2 client-credentials flow instead of a static token. |
| `sasl.oauth.provider` | oauthbearer | `static`, `client_credentials`, `gcp` (Application Default Credentials, for Google Managed Kafka). Default: from the keys given. |
| `sasl.gssapi.service_name`, `realm`, `username`, `password`, `keytab`, `krb5_conf` | gssapi | Kerberos. |
| `sasl.aws.region`, `access_key`, `secret_key`, `session_token`, `profile`, `role_arn` | aws_msk_iam | AWS MSK IAM; without keys, the default AWS credential chain. |
| `tls.ca` | | CA certificates: a file (PEM, PKCS12 `.p12`/`.pfx`, Java `.jks`), or a value holding PEM text. Several allowed (list). Without it, the system roots. |
| `tls.ca_format` | | `pem`, `pkcs12`, `jks` when the extension does not tell. |
| `tls.ca_password` | | Password of a PKCS12 or JKS truststore. |
| `tls.system_roots` | | Also trust the system roots when `ca` is given. Default `false`. |
| `tls.cert`, `tls.key`, `tls.key_password` | | Client certificate for mTLS: PEM files or values. |
| `tls.keystore`, `tls.keystore_password`, `tls.keystore_format`, `tls.key_alias` | | Client certificate from a PKCS12 or JKS keystore instead. |
| `tls.server_name` | | TLS server name when it differs from the broker host. |
| `tls.min_version` | | `1.2` (default) or `1.3`. |
| `tls.insecure_skip_verify` | | Do not verify the brokers' certificates. Default `false`. When `true`, the header shows `TLS NOT VERIFIED` in the warning colour. |
| `shared_credentials` | | `true` when the credentials are the application's own: shows the quota notice (§2.4). Default `false`. |
| `client_id`, `rack` | | Override of `huginn.yaml` `kafka.client_id`; `rack` enables fetching from the closest replica when the cluster allows it (KIP-392), to keep load off the leaders. |
| `max_version` | | Highest Kafka protocol version to use (`2.8`, `3.6` …), for old or picky brokers. Default: negotiated. |
| `connect_timeout`, `request_timeout` | | Overrides of `huginn.yaml`. |

### 3.6 `vars`
Free variables, written `{name}` in any string of the profile. They exist so one profile can serve accounts or files whose names follow a pattern: `${{account}_PASSWORD}` reads `ORDERS_PASSWORD` when `account: ORDERS`. Defined on the profile, overridden per repository (§3.11) and per topic (§3.7). A var value may itself use `{env}`, `{repo}` and `${KEY}`. Names: lower-case letters, digits, `_`; the placeholder names of §3.3 are reserved.

### 3.7 `topics`
| Key | Meaning |
|---|---|
| `consume`, `produce` | Topics with a direction. |
| `list` | Topics without a direction. |
| `discover` | Key globs over the merged sources; every value found is a topic without a direction (values with commas or spaces are split). |
| `discover_brokers` | Glob over the topic names the brokers report (`orders.*`): every topic the credentials can describe and that matches is added without a direction. Off when absent. |
| `exclude` | Topic name globs never shown (`__*`, `*.retry`), applied last. |
| `defaults` | Display options (§3.9) applied to every topic of the profile. |

A topic is a string (literal or `${KEY}`) or an object:

| Key | Meaning |
|---|---|
| `name` | Required. Literal or `${KEY}`. |
| `label` | Name shown in the list instead of the topic name. |
| `vars`, `connection` | Override the profile's for this topic only (another account, another cluster). |
| `partitions` | Partitions read by default (`[0, 3]`); `S` still picks others. |
| `start` | Window when the topic opens: `tail`, `tail:N`, a duration (`15m`), or `offset:<n>` on every partition. Default: `huginn.yaml` `kafka.default_start`. |
| `isolation` | Override of the initial isolation. |
| display options | `key`, `value`, `headers`, `redact`, `columns` (§3.9). |

Huginn opens **one client per distinct connection** of the screen and closes them all on leaving.

### 3.8 `network`
All off by default: brokers are reached directly.

| Key | Meaning |
|---|---|
| `proxy` | `socks5://[user:pass@]host:port` (values allowed): every broker connection goes through it. |
| `address_map` | Map `advertised host:port → reachable host:port` (globs allowed on the left, `{1}` captures on the right). Solves brokers that advertise internal names a workstation cannot resolve. |
| `resolve` | Map `host → IP`, a local DNS override for broker names. |
| `tunnel` | `none` (default) or `port_forward`: reach each broker through a Kubernetes port-forward to its pod, with the environment's kube context. Needs `tunnel_pods` (a label selector or pod name pattern, with `{1}` the broker id), `tunnel_namespace`, `tunnel_port`; implies an `address_map` to the local ports. A port-forward creates no resource and changes nothing in the cluster, but needs the `pods/portforward` permission; the header shows `via port-forward`. |
| `dial_keepalive` | TCP keep-alive. Default `30s`. |

### 3.9 Display options (per topic, or `topics.defaults`)
| Key | Meaning |
|---|---|
| `value.format`, `key.format` | `auto` (default: JSON, then text, then hex), `json`, `text`, `hex`, `base64`, `avro`, `protobuf`, `json_schema`, `msgpack`, `cbor`. |
| `value.encoding`, `key.encoding` | Text encoding for `text` (`utf-8` default, `latin1`, `utf-16`). |
| `value.wire` | `none` or `confluent` (magic byte + schema id); `auto` detects it. |
| `value.schema` | A local schema instead of a registry: `.avsc` for Avro, a `.proto` file or a compiled descriptor set (`.pb`/`.desc`) with `value.message` (fully qualified message name) for Protobuf, a JSON Schema file. |
| `value.compression` | `none` (default) or `gzip`, `zstd`, `snappy`, `lz4`: an application-level compression inside the value. Kafka's own batch compression is always handled. |
| `headers.format` | Same values as `value.format`, for header values. Default `text`. |
| `headers.hide` | Header name globs not shown on the stream (still in zoom). |
| `redact` | JSON paths (`card.number`, `*.password`) or header names whose values are replaced by `[redacted]` on screen, in zoom and in copies. Added to the existing domain `Redactor`. |
| `columns` | Fields of the value shown as columns on the stream: `{name, show}` with the layout template language (`{field:order.id}`), like `layouts/`. Default: the one-line preview. |
| `time` | `record` (default: the record timestamp) or a JSON path in the value used as the time column and for sorting. |
| `level` | A JSON path whose value is mapped to a level (`error`, `warn` …) with the same `levels` rules as `formats/`, so `e`/`w` filters work on records too. |

### 3.10 `schema_registry` (optional)
Decoding Avro, Protobuf and JSON Schema values written with a schema registry. Huginn only **reads** schemas.

| Key | Meaning |
|---|---|
| `url` | Registry URL (value). Required to enable it. |
| `username`, `password` | Basic auth. |
| `token` | Bearer token (`cmd:`, `file:`, `env:` …). |
| `tls` | Same keys as `connection.tls`. |
| `proxy` | Same as `network.proxy`. |
| `timeout` | Default `10s`. |
| `cache` | Schemas kept in memory per screen. Default `256`. |

Requests are limited to `GET /schemas/ids/{id}` and `GET /subjects/…/versions/…` by an HTTP guard of the same kind as §2.2: any other method or path is refused before it is sent.

### 3.11 `repos`: per repository
A map from repository name (or glob) to:
- `path`: the repository folder when it is not `repos_root/<repo>`;
- `vars`, `connection`, `network`, `schema_registry`, `sources`: merged over the profile's (`sources` are appended after the profile's unless `sources_replace: true`);
- `topics`: added to the profile's (`topics_replace: true` to replace them);
- `enabled: false`: no Kafka screen for this repository.

Overrides are merged key by key: a repository changing `tls.ca` keeps the profile's `sasl`.

### 3.12 `groups` (optional, off by default): the application's consumer lag
Read the committed offsets of the application's own consumer groups and show the lag per partition next to the topic, **without joining the groups**.

| Key | Meaning |
|---|---|
| `names` | Group ids (values: `${<GROUP_KEY>}`), or key globs over the sources with `discover`. |
| `refresh` | Default `10s`. |

When `groups` is present, and only then, the allow-list of §2.2 also accepts **OffsetFetch** (9) and **FindCoordinator** (10) — both read-only. JoinGroup, SyncGroup, Heartbeat, LeaveGroup and OffsetCommit stay refused. Needs the `DESCRIBE` permission on the groups.

### 3.13 `huginn.yaml`: `kafka:` section (optional, neutral technical defaults)
| Key | Default | Meaning |
|---|---|---|
| `kafka.tail_records` | `100` | Records per partition loaded by the tail (`0`). |
| `kafka.default_start` | `tail` | Window when a topic opens: `tail`, `tail:N` or a duration. |
| `kafka.max_records` | `20000` | Records kept in memory per screen. |
| `kafka.fetch_max_bytes` | `1MiB` | Bytes per fetch response. |
| `kafka.partition_fetch_max_bytes` | `256KiB` | Bytes per partition per fetch. |
| `kafka.fetch_max_wait` | `500ms` | How long a broker may hold a fetch while following. |
| `kafka.max_records_per_second` | `500` | Live records shown per second; beyond, the screen says how many were skipped. |
| `kafka.max_value_bytes` | `1MiB` | Larger values are kept truncated, with their real size shown. |
| `kafka.client_id` | `huginn` | Kafka client id. |
| `kafka.connect_timeout` | `10s` | Time to reach the brokers and authenticate before the screen says they are unreachable. |
| `kafka.request_timeout` | `30s` | Time allowed for one Metadata, ListOffsets or Fetch request. |
| `kafka.metadata_refresh` | `5m` | How often partitions and leaders are refreshed while the screen is open. |
| `kafka.isolation` | `read_uncommitted` | Isolation when a screen opens. |
| `kafka.time_format` | `15:04:05.000` | Time column, Go layout. |

Keys of the Kafka screen are in `ui.yaml` `keymap` as for every screen (`kafka`, `kafka_partitions`, `kafka_isolation`).

### 3.14 Validation (in `internal/config`, with file, line and column)
- Structure: strict keys, known enum values, positive sizes and durations, placeholder names known, `${…}`, `cmd:` and globs well formed, `{repo_dir}` needs `repos_root` or a `path`, `sasl` keys matching the mechanism, a topic object has a `name`, `tunnel: port_forward` has its pod and port keys, `value.message` given with a Protobuf descriptor set, one source kind per source.
- What depends on files, commands or the cluster (sources, keys, certificates, schemas) is checked when the screen opens, since it depends on the repository and environment.
- No application value in code or as a default (D-030); `archtest` keeps checking it.

### 3.15 Shapes the same folder supports
1. **Everything literal**, for a shared dev cluster: `bootstrap: broker:9092`, `security: plaintext`, `topics.list: [orders]`, no source.
2. **Personal read-only account**: `username: env:KAFKA_USER`, `password: env:KAFKA_PASSWORD`, bootstrap from the repository.
3. **Everything from the repository**: dotenv overlays decrypted with sops, per-topic accounts through `vars`, PKCS12 truststore found by glob, topics by `discover`. This is the shape of the original debug script; its profile goes in `examples/config/kafka/` with neutral names.
4. **Spring Boot properties**: `application-{env}.yaml` as a `yaml` source, `spring.kafka.bootstrap-servers` and friends read as dotted keys, the password from a Kubernetes Secret source.
5. **Managed cloud Kafka**: `oauthbearer` with `provider: gcp`, or `aws_msk_iam`, no secret file at all.
6. **Brokers only reachable in the cluster**: `network.tunnel: port_forward` with an `address_map`.
7. **Avro with a schema registry**: `value.format: avro`, `schema_registry.url` from the sources.

## 4. Opening the screen, step by step
1. Pick the first profile matching the repository (its `files` checked for the current environment); merge the repository's `repos:` entry over it.
2. Replace `{…}` placeholders.
3. Read the sources in order (files, sops, commands, Kubernetes Secrets/ConfigMaps), merge.
4. Build the topic list: listed, then discovered; resolve names.
5. Per topic: apply its `vars` and `connection`, resolve credentials, certificates, network and display options; group topics by identical connection.
6. Per group: one client (through the proxy or tunnel if any), Metadata for its topics only; `discover_brokers` adds the matching topics the brokers report.
7. On selecting a topic: ListOffsets (latest, or by timestamp), Fetch from `end - tail_records` or the window's offsets, merge partitions by timestamp, deliver batches at most ~30 times a second.
8. Each failure stops only what depends on it and says why on the screen.

## 5. Architecture

| Layer | Addition |
|---|---|
| `core/domain` | `KafkaTopic{Name, Direction}` (Consume, Produce, Both, None), `KafkaRecord{Topic, Partition, Offset, Time, TimeType, Key, Value, ValueSize, Headers}`, `KafkaStart`, `KafkaIsolation`, `KafkaConnection` (credentials as `Secret`, CA bytes), placeholder expansion and `${…}` resolution (pure functions), a bounded `RecordBuffer`, record filtering on the existing filter engine. |
| `core/ports` driven | `TopicSource` and `TopicSourceFactory` (connection → source); `ValueSources` (read, decrypt and flatten a source into keys); `CertificateLoader` (CA pool and client certificate from PEM, PKCS12, JKS); `TokenSource` (OAuth tokens); `RecordDecoder` (bytes → decoded value, by format); `SchemaResolver` (schema by id); `GroupOffsets` (optional lag). |
| `core/ports` driving | `KafkaCatalog` (does a profile match this repository and environment, cheap) and `TopicSession` (resolve, list topics, open a topic). |
| `core/app` | `KafkaServices`: the steps of §4, limits, merging, batching. No I/O of its own. |
| `adapters/driven/kafka` | The only package importing franz-go: client options, SASL mechanisms (plain, scram, oauth, kerberos, aws), dialer with proxy, address map, DNS overrides and the request guard (§2.2), offsets and fetch, optional group offsets, error classification (`ErrUnauthorized`, `ErrUnreachable`, `ErrUnknownTopic`). |
| `adapters/driven/kubernetes` | Adds read-only `GetSecret`, `GetConfigMap` and a port-forward dialer for `network.tunnel`. |
| `adapters/driven/valuesource` | file (dotenv, properties, YAML, JSON, ini, raw), command, process environment and inline sources; flattening, `prefix`, `keys`, `rename`, `base64`; `sops: true` goes through the `SecretsProvider`. Kubernetes Secret and ConfigMap sources go through a new read-only method of the Kubernetes adapter. |
| `adapters/driven/sops` | Extended from dotenv to the four formats (`--input-type` from the source format). |
| `adapters/driven/certs` | PEM, PKCS12 (`software.sslmate.com/src/go-pkcs12`) and JKS (`github.com/pavlo-v-chernykh/keystore-go`), truststores and keystores. |
| `adapters/driven/oauth` | OAuth tokens: static value, client credentials (`golang.org/x/oauth2`), GCP ADC. |
| `adapters/driven/schemaregistry` | Read-only HTTP client with the GET guard of §3.10 and a schema cache. |
| `adapters/driven/kafkapayload` | Decoders registered by name (`ports.Registry`): auto, json, text, hex, base64, avro (`github.com/hamba/avro/v2`), protobuf (`google.golang.org/protobuf` dynamic messages), json_schema, msgpack, cbor; Confluent wire format; application-level decompression; redaction and columns. |
| `adapters/driven/demo` | Synthetic topics and records for `--demo`. |
| `driving/tui` | `kafka.go`, `kafkazoom.go`, `partitionpicker.go`; actions `kafka`, `kafka_partitions`, `kafka_isolation`; golden files. |
| `config` | `KafkaProfile` structs and `Huginn.Kafka`, validation, `make schema` → `docs/schema/kafka.schema.json`, `kafka` added to the folder's known names, `docs/CONFIG.md` section 10. |
| `bootstrap` | Builds the Kafka graph only when `kafka/` has at least one profile. |
| `archtest` | Rules for the new packages; each third-party library confined to its package; the `forbidigo` list of §2.2. |
| `portstest` | Fakes for the new ports; `RunTopicSourceContract` on the demo adapter and on `kfake`, with the allow-list assertion. |

## 6. Records with or without a declared format
Nothing has to be declared: by default each value is shown in the best readable form detected per record (JSON, text, hex), and Confluent framing is recognised and labelled. When the user wants more, the display options (§3.9) choose a decoder per topic, with a local schema or a schema registry (§3.10), plus redaction, columns, time and level fields.

## 7. Steps
1. **K0 — config**: every key of §3 in the structs, validation and schema (even the ones implemented later, so the format is stable), `docs/CONFIG.md`, examples (the shapes of §3.15); `valuesource` (files, env, inline, commands) and `certs` (PEM, PKCS12, JKS) adapters; sops formats.
2. **K1 — core and demo**: domain, ports, app, fakes, demo adapter, TUI screen and goldens. Works with `--demo`.
3. **K2 — real brokers**: franz-go adapter with plain/scram, TLS and mTLS, request guard, `kfake` contract suite, error classification.
4. **K3 — comfort**: windows by timestamp, partition picker, isolation switch, header filters, copy, redaction, columns, time and level fields.
5. **K4 — decoders**: Avro, Protobuf, JSON Schema with local schemas and the schema registry; msgpack, cbor, compression.
6. **K5 — more ways in**: OAuth (static, client credentials, GCP), Kerberos, AWS MSK IAM; proxy, address map, DNS overrides; Kubernetes Secret/ConfigMap sources; port-forward tunnel; consumer group lag.

Until a step lands, a key it covers is accepted by validation and reported on the screen as `not supported yet: <key>` instead of being ignored.

## 8. Out of scope
- Producing, replaying, resetting offsets, deleting or creating anything (never, whatever the config says).
- Joining a consumer group or committing an offset (never; §2.1).
- Writing decoded records or secrets to disk (export is an explicit `ctrl+s` of the visible records only, through the existing save action, with redaction applied).

## 9. Choices settled before coding
- **Heavy dependencies stay optional.** Kerberos (gokrb5) and the AWS default credential chain (aws-sdk-go-v2) weigh more than the rest of Huginn. They are built only with the build tags `kafka_gssapi` and `kafka_aws`; without them, the keys validate and the screen says `not built with kafka_gssapi`. Static AWS keys work without the tag (franz-go signs them itself).
- **`--demo`.** The demo replaces the `TopicSourceFactory` and the `ValueSources` with synthetic ones; `examples/config/kafka/` holds a profile with literal values and no `match.files`, so every demo repository with a Kafka role shows the screen without any file or broker.
- **Order of work.** K0 → K1 → K2 cover the original script entirely (dotenv + sops, SCRAM, PKCS12 truststore, discover, tail and windows). K3 to K5 follow, each a separate commit series with its own plan update if the design moves.
