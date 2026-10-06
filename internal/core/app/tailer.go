package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// tailer reads one container: its history, then (when following) live
// lines, reconnecting from the last line's time.
type tailer struct {
	s         *LogSessions
	q         ports.LogQuery
	scope     ports.Scope
	pod       domain.Pod // owned by the session loop after start; see name, namespace
	name, ns  string
	container string
	msgs      chan<- tailMsg
	format    ports.LogFormat // chosen on first use, see logFormat
	hist      *historyCollector

	// running mirrors the container state from the pod watch, and wake is
	// signalled when an instance starts running: a container that is not
	// running has no stream to follow until then.
	running atomic.Bool
	wake    chan struct{}
	// done: the container will not run again (a completed init
	// container, a finished Job pod); the end of its stream is final.
	done atomic.Bool
}

// waitFallback bounds the wait for a container change, in case the pod
// watch missed it.
const waitFallback = time.Minute

func newTailer(r *session, p domain.Pod, container string) *tailer {
	t := &tailer{s: r.s, q: r.q, scope: r.scope, pod: p, name: p.Name, ns: p.Namespace, container: container, msgs: r.msgs, hist: r.hist, wake: make(chan struct{}, 1)}
	t.observe(p)
	return t
}

// observe records the container's state from a pod update and wakes the
// tailer when a new instance runs.
func (t *tailer) observe(p domain.Pod) {
	c, ok := containerOf(p, t.container)
	now := ok && c.State == domain.ContainerRunning && !p.Deleted // a terminating pod's stream ends for good
	prev, had := containerOf(t.pod, t.container)
	t.pod = p
	t.running.Store(now)
	done := ok && c.State == domain.ContainerTerminated &&
		(domain.InitDone(c) || p.Phase == domain.PodSucceeded || p.Phase == domain.PodFailed)
	if done && !t.done.Swap(true) { // a tailer waiting for a restart can stop
		select {
		case t.wake <- struct{}{}:
		default:
		}
	}
	// Only a (new) running instance has logs to follow; a crash is not
	// news, the end of its stream already told.
	if now && (!had || prev.State != domain.ContainerRunning || prev.Restarts != c.Restarts) {
		select {
		case t.wake <- struct{}{}:
		default:
		}
	}
}

func containerOf(p domain.Pod, name string) (domain.Container, bool) {
	for _, c := range p.Containers {
		if c.Name == name {
			return c, true
		}
	}
	return domain.Container{}, false
}

// logFormat returns the container's log format, chosen once.
func (t *tailer) logFormat() ports.LogFormat {
	if t.format.Decoder == nil {
		t.format = t.s.formatFor(t.q, t.container)
	}
	return t.format
}

func (t *tailer) send(ctx context.Context, m tailMsg) bool {
	m.pod, m.container = t.name, t.container
	select {
	case t.msgs <- m:
		return true
	case <-ctx.Done():
		return false
	}
}

func (t *tailer) request() ports.LogRequest {
	limit := t.s.MaxHistory
	if limit <= 0 {
		limit = 50000
	}
	req := ports.LogRequest{Scope: t.scope, Namespace: t.ns, Pod: t.name, Container: t.container, Window: t.q.Window, Previous: t.q.Previous}
	if w := t.q.Window; w.IsHead() { // the first lines, of the previous instance too
		req.Window.Head = min(w.Head, limit)
		if t.logFormat().Mute != nil {
			req.Window.Head *= muteBudget
		}
		return req
	}
	req.Limit = limit
	if t.logFormat().Mute != nil { // muted lines must not use up the room
		req.Limit *= muteBudget
	}
	if t.q.Previous { // the whole instance, up to the limit
		req.Window = domain.TimeWindow{}
	}
	return req
}

