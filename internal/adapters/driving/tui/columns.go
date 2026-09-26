package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Automatic narrowing thresholds: until the user picks columns, the thread
// is hidden below threadMinWidth cells and the logger below loggerMinWidth.
const (
	threadMinWidth = 140
	loggerMinWidth = 110
)

// columnState is what the focus layout saves and restores.
type columnState struct {
	hide  ports.Columns
	podID podIDMode
}

// effectiveHide is the set of hidden columns for a terminal width.
func (l *logsScreen) effectiveHide(width int) ports.Columns {
	h := l.hide
	if !l.manualColumns {
		if width < threadMinWidth {
			h = h.With(ports.ColThread)
		}
		if width < loggerMinWidth {
			h = h.With(ports.ColLogger)
		}
	}
	return h
}

// toggleColumn flips a column and records a manual choice.
func (l *logsScreen) toggleColumn(m *Model, c ports.Column, name string) {
	l.hide = l.effectiveHide(m.width)
	l.manualColumns, l.focus = true, false
	if l.hide.Has(c) {
		l.hide = l.hide.Without(c)
		m.flash(name + " shown")
	} else {
		l.hide = l.hide.With(c)
		m.flash(name + " hidden")
	}
}

func (l *logsScreen) togglePod(m *Model) {
	l.manualColumns, l.focus = true, false
	if l.podID == podIDNone {
		l.podID = podIDShort
		m.flash("pod shown")
	} else {
		l.podID = podIDNone
		m.flash("pod hidden")
	}
}

// cycleTime goes local → UTC → relative → hidden → local.
func (l *logsScreen) cycleTime(m *Model) {
	switch {
	case l.hide.Has(ports.ColTime):
		l.hide, l.timestamps = l.hide.Without(ports.ColTime), ports.TimestampLocal
	case l.timestamps == ports.TimestampRelative:
		l.hide = l.hide.With(ports.ColTime)
	default:
		l.timestamps++
	}
	name := [...]string{"local", "UTC", "relative"}[l.timestamps]
	if l.hide.Has(ports.ColTime) {
		name = "hidden"
	}
	m.flash("timestamps " + name)
}

// toggleFocus hides pod, thread and logger in one key, or restores the
// previous columns.
func (l *logsScreen) toggleFocus(m *Model) {
	if l.focus {
		l.hide, l.podID, l.focus = l.beforeFocus.hide, l.beforeFocus.podID, false
		m.flash("focus layout off")
		return
	}
	l.beforeFocus = columnState{hide: l.hide, podID: l.podID}
	l.hide = l.hide.With(ports.ColThread).With(ports.ColLogger)
	l.podID, l.focus, l.manualColumns = podIDNone, true, true
	m.flash("focus layout on")
}

func (l *logsScreen) resetColumns(m *Model) {
	l.hide, l.podID, l.focus, l.manualColumns = 0, podIDShort, false, false
	m.flash("columns reset")
}

// columnsLabel lists the shown columns when they differ from the default.
func (l *logsScreen) columnsLabel(width int) string {
	hide := l.effectiveHide(width)
	if hide == 0 && l.podID != podIDNone {
		return ""
	}
	var shown []string
	add := func(on bool, name string) {
		if on {
			shown = append(shown, name)
		}
	}
	add(!hide.Has(ports.ColTime), "time")
	add(l.podID != podIDNone, "pod")
	add(!hide.Has(ports.ColLevel), "level")
	add(!hide.Has(ports.ColThread), "thread")
	add(!hide.Has(ports.ColLogger), "class")
	return "cols " + strings.Join(append(shown, "msg"), " ")
}

// columnsPicker is the columns popup (C): one letter per column.
type columnsPicker struct{ logs *logsScreen }

func newColumnsPicker(l *logsScreen) *columnsPicker { return &columnsPicker{logs: l} }

type columnChoice struct {
	key, name string
	col       ports.Column // 0 for the pod
}

var columnChoices = []columnChoice{
	{"t", "time", ports.ColTime},
	{"p", "pod", 0},
	{"l", "level", ports.ColLevel},
	{"h", "thread", ports.ColThread},
	{"c", "class (logger)", ports.ColLogger},
}

func (p *columnsPicker) update(m *Model, k tea.KeyPressMsg) tea.Cmd {
	l := p.logs
	key := k.String()
	for _, c := range columnChoices {
		if key != c.key {
			continue
		}
		if c.col == 0 {
			l.togglePod(m)
		} else {
			l.toggleColumn(m, c.col, c.name)
		}
		return nil
	}
	switch {
	case key == "z":
		l.hide = ports.Columns(0).With(ports.ColThread).With(ports.ColLogger).With(ports.ColLevel).With(ports.ColTime)
		l.podID, l.manualColumns, l.focus = podIDNone, true, false
		m.flash("message only")
	case key == "r":
		l.resetColumns(m)
	case m.opts.Keys.Is(key, ActBack), m.opts.Keys.Is(key, ActColumns), m.opts.Keys.Is(key, ActOpen):
		m.popup = nil
	}
	return nil
}

func (p *columnsPicker) view(m *Model) string {
	t := m.opts.Theme
	l := p.logs
	hide := l.effectiveHide(m.width)
	var lines []string
	for _, c := range columnChoices {
		on := !hide.Has(c.col)
		if c.col == 0 {
			on = l.podID != podIDNone
		}
		box := "[ ]"
		if on {
			box = "[x]"
		}
		lines = append(lines, " "+t.Key.Render(c.key)+"  "+box+" "+c.name)
	}
	lines = append(lines, " "+t.Dim.Render("    [x] message (always shown)"), "",
		" "+t.Key.Render("z")+"  message only", " "+t.Key.Render("r")+"  reset to defaults")
	if !l.manualColumns {
		lines = append(lines, "", t.Dim.Render(" automatic: thread hidden below 140 cells,"), t.Dim.Render(" class below 110, until you choose"))
	}
	w := 0
	for _, s := range lines {
		w = max(w, lipgloss.Width(s))
	}
	for i, s := range lines {
		lines[i] = s + strings.Repeat(" ", w-lipgloss.Width(s))
	}
	help := t.Key.Render("letter") + t.Dim.Render(" toggle  ") + t.Key.Render("esc") + t.Dim.Render(" close")
	return t.Popup.Render(strings.Join(append(append([]string{t.PopupTitle.Render("Columns"), ""}, lines...), "", help), "\n"))
}

func (p *columnsPicker) hints(m *Model) []hint {
	return []hint{{"t", "time"}, {"p", "pod"}, {"l", "level"}, {"h", "thread"}, {"c", "class"}, {"z", "message only"}, {"r", "reset"}, m.h(ActBack, "close")}
}
