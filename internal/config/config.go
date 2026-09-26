// Package config loads, defaults and validates Huginn's YAML configuration.
//
// Configuration is plain data: adapters receive only their own section from
// the composition root, and the core never reads it. Everything specific to
// a company (contexts, namespaces, JSON field names, manifest layout, label
// keys, secret locations) is expressed here rather than in code.
package config

import "go.yaml.in/yaml/v3"

// Config is the root of config.yaml.
type Config struct {
	DefaultEnv   string                 `yaml:"default_env" doc:"Environment used when none is given on the command line."`
	ReposRoot    string                 `yaml:"repos_root" doc:"Directory containing the repositories to scan for manifests (optional)."`
	Cluster      Cluster                `yaml:"cluster" doc:"Which cluster client adapter to use."`
	Environments map[string]Environment `yaml:"environments" doc:"Environments by name. Several may share one kube context."`
	Repos        []RepoMapping          `yaml:"repos" doc:"Explicit repository to workload mapping; checked first by the resolver chain."`
	Resolver     Resolver               `yaml:"resolver" doc:"How workloads are mapped to repositories."`
	Manifests    Manifests              `yaml:"manifests" doc:"How repository manifests are scanned."`
	Logs         Logs                   `yaml:"logs" doc:"Log loading, buffering and display."`
	LogFormats   map[string]LogFormat   `yaml:"log_formats" doc:"Log format profiles by name; logs.format selects one."`
	Containers   Containers             `yaml:"containers" doc:"Which containers are considered application containers."`
	Secrets      Secrets                `yaml:"secrets" doc:"Secret provider used for values referenced as sops:<file>#<key>."`
	Proxy        Proxy                  `yaml:"proxy" doc:"Corporate proxy and TLS settings."`
	UI           UI                     `yaml:"ui" doc:"Theme and key bindings."`
	Demo         Demo                   `yaml:"demo" doc:"Synthetic cluster used by --demo."`
}

// Cluster selects the cluster adapter.
type Cluster struct {
	Client string `yaml:"client" doc:"Cluster client adapter: kubernetes or demo." enum:"kubernetes,demo"`
}

// Environment describes where one environment lives.
type Environment struct {
	Context       string   `yaml:"context" doc:"kubeconfig context name (empty: current context)."`
	Project       string   `yaml:"project" doc:"GCP project, used for Cloud Logging links."`
	Location      string   `yaml:"location" doc:"GKE location (region or zone), used for fix-it commands."`
	ClusterName   string   `yaml:"cluster" doc:"GKE cluster name, used for fix-it commands."`
	Namespaces    []string `yaml:"namespaces" doc:"Namespaces holding the environment's workloads."`
	NamespaceFrom string   `yaml:"namespace_from" doc:"Read the namespace from a secret instead, e.g. sops:overlays/rec/config.env#NAMESPACE."`
	Production    bool     `yaml:"production" doc:"Show the production banner and warnings."`
}

// RepoMapping maps a repository to its workloads explicitly.
type RepoMapping struct {
	Name      string        `yaml:"name" doc:"Repository name as shown on the services screen."`
	Workloads []WorkloadRef `yaml:"workloads" doc:"Workloads owned by the repository."`
}

// WorkloadRef identifies one workload.
type WorkloadRef struct {
	Env       string `yaml:"env" doc:"Environment name."`
	Namespace string `yaml:"namespace" doc:"Namespace (default: the environment's first namespace)."`
	Kind      string `yaml:"kind" doc:"Workload kind (default Deployment)." enum:"Deployment,StatefulSet,DaemonSet,CronJob"`
	Name      string `yaml:"name" doc:"Workload name."`
}

// Resolver configures the repo resolver chain.
type Resolver struct {
	Order     []string `yaml:"order" doc:"Resolver chain, first match wins." enum:"config,labels,manifests"`
	LabelKeys []string `yaml:"label_keys" doc:"Workload labels or annotations whose value is the repository name."`
}

// Manifests configures manifest scanning.
type Manifests struct {
	Scanner     string   `yaml:"scanner" doc:"Manifest scanner adapter." enum:"kustomize,none"`
	OverlayGlob string   `yaml:"overlay_glob" doc:"Glob, relative to a repository, of an environment's overlay; {env} is replaced."`
	Files       []string `yaml:"files" doc:"Entry files read in an overlay."`
}