func (t *tailer) run(ctx context.Context) {
	defer recovered(t.s.logger(), "log stream of "+t.name+"/"+t.container, func(err error) {
		t.send(ctx, tailMsg{streamErr: err})
	})
	req := t.request()
	h, err := t.history(ctx)
	if limit := t.s.limit(); len(h.own) > limit {
		var skipped uint64
		h.own = keepNewest(h.own, limit, &skipped)
		h.skipped += int(skipped)
	}
	capped := req.Limit > 0 && h.read >= req.Limit
	msg := tailMsg{historyDone: true, historyErr: err, capped: capped, history: h}
	if !t.send(ctx, msg) || !t.q.Follow {
		return
	}
	last, seen := h.last, h.seen
	for attempt := 0; ctx.Err() == nil; attempt++ {
		select { // a start seen while the stream ran is not news
		case <-t.wake:
		default:
		}
		req := t.request()
		req.Follow, req.Limit = true, 0
		if !last.IsZero() {
			req.SinceTime = last
		}
		n, newLast, err := t.follow(ctx, req, seen)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			attempt = 0
		}
		if !newLast.IsZero() {
			last = newLast
		}
		if err == nil && t.done.Load() {
			t.send(ctx, tailMsg{clearErr: true})
			return
		}
		// A stream ends without error when its container stops (a crash
		// loop), and cannot start before the container does: wait for the
		// pod watch to report a change instead of reconnecting in a loop.
		if errors.Is(err, domain.ErrNotStarted) || (err == nil && !t.running.Load()) {
			if !t.send(ctx, tailMsg{streamErr: fmt.Errorf("%s/%s is not running: %w", t.name, t.container, domain.ErrNotStarted)}) ||
				!t.await(ctx) {
				return
			}
			if t.done.Load() { // it finished instead of restarting
				t.send(ctx, tailMsg{clearErr: true})
				return
			}
			attempt = -1
			continue
		}
		if err == nil {
			err = fmt.Errorf("log stream of %s/%s ended: %w", t.name, t.container, domain.ErrUnreachable)
		}
		if attempt == 0 && !errors.Is(err, domain.ErrNotFound) {
			t.send(ctx, tailMsg{notice: "reconnecting to " + t.name + "/" + t.container})
		}
		t.send(ctx, tailMsg{streamErr: err})
		if !t.sleep(ctx, t.backoff(attempt)) {
			return
		}
	}
}

// historyResult is what a tailer read of its container's window.
type historyResult struct {
	last    time.Time       // source time of the last line read
	seen    map[string]bool // texts of the lines at last
	first   time.Time       // source time of the first line read
	read    int             // lines read
	skipped int             // lines that could not be kept (beyond the window's cut)
	own     []domain.LogEntry
	muted   map[string]int // lines of muted loggers decoded while read
}

// historyOfferBatch is the number of lines a tailer offers the history
// collector at a time (one lock per batch).
const historyOfferBatch = 256

// history reads the window without following. Its lines go to the
// session's history collector undecoded, unless they cannot be kept there
// (beyond the cut, skipped as read). Heads are decoded as read, since a
// head counts the lines it shows. Once the session's history is cut (a pod
// appeared later), the tailer keeps and decodes its own, which the session
// places as live lines. It also returns what resuming the stream needs:
// the source time of the last line and the texts of the lines at that
// time, which a stream resumed from that time delivers again.
func (t *tailer) history(ctx context.Context) (historyResult, error) {
	res := historyResult{seen: map[string]bool{}}
	req := t.request()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := t.s.Logs.Stream(ctx, req)
	if err != nil {
		return res, err
	}
	f := t.logFormat()
	hc := t.hist
	head := 0
	if req.Window.IsHead() {
		head = min(t.q.Window.Head, t.s.limit())
	}
	own := hc.isClosed()
	q := hc.newQueue()
	shown := 0 // lines kept by a head
	var batch []histItem
	flush := func() {
		if len(batch) > 0 && !own && !hc.offer(q, batch, f.Mute != nil) {
			own = true // the history was cut meanwhile
		}
		if own {
			for i := range batch {
				t.keepOwn(&res, &batch[i], head)
			}
		}
		batch = batch[:0]
	}
	for l := range st.Lines() {
		if res.read == 0 {
			res.first = l.Time
		}
		res.read++
		if l.Time.After(res.last) {
			res.last = l.Time
			clear(res.seen)
		}
		res.seen[l.Text] = true
		if !own && !hc.admits(l.Time) {
			res.skipped++
			continue
		}
		it := histItem{line: l, format: &t.format, seq: uint64(res.read)}
		if head > 0 { // decoded now: a head counts the lines it shows
			e := safeDecode(f.Decoder, l)
			if pattern, ok := f.Mute.Match(&e); ok {
				if res.muted == nil {
					res.muted = map[string]int{}
				}
				res.muted[pattern]++
				continue
			}
			it.entry = &e
			shown++
		}
		batch = append(batch, it)
		if len(batch) == historyOfferBatch {
			flush()
		}
		if head > 0 && shown >= head {
			break // the deferred cancel closes the stream
		}
	}
	flush()
	if head > 0 && shown >= head {
		return res, nil
	}
	return res, st.Err()
}

