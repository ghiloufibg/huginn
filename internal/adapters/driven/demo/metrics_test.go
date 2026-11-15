package demo

import (
	"context"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

func TestMetricsSourceContract(t *testing.T) {
	portstest.RunMetricsSourceContract(t, func(t *testing.T) portstest.MetricsFixture {
		c, _ := newTest(t)
		return portstest.MetricsFixture{Source: c, Scope: recScope}
	})
}

func usageByPod(t *testing.T, c *Cluster) (map[domain.PodKey]domain.PodUsage, []domain.Pod) {
	t.Helper()
	us, err := c.PodUsage(context.Background(), recScope)
	if err != nil {
		t.Fatal(err)
	}
	pods, _ := c.ListPods(context.Background(), recScope, nil)
	m := map[domain.PodKey]domain.PodUsage{}
	for _, u := range us {
		m[u.Key()] = u
	}
	return m, pods
}

func TestMetricsAreDeterministic(t *testing.T) {
	a, _ := newTest(t)
	b, _ := newTest(t)
	ua, _ := usageByPod(t, a)
	ub, _ := usageByPod(t, b)
	if len(ua) == 0 || len(ua) != len(ub) {
		t.Fatalf("%d and %d samples", len(ua), len(ub))
	}
	for k, u := range ua {
		if got := ub[k]; len(got.Containers) != len(u.Containers) || got.Containers[0] != u.Containers[0] {
			t.Errorf("%v differs between two worlds with the same seed", k)
		}
	}
}

func TestMetricsScenarios(t *testing.T) {
	c, _ := newTest(t)
	usage, pods := usageByPod(t, c)

	rollup := func(repo string, all bool) domain.Rollup {
		var ps []domain.Pod
		for _, p := range pods {
			if p.Labels["app.kubernetes.io/part-of"] == repo {
				ps = append(ps, p)
			}
		}
		return domain.RollupPods(ps, usage, domain.ContainerFilter{Deny: []string{"istio-proxy", "vault-agent"}}, all)
	}

	t.Run("the OOM repository is close to its memory limit", func(t *testing.T) {
		r := rollup("order-orchestrator", false)
		if r.Reporting == 0 {
			t.Fatal("no pod reports")
		}
		for _, p := range pods { // the killed container runs no more: only its sidecars are measured
			for _, ct := range p.Containers {
				if ct.Reason != "OOMKilled" {
					continue
				}
				for _, cu := range usage[p.Key()].Containers {
					if cu.Name == ct.Name {
						t.Errorf("killed container %s has usage", ct.Name)
					}
				}
			}
		}
		// The repository's other workloads are healthy: the sum reads lower
		// than its hottest pod, which is what the peak column is for.
		if r.HottestMemPct < 90 {
			t.Errorf("hottest pod at %.0f%%", r.HottestMemPct)
		}
		if pct, ok := domain.Pct(r.Use.MemBytes, r.Limit.MemBytes); !ok || pct >= r.HottestMemPct {
			t.Errorf("sum at %.0f%% hides nothing from a hottest pod at %.0f%%", pct, r.HottestMemPct)
		}
	})

	t.Run("a pod that does not run has no sample", func(t *testing.T) {
		for _, p := range pods {
			if p.Phase != domain.PodRunning {
				if _, ok := usage[p.Key()]; ok {
					t.Errorf("%s is %s and has a sample", p.Name, p.Phase)
				}
			}
		}
	})

	t.Run("a container without a limit leaves no limit ratio", func(t *testing.T) {
		var noLimit bool
		for _, p := range pods {
			if p.Labels["app.kubernetes.io/part-of"] == "" {
				continue
			}
			r := rollup(p.Labels["app.kubernetes.io/part-of"], true)
			if r.Reporting > 0 && !r.LimitSet.Mem {
				noLimit = true
			}
		}
		if !noLimit {
			t.Error("no repository shows a container without a limit")
		}
	})

	t.Run("some repository is over-provisioned and the rest are not all", func(t *testing.T) {
		th := domain.Thresholds{Warn: 80, Crit: 95}
		seen := map[domain.Provisioning]int{}
		repos := map[string]bool{}
		for _, p := range pods {
			repos[p.Labels["app.kubernetes.io/part-of"]] = true
		}
		for repo := range repos {
			if repo != "" {
				seen[rollup(repo, false).Provision(th)]++
			}
		}
		if seen[domain.ProvisioningOver] == 0 || seen[domain.ProvisioningOK] == 0 {
			t.Errorf("provisioning across repositories: %v", seen)
		}
	})
}

func TestMetricsFollowTheClock(t *testing.T) {
	c, clock := newTest(t)
	first, _ := c.PodUsage(context.Background(), recScope)
	clock.Advance(10 * time.Minute)
	second, _ := c.PodUsage(context.Background(), recScope)
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("no samples")
	}
	if !second[0].SampledAt.After(first[0].SampledAt) {
		t.Errorf("sampled at %v then %v", first[0].SampledAt, second[0].SampledAt)
	}
	var _ ports.MetricsSource = c
}
