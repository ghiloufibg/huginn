package config

// Built-in defaults. Only zero values are filled, so anything set in the
// file wins. Lists and maps set in the file replace the default entirely.

// DefaultContainerDenylist is the built-in list of auxiliary containers
// hidden from the logs screen; containers.denylist extends it.
var DefaultContainerDenylist = []string{
	"istio-proxy", "istio-init", "istio-validation", "linkerd-proxy", "linkerd-init",
	"vault-agent", "vault-agent-init", "cloud-sql-proxy", "cloudsql-proxy", "fluent-bit",
	"fluentd", "filebeat", "otel-collector", "opentelemetry-collector", "datadog-agent",
	"config-reloader", "envoy", "oauth2-proxy",
}

// DefaultLogFormatName is the profile used when logs.format is empty.
const DefaultLogFormatName = "logstash"

// Default returns a configuration with every default applied.
func Default() *Config {
	c := &Config{}
	applyDefaults(c)
	return c
}

func logstashFormat() LogFormat {
	return LogFormat{
		Decoder: "json-fields",
		Fields: FieldMap{
			Timestamp: Paths{"@timestamp", "timestamp", "time"},
			Level:     Paths{"level", "severity", "log.level", "levelname"},
			Logger:    Paths{"logger_name", "logger", "log.logger"},
			Thread:    Paths{"thread_name", "thread", "process.thread.name"},
			Message:   Paths{"message", "msg"},
			Stack:     Paths{"stack_trace", "error.stack_trace", "exception"},
			TraceID:   Paths{"traceId", "trace_id", "trace.id", "X-B3-TraceId", "correlationId", "requestId"},
			App:       Paths{"app", "application", "service.name", "springAppName"},
			PID:       Paths{"pid", "process.pid"},
		},
		Hidden: []string{"kubernetes.*", "k8s.*", "docker.*", "host*", "@version", "level_value", "stream", "logtag"},
	}
}

func applyDefaults(c *Config) {
	if c.Cluster.Client == "" {
		c.Cluster.Client = "kubernetes"
	}
	if c.DefaultEnv == "" {
		c.DefaultEnv = "rec"
	}
	if len(c.Environments) == 0 {
		c.Environments = map[string]Environment{
			"dev":   {Namespaces: []string{"app-dev"}},
			"rec":   {Namespaces: []string{"app-rec"}},
			"prprd": {Namespaces: []string{"app-prprd"}},
			"prd":   {Namespaces: []string{"app-prd"}, Production: true},
		}
	}
	if len(c.Resolver.Order) == 0 {
		c.Resolver.Order = []string{"config", "labels", "manifests"}
	}
	if len(c.Resolver.LabelKeys) == 0 {
		c.Resolver.LabelKeys = []string{"app.kubernetes.io/part-of", "app.kubernetes.io/name", "app"}
	}
	if c.Manifests.Scanner == "" {
		c.Manifests.Scanner = "kustomize"
	}
	if c.Manifests.OverlayGlob == "" {
		c.Manifests.OverlayGlob = "**/overlays/{env}"
	}
	if len(c.Manifests.Files) == 0 {
		c.Manifests.Files = []string{"kustomization.yaml", "kustomization.yml"}
	}
	applyLogDefaults(c)
	if c.Secrets.Provider == "" {
		c.Secrets.Provider = "sops"
	}
	if c.Secrets.Sops.Binary == "" {
		c.Secrets.Sops.Binary = "sops"
	}
	if c.UI.Theme == "" {
		c.UI.Theme = "light"
	}
	if c.UI.KeyBar == "" {
		c.UI.KeyBar = "compact"
	}
	if c.Demo.Seed == 0 {
		c.Demo.Seed = 42
	}
	if c.Demo.Rate == 0 {
		c.Demo.Rate = 1
	}
}

func applyLogDefaults(c *Config) {
	l := &c.Logs
	if l.DefaultWindow == "" {
		l.DefaultWindow = "15m"
	}
	if l.TailLines == 0 {
		l.TailLines = 500
	}
	if l.BufferLines == 0 {
		l.BufferLines = 50000
	}
	if l.Format == "" {
		l.Format = DefaultLogFormatName
	}
	if l.Renderer == "" {
		l.Renderer = "spring-compact"
	}
	if c.LogFormats == nil {
		c.LogFormats = map[string]LogFormat{}
	}
	if _, ok := c.LogFormats[DefaultLogFormatName]; !ok {
		c.LogFormats[DefaultLogFormatName] = logstashFormat()
	}
}
