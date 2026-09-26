package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// envPicker is the environment switch popup.
type envPicker struct{ cursor int }

func newEnvPicker(m *Model) *envPicker {
	p := &envPicker{}
	for i, e := range m.opts.Envs {
		if e.Name == m.env.Name {
			p.cursor = i
		}
	}
	return p
}

func (p *envPicker) update(m *Model, k tea.KeyPressMsg) tea.Cmd {
	keys, key := m.opts.Keys, k.String()
	switch {
	case keys.Is(key, ActDown):
		p.cursor = min(p.cursor+1, len(m.opts.Envs)-1)
	case keys.Is(key, ActUp):
		p.cursor = max(p.cursor-1, 0)
	case keys.Is(key, ActOpen):
		m.popup = nil
		return m.switchEnv(m.opts.Envs[p.cursor])
	case keys.Is(key, ActBack), keys.Is(key, ActSwitchEnv), key == "q":
		m.popup = nil
	}
	return nil
}

func (p *envPicker) view(m *Model) string {
	t := m.opts.Theme
	var lines []string
	width := 0
	for _, e := range m.opts.Envs {
		width = max(width, len(e.Name))
	}
	for i, e := range m.opts.Envs {
		where := e.Context
		if len(e.Namespaces) > 0 {
			where += " / " + strings.Join(e.Namespaces, ",")
		}
		line := " " + e.Name + strings.Repeat(" ", width-len(e.Name)+3) + where
		if e.Production {
			line += "   PRODUCTION"
		}
		if e.Name == m.env.Name {
			line += "   (current)"
		}
		style := lipgloss.NewStyle()
		if e.Production {
			style = t.Bad
		}
		if i == p.cursor {
			style = t.Selected
		}
		lines = append(lines, style.Render(line+" "))
	}
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	title := t.PopupTitle.Render("Switch environment")
	help := t.Key.Render(m.label(ActOpen)) + t.Dim.Render(" switch  ") + t.Key.Render(m.label(ActBack)) + t.Dim.Render(" cancel")
	return t.Popup.Render(strings.Join(append(append([]string{title, ""}, lines...), "", help), "\n"))
}

func (p *envPicker) hints(m *Model) []hint {
	return []hint{m.pair(ActDown, ActUp, "move"), m.h(ActOpen, "switch"), m.h(ActBack, "cancel")}
}
