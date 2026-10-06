package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
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
	muted   uint64
	mutedBy map[string]uint64
	skipped uint64
	largest int // most entries in one batch
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
			r.muted += b.Muted
			r.skipped += b.Skipped
			r.largest = max(r.largest, len(b.Entries))
			if b.MutedBy != nil {
				r.mutedBy = b.MutedBy
			}
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

// TestResumeWithSecondPrecisionSource reproduces the Kubernetes API, which
// honors sinceTime to the second: after a reconnect, the lines of the same
// second before the last delivered one must not come back.
func TestResumeWithSecondPrecisionSource(t *testing.T) {
	f := newFixture(t)
	f.logs.SecondPrecision = true
	sec := t0.Add(-time.Minute).Truncate(time.Second)
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{
		line("api-1", sec.Add(100*time.Millisecond), "early"), line("api-1", sec.Add(500*time.Millisecond), "last"),
	}, nil)
	f.s.Backoff = []time.Duration{time.Second}
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Close("ns", "api-1", "api")
	r.until(100*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Push("ns", "api-1", line("api-1", f.clock.Now(), "after"))
	r.until(50*time.Millisecond, func() bool { return slices.Contains(r.entries, "after") })
	counts := map[string]int{}
	for _, e := range r.entries {
		counts[e]++
	}
	if counts["early"] != 1 || counts["last"] != 1 {
		t.Fatalf("resumed lines delivered again: %v", counts)
	}
}

// noisyDecoders decode lines starting with "pool" as entries of the
// logger "pool", which they mute, and the others as entries of "app".
type noisyDecoders struct{ mute *domain.LoggerMute }

func (d noisyDecoders) For(string, string) ports.LogFormat {
	return ports.LogFormat{Decoder: noisyDecoder{}, Mute: d.mute}
}

type noisyDecoder struct{}

func (noisyDecoder) Decode(r domain.RawLine) domain.LogEntry {
	e := passthrough{}.Decode(r)
	e.Logger, e.Structured = "app", true
	if strings.HasPrefix(r.Text, "pool") {
		e.Logger = "pool"
	}
	return e
}

func newNoisyFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	mute, err := domain.NewLoggerMute([]string{"pool"}, []domain.Level{domain.LevelError})
	if err != nil {
		t.Fatal(err)
	}
	f.s.Decoders = noisyDecoders{mute: mute}
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{
		line("api-1", t0.Add(-5*time.Minute), "a1"), line("api-1", t0.Add(-4*time.Minute), "pool-h"), line("api-1", t0.Add(-3*time.Minute), "a3"),
	}, nil)
	return f
}

func TestMutedLoggersLeftOutAndCounted(t *testing.T) {
	f := newNoisyFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a1,b2,a3,b4" {
		t.Fatalf("history %s", got)
	}
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Push("ns", "api-1", line("api-1", t0.Add(100*time.Millisecond), "pool-live"))
	f.logs.Push("ns", "api-1", line("api-1", t0.Add(200*time.Millisecond), "after"))
	r.until(20*time.Millisecond, func() bool { return slices.Contains(r.entries, "after") && r.muted == 2 })
	if slices.ContainsFunc(r.entries, func(e string) bool { return strings.HasPrefix(e, "pool") }) {
		t.Fatalf("muted lines delivered: %v", r.entries)
	}
	if len(r.mutedBy) != 1 || r.mutedBy["pool"] != 2 {
		t.Fatalf("muted per pattern %v, want pool:2", r.mutedBy)
	}
}

// The patterns that apply are reported before they mute anything, so the
// view can list them with 0.
func TestMutePatternsReportedBeforeMuting(t *testing.T) {
	f := newFixture(t)
	mute, err := domain.NewLoggerMute([]string{"pool", "idle.*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.s.Decoders = noisyDecoders{mute: mute}
	r, cancel := f.open(t, ports.LogQuery{})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && r.mutedBy != nil })
	if len(r.mutedBy) != 2 || r.mutedBy["pool"] != 0 || r.mutedBy["idle.*"] != 0 || r.muted != 0 {
		t.Fatalf("muted %d, per pattern %v; want both patterns at 0", r.muted, r.mutedBy)
	}
}

