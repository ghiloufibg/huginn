package demo

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

var t0 = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

var recScope = ports.Scope{Env: domain.Env("rec"), Namespaces: []string{"app-rec"}}

func newTest(t *testing.T) (*Cluster, *portstest.FakeClock) {
	t.Helper()
	clock := portstest.NewFakeClock(t0)
	return New(Options{Seed: 42, Rate: 0.5, Clock: clock}), clock
}

func podOf(t *testing.T, c *Cluster, workload string, pick func(domain.Pod) bool) domain.Pod {
	t.Helper()
	pods, err := c.ListPods(context.Background(), recScope, ports.Selector{"app.kubernetes.io/name": workload})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pods {
		if pick == nil || pick(p) {
			return p
		}
	}
	t.Fatalf("no matching pod for %s", workload)
	return domain.Pod{}
}

func restarted(p domain.Pod) bool { return p.Restarts() > 0 }

func TestClusterContract(t *testing.T) {
	portstest.RunClusterContract(t, func(t *testing.T) portstest.ClusterFixture {
		c, _ := newTest(t)
		return portstest.ClusterFixture{Client: c, Scope: recScope, PodLabels: ports.Selector{"app.kubernetes.io/part-of": "user-api"}}
	})
}

func TestLogSourceContract(t *testing.T) {
	portstest.RunLogSourceContract(t, func(t *testing.T) portstest.LogFixture {
		c, clock := newTest(t)
		withPrev := podOf(t, c, "payment-service", restarted)
		noPrev := podOf(t, c, "user-api", nil)
		return portstest.LogFixture{
			Source: c, Clock: clock,
			Request:       ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: withPrev.Name, Container: "payment-service"},
			NoPrevRequest: ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: noPrev.Name, Container: "user-api"},
		}
	})
}

func TestDeterministic(t *testing.T) {
	a, _ := newTest(t)
	b, _ := newTest(t)
	pa, _ := a.ListPods(context.Background(), recScope, nil)
	pb, _ := b.ListPods(context.Background(), recScope, nil)
	if len(pa) != len(pb) || pa[0].Name != pb[0].Name || pa[len(pa)-1].Name != pb[len(pb)-1].Name {
		t.Fatal("same seed must give the same pods")
	}
	req := ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: pa[0].Name, Container: pa[0].OwnerName, Window: domain.TimeWindow{Tail: 50}}
	la, lb := collect(t, a, req), collect(t, b, req)
	if len(la) != len(lb) || la[len(la)-1].Text != lb[len(lb)-1].Text {
		t.Fatal("same seed must give the same lines")
	}
}

func TestScenarioCoversPrototypeStatuses(t *testing.T) {
	c, _ := newTest(t)
	ws, _ := c.ListWorkloads(context.Background(), recScope)
	repos := map[string]bool{}
	for _, w := range ws {
		if r := w.Labels["app.kubernetes.io/part-of"]; r != "" {
			repos[r] = true
		}
	}
	if len(repos) != 14 {
		t.Fatalf("got %d repos, want 14", len(repos))
	}
	crash := podOf(t, c, "catalog-indexer", nil)
	if app, _ := findContainer(crash, "catalog-indexer"); app.Reason != "CrashLoopBackOff" || app.Restarts == 0 {
		t.Errorf("catalog-indexer: %+v", app)
	}
	pull := podOf(t, c, "document-renderer", nil)
	if pull.Phase != domain.PodPending {
		t.Errorf("document-renderer phase %s", pull.Phase)
	}
	hasSidecar, hasInit := false, false
	for _, ctr := range pull.Containers {
		hasSidecar = hasSidecar || ctr.Name == "istio-proxy"
		hasInit = hasInit || ctr.Init
	}
	if !hasSidecar || !hasInit {
		t.Error("pods must carry istio sidecar and init containers")
	}
}

func TestLinesAreEnrichedLogstashJSON(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "user-api", nil)
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "user-api", Window: domain.TimeWindow{Tail: 30}})
	if len(lines) != 30 {
		t.Fatalf("got %d lines", len(lines))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0].Text), &rec); err != nil {
		t.Fatalf("not JSON: %s", lines[0].Text)
	}
	for _, k := range []string{"@timestamp", "level", "logger_name", "thread_name", "message", "kubernetes"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing %q in %s", k, lines[0].Text)
		}
	}
}

func TestPreviousInstanceEndsWithCrash(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "payment-service", restarted)
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "payment-service", Previous: true, Window: domain.TimeWindow{Tail: 20}})
	if last := lines[len(lines)-1].Text; !strings.Contains(last, "OutOfMemoryError") {
		t.Fatalf("last previous line should be the OOM: %s", last)
	}
}

