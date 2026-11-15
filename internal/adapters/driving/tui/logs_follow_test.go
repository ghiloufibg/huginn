package tui

import (
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Found on the real cluster (M22 F-7): follow off reloaded the window, which
// left the screen empty for about 300 ms, and a save in that time found
// nothing to save. With everything loaded, stopping the stream is enough.
func TestFollowOffKeepsWhatIsShown(t *testing.T) {
	m, l := openLogs(t)
	feed(m, l, ports.LogBatch{HistoryDone: true})
	queries := len(sessions.queries)
	before := len(l.rows)
	if before == 0 {
		t.Fatal("no lines loaded")
	}
	press(m, "f")
	if len(sessions.queries) != queries {
		t.Fatalf("f reopened the session: %+v", sessions.queries[queries:])
	}
	if sessions.ctxs[queries-1].Err() == nil {
		t.Error("f must stop the stream")
	}
	if len(l.rows) != before {
		t.Errorf("the lines went away: %d, was %d", len(l.rows), before)
	}
	out := render(m, 160, 20)
	if !strings.Contains(out, "STOPPED") || !strings.Contains(out, "follow off") && !strings.Contains(m.note, "follow off") {
		t.Errorf("stopped state not shown:\n%s", out)
	}
	if strings.Contains(out, "loading") || strings.Contains(out, "0/0 lines") {
		t.Errorf("the screen was emptied:\n%s", out)
	}

	press(m, "f") // back on: a new stream, from the window
	if q := lastQuery(t); !q.Follow || len(sessions.queries) != queries+1 {
		t.Errorf("follow on must open a following session: %+v", sessions.queries[queries:])
	}
}

// Before the history is loaded, with a pause, or on pages, there is more to
// read than what is shown: follow off reopens as before (D-025).
func TestFollowOffReopensWhenNotSettled(t *testing.T) {
	t.Run("history still loading", func(t *testing.T) {
		m, _ := openLogs(t)
		press(m, "2") // a window change reopens: loading again, nothing fed yet
		n := len(sessions.queries)
		press(m, "f")
		if len(sessions.queries) != n+1 || lastQuery(t).Follow {
			t.Errorf("expected a reopen without follow: %+v", sessions.queries[n:])
		}
	})
	t.Run("paused", func(t *testing.T) {
		m, l := openLogs(t)
		feed(m, l, ports.LogBatch{HistoryDone: true})
		press(m, "space")
		n := len(sessions.queries)
		press(m, "f")
		if len(sessions.queries) != n+1 || lastQuery(t).Follow {
			t.Errorf("expected a reopen without follow: %+v", sessions.queries[n:])
		}
	})
}
