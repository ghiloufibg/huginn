package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// fakeUsage records Watch queries; tests feed snapshots directly.
type fakeUsage struct {
	mu      sync.Mutex
	queries []ports.UsageQuery
	ctxs    []context.Context
}

func (f *fakeUsage) Watch(ctx context.Context, q ports.UsageQuery) (<-chan ports.UsageSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.ctxs = append(f.ctxs, ctx)
	return make(chan ports.UsageSnapshot), nil
}

func (f *fakeUsage) last() ports.UsageQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries[len(f.queries)-1]
}

const (
	mi = int64(1) << 20
	gi = int64(1) << 30
)

// rollup builds a rollup with every ratio defined.
func rollup(pods, reporting int, cpuUse, cpuReq, cpuLim, memUse, memReq, memLim int64) domain.Rollup {
	full := domain.Coverage{CPU: true, Mem: true}
	return domain.Rollup{
		Pods: pods, Reporting: reporting,
		Use:        domain.Amount{CPUMilli: cpuUse, MemBytes: memUse},
		Request:    domain.Amount{CPUMilli: cpuReq, MemBytes: memReq},
		Limit:      domain.Amount{CPUMilli: cpuLim, MemBytes: memLim},
		RequestSet: full, LimitSet: full,
		SampledAt: t0.Add(-12 * time.Second),
	}
}

func withPeak(r domain.Rollup, pod string, pct float64) domain.Rollup {
	r.HottestPod, r.HottestMemPct = pod, pct
	return r
}

// usageSnapshot is the mock of docs/plan/M21: a busy namespace with a service
// near its memory limit, an over-provisioned one, one without limit and a pod
// not reporting, plus a namespace the user cannot read.
func usageSnapshot() ports.UsageSnapshot {
	noLimit := rollup(3, 2, 400, 600, 0, 1*gi, 1536*mi, 0)
	noLimit.LimitSet = domain.Coverage{}
	noLimit.Limit = domain.Amount{}
	unlisted := domain.Rollup{Pods: 3, Reporting: 3, Use: domain.Amount{CPUMilli: 100, MemBytes: 400 * mi}, SampledAt: t0.Add(-12 * time.Second)}
	total := rollup(31, 31, 14_200, 20_000, 29_500, 38*gi+100*mi, 41*gi, 49*gi)
	return ports.UsageSnapshot{
		Env: "rec", Synced: true, SampledAt: t0.Add(-12 * time.Second), Window: time.Minute,
		Namespaces: []domain.NamespaceUsage{
			{
				Name: "app-rec", Total: total, Unlisted: unlisted,
				Services: []domain.ServiceUsage{
					{
						Repo: "payment-service", Rollup: withPeak(rollup(4, 4, 1900, 2000, 3000, 7*gi+200*mi, 7*gi+300*mi, 7*gi+500*mi), "payment-service-7c9f-x2", 96),
						History: rampHistory(), PodRows: usagePaymentPods(),
					},
					{Repo: "order-orchestrator", Rollup: withPeak(rollup(6, 6, 2400, 3000, 6000, 9*gi+600*mi, 11*gi+800*mi, 16*gi), "order-orchestrator-5b8d-q1", 71)},
					{Repo: "pricing-engine", Rollup: withPeak(rollup(3, 3, 200, 1500, 3000, 1*gi+100*mi, 6*gi, 12*gi), "pricing-engine-6f4a-c7", 20)},
					{Repo: "ledger-writer", Rollup: withPeak(noLimit, "ledger-writer-77d1-a0", 0)},
					{Repo: "orphan-job", Unassigned: true, Rollup: rollup(1, 1, 50, 100, 200, 100*mi, 256*mi, 512*mi)},
				},
			},
			{Name: "app-rec-2", Total: rollup(9, 9, 1100, 2750, 5000, 5*gi, 8*gi, 11*gi), Services: []domain.ServiceUsage{
				{Repo: "batch-runner", Rollup: rollup(9, 9, 1100, 2750, 5000, 5*gi, 8*gi, 11*gi)},
			}},
			{Name: "batch-prd", Err: domain.KindError(domain.ErrForbidden, "forbidden")},
		},
	}
}

