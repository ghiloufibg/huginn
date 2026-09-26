package demo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// jsonLine is the logstash-logback-encoder layout with the Kubernetes
// enrichment many log pipelines add. Field order is fixed by the struct.
type jsonLine struct {
	Timestamp  string       `json:"@timestamp"`
	Version    string       `json:"@version"`
	Message    string       `json:"message"`
	Logger     string       `json:"logger_name"`
	Thread     string       `json:"thread_name"`
	Level      string       `json:"level"`
	LevelValue int          `json:"level_value"`
	StackTrace string       `json:"stack_trace,omitempty"`
	TraceID    string       `json:"traceId,omitempty"`
	SpanID     string       `json:"spanId,omitempty"`
	App        string       `json:"app"`
	PID        string       `json:"pid"`
	Kubernetes k8sMetadata  `json:"kubernetes"`
	Extra      *extraFields `json:"extra,omitempty"`
}

type k8sMetadata struct {
	Namespace     string            `json:"namespace_name"`
	Pod           string            `json:"pod_name"`
	Container     string            `json:"container_name"`
	ContainerHash string            `json:"container_image"`
	Host          string            `json:"host"`
	Labels        map[string]string `json:"labels"`
}

type extraFields struct {
	OrderID  string `json:"orderId,omitempty"`
	Customer string `json:"customer,omitempty"`
}

var levelValues = map[string]int{"DEBUG": 10000, "INFO": 20000, "WARN": 30000, "ERROR": 40000}

// entry is a generated log event before encoding.
type entry struct {
	at      time.Time
	level   string
	logger  string
	thread  string
	message string
	stack   string
	trace   string
	extra   *extraFields
	plain   bool // emit message as a raw, non-JSON line
}

// encode renders e as a JSON line for pod ctx, or as plain text.
func encode(e entry, pc podContext) string {
	if e.plain {
		return e.message
	}
	l := jsonLine{
		Timestamp: e.at.UTC().Format("2006-01-02T15:04:05.000Z07:00"), Version: "1",
		Message: e.message, Logger: e.logger, Thread: e.thread, Level: e.level,
		LevelValue: levelValues[e.level], StackTrace: e.stack, TraceID: e.trace,
		App: pc.workload, PID: "1", Extra: e.extra,
		Kubernetes: k8sMetadata{
			Namespace: pc.namespace, Pod: pc.pod, Container: pc.container,
			ContainerHash: pc.image, Host: pc.node, Labels: pc.labels,
		},
	}
	if e.trace != "" {
		l.SpanID = e.trace[:16]
	}
	b, err := json.Marshal(l)
	if err != nil {
		return e.message
	}
	return string(b)
}

// podContext is the Kubernetes metadata attached to each line.
type podContext struct {
	namespace, pod, container, image, node, workload string
	labels                                           map[string]string
}

// springBanner is printed as plain lines when a Spring Boot app starts.
var springBanner = []string{
	"",
	"  .   ____          _            __ _ _",
	" /\\\\ / ___'_ __ _ _(_)_ __  __ _ \\ \\ \\ \\",
	"( ( )\\___ | '_ | '_| | '_ \\/ _` | \\ \\ \\ \\",
	" \\\\/  ___)| |_)| | | | | || (_| |  ) ) ) )",
	"  '  |____| .__|_| |_|_| |_\\__, | / / / /",
	" =========|_|==============|___/=/_/_/_/",
	"",
	" :: Spring Boot ::                (v3.4.1)",
	"",
}

func startupEntries(r repoSpec, workload, version string, at time.Time) []entry {
	app := className(workload) + "Application"
	var out []entry
	for i, s := range springBanner {
		out = append(out, entry{at: at.Add(time.Duration(i) * time.Millisecond), message: s, plain: true})
	}
	at = at.Add(50 * time.Millisecond)
	out = append(out,
		entry{at: at, level: "INFO", logger: r.pkg + "." + app, thread: "main", message: fmt.Sprintf("Starting %s v%s using Java 21.0.5 with PID 1", app, strings.TrimPrefix(version, "v"))},
		entry{at: at.Add(40 * time.Millisecond), level: "INFO", logger: r.pkg + "." + app, thread: "main", message: `The following 1 profile is active: "kubernetes"`},
		entry{at: at.Add(2100 * time.Millisecond), level: "INFO", logger: "o.s.b.w.embedded.tomcat.TomcatWebServer", thread: "main", message: "Tomcat initialized with port 8080 (http)"},
		entry{at: at.Add(5200 * time.Millisecond), level: "INFO", logger: "com.zaxxer.hikari.HikariDataSource", thread: "main", message: "HikariPool-1 - Start completed."},
	)
	return out
}

func readyEntry(r repoSpec, workload string, at time.Time) entry {
	app := className(workload) + "Application"
	return entry{at: at, level: "INFO", logger: r.pkg + "." + app, thread: "main", message: fmt.Sprintf("Started %s in 7.412 seconds (process running for 8.03)", app)}
}

// className turns "payment-service" into "PaymentService".
func className(s string) string {
	var b strings.Builder
	for _, part := range strings.Split(s, "-") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

func noun(repo string) string {
	n, _, _ := strings.Cut(repo, "-")
	return n
}

// domainClass is the class-name stem of a workload: "payment-service" gives
// "Payment", so that generated classes read PaymentService, not
// PaymentServiceService.
func domainClass(workload string) string {
	c := strings.TrimSuffix(className(workload), "Service")
	if c == "" {
		return "App"
	}
	return c
}
