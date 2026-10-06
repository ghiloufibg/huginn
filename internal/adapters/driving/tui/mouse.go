package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Mouse on the logs screen (M9.3, D-050): a click moves the cursor to the
// clicked line, shift+click selects from the cursor to it, and a drag
// selects the lines it passes over. Each frame records which line fills
// each screen row, so wrapped lines and folded stacks map back to their
// entry.

// lineAtY returns the display position of the line drawn at screen row y,
// or false outside the lines.
func (l *logsScreen) lineAtY(y int) (int, bool) {
	r := y - l.linesTop
	if r < 0 || r >= len(l.screenRows) {
		return 0, false
	}
	return l.screenRows[r], true
}

// mouse handles clicks, drags and releases on the lines.
func (l *logsScreen) mouse(m *Model, msg tea.MouseMsg) {
	ev := msg.Mouse()
	i, ok := l.lineAtY(ev.Y)
	switch msg.(type) {
	case tea.MouseReleaseMsg:
		l.dragging = false
		return
	case tea.MouseClickMsg:
		if !ok || ev.Button != tea.MouseLeft {
			return
		}
		e, found := l.entryAt(i)
		if !found {
			return
		}
		if ev.Mod&tea.ModShift != 0 {
			if cur, has := l.entryAt(l.displayCursor()); has && (l.sel.anchor == 0 || l.sel.extending) {
				l.sel.anchor = cur.Seq
			}
			l.sel.end, l.sel.extending = e.Seq, false
			l.moveCursorTo(i)
			m.flash("range kept · y copy · Y copy raw · esc clear")
			return
		}
		l.moveCursorTo(i)
		l.dragging, l.dragFrom = true, e.Seq
	case tea.MouseMotionMsg:
		if !ok || !l.dragging || ev.Button != tea.MouseLeft {
			return
		}
		e, found := l.entryAt(i)
		if !found {
			return
		}
		if e.Seq != l.dragFrom || l.sel.anchor != 0 {
			l.sel.anchor, l.sel.end, l.sel.extending = l.dragFrom, e.Seq, false
		}
		l.moveCursorTo(i)
	}
}

// moveCursorTo puts the cursor on display position i, leaving the tail.
func (l *logsScreen) moveCursorTo(i int) {
	l.cursor = i
	l.tail = false
	l.scroll(0) // recompute tail when i is the newest line
}