// rampHistory is a memory that climbs for fifteen minutes.
func rampHistory() []domain.UsageSample {
	var h []domain.UsageSample
	for i := 0; i < 20; i++ {
		h = append(h, domain.UsageSample{At: t0.Add(time.Duration(i-20) * 45 * time.Second), Use: domain.Amount{MemBytes: int64(i+1) * 350 * mi}})
	}
	return h
}

func usagePaymentPods() []domain.PodDetail {
	pod := func(name string, restarts int, mem int64, sidecar bool) domain.PodDetail {
		app := domain.ContainerDetail{
			Name: "payment-service", Measured: true, Use: domain.Amount{CPUMilli: 480, MemBytes: mem},
			Request: domain.Amount{CPUMilli: 500, MemBytes: 1900 * mi}, Limit: domain.Amount{CPUMilli: 750, MemBytes: 2 * gi}, Restarts: restarts,
		}
		cs := []domain.ContainerDetail{app}
		if sidecar {
			cs = append(cs, domain.ContainerDetail{Name: "istio-proxy", Measured: false, Request: domain.Amount{CPUMilli: 100, MemBytes: 128 * mi}})
		}
		return domain.PodDetail{
			Pod:        domain.Pod{Name: name, Node: "gke-main-pool-1", Phase: domain.PodRunning, OwnerName: "payment-service", Containers: []domain.Container{{Name: "payment-service", Restarts: restarts}}},
			Rollup:     rollup(1, 1, 480, 500, 750, mem, 1900*mi, 2*gi),
			Containers: cs,
		}
	}
	return []domain.PodDetail{pod("payment-service-7c9f-x2", 2, 1950*mi, true), pod("payment-service-7c9f-k8", 0, 1600*mi, false)}
}

func openUsage(t *testing.T, repo string) (*Model, *usageScreen, *fakeUsage) {
	t.Helper()
	m, fc := newTestModel(t, 1, "")
	fu := &fakeUsage{}
	m.opts.Usage = fu
	_ = fc
	snapshot(m, ports.CatalogSnapshot{Env: "rec", Synced: true, Services: []domain.ServiceSummary{{Repo: "payment-service"}, {Repo: "ledger-writer"}}})
	_ = repo
	press(m, "u")
	s, ok := m.top().(*usageScreen)
	if !ok {
		t.Fatalf("top screen is %T, want the usage screen", m.top())
	}
	return m, s, fu
}

func feedUsage(m *Model, s *usageScreen, snap ports.UsageSnapshot) {
	m.Update(usageMsg{screen: s, gen: s.gen, snap: snap, ch: make(chan ports.UsageSnapshot)})
}

func TestUsageScreenGolden(t *testing.T) {
	m, s, fu := openUsage(t, "")
	if q := fu.last(); q.Env != "rec" || q.Containers != domain.ContainersAll {
		t.Errorf("query %+v", q)
	}
	golden(t, "usage_loading_120x16", render(m, 120, 16))
	feedUsage(m, s, usageSnapshot())
	golden(t, "usage_160x24", render(m, 160, 24))
	golden(t, "usage_120x20", render(m, 120, 20))
	golden(t, "usage_80x16", render(m, 80, 16))
	press(m, "k", "k") // the services screen closes nothing: the cursor starts on the first row
	press(m, "esc")
	if _, ok := m.top().(*servicesScreen); !ok {
		t.Errorf("esc leaves %T", m.top())
	}
}

