package portstest

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

var t0 = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

func TestFakeClusterContract(t *testing.T) {
	RunClusterContract(t, func(t *testing.T) ClusterFixture {
		c := NewFakeCluster()
		ref := domain.WorkloadRef{Env: domain.EnvRec, Namespace: "app-rec", Kind: domain.KindDeployment, Name: "api"}
		c.AddWorkload(domain.Workload{Ref: ref})
		c.AddWorkload(domain.Workload{Ref: domain.WorkloadRef{Env: domain.EnvPrd, Namespace: "app-prd", Name: "api"}})
		c.PutPod(domain.Pod{Env: domain.EnvRec, Namespace: "app-rec", Name: "api-1", Labels: map[string]string{"app": "api"}})
		c.PutPod(domain.Pod{Env: domain.EnvRec, Namespace: "app-rec", Name: "web-1", Labels: map[string]string{"app": "web"}})
		return ClusterFixture{Client: c, Scope: ports.Scope{Env: domain.EnvRec, Namespaces: []string{"app-rec"}}, PodLabels: ports.Selector{"app": "api"}}
	})
}

func TestFakeLogSourceContract(t *testing.T) {
	RunLogSourceContract(t, func(t *testing.T) LogFixture {
		clock := NewFakeClock(t0)
		src := NewFakeLogSource(clock)
		var lines []domain.RawLine
		for i := 20; i > 0; i-- {
			lines = append(lines, domain.RawLine{Time: t0.Add(-time.Duration(i) * time.Minute), Pod: "api-1", Container: "app", Text: "hello"})
		}
		src.SetLines("ns", "api-1", "app", lines, lines[:3])
		src.SetLines("ns", "api-2", "app", lines, nil)
		base := ports.LogRequest{Namespace: "ns", Pod: "api-1", Container: "app"}
		noPrev := base
		noPrev.Pod = "api-2"
		return LogFixture{Source: src, Clock: clock, Request: base, NoPrevRequest: noPrev}
	})
}

func TestFakeClockTicker(t *testing.T) {
	c := NewFakeClock(t0)
	tk := c.NewTicker(time.Second)
	c.Advance(500 * time.Millisecond)
	select {
	case <-tk.C():
		t.Fatal("ticked too early")
	default:
	}
	c.Advance(600 * time.Millisecond)
	select {
	case got := <-tk.C():
		if !got.Equal(t0.Add(time.Second)) {
			t.Fatalf("tick at %v", got)
		}
	default:
		t.Fatal("no tick")
	}
	tk.Stop()
	c.Advance(5 * time.Second)
	select {
	case <-tk.C():
		t.Fatal("tick after stop")
	default:
	}
}

func TestFakeClusterWatchDeliversChanges(t *testing.T) {
	c := NewFakeCluster()
	ctx := t.Context()
	ch, err := c.WatchPods(ctx, ports.Scope{Env: domain.EnvRec}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.PutPod(domain.Pod{Env: domain.EnvRec, Namespace: "n", Name: "p"})
	c.DeletePod("n", "p")
	if ev := <-ch; ev.Type != domain.PodAdded {
		t.Fatalf("got %v", ev.Type)
	}
	if ev := <-ch; ev.Type != domain.PodDeleted || !ev.Pod.Deleted {
		t.Fatalf("got %+v", ev)
	}
}
