package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// LogSessions implements ports.LogSession: it streams the logs of a
// repository's application containers, merged in time order.
//
// Each container has a tailer that first reads its history (the window,
// without following), then, when following, re-opens the stream from the
// last line's time and delivers live lines, reconnecting with backoff when
// the stream ends while the pod still exists. Live lines wait in a reorder
// window so lines of different pods interleave correctly; committed lines
// are never reordered.
type LogSessions struct {
	Cluster  ports.ClusterClient
	Logs     ports.LogSource
	Resolver ports.RepoResolver
	Scopes   func(domain.Env) (ports.Scope, bool)
	Filter   domain.ContainerFilter
	Decoder  ports.LogDecoder
	Clock    ports.Clock
	// Flush is the delay between two batches (default 33ms, about 30/s).
	Flush time.Duration
	// Reorder is how long live lines wait before being committed
	// (default 250ms).
	Reorder time.Duration
	// Backoff lists the waits between reconnections (default 1s, 2s, 5s,
	// 10s, 30s; the last repeats).
	Backoff []time.Duration
	Log     *slog.Logger
}

// Open implements ports.LogSession.
func (s *LogSessions) Open(ctx context.Context, q ports.LogQuery) (<-chan ports.LogBatch, error) {
	scope, ok := s.Scopes(q.Env)
	if !ok {
		return nil, fmt.Errorf("environment %q is not configured", q.Env)
	}
	workloads, err := s.workloadsOf(ctx, scope, q)
	if err != nil {
		return nil, err
	}
	pods, err := s.podsOf(ctx, scope, workloads)
	if err != nil {
		return nil, err
	}
	run := &session{
		s: s, q: q, scope: scope, workloads: workloads, opened: s.Clock.Now(),
		pods: map[string]*podState{}, msgs: make(chan tailMsg, 1024), out: make(chan ports.LogBatch, 16),
	}
	go run.loop(ctx, pods)
	return run.out, nil
}

