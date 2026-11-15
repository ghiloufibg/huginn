package domain

import (
	"testing"
	"time"
)

const miB = 1 << 20

func mkCtr(name string, reqCPU, limCPU, reqMem, limMem int64) Container {
	return Container{Name: name, Amounts: ResourceAmounts{
		Request: Amount{CPUMilli: reqCPU, MemBytes: reqMem},
		Limit:   Amount{CPUMilli: limCPU, MemBytes: limMem},
	}}
}

func mkPod(name string, cs ...Container) Pod {
	return Pod{Namespace: "ns", Name: name, OwnerName: "app", Containers: cs}
}

func mkSample(p Pod, at time.Time, use ...ContainerUsage) PodUsage {
	return PodUsage{Namespace: p.Namespace, Name: p.Name, SampledAt: at, Containers: use}
}

func mkUsage(us ...PodUsage) map[PodKey]PodUsage {
	m := map[PodKey]PodUsage{}
	for _, u := range us {
		m[u.Key()] = u
	}
	return m
}

func TestRollupPods(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f := ContainerFilter{Deny: []string{"proxy"}}
	app := func(name string) Container { return mkCtr("app", 100, 200, 100*miB, 200*miB) }
	proxy := mkCtr("proxy", 10, 20, 50*miB, 100*miB)
	init := mkCtr("migrate", 500, 500, 500*miB, 500*miB)
	init.Init = true

	a, b := mkPod("a", app("a"), proxy, init), mkPod("b", app("b"), proxy)
	ua := mkSample(a, t0, ContainerUsage{"app", Amount{50, 190 * miB}}, ContainerUsage{"proxy", Amount{5, 40 * miB}})
	ub := mkSample(b, t0.Add(-time.Minute), ContainerUsage{"app", Amount{150, 20 * miB}}, ContainerUsage{"proxy", Amount{5, 40 * miB}})

	t.Run("all containers, init excluded", func(t *testing.T) {
		r := RollupPods([]Pod{a, b}, mkUsage(ua, ub), f, true)
		if r.Pods != 2 || r.Reporting != 2 {
			t.Fatalf("pods %d/%d", r.Reporting, r.Pods)
		}
		if want := (Amount{210, 290 * miB}); r.Use != want {
			t.Errorf("use %+v, want %+v", r.Use, want)
		}
		if want := (Amount{220, 300 * miB}); r.Request != want {
			t.Errorf("request %+v, want %+v", r.Request, want)
		}
		if !r.SampledAt.Equal(t0.Add(-time.Minute)) {
			t.Errorf("sampled at %v, want the oldest", r.SampledAt)
		}
	})
	t.Run("application containers only", func(t *testing.T) {
		r := RollupPods([]Pod{a, b}, mkUsage(ua, ub), f, false)
		if want := (Amount{200, 210 * miB}); r.Use != want {
			t.Errorf("use %+v, want %+v", r.Use, want)
		}
		if want := (Amount{400, 400 * miB}); r.Limit != want {
			t.Errorf("limit %+v, want %+v", r.Limit, want)
		}
	})
	t.Run("hottest pod is not hidden by the sum", func(t *testing.T) {
		r := RollupPods([]Pod{a, b}, mkUsage(ua, ub), f, false)
		if r.HottestPod != "a" || r.HottestMemPct < 94 || r.HottestMemPct > 96 {
			t.Errorf("hottest %s %.1f%%, want a 95%%", r.HottestPod, r.HottestMemPct)
		}
		if pct, _ := Pct(r.Use.MemBytes, r.Limit.MemBytes); pct > 60 {
			t.Errorf("sum ratio %.1f%% should read low next to the hot pod", pct)
		}
	})
	t.Run("a pod without a sample is not zero", func(t *testing.T) {
		r := RollupPods([]Pod{a, b}, mkUsage(ua), f, false)
		if r.Pods != 2 || r.Reporting != 1 {
			t.Fatalf("pods %d/%d, want 1/2", r.Reporting, r.Pods)
		}
		if r.Request != (Amount{100, 100 * miB}) || r.Limit != (Amount{200, 200 * miB}) {
			t.Errorf("request %+v limit %+v: only the reporting pod counts", r.Request, r.Limit)
		}
	})
	t.Run("nothing reporting defines no ratio", func(t *testing.T) {
		r := RollupPods([]Pod{a}, nil, f, true)
		if r.Reporting != 0 || r.LimitSet != (Coverage{}) || r.RequestSet != (Coverage{}) {
			t.Errorf("got %+v", r)
		}
		if got := r.Provision(Thresholds{80, 95}); got != ProvisioningUnknown {
			t.Errorf("provision %v", got)
		}
	})
	t.Run("one container without limit", func(t *testing.T) {
		c := mkPod("c", app("c"), mkCtr("sidecar-x", 10, 0, 10*miB, 0))
		both := mkSample(c, t0, ContainerUsage{"app", Amount{1, 1}}, ContainerUsage{"sidecar-x", Amount{1, 1}})
		r := RollupPods([]Pod{c}, mkUsage(both), ContainerFilter{}, true)
		if r.LimitSet.CPU || r.LimitSet.Mem || !r.RequestSet.CPU {
			t.Errorf("coverage limit %+v request %+v", r.LimitSet, r.RequestSet)
		}
		if r.HottestPod != "" {
			t.Errorf("hottest %q for a pod with an unbounded container", r.HottestPod)
		}
	})
	t.Run("a container that is not measured counts nowhere", func(t *testing.T) {
		c := mkPod("c", app("c"), mkCtr("sidecar-x", 10, 0, 10*miB, 0))
		only := mkSample(c, t0, ContainerUsage{"app", Amount{50, 100 * miB}})
		r := RollupPods([]Pod{c}, mkUsage(only), ContainerFilter{}, true)
		if r.Limit != (Amount{200, 200 * miB}) || !r.LimitSet.Mem || r.Request.CPUMilli != 100 {
			t.Errorf("limit %+v (set %+v) request %+v: the idle container must not count", r.Limit, r.LimitSet, r.Request)
		}
	})
}

