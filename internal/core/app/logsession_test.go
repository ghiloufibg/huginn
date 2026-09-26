package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// passthrough decodes nothing: the message is the raw text.
type passthrough struct{}

func (passthrough) Decode(r domain.RawLine) domain.LogEntry {
	return domain.LogEntry{Time: r.Time, Pod: r.Pod, Container: r.Container, Message: r.Text, Raw: r.Text}
}

type fixture struct {
	cluster *portstest.FakeCluster
	logs    *portstest.FakeLogSource
	clock   *portstest.FakeClock
	s       *LogSessions
}

func podWithSidecar(name string, started time.Time) domain.Pod {
	return domain.Pod{
		Env: domain.Env("rec"), Namespace: "ns", Name: name, OwnerName: "api", Phase: domain.PodRunning, Started: started,
		Labels: map[string]string{"app": "api"},
		Containers: []domain.Container{
			{Name: "api", State: domain.ContainerRunning, Ready: true},
			{Name: "istio-proxy", State: domain.ContainerRunning, Ready: true},
		},
	}
}

func line(pod string, at time.Time, text string) domain.RawLine {
	return domain.RawLine{Time: at, Pod: pod, Container: "api", Text: text}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := portstest.NewFakeClock(t0)
	fc := portstest.NewFakeCluster()
	fc.AddWorkload(deployment("ns", "api", "shop", 2, 2))
	fc.PutPod(podWithSidecar("api-1", t0.Add(-2*time.Hour)))
	fc.PutPod(podWithSidecar("api-2", t0.Add(-2*time.Hour)))
	logs := portstest.NewFakeLogSource(clock)
	logs.SetLines("ns", "api-1", "api", []domain.RawLine{
		line("api-1", t0.Add(-5*time.Minute), "a1"), line("api-1", t0.Add(-3*time.Minute), "a3"),
	}, nil)
	logs.SetLines("ns", "api-2", "api", []domain.RawLine{
		line("api-2", t0.Add(-4*time.Minute), "b2"), line("api-2", t0.Add(-time.Minute), "b4"),
	}, nil)
	logs.SetLines("ns", "api-1", "istio-proxy", []domain.RawLine{{Time: t0.Add(-2 * time.Minute), Pod: "api-1", Container: "istio-proxy", Text: "envoy"}}, nil)
	return &fixture{cluster: fc, logs: logs, clock: clock, s: &LogSessions{
		Cluster: fc, Logs: logs, Clock: clock, Decoders: ports.OneDecoder{LogDecoder: passthrough{}},
		Resolver: LabelResolver{Keys: []string{"app.kubernetes.io/part-of"}},
		Scopes:   scopes("ns"),
		Filter:   domain.ContainerFilter{Deny: []string{"istio-proxy"}},
	}}
}

// reader accumulates batches while advancing the fake clock.
type reader struct {
	t       *testing.T
	ch      <-chan ports.LogBatch
	clock   *portstest.FakeClock
	entries []string
	pods    []ports.PodState
	notices []string
	history bool
}

func (r *reader) until(step time.Duration, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timeout; entries %v notices %v", r.entries, r.notices)
		}
		select {
		case b, open := <-r.ch:
			if !open {
				r.t.Fatal("closed")
			}
			for _, e := range b.Entries {
				r.entries = append(r.entries, e.Message)
			}
			if b.Pods != nil {
				r.pods = b.Pods
			}
			for _, n := range b.Notices {
				r.notices = append(r.notices, n.Text)
			}
			r.history = r.history || b.HistoryDone
		case <-time.After(2 * time.Millisecond):
			r.clock.Advance(step)
		}
	}
}

func (f *fixture) open(t *testing.T, q ports.LogQuery) (*reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	q.Env, q.Repo = domain.Env("rec"), "shop"
	if q.Window == (domain.TimeWindow{}) {
		q.Window = domain.TimeWindow{Since: 15 * time.Minute}
	}
	ch, err := f.s.Open(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return &reader{t: t, ch: ch, clock: f.clock}, cancel
}

func TestHistoryMergedAndAppOnly(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a1,b2,a3,b4" {
		t.Fatalf("history %s", got)
	}
	if len(r.pods) != 2 || !slices.Equal(r.pods[0].Containers, []string{"api"}) {
		t.Fatalf("pods %+v", r.pods)
	}
}

