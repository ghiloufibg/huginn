package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Columns come from the layouts of the config folder
// (layouts/<name>.yaml): m.opts.Columns lists them in order, with their
// picker key, color role and automatic narrowing width. The pod column is
// the TUI's own (I, p in the picker).

// columnState is what the focus layout and the column cycle save and
// restore.
type columnState struct {
	hide   ports.ColumnSet
	podID  podIDMode
	manual bool
}

func sameState(a, b columnState) bool {
	return a.podID == b.podID && a.manual == b.manual && maps.Equal(a.hide, b.hide)
}

// initialHide hides the columns the layouts mark as not visible.
func initialHide(cols []ports.ColumnSpec) ports.ColumnSet {
	hide := ports.ColumnSet{}
	for _, c := range cols {
		if !c.Visible {
			hide = hide.With(c.Name)
		}
	}
	return hide
}

// effectiveHide is the set of hidden columns for a terminal width.
func (l *logsScreen) effectiveHide(m *Model, width int) ports.ColumnSet {
	h := l.hide
	if !l.manualColumns {
		for _, c := range m.opts.Columns {
			if c.HideBelow > 0 && width < c.HideBelow {
				h = h.With(c.Name)
			}
		}
	}
	return h
}

// namesWithRole lists the columns of a role (time, level…).
func namesWithRole(cols []ports.ColumnSpec, roles ...ports.Role) []string {
	var out []string
	for _, c := range cols {
		if slices.Contains(roles, c.Role) {
			out = append(out, c.Name)
		}
	}
	return out
}

// levelHidden reports whether no level column is shown, so WARN messages
// must carry the level themselves.
func levelHidden(cols []ports.ColumnSpec, hide ports.ColumnSet) bool {
	levels := namesWithRole(cols, ports.RoleLevel)
	for _, n := range levels {
		if !hide.Has(n) {
			return false
		}
	}
	return len(levels) > 0
}

// cycleColumns hides the next shown column (in layout order); once none is
// left, it restores the layout from before the first press. Columns
// already hidden (by width, C or config) are skipped, so every press
// changes the line.
func (l *logsScreen) cycleColumns(m *Model) {
	hide := l.effectiveHide(m, m.width)
	for _, c := range m.opts.Columns {
		if hide.Has(c.Name) {
			continue
		}
		if !l.cycling {
			l.beforeCycle = columnState{hide: l.hide, podID: l.podID, manual: l.manualColumns}
			l.cycling = true
		}
		l.hide, l.manualColumns, l.focus = hide.With(c.Name), true, false
		m.flash(c.Name + " hidden")
		return
	}
	if l.cycling {
		b := l.beforeCycle
		l.hide, l.podID, l.manualColumns, l.cycling = b.hide, b.podID, b.manual, false
		m.flash("columns restored")
		return
	}
	// Every column was hidden another way: show them.
	for _, c := range m.opts.Columns {
		hide = hide.Without(c.Name)
	}
	l.hide, l.manualColumns, l.focus = hide, true, false
	m.flash("columns shown")
}

// toggleColumn flips a column and records a manual choice.
func (l *logsScreen) toggleColumn(m *Model, name string) {
	l.hide = l.effectiveHide(m, m.width)
	l.manualColumns, l.focus, l.cycling = true, false, false
	if l.hide.Has(name) {
		l.hide = l.hide.Without(name)
		m.flash(name + " shown")
	} else {
		l.hide = l.hide.With(name)
		m.flash(name + " hidden")
	}
}

func (l *logsScreen) togglePod(m *Model) {
	l.manualColumns, l.focus, l.cycling = true, false, false
	if l.podID == podIDNone {
		l.podID = podIDShort
		m.flash("pod shown")
	} else {
		l.podID = podIDNone
		m.flash("pod hidden")
	}
}

// cycleTime goes local → UTC → relative → local. It never hides the time
// (c does): when the time is hidden, it shows it in the next format.
func (l *logsScreen) cycleTime(m *Model) {
	if l.timestamps >= ports.TimestampRelative {
		l.timestamps = ports.TimestampLocal
	} else {
		l.timestamps++
	}
	if times := namesWithRole(m.opts.Columns, ports.RoleTimestamp); slices.ContainsFunc(times, l.hide.Has) {
		l.hide, l.cycling = l.hide.Without(times...), false
	}
	m.flash("time " + timeFormatName(l.timestamps))
}

func timeFormatName(t ports.TimestampMode) string {
	switch t {
	case ports.TimestampUTC:
		return "UTC"
	case ports.TimestampRelative:
		return "relative"
	}
	return "local"
}