func TestNoMuteShowsMutedLoggers(t *testing.T) {
	f := newNoisyFixture(t)
	r, cancel := f.open(t, ports.LogQuery{NoMute: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a1,pool-h,b2,a3,b4" || r.muted != 0 {
		t.Fatalf("history %s, %d muted", got, r.muted)
	}
}

// TestMutedLinesMoveTheResumePoint: a muted line is not delivered but is
// read; after a reconnect, it must not be read (and counted) again.
func TestMutedLinesMoveTheResumePoint(t *testing.T) {
	f := newNoisyFixture(t)
	f.logs.SecondPrecision = true
	f.s.Backoff = []time.Duration{time.Second}
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Push("ns", "api-1", line("api-1", t0.Add(100*time.Millisecond), "pool-live"))
	r.until(10*time.Millisecond, func() bool { return r.muted == 2 })
	f.logs.Close("ns", "api-1", "api")
	r.until(100*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Push("ns", "api-1", line("api-1", f.clock.Now(), "after"))
	r.until(50*time.Millisecond, func() bool { return slices.Contains(r.entries, "after") })
	if r.muted != 2 {
		t.Fatalf("%d muted lines, want 2 (the muted line was read again)", r.muted)
	}
}

// TestSlowViewKeepsSessionBounded: a view that does not read keeps at
// most its buffer's worth of entries waiting in the session; the older ones
// are counted as skipped, and the newest are delivered.
func TestSlowViewKeepsSessionBounded(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 10
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	before := len(r.entries)
	for i := range 200 {
		f.logs.Push("ns", "api-1", line("api-1", t0.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("live-%03d", i)))
	}
	for range 20 { // the session commits and waits for the view
		f.clock.Advance(100 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
	}
	r.until(10*time.Millisecond, func() bool { return uint64(len(r.entries)-before)+r.skipped == 200 })
	if r.largest > 10 {
		t.Fatalf("a batch of %d entries, more than the buffer (10)", r.largest)
	}
	if got := r.entries[len(r.entries)-1]; got != "live-199" {
		t.Fatalf("newest entry %s, want live-199", got)
	}
	if r.skipped == 0 {
		t.Fatal("nothing skipped: the view kept up, the test proves nothing")
	}
}

// TestCrashLoopWaitsForRestart reproduces B3: the follow stream of a
// crash-looping container ends without error; the tailer must wait for the
// pod watch to show a new instance, not reconnect in a loop.
func TestCrashLoopWaitsForRestart(t *testing.T) {
	f := newFixture(t)
	f.s.Backoff = []time.Duration{time.Second}
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && f.logs.Following("ns", "api-1", "api") == 1 })

	crashed := podWithSidecar("api-1", t0.Add(-2*time.Hour))
	crashed.Containers[0] = domain.Container{Name: "api", State: domain.ContainerWaiting, Reason: "CrashLoopBackOff", Restarts: 1}
	f.cluster.PutPod(crashed)
	podErr := func() error {
		for _, p := range r.pods {
			if p.Pod.Name == "api-1" {
				return p.Err
			}
		}
		return nil
	}
	r.until(10*time.Millisecond, func() bool {
		return r.pods != nil && podErr() == nil && r.pods[0].Pod.Containers[0].Reason == "CrashLoopBackOff"
	})
	f.logs.Close("ns", "api-1", "api")
	r.until(10*time.Millisecond, func() bool { return errors.Is(podErr(), domain.ErrNotStarted) })
	for range 30 { // well past the backoff: no reconnection
		f.clock.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
	if n := f.logs.Following("ns", "api-1", "api"); n != 0 {
		t.Fatalf("%d streams while the container waits", n)
	}
	if slices.Contains(r.notices, "reconnecting to api-1/api") {
		t.Error("a waiting container is not a connection problem")
	}

	restarted := podWithSidecar("api-1", t0.Add(-2*time.Hour))
	restarted.Containers[0].Restarts = 2
	f.cluster.PutPod(restarted)
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 && podErr() == nil })
}

