# Writing a config folder

Huginn knows nothing about your applications. Everything specific to them comes from a **config folder** that you write:
- where your environments live;
- how a workload maps to a repository;
- which containers are sidecars;
- how a log line is read;
- how a log line is drawn;
- where the Kafka settings of a service are, optionally.

This page is the complete reference for that folder.

- [1. Starting Huginn with a folder](#1-starting-huginn-with-a-folder)
- [2. The folder](#2-the-folder)
- [3. `huginn.yaml`](#3-huginnyaml)
- [4. `environments.yaml`](#4-environmentsyaml)
- [5. `services.yaml`](#5-servicesyaml)
- [6. `containers.yaml`](#6-containersyaml)
- [7. `ui.yaml`](#7-uiyaml)
- [8. `formats/<name>.yaml`](#8-formatsnameyaml): reading lines
- [9. `layouts/<name>.yaml`](#9-layoutsnameyaml): drawing lines
- [10. `kafka/<name>.yaml`](#10-kafkanameyaml): Kafka topics, read only
- [11. Errors](#11-errors)
- [12. Your folder in 15 minutes](#12-your-folder-in-15-minutes)

Complete, tested examples:

| Folder | Case |
|---|---|
| [`examples/config/`](../examples/config) | Spring Boot 3, JSON logs from the logstash encoder, Kubernetes metadata added by the log agent. Also the folder used by `--demo`. |
| [`examples/config-node/`](../examples/config-node) | Node.js with pino: numeric levels, epoch milliseconds, a column taken from any JSON path |
| [`examples/config-nginx/`](../examples/config-nginx) | nginx access lines read with a regular expression, next to JSON application logs: two formats chosen per container |
| [`examples/config-kafka/`](../examples/config-kafka) | Kafka profiles: settings read from a repository's dotenv overlays (one encrypted with sops), and a local cluster written by hand |

**Editor completion**: each example file starts with a `# yaml-language-server: $schema=…` line pointing to the JSON schemas in [`docs/schema/`](schema). Editors that support it (VS Code with the YAML extension, JetBrains IDEs) then complete keys and show their documentation. Keep that line when you copy a file, adjusting the relative path.

## 1. Starting Huginn with a folder

```
huginn --config ~/work/acme-huginn rec     # explicit folder
HUGINN_CONFIG=~/work/acme-huginn huginn    # environment variable
huginn                                     # <user config dir>/huginn/
huginn --demo                              # synthetic cluster + the embedded examples/config
```

| Order | Source |
|---|---|
| 1 | `--config <folder>` |
| 2 | `HUGINN_CONFIG` |
| 3 | `<user config dir>/huginn/`. On Linux this is `~/.config/huginn/`; on macOS `~/Library/Application Support/huginn/`; on Windows `%AppData%\huginn\`. |

`~` is expanded. `--demo` uses the embedded example folder unless `--config` is also given.

Huginn reads and checks the whole folder **before** the terminal UI opens:
- if the folder is missing, Huginn prints the expected structure;
- if the folder is invalid, it prints **every** problem with its file, line and column (see [Errors](#11-errors)).

In both cases it exits with code 2. It never starts with a partly understood folder.

## 2. The folder

```
acme-huginn/
├── huginn.yaml           required   default environment, time windows, limits
├── environments.yaml     required   kube contexts and namespaces
├── services.yaml         required   how workloads map to repositories
├── containers.yaml       optional   sidecars to hide
├── ui.yaml               optional   theme, keymap, key bar, columns
├── formats/              required   at least one .yaml file, one format per file
│   └── <name>.yaml
├── layouts/              required   at least one .yaml file, one layout per file
│   └── <name>.yaml
└── kafka/                optional   Kafka profiles, one per file
    └── <name>.yaml
```

Rules common to every file:
- **The names are fixed.** Any other file or folder is an error with a suggestion ("did you mean environments.yaml?"), so a typo never goes unnoticed. Hidden files (`.git`, `.gitignore`) and Markdown files (`README.md`) are ignored, so the folder can live in its own git repository with its own notes.
- **Every file starts with `version: 1`.** This is the version of the structure described here. A future Huginn that changes the structure will recognise and report older files instead of misreading them.
- **Formats, layouts and Kafka profiles are named after their file**: `formats/spring-json.yaml` is the format `spring-json`. Names use lower-case letters, digits, `.`, `_` and `-`. Both `.yaml` and `.yml` work.
- **Decoding is strict**: unknown keys, wrong types and unknown values are errors.
- **Optional keys may be left out.** The defaults below are neutral technical values. Huginn has no default environment, format, layout, label or sidecar.

Types used below:

| Type | Written as |
|---|---|
| string | `rec`, `"15:04:05.000"` (quote values containing `:`, `{`, `#`, `*` or starting with `@`) |
| list | `[a, b]` or one `- item` per line |
| paths | a string or a list of strings: `message` or `[message, msg]` |
| duration | `15m`, `1h`, `2d` (minutes `m`, hours `h`, days `d`) |
| glob | `*` any text, `?` one character, `[abc]` one of: `payment-*`, `nginx*`, `5*` |

## 3. `huginn.yaml`

General settings.

```yaml
version: 1
default_env: rec
repos_root: ~/work/repos
windows:
  presets: [15m, 30m, 40m, 45m, 1h, 1d, 2d]
  tail_lines: 500
  head_lines: 500
  default: 15m
logs:
  buffer_lines: 50000
demo:
  seed: 42
  rate: 1
```

| Key | Type | Req. | Default | Meaning |
|---|---|---|---|---|
| `version` | int | yes | | Must be `1`. |
| `default_env` | string | yes | | Environment opened by `huginn` without argument. It must be a key of `environments.yaml`. `HUGINN_ENV`, `-e` and the positional argument override it. |
| `repos_root` | string | | | Folder containing your repositories. Needed by the `manifests` rule of `services.yaml` and by `{repo_dir}` in `kafka/`. |
| `windows.presets` | list of durations | | `15m 30m 40m 45m 1h 1d 2d` | Windows of keys `1`…`7`, in order; at most 7. Tail (key `0`) and head (key `9`) are always there and are not presets. |
| `windows.tail_lines` | int | | `500` | Lines loaded by the tail window (key `0`). |
| `windows.head_lines` | int | | `500` | Lines loaded **per container** by the head window (key `9`): the first lines the node still keeps. At most `logs.buffer_lines`. |
| `windows.default` | duration, `tail` or `head` | | `15m` | Window used when a logs screen opens. `--since` overrides it. `tail:N` and `head:N` set the size. |
| `logs.buffer_lines` | int | | `50000` | Lines kept in memory per logs screen; older ones are dropped. At least 1000. |
| `demo.seed` | int | | `42` | `--demo` only: the same seed gives the same synthetic cluster. |
| `demo.rate` | number | | `1` | `--demo` only: live lines per second per pod. |
| `kafka.*` | | | | Limits of the Kafka screen, see [Kafka limits](#limits-huginnyaml-kafka). |

## 4. `environments.yaml`

Where each environment lives. The environment picker (`ctrl+e`) lists them **in file order**.

```yaml
version: 1
environments:
  rec:
    context: gke_acme_europe-west1_main
    namespaces: [app-rec]
  prd:
    context: gke_acme_europe-west1_main
    namespace_from: sops:~/work/infra/overlays/prd/config.env#K8S_NAMESPACE
    production: true
```

| Key | Type | Req. | Default | Meaning |
|---|---|---|---|---|
| `version` | int | yes | | Must be `1`. |
| `environments` | map | yes | | At least one environment. Names use lower-case letters, digits and `-`, 32 characters at most. |
| `environments.<env>.context` | string | | current context | kubeconfig context. It must already exist, for example after `gcloud container clusters get-credentials`. Huginn never logs in for you. |
| `environments.<env>.namespaces` | list | one of the two | | Namespaces holding the environment's workloads. |
| `environments.<env>.namespace_from` | string | one of the two | | Read the namespace from a sops-encrypted dotenv file: `sops:<file>#<key>`. A relative `<file>` is relative to the config folder (for example `sops:../infra/overlays/prd/config.env#K8S_NAMESPACE`); `~/` is your home folder. The `sops` command must be installed and able to decrypt the file with your usual keys (age, GCP KMS…). It runs once, when the environment is first opened; the decrypted content stays in memory. If it fails, the services screen shows sops' reason. |
| `environments.<env>.production` | bool | | `false` | Shows the red production banner. |

Several environments may share one context (one cluster, one namespace each) or use different clusters.

## 5. `services.yaml`

The services screen shows **repositories**, not raw workloads. This file says how each Deployment, StatefulSet, DaemonSet or CronJob is attached to a repository.

```yaml
version: 1
resolve: [explicit, labels, manifests]
label_keys: [app.kubernetes.io/part-of, app.kubernetes.io/name, app]
manifests:
  overlay_glob: "**/overlays/{env}"
  files: [kustomization.yaml]
explicit:
  - repo: payment-service
    workloads:
      - { env: rec, name: payment-service }
      - { env: rec, name: payment-worker, kind: Deployment }
```

| Key | Type | Req. | Default | Meaning |
|---|---|---|---|---|
| `version` | int | yes | | Must be `1`. |
| `resolve` | list of `explicit`, `labels`, `manifests` | yes | | Rules tried in order for each workload; the first one that names a repository wins. Workloads no rule attributes are listed as "(no repo)" under their own name. |
| `label_keys` | list | with `labels` | | The value of the first of these workload labels (or annotations) is the repository name. |
| `manifests.overlay_glob` | glob | with `manifests` | | Where an environment's overlay lives inside a repository under `repos_root`; `{env}` is replaced by the environment name. |
| `manifests.files` | list | | | Entry files read in the overlay folder. |
| `explicit` | list | with `explicit` | | Repositories listed by hand. |
| `explicit[].repo` | string | yes | | Repository name as shown on the services screen. |
| `explicit[].workloads[].env` | string | yes | | A key of `environments.yaml`. |
| `explicit[].workloads[].name` | string | yes | | Workload name. |
| `explicit[].workloads[].namespace` | string | | first namespace of the environment | |
| `explicit[].workloads[].kind` | `Deployment`, `StatefulSet`, `DaemonSet`, `CronJob` | | `Deployment` | |
| `standalone_pods` | bool | | `true` | List the pods that no known workload owns as rows of their own, grouped by owner: a bare pod (`debug-shell (Pod)`), a Job made by hand (`migrate (Job)`), pods of a ReplicaSet whose Deployment is gone or of a controller Huginn does not know (`canary (Rollout)`). When shown this way, they go through the rules above with their pods' labels, so a debug pod labelled like an application joins its repository. Set `false` to not show them at all (e.g. on clusters full of finished one-off Jobs); a labelled orphaned pod then does not join its repository either, since it is never turned into a row to begin with. |

The `manifests` rule is accepted and validated, but it is read only from milestone M4 (the real cluster connection). Until then, use `labels` or `explicit`.

## 6. `containers.yaml`

Optional. It says which containers are **sidecars**: they are not counted in restarts, readiness or the version, they are listed under SIDECARS in the services preview, and the logs screen does not follow them in `app` mode (the default). Key `A` on the logs screen follows all containers, sidecars included; `S` chooses pods and containers. Without this file, every container is an application container.

```yaml
version: 1
hide: [istio-proxy, istio-init, vault-agent, cloud-sql-proxy]
always_show: []
show_init: false
default_mode: app      # app | all
```

| Key | Type | Default | Meaning |
|---|---|---|---|
| `version` | int | | Must be `1` (required). |
| `hide` | list | | Sidecars. A name matches that container and `<name>-*` (`vault-agent` also matches `vault-agent-init`); an image name matches every container running it (the last part of the image path without its tag: `proxyv2` for `docker.io/istio/proxyv2:1.24`). |
| `always_show` | list | | Container names always application containers, even if `hide` matches them. |
| `show_init` | bool | `false` | Treat init containers as application containers. |
| `default_mode` | `app`, `all` | `app` | Containers a logs screen opens on. `app`: application containers (and an init container blocking the pod, whose output says why). `all`: every container, sidecars and init containers included. `A` switches during a session; `huginn --containers all` overrides it for one run. |

A container named after its workload is always an application container. Init containers are sidecars unless `show_init` is true, or they are listed in `always_show`.

**Why sidecars are not followed by default:** a mesh sidecar often logs more than the application (one access line per request). The logs screen keeps a bounded buffer shared by all containers, so following sidecars all the time would push the application's lines out sooner. In `all` mode, the status bar says when older lines no longer fit.

## 7. `ui.yaml`

Optional. These are personal display choices, usually not shared by a team.

```yaml
version: 1
theme: light
key_bar: compact
keymap:
  follow: [f, ctrl+l]
log_columns: [time, level, logger]
```

| Key | Type | Default | Meaning |
|---|---|---|---|
| `version` | int | | Must be `1` (required). |
| `theme` | `light`, `accessible`, `classic`, `none` | `light` | `--theme`, `NO_COLOR` and `HUGINN_THEME` override it. |
| `paint_background` | bool | `false` | Paint the theme background instead of keeping the terminal's. |
| `key_bar` | `compact`, `full`, `hidden` | `compact` | Key bar at the bottom (`f2` cycles it). |
| `keymap` | map action → keys | | Replaces all default keys of an action. The action names are those of the help screen (`?`) and the README; for example `follow`, `filter`, `columns_cycle`. |
| `log_columns` | list | | Columns shown when a logs screen opens: `pod` and column names from `layouts/`. Without it, every visible column is shown and narrowed automatically. |

## 8. `formats/<name>.yaml`

A format says how to **read** the lines of some containers. You can have several.

**Which format reads a container?** Formats are tried **in file name order**. The first one whose `match` accepts the repository and container wins. A format without `match` accepts everything, so give it a name that sorts last, such as `zz-default.yaml`, or number your files (`10-nginx.yaml`, `20-app.yaml`). Containers that no format accepts are read as plain text.

**Lines a format cannot parse** keep their text as the message: a JSON format meeting a non-JSON line, or a regex format meeting a line its pattern does not match. Their level is guessed from a level word near the start of the line. Nothing is ever dropped.

Common keys:

| Key | Type | Req. | Meaning |
|---|---|---|---|
| `version` | int | yes | Must be `1`. |
| `decoder` | `json`, `regex`, `plain` | yes | How lines are read (see below). |
| `match.repos` | list of globs | | Repositories read with this format; empty means any. |
| `match.containers` | list of globs | | Containers read with this format; empty means any. Both lists must accept a container when both are given. |
| `levels` | map level → spellings | | Extra spellings of each level in these logs, case ignored. The keys are `error`, `warn`, `info` and `debug`. Common spellings are already understood: `ERROR`, `ERR`, `FATAL`, `SEVERE`, `CRITICAL`, `WARN`, `WARNING`, `INFO`, `NOTICE`, `DEBUG`, `TRACE`, `FINE`, and klog letters. |
| `layout` | string | yes | Layout drawing these lines: a file name of `layouts/` without extension. |

### `decoder: json`: one JSON object per line

```yaml
version: 1
decoder: json
fields:
  time:     ["@timestamp", timestamp, time]
  level:    [level, severity, log.level]
  logger:   [logger_name, logger]
  thread:   thread_name
  message:  [message, msg]
  stack:    stack_trace
  trace_id: [traceId, trace_id]
  app:      [app, service.name]
  pid:      pid
levels:
  error: ["50"]
hidden: ["kubernetes.*", "@version", "host*"]
layout: spring
```

| Key | Type | Req. | Meaning |
|---|---|---|---|
| `fields.<field>` | paths | `message` is | Where each **standard field** is. Candidates are tried in order and the first one present wins. A path is first looked up as a key (`log.level` as one key), then as a walk into nested objects (`log` → `level`). |
| `hidden` | list of globs | | Fields never shown on the stream, only in the zoom view's metadata section. Typical use: the Kubernetes metadata your log agent adds. |

The standard fields are:

| Field | Meaning |
|---|---|
| `time` | The entry time. It is read from RFC 3339 text, or from a number of epoch seconds or milliseconds. Without it, the time the kubelet received the line is used. |
| `level` | The severity, mapped with `levels`. |
| `logger` | Logger or class name. |
| `thread` | Thread name. |
| `message` | The message. |
| `stack` | Stack trace, folded on the stream and shown in full in zoom. |
| `trace_id` | Correlation id, shown in zoom and searchable. |
| `app` | Application name. |
| `pid` | Process id. |

Some details of how lines are read:
- **Numbers** are shown as written in the line, so long ids keep all their digits.
- **Repeated keys**: when an object repeats a key, the first occurrence wins.
- **Hidden objects**: a hidden key that holds an object hides the whole object.
- **Invalid UTF-8**: bytes that aren't valid UTF-8 are shown as `�`.

Every other field of the object stays available:
- zoom shows it;
- text filters search it (`key=value`);
- layouts can draw it with `{field:<path>}`, where `<path>` is its dotted path, for example `http.status`.

### `decoder: regex`: text lines

```yaml
version: 1
decoder: regex
match: { containers: ["nginx*"] }
pattern: '^(?P<remote>\S+) \S+ \S+ \[(?P<time>[^\]]+)\] "(?P<message>[^"]*)" (?P<status>\d{3})'
time_format: "02/Jan/2006:15:04:05 -0700"
level_from:
  field: status
  map: { "5*": error, "4*": warn, "*": info }
layout: access
```

| Key | Type | Req. | Meaning |
|---|---|---|---|
| `pattern` | string | yes | A Go regular expression ([syntax](https://pkg.go.dev/regexp/syntax)) with **named groups** `(?P<name>…)`. Write it between single quotes in YAML. Groups named like standard fields fill them (`time`, `level`, `logger`, `thread`, `message`, `trace_id`, `app`, `pid`). Other groups become extra fields (`{field:status}`). A `message` group is required. |
| `time_format` | string | | Layout of the `time` group in Go's reference-date notation, as described under `time_format` in [Layouts](#9-layoutsnameyaml). Default: RFC 3339. |
| `level_from.field` | string | | A group whose value decides the level, for example an HTTP status. It must be a group of `pattern`. |
| `level_from.map` | map glob → level | | Value glob to `error`, `warn`, `info` or `debug`; the longest glob is tried first. |

`fields` and `hidden` are not used by this decoder.

### `decoder: plain`

This decoder reads nothing: the message is the line, and the level is guessed from a level word near the start. It is useful when you want a layout, and the columns picker, for containers whose lines have no structure.

## 9. `layouts/<name>.yaml`

A layout says how to **draw** the lines of the formats that name it.

```yaml
version: 1
stream:
  time_format: "15:04:05.000"
  columns:
    - { name: time,   key: t, show: "{time}",                      role: time }
    - { name: level,  key: l, show: "{level|right:5}",             role: level }
    - { name: thread, key: h, show: "[{thread|last:15|right:15}]", role: thread, hide_below: 140 }
    - { name: class,  key: c, show: "{logger|abbrev:30|left:30}",  role: logger, hide_below: 110 }
    - { name: trace,  key: x, show: "{trace_id|last:8}",           role: dim,    visible: false }
  separator: { text: ": ", after: [thread, class] }
zoom:
  time_format: "2006-01-02T15:04:05.000Z07:00"
  columns:
    - { name: time, show: "{time}", role: time }
    - { name: level, show: "{level|right:5}", role: level }
    - { name: app, show: "[{app}]", role: dim }
  separator: { text: ": " }
stack:
  framework_prefixes: [java., jakarta., org.springframework.]
```

A drawn line is:
1. the pod id, which belongs to Huginn (`I` cycles it, `p` in the picker);
2. the **columns** in order, each followed by one space;
3. the **separator**;
4. the **message**.

| Key | Type | Req. | Meaning |
|---|---|---|---|
| `version` | int | yes | Must be `1`. |
| `stream` | line | yes | The line on the logs screen. |
| `zoom` | line | | The first line of the zoom view (`enter`). Default: the stream line. |
| `stack.framework_prefixes` | list | | Stack frames starting with one of these prefixes are framework code and dimmed in zoom; the others are your code, in bold. A module prefix such as `java.base/` is ignored. |

A **line** has:

| Key | Type | Req. | Default | Meaning |
|---|---|---|---|---|
| `time_format` | string | | `15:04:05.000` | How `{time}` is written, as a Go reference layout (see below). |
| `columns` | list | yes | | At least one column. |
| `separator.text` | string | | | Text between the columns and the message, for example `": "`. |
| `separator.after` | list | | | Show the separator only when one of these columns is shown. Default: whenever any column is shown. |

A `time_format` is written as the reference date **Mon Jan 2 15:04:05 MST 2006** laid out the way you want. Its parts are:
- `2006` year, `01` month, `02` day;
- `15` hour, `04` minutes, `05` seconds, `.000` milliseconds;
- `Z07:00` time zone.

So `15:04:05.000` gives `19:12:40.104`.

A **column** has:

| Key | Type | Req. | Default | Meaning |
|---|---|---|---|---|
| `name` | string | yes | | Unique in its line. It is shown in the columns picker and the status bar, and used by `separator.after` and `ui.yaml` `log_columns`. The same name in several layouts is one column for the picker and `c`. |
| `show` | template | yes | | What the column shows (see below). |
| `role` | `time`, `level`, `thread`, `logger`, `pid`, `dim`, `plain` | | `plain` | Colour from the theme. Layouts never contain colours, so every theme and `NO_COLOR` work. `time` and `level` also have behaviour, described after this table. |
| `key` | one character | | | Stream only: the letter toggling the column in the columns picker (`C`). `p`, `z`, `r` and `f` are taken by the picker. |
| `hide_below` | int | | | Stream only: the column is hidden automatically when the terminal is narrower than this many cells, until you choose columns yourself. |
| `visible` | bool | | `true` | Stream only: `false` keeps the column hidden when a logs screen opens; the picker shows it. |

What `role` changes besides the colour:
- **`time`:** `ctrl+t` switches the format of these columns (local, UTC, relative) and shows them again if they were hidden.
- **`level`:** when every `level` column is hidden, WARN messages take the warning colour so the level stays visible.
- **`time` and `level` together:** the focus layout (`z`) keeps these columns and hides the others.

`c` hides the stream columns one by one **in the order of the file**, then shows them again.

### Templates

A template is text with placeholders:

```
[{thread|last:15|right:15}]
```

- A **placeholder** is `{field|filter|filter…}`. `field` is a standard field (`time`, `level`, `logger`, `thread`, `message`, `trace_id`, `app`, `pid`) or `field:<path>` for any other field of the line (`{field:http.status}`, `{field:req.method}`).
- `{level}` is written `ERROR`, `WARN`, `INFO`, `DEBUG`, or `-` when unknown.
- `{time}` follows `time_format` and the current timestamp mode (`ctrl+t`).
- Write `{{` and `}}` for literal braces.

Filters, applied left to right:

| Filter | Effect | Example |
|---|---|---|
| `left:N` | pad with spaces to N, text on the left | `{logger\|left:30}` |
| `right:N` | pad with spaces to N, text on the right | `{level\|right:5}` gives ` WARN` |
| `first:N` | keep the first N characters | `{trace_id\|first:8}` |
| `last:N` | keep the last N characters | `{thread\|last:15}` gives `nio-8080-exec-7` |
| `abbrev:N` | shorten a dotted name to N like Logback's `%logger{N}` | `io.gimle.payment.Gateway` gives `i.g.payment.Gateway` |
| `upper`, `lower` | change case | `{level\|lower}` |
| `default:x` | use `x` when the field is empty | `{logger\|default:-}` |

Two rules for columns:
- **A column whose fields are all empty is left out, with its space.** For example, `[{app}]` disappears when there is no app name. A `default` filter keeps the column.
- **Lines no format could parse keep only the columns that use nothing but `{time}`**, followed by the raw text.

## 10. `kafka/<name>.yaml`

Optional. A Kafka profile says **where the Kafka settings of some repositories are** and **which topics to show**, so Huginn can list the records of those topics, read only. Without a `kafka/` folder, nothing about Kafka exists in Huginn.

> **Status**: the Kafka screens (key `M` on the services screen, see the README) work with `--demo`, whose `examples/config/kafka/demo.yaml` lists topics of the demo services. Reading a real cluster arrives with the next step of milestone M5 ([`docs/plan/M5-kafka.md`](plan/M5-kafka.md)); until then a real run loads and checks the folder but shows no Kafka screen.

**Read only, always.** Huginn never joins a consumer group, never commits an offset, never produces and never creates a topic. No key of this file can change that. Reading does not take records away from the services that consume them.

**Which profile applies?** Profiles are tried **in file name order**. The first whose `match` accepts the repository wins, as for `formats/`. The environment is the one Huginn runs on (`huginn rec`, `-e`, `ctrl+e`); it is written `{env}`.

```yaml
version: 1

match:
  repos: ["*"]
  files: ["{repo_dir}/deploy/overlays/{env}/secrets/kafka.env"]

sources:                                   # merged in order, the last one wins
  - file: "{repo_dir}/deploy/base/kafka.env"
  - file: "{repo_dir}/deploy/overlays/{env}/kafka.env"
    optional: true
  - file: "{repo_dir}/deploy/overlays/{env}/secrets/kafka.env"
    sops: true

vars:
  account: APP

connection:
  bootstrap: ${KAFKA_BOOTSTRAP_SERVERS}
  security: ${KAFKA_SECURITY_PROTOCOL:-plaintext}
  sasl:
    mechanism: scram-sha-512
    username: ${{account}_USERNAME}
    password: ${{account}_PASSWORD}
  tls:
    ca: "{repo_dir}/src/main/resources/truststore.p12"
    ca_password: ${{account}_TRUSTSTORE_PASSWORD}

topics:
  discover: ["KAFKA_TOPIC_*"]

repos:
  orders:
    vars: { account: ORDERS }
    topics:
      consume: ["${KAFKA_TOPIC_ORDERS_IN}"]
      produce:
        - { name: "${KAFKA_TOPIC_ORDERS_OUT}", vars: { account: ORDERS_OUT } }
```

Every path, key name and topic above is an example: write those of your repositories. Huginn has no default for any of them.

### Placeholders and references

Two kinds of references, always replaced in this order:

| Written | Replaced by |
|---|---|
| `{env}` | The environment Huginn runs on. |
| `{repo}` | The repository name. |
| `{repo_dir}` | The repository folder: `repos_root` of `huginn.yaml` followed by the repository name, or the repository's `path` under `repos:`. |
| `{name}` | A variable of `vars` (lower-case letters, digits, `_`). |
| `${KEY}` | The value of `KEY` in the merged `sources`. A key that is missing is reported on the topic that needs it. |
| `${KEY:-default}` | The same, with a value used when the key is missing or empty. |
| `env:VAR` | The whole value read from the environment variable `VAR` of your shell, for credentials of your own. |

So `${{account}_PASSWORD}` reads `ORDERS_PASSWORD` when `account` is `ORDERS`. `$$` writes a literal `$`.

**Quoting**: a value that **starts** with `{` (such as `"{repo_dir}/x.env"`) and every reference written **inside** `[ ]` or `{ }` must be quoted: `consume: ["${TOPIC}"]`.

### Keys

| Key | Type | Req. | Meaning |
|---|---|---|---|
| `version` | int | yes | Must be `1`. |
| `match.repos` | list of globs | | Repositories this profile applies to. Empty means any. A repository listed under `repos:` is accepted too. |
| `match.files` | list of path globs | | The profile applies only when each glob matches at least one existing file. Huginn only checks that the files exist: nothing is read or decrypted until the Kafka screen opens. Use it so that only repositories with Kafka settings in the current environment get a Kafka screen. |
| `sources` | list | | Dotenv files read in order and merged; a key in a later file replaces the earlier value. |
| `sources[].file` | path | yes | A dotenv file: `KEY=VALUE` lines; blank lines and `#` comments are skipped, `export ` is ignored, quotes around a value are removed. A glob must match exactly one file. |
| `sources[].sops` | bool | | Decrypt the file with `sops` first. The decrypted content stays in memory. |
| `sources[].optional` | bool | | A missing file is skipped instead of being an error (an overlay that does not exist for every environment). |
| `vars` | map | | Variables written `{name}`. `env`, `repo` and `repo_dir` are reserved. Their values are used as written. |
| `connection.bootstrap` | string | yes | Brokers, `host:port`, comma separated. |
| `connection.security` | string | yes | `plaintext`, `ssl`, `sasl_plaintext` or `sasl_ssl`; case is ignored, so `SASL_SSL` read from a file works. |
| `connection.sasl.mechanism` | string | with `sasl_*` | `plain`, `scram-sha-256` or `scram-sha-512` (case ignored). |
| `connection.sasl.username`, `password` | string | with `sasl_*` | |
| `connection.tls.ca` | path | | Certificates trusted for the brokers: a PEM file (`.pem`, `.crt`, `.cer`) or a PKCS12 file (`.p12`, `.pfx`) such as a Java truststore. A glob must match exactly one file. Without it, the system's certificates are trusted. |
| `connection.tls.ca_password` | string | | Password of a PKCS12 file. |
| `topics.consume`, `topics.produce` | list | | Topics the services consume or produce, shown in two groups. |
| `topics.list` | list | | Topics without a direction. |
| `topics.discover` | list of globs | | Globs on the keys of the sources: the value of each matching key is a topic (a value with commas gives several). |
| `repos.<repo>.path` | path | | The repository folder when it is not `repos_root/<repo>`. |
| `repos.<repo>.enabled` | bool | | `false`: no Kafka screen for this repository. |
| `repos.<repo>.vars`, `connection` | | | Merged over the profile's, key by key: changing `tls.ca` keeps the profile's `sasl`. |
| `repos.<repo>.sources` | list | | Read after the profile's sources. |
| `repos.<repo>.topics` | | | Added to the profile's topics. |

A topic is a name, or an object `{name, vars}` whose `vars` apply to that topic only, for example another SASL account. Topics can be given a direction or not: a script that does not know whether a service consumes or produces a topic can rely on `discover` alone.

The values that are known when the folder is loaded (`security`, `mechanism`, placeholders, reference syntax) are checked then. The others (files, keys, certificates) are checked when the Kafka screen opens, and a problem is shown on the topic it concerns.

**PKCS12 files**: Java truststores (`keytool`) and keystores are read. A certificate-only PKCS12 file made by `openssl` without Java's trust attribute is not: convert it once with `openssl pkcs12 -in truststore.p12 -nokeys -out ca.pem` and point `tls.ca` to the PEM file.

### Limits: `huginn.yaml` `kafka:`

Optional, the same for every profile.

| Key | Default | Meaning |
|---|---|---|
| `kafka.tail_records` | `100` | Records per partition loaded by the tail (key `0`). 1 to 10 000. |
| `kafka.max_records` | `20000` | Records kept per Kafka screen; the oldest are dropped first. |
| `kafka.max_buffer_bytes` | `64MiB` | Bytes of keys, values and headers kept per Kafka screen; the oldest records are dropped first. |
| `kafka.max_value_bytes` | `256KiB` | A larger key or value is kept truncated, with its real size shown. At most `max_buffer_bytes`. |
| `kafka.fetch_max_bytes` | `1MiB` | Bytes per fetch response. |
| `kafka.partition_fetch_max_bytes` | `256KiB` | Bytes per partition per fetch response. |
| `kafka.connect_timeout` | `10s` | Time to reach a broker and authenticate. |
| `kafka.request_timeout` | `30s` | Time allowed for one request. |
| `kafka.client_id` | `huginn` | Client id, so the brokers' operators recognise Huginn. |
| `kafka.isolation` | `read_uncommitted` | `read_uncommitted` shows every record; `read_committed` hides aborted transactions. Key `i` switches it. |

Sizes are a number of bytes or a number followed by `KiB`, `MiB` or `GiB`. Durations use Go's notation: `500ms`, `10s`, `1m`.

**Shared quotas**: when the credentials are the application's own, the brokers may count Huginn's reads against the same quota as the application's pods. The limits above keep reads small; Huginn reads only when asked (the tail, a window, or `f` to follow).

## 11. Errors

Every problem of the folder is listed at once, sorted by file and position. The location is `file:line:column` of the key concerned, or just the file when the problem is the file itself:

```
huginn: the config folder ~/work/acme-huginn has 5 errors (see docs/CONFIG.md):
  enviroments.yaml           unexpected file (did you mean environments.yaml?)
  environments.yaml          missing file (required)
  layouts/spring.yaml:9:31   stream.columns[1].show: unknown filter "rigth" (filters: left:N, right:N, …)
  services.yaml:9:1          unknown key "resolver" (did you mean "resolve"?)
  ui.yaml:4:3                keymap: unknown action "folow" (actions: see docs/CONFIG.md, ui.yaml)
```

| Message | Fix |
|---|---|
| `missing file (required)` | Create the file (see [the folder](#2-the-folder)). |
| `unexpected file` / `unexpected entry` | Rename it (a suggestion is given) or remove it. Only `.yaml` files go in `formats/` and `layouts/`. |
| `version: must be 1` | Start the file with `version: 1`. |
| `unknown key "x" (did you mean "y"?)` | Fix the spelling, or remove a key that does not exist. |
| `cannot unmarshal …` | Wrong type: a list where a string is expected, text where a number is expected. |
| `missing required key "x"` | Add the key (the tables above mark required keys). |
| `"x" is not one of: …` | Use one of the listed values. |
| `default_env: "x" is not in environments.yaml` | Use an environment defined in `environments.yaml`. |
| `set namespaces or namespace_from` | Give exactly one of the two. |
| `the labels rule needs label_keys` (and similar) | A rule listed in `resolve` needs its settings. |
| `pattern: invalid regular expression` | Fix the regex. Quote it with single quotes in YAML. |
| `missing group (?P<message>…)` | A regex format needs a `message` group. |
| `layout: "x" is not a file of layouts/` | Create `layouts/x.yaml` or fix the name. |
| `unknown field` / `unknown filter` in a template | See [Templates](#templates). |
| `key "p" is used by the columns picker` | Pick another letter; `p`, `z`, `r` and `f` are taken. |
| `is already the name of` / `is already the key of` | Column names and keys must be unique in a line. |
| `unknown placeholder {x}` | Use `{env}`, `{repo}`, `{repo_dir}` or a variable defined in `vars`. |
| `malformed reference` | Write `${KEY}` or `${KEY:-default}`; quote the value inside `[ ]` or `{ }`. |
| `{repo_dir} needs repos_root` | Set `repos_root` in `huginn.yaml`, or a `path` for the repository. |
| `sasl_ssl needs sasl.username` (and similar) | A SASL protocol needs the mechanism, user name and password. |

## 12. Your folder in 15 minutes

To have a coding agent (such as GitHub Copilot) draft the folder from your repositories, use the prompt in [`docs/prompts/copilot-config-folder.md`](prompts/copilot-config-folder.md), then check its result with the steps below.

1. **Copy the closest example**: `cp -r examples/config ~/work/acme-huginn`, or `config-node`, or `config-nginx`.
2. **Environments**: in `environments.yaml`, put your kube contexts (`kubectl config get-contexts`) and namespaces, then set `default_env` in `huginn.yaml`.
3. **Repositories**: look at a workload's labels (`kubectl get deploy <name> --show-labels`). If one of them names the repository, put its key in `label_keys` of `services.yaml`. Otherwise use an `explicit` list.
4. **Sidecars**: list your injected containers in `containers.yaml` (`kubectl get pod <pod> -o jsonpath='{.spec.containers[*].name}'`).
5. **Format**: copy one real line (`kubectl logs <pod> -c <container> --tail 1`).
   - If it is JSON, map each standard field to the key that holds it in `formats/<name>.yaml`. Put the metadata you never want on screen in `hidden`.
   - If it is text, write a `regex` format and test the pattern on your line.
6. **Layout**: start from `layouts/spring.yaml` and adapt the columns, keeping only what helps you read. Name the layout in the format's `layout` key.
7. **Check**: run `huginn --config ~/work/acme-huginn`. Fix what it reports; every problem is listed at once, with its line. Then open a repository and compare a few lines with `kubectl logs`.
8. **Share**: commit the folder to a git repository for your team. Personal choices stay in `ui.yaml`, so each person can keep their own.
