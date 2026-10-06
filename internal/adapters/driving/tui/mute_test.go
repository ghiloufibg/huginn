package tui

import (
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func TestMutedLinesCountedAndShownOnDemand(t *testing.T) {
	m, l := openLogs(t)
	feed(m, l, ports.LogBatch{Muted: 40})
	feed(m, l, ports.LogBatch{Muted: 2})
	if out := render(m, 220, 24); !strings.Contains(out, "muted 42") {
		t.Fatalf("status bar must count the muted lines:\n%s", out)
	}
	before := len(sessions.queries)
	press(m, "M")
	if len(sessions.queries) != before+1 || !sessions.queries[before].NoMute {
		t.Fatalf("M must reopen showing the muted loggers: %+v", sessions.queries[before:])
	}
	if out := render(m, 220, 24); m.flashText != "muted loggers shown" || !strings.Contains(out, "muted loggers shown") || l.muted != 0 {
		t.Fatalf("flash %q, muted %d:\n%s", m.flashText, l.muted, out)
	}
	press(m, "M")
	if q := sessions.queries[len(sessions.queries)-1]; q.NoMute {
		t.Fatal("M again must hide the muted loggers")
	}
}

func TestEmptyViewSaysLinesAreMuted(t *testing.T) {
	m, l := openLogs(t)
	press(m, "M", "M") // reopen: an empty buffer
	feed(m, l, ports.LogBatch{Muted: 7, HistoryDone: true})
	out := render(m, 160, 24)
	if !strings.Contains(out, "all 7 lines are from muted loggers") || !strings.Contains(out, "M show them") {
		t.Fatalf("empty view:\n%s", out)
	}
}
