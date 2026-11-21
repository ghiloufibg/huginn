package portstest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

var t0 = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

func TestFakeClusterContract(t *testing.T) {
	RunClusterContract(t, func(t *testing.T) ClusterFixture {
		c := NewFakeCluster()
		ref := domain.WorkloadRef{Env: domain.Env("rec"), Namespace: "app-rec", Kind: domain.KindDeployment, Name: "api"}
		c.AddWorkload(domain.Workload{Ref: ref})
		c.AddWorkload(domain.Workload{Ref: domain.WorkloadRef{Env: domain.Env("prd"), Namespace: "app-prd", Name: "api"}})
		c.PutPod(domain.Pod{Env: domain.Env("rec"), Namespace: "app-rec", Name: "api-1", Labels: map[string]string{"app": "api"}})
		c.PutPod(domain.Pod{Env: domain.Env("rec"), Namespace: "app-rec", Name: "web-1", Labels: map[string]string{"app": "web"}})
		return ClusterFixture{Client: c, Scope: ports.Scope{Env: domain.Env("rec"), Namespaces: []string{"app-rec"}}, PodLabels: ports.Selector{"app": "api"}}
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
	ch, err := c.WatchPods(ctx, ports.Scope{Env: domain.Env("rec")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.PutPod(domain.Pod{Env: domain.Env("rec"), Namespace: "n", Name: "p"})
	c.DeletePod("n", "p")
	if ev := <-ch; ev.Type != domain.PodAdded {
		t.Fatalf("got %v", ev.Type)
	}
	if ev := <-ch; ev.Type != domain.PodDeleted || !ev.Pod.Deleted {
		t.Fatalf("got %+v", ev)
	}
}

func TestFakeReleaseHistoryContract(t *testing.T) {
	RunReleaseHistoryContract(t, func(_ *testing.T, fx ReleaseFixture) (ports.ReleaseHistory, ports.ReleaseProject) {
		return &FakeReleaseHistory{Fixture: fx}, ports.ReleaseProject{BaseURL: "https://gitlab.test", Path: "g/p"}
	})
}

func TestFakeCommitLogContract(t *testing.T) {
	RunCommitLogContract(t, func(*testing.T) (ports.CommitLog, ports.ReleaseProject, HistoryFixture) {
		fx := HistoryFixture{First: strings.Repeat("1", 40), Second: strings.Repeat("2", 40), Third: strings.Repeat("3", 40), Hotfix: strings.Repeat("4", 40)}
		at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		g := &FakeCommitLog{
			Commits: map[string]ports.Commit{
				fx.First: {Subject: "first", Author: "a", At: at}, fx.Second: {Subject: "second", Author: "a", At: at},
				fx.Third: {Subject: "third", Author: "a", At: at}, fx.Hotfix: {Subject: "hotfix", Author: "a", At: at},
			},
			Tags:     map[string]string{"v1.0": fx.First},
			Branches: map[string]string{"main": fx.Third},
			Behind:   map[string]int{fx.First: 2},
			Off:      map[string]bool{fx.Hotfix: true},
		}
		return &pathAware{g, fx}, ports.ReleaseProject{Path: "g/p"}, fx
	})
}

// pathAware gives the fake a per-path answer for the contract's one path case.
type pathAware struct {
	*FakeCommitLog
	fx HistoryFixture
}

func (p *pathAware) Count(ctx context.Context, rp ports.ReleaseProject, sha, branch string, paths []string) (int, bool, bool, error) {
	n, capped, on, err := p.FakeCommitLog.Count(ctx, rp, sha, branch, paths)
	if len(paths) > 0 && sha == p.fx.First {
		n = 1
	}
	return n, capped, on, err
}