// toggleFocus keeps only the time and level columns (and the message), or
// restores the previous columns.
func (l *logsScreen) toggleFocus(m *Model) {
	if l.focus {
		l.hide, l.podID, l.focus = l.beforeFocus.hide, l.beforeFocus.podID, false
		m.flash("focus layout off")
		return
	}
	l.beforeFocus = columnState{hide: l.hide, podID: l.podID}
	keep := namesWithRole(m.opts.Columns, ports.RoleTimestamp, ports.RoleLevel)
	for _, c := range m.opts.Columns {
		if !slices.Contains(keep, c.Name) {
			l.hide = l.hide.With(c.Name)
		}
	}
	l.podID, l.focus, l.manualColumns, l.cycling = podIDNone, true, true, false
	m.flash("focus layout on")
}

// resetColumns returns to the configured columns (automatic narrowing
// when none are configured).
func (l *logsScreen) resetColumns(m *Model) {
	l.hide, l.podID, l.focus, l.manualColumns, l.cycling = initialHide(m.opts.Columns), podIDShort, false, false, false
	l.withColumns(m.opts.LogColumns, m.opts.Columns)
	m.flash("columns reset")
}

// resetDisplay puts back how lines look — columns, pod id, time format,
// pan, wrap — never which lines are shown (filters, window, scope).
func (l *logsScreen) resetDisplay(m *Model) {
	l.resetColumns(m)
	l.timestamps, l.pan, l.wrap = ports.TimestampLocal, 0, false
	m.flash("display reset")
}

// columnsLabel lists the shown columns when they differ from the default.
func (l *logsScreen) columnsLabel(m *Model, width int) string {
	hide := l.effectiveHide(m, width)
	if len(hide) == 0 && l.podID != podIDNone {
		return ""
	}
	var shown []string
	if l.podID != podIDNone {
		shown = append(shown, "pod")
	}
	for _, c := range m.opts.Columns {
		if !hide.Has(c.Name) {
			shown = append(shown, c.Name)
		}
	}
	return "cols " + strings.Join(append(shown, "msg"), " ")
}

// columnsPicker is the columns popup (C): one letter per column.
type columnsPicker struct{ logs *logsScreen }

func newColumnsPicker(l *logsScreen) *columnsPicker { return &columnsPicker{logs: l} }

func (p *columnsPicker) update(m *Model, k tea.KeyPressMsg) tea.Cmd {
	l := p.logs
	key := k.String()
	for _, c := range m.opts.Columns {
		if c.Key != "" && key == c.Key {
			l.toggleColumn(m, c.Name)
			return nil
		}
	}
	switch {
	case key == "p":
		l.togglePod(m)
	case key == "z":
		for _, c := range m.opts.Columns {
			l.hide = l.hide.With(c.Name)
		}
		l.podID, l.manualColumns, l.focus, l.cycling = podIDNone, true, false, false
		m.flash("message only")
	case key == "f":
		l.cycleTime(m)
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
	hide := l.effectiveHide(m, m.width)
	box := func(on bool) string {
		if on {
			return "[x]"
		}
		return "[ ]"
	}
	lines := []string{" " + t.Key.Render("p") + "  " + box(l.podID != podIDNone) + " pod"}
	for _, c := range m.opts.Columns {
		key := t.Key.Render(c.Key)
		if c.Key == "" {
			key = " "
		}
		line := " " + key + "  " + box(!hide.Has(c.Name)) + " " + c.Name
		if c.Role == ports.RoleTimestamp {
			line += t.Dim.Render("  "+timeFormatName(l.timestamps)+" (") + t.Key.Render("f") + t.Dim.Render(" format)")
		}
		lines = append(lines, line)
	}
	lines = append(lines, " "+t.Dim.Render("    [x] message (always shown)"), "",
		" "+t.Key.Render("z")+"  message only", " "+t.Key.Render("r")+"  reset to defaults")
	if !l.manualColumns {
		var auto []string
		for _, c := range m.opts.Columns {
			if c.HideBelow > 0 {
				auto = append(auto, fmt.Sprintf("%s below %d", c.Name, c.HideBelow))
			}
		}
		if len(auto) > 0 {
			lines = append(lines, "", t.Dim.Render(" automatic until you choose:"), t.Dim.Render(" "+strings.Join(auto, ", ")))
		}
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
	hs := []hint{{"p", "pod"}}
	for _, c := range m.opts.Columns {
		if c.Key != "" {
			hs = append(hs, hint{c.Key, c.Name})
		}
	}
	return append(hs, hint{"f", "time format"}, hint{"z", "message only"}, hint{"r", "reset"}, m.h(ActBack, "close"))
}

// withColumns applies the configured columns (ui.yaml log_columns); none
// configured keeps the layouts' visibility and automatic narrowing.
func (l *logsScreen) withColumns(shown []string, cols []ports.ColumnSpec) *logsScreen {
	if len(shown) == 0 {
		return l
	}
	l.hide = ports.ColumnSet{}
	for _, c := range cols {
		if !slices.Contains(shown, c.Name) {
			l.hide = l.hide.With(c.Name)
		}
	}
	if !slices.Contains(shown, "pod") {
		l.podID = podIDNone
	}
	l.manualColumns = true
	return l
}
