package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// traceEntry is an entry of trace id at t0 + ms milliseconds.
func traceEntry(ms int, pod string, lvl domain.Level, logger, msg, id string) domain.LogEntry {
	e := logEntry(0, pod, lvl, logger, msg)
	e.Time = t0.Add(time.Duration(ms) * time.Millisecond)
	e.TraceID = id
	return e
}

// traceBatch arrives in this order, which is not the order of the entries'
// own time: the trace view sorts them.
func traceBatch() ports.LogBatch {
	return ports.LogBatch{Entries: []domain.LogEntry{
		traceEntry(-11898, podA, domain.LevelInfo, "i.g.p.OrderController", "request received POST /v1/orders", "t1"),
		traceEntry(-12000, podB, domain.LevelDebug, "i.g.p.Gateway", "routing request", "t1"),
		traceEntry(-11280, podB, domain.LevelWarn, "i.g.p.DownstreamClient", "downstream latency above threshold", "t1"),
		traceEntry(-11161, podA, domain.LevelError, "i.g.p.OrderSagaService", "Request processing failed", "t1"),
		traceEntry(-11000, podA, domain.LevelInfo, "i.g.p.OrderController", "request completed", "t2"),
	}}
}

func TestTraceView(t *testing.T) {
	m, l := openLogs(t)
	feed(m, l, traceBatch())
	press(m, "e") // errors only: the trace ignores it, and esc restores it
	var failed uint64
	for i := range l.shown() {
		if e, _ := l.entryAt(i); e.Message == "Request processing failed" {
			l.cursor, l.tail, failed = i, false, e.Seq
		}
	}
	press(m, "x")
	press(m, "x") // back to filter mode
	press(m, "v")
	if l.trace == nil || len(l.rows) != 4 {
		t.Fatalf("v opens the 4 lines of trace t1 on both pods, got %d rows", len(l.rows))
	}
	var msgs []string
	for i := range l.shown() {
		e, _ := l.entryAt(i)
		msgs = append(msgs, e.Message)
	}
	if got := strings.Join(msgs, " | "); got != "routing request | request received POST /v1/orders | downstream latency above threshold | Request processing failed" {
		t.Fatalf("ordered by the entries' time: %s", got)
	}
	if e, _ := l.entryAt(l.displayCursor()); e.Seq != failed {
		t.Fatal("the cursor stays on the line v was pressed on")
	}
	out := render(m, 140, 10)
	golden(t, "logs_trace_140x10", out)
	for _, want := range []string{"TRACE", "trace t1", "4 lines · 2 pods · +839 ms", "+102 ms", "+0 ms"} {
		if !strings.Contains(render(m, 220, 10), want) {
			t.Errorf("missing %q in:\n%s", want, render(m, 220, 10))
		}
	}

	press(m, "x")
	if l.filter.Mode != domain.ModeFilter || len(l.rows) != 4 {
		t.Fatal("x does not turn the trace into a highlight of every line")
	}

	feed(m, l, ports.LogBatch{Entries: []domain.LogEntry{
		traceEntry(-11500, podB, domain.LevelInfo, "i.g.p.PaymentClient", "retry 1/3", "t1"),
		traceEntry(-11400, podB, domain.LevelInfo, "i.g.p.PaymentClient", "unrelated", "t3"),
	}})
	if len(l.rows) != 5 {
		t.Fatalf("a live line of the trace joins it: %d rows", len(l.rows))
	}
	if e, _ := l.entryAt(2); e.Message != "retry 1/3" {
		t.Errorf("at its place in time, got %q", e.Message)
	}
	if e, _ := l.entryAt(l.displayCursor()); e.Seq != failed {
		t.Error("a live line does not move the cursor off its entry")
	}

	press(m, "/", "r", "e", "t", "r", "y", "enter")
	if len(l.rows) != 1 {
		t.Fatalf("a filter narrows the trace: %d rows", len(l.rows))
	}
	press(m, "esc")
	if l.trace == nil || len(l.rows) != 5 {
		t.Fatalf("esc first clears the filter added in the trace: trace %v, %d rows", l.trace != nil, len(l.rows))
	}
	press(m, "esc")
	if l.trace != nil || l.filter.Levels[domain.LevelInfo] || l.filter.Active() {
		t.Fatal("esc leaves the trace and restores the levels and filters")
	}
	if e, _ := l.entryAt(l.displayCursor()); e.Seq != failed {
		t.Fatal("back on the entry v was pressed on")
	}

	press(m, "enter", "v")
	if m.top() != l || l.trace == nil || l.trace.id != "t1" {
		t.Fatal("v in zoom opens the trace of the zoomed entry")
	}
	press(m, "esc")
}

// TestTraceViewShowsFromItsStart: entering a trace from far down the logs
// shows the trace from its first line, not from the old scroll position.
func TestTraceViewShowsFromItsStart(t *testing.T) {
	m, l := openLogs(t)
	feed(m, l, traceBatch())
	l.offset = 12 // scrolled down before v
	for i := range l.shown() {
		if e, _ := l.entryAt(i); e.Message == "Request processing failed" {
			l.cursor, l.tail = i, false
		}
	}
	press(m, "v")
	if out := render(m, 140, 12); !strings.Contains(out, "+0 ms") || !strings.Contains(out, "Request processing failed") {
		t.Fatalf("the whole trace shows:\n%s", out)
	}
	press(m, "esc")
	if l.offset != 12 && l.offset > l.displayCursor() {
		t.Errorf("esc restores the scroll position, got offset %d", l.offset)
	}
}

func TestTraceViewWithoutTraceID(t *testing.T) {
	m, l := openLogs(t)
	press(m, "v")
	if l.trace != nil {
		t.Fatal("no trace for a line without trace_id")
	}
	if out := render(m, 200, 10); !strings.Contains(out, "this line has no trace_id") {
		t.Errorf("the flash says why:\n%s", out)
	}
}

func TestFormatDelta(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "+0 ms", 102 * time.Millisecond: "+102 ms", 1200 * time.Millisecond: "+1.2 s",
		184 * time.Second: "+3 m 04 s", 2*time.Hour + 5*time.Minute: "+2 h 05 m", -3 * time.Millisecond: "-3 ms",
	} {
		if got := formatDelta(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

// BenchmarkTraceView opens and leaves a 50-line trace in a full buffer of
// 50 000 lines: what v and esc cost.
func BenchmarkTraceView(b *testing.B) {
	m, _ := newTestModel(b, 1, "")
	l := newLogsScreen(m, "payment-service")
	l.buf = domain.NewLogBuffer(50000)
	var seq uint64
	for i := range 50000 {
		id := fmt.Sprintf("%08x", i)
		if i%1000 == 0 {
			id = "t1"
		}
		e := traceEntry(i, []string{podA, podB}[i%2], domain.LevelInfo, "i.g.p.OrderController", "request completed", id)
		s := l.buf.Append(e)
		if i == 25000 {
			seq = s
		}
	}
	l.rebuild()
	for b.Loop() {
		l.enterTrace(m, seq)
		l.exitTrace(m)
	}
}
