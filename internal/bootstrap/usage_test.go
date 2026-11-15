package bootstrap

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
	"github.com/ghiloufibg/huginn/internal/diag"
)

// TestBuildDemoHasUsage: the demo cluster reads metrics, so the usage
// screen is available.
func TestBuildDemoHasUsage(t *testing.T) {
	app, err := Build(cli.Options{Demo: true}, noFiles(), diag.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if app.UI.Usage == nil {
		t.Error("no usage in demo mode")
	}
}

// onlyCluster hides everything a cluster adapter offers but the cluster port.
type onlyCluster struct{ ports.ClusterClient }

func TestNoUsageWithoutMetrics(t *testing.T) {
	c := demoConfig(t)
	clock := portstest.NewFakeClock(t0)
	cluster := demo.New(demo.Options{Seed: 1, Clock: clock})
	if newUsage(scopes(c, nil), nil, onlyCluster{cluster}, clock, domain.ContainerFilter{}, diag.Discard()) != nil {
		t.Error("usage wired over a cluster that reads no metrics")
	}
}

// TestDemoUsageEndToEnd runs the real wiring (shared catalog, usage use case)
// over the demo cluster.
func TestDemoUsageEndToEnd(t *testing.T) {
	c := demoConfig(t)
	clock := portstest.NewFakeClock(t0)
	cluster := demo.New(demo.Options{Seed: c.Huginn.Demo.Seed, Rate: c.Huginn.Demo.Rate, Clock: clock})
	sc := scopes(c, nil)
	filter := containerFilter(c)
	usage := newUsage(sc, newCatalog(c, sc, cluster, clock, filter, diag.Discard()), cluster, clock, filter, diag.Discard())
	ch, err := usage.Watch(t.Context(), ports.UsageQuery{Env: domain.Env("rec")})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(500 * time.Millisecond)
		select {
		case s := <-ch:
			if len(s.Namespaces) == 1 && s.Namespaces[0].Total.Reporting > 0 && len(s.Namespaces[0].Services) > 3 {
				ns := s.Namespaces[0]
				if ns.Total.Use.MemBytes == 0 || ns.Total.Pods < ns.Total.Reporting {
					t.Errorf("total %+v", ns.Total)
				}
				return
			}
		case <-time.After(2 * time.Millisecond):
		}
	}
	t.Fatal("no usage snapshot with services and samples")
}
