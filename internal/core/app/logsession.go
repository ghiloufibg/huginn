package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
	Scopes   ScopeFunc
	Filter   domain.ContainerFilter
	// Decoders picks each container's decoder.
	Decoders ports.LogDecoders
	Clock    ports.Clock
	// Flush is the delay between two batches (default 33ms, about 30/s).
	Flush time.Duration
	// Reorder is how long live lines wait before being committed
	// (default 250ms).
	Reorder time.Duration
	// MaxHistory caps the history read per container (default 50 000,
	// usually the view's buffer size): older lines of the window are not
	// fetched.
	MaxHistory int
	// Backoff lists the waits between reconnections (default 1s, 2s, 5s,
	// 10s, 30s; the last repeats).
	Backoff []time.Duration
	Log     *slog.Logger
	// Standalone lets a repository be made of standalone pods (pods no
	// known workload claims), as the catalog lists them.
	Standalone bool
}

// Open implements ports.LogSession.
func (s *LogSessions) Open(ctx context.Context, q ports.LogQuery) (<-chan ports.LogBatch, error) {
	scope, err := s.Scopes(ctx, q.Env)
	if err != nil {
		return nil, err
	}
	workloads, err := s.workloadsOf(ctx, scope, q)
	if err != nil {
		return nil, err
	}
	pods, err := s.podsOf(ctx, scope, workloads)
	if err != nil {
		return nil, err
	}
	if q.Previous || q.Window.IsHead() { // history only: a head never follows
		q.Follow = false
	}
	run := &session{
		s: s, q: q, scope: scope, workloads: workloads, opened: s.Clock.Now(),
		hist: newHistoryCollector(s.limit(), q.Window.IsHead() && !q.Previous),
		pods: map[string]*podState{}, msgs: make(chan tailMsg, 1024), out: make(chan ports.LogBatch, 1),
	}
	go run.loop(ctx, pods)
	return run.out, nil
}