// keepOwn keeps a history line of a tailer whose session's history is
// already cut: decoded, the newest limit (or the first head ones).
func (t *tailer) keepOwn(res *historyResult, it *histItem, head int) {
	e := it.entry
	if e == nil {
		d := safeDecode(t.format.Decoder, it.line)
		if pattern, ok := t.format.Mute.Match(&d); ok {
			if res.muted == nil {
				res.muted = map[string]int{}
			}
			res.muted[pattern]++
			return
		}
		e = &d
	}
	if head > 0 && len(res.own) >= head {
		return
	}
	res.own = append(res.own, *e)
	if limit := t.s.limit(); head == 0 && len(res.own) > 2*limit {
		var skipped uint64
		res.own = keepNewest(res.own, limit, &skipped)
		res.skipped += int(skipped)
	}
}

// liveBatch is the most live entries a tailer sends in one message.
const liveBatch = 256

// follow streams live lines until the stream ends, skipping lines already
// delivered (same source time and text at the resume boundary). It returns
// the number of lines read and the last source time. Muted lines are read
// but not delivered: they still move the resume point, and a stream of
// muted lines only is a healthy one.
//
// Lines are sent in batches without waiting: after a line arrives, the
// lines already waiting in the stream join it, up to liveBatch. A quiet
// stream sends each line at once; a busy one sends one message per
// hundreds of lines instead of one per line.
func (t *tailer) follow(ctx context.Context, req ports.LogRequest, seen map[string]bool) (int, time.Time, error) {
	st, err := t.s.Logs.Stream(ctx, req)
	if err != nil {
		return 0, time.Time{}, err
	}
	t.send(ctx, tailMsg{clearErr: true})
	f := t.logFormat()
	n := 0
	var last time.Time
	var batch []domain.LogEntry
	var muted map[string]int
	read := func(l domain.RawLine) {
		if skipResumed(req.SinceTime, l, seen) {
			return
		}
		e := safeDecode(f.Decoder, l)
		if pattern, ok := f.Mute.Match(&e); ok {
			if muted == nil {
				muted = map[string]int{}
			}
			muted[pattern]++
		} else {
			batch = append(batch, e)
		}
		n++
		if l.Time.After(last) {
			last = l.Time
			clear(seen)
		}
		seen[l.Text] = true
	}
	lines := st.Lines()
	for l := range lines {
		// Sized for the lines already waiting: no regrowth, and no
		// large batch for a quiet stream.
		batch = make([]domain.LogEntry, 0, min(len(lines)+1, liveBatch))
		read(l)
		open := true
	drain:
		for open && len(batch) < liveBatch {
			select {
			case l, open = <-lines:
				if open {
					read(l)
				}
			default:
				break drain
			}
		}
		if (len(batch) > 0 || muted != nil) && !t.send(ctx, tailMsg{live: batch, muted: muted}) {
			return n, last, nil
		}
		muted = nil
		if !open {
			break
		}
	}
	return n, last, st.Err()
}

// await waits for the container to run again, or for waitFallback.
func (t *tailer) await(ctx context.Context) bool {
	tk := t.s.Clock.NewTicker(waitFallback)
	defer tk.Stop()
	select {
	case <-t.wake:
		return true
	case <-tk.C():
		return true
	case <-ctx.Done():
		return false
	}
}

func (t *tailer) backoff(attempt int) time.Duration {
	b := t.s.Backoff
	if len(b) == 0 {
		b = defaultBackoff
	}
	return b[min(attempt, len(b)-1)]
}

func (t *tailer) sleep(ctx context.Context, d time.Duration) bool {
	tk := t.s.Clock.NewTicker(d)
	defer tk.Stop()
	select {
	case <-tk.C():
		return true
	case <-ctx.Done():
		return false
	}
}

// skipResumed tells whether a line of a resumed stream was already
// delivered. The stream restarts at since, the time of the last delivered
// line, but sources may honor since to the second only (the Kubernetes
// API): lines strictly before since were delivered, and lines at since
// were delivered when their text was seen at that time.
func skipResumed(since time.Time, l domain.RawLine, seen map[string]bool) bool {
	if since.IsZero() || l.Time.IsZero() {
		return false
	}
	return l.Time.Before(since) || (l.Time.Equal(since) && seen[l.Text])
}