// Logs configures log loading and display.
type Logs struct {
	DefaultWindow string `yaml:"default_window" doc:"Initial time window: 15m, 1h, 2d, tail…"`
	TailLines     int    `yaml:"tail_lines" doc:"Lines loaded by the tail window."`
	BufferLines   int    `yaml:"buffer_lines" doc:"Maximum lines kept in memory per view; older lines are dropped."`
	Format        string `yaml:"format" doc:"Name of the log format profile used to decode lines."`
	Renderer      string `yaml:"renderer" doc:"Line layout." enum:"spring-compact,spring-full"`
	Redact        *bool  `yaml:"redact" doc:"Mask tokens, passwords, emails and card numbers in display and exports (default true)."`
}

// LogFormat is a log format profile: how to read one kind of structured log
// line. Each field lists candidate JSON paths; the first present wins.
type LogFormat struct {
	Decoder      string            `yaml:"decoder" doc:"Decoder adapter." enum:"json-fields,plain"`
	Fields       FieldMap          `yaml:"fields" doc:"Canonical field to JSON paths."`
	LevelAliases map[string]string `yaml:"level_aliases" doc:"Extra level spellings, e.g. {\"30\": info}."`
	Hidden       []string          `yaml:"hidden" doc:"JSON paths (glob) hidden from the stream and shown only in zoom metadata."`
}

// FieldMap maps canonical fields to candidate JSON paths.
type FieldMap struct {
	Timestamp Paths `yaml:"timestamp" doc:"Entry time."`
	Level     Paths `yaml:"level" doc:"Severity."`
	Logger    Paths `yaml:"logger" doc:"Logger name."`
	Thread    Paths `yaml:"thread" doc:"Thread name."`
	Message   Paths `yaml:"message" doc:"Message text."`
	Stack     Paths `yaml:"stack" doc:"Stack trace."`
	TraceID   Paths `yaml:"trace_id" doc:"Correlation / trace identifier."`
	App       Paths `yaml:"app" doc:"Application name."`
	PID       Paths `yaml:"pid" doc:"Process id."`
}

// Containers selects application containers.
type Containers struct {
	Denylist    []string `yaml:"denylist" doc:"Container names or image substrings to hide, added to the built-in list."`
	Allowlist   []string `yaml:"allowlist" doc:"Container names always shown, even if denylisted."`
	IncludeInit bool     `yaml:"include_init" doc:"Show init containers."`
}

// Secrets configures the secret provider.
type Secrets struct {
	Provider string `yaml:"provider" doc:"Secret provider adapter." enum:"sops,none"`
	Sops     Sops   `yaml:"sops" doc:"sops settings."`
}

// Sops configures the sops CLI.
type Sops struct {
	Binary string `yaml:"binary" doc:"sops executable name or path."`
}

// Proxy configures outbound connectivity.
type Proxy struct {
	CABundle string `yaml:"ca_bundle" doc:"Extra PEM CA bundle for proxies that intercept TLS (SSL_CERT_FILE also works)."`
}

// UI configures the terminal UI.
type UI struct {
	Theme           string              `yaml:"theme" doc:"Color theme." enum:"light,accessible,classic,none"`
	PaintBackground bool                `yaml:"paint_background" doc:"Paint the theme background instead of using the terminal's."`
	Keymap          map[string][]string `yaml:"keymap" doc:"Action name to keys, overriding defaults, e.g. {follow: [f, ctrl+l]}."`
}

// Demo configures the synthetic cluster.
type Demo struct {
	Seed int64   `yaml:"seed" doc:"Random seed; the same seed gives the same cluster and logs."`
	Rate float64 `yaml:"rate" doc:"Average live log lines per second per pod."`
}

// RedactEnabled reports whether redaction is on (the default).
func (l Logs) RedactEnabled() bool { return l.Redact == nil || *l.Redact }

// Paths is a list of candidate JSON paths. In YAML it may be written as a
// single string or a list.
type Paths []string

// UnmarshalYAML accepts a scalar or a sequence.
func (p *Paths) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*p = Paths{n.Value}
		return nil
	}
	var xs []string
	if err := n.Decode(&xs); err != nil {
		return err
	}
	*p = xs
	return nil
}
