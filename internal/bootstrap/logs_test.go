package bootstrap

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
	"github.com/ghiloufibg/huginn/internal/diag"
)

type logReader struct {
	t       *testing.T
	ch      <-chan ports.LogBatch
	clock   *portstest.FakeClock
	entries []domain.LogEntry
	pods    []ports.PodState
	notices []string
	history bool
}

func (r *logReader) until(step time.Duration, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timeout after %d entries, notices %v", len(r.entries), r.notices)
		}
		select {
		case b := <-r.ch:
			r.entries = append(r.entries, b.Entries...)
			if b.Pods != nil {
				r.pods = b.Pods
			}
			for _, n := range b.Notices {
				r.notices = append(r.notices, n.Text)
			}
			r.history = r.history || b.HistoryDone
		case <-time.After(time.Millisecond):
			r.clock.Advance(step)
		}
	}
}

func openDemoLogs(t *testing.T, q ports.LogQuery) *logReader {
	t.Helper()
	c := demoConfig(t)
	clock := portstest.NewFakeClock(t0)
	cluster := demo.New(demo.Options{Seed: c.Huginn.Demo.Seed, Rate: c.Huginn.Demo.Rate, Clock: clock})
	lp, probs := logParts(c)
	if probs != nil {
		t.Fatal(probs)
	}
	s := newLogSessions(c, cluster, clock, containerFilter(c), lp.decoders, diag.Discard())
	q.Env, q.Repo = domain.Env("rec"), "payment-service"
	ch, err := s.Open(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return &logReader{t: t, ch: ch, clock: clock}
}

func TestDemoLogsAreOrderedAppOnlyAndDecoded(t *testing.T) {
	r := openDemoLogs(t, ports.LogQuery{Window: domain.TimeWindow{Since: 15 * time.Minute}})
	r.until(10*time.Millisecond, func() bool { return r.history })
	if len(r.entries) < 100 {
		t.Fatalf("only %d entries", len(r.entries))
	}
	pods := map[string]bool{}
	for i, e := range r.entries {
		if e.Container != "payment-service" && e.Container != "payment-worker" {
			t.Fatalf("sidecar line from %s", e.Container)
		}
		if i > 0 && e.Time.Before(r.entries[i-1].Time) {
			t.Fatalf("entry %d out of order", i)
		}
		if !e.Structured || e.Logger == "" || e.HiddenFields()["kubernetes.namespace_name"] != "app-rec" {
			t.Fatalf("not decoded with the logstash profile: %+v", e)
		}
		pods[e.Pod] = true
	}
	if len(pods) != 4 {
		t.Errorf("lines from %d pods, want 4 (2 workloads x 2 replicas)", len(pods))
	}
}

func TestDemoLiveRolloutStreamsNewPod(t *testing.T) {
	r := openDemoLogs(t, ports.LogQuery{Window: domain.TimeWindow{Tail: 20}, Follow: true})
	r.until(10*time.Millisecond, func() bool { return r.history })
	r.until(500*time.Millisecond, func() bool {
		for _, e := range r.entries {
			if strings.Contains(e.Message, "Spring Boot") || strings.Contains(e.Message, "Started PaymentServiceApplication") {
				return true
			}
		}
		return false
	})
	hasNew := false
	for _, p := range r.pods {
		hasNew = hasNew || p.New
	}
	if !hasNew {
		t.Error("the rollout pod must be flagged new")
	}
}

func TestDemoRetentionNotice(t *testing.T) {
	r := openDemoLogs(t, ports.LogQuery{Window: domain.TimeWindow{Since: 48 * time.Hour}})
	r.until(10*time.Millisecond, func() bool { return r.history })
	found := false
	for _, n := range r.notices {
		found = found || strings.Contains(n, "available from")
	}
	if !found {
		t.Fatalf("2d in demo must report retention: %v", r.notices)
	}
}

// TestClosedSessionsLeaveNoGoroutines opens and closes demo log sessions:
// a long-running TUI opens many.
func TestClosedSessionsLeaveNoGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()
	for range 5 {
		ctx, cancel := context.WithCancel(t.Context())
		c := demoConfig(t)
		clock := portstest.NewFakeClock(t0)
		cluster := demo.New(demo.Options{Seed: 42, Rate: 1, Clock: clock})
		lp, _ := logParts(c)
		s := newLogSessions(c, cluster, clock, containerFilter(c), lp.decoders, diag.Discard())
		ch, err := s.Open(ctx, ports.LogQuery{Env: "rec", Repo: "payment-service", Window: domain.TimeWindow{Tail: 10}, Follow: true})
		if err != nil {
			t.Fatal(err)
		}
		<-ch
		cancel()
		for range ch { //nolint:revive // drain until the session closes its channel
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base+2 {
		buf := make([]byte, 1<<16)
		t.Fatalf("%d goroutines left (was %d):\n%s", n, base, buf[:runtime.Stack(buf, true)])
	}
}
