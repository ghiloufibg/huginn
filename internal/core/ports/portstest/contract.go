package portstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// ClusterFixture is what a ClusterClient implementation provides to the
// contract suite: a client and a scope that contains at least one workload
// and one pod labelled with PodLabels.
type ClusterFixture struct {
	Client    ports.ClusterClient
	Scope     ports.Scope
	PodLabels ports.Selector
}

// RunClusterContract checks the behavior every ClusterClient must have.
func RunClusterContract(t *testing.T, newFixture func(t *testing.T) ClusterFixture) {
	t.Helper()
	ctx := context.Background()

	t.Run("workloads are in scope", func(t *testing.T) {
		f := newFixture(t)
		ws, err := f.Client.ListWorkloads(ctx, f.Scope)
		if err != nil || len(ws) == 0 {
			t.Fatalf("ListWorkloads: %d, %v", len(ws), err)
		}
		for _, w := range ws {
			if w.Ref.Env != f.Scope.Env || !contains(f.Scope.Namespaces, w.Ref.Namespace) {
				t.Errorf("workload %s/%s out of scope", w.Ref.Namespace, w.Ref.Name)
			}
		}
	})

	t.Run("pods match selector", func(t *testing.T) {
		f := newFixture(t)
		pods, err := f.Client.ListPods(ctx, f.Scope, f.PodLabels)
		if err != nil || len(pods) == 0 {
			t.Fatalf("ListPods: %d, %v", len(pods), err)
		}
		for _, p := range pods {
			if !f.PodLabels.Matches(p.Labels) || p.Env != f.Scope.Env {
				t.Errorf("pod %s does not match", p.Name)
			}
		}
	})

	t.Run("watch sends current state then closes on cancel", func(t *testing.T) {
		f := newFixture(t)
		wctx, cancel := context.WithCancel(ctx)
		ch, err := f.Client.WatchPods(wctx, f.Scope, f.PodLabels)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case ev := <-ch:
			if ev.Type != domain.PodAdded {
				t.Errorf("first event type = %v, want added", ev.Type)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no initial event")
		}
		cancel()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatal("watch not closed after cancel")
			}
		}
	})

	t.Run("workload watch closes on cancel", func(t *testing.T) {
		f := newFixture(t)
		wctx, cancel := context.WithCancel(ctx)
		ch, err := f.Client.WatchWorkloads(wctx, f.Scope)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		drainUntilClosed(t, ch)
	})
}

// LogFixture is what a LogSource implementation provides to the contract
// suite: a source, a clock it uses, and a container that has at least
// three lines of history and a previous instance, plus one that has no
// previous instance.
type LogFixture struct {
	Source        ports.LogSource
	Clock         ports.Clock
	Request       ports.LogRequest
	NoPrevRequest ports.LogRequest
}

// RunLogSourceContract checks the behavior every LogSource must have.
func RunLogSourceContract(t *testing.T, newFixture func(t *testing.T) LogFixture) {
	t.Helper()
	ctx := context.Background()

	t.Run("tail limits lines and lines are attributed and ordered", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Window = domain.TimeWindow{Tail: 2}
		lines := collect(t, ctx, f.Source, req)
		if len(lines) == 0 || len(lines) > 2 {
			t.Fatalf("got %d lines, want 1..2", len(lines))
		}
		for i, l := range lines {
			if l.Pod != req.Pod || l.Container != req.Container {
				t.Errorf("line %d attributed to %s/%s", i, l.Pod, l.Container)
			}
			if i > 0 && l.Time.Before(lines[i-1].Time) {
				t.Errorf("line %d out of order", i)
			}
		}
	})

	t.Run("since excludes older lines", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Window = domain.TimeWindow{Since: 5 * time.Minute}
		cutoff := f.Clock.Now().Add(-5 * time.Minute)
		for _, l := range collect(t, ctx, f.Source, req) {
			if !l.Time.IsZero() && l.Time.Before(cutoff) {
				t.Fatalf("line at %v older than window start %v", l.Time, cutoff)
			}
		}
	})

	t.Run("since time excludes older lines", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Window = domain.TimeWindow{Tail: 3}
		lines := collect(t, ctx, f.Source, req)
		req.Window, req.SinceTime = domain.TimeWindow{}, lines[len(lines)-1].Time
		for _, l := range collect(t, ctx, f.Source, req) {
			// Second precision is allowed (the Kubernetes API).
			if l.Time.Before(req.SinceTime.Truncate(time.Second)) {
				t.Fatalf("line at %v before %v", l.Time, req.SinceTime)
			}
		}
	})

	t.Run("limit keeps the most recent lines", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Window = domain.TimeWindow{Since: 48 * time.Hour}
		all := collect(t, ctx, f.Source, req)
		req.Limit = 2
		got := collect(t, ctx, f.Source, req)
		if len(got) != min(2, len(all)) || got[len(got)-1].Text != all[len(all)-1].Text {
			t.Fatalf("limit 2: %d lines, last %q", len(got), got[len(got)-1].Text)
		}
	})

	t.Run("unknown pod is not found", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Pod = "does-not-exist-0"
		_, err := f.Source.Stream(ctx, req)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("previous", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Previous, req.Window = true, domain.TimeWindow{Tail: 100}
		if lines := collect(t, ctx, f.Source, req); len(lines) == 0 {
			t.Fatal("previous instance returned no lines")
		}
		req = f.NoPrevRequest
		req.Previous, req.Window = true, domain.TimeWindow{Tail: 100}
		if _, err := f.Source.Stream(ctx, req); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("previous without restart: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("follow closes on cancel", func(t *testing.T) {
		f := newFixture(t)
		req := f.Request
		req.Follow, req.Window = true, domain.TimeWindow{Tail: 1}
		sctx, cancel := context.WithCancel(ctx)
		st, err := f.Source.Stream(sctx, req)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		drainUntilClosed(t, st.Lines())
	})
}

func collect(t *testing.T, ctx context.Context, src ports.LogSource, req ports.LogRequest) []domain.RawLine {
	t.Helper()
	st, err := src.Stream(ctx, req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var out []domain.RawLine
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-st.Lines():
			if !ok {
				if err := st.Err(); err != nil {
					t.Fatalf("stream error: %v", err)
				}
				return out
			}
			out = append(out, l)
		case <-timeout:
			t.Fatal("history stream did not end")
		}
	}
}

func drainUntilClosed[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}

func contains(xs []string, x string) bool {
	if len(xs) == 0 {
		return true
	}
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
