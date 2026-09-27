# Prompt: generate a Huginn config folder from your repositories

A prompt for GitHub Copilot (agent mode) or any coding agent. It scans your
application repositories and writes a [config folder](../CONFIG.md) for
Huginn.

**How to use it**

1. Open a workspace that contains your application repositories and a copy of
   this Huginn repository. The agent needs `docs/CONFIG.md`, `docs/schema/` and
   `examples/`. In VS Code, a multi-root workspace works.
2. Build Huginn once (`make build`) so the agent can validate the folder with
   `bin/huginn`.
3. Copy everything under the line below into Copilot Chat in **agent** mode.
   You can also save it as `.github/prompts/huginn-config.prompt.md` in your
   workspace and run it with `/huginn-config`. Replace `<OUTPUT_DIR>` first,
   for example `~/work/huginn-config`.
4. Review the generated `README.md`, especially its TODO list, then run
   `huginn --config <OUTPUT_DIR>` against a **non-production** environment
   first.

---

You are generating a **config folder for Huginn**, a read-only Kubernetes (GKE)
pod-log viewer. Huginn has no knowledge of my applications. Everything it knows
about them comes from this folder. Write it to `<OUTPUT_DIR>`.

## Sources of truth

- **Format reference:** `docs/CONFIG.md` in the Huginn repository is the only
  authority on keys, types, defaults and rules. Read all of it before writing
  anything. Use only the keys it documents; decoding is strict and unknown keys
  are errors.
- **Schemas:** `docs/schema/*.schema.json`.
- **Worked examples:** `examples/config/` (Spring Boot JSON), `examples/config-node/`
  (pino), `examples/config-nginx/` (regex plus a second JSON format). Start from
  the closest one.
- **My applications:** every other repository in the workspace.

## Hard rules

1. **Read-only on my repositories.** Do not edit, commit or create files
   anywhere except `<OUTPUT_DIR>`.
2. **No secrets.** Never decrypt sops files. Never copy values out of `.env`,
   Secret manifests, Vault paths or CI variables. Never write tokens, passwords,
   keys or connection strings into the folder. Referencing a sops file with
   `namespace_from: sops:<file>#<key>` is allowed, because Huginn decrypts it at
   run time with my own keys.
3. **Do not invent.** Every value must come from something you read. When you
   cannot find a value, write your best guess, mark it with a
   `# TODO(verify): <why>` comment on that line, and list it in the README (see
   Deliverables).
4. **Do not use the `manifests` rule** in `services.yaml`. It is validated but
   not implemented yet. Use `labels` and/or `explicit` only.
5. **Folder layout:** only the file names allowed by `docs/CONFIG.md` §2. Every
   YAML file starts with the `# yaml-language-server: $schema=…` line (an
   absolute path or URL to the Huginn `docs/schema/` file) followed by
   `version: 1`. `README.md` is the only extra file allowed; Huginn ignores
   Markdown.
6. **Huginn reads raw container output** (stdout/stderr through the Kubernetes
   API), not what a log shipper stores. Fields added downstream by Fluent Bit,
   Datadog, Cloud Logging and similar are not in the lines Huginn sees. Map the
   fields the **application itself** writes.

## Discovery: what to look for, file by file

Make one pass over all repositories and take notes, then write the files.

### `environments.yaml` and `huginn.yaml`

- **Environments:** Kustomize overlays (`**/overlays/<env>/`), Helm values
  files (`values-<env>.yaml`), deployment pipelines (`.github/workflows`,
  `.gitlab-ci.yml`, `cloudbuild.yaml`, Jenkinsfiles, Argo CD / Flux
  `Application` manifests). Keep the environment names the team already uses.
  Names are lower-case letters, digits and `-`, at most 32 characters. List them
  from least to most critical.
- **Contexts:** look for `gcloud container clusters get-credentials <cluster>
  --region|--zone <location> --project <project>` in CI or scripts. The GKE
  kube context is then `gke_<project>_<location>_<cluster>`. If you cannot find
  it, write a TODO so I can fill it in from `kubectl config get-contexts`.
- **Namespaces:** `namespace:` in `kustomization.yaml`, `metadata.namespace`,
  Helm `--namespace` / `-n` flags, Argo CD `destination.namespace`. When the
  namespace comes from a sops-encrypted dotenv file, use
  `namespace_from: sops:<path>#<KEY>`. The path is relative to the config
  folder or starts with `~/`; take the key name from the file, never its value.
- Set `production: true` on production environments.
- In `huginn.yaml`, set `default_env` to a non-production environment such as
  staging or recette. Leave the other keys at the documented defaults unless the
  repositories give a reason to change them.

### `services.yaml` (workload → repository)

- For every Deployment, StatefulSet, DaemonSet and CronJob in each repository,
  record its name and the labels **and annotations on the workload's own
  `metadata`**, not only on the pod template.
- If one label key consistently holds the repository name across repositories
  (`app.kubernetes.io/part-of`, `app.kubernetes.io/name`, `app` or a company
  key), use `resolve: [labels]` with those keys in priority order.
- If there is no consistent label, or some workloads break the pattern, use
  `resolve: [explicit, labels]` and list those workloads under `explicit`, for
  each environment they run in. The repository name is the git repository
  (folder) name.
- Keep `standalone_pods` at its default unless the repositories show many
  one-off Jobs or bare pods.

