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
3. Make sure your kubeconfig has a context for every environment, production
   included (`gcloud container clusters get-credentials …`), and that you are
   logged in (`gcloud auth login`). The agent checks its work against the live
   clusters with read-only `kubectl` commands.
4. Copy everything under the line below into Copilot Chat in **agent** mode.
   You can also save it as `.github/prompts/huginn-config.prompt.md` in your
   workspace and run it with `/huginn-config`. Replace `<OUTPUT_DIR>` first,
   for example `~/work/huginn-config`.
5. Review the generated `README.md`, especially its TODO list and cluster
   check table, then run `huginn --config <OUTPUT_DIR> <env>`.

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
- **My applications:** every other repository in the workspace. They say what
  is *meant* to run.
- **The live clusters:** every environment, production included, through
  read-only `kubectl` (see the rules below). They say what *actually* runs and
  what the containers *actually* print. When a repository and a cluster
  disagree, the cluster wins; note the difference in the README.

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
7. **`kubectl` is allowed on every environment, production included, with
   read-only commands only:** `get`, `describe`, `logs`, `top`, `api-resources`,
   `auth can-i`, `config get-contexts`, `config current-context`,
   `config view --minify`.
   - Never run a command that changes anything: `apply`, `create`, `edit`,
     `patch`, `delete`, `scale`, `rollout restart|undo`, `label`, `annotate`,
     `set`, `cordon`, `drain`, `taint`.
   - Never open a session into a pod: `exec`, `attach`, `cp`, `port-forward`,
     `debug`, `proxy`.
   - Never read Secrets (`get secret`, `describe secret`, `-o yaml` of a
     Secret).
   - Never run `kubectl config use-context` or `set-context`: they change my
     current context. Pass `--context <ctx>` and `-n <ns>` on every command.
   - Keep log reads small: `--tail 50` at most per container.
8. **Real log lines stay out of the files unredacted.** You may read as many
   as you need to get the mapping right. When you put an example line in a
   comment, replace personal data, ids, emails, IPs, tokens and business
   values with placeholders.

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
  kube context is then `gke_<project>_<location>_<cluster>`. Confirm it exists
  with `kubectl config get-contexts -o name`.
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
- Add `mute.loggers` only for loggers the user names as noise, or that the
  code plainly logs on a timer (connection pool statistics, periodic
  resource snapshots); list each with the file that shows it in the README.
  Never mute a logger of the application's own business code, and never
  guess a name: a pattern must match the logger as the lines write it.
- **Order:** formats are tried in file-name order and the first whose `match`
  accepts the repository and container wins. Name the files `10-<name>.yaml`,
  `20-<name>.yaml`, … and end with one catch-all format without `match`, named
  to sort last.
- At the top of each format file, add a comment with **one example line**
  taken from `kubectl logs` and redacted (rule 8), as the examples do.

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
3. **Cluster check (required, on every environment, production included).**
   Use only the commands allowed by rule 7, with `--context` and `-n` on each.
   Fix the folder after each step.
   1. **Access:** every `context` exists (`kubectl config get-contexts -o name`).
      For every namespace, `kubectl auth can-i list pods` and
      `kubectl auth can-i get pods --subresource=log` answer `yes`. A `no` is a
      TODO for me, not something to work around.
   2. **Workloads → repositories:**
      `kubectl get deploy,sts,ds,cronjob --show-labels` (and
      `-o jsonpath='{.items[*].metadata.annotations}'` when `label_keys` may
      use annotations). Every workload must resolve to the intended repository
      through `services.yaml`'s rules. List the ones that don't and fix them
      with `explicit` entries or another label key.
   3. **Standalone pods:** `kubectl get pods --show-labels` and each pod's
      `metadata.ownerReferences`. Find the bare pods, hand-made Jobs and
      controllers other than the four workload kinds, and decide whether
      `standalone_pods` should stay on.
   4. **Containers:** for one running pod of each workload,
      `kubectl get pod <pod> -o jsonpath='{.spec.initContainers[*].name} | {.spec.containers[*].name} | {.spec.containers[*].image}'`.
      Every non-application container, injected ones included, is matched by
      `hide`. No application container is matched by `hide`, unless it is in
      `always_show`.
   5. **Formats:** for each application container and each non-hidden
      sidecar, `kubectl logs <pod> -c <container> --tail 50`. Also check a
      crashing pod with `--previous` if there is one: startup and crash output
      often differ. Check that:
      - the first matching format file is the intended one;
      - every mapped key is present in the lines;
      - `time` is RFC 3339 or epoch seconds/milliseconds (otherwise map
        another key or leave it out);
      - every level value is a known spelling or is listed in `levels`;
      - multi-line output (stack traces, banners) is understood.
   6. Environments must not differ silently. If production logs differ from
      staging (another profile, another encoder), write a format with a
      `match` for it, or explain in the README why one format covers both.

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
  - **Cluster check:** a table per environment: context and access result,
    workloads found / resolved to a repository / unresolved, sidecars seen,
    and for each format the containers and number of lines it was checked
    against.
  - **TODO (verify):** what the cluster check could not settle (a namespace
    I cannot read, a workload with no running pod…), with the exact command
    to confirm it.
  - **Run:** `huginn --config <OUTPUT_DIR> <default_env>`.

Finally, give me a short summary: the environments and repositories covered,
the formats and layouts created, the cluster check result per environment,
the number of TODOs, and the result of the validation run.
