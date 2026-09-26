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

var t0 = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

func scopes(namespaces ...string) func(domain.Env) (ports.Scope, bool) {
	return func(e domain.Env) (ports.Scope, bool) {
		return ports.Scope{Env: e, Namespaces: namespaces}, e == domain.Env("rec")
	}
}

func newCatalog(c ports.ClusterClient, clock ports.Clock, ns ...string) *Catalog {
	return &Catalog{
		Cluster: c, Clock: clock, Scopes: scopes(ns...),
		Resolver: LabelResolver{Keys: []string{"app.kubernetes.io/part-of"}},
		Filter:   domain.ContainerFilter{Deny: []string{"istio-proxy"}},
	}
}

// next advances the fake clock until a snapshot satisfying ok arrives.
func next(t *testing.T, ch <-chan ports.CatalogSnapshot, clock *portstest.FakeClock, ok func(ports.CatalogSnapshot) bool) ports.CatalogSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(100 * time.Millisecond)
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
	return ports.CatalogSnapshot{}
}

func deployment(ns, name, repo string, desired, ready int) domain.Workload {
	return domain.Workload{
		Ref:    domain.WorkloadRef{Env: domain.Env("rec"), Namespace: ns, Kind: domain.KindDeployment, Name: name},
		Labels: map[string]string{"app.kubernetes.io/part-of": repo}, DesiredReplicas: desired, ReadyReplicas: ready, UpdatedReplicas: desired,
		Selector: map[string]string{"app": name}, Created: t0.Add(-time.Hour),
	}
}

func appPod(ns, name, owner string, ready bool) domain.Pod {
	return domain.Pod{
		Env: domain.Env("rec"), Namespace: ns, Name: name, OwnerName: owner, Phase: domain.PodRunning,
		Labels:     map[string]string{"app": owner},
		Containers: []domain.Container{{Name: owner, Image: "r/" + owner + ":v1", State: domain.ContainerRunning, Ready: ready}},
	}
}

func TestCatalogSnapshotsAndUpdates(t *testing.T) {
	clock := portstest.NewFakeClock(t0)
	fc := portstest.NewFakeCluster()
	fc.AddWorkload(deployment("ns", "api", "shop", 1, 1))
	fc.AddWorkload(deployment("ns", "worker", "shop", 1, 1))
	fc.AddWorkload(deployment("ns", "stray", "", 1, 1))
	fc.PutPod(appPod("ns", "api-1", "api", true))
	fc.PutPod(appPod("ns", "worker-1", "worker", true))
	fc.PutPod(appPod("ns", "stray-1", "stray", true))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := newCatalog(fc, clock, "ns").Watch(ctx, domain.Env("rec"))
	if err != nil {
		t.Fatal(err)
	}
	s := next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return len(s.Services) == 2 && s.Synced })
	shop, stray := s.Services[0], s.Services[1]
	if shop.Repo != "shop" || shop.Workloads != 2 || shop.Status != domain.StatusHealthy || len(shop.Pods) != 2 {
		t.Fatalf("shop: %+v", shop)
	}
	if stray.Repo != "stray" || !stray.Unassigned {
		t.Fatalf("stray: %+v", stray)
	}

	crashing := appPod("ns", "api-1", "api", false)
	crashing.Containers[0].State, crashing.Containers[0].Reason = domain.ContainerWaiting, "CrashLoopBackOff"
	fc.PutPod(crashing)
	s = next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return s.Services[0].Status == domain.StatusCrashLoopBackOff })
	if !s.UpdatedAt.After(t0) {
		t.Fatalf("UpdatedAt %v", s.UpdatedAt)
	}
	fc.DeletePod("ns", "worker-1")
	next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return len(s.Services[0].Pods) == 1 })

	cancel()
	for range ch {
	}
}

func TestCatalogForbiddenNamespaceDoesNotHideOthers(t *testing.T) {
	clock := portstest.NewFakeClock(t0)
	fc := portstest.NewFakeCluster()
	fc.NamespaceErr = map[string]error{"secret-ns": domain.ErrForbidden}
	fc.AddWorkload(deployment("ok-ns", "api", "shop", 1, 1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := newCatalog(fc, clock, "ok-ns", "secret-ns").Watch(ctx, domain.Env("rec"))
	s := next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return len(s.Services) == 1 && s.NamespaceErrs["secret-ns"] != nil })
	if s.Err != nil || s.Synced || !errors.Is(s.NamespaceErrs["secret-ns"], domain.ErrForbidden) {
		t.Fatalf("snapshot: %+v", s)
	}
}

func TestCatalogReportsErrorAndRecovers(t *testing.T) {
	clock := portstest.NewFakeClock(t0)
	fc := portstest.NewFakeCluster()
	fc.AddWorkload(deployment("ns", "api", "shop", 1, 1))
	fc.SetErr(domain.ErrUnauthorized)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := newCatalog(fc, clock, "ns").Watch(ctx, domain.Env("rec"))
	s := next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return s.Err != nil })
	if !errors.Is(s.Err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v", s.Err)
	}
	fc.SetErr(nil)
	next(t, ch, clock, func(s ports.CatalogSnapshot) bool { return s.Err == nil && len(s.Services) == 1 && s.Synced })
}

func TestCatalogUnknownEnv(t *testing.T) {
	c := newCatalog(portstest.NewFakeCluster(), portstest.NewFakeClock(t0), "ns")
	if _, err := c.Watch(context.Background(), domain.Env("prd")); err == nil {
		t.Fatal("expected error")
	}
}

func BenchmarkSnapshot500Repos(b *testing.B) {
	st := &state{workloads: map[string]domain.Workload{}, pods: map[string]domain.Pod{}, nsErr: map[string]error{}, connected: map[string]bool{"ns": true}}
	for i := range 500 {
		name := "svc-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		st.workloads[key("ns", name)] = deployment("ns", name, name, 6, 6)
		for j := range 6 {
			p := appPod("ns", name+"-"+string(rune('a'+j)), name, true)
			st.pods[key("ns", p.Name)] = p
		}
	}
	c := newCatalog(nil, portstest.NewFakeClock(t0), "ns")
	b.ResetTimer()
	for b.Loop() {
		c.snapshot(context.Background(), domain.Env("rec"), []string{"ns"}, st)
	}
}
