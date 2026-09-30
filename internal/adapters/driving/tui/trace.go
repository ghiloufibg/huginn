package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// traceState is the trace view of a logs screen (docs/plan/M8, D-047):
// the lines of the buffer sharing one trace_id, on every pod, in the
// order of their own time, with the time since the first one. The
// filters and scope of the logs are kept here and restored on esc.
type traceState struct {
	id string

	// what the logs showed before, restored on leaving
	filter         domain.LogFilter
	committed      []domain.TextFilter
	input          string
	scope          map[string]bool
	containerScope map[string]bool
	seq            uint64 // the entry v was pressed on
	tail           bool
	offset         int // first displayed row

	// summary of the rows, updated when they change
	start, end time.Time
	pods       int
	partial    bool // the trace may start before the loaded lines
}

// enterTrace opens the trace of the entry seq, or switches to it from
// another trace.
func (l *logsScreen) enterTrace(m *Model, seq uint64) {
	i, ok := l.buf.Index(seq)
	if !ok {
		return
	}
	id := l.buf.At(i).TraceID
	if id == "" {
		m.flash("this line has no trace_id: map fields.trace_id, or name a transform group trace_id, in its format")
		return
	}
	if l.trace == nil {
		l.trace = &traceState{
			filter: l.filter, committed: l.committed, input: l.input.String(),
			scope: l.scope, containerScope: l.containerScope, seq: seq, tail: l.tail, offset: l.offset,
		}
	}
	l.trace.id = id
	l.filter = domain.LogFilter{Levels: domain.AllLevels(), Mode: domain.ModeFilter}
	l.committed = []domain.TextFilter{domain.FieldFilter("trace_id", id, false)}
	l.input.Clear()
	l.editing = false
	l.scope, l.containerScope = nil, nil
	l.setTexts()
	l.tail, l.offset = false, 0 // a trace is short: show it from its first line
	l.rebuildFrom(seq)
	m.flash(fmt.Sprintf("trace %s: %d lines", shortTrace(id), len(l.rows)))
}

// exitTrace restores the logs as they were before the trace, on the entry
// v was pressed on.
func (l *logsScreen) exitTrace(m *Model) {
	t := l.trace
	l.trace = nil
	l.filter, l.committed = t.filter, t.committed
	l.input = lineEdit{text: []rune(t.input)}
	l.scope, l.containerScope = t.scope, t.containerScope
	l.setTexts()
	keep := t.seq
	if t.tail {
		keep = 0
	}
	l.tail, l.offset = t.tail, t.offset
	l.rebuildFrom(keep)
	m.flash("back to the logs")
}

// traceBase is the number of filters the trace itself stacks: esc removes
// the filters added in the trace before it leaves the trace.
const traceBase = 1

// traceHasExtraFilters reports filters added inside the trace.
func (l *logsScreen) traceHasExtraFilters() bool {
	return l.input.String() != "" || len(l.committed) > traceBase
}

// orderTrace puts the trace rows in the order of their entries' time, the
// time the application wrote (rows are appended in the order the cluster
// received them), and updates the summary.
func (l *logsScreen) orderTrace() {
	t := l.trace
	timeOf := func(r viewRow) time.Time {
		if i, ok := l.buf.Index(r.seq); ok {
			return l.buf.At(i).Time
		}
		return time.Time{}
	}
	sorted := slices.IsSortedFunc(l.rows, func(a, b viewRow) int { return timeOf(a).Compare(timeOf(b)) })
	if !sorted {
		slices.SortStableFunc(l.rows, func(a, b viewRow) int { return timeOf(a).Compare(timeOf(b)) })
	}
	t.start, t.end, t.pods, t.partial = time.Time{}, time.Time{}, 0, false
	if len(l.rows) == 0 {
		return
	}
	t.start, t.end = timeOf(l.rows[0]), timeOf(l.rows[len(l.rows)-1])
	pods := map[string]bool{}
	for _, r := range l.rows {
		if i, ok := l.buf.Index(r.seq); ok {
			pods[l.buf.At(i).Pod] = true
		}
		if r.seq == l.buf.FirstSeq() {
			t.partial = true // older lines of the trace may have been dropped or not loaded
		}
	}
	t.pods = len(pods)
}

// keepCursor runs reorder, which moves rows, keeping the cursor on the
// same entry (the tail stays the tail).
func (l *logsScreen) keepCursor(reorder func()) {
	var keep uint64
	if !l.tail {
		if e, ok := l.entryAt(l.displayCursor()); ok {
			keep = e.Seq
		}
	}
	reorder()
	if keep == 0 {
		return
	}
	if i := slices.IndexFunc(l.rows, func(r viewRow) bool { return r.seq == keep }); i >= 0 {
		l.cursor = i
		if l.newestTop {
			l.cursor = len(l.rows) - 1 - i
		}
	}
}

// traceDelta is the delta column of a trace row.
func (l *logsScreen) traceDelta(e *domain.LogEntry) string {
	return fmt.Sprintf("%9s ", formatDelta(e.Time.Sub(l.trace.start)))
}

// traceSummary is the status bar of the trace view.
func (l *logsScreen) traceSummary() []string {
	t := l.trace
	out := []string{
		"trace " + shortTrace(t.id),
		fmt.Sprintf("%d lines · %d pods · %s", len(l.rows), t.pods, formatDelta(t.end.Sub(t.start))),
	}
	if t.partial {
		out = append(out, "may start before the loaded lines (t: wider window)")
	}
	if len(l.filter.Texts) > traceBase {
		var extra []string
		for _, f := range l.filter.Texts[traceBase:] {
			extra = append(extra, f.String())
		}
		out = append(out, "filter "+strings.Join(extra, " AND "))
	}
	return out
}

// formatDelta writes a delta as +0 ms, +102 ms, +1.2 s, +3 m 04 s.
func formatDelta(d time.Duration) string {
	sign := "+"
	if d < 0 {
		sign, d = "-", -d
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%s%d ms", sign, d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%s%.1f s", sign, d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%s%d m %02d s", sign, int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%s%d h %02d m", sign, int(d.Hours()), int(d.Minutes())%60)
}

// shortTrace shortens a trace id for the status bar.
func shortTrace(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
