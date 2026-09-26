package tui

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

var t0 = time.Date(2026, 9, 26, 19, 14, 2, 0, time.UTC)

// fakeCatalog records Watch calls; tests feed snapshots directly.
type fakeCatalog struct {
	mu    sync.Mutex
	calls []domain.Env
	ctxs  []context.Context
}

func (f *fakeCatalog) Watch(ctx context.Context, env domain.Env) (<-chan ports.CatalogSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, env)
	f.ctxs = append(f.ctxs, ctx)
	return make(chan ports.CatalogSnapshot), nil
}

type row struct {
	repo           string
	workloads      int
	ready, desired int
	status         domain.ServiceStatus
	restarts       int
	lastRestart    time.Duration
	version        string
	age            time.Duration
}

// mockupRows are the services of mockup board 1.
var mockupRows = []row{
	{"catalog-indexer", 1, 0, 2, domain.StatusCrashLoopBackOff, 23, 4 * time.Minute, "v1.3.7", 21 * time.Minute},
	{"order-orchestrator", 2, 2, 3, domain.StatusOOMKilled, 12, 38 * time.Minute, "v2.7.4", time.Hour},
	{"document-renderer", 1, 0, 1, domain.StatusImagePullBackOff, 0, 0, "v1.11.0", 14 * time.Minute},
	{"billing-gateway", 2, 1, 3, domain.StatusDegraded, 7, 9 * time.Minute, "v3.9.2", 47 * time.Minute},
	{"email-dispatcher", 1, 1, 2, domain.StatusPending, 0, 0, "v1.6.9", 8 * time.Minute},
	{"notification-worker", 1, 2, 3, domain.StatusProgressing, 1, 3 * time.Hour, "v1.7.9→v1.8.0", 3 * time.Hour},
	{"ledger-writer", 1, 2, 2, domain.StatusProgressing, 3, 26 * time.Minute, "a41c9e2", 26 * time.Minute},
	{"payment-service", 2, 5, 5, domain.StatusHealthy, 1, 2 * time.Hour, "v2.14.3", 12 * 24 * time.Hour},
	{"fraud-detector", 1, 3, 3, domain.StatusHealthy, 2, 96 * time.Hour, "v0.19.6", 5 * 24 * time.Hour},
	{"search-api", 2, 3, 3, domain.StatusHealthy, 1, 120 * time.Hour, "v3.1.2", 6 * 24 * time.Hour},
	{"user-api", 3, 6, 6, domain.StatusHealthy, 0, 0, "v4.2.1", 8 * 24 * time.Hour},
	{"pricing-engine", 2, 4, 4, domain.StatusHealthy, 0, 0, "v2.12.0", 9 * 24 * time.Hour},
	{"audit-stream", 2, 4, 4, domain.StatusHealthy, 0, 0, "v2.4.8", 18 * 24 * time.Hour},
	{"identity-provider", 2, 4, 4, domain.StatusHealthy, 0, 0, "v5.0.1", 31 * 24 * time.Hour},
}

func mockupSnapshot(env string) ports.CatalogSnapshot {
	s := ports.CatalogSnapshot{Env: domain.Env(env), UpdatedAt: t0, Synced: true}
	for _, r := range mockupRows {
		sum := domain.ServiceSummary{
			Repo: r.repo, Workloads: r.workloads, ReadyPods: r.ready, DesiredPods: r.desired, Status: r.status,
			Restarts: r.restarts, Version: r.version, Created: t0.Add(-r.age),
		}
		if r.lastRestart > 0 {
			sum.LastRestart = t0.Add(-r.lastRestart)
		}
		if r.repo == "payment-service" {
			sum.Pods = paymentPods()
		}
		s.Services = append(s.Services, sum)
	}
	s.Services = append(s.Services, domain.ServiceSummary{Repo: "legacy-cron", Workloads: 1, ReadyPods: 1, DesiredPods: 1, Status: domain.StatusHealthy, Version: "1.0", Created: t0.Add(-400 * time.Hour), Unassigned: true})
	return s
}

func paymentPods() []domain.Pod {
	mk := func(name string, restarts int, age time.Duration) domain.Pod {
		return domain.Pod{
			Name: name, OwnerName: "payment-service", Phase: domain.PodRunning, Node: "gke-main-pool-2-9c1f", Created: t0.Add(-age),
			Containers: []domain.Container{
				{Name: "istio-init", Init: true, State: domain.ContainerTerminated, Reason: "Completed"},
				{Name: "payment-service", Image: "eu.gcr.io/acme/payment-service:v2.14.3", State: domain.ContainerRunning, Ready: true, Restarts: restarts},
				{Name: "istio-proxy", State: domain.ContainerRunning, Ready: true},
				{Name: "vault-agent", State: domain.ContainerRunning, Ready: true},
			},
		}
	}
	return []domain.Pod{
		mk("payment-service-7b9c8d4f6c-m8q7v", 1, 12*24*time.Hour),
		mk("payment-service-7b9c8d4f6c-x4k2p", 0, 12*24*time.Hour),
	}
}