func TestUsageValuesAreInTheRows(t *testing.T) {
	m, s, _ := openUsage(t, "")
	feedUsage(m, s, usageSnapshot())
	out := render(m, 160, 24)
	for _, want := range []string{
		"app-rec", "payment-service", "7.2Gi", "96%", "MEM 96% of limit", // near the limit, said in words
		"over-provisioned", "no limit", "1 pod not reporting", "(not in the catalog)",
		"no access: pods.metrics.k8s.io", "orphan-job (no repo)",
		"sampled 12s ago", "1m window", usageFootnote,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
}

func TestUsageFocusAndFold(t *testing.T) {
	m, s, _ := openUsage(t, "")
	feedUsage(m, s, usageSnapshot())
	// The screen was opened from the services screen with the cursor on
	// ledger-writer: the cursor starts there.
	if r := s.current(); r == nil || r.name != "ledger-writer" {
		t.Fatalf("cursor on %+v, want the service the screen was opened from", r)
	}
	press(m, "g")
	// Sorted by memory, app-rec comes first (38Gi).
	if r := s.current(); r.kind != usageNamespace || r.name != "app-rec" {
		t.Errorf("cursor on %+v, want the first namespace", r)
	}
	press(m, "space")
	if !s.collapsed["app-rec"] {
		t.Error("space does not fold the namespace")
	}
	out := render(m, 120, 20)
	if strings.Contains(out, "payment-service") || !strings.Contains(out, "▸ app-rec") {
		t.Errorf("folded namespace still lists its services:\n%s", out)
	}
	press(m, "right")
	if s.collapsed["app-rec"] {
		t.Error("right does not unfold")
	}
	press(m, "j", "left") // on a service: left folds its namespace and selects it
	if !s.collapsed["app-rec"] || s.current().kind != usageNamespace {
		t.Errorf("left on a service: %+v folded=%v", s.current(), s.collapsed["app-rec"])
	}
}

func TestUsageFocusedOnTheOpeningService(t *testing.T) {
	m, fc := newTestModel(t, 1, "")
	_ = fc
	m.opts.Usage = &fakeUsage{}
	snapshot(m, ports.CatalogSnapshot{Env: "rec", Synced: true, Services: []domain.ServiceSummary{
		{Repo: "alpha"}, {Repo: "ledger-writer"},
	}})
	press(m, "j") // services are sorted by status then name: the cursor moves off the first row
	want := m.top().(*servicesScreen).current(m).Repo
	press(m, "u")
	s := m.top().(*usageScreen)
	snap := usageSnapshot()
	snap.Namespaces[0].Services = append(snap.Namespaces[0].Services, domain.ServiceUsage{Repo: want, Rollup: rollup(1, 1, 1, 1, 1, mi, mi, mi)})
	feedUsage(m, s, snap)
	if r := s.current(); r == nil || r.name != want || r.kind != usageService {
		t.Errorf("cursor on %+v, want service %q", r, want)
	}
}

func TestUsageSortFilterAndContainers(t *testing.T) {
	m, s, fu := openUsage(t, "")
	feedUsage(m, s, usageSnapshot())

	names := func() []string {
		var out []string
		for _, r := range s.rows {
			if r.kind == usageService && r.ns == "app-rec" {
				out = append(out, r.name)
			}
		}
		return out
	}
	if got := strings.Join(names(), ","); got != "order-orchestrator,payment-service,pricing-engine,ledger-writer,orphan-job" {
		t.Errorf("by memory: %s", got)
	}
	press(m, "s") // cpu
	if got := strings.Join(names(), ","); got != "order-orchestrator,payment-service,ledger-writer,pricing-engine,orphan-job" {
		t.Errorf("by cpu: %s", got)
	}
	press(m, "s") // memory % of limit: payment-service is at 96%, the one without limit is last
	got := names()
	if got[0] != "payment-service" || got[len(got)-1] != "ledger-writer" {
		t.Errorf("by memory %% of limit: %v", got)
	}
	press(m, "s") // name
	if got := strings.Join(names(), ","); got != "ledger-writer,order-orchestrator,orphan-job,payment-service,pricing-engine" {
		t.Errorf("by name: %s", got)
	}

	press(m, "/")
	for _, k := range []string{"o", "r", "d"} {
		press(m, k)
	}
	press(m, "enter")
	if got := strings.Join(names(), ","); got != "order-orchestrator" {
		t.Errorf("filter ord: %s", got)
	}
	if out := render(m, 120, 20); strings.Contains(out, "(not in the catalog)") || strings.Contains(out, "batch-prd") {
		t.Errorf("a filter keeps rows it does not match:\n%s", out)
	}
	press(m, "esc") // clears the filter, does not leave
	if _, ok := m.top().(*usageScreen); !ok || s.filter.String() != "" {
		t.Errorf("esc with a filter: %T %q", m.top(), s.filter.String())
	}

	before := len(fu.queries)
	press(m, "A") // application containers only: a new watch
	if q := fu.last(); len(fu.queries) != before+1 || q.Containers != domain.ContainersApp {
		t.Errorf("A: %d queries, last %+v", len(fu.queries), q)
	}
	if !strings.Contains(render(m, 120, 20), "reading usage") {
		t.Error("the screen should wait for the new watch")
	}
}

func TestUsageOpensLogsOfAService(t *testing.T) {
	m, s, _ := openUsage(t, "")
	feedUsage(m, s, usageSnapshot())
	press(m, "g", "j", "L") // the first service of the first namespace
	l, ok := m.top().(*logsScreen)
	if !ok || l.repo != s.rows[1].name {
		t.Fatalf("top %T, want the logs of %q", m.top(), s.rows[1].name)
	}
}

func TestUsageErrorStates(t *testing.T) {
	t.Run("no metrics-server", func(t *testing.T) {
		m, s, _ := openUsage(t, "")
		feedUsage(m, s, ports.UsageSnapshot{Env: "rec", Err: domain.KindError(domain.ErrMetricsUnavailable, "metrics.k8s.io is not served (is metrics-server installed?)")})
		golden(t, "usage_unavailable_120x16", render(m, 120, 16))
	})
	t.Run("the watch does not start", func(t *testing.T) {
		m, s, _ := openUsage(t, "")
		m.Update(usageStartedMsg{screen: s, gen: s.gen, err: errors.New("boom")})
		if out := render(m, 120, 16); !strings.Contains(out, "boom") {
			t.Errorf("error not shown:\n%s", out)
		}
	})
	t.Run("stale values are kept and dated", func(t *testing.T) {
		m, s, _ := openUsage(t, "")
		snap := usageSnapshot()
		snap.StaleSince = t0.Add(-3 * time.Minute)
		feedUsage(m, s, snap)
		if out := render(m, 160, 24); !strings.Contains(out, "stale since 19:11 (3m)") || !strings.Contains(out, "payment-service") {
			t.Errorf("stale state:\n%s", out)
		}
	})
	t.Run("an older watch is ignored", func(t *testing.T) {
		m, s, _ := openUsage(t, "")
		old := s.gen
		press(m, "r") // reopens: a new generation
		m.Update(usageMsg{screen: s, gen: old, snap: usageSnapshot(), ch: make(chan ports.UsageSnapshot)})
		if s.snap != nil {
			t.Error("a snapshot of a cancelled watch was applied")
		}
	})
}

func TestUsageWithoutMetricsSource(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, ports.CatalogSnapshot{Env: "rec", Synced: true, Services: []domain.ServiceSummary{{Repo: "alpha"}}})
	press(m, "u")
	if _, ok := m.top().(*servicesScreen); !ok {
		t.Fatalf("top %T: no source, no screen", m.top())
	}
	if !strings.Contains(m.flashText, "no usage metrics") {
		t.Errorf("flash %q", m.flashText)
	}
}

