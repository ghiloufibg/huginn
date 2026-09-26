package app

import (
	"context"
	"errors"
	"fmt"
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
	pod       domain.Pod
	container string
	msgs      chan<- tailMsg
	dec       ports.LogDecoder // chosen on first use
}

func (t *tailer) decode(l domain.RawLine) domain.LogEntry {
	if t.dec == nil {
		t.dec = t.s.Decoders.For(t.q.Repo, t.container)
	}
	return t.dec.Decode(l)
}

func (t *tailer) send(ctx context.Context, m tailMsg) bool {
	m.pod, m.container = t.pod.Name, t.container
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
	return ports.LogRequest{Scope: t.scope, Namespace: t.pod.Namespace, Pod: t.pod.Name, Container: t.container, Window: t.q.Window, Limit: limit}
}

func (t *tailer) run(ctx context.Context) {
	req := t.request()
	hist, last, seen, err := t.history(ctx)
	capped := req.Limit > 0 && len(hist) >= req.Limit
	if !t.send(ctx, tailMsg{history: hist, historyDone: true, historyErr: err, capped: capped}) || !t.q.Follow {
		return
	}
	for attempt := 0; ctx.Err() == nil; attempt++ {
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
		if err == nil {
			err = fmt.Errorf("log stream of %s/%s ended: %w", t.pod.Name, t.container, domain.ErrUnreachable)
		}
		if attempt == 0 && !errors.Is(err, domain.ErrNotFound) {
			t.send(ctx, tailMsg{notice: "reconnecting to " + t.pod.Name + "/" + t.container})
		}
		t.send(ctx, tailMsg{streamErr: err})
		if !t.sleep(ctx, t.backoff(attempt)) {
			return
		}
	}
}

// history reads the window without following. It returns the entries, the
// source time of the last line and the texts of the lines at that time,
// which a stream resumed from that time delivers again.
func (t *tailer) history(ctx context.Context) ([]domain.LogEntry, time.Time, map[string]bool, error) {
	seen := map[string]bool{}
	st, err := t.s.Logs.Stream(ctx, t.request())
	if err != nil {
		return nil, time.Time{}, seen, err
	}
	var out []domain.LogEntry
	var last time.Time
	for l := range st.Lines() {
		out = append(out, t.decode(l))
		if l.Time.After(last) {
			last = l.Time
			clear(seen)
		}
		seen[l.Text] = true
	}
	return out, last, seen, st.Err()
}

// follow streams live lines until the stream ends, skipping lines already
// delivered (same source time and text at the resume boundary). It returns
// the number of lines delivered and the last source time.
func (t *tailer) follow(ctx context.Context, req ports.LogRequest, seen map[string]bool) (int, time.Time, error) {
	st, err := t.s.Logs.Stream(ctx, req)
	if err != nil {
		return 0, time.Time{}, err
	}
	t.send(ctx, tailMsg{clearErr: true})
	n := 0
	var last time.Time
	for l := range st.Lines() {
		if !req.SinceTime.IsZero() && !l.Time.After(req.SinceTime) {
			if seen[l.Text] {
				continue
			}
		}
		e := t.decode(l)
		if !t.send(ctx, tailMsg{live: &e}) {
			return n, last, nil
		}
		n++
		if l.Time.After(last) {
			last = l.Time
			clear(seen)
		}
		seen[l.Text] = true
	}
	return n, last, st.Err()
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