// TestPodsOfOtherOwnersIgnored: selectors can overlap; a pod that names
// another owner is not the repository's, even when its labels match.
func TestPodsOfOtherOwnersIgnored(t *testing.T) {
	f := newFixture(t)
	other := podWithSidecar("batch-1", t0.Add(-time.Hour))
	other.OwnerName = "nightly-batch"
	f.cluster.PutPod(other)
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && len(r.pods) > 0 })
	late := podWithSidecar("batch-2", t0)
	late.OwnerName = "nightly-batch"
	f.cluster.PutPod(late)
	f.cluster.PutPod(podWithSidecar("api-3", t0))
	r.until(10*time.Millisecond, func() bool { return len(r.pods) == 3 })
	for _, p := range r.pods {
		if strings.HasPrefix(p.Pod.Name, "batch") {
			t.Errorf("pod %s of another owner streamed", p.Pod.Name)
		}
	}
}

func TestPreviousInstance(t *testing.T) {
	f := newFixture(t)
	restarted := podWithSidecar("api-1", t0.Add(-time.Hour))
	restarted.Containers[0].Restarts = 3
	f.cluster.PutPod(restarted)
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{line("api-1", t0, "current")},
		[]domain.RawLine{line("api-1", t0.Add(-3*time.Hour), "old boot"), line("api-1", t0.Add(-2*time.Hour), "crash")})
	r, cancel := f.open(t, ports.LogQuery{Previous: true, Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && len(r.pods) == 2 })
	// The whole previous instance, whatever the window; nothing of the
	// current one, and no stream left open.
	if !slices.Equal(r.entries, []string{"old boot", "crash"}) {
		t.Fatalf("entries %v", r.entries)
	}
	if n := f.logs.Following("ns", "api-1", "api"); n != 0 {
		t.Errorf("%d streams followed", n)
	}
	for _, p := range r.pods {
		if p.Pod.Name == "api-2" && !errors.Is(p.Err, domain.ErrNoPrevious) {
			t.Errorf("api-2 never restarted: err %v", p.Err)
		}
	}
}