### `containers.yaml` (sidecars)

- Find every container that is not the application: extra `containers:` in
  the pod specs (for example cloud-sql-proxy, auth proxies, log forwarders),
  `initContainers`, and injected sidecars revealed by annotations and labels
  (`sidecar.istio.io/inject`, a namespace labelled `istio-injection`,
  `vault.hashicorp.com/agent-inject`, `linkerd.io/inject`, Datadog / OTel
  injection).
- Put them in `hide`. Use the container name, or the image name when the
  container name varies. Keep `examples/config/containers.yaml` entries only
  when they appear in my repositories.
- If an application container would be matched by `hide`, add it to
  `always_show`.

### `formats/` (reading lines)

For each distinct way my applications log, find the logging configuration and
work out **the exact JSON keys or text pattern written to the console**:

- **Java / Spring:** `logback-spring.xml` / `logback.xml` (look for
  `LogstashEncoder`, `LoggingEventCompositeJsonEncoder`, `<fieldNames>`
  overrides, `<customFields>`, `<includeMdcKeyName>`, the console appender that
  is active for the Kubernetes profile), `log4j2*.xml` (`JsonTemplateLayout`
  and its template), and `application*.yml` / `.properties`
  (`logging.structured.format.console`: `ecs`, `logstash` or `gelf`;
  `logging.pattern.console`). Check which Spring profile each environment
  activates.
- **Node:** pino (numeric levels, epoch-millisecond `time`), winston formats,
  bunyan.
- **Go:** zap / zerolog / slog handler options (key names, time encoding).
- **Python:** `python-json-logger`, structlog, `logging.config` dicts.
- **Other containers:** nginx, envoy, or anything with a text format get a
  `regex` format restricted with `match.containers`; test the pattern.
- **Trace ids:** MDC / context keys such as `traceId`, `trace_id`, `spanId`,
  a correlation id.

Then, for each format:

- Map every standard field (`time`, `level`, `logger`, `thread`, `message`,
  `stack`, `trace_id`, `app`, `pid`) to the key that holds it. Give a list of
  candidates when repositories differ, most common first. `message` is
  required.
- Add `levels` spellings only for values that are not already understood (see
  `docs/CONFIG.md` §8), for example numeric levels.
- Put noisy metadata in `hidden`.
- **Order:** formats are tried in file-name order and the first whose `match`
  accepts the repository and container wins. Name the files `10-<name>.yaml`,
  `20-<name>.yaml`, … and end with one catch-all format without `match`, named
  to sort last.
- At the top of each format file, add a comment with **one realistic example
  line** reconstructed from the configuration, as the examples do. Mark it
  `# reconstructed, not copied from a real log`.

### `layouts/` (drawing lines)

- One layout per logging style; formats with the same shape share a layout.
- Base the stream columns on the team's console pattern
  (`logging.pattern.console` or the equivalent) so lines look familiar. Keep
  only what helps reading: time, level, a shortened logger, maybe the thread.
- Follow `docs/CONFIG.md` §9: unique column `name`s and `key`s in a line; keys
  `p`, `z`, `r` and `f` are reserved; `role` from the allowed list; filters only
  from the documented list.
- Set `stack.framework_prefixes` to the frameworks and libraries actually in
  the dependencies (pom.xml, build.gradle, package.json, go.mod), so my own
  code stands out in stack traces.

### `ui.yaml`

Optional. Personal preferences; skip it unless I asked for something.

## Validation loop (required)

1. From a terminal, run:
   ```sh
   <path-to-huginn>/bin/huginn --config <OUTPUT_DIR> < /dev/null
   ```
   - If it prints a list of `file:line:column` errors, fix **every** one and run
     it again.
   - When the folder is valid, the only output is
     `huginn: needs an interactive terminal …`. The exit code is 2 in both
     cases, so check the message, not the code.
2. Check that each format's `pattern` (for regex formats) matches its example
   line, and that each JSON example line contains the mapped keys.
3. **Optional, only if I confirm it and only on a non-production environment:**
   compare with the cluster using read-only commands:
   `kubectl get deploy,sts,ds,cronjob -n <ns> --show-labels`,
   `kubectl get pod <pod> -n <ns> -o jsonpath='{.spec.containers[*].name}'`,
   `kubectl logs <pod> -n <ns> -c <container> --tail 3`. Do not run any other
   kubectl verb. Do not paste real log lines into the folder; they may contain
   personal data.

## Deliverables

In `<OUTPUT_DIR>`:

- The config folder: `huginn.yaml`, `environments.yaml`, `services.yaml`,
  `containers.yaml` (when sidecars exist), `formats/*.yaml`, `layouts/*.yaml`,
  each commented with where its values came from (`# from payments-api/k8s/overlays/rec/kustomization.yaml`).
- `README.md` with:
  - **Sources:** a table of repositories scanned → logging style, format and
    layout used, workloads found.
  - **Decisions:** why `labels` or `explicit`, why each sidecar is hidden, how
    formats are ordered.
  - **TODO (verify):** every guessed value, with the exact command to confirm
    it (`kubectl config get-contexts`, `kubectl logs … --tail 1`, …).
  - **Run:** `huginn --config <OUTPUT_DIR> <default_env>`.

Finally, give me a short summary: the environments and repositories covered,
the formats and layouts created, the number of TODOs, and the result of the
validation run.
