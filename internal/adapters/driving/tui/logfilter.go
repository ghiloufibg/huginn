package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// filterTickMsg applies a debounced filter change.
type filterTickMsg struct{ screen *logsScreen }

// Debounce delays: fast filters recompute at most once per frame; regexes
// that need the regex engine on many lines wait for a pause in typing.
const (
	fastDebounce = 30 * time.Millisecond
	slowDebounce = 300 * time.Millisecond
)

// contextSteps are the context sizes cycled by the context key.
var contextSteps = []int{0, 1, 3, 5}

// needsFullSelect reports whether appended entries cannot be filtered one
// by one (context rows depend on neighbours).
func (l *logsScreen) needsFullSelect() bool {
	return l.filter.Mode == domain.ModeFilter && l.filter.Active() && l.filter.Context > 0
}

// rowFor filters one new entry.
func (l *logsScreen) rowFor(seq uint64, e *domain.LogEntry) (viewRow, bool) {
	if !l.filter.LevelOK(e) {
		return viewRow{}, false
	}
	match := l.filter.Active() && l.filter.TextOK(e)
	if l.filter.Mode == domain.ModeFilter && l.filter.Active() && !match {
		return viewRow{}, false
	}
	return viewRow{seq: seq, level: e.Level, match: match}, true
}

// setTexts rebuilds the text filters from the committed ones and the
// input, keeping the last valid filter when the input is not.
func (l *logsScreen) setTexts() {
	texts := append([]domain.TextFilter(nil), l.committed...)
	l.inputErr = ""
	if in := l.input.String(); in != "" {
		f, err := domain.ParseTextFilter(in, l.regex)
		if err != nil {
			l.inputErr = err.Error()
			return
		}
		texts = append(texts, f)
	}
	l.filter.Texts = texts
	l.dirty = true
}

// schedule debounces the recompute of rows.
func (l *logsScreen) schedule() tea.Cmd {
	if l.pending {
		return nil
	}
	l.pending = true
	d := fastDebounce
	if l.slowFilter() {
		d = slowDebounce
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return filterTickMsg{screen: l} })
}

// slowFilter reports a regex that runs the regex engine on most lines.
func (l *logsScreen) slowFilter() bool {
	for _, t := range l.filter.Texts {
		if t.Regex {
			return true
		}
	}
	return false
}

// filterKey handles keys while the prompt is open.
func (l *logsScreen) filterKey(m *Model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		l.editing = false
		l.rebuild()
		return nil
	case "esc":
		l.editing = false
		l.input = lineEdit{text: []rune(l.before)}
		l.setTexts()
		l.rebuild()
		return nil
	case "ctrl+r":
		l.regex = !l.regex
		m.flash(onOff("regex", l.regex))
	case "ctrl+x":
		l.toggleMode(m)
	case "ctrl+a":
		if l.inputErr == "" && l.input.String() != "" {
			l.committed = l.filter.Texts
			l.input.Clear()
			m.flash(fmt.Sprintf("filter added (%d stacked)", len(l.committed)))
		}
	default:
		if !l.input.handle(k) {
			return nil
		}
	}
	l.setTexts()
	return l.schedule()
}

// startEditing opens the prompt on the last filter.
func (l *logsScreen) startEditing() {
	l.editing = true
	l.before = l.input.String()
}

// addFilter stacks f on the filters, the one of the prompt included, and
// puts the cursor on the entry keep when it still shows.
func (l *logsScreen) addFilter(m *Model, f domain.TextFilter, keep uint64) {
	l.committed = append(slices.Clone(l.filter.Texts), f)
	l.input.Clear()
	l.editing = false
	l.setTexts()
	l.rebuildFrom(keep)
	m.flash("filter " + f.String())
}

// clearLastFilter removes the filter being edited, else the last stacked.
func (l *logsScreen) clearLastFilter(m *Model) bool {
	switch {
	case l.input.String() != "":
		l.input.Clear()
	case len(l.committed) > 0:
		l.committed = l.committed[:len(l.committed)-1]
	default:
		return false
	}
	l.setTexts()
	l.rebuild()
	m.flash("filter cleared")
	return true
}

func (l *logsScreen) toggleMode(m *Model) {
	if l.filter.Mode == domain.ModeFilter {
		l.filter.Mode = domain.ModeHighlight
	} else {
		l.filter.Mode = domain.ModeFilter
	}
	l.dirty = true
	m.flash(strings.ToLower(l.filter.Mode.String()) + " mode")
}

func (l *logsScreen) cycleContext(m *Model) {
	i := 0
	for j, c := range contextSteps {
		if c == l.filter.Context {
			i = j
		}
	}
	l.filter.Context = contextSteps[(i+1)%len(contextSteps)]
	l.rebuild()
	m.flash(fmt.Sprintf("context %d lines", l.filter.Context))
}