func TestWaitingContainerHasNoLogs(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "document-renderer", nil)
	_, err := c.Stream(context.Background(), ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "document-renderer", Window: domain.TimeWindow{Tail: 10}})
	if !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetentionLimitsHistory(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "user-api", nil)
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "user-api", Window: domain.TimeWindow{Since: 48 * time.Hour}})
	if first := lines[0].Time; first.Before(t0.Add(-6 * time.Hour)) {
		t.Fatalf("history older than retention: %v", first)
	}
}

func TestFollowStreamsAsClockAdvances(t *testing.T) {
	c, clock := newTest(t)
	p := podOf(t, c, "user-api", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := c.Stream(ctx, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "user-api", Follow: true, Window: domain.TimeWindow{Tail: 1}})
	if err != nil {
		t.Fatal(err)
	}
	<-st.Lines() // history
	deadline := time.After(5 * time.Second)
	for {
		clock.Advance(time.Second)
		select {
		case l := <-st.Lines():
			if !l.Time.After(t0) {
				t.Fatalf("live line at %v not after start", l.Time)
			}
			return
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatal("no live line")
		}
	}
}

func TestLiveRolloutReplacesPod(t *testing.T) {
	c, clock := newTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := c.WatchPods(ctx, recScope, ports.Selector{"app.kubernetes.io/name": "payment-service"})
	if err != nil {
		t.Fatal(err)
	}
	initial := 0
	for len(ch) > 0 {
		<-ch
		initial++
	}
	clock.Advance(liveRollout.retire + time.Second)
	c.advance()
	var types []domain.PodEventType
	timeout := time.After(2 * time.Second)
	for len(types) < 3 {
		select {
		case ev := <-ch:
			types = append(types, ev.Type)
		case <-timeout:
			t.Fatalf("rollout events: %v", types)
		}
	}
	if types[0] != domain.PodAdded || types[1] != domain.PodUpdated || types[2] != domain.PodDeleted {
		t.Fatalf("rollout events: %v", types)
	}
	pods, _ := c.ListPods(ctx, recScope, ports.Selector{"app.kubernetes.io/name": "payment-service"})
	if len(pods) != initial {
		t.Fatalf("pods after rollout: %d, want %d", len(pods), initial)
	}
}

func TestSidecarLinesArePlain(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "user-api", nil)
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "istio-proxy", Window: domain.TimeWindow{Tail: 3}})
	if len(lines) == 0 || strings.HasPrefix(lines[0].Text, "{") {
		t.Fatalf("istio-proxy lines: %v", lines)
	}
}

func collect(t *testing.T, c *Cluster, req ports.LogRequest) []domain.RawLine {
	t.Helper()
	st, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.RawLine
	for l := range st.Lines() {
		out = append(out, l)
	}
	return out
}

func TestHeadStartsAtTheInstanceStart(t *testing.T) {
	c, _ := newTest(t)
	var app domain.Container
	p := podOf(t, c, "payment-service", func(p domain.Pod) bool {
		app = p.Containers[1]
		return app.State == domain.ContainerRunning && app.Started.After(t0.Add(-6*time.Hour))
	})
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: app.Name, Window: domain.TimeWindow{Head: 20}})
	if len(lines) != 20 || !lines[0].Time.Equal(app.Started) {
		t.Fatalf("head: %d lines, first at %v, started %v", len(lines), lines[0].Time, app.Started)
	}
	if !slices.ContainsFunc(lines, func(l domain.RawLine) bool { return strings.Contains(l.Text, "Starting") }) {
		t.Fatal("the head holds the startup")
	}
}

// A pod older than the retention: the head starts where "the node" keeps
// logs, not at the container start (the rotation notice's case).
func TestHeadIsBoundedByRetention(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "user-api", nil)
	if !p.Containers[1].Started.Before(t0.Add(-6 * time.Hour)) {
		t.Fatalf("fixture: user-api started %v", p.Containers[1].Started)
	}
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "user-api", Window: domain.TimeWindow{Head: 3}})
	if len(lines) != 3 || lines[0].Time.Before(t0.Add(-6*time.Hour)) || lines[0].Time.After(t0.Add(-5*time.Hour)) {
		t.Fatalf("head: %d lines, first %v", len(lines), lines[0].Time)
	}
}

func TestHeadOfThePreviousInstance(t *testing.T) {
	c, _ := newTest(t)
	p := podOf(t, c, "payment-service", restarted)
	lines := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "payment-service", Previous: true, Window: domain.TimeWindow{Head: 2}})
	whole := collect(t, c, ports.LogRequest{Scope: recScope, Namespace: "app-rec", Pod: p.Name, Container: "payment-service", Previous: true})
	if len(lines) != 2 || lines[0] != whole[0] || lines[1] != whole[1] {
		t.Fatalf("previous head: %d lines, want the first 2 of the instance", len(lines))
	}
}
