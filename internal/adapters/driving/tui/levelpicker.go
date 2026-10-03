package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// levelPicker is the multi-select level popup (l).
type levelPicker struct {
	logs   *logsScreen
	chosen domain.LevelSet
	cursor int
}

var pickerLevels = []domain.Level{domain.LevelError, domain.LevelWarn, domain.LevelInfo, domain.LevelDebug, domain.LevelUnknown}

func newLevelPicker(l *logsScreen) *levelPicker {
	chosen := domain.LevelSet{}
	for _, lv := range pickerLevels {
		chosen[lv] = l.filter.Levels[lv]
	}
	return &levelPicker{logs: l, chosen: chosen}
}

func (p *levelPicker) update(m *Model, k tea.KeyPressMsg) tea.Cmd {
	keys, key := m.opts.Keys, k.String()
	switch {
	case keys.Is(key, ActDown):
		p.cursor = min(p.cursor+1, len(pickerLevels)-1)
	case keys.Is(key, ActUp):
		p.cursor = max(p.cursor-1, 0)
	case key == "space":
		lv := pickerLevels[p.cursor]
		p.chosen[lv] = !p.chosen[lv]
	case keys.Is(key, ActErrorsOnly):
		p.chosen = errorsOnly()
	case keys.Is(key, ActWarnAndError):
		p.chosen = warnAndError()
	case keys.Is(key, ActAllLevels):
		p.chosen = domain.AllLevels()
	case keys.Is(key, ActOpen):
		m.popup = nil
		p.logs.setLevels(m, p.chosen)
	case keys.Is(key, ActBack), keys.Is(key, ActLevels):
		m.popup = nil
	}
	return nil
}

func errorsOnly() domain.LevelSet { return domain.LevelSet{domain.LevelError: true} }
func warnAndError() domain.LevelSet {
	return domain.LevelSet{domain.LevelError: true, domain.LevelWarn: true}
}

func (p *levelPicker) view(m *Model) string {
	t := &m.opts.Theme
	hints := map[domain.Level]string{
		domain.LevelError: m.label(ActErrorsOnly) + "  errors only", domain.LevelWarn: m.label(ActWarnAndError) + "  warn and error",
		domain.LevelInfo: m.label(ActAllLevels) + "  all levels", domain.LevelUnknown: "(no level detected)",
	}
	var lines []string
	for i, lv := range pickerLevels {
		box := "[ ]"
		if p.chosen[lv] {
			box = "[x]"
		}
		name := lv.String()
		if lv == domain.LevelUnknown {
			name = "unknown"
		}
		line := " " + box + " " + t.levelStyle(lv).Render(padRight(name, 8)) + t.Dim.Render(hints[lv])
		if i == p.cursor {
			line = t.Selected.Render(" "+box+" "+padRight(name, 8)) + t.Dim.Render(hints[lv])
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
	help := t.Key.Render("space") + t.Dim.Render(" toggle  ") + t.Key.Render("enter") + t.Dim.Render(" apply  ") + t.Key.Render("esc") + t.Dim.Render(" cancel")
	return t.Popup.Render(strings.Join(append(append([]string{t.PopupTitle.Render("Log levels"), ""}, lines...), "", help), "\n"))
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func (p *levelPicker) hints(m *Model) []hint {
	return []hint{m.pair(ActDown, ActUp, "move"), {"space", "toggle"}, m.h(ActErrorsOnly, "errors"), m.h(ActWarnAndError, "warn+"), m.h(ActAllLevels, "all"), m.h(ActOpen, "apply"), m.h(ActBack, "cancel")}
}