func (l *logsScreen) setLevels(m *Model, levels domain.LevelSet) {
	l.filter.Levels = levels
	l.rebuild()
	m.flash("levels " + levelsLabel(levels))
}

// jumpMatch moves to the next or previous matching row, wrapping.
func (l *logsScreen) jumpMatch(m *Model, dir int) {
	n := l.shown()
	if n == 0 || !l.filter.Active() {
		return
	}
	cur := l.displayCursor()
	for step := 1; step <= n; step++ {
		i := ((cur+dir*step)%n + n) % n
		if r, ok := l.rowAt(i); ok && r.match {
			l.scroll(i - cur)
			if (dir > 0 && i < cur) || (dir < 0 && i > cur) {
				m.flash("search wrapped")
			}
			return
		}
	}
}

// matchPosition returns the index (1-based) of the cursor among matches
// and the number of matches.
func (l *logsScreen) matchPosition() (int, int) {
	pos, total, cur := 0, 0, l.displayCursor()
	for i := range l.shown() {
		if r, _ := l.rowAt(i); r.match {
			total++
			if i <= cur {
				pos = total
			}
		}
	}
	return pos, total
}

func levelsLabel(s domain.LevelSet) string {
	var names []string
	all := true
	for _, lv := range []domain.Level{domain.LevelError, domain.LevelWarn, domain.LevelInfo, domain.LevelDebug} {
		if s[lv] {
			names = append(names, lv.String())
		} else {
			all = false
		}
	}
	if all && s[domain.LevelUnknown] {
		return "all"
	}
	if s[domain.LevelUnknown] {
		names = append(names, "unknown")
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, " ")
}

func onOff(what string, on bool) string {
	if on {
		return what + " on"
	}
	return what + " off"
}

// filterSummary is the status-bar description of the text filters.
func (l *logsScreen) filterSummary() string {
	if !l.filter.Active() {
		return ""
	}
	var parts []string
	for _, t := range l.filter.Texts {
		parts = append(parts, t.String())
	}
	s := strings.ToLower(l.filter.Mode.String()) + " " + strings.Join(parts, " AND ")
	if l.filter.Mode == domain.ModeHighlight {
		pos, total := l.matchPosition()
		s += fmt.Sprintf(" · match %d of %d", pos, total)
	} else if l.filter.Context > 0 {
		s += fmt.Sprintf(" · context %d", l.filter.Context)
	}
	return s
}

// promptLine is the filter input line shown above the status bar.
func (l *logsScreen) promptLine(m *Model) string {
	t := m.opts.Theme
	bar := t.Status
	chip := func(on bool, s string) string {
		if on {
			return t.Chip.Render(s)
		}
		return t.Dim.Inherit(bar).Render(" " + strings.ToLower(s) + " ")
	}
	var b strings.Builder
	b.WriteString(t.Prompt.Render("/"))
	for _, c := range l.committed {
		b.WriteString(t.Dim.Inherit(bar).Render(" " + c.String() + " AND"))
	}
	b.WriteString(t.Bold.Inherit(bar).Render(" " + l.input.String() + "_"))
	b.WriteString(bar.Render("   "))
	b.WriteString(chip(true, l.filter.Mode.String()) + bar.Render(" "))
	b.WriteString(chip(l.regex, "REGEX") + bar.Render(" "))
	if l.inputErr != "" {
		b.WriteString(t.Bad.Inherit(bar).Render("  " + l.inputErr))
	} else if l.filter.Active() {
		pos, total := l.matchPosition()
		if l.filter.Mode == domain.ModeFilter {
			total = 0
			for _, r := range l.rows {
				if r.match {
					total++
				}
			}
			b.WriteString(bar.Render(fmt.Sprintf("  %d matching lines", total)))
		} else {
			b.WriteString(bar.Render(fmt.Sprintf("  match %d of %d", pos, total)))
		}
	}
	return b.String()
}

// highlight renders s with the filter's matches marked.
func (l *logsScreen) highlight(m *Model, s string, base ink) string {
	ranges := l.filter.Ranges(s)
	if len(ranges) == 0 {
		return base.paint(s)
	}
	hl := m.ink("highlight", func() lipgloss.Style { return m.opts.Theme.Highlight })
	marked := make([]bool, len(s))
	for _, r := range ranges {
		for i := r[0]; i < r[1]; i++ {
			marked[i] = true
		}
	}
	var b strings.Builder
	start := 0
	for i := 1; i <= len(s); i++ {
		if i == len(s) || marked[i] != marked[start] {
			k := base
			if marked[start] {
				k = hl
			}
			b.WriteString(k.paint(s[start:i]))
			start = i
		}
	}
	return b.String()
}
