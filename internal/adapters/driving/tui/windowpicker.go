package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// windowPicker is the time-window popup (T).
type windowPicker struct {
	logs   *logsScreen
	cursor int
}

func newWindowPicker(m *Model, l *logsScreen) *windowPicker {
	p := &windowPicker{logs: l}
	for i, w := range m.opts.Windows {
		if w == l.window {
			p.cursor = i
		}
	}
	return p
}

func (p *windowPicker) update(m *Model, k tea.KeyPressMsg) tea.Cmd {
	keys, key := m.opts.Keys, k.String()
	if w, ok := p.logs.windowKey(m, key); ok {
		m.popup = nil
		return p.logs.setWindow(m, w)
	}
	switch {
	case keys.Is(key, ActDown):
		p.cursor = min(p.cursor+1, len(m.opts.Windows)-1)
	case keys.Is(key, ActUp):
		p.cursor = max(p.cursor-1, 0)
	case keys.Is(key, ActOpen):
		m.popup = nil
		return p.logs.setWindow(m, m.opts.Windows[p.cursor])
	case keys.Is(key, ActBack), keys.Is(key, ActWindowPick):
		m.popup = nil
	}
	return nil
}

func (p *windowPicker) view(m *Model) string {
	t := m.opts.Theme
	keysFor := []Action{ActWindow1, ActWindow2, ActWindow3, ActWindow4, ActWindow5, ActWindow6, ActWindow7}
	var lines []string
	for i, w := range m.opts.Windows {
		key := m.label(ActWindowTail)
		if i < len(m.opts.Windows)-1 && i < len(keysFor) {
			key = m.label(keysFor[i])
		}
		label := w.Label()
		if w.IsTail() {
			label = w.String() + " lines"
		}
		line := " " + t.Key.Render(key) + "  " + label
		if w == p.logs.window {
			line += t.Dim.Render("  (current)")
		}
		if i == p.cursor {
			line = t.Selected.Render(" " + key + "  " + label + " ")
		}
		lines = append(lines, line)
	}
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	help := t.Key.Render("enter") + t.Dim.Render(" load  ") + t.Key.Render("esc") + t.Dim.Render(" cancel")
	return t.Popup.Render(strings.Join(append(append([]string{t.PopupTitle.Render("Time window"), ""}, lines...), "", help), "\n"))
}

func (p *windowPicker) hints(m *Model) []hint {
	return []hint{m.pair(ActDown, ActUp, "move"), {m.label(ActWindow1) + "…" + m.label(ActWindowTail), "pick"}, m.h(ActOpen, "load"), m.h(ActBack, "cancel")}
}
