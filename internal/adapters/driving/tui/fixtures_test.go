package tui

import (
	"context"
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
