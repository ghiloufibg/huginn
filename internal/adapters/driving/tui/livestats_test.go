package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func TestLevelCountsFollowTheView(t *testing.T) {
	m, l := openLogs(t)
	if l.levels[domain.LevelError] != 2 || l.levels[domain.LevelWarn] != 1 {
		t.Fatalf("counts %v", l.levels)
	}
	if out := render(m, 160, 20); !strings.Contains(out, "2 errors · 1 warning") {
		t.Fatalf("status bar:\n%s", out)
	}
	press(m, "e") // errors only: the view holds no warning
	if l.levels[domain.LevelError] != 2 || l.levels[domain.LevelWarn] != 0 {
		t.Fatalf("after a level filter: %v", l.levels)
	}
	press(m, "a")
	feed(m, l, ports.LogBatch{Entries: []domain.LogEntry{logEntry(60, podA, domain.LevelError, "a.B", "boom")}})
	if l.levels[domain.LevelError] != 3 {
		t.Fatalf("live error not counted: %v", l.levels)
	}
}

func TestLevelCountsDropEvictedRows(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	m.opts.BufferLines = 1000
	snapshot(m, mockupSnapshot("rec"))
	l := newLogsScreen(m, "payment-service")
	var b ports.LogBatch
	for i := range 1500 {
		lvl := domain.LevelInfo
		if i < 100 {
			lvl = domain.LevelError // all evicted by the 500 newest
		}
		b.Entries = append(b.Entries, logEntry(i, podA, lvl, "a.B", "x"))
	}
	l.apply(b, t0)
	if l.levels[domain.LevelError] != 0 || l.levels[domain.LevelInfo] != len(l.rows) {
		t.Fatalf("counts %v for %d rows", l.levels, len(l.rows))
	}
}

func TestRateMeter(t *testing.T) {
	var r rateMeter
	if r.label(t0) != "" {
		t.Fatal("no rate before lines")
	}
	for i := range 10 {
		r.add(t0.Add(time.Duration(i)*500*time.Millisecond), 21) // 42 lines/s
	}
	if got := r.label(t0.Add(4500 * time.Millisecond)); got != "42/s" {
		t.Fatalf("rate %q", got)
	}
	if got := r.label(t0.Add(time.Minute)); got != "" {
		t.Fatalf("stale rate %q", got)
	}
	r.add(t0, 2)
	if got := r.label(t0); got != "0.4/s" {
		t.Fatalf("slow rate %q", got)
	}
}

func TestHistoryIsNotCountedAsLive(t *testing.T) {
	_, l := openLogs(t) // the mockup batch is history
	if got := l.rate.label(t0); got != "" {
		t.Fatalf("history counted as live: %q", got)
	}
}

func TestEmptyStatesOfferTheNextStep(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	run(m, m.push(newLogsScreen(m, "payment-service")))
	l := m.top().(*logsScreen)
	if !m.busy() {
		t.Fatal("the spinner runs while the history loads")
	}
	feed(m, l, ports.LogBatch{HistoryDone: true})
	out := render(m, 120, 12)
	if !strings.Contains(out, "no log line in the last 15m") || !strings.Contains(out, "t longer window") || !strings.Contains(out, "0 last lines") {
		t.Fatalf("empty window:\n%s", out)
	}
	if m.busy() || !m.ticking() {
		t.Fatal("loaded and following: no spinner, the clock ticks")
	}
	feed(m, l, paymentBatch())
	press(m, "/", "z", "z", "z", "enter")
	l.rebuild()
	if out := render(m, 120, 12); !strings.Contains(out, "no line out of") || !strings.Contains(out, "esc clear the last filter") {
		t.Fatalf("no match:\n%s", out)
	}
}

func TestLogsErrorCanBeRetried(t *testing.T) {
	m, l := openLogs(t)
	l.err = fmt.Errorf("list pods: %w", domain.ErrForbidden)
	if out := render(m, 120, 12); !strings.Contains(out, "r retry") {
		t.Fatalf("error state:\n%s", out)
	}
	before := len(sessions.queries)
	press(m, "r")
	if len(sessions.queries) != before+1 || l.err != nil || m.flashText != "reloading payment-service" {
		t.Fatalf("r must reopen the logs: %d queries, err %v", len(sessions.queries)-before, l.err)
	}
}