func (s *LogSessions) workloadsOf(ctx context.Context, scope ports.Scope, q ports.LogQuery) ([]domain.Workload, error) {
	all, err := s.Cluster.ListWorkloads(ctx, scope)
	if err != nil {
		return nil, err
	}
	repos, rest, err := s.Resolver.Resolve(ctx, q.Env, all)
	if err != nil {
		return nil, err
	}
	var refs []domain.WorkloadRef
	for _, r := range repos {
		if r.Name == q.Repo {
			refs = r.Workloads
		}
	}
	for _, w := range rest { // unassigned workloads are listed under their own name
		if w.Ref.Name == q.Repo {
			refs = append(refs, w.Ref)
		}
	}
	var out []domain.Workload
	for _, w := range all {
		if slices.Contains(refs, w.Ref) {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("repository %q in %s: %w", q.Repo, q.Env, domain.ErrNotFound)
	}
	return out, nil
}

func (s *LogSessions) podsOf(ctx context.Context, scope ports.Scope, ws []domain.Workload) ([]domain.Pod, error) {
	var out []domain.Pod
	for _, w := range ws {
		sc := scope
		sc.Namespaces = []string{w.Ref.Namespace}
		pods, err := s.Cluster.ListPods(ctx, sc, ports.Selector(w.Selector))
		if err != nil {
			return nil, err
		}
		out = append(out, pods...)
	}
	slices.SortFunc(out, func(a, b domain.Pod) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (s *LogSessions) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.DiscardHandler)
}

// tailMsg is what a tailer or pod watch reports to the session loop.
type tailMsg struct {
	pod, container string
	history        []domain.LogEntry // with historyDone
	historyDone    bool
	historyErr     error
	live           *domain.LogEntry
	notice         string
	podEvent       *domain.PodEvent
	streamErr      error // current error of the container, nil when streaming
	clearErr       bool
}

type podState struct {
	ports.PodState
	cancel context.CancelFunc
}

type session struct {
	s         *LogSessions
	q         ports.LogQuery
	scope     ports.Scope
	workloads []domain.Workload
	opened    time.Time
	pods      map[string]*podState
	msgs      chan tailMsg
	out       chan ports.LogBatch

	awaiting    int // initial containers whose history is pending
	history     []domain.LogEntry
	historySent bool
	reorder     []domain.LogEntry
	committed   time.Time
	pending     ports.LogBatch
	podsChanged bool
}

func (r *session) loop(ctx context.Context, initial []domain.Pod) {
	defer close(r.out)
	for _, p := range initial {
		r.addPod(ctx, p, false)
	}
	r.watchPods(ctx)
	if r.awaiting == 0 {
		r.finishHistory()
	}
	flush := r.s.Flush
	if flush <= 0 {
		flush = 33 * time.Millisecond
	}
	tick := r.s.Clock.NewTicker(flush)
	defer tick.Stop()
	for {
		var out chan ports.LogBatch
		if r.hasPending() {
			out = r.out
		}
		select {
		case <-ctx.Done():
			for _, p := range r.pods {
				p.cancel()
			}
			return
		case m := <-r.msgs:
			r.handle(ctx, m)
		case <-tick.C():
			r.commit(false)
		case out <- r.batch():
			r.pending, r.podsChanged = ports.LogBatch{}, false
		}
	}
}

func (r *session) hasPending() bool {
	return len(r.pending.Entries) > 0 || len(r.pending.Notices) > 0 || r.pending.HistoryDone || r.podsChanged
}

// batch returns the pending batch, with the pod list if it changed.
func (r *session) batch() ports.LogBatch {
	b := r.pending
	if r.podsChanged {
		b.Pods = r.podList()
	}
	return b
}

func (r *session) podList() []ports.PodState {
	out := make([]ports.PodState, 0, len(r.pods))
	for _, p := range r.pods {
		out = append(out, p.PodState)
	}
	slices.SortFunc(out, func(a, b ports.PodState) int { return strings.Compare(a.Pod.Name, b.Pod.Name) })
	return out
}

// addPod registers a pod and starts a tailer per application container.
func (r *session) addPod(ctx context.Context, p domain.Pod, isNew bool) {
	pctx, cancel := context.WithCancel(ctx)
	st := &podState{PodState: ports.PodState{Pod: p, New: isNew}, cancel: cancel}
	for _, c := range r.s.Filter.AppContainers(p) {
		st.Containers = append(st.Containers, c.Name)
		if !isNew {
			r.awaiting++
		}
		t := &tailer{s: r.s, q: r.q, scope: r.scope, pod: p, container: c.Name, msgs: r.msgs}
		go t.run(pctx)
	}
	r.pods[p.Name] = st
	r.podsChanged = true
}

// watchPods follows pods of the repository's workloads to pick up new ones
// (rollouts, restarts) and mark deleted ones.
func (r *session) watchPods(ctx context.Context) {
	for _, w := range r.workloads {
		sc := r.scope
		sc.Namespaces = []string{w.Ref.Namespace}
		ch, err := r.s.Cluster.WatchPods(ctx, sc, ports.Selector(w.Selector))
		if err != nil {
			r.s.logger().Warn("pod watch failed", "workload", w.Ref.Name, "err", err)
			r.pending.Notices = append(r.pending.Notices, ports.LogNotice{Text: "pod changes not followed: " + err.Error()})
			continue
		}
		go func() {
			for ev := range ch {
				select {
				case r.msgs <- tailMsg{podEvent: &ev}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

func (r *session) handle(ctx context.Context, m tailMsg) {
	switch {
	case m.podEvent != nil:
		r.handlePod(ctx, *m.podEvent)
	case m.historyDone:
		r.handleHistory(m)
	case m.live != nil:
		r.reorder = append(r.reorder, *m.live)
	case m.notice != "":
		r.pending.Notices = append(r.pending.Notices, ports.LogNotice{Pod: m.pod, Text: m.notice})
	case m.streamErr != nil || m.clearErr:
		if p := r.pods[m.pod]; p != nil && !errors.Is(p.Err, m.streamErr) {
			p.Err = m.streamErr
			r.podsChanged = true
		}
	}
}

func (r *session) handlePod(ctx context.Context, ev domain.PodEvent) {
	p, known := r.pods[ev.Pod.Name]
	switch {
	case ev.Type == domain.PodDeleted && known:
		p.Terminated, p.Pod = true, ev.Pod
		p.cancel()
		r.podsChanged = true
	case !known && ev.Type != domain.PodDeleted:
		r.addPod(ctx, ev.Pod, true)
	case known && !p.Terminated:
		p.Pod = ev.Pod
		r.podsChanged = true
	}
}

func (r *session) handleHistory(m tailMsg) {
	if m.historyErr != nil {
		if p := r.pods[m.pod]; p != nil {
			p.Err = m.historyErr
			r.podsChanged = true
		}
	}
	if r.historySent {
		// History of a pod that appeared later: it is recent, treat it as
		// live so the reorder window places it.
		r.reorder = append(r.reorder, m.history...)
		return
	}
	r.history = append(r.history, m.history...)
	if n := r.retentionNotice(m); n != "" {
		r.pending.Notices = append(r.pending.Notices, ports.LogNotice{Pod: m.pod, Text: n})
	}
	r.awaiting--
	if r.awaiting <= 0 {
		r.finishHistory()
	}
}

// retentionNotice reports when a container that was running at the start
// of the window returned nothing for a significant part of it (more than
// 5 minutes and 10% of the window): usually the node rotated or dropped
// those logs. It only states what was received.
func (r *session) retentionNotice(m tailMsg) string {
	if r.q.Window.IsTail() || r.q.Window.Since <= 0 || len(m.history) == 0 {
		return ""
	}
	start := r.opened.Add(-r.q.Window.Since)
	p := r.pods[m.pod]
	if p == nil || p.Pod.Started.IsZero() || !p.Pod.Started.Before(start) {
		return ""
	}
	first := m.history[0].Time
	if gap := first.Sub(start); gap <= 5*time.Minute || gap <= r.q.Window.Since/10 {
		return ""
	}
	return fmt.Sprintf("logs of %s available from %s only", m.pod, first.In(time.Local).Format("Jan 2 15:04"))
}

func (r *session) finishHistory() {
	slices.SortStableFunc(r.history, compareEntries)
	r.pending.Entries = append(r.pending.Entries, r.history...)
	if n := len(r.history); n > 0 {
		r.committed = r.history[n-1].Time
	}
	r.history, r.historySent = nil, true
	r.pending.HistoryDone = true
}

// commit moves live lines older than the reorder window to the pending
// batch, in time order. Lines older than what is already committed are
// committed at once (they cannot be placed before committed lines).
func (r *session) commit(all bool) {
	if !r.historySent || len(r.reorder) == 0 {
		return
	}
	window := r.s.Reorder
	if window <= 0 {
		window = 250 * time.Millisecond
	}
	cutoff := r.s.Clock.Now().Add(-window)
	slices.SortStableFunc(r.reorder, compareEntries)
	n := 0
	for n < len(r.reorder) && (all || !r.reorder[n].Time.After(cutoff) || r.reorder[n].Time.Before(r.committed)) {
		n++
	}
	if n == 0 {
		return
	}
	r.pending.Entries = append(r.pending.Entries, r.reorder[:n]...)
	if last := r.reorder[n-1].Time; last.After(r.committed) {
		r.committed = last
	}
	r.reorder = slices.Delete(r.reorder, 0, n)
}

func compareEntries(a, b domain.LogEntry) int {
	if c := a.Time.Compare(b.Time); c != 0 {
		return c
	}
	return strings.Compare(a.Pod, b.Pod)
}