func TestUsageWatchEndsWithTheScreen(t *testing.T) {
	m, _, fu := openUsage(t, "")
	press(m, "esc")
	select {
	case <-fu.ctxs[0].Done():
	case <-time.After(time.Second):
		t.Error("the usage watch keeps running after the screen is closed")
	}
}

func TestUsageNumberFormats(t *testing.T) {
	for _, c := range []struct {
		milli int64
		want  string
	}{{0, "0m"}, {250, "250m"}, {999, "999m"}, {1000, "1.00"}, {1900, "1.90"}, {14_200, "14.2"}} {
		if got := formatCPU(c.milli); got != c.want {
			t.Errorf("cpu %d: %q, want %q", c.milli, got, c.want)
		}
	}
	for _, c := range []struct {
		b    int64
		want string
	}{{512 * 1024, "512Ki"}, {128 * mi, "128Mi"}, {1023 * mi, "1023Mi"}, {gi, "1.0Gi"}, {7*gi + 300*mi, "7.3Gi"}, {38 * gi, "38Gi"}} {
		if got := formatMemory(c.b); got != c.want {
			t.Errorf("memory %d: %q, want %q", c.b, got, c.want)
		}
	}
}

func TestUsageDrillDown(t *testing.T) {
	m, s, _ := openUsage(t, "")
	feedUsage(m, s, usageSnapshot())
	press(m, "g")
	var payment int
	for i, r := range s.rows {
		if r.kind == usageService && r.name == "payment-service" {
			payment = i
		}
	}
	for s.tbl.cursor != payment {
		press(m, "j")
	}
	if out := render(m, 160, 24); !strings.Contains(out, "▸   payment-service") && !strings.Contains(out, "▸ payment-service") {
		t.Errorf("an expandable service carries a marker:\n%s", out)
	}
	press(m, "enter") // the service opens on its pods
	out := render(m, 160, 24)
	for _, want := range []string{"payment-service-7c9f-x2", "payment-service-7c9f-k8", "▾ payment-service"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded service lacks %q:\n%s", want, out)
		}
	}
	if s.current().name != "payment-service" {
		t.Errorf("the cursor left the service: %+v", s.current().name)
	}
	press(m, "j", "enter") // the first pod opens on its containers
	out = render(m, 160, 24)
	if !strings.Contains(out, "istio-proxy") || !strings.Contains(out, "not measured (not running)") {
		t.Errorf("containers of the pod:\n%s", out)
	}
	golden(t, "usage_drilldown_160x24", out)
	press(m, "enter") // and closes again
	if strings.Contains(render(m, 160, 24), "istio-proxy") {
		t.Error("enter on an open pod does not close it")
	}
	press(m, "k", "enter")
	if strings.Contains(render(m, 160, 24), "payment-service-7c9f-x2") {
		t.Error("enter on an open service does not close it")
	}
}

