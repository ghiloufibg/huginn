package app

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// bare returns a pod without owner.
func bare(name string, labels map[string]string) domain.Pod {
	p := appPod("ns", name, "", true)
	p.Labels = labels
	p.Containers[0].Name = "sh"
	return p
}

func TestCatalogListsStandalonePods(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		clock := portstest.NewFakeClock(t0)
		fc := portstest.NewFakeCluster()
		fc.AddWorkload(deployment("ns", "api", "shop", 1, 1))
		fc.PutPod(appPod("ns", "api-1", "api", true))
		fc.PutPod(bare("debug-shell", map[string]string{"run": "debug-shell"}))
		fc.PutPod(bare("shop-debug", map[string]string{"app.kubernetes.io/part-of": "shop"}))
		job := appPod("ns", "migrate-x1", "migrate", true)
		job.OwnerKind, job.Labels = "Job", nil
		fc.PutPod(job)
		c := newCatalog(fc, clock, "ns")
		c.Standalone = enabled
		ctx, cancel := context.WithCancel(context.Background())
		ch, _ := c.Watch(ctx, domain.Env("rec"))
		s := next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return len(s.Services) > 0 })
		cancel()
		rows := map[string]domain.ServiceSummary{}
		for _, r := range s.Services {
			rows[r.Repo] = r
		}
		if !enabled {
			if len(rows) != 1 || rows["shop"].Workloads != 1 {
				t.Fatalf("disabled: %v", rows)
			}
			continue
		}
		// the labelled debug pod joins its repository; the others are rows
		if shop := rows["shop"]; shop.Workloads != 2 || len(shop.Pods) != 2 {
			t.Errorf("shop: %d workloads, %d pods", shop.Workloads, len(shop.Pods))
		}
		for name, kind := range map[string]domain.WorkloadKind{"debug-shell": domain.KindPod, "migrate": "Job"} {
			r, ok := rows[name]
			if !ok || !r.Unassigned || len(r.Pods) != 1 || r.WorkloadStates[0].Ref.Kind != kind || !r.WorkloadStates[0].Standalone {
				t.Errorf("%s: %+v", name, r)
			}
		}
	}
}

func TestLogsOfAStandalonePod(t *testing.T) {
	f := newFixture(t)
	f.s.Standalone = true
	f.cluster.PutPod(bare("debug-shell", map[string]string{"run": "debug-shell"}))
	f.logs.SetLines("ns", "debug-shell", "sh", []domain.RawLine{{Time: t0.Add(-time.Minute), Pod: "debug-shell", Container: "sh", Text: "hello from debug"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := f.s.Open(ctx, ports.LogQuery{Env: "rec", Repo: "debug-shell", Window: domain.TimeWindow{Since: 15 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	r := &reader{t: t, ch: ch, clock: f.clock}
	r.until(10*time.Millisecond, func() bool { return r.history })
	if !slices.Equal(r.entries, []string{"hello from debug"}) || len(r.pods) != 1 {
		t.Fatalf("entries %v, pods %d", r.entries, len(r.pods))
	}
}

func TestAllContainersMode(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Containers: domain.ContainersAll})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if !slices.Contains(r.entries, "envoy") {
		t.Fatalf("sidecar not streamed: %v", r.entries)
	}
	for _, p := range r.pods {
		if p.Pod.Name == "api-1" && (len(p.Containers) != 2 || p.Roles["istio-proxy"] != domain.RoleSidecar || p.Roles["api"] != domain.RoleApp) {
			t.Errorf("pod state %+v", p)
		}
	}
	r2, cancel2 := f.open(t, ports.LogQuery{})
	defer cancel2()
	r2.until(10*time.Millisecond, func() bool { return r2.history })
	if slices.Contains(r2.entries, "envoy") {
		t.Fatal("app mode streams the sidecar")
	}
}