// fakeSessions records opened log queries; tests feed batches directly.
type fakeSessions struct {
	mu      sync.Mutex
	queries []ports.LogQuery
	ctxs    []context.Context
}

func (f *fakeSessions) Open(ctx context.Context, q ports.LogQuery) (<-chan ports.LogBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.ctxs = append(f.ctxs, ctx)
	return make(chan ports.LogBatch), nil
}

// compactRenderer mimics the Spring compact layout without importing the
// adapter: time, level, logger, message.
type compactRenderer struct{}

func (compactRenderer) Render(e domain.LogEntry, o ports.RenderOptions) []ports.Segment {
	var out []ports.Segment
	if o.Timestamps != ports.TimestampNone {
		out = append(out, ports.Segment{Text: e.Time.UTC().Format("15:04:05.000") + " ", Role: ports.RoleTimestamp})
	}
	if !e.Structured {
		return append(out, ports.Segment{Text: e.Message, Role: ports.RoleMessage})
	}
	return append(out,
		ports.Segment{Text: fmt.Sprintf("%5s ", e.Level), Role: ports.RoleLevel},
		ports.Segment{Text: "[" + e.Thread + "] ", Role: ports.RoleThread},
		ports.Segment{Text: e.Logger, Role: ports.RoleLogger},
		ports.Segment{Text: " : ", Role: ports.RoleDim},
		ports.Segment{Text: e.Message, Role: ports.RoleMessage},
	)
}

const (
	podA = "payment-service-7b9c8d4f6c-m8q7v"
	podB = "payment-service-7b9c8d4f6c-x4k2p"
	podC = "payment-service-5d6f4c9b8d-p3w1n"
)

func logEntry(sec int, pod string, lvl domain.Level, logger, msg string) domain.LogEntry {
	return domain.LogEntry{
		Time: t0.Add(time.Duration(sec) * time.Second), Pod: pod, Container: "payment-service", Level: lvl, Structured: true,
		Thread: "exec-1", Logger: logger, Message: msg, Raw: `{"message":"` + msg + `"}`,
	}
}

// paymentBatch is a history batch resembling mockup board 3.
func paymentBatch() ports.LogBatch {
	errEntry := logEntry(-50, podA, domain.LevelError, "i.g.p.PaymentService", "Payment authorization failed orderId=ord_8f91a2 traceId=7fd28c90")
	errEntry.Stack = "io.gimle.payment.PaymentGatewayException: upstream request timed out\n\tat io.gimle.payment.gateway.GatewayClient.charge(GatewayClient.java:184)\n\tat org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:885)\nCaused by: java.net.SocketTimeoutException: Read timed out"
	errEntry.TraceID = "7fd28c90"
	errEntry.Fields = map[string]string{"extra.orderId": "ord_8f91a2", "spanId": "91ac07"}
	errEntry.Hidden = map[string]string{"kubernetes.namespace_name": "app-rec", "kubernetes.pod_name": podA}
	entries := []domain.LogEntry{
		logEntry(-60, podB, domain.LevelInfo, "i.g.p.PaymentController", "request completed POST /v1/payments status=201 duration=96ms"),
		logEntry(-58, podA, domain.LevelDebug, "c.z.hikari.pool.HikariPool", "HikariPool-1 - Pool stats (total=40, active=12, idle=28, waiting=0)"),
		logEntry(-55, podB, domain.LevelWarn, "i.g.p.gateway.GatewayClient", "gateway latency above threshold provider=adyen p95=842ms"),
		errEntry,
		logEntry(-45, podB, domain.LevelInfo, "i.g.p.PaymentController", "request completed GET /v1/payments/pay_802ae status=200 duration=14ms"),
		{Time: t0.Add(-44 * time.Second), Pod: podC, Container: "payment-service", Message: " :: Spring Boot ::                (v3.4.1)", Raw: " :: Spring Boot ::                (v3.4.1)"},
		logEntry(-40, podC, domain.LevelInfo, "i.g.p.PaymentApplication", "Started PaymentApplication in 7.412 seconds"),
		logEntry(-30, podA, domain.LevelError, "i.g.p.card.CardController", "Card declined by issuer orderId=ord_72bf10 retryable=false"),
		logEntry(-20, podB, domain.LevelInfo, "i.g.p.HealthReporter", "health probe succeeded components=db,redis,gateway"),
	}
	pods := paymentPods()
	newPod := pods[0]
	newPod.Name = podC
	newPod.Containers = slices.Clone(newPod.Containers)
	newPod.Containers[1].Image, newPod.Containers[1].Restarts = "eu.gcr.io/acme/payment-service:v2.14.4", 0
	return ports.LogBatch{
		Entries: entries, HistoryDone: true,
		Pods: []ports.PodState{{Pod: pods[0], Containers: []string{"payment-service"}}, {Pod: pods[1], Containers: []string{"payment-service"}}, {Pod: newPod, Containers: []string{"payment-service"}, New: true}},
	}
}
