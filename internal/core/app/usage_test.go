package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

const miB = 1 << 20

// staticCatalog is a ServiceCatalog that delivers one snapshot.
type staticCatalog struct{ snap ports.CatalogSnapshot }

func (c staticCatalog) Watch(ctx context.Context, _ domain.Env) (<-chan ports.CatalogSnapshot, error) {
	ch := make(chan ports.CatalogSnapshot, 1)
	ch <- c.snap
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func usagePod(ns, name, owner string, cs ...domain.Container) domain.Pod {
	return domain.Pod{Env: "rec", Namespace: ns, Name: name, OwnerName: owner, Phase: domain.PodRunning, Containers: cs}
}

func usageCtr(name string, limMem int64) domain.Container {
	return domain.Container{Name: name, Amounts: domain.ResourceAmounts{
		Request: domain.Amount{CPUMilli: 100, MemBytes: limMem / 2},
		Limit:   domain.Amount{CPUMilli: 200, MemBytes: limMem},
	}}
}

func sampleOf(p domain.Pod, at time.Time, cpu, mem int64) domain.PodUsage {
	return domain.PodUsage{
		Namespace: p.Namespace, Name: p.Name, SampledAt: at, Window: time.Minute,
		Containers: []domain.ContainerUsage{{Name: "app", Use: domain.Amount{CPUMilli: cpu, MemBytes: mem}}},
	}
}

func nextUsage(t *testing.T, ch <-chan ports.UsageSnapshot, clock *portstest.FakeClock, ok func(ports.UsageSnapshot) bool) ports.UsageSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(500 * time.Millisecond)
		select {
		case s, open := <-ch:
			if !open {
				t.Fatal("channel closed")
			}
			if ok(s) {
				return s
			}
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Fatal("no matching snapshot")
	return ports.UsageSnapshot{}
}

func newUsage(snap ports.CatalogSnapshot, m ports.MetricsSource, clock ports.Clock, ns ...string) *Usage {
	return &Usage{
		Catalog: staticCatalog{snap}, Metrics: m, Clock: clock, Scopes: scopes(ns...),
		Filter: domain.ContainerFilter{Deny: []string{"istio-proxy"}},
	}
}

func catalogOf(pods ...domain.Pod) ports.CatalogSnapshot {
	by := map[string][]domain.Pod{}
	var order []string
	for _, p := range pods {
		if _, ok := by[p.OwnerName]; !ok {
			order = append(order, p.OwnerName)
		}
		by[p.OwnerName] = append(by[p.OwnerName], p)
	}
	snap := ports.CatalogSnapshot{Env: "rec", Synced: true}
	for _, o := range order {
		snap.Services = append(snap.Services, domain.ServiceSummary{Repo: o, Pods: by[o]})
	}
	return snap
}

func TestUsageJoinsCatalogAndMetrics(t *testing.T) {
	a1 := usagePod("app-rec", "api-1", "api", usageCtr("app", 1000*miB))
	a2 := usagePod("app-rec", "api-2", "api", usageCtr("app", 1000*miB))
	w1 := usagePod("app-rec", "web-1", "web", usageCtr("app", 500*miB))
	clock := portstest.NewFakeClock(t0)
	m := portstest.NewFakeMetrics()
	m.Set("app-rec",
		sampleOf(a1, t0, 100, 900*miB), sampleOf(a2, t0, 50, 100*miB), // a1 is hot
		sampleOf(w1, t0, 10, 50*miB),
		sampleOf(usagePod("app-rec", "job-9", "job"), t0, 5, 20*miB), // no service lists it
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := newUsage(catalogOf(a1, a2, w1), m, clock, "app-rec").Watch(ctx, ports.UsageQuery{Env: "rec", Containers: domain.ContainersAll})
	if err != nil {
		t.Fatal(err)
	}
	snap := nextUsage(t, ch, clock, func(s ports.UsageSnapshot) bool {
		return len(s.Namespaces) == 1 && s.Namespaces[0].Total.Reporting == 4
	})
	ns := snap.Namespaces[0]
	if !snap.Synced || snap.Err != nil || snap.Window != time.Minute {
		t.Errorf("snapshot %+v", snap)
	}
	if len(ns.Services) != 2 || ns.Services[0].Repo != "api" || ns.Services[1].Repo != "web" {
		t.Fatalf("services %+v", ns.Services)
	}
	api := ns.Services[0]
	if api.Pods != 2 || api.Reporting != 2 || api.Use != (domain.Amount{CPUMilli: 150, MemBytes: 1000 * miB}) {
		t.Errorf("api rollup %+v", api.Rollup)
	}
	if api.HottestPod != "api-1" {
		t.Errorf("hottest %q, want api-1", api.HottestPod)
	}
	if ns.Unlisted.Pods != 1 || ns.Unlisted.Use.MemBytes != 20*miB {
		t.Errorf("unlisted %+v", ns.Unlisted)
	}
	if ns.Total.Pods != 4 || ns.Total.Use != (domain.Amount{CPUMilli: 165, MemBytes: 1070 * miB}) {
		t.Errorf("total %+v: services and unlisted must add up", ns.Total)
	}
	if ns.Total.LimitSet.Mem {
		t.Error("a total with unlisted pods has no defined limit ratio")
	}
	if !snap.SampledAt.Equal(t0) {
		t.Errorf("sampled at %v", snap.SampledAt)
	}
}

func TestUsageContainerModeChangesTheSums(t *testing.T) {
	p := usagePod("app-rec", "api-1", "api", usageCtr("api", 1000*miB), usageCtr("istio-proxy", 100*miB))
	u := sampleOf(p, t0, 100, 300*miB)
	u.Containers[0].Name = "api"
	u.Containers = append(u.Containers, domain.ContainerUsage{Name: "istio-proxy", Use: domain.Amount{CPUMilli: 20, MemBytes: 50 * miB}})
	for mode, want := range map[domain.ContainerMode]int64{domain.ContainersApp: 300 * miB, domain.ContainersAll: 350 * miB} {
		clock := portstest.NewFakeClock(t0)
		m := portstest.NewFakeMetrics()
		m.Set("app-rec", u)
		ctx, cancel := context.WithCancel(context.Background())
		ch, _ := newUsage(catalogOf(p), m, clock, "app-rec").Watch(ctx, ports.UsageQuery{Env: "rec", Containers: mode})
		snap := nextUsage(t, ch, clock, func(s ports.UsageSnapshot) bool {
			return len(s.Namespaces) == 1 && s.Namespaces[0].Total.Reporting == 1
		})
		if got := snap.Namespaces[0].Total.Use.MemBytes; got != want {
			t.Errorf("%v: use %d, want %d", mode, got, want)
		}
		cancel()
	}
}

func TestUsagePerNamespaceErrors(t *testing.T) {
	ok := usagePod("app-rec", "api-1", "api", usageCtr("app", 1000*miB))
	denied := usagePod("batch-rec", "job-1", "job", usageCtr("app", 1000*miB))
	clock := portstest.NewFakeClock(t0)
	m := portstest.NewFakeMetrics()
	m.Set("app-rec", sampleOf(ok, t0, 10, 10*miB))
	m.SetErr("batch-rec", domain.ErrForbidden)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := newUsage(catalogOf(ok, denied), m, clock, "app-rec", "batch-rec").Watch(ctx, ports.UsageQuery{Env: "rec"})
	snap := nextUsage(t, ch, clock, func(s ports.UsageSnapshot) bool { return s.Synced && len(s.Namespaces) == 2 })
	if snap.Err != nil {
		t.Errorf("one namespace works, snapshot error %v", snap.Err)
	}
	if snap.Namespaces[0].Name != "app-rec" || snap.Namespaces[0].Err != nil || snap.Namespaces[0].Total.Reporting != 1 {
		t.Errorf("app-rec %+v", snap.Namespaces[0])
	}
	if b := snap.Namespaces[1]; !errors.Is(b.Err, domain.ErrForbidden) || b.Total.Reporting != 0 || b.Total.Pods != 1 {
		t.Errorf("batch-rec %+v", b)
	}
}

func TestUsageNoMetricsAnywhere(t *testing.T) {
	p := usagePod("app-rec", "api-1", "api", usageCtr("app", 1000*miB))
	clock := portstest.NewFakeClock(t0)
	m := portstest.NewFakeMetrics()
	m.Err = domain.ErrMetricsUnavailable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := newUsage(catalogOf(p), m, clock, "app-rec").Watch(ctx, ports.UsageQuery{Env: "rec"})
	snap := nextUsage(t, ch, clock, func(s ports.UsageSnapshot) bool { return s.Err != nil })
	if !errors.Is(snap.Err, domain.ErrMetricsUnavailable) {
		t.Errorf("error %v", snap.Err)
	}
}

func TestUsageKeepsStaleValuesAndBacksOff(t *testing.T) {
	p := usagePod("app-rec", "api-1", "api", usageCtr("app", 1000*miB))
	clock := portstest.NewFakeClock(t0)
	m := portstest.NewFakeMetrics()
	m.Set("app-rec", sampleOf(p, t0, 10, 10*miB))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := newUsage(catalogOf(p), m, clock, "app-rec").Watch(ctx, ports.UsageQuery{Env: "rec"})
	nextUsage(t, ch, clock, func(s ports.UsageSnapshot) bool {
		return len(s.Namespaces) == 1 && s.Namespaces[0].Total.Reporting == 1
	})

	m.SetErr("app-rec", domain.ErrUnreachable)
	before := m.Calls("app-rec")
	var stale ports.UsageSnapshot
	for i := 0; i < 40 && stale.StaleSince.IsZero(); i++ { // 40 refresh periods
		clock.Advance(15 * time.Second)
		select {
		case s := <-ch:
			stale = s
		case <-time.After(20 * time.Millisecond):
		}
	}
	if stale.StaleSince.IsZero() {
		t.Fatal("no stale snapshot")
	}
	if stale.Namespaces[0].Total.Reporting != 1 {
		t.Errorf("values dropped on failure: %+v", stale.Namespaces[0].Total)
	}
	if calls := m.Calls("app-rec") - before; calls >= 40 {
		t.Errorf("%d polls in 40 refresh periods: no backoff", calls)
	}
}

func TestUsageHistoryIsBounded(t *testing.T) {
	p := usagePod("app-rec", "api-1", "api", usageCtr("app", 1000*miB))
	clock := portstest.NewFakeClock(t0)
	m := portstest.NewFakeMetrics()
	m.Set("app-rec", sampleOf(p, t0, 10, 10*miB))
	u := newUsage(catalogOf(p), m, clock, "app-rec")
	u.Refresh, u.History = 5*time.Second, time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := u.Watch(ctx, ports.UsageQuery{Env: "rec"})
	var last ports.UsageSnapshot
	for i := 0; i < 60; i++ { // five minutes of five-second polls
		clock.Advance(5 * time.Second)
		select {
		case s := <-ch:
			last = s
		case <-time.After(20 * time.Millisecond):
		}
	}
	if n := len(last.Namespaces); n != 1 {
		t.Fatalf("%d namespaces", n)
	}
	if h := last.Namespaces[0].History; len(h) == 0 || len(h) > 13 {
		t.Errorf("history has %d points for 1 minute at 5s", len(h))
	}
	if h := last.Namespaces[0].Services[0].History; len(h) == 0 {
		t.Error("service history is empty")
	}
}

func TestUsageRefreshFloorAndStop(t *testing.T) {
	if got := (&Usage{Refresh: time.Second}).refresh(); got != MinUsageRefresh {
		t.Errorf("refresh %v, want the floor", got)
	}
	if got := (&Usage{}).refresh(); got != 15*time.Second {
		t.Errorf("default refresh %v", got)
	}
	p := usagePod("app-rec", "api-1", "api", usageCtr("app", 1000*miB))
	clock := portstest.NewFakeClock(t0)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := newUsage(catalogOf(p), portstest.NewFakeMetrics(), clock, "app-rec").Watch(ctx, ports.UsageQuery{Env: "rec"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel still open after cancel")
		}
	}
}
