package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// rowOf returns the screen row of the first line whose message starts
// with prefix, after a 140x16 frame.
func rowOf(t *testing.T, m *Model, l *logsScreen, prefix string) int {
	t.Helper()
	render(m, 140, 16)
	for r, i := range l.screenRows {
		if e, _ := l.entryAt(i); strings.HasPrefix(e.Message, prefix) {
			return l.linesTop + r
		}
	}
	t.Fatalf("no row for %q", prefix)
	return 0
}

func click(m *Model, y int, mod tea.KeyMod) {
	m.Update(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft, Mod: mod})
}

func TestMouseClickMovesTheCursor(t *testing.T) {
	m, l := openLogs(t)
	y := rowOf(t, m, l, "Payment authorization failed")
	click(m, y, 0)
	if e, _ := l.entryAt(l.displayCursor()); !strings.HasPrefix(e.Message, "Payment authorization failed") || l.tail {
		t.Fatalf("the cursor goes to the clicked line, got %q", e.Message)
	}
	// The folded stack row below belongs to the same entry.
	click(m, y+1, 0)
	if e, _ := l.entryAt(l.displayCursor()); !strings.HasPrefix(e.Message, "Payment authorization failed") {
		t.Errorf("a stack row selects its entry, got %q", e.Message)
	}
	cur := l.displayCursor()
	click(m, 0, 0)  // the header
	click(m, 15, 0) // the status bar
	if l.displayCursor() != cur {
		t.Error("clicks outside the lines do nothing")
	}
}

func TestMouseShiftClickAndDragSelect(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	click(m, rowOf(t, m, l, "request completed POST"), 0)
	click(m, rowOf(t, m, l, "gateway latency"), tea.ModShift)
	if lo, hi, ok := l.rangeBounds(); !ok || hi-lo+1 != 3 {
		t.Fatalf("shift+click selects from the cursor: %d..%d %v", lo, hi, ok)
	}
	press(m, "y")
	if got := lastCopy(t, fc); strings.Count(got, "\n") != 3 || !strings.Contains(got, "HikariPool") {
		t.Fatalf("copy of the clicked range:\n%s", got)
	}
	press(m, "esc")

	from, to := rowOf(t, m, l, "request completed GET"), rowOf(t, m, l, "Card declined")
	click(m, from, 0)
	m.Update(tea.MouseMotionMsg{X: 10, Y: to - 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 10, Y: to, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 10, Y: to, Button: tea.MouseLeft})
	if lo, hi, ok := l.rangeBounds(); !ok || hi-lo+1 != 4 || l.dragging {
		t.Fatalf("a drag selects the lines it passes over: %d..%d %v, dragging %v", lo, hi, ok, l.dragging)
	}
	m.Update(tea.MouseMotionMsg{X: 10, Y: from, Button: tea.MouseLeft})
	if lo, hi, ok := l.rangeBounds(); !ok || hi-lo+1 != 4 || l.dragging {
		t.Errorf("motion after the release changes nothing: %d..%d", lo, hi)
	}
}

func TestMouseOption(t *testing.T) {
	m, _ := openLogs(t)
	m.opts.Mouse = false
	if v := m.View(); v.MouseMode != tea.MouseModeNone {
		t.Errorf("mouse: false leaves the mouse to the terminal, got mode %v", v.MouseMode)
	}
	m.opts.Mouse = true
	if v := m.View(); v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("mouse: true reads clicks and drags, got mode %v", v.MouseMode)
	}
}