func TestUsagePreviewPanel(t *testing.T) {
	m, s, _ := openUsage(t, "")
	m.snap.Services[0].Status = domain.StatusOOMKilled
	feedUsage(m, s, usageSnapshot())
	press(m, "p", "g")
	if !s.preview {
		t.Fatal("p does not open the panel")
	}
	golden(t, "usage_preview_namespace_140x20", render(m, 140, 20))
	press(m, "j", "j") // payment-service is the second service by memory
	out := render(m, 140, 20)
	if !strings.Contains(out, "service payment-service in app-rec") || !strings.Contains(out, "requests") {
		t.Errorf("service panel:\n%s", out)
	}
	if out := render(m, 140, 10); strings.Contains(out, "service payment-service in app-rec") {
		t.Error("a panel must never take the table's last lines on a short terminal")
	}
	press(m, "p")
	if s.preview || strings.Contains(render(m, 140, 20), "service payment-service in app-rec") {
		t.Error("p does not close the panel")
	}
}

func TestSparkline(t *testing.T) {
	mem := func(vs ...int64) []domain.UsageSample {
		var h []domain.UsageSample
		for _, v := range vs {
			h = append(h, domain.UsageSample{Use: domain.Amount{MemBytes: v}})
		}
		return h
	}
	for _, c := range []struct {
		name string
		h    []domain.UsageSample
		w    int
		want string
	}{
		{"one point is no trend", mem(5), 10, ""},
		{"flat reads as flat and high", mem(4, 4, 4, 4), 4, "████"},
		{"zero stays low", mem(0, 0, 0), 3, "▁▁▁"},
		{"a ramp rises", mem(1, 2, 3, 4, 5, 6, 7, 8), 8, "▁▂▃▄▅▆▇█"},
		{"more samples than cells are averaged", mem(1, 1, 8, 8), 2, "▁█"},
	} {
		if got := sparkline(c.h, c.w); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