func TestSkipResumed(t *testing.T) {
	since := t0.Add(500 * time.Millisecond)
	seen := map[string]bool{"x": true}
	for _, c := range []struct {
		at   time.Duration
		text string
		skip bool
	}{
		{100 * time.Millisecond, "a", true},  // same second, before: delivered already
		{500 * time.Millisecond, "x", true},  // at since, seen
		{500 * time.Millisecond, "y", false}, // at since, new text
		{900 * time.Millisecond, "z", false}, // after
	} {
		if got := skipResumed(since, domain.RawLine{Time: t0.Add(c.at), Text: c.text}, seen); got != c.skip {
			t.Errorf("%v %s: skip %v", c.at, c.text, got)
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

// A terminating pod's stream ends for good: not running, so no
// "reconnecting" while the rollout deletes it.
func TestTerminatingPodIsNotRunning(t *testing.T) {
	p := podWithSidecar("api-1", t0)
	tl := &tailer{container: "api", pod: p, wake: make(chan struct{}, 1)}
	tl.observe(p)
	if !tl.running.Load() {
		t.Fatal("running container")
	}
	p.Deleted = true
	tl.observe(p)
	if tl.running.Load() {
		t.Fatal("terminating pod counted as running")
	}
}

// E1: one namespace the user cannot read must not block the logs of a
// repository that lives in another.
func TestForbiddenNamespaceDoesNotBlockLogs(t *testing.T) {
	f := newFixture(t)
	f.cluster.NamespaceErr = map[string]error{"ns2": domain.ErrForbidden}
	f.s.Scopes = scopes("ns", "ns2")
	r, cancel := f.open(t, ports.LogQuery{})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if !slices.Contains(r.entries, "a1") {
		t.Fatalf("entries %v", r.entries)
	}
	f.s.Scopes = scopes("ns2")
	ctx := context.Background()
	if _, err := f.s.Open(ctx, ports.LogQuery{Env: "rec", Repo: "shop"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("repository only in a forbidden namespace: %v", err)
	}
}

// E9: a stream error is logged and shown once as a notice with its text.
func TestStreamErrorIsReported(t *testing.T) {
	f := newFixture(t)
	f.s.Backoff = []time.Duration{time.Second}
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.SetErr(errors.New("http2: client connection lost"))
	f.logs.Close("ns", "api-1", "api")
	r.until(20*time.Millisecond, func() bool {
		return slices.ContainsFunc(r.notices, func(n string) bool { return strings.Contains(n, "http2: client connection lost") })
	})
	n := 0
	for range 20 {
		f.clock.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
	r.until(10*time.Millisecond, func() bool { return true })
	for _, x := range r.notices {
		if strings.Contains(x, "http2") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the same error was noticed %d times", n)
	}
}

// E4: the stream of a finished Job pod ends for good: no "waiting", no
// reconnection.
func TestCompletedPodStreamEnds(t *testing.T) {
	f := newFixture(t)
	f.s.Backoff = []time.Duration{time.Second}
	r, cancel := f.open(t, ports.LogQuery{Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && f.logs.Following("ns", "api-1", "api") == 1 })
	done := podWithSidecar("api-1", t0.Add(-2*time.Hour))
	done.Phase = domain.PodSucceeded
	done.Containers[0] = domain.Container{Name: "api", State: domain.ContainerTerminated, Reason: "Completed"}
	f.cluster.PutPod(done)
	r.until(10*time.Millisecond, func() bool {
		return slices.ContainsFunc(r.pods, func(p ports.PodState) bool { return p.Pod.Phase == domain.PodSucceeded })
	})
	f.logs.Close("ns", "api-1", "api")
	for range 90 {
		f.clock.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
	r.until(10*time.Millisecond, func() bool { return true })
	for _, p := range r.pods {
		if p.Pod.Name == "api-1" && p.Err != nil {
			t.Fatalf("finished pod has error %v", p.Err)
		}
	}
	if n := f.logs.Following("ns", "api-1", "api"); n != 0 {
		t.Fatalf("%d streams on a finished container", n)
	}
}

func TestHeadWindow(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Head: 1}, Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a1,b2" {
		t.Fatalf("head 1 per container: %s", got)
	}
	if n := f.logs.Following("ns", "api-1", "api") + f.logs.Following("ns", "api-2", "api"); n != 0 {
		t.Fatalf("a head never follows: %d streams followed", n)
	}
	if len(r.notices) != 0 {
		t.Fatalf("notices %v", r.notices)
	}
}

func TestHeadIsCappedToTheOldestLinesOfAllContainers(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 1
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Head: 2}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a1" {
		t.Fatalf("the buffer holds 1 line: the oldest of all containers, got %s", got)
	}
	if len(r.notices) != 1 || !strings.Contains(r.notices[0], "newer lines of the heads not loaded: 1 lines kept (buffer size), 1 newer skipped") {
		t.Fatalf("notices %v", r.notices)
	}
}

// headIgnored is a source that knows nothing of heads: it sends everything.
type headIgnored struct{ ports.LogSource }

func (s headIgnored) Stream(ctx context.Context, req ports.LogRequest) (ports.LogStream, error) {
	req.Window = domain.TimeWindow{}
	return s.LogSource.Stream(ctx, req)
}

func TestHeadStopsASourceThatSendsMore(t *testing.T) {
	f := newFixture(t)
	f.s.Logs = headIgnored{f.logs}
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Head: 1}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "a1,b2" {
		t.Fatalf("head 1 per container: %s", got)
	}
}

func TestHeadOfThePreviousInstance(t *testing.T) {
	f := newFixture(t)
	restarted := podWithSidecar("api-1", t0.Add(-time.Hour))
	restarted.Containers[0].Restarts = 3
	f.cluster.PutPod(restarted)
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{line("api-1", t0, "current")},
		[]domain.RawLine{line("api-1", t0.Add(-3*time.Hour), "old boot"), line("api-1", t0.Add(-2*time.Hour), "crash")})
	r, cancel := f.open(t, ports.LogQuery{Previous: true, Window: domain.TimeWindow{Head: 1}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && len(r.pods) == 2 })
	if !slices.Equal(r.entries, []string{"old boot"}) {
		t.Fatalf("the first line of the previous instance: %v", r.entries)
	}
}