func TestPct(t *testing.T) {
	if _, ok := Pct(5, 0); ok {
		t.Error("no ratio against an unset reference")
	}
	if got, ok := Pct(50, 200); !ok || got != 25 {
		t.Errorf("got %v %v", got, ok)
	}
}

func TestSeverity(t *testing.T) {
	th := Thresholds{Warn: 80, Crit: 95}
	for _, c := range []struct {
		pct  float64
		want Severity
	}{{0, SeverityOK}, {79.9, SeverityOK}, {80, SeverityWarn}, {94.9, SeverityWarn}, {95, SeverityCrit}, {130, SeverityCrit}} {
		if got := th.Severity(c.pct); got != c.want {
			t.Errorf("%.1f: got %v, want %v", c.pct, got, c.want)
		}
	}
}

func TestProvision(t *testing.T) {
	th := Thresholds{Warn: 80, Crit: 95}
	full := Coverage{CPU: true, Mem: true}
	base := Rollup{
		Reporting: 1, RequestSet: full, LimitSet: full,
		Request: Amount{1000, 1000 * miB}, Limit: Amount{2000, 2000 * miB},
	}
	for _, c := range []struct {
		name string
		use  Amount
		mod  func(*Rollup)
		want Provisioning
	}{
		{"ok", Amount{600, 600 * miB}, nil, ProvisioningOK},
		{"over", Amount{100, 100 * miB}, nil, ProvisioningOver},
		{"only cpu low is not over", Amount{100, 600 * miB}, nil, ProvisioningOK},
		{"under by memory", Amount{100, 1950 * miB}, nil, ProvisioningUnder},
		{"under by cpu", Amount{1950, 100 * miB}, nil, ProvisioningUnder},
		{"no limit: no under", Amount{100, 1950 * miB}, func(r *Rollup) { r.LimitSet = Coverage{} }, ProvisioningOK},
		{"no request: unknown", Amount{100, 100 * miB}, func(r *Rollup) { r.RequestSet.Mem = false }, ProvisioningUnknown},
	} {
		r := base
		r.Use = c.use
		if c.mod != nil {
			c.mod(&r)
		}
		if got := r.Provision(th); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPodDetails(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	b, a := mkPod("b", mkCtr("app", 100, 200, 100*miB, 200*miB)), mkPod("a", mkCtr("app", 100, 200, 100*miB, 200*miB), mkCtr("side", 10, 0, 10*miB, 0))
	a.Containers[1].Restarts = 3
	ua := mkSample(a, t0, ContainerUsage{"app", Amount{50, 150 * miB}})
	got := PodDetails([]Pod{b, a}, mkUsage(ua), ContainerFilter{}, true)
	if len(got) != 2 || got[0].Pod.Name != "a" || got[1].Pod.Name != "b" {
		t.Fatalf("order: %+v", got)
	}
	app, side := got[0].Containers[0], got[0].Containers[1]
	if !app.Measured || app.Use != (Amount{50, 150 * miB}) || app.Limit.MemBytes != 200*miB {
		t.Errorf("app %+v", app)
	}
	if side.Measured || side.Use != (Amount{}) || side.Restarts != 3 || side.Limit != (Amount{}) {
		t.Errorf("a container the sample does not cover must read as not measured: %+v", side)
	}
	if got[1].Reporting != 0 || got[1].Containers[0].Measured {
		t.Errorf("a pod without a sample: %+v", got[1])
	}
}
