package bootstrap

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
	"github.com/ghiloufibg/huginn/internal/diag"
)

var t0 = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

// demoCatalog wires the real catalog over the demo cluster with a fake
// clock: the same wiring as Build, deterministic.
func demoCatalog(t *testing.T) (<-chan ports.CatalogSnapshot, *portstest.FakeClock) {
	t.Helper()
	c := demoConfig(t)
	clock := portstest.NewFakeClock(t0)
	cluster := demo.New(demo.Options{Seed: c.Huginn.Demo.Seed, Rate: c.Huginn.Demo.Rate, Clock: clock})
	cat := newCatalog(c, scopes(c, nil), cluster, clock, containerFilter(c), diag.Discard())
	ch, err := cat.Watch(t.Context(), domain.Env("rec"))
	if err != nil {
		t.Fatal(err)
	}
	return ch, clock
}

func waitFor(t *testing.T, ch <-chan ports.CatalogSnapshot, clock *portstest.FakeClock, step time.Duration, ok func(ports.CatalogSnapshot) bool) ports.CatalogSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(step)
		select {
		case s := <-ch:
			if ok(s) {
				return s
			}
		case <-time.After(2 * time.Millisecond):
		}
	}
	t.Fatal("condition not reached")
	return ports.CatalogSnapshot{}
}

func byRepo(s ports.CatalogSnapshot) map[string]domain.ServiceSummary {
	out := map[string]domain.ServiceSummary{}
	for _, r := range s.Services {
		out[r.Repo] = r
	}
	return out
}

func TestDemoCatalogMatchesMockup(t *testing.T) {
	ch, clock := demoCatalog(t)
	s := waitFor(t, ch, clock, 100*time.Millisecond, func(s ports.CatalogSnapshot) bool { return s.Synced && len(s.Services) == 15 })
	rows := byRepo(s)
	want := map[string]domain.ServiceStatus{
		"catalog-indexer": domain.StatusCrashLoopBackOff, "order-orchestrator": domain.StatusOOMKilled,
		"document-renderer": domain.StatusImagePullBackOff, "billing-gateway": domain.StatusDegraded,
		"email-dispatcher": domain.StatusPending, "notification-worker": domain.StatusProgressing,
		"ledger-writer": domain.StatusProgressing, "payment-service": domain.StatusHealthy, "user-api": domain.StatusHealthy,
	}
	for repo, st := range want {
		if rows[repo].Status != st {
			t.Errorf("%s: %v, want %v", repo, rows[repo].Status, st)
		}
	}
	p := rows["payment-service"]
	if p.Workloads != 2 || p.Restarts != 1 || p.Version != "v2.14.3" || p.ReadyPods != 4 || p.DesiredPods != 4 {
		t.Errorf("payment-service: %+v", p)
	}
	if !rows["nightly-report"].Unassigned {
		t.Error("nightly-report should have no repository")
	}
	if rows["catalog-indexer"].Restarts != 23 {
		t.Errorf("catalog-indexer restarts %d (sidecars must not count)", rows["catalog-indexer"].Restarts)
	}
}

func TestDemoLiveRolloutShowsInCatalog(t *testing.T) {
	ch, clock := demoCatalog(t)
	waitFor(t, ch, clock, 100*time.Millisecond, func(s ports.CatalogSnapshot) bool { return s.Synced })
	rolling := waitFor(t, ch, clock, time.Second, func(s ports.CatalogSnapshot) bool {
		return byRepo(s)["payment-service"].Status == domain.StatusProgressing
	})
	if v := byRepo(rolling)["payment-service"].Version; v != "v2.14.3→v2.14.4" {
		t.Errorf("version during rollout %q", v)
	}
	waitFor(t, ch, clock, time.Second, func(s ports.CatalogSnapshot) bool {
		return byRepo(s)["payment-service"].Status == domain.StatusHealthy
	})
	if elapsed := clock.Now().Sub(t0); elapsed < 90*time.Second || elapsed > 3*time.Minute {
		t.Errorf("rollout finished after %v", elapsed)
	}
}