func TestHeadRotationNotice(t *testing.T) {
	f := newFixture(t)
	rotated := podWithSidecar("api-1", t0.Add(-2*time.Hour))
	rotated.Containers[0].Started = t0.Add(-10 * time.Minute) // first line at -5m
	f.cluster.PutPod(rotated)
	fresh := podWithSidecar("api-2", t0.Add(-2*time.Hour))
	fresh.Containers[0].Started = t0.Add(-4*time.Minute - 30*time.Second) // first line at -4m
	f.cluster.PutPod(fresh)
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Head: 5}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if len(r.notices) != 1 || !strings.HasPrefix(r.notices[0], "api-1: first line at ") ||
		!strings.Contains(r.notices[0], "(older lines rotated away on the node)") {
		t.Fatalf("notices %v", r.notices)
	}
}

func TestHeadOfANewPod(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Window: domain.TimeWindow{Head: 1}})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	f.logs.SetLines("ns", "api-3", "api", []domain.RawLine{
		line("api-3", t0.Add(time.Second), "banner"), line("api-3", t0.Add(2*time.Second), "ready"),
	}, nil)
	f.cluster.PutPod(podWithSidecar("api-3", t0))
	r.until(20*time.Millisecond, func() bool { return slices.Contains(r.entries, "banner") })
	r.until(20*time.Millisecond, func() bool { return len(r.pods) == 3 })
	if slices.Contains(r.entries, "ready") {
		t.Fatalf("a new pod loads its head only: %v", r.entries)
	}
}

// BenchmarkSessionLive pushes live lines from 20 pods through a session,
// as fast as it takes them, and reads every batch: the cost per line of
// the whole live path (tailers, reorder window, batches), decoding aside.
func BenchmarkSessionLive(b *testing.B) {
	const pods = 20
	clock := portstest.NewFakeClock(t0)
	fc := portstest.NewFakeCluster()
	fc.AddWorkload(deployment("ns", "api", "shop", pods, pods))
	logs := portstest.NewFakeLogSource(clock)
	names := make([]string, pods)
	for i := range names {
		names[i] = fmt.Sprintf("api-%02d", i)
		fc.PutPod(podWithSidecar(names[i], t0.Add(-2*time.Hour)))
		logs.SetLines("ns", names[i], "api", nil, nil)
	}
	s := &LogSessions{
		Cluster: fc, Logs: logs, Clock: clock, Decoders: ports.OneDecoder{LogDecoder: passthrough{}},
		Resolver: LabelResolver{Keys: []string{"app.kubernetes.io/part-of"}}, Scopes: scopes("ns"),
		Filter: domain.ContainerFilter{Deny: []string{"istio-proxy"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := s.Open(ctx, ports.LogQuery{Env: "rec", Repo: "shop", Window: domain.TimeWindow{Since: time.Minute}, Follow: true})
	if err != nil {
		b.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() { // time passes: the reorder window commits
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				clock.Advance(50 * time.Millisecond)
			}
		}
	}()
	var got atomic.Int64
	go func() {
		for batch := range ch {
			got.Add(int64(len(batch.Entries)+len(batch.Late)) + int64(batch.Skipped))
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for logs.Following("ns", names[pods-1], "api") == 0 {
		if time.Now().After(deadline) {
			b.Fatal("streams not followed")
		}
		time.Sleep(time.Millisecond)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		p := names[i%pods]
		logs.Push("ns", p, domain.RawLine{Time: t0.Add(time.Duration(i) * time.Microsecond), Pod: p, Container: "api", Text: "line"})
	}
	for got.Load() < int64(b.N) {
		if time.Now().After(deadline.Add(time.Minute)) {
			b.Fatalf("%d of %d lines delivered", got.Load(), b.N)
		}
		time.Sleep(100 * time.Microsecond)
	}
}