func TestLiveLinesReordered(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	r.until(10*time.Millisecond, func() bool {
		return f.logs.Following("ns", "api-1", "api") == 1 && f.logs.Following("ns", "api-2", "api") == 1
	})
	f.logs.Push("ns", "api-1", line("api-1", t0.Add(200*time.Millisecond), "late-a"))
	f.logs.Push("ns", "api-2", line("api-2", t0.Add(100*time.Millisecond), "early-b"))
	r.until(20*time.Millisecond, func() bool { return len(r.entries) == 6 })
	if got := strings.Join(r.entries[4:], ","); got != "early-b,late-a" {
		t.Fatalf("live order %s", got)
	}
}

func TestNewPodAndTerminatedPod(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	f.logs.SetLines("ns", "api-3", "api", []domain.RawLine{line("api-3", t0.Add(time.Second), "banner")}, nil)
	f.cluster.PutPod(podWithSidecar("api-3", t0))
	f.cluster.DeletePod("ns", "api-1")
	r.until(20*time.Millisecond, func() bool { return slices.Contains(r.entries, "banner") && len(r.pods) == 3 && r.pods[0].Terminated })
	if !r.pods[2].New || r.pods[2].Pod.Name != "api-3" {
		t.Fatalf("new pod %+v", r.pods[2])
	}
	if !slices.Contains(r.entries, "a1") {
		t.Fatal("lines of the terminated pod must stay")
	}
}

func TestReconnectWithoutDuplicates(t *testing.T) {
	f := newFixture(t)
	f.s.Backoff = []time.Duration{time.Second}
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Close("ns", "api-1", "api")
	r.until(50*time.Millisecond, func() bool { return slices.Contains(r.notices, "reconnecting to api-1/api") })
	r.until(100*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Push("ns", "api-1", line("api-1", f.clock.Now(), "after"))
	r.until(50*time.Millisecond, func() bool { return slices.Contains(r.entries, "after") })
	counts := map[string]int{}
	for _, e := range r.entries {
		counts[e]++
	}
	for e, n := range counts {
		if n > 1 {
			t.Errorf("%s delivered %d times", e, n)
		}
	}
}

func TestRetentionNotice(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Since: time.Hour}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if len(r.notices) != 2 || !strings.Contains(r.notices[0], "available from") {
		t.Fatalf("notices %v", r.notices)
	}
	f2 := newFixture(t)
	r2, cancel2 := f2.open(t, ports.LogQuery{Window: domain.TimeWindow{Since: 9 * time.Minute}})
	defer cancel2()
	r2.until(10*time.Millisecond, func() bool { return r2.history })
	if len(r2.notices) != 0 {
		t.Fatalf("a short quiet gap must not warn: %v", r2.notices)
	}
}

func TestTailWindowAndUnknownRepo(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Tail: 1}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a3,b4" {
		t.Fatalf("tail 1 per container: %s", got)
	}
	_, err := f.s.Open(context.Background(), ports.LogQuery{Env: domain.Env("rec"), Repo: "nope", Window: domain.TimeWindow{Tail: 1}})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestSessionClosesOnCancel(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := f.s.Open(ctx, ports.LogQuery{Env: domain.Env("rec"), Repo: "shop", Window: domain.TimeWindow{Tail: 5}, Follow: true})
	if err != nil {
		t.Fatal(err)
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
			t.Fatal("not closed")
		}
	}
}

func TestHistoryIsCappedToTheNewestLinesOfAllContainers(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 1
	r, cancel := f.open(t, ports.LogQuery{})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "b4" {
		t.Fatalf("the buffer holds 1 line: the newest of all containers, got %s", got)
	}
	if len(r.notices) != 3 || !strings.Contains(r.notices[0], "not loaded (limit 1 lines per container)") ||
		!strings.Contains(r.notices[2], "1 lines kept (buffer size), 1 older skipped") {
		t.Fatalf("a capped history must say so, not report retention: %v", r.notices)
	}
}