func (s *LogSessions) workloadsOf(ctx context.Context, scope ports.Scope, q ports.LogQuery) ([]domain.Workload, error) {
	// Namespace by namespace: one namespace the user cannot read must not
	// hide the repository's workloads in the others.
	var all []domain.Workload
	var nsErr error
	for _, ns := range scope.Namespaces {
		sc := scope
		sc.Namespaces = []string{ns}
		ws, err := s.Cluster.ListWorkloads(ctx, sc)
		if err != nil {
			nsErr = errors.Join(nsErr, err)
			continue
		}
		all = append(all, ws...)
		if s.Standalone {
			pods, err := s.Cluster.ListPods(ctx, sc, nil)
			if err != nil {
				nsErr = errors.Join(nsErr, err)
				continue
			}
			all = append(all, domain.StandaloneWorkloads(ws, pods)...)
		}
	}
	if all == nil && nsErr != nil {
		return nil, nsErr
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
		if slices.ContainsFunc(refs, func(r domain.WorkloadRef) bool { return sameRef(r, w.Ref) }) {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		if nsErr != nil { // perhaps in the namespace that could not be read
			return nil, nsErr
		}
		return nil, fmt.Errorf("repository %q in %s: %w", q.Repo, q.Env, domain.ErrNotFound)
	}
	return out, nil
}

func (s *LogSessions) podsOf(ctx context.Context, scope ports.Scope, ws []domain.Workload) ([]domain.Pod, error) {
	var out []domain.Pod
	for _, w := range ws {
		sc := scope
		sc.Namespaces = []string{w.Ref.Namespace}
		sel := ports.Selector(w.Selector)
		if w.Standalone {
			sel = nil // found by owner or name
		}
		pods, err := s.Cluster.ListPods(ctx, sc, sel)
		if err != nil {
			return nil, err
		}
		for _, p := range pods {
			if ownedBy(p, ws) && !slices.ContainsFunc(out, func(o domain.Pod) bool { return o.Name == p.Name && o.Namespace == p.Namespace }) {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b domain.Pod) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ownedBy tells whether a pod belongs to one of the workloads
// (domain.Workload.Owns): selectors can overlap (a CronJob without labels
// selects everything), so a pod that names its owner must name one of them.
func ownedBy(p domain.Pod, ws []domain.Workload) bool {
	return slices.ContainsFunc(ws, func(w domain.Workload) bool { return w.Owns(p) })
}

// sameRef compares workload references, ignoring the environment.
func sameRef(a, b domain.WorkloadRef) bool {
	a.Env, b.Env = "", ""
	return a == b
}

func (r *session) owned(p domain.Pod) bool { return ownedBy(p, r.workloads) }

// formatFor returns a container's log format; a query asking for muted
// lines gets the format without its mute.
func (s *LogSessions) formatFor(q ports.LogQuery, container string) ports.LogFormat {
	f := s.Decoders.For(q.Repo, container)
	if q.NoMute {
		f.Mute = nil
	}
	return f
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
	history        historyResult // with historyDone
	historyDone    bool
	historyErr     error
	capped         bool              // the history reached the line limit
	live           []domain.LogEntry // decoded live entries, in order
	muted          map[string]int    // live lines left out, per mute pattern
	notice         string
	podEvent       *domain.PodEvent
	streamErr      error // current error of the container, nil when streaming
	clearErr       bool
}

type podState struct {
	ports.PodState
	cancel  context.CancelFunc
	tailers []*tailer
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
	hist        *historyCollector
	histSkipped int // history lines tailers could not keep
	historySent bool
	reorder     reorderBuffer
	committed   time.Time
	pending     ports.LogBatch
	podsChanged bool
	// mutedBy counts the lines of muted loggers left out per pattern, and
	// muted in total; mutedReported is the total already put in a batch.
	muted         uint64
	mutedBy       map[string]uint64
	mutedReported uint64
	lastBatch     int // entries in the last batch sent: the next one's likely size
}

// limit is the number of entries the view keeps (its buffer size): the
// most any stage of the session holds, since older entries would be
// evicted from the view anyway.
func (s *LogSessions) limit() int {
	if s.MaxHistory > 0 {
		return s.MaxHistory
	}
	return 50000
}

func (r *session) loop(ctx context.Context, initial []domain.Pod) {
	defer close(r.out)
	defer recovered(r.s.logger(), "log session of "+r.q.Repo, func(err error) {
		select { // best effort: the view shows why the stream stopped
		case r.out <- ports.LogBatch{Notices: []ports.LogNotice{{Text: err.Error()}}}:
		default:
		}
	})
	for _, p := range initial {
		r.addPod(ctx, p, false)
	}
	if !r.q.Previous { // a previous instance is history: no pod changes
		r.watchPods(ctx)
	}
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
			r.reportMuted()
		case out <- r.batch():
			r.lastBatch = len(r.pending.Entries)
			r.pending, r.podsChanged = ports.LogBatch{}, false
		}
	}
}

func (r *session) hasPending() bool {
	b := &r.pending
	return len(b.Entries) > 0 || len(b.Late) > 0 || len(b.Notices) > 0 || b.HistoryDone || b.Muted > 0 || b.Skipped > 0 || r.podsChanged
}

// batch returns the pending batch, with the pod list if it changed.
func (r *session) batch() ports.LogBatch {
	b := r.pending
	if r.podsChanged {
		b.Pods = r.podList()
	}
	return b
}

// countMuted adds lines left out by muted loggers.
func (r *session) countMuted(byPattern map[string]int) {
	for p, n := range byPattern {
		if r.mutedBy == nil {
			r.mutedBy = map[string]uint64{}
		}
		r.mutedBy[p] += uint64(n)
		r.muted += uint64(n)
	}
}

// reportMuted puts the lines muted since the last report in the pending
// batch, with a copy of the counts per pattern: once per flush tick at
// most, whatever the rate of muted lines.
func (r *session) reportMuted() {
	if r.muted == r.mutedReported {
		return
	}
	r.pending.Muted += r.muted - r.mutedReported
	r.pending.MutedBy = maps.Clone(r.mutedBy)
	r.mutedReported = r.muted
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
	for _, c := range r.s.Filter.StreamContainers(p, r.q.Containers) {
		if r.q.Previous && c.Restarts == 0 && c.LastTermination == nil {
			continue
		}
		st.Containers = append(st.Containers, c.Name)
		if st.Roles == nil {
			st.Roles = map[string]domain.ContainerRole{}
		}
		st.Roles[c.Name] = r.s.Filter.Role(p, c)
		if !isNew {
			r.awaiting++
		}
		t := newTailer(r, p, c.Name)
		st.tailers = append(st.tailers, t)
		go t.run(pctx)
	}
	if r.q.Previous && len(st.Containers) == 0 {
		st.Err = fmt.Errorf("%s: %w", p.Name, domain.ErrNoPrevious)
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
			defer recovered(r.s.logger(), "pod watch of "+w.Ref.Name, nil)
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
	case m.live != nil || m.muted != nil:
		r.countMuted(m.muted)
		r.reorder.add(m.pod, m.container, m.live)
		r.boundReorder()
	case m.notice != "":
		r.pending.Notices = append(r.pending.Notices, ports.LogNotice{Pod: m.pod, Text: m.notice})
	case m.streamErr != nil || m.clearErr:
		p := r.pods[m.pod]
		if p == nil || errors.Is(p.Err, m.streamErr) {
			return
		}
		if m.streamErr != nil && !errors.Is(m.streamErr, domain.ErrNotStarted) && (p.Err == nil || p.Err.Error() != m.streamErr.Error()) {
			// Say why, once per distinct error: the pod strip only
			// shows its kind.
			r.s.logger().Warn("log stream failed", "pod", m.pod, "container", m.container, "err", m.streamErr)
			r.pending.Notices = append(r.pending.Notices, ports.LogNotice{Pod: m.pod, Text: m.pod + ": " + m.streamErr.Error()})
		}
		p.Err = m.streamErr
		r.podsChanged = true
	}
}

func (r *session) handlePod(ctx context.Context, ev domain.PodEvent) {
	if !r.owned(ev.Pod) {
		return
	}
	p, known := r.pods[ev.Pod.Name]
	switch {
	case ev.Type == domain.PodDeleted && known:
		p.Terminated, p.Pod = true, ev.Pod
		p.cancel()
		r.reorder.forget(ev.Pod.Name)
		r.podsChanged = true
	case !known && ev.Type != domain.PodDeleted:
		r.addPod(ctx, ev.Pod, true)
	case known && !p.Terminated:
		p.Pod = ev.Pod
		for _, t := range p.tailers {
			t.observe(ev.Pod)
		}
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
	h := m.history
	r.countMuted(h.muted)
	if r.historySent {
		// History of a pod that appeared later: it is recent, treat it as
		// live so the reorder window places it.
		r.reorder.add(m.pod, m.container, h.own)
		r.boundReorder()
		return
	}
	r.histSkipped += h.skipped
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
	if r.q.Previous {
		return ""
	}
	if r.q.Window.IsHead() {
		return r.rotationNotice(m)
	}
	if m.capped && !r.q.Window.IsTail() {
		return fmt.Sprintf("%s: older lines of the window not loaded (limit %d lines per container)", m.pod, m.history.read)
	}
	if r.q.Window.IsTail() || r.q.Window.Since <= 0 || m.history.read == 0 {
		return ""
	}
	start := r.opened.Add(-r.q.Window.Since)
	p := r.pods[m.pod]
	if p == nil || p.Pod.Started.IsZero() || !p.Pod.Started.Before(start) {
		return ""
	}
	first := m.history.first
	if first.IsZero() {
		return ""
	}
	if gap := first.Sub(start); gap <= 5*time.Minute || gap <= r.q.Window.Since/10 {
		return ""
	}
	return fmt.Sprintf("logs of %s available from %s only", m.pod, first.In(time.Local).Format("Jan 2 15:04"))
}

// rotationNotice reports, for a head, when a container's first line came
// more than a minute after its instance started: the node rotated the
// start of its logs away, and the API serves the current file only.
func (r *session) rotationNotice(m tailMsg) string {
	p := r.pods[m.pod]
	if p == nil || m.history.read == 0 || m.history.first.IsZero() {
		return ""
	}
	c, ok := containerOf(p.Pod, m.container)
	if !ok || c.Started.IsZero() {
		return ""
	}
	first := m.history.first
	if first.Sub(c.Started) <= time.Minute {
		return ""
	}
	who := m.pod
	if len(p.Containers) > 1 {
		who += "/" + m.container
	}
	return fmt.Sprintf("%s: first line at %s, the container started at %s (older lines rotated away on the node)",
		who, first.In(time.Local).Format("Jan 2 15:04"), c.Started.In(time.Local).Format("Jan 2 15:04"))
}

// finishHistory cuts the history of the window: the lines kept by the
// collector are decoded, from the side shown, until the buffer is full.
func (r *session) finishHistory() {
	head := r.q.Window.IsHead() && !r.q.Previous
	items, evicted := r.hist.close()
	entries, notDecoded, muted := decodeKept(items, r.s.limit(), head)
	r.countMuted(muted)
	dropped := r.histSkipped + evicted + notDecoded
	switch {
	case dropped > 0 && head:
		r.pending.Notices = append(r.pending.Notices, ports.LogNotice{
			Text: fmt.Sprintf("newer lines of the heads not loaded: %d lines kept (buffer size), %d newer skipped", len(entries), dropped),
		})
	case dropped > 0:
		r.pending.Notices = append(r.pending.Notices, ports.LogNotice{
			Text: fmt.Sprintf("older lines of the window not loaded: %d lines kept (buffer size), %d older skipped", len(entries), dropped),
		})
	}
	r.pending.Entries = append(r.pending.Entries, entries...)
	if n := len(entries); n > 0 {
		r.committed = entries[n-1].OrderTime()
	}
	r.historySent = true
	r.pending.HistoryDone = true
	r.boundPending()
}

// commit moves live lines older than the reorder window to the pending
// batch, in time order. Lines older than what is already committed are
// committed at once (they cannot be placed before committed lines).
func (r *session) commit(all bool) {
	if !r.historySent || r.reorder.len() == 0 {
		return
	}
	window := r.s.Reorder
	if window <= 0 {
		window = 250 * time.Millisecond
	}
	cutoff := r.s.Clock.Now().Add(-window)
	for {
		e, ok := r.reorder.oldest()
		if !ok {
			break
		}
		due := all || !e.OrderTime().After(cutoff) || e.OrderTime().Before(r.committed)
		if !due {
			break
		}
		r.commitOne(r.reorder.pop())
	}
	r.boundPending()
}

// commitOne adds an entry to the pending batch. One older than what the
// view already has (a stream that recovered after an outage delivers what
// it missed) is late: the view inserts it.
func (r *session) commitOne(e domain.LogEntry) {
	t := e.OrderTime()
	if t.Before(r.committed) {
		r.pending.Late = append(r.pending.Late, e)
		return
	}
	if r.pending.Entries == nil { // sized like the last batch: no regrowth
		r.pending.Entries = make([]domain.LogEntry, 0, max(r.lastBatch, 16))
	}
	r.pending.Entries = append(r.pending.Entries, e)
	r.committed = t
}

// boundReorder keeps at most limit entries waiting: past that, the oldest
// are committed at once (dropped while the history loads: the view would
// evict them anyway). The memory of a session then stays bounded whatever
// the line rate.
func (r *session) boundReorder() {
	for r.reorder.len() > r.s.limit() {
		e := r.reorder.pop()
		if !r.historySent {
			r.pending.Skipped++
			continue
		}
		r.commitOne(e)
	}
	r.boundPending()
}

// boundPending keeps the newest limit entries of the pending batch (and of
// its late entries): when the view cannot keep up, older ones would be
// evicted from its buffer on arrival. They are counted as skipped.
func (r *session) boundPending() {
	limit := r.s.limit()
	r.pending.Entries = keepNewest(r.pending.Entries, limit, &r.pending.Skipped)
	r.pending.Late = keepNewest(r.pending.Late, limit, &r.pending.Skipped)
}

// keepNewest drops the oldest entries beyond limit, counting them.
func keepNewest(es []domain.LogEntry, limit int, skipped *uint64) []domain.LogEntry {
	over := len(es) - limit
	if over <= 0 {
		return es
	}
	clear(es[:over]) // release their strings
	*skipped += uint64(over)
	return es[over:]
}

func compareEntries(a, b domain.LogEntry) int { return compareEntryPtrs(&a, &b) }

// compareEntryPtrs orders entries by time, then pod, without copying them.
func compareEntryPtrs(a, b *domain.LogEntry) int {
	if c := a.OrderTime().Compare(b.OrderTime()); c != 0 {
		return c
	}
	return strings.Compare(a.Pod, b.Pod)
}
