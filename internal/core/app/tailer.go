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
	t := &tailer{s: r.s, q: r.q, scope: r.scope, pod: p, name: p.Name, ns: p.Namespace, container: container, msgs: r.msgs, wake: make(chan struct{}, 1)}
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
		return req
	}
	req.Limit = limit
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
	hist, last, seen, err := t.history(ctx)
	capped := req.Limit > 0 && len(hist) >= req.Limit
	if !t.send(ctx, tailMsg{history: hist, format: t.logFormat(), historyDone: true, historyErr: err, capped: capped}) || !t.q.Follow {
		return
	}
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

// history reads the window without following. It returns the raw lines
// (decoded later by the session, which keeps only the newest lines of all
// containers), the source time of the last line and the texts of the lines
// at that time, which a stream resumed from that time delivers again. A
// head stops after its lines and closes the stream, whatever the source
// sends.
func (t *tailer) history(ctx context.Context) ([]domain.RawLine, time.Time, map[string]bool, error) {
	seen := map[string]bool{}
	req := t.request()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := t.s.Logs.Stream(ctx, req)
	if err != nil {
		return nil, time.Time{}, seen, err
	}
	var out []domain.RawLine
	var last time.Time
	for l := range st.Lines() {
		out = append(out, l)
		if l.Time.After(last) {
			last = l.Time
			clear(seen)
		}
		seen[l.Text] = true
		if req.Window.IsHead() && len(out) >= req.Window.Head {
			return out, last, seen, nil // the deferred cancel closes the stream
		}
	}
	return out, last, seen, st.Err()
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
		batch, muted = nil, nil
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
