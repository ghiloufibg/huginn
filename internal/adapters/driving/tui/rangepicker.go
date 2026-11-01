package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// windowTarget is a screen that loads a time window and can take a range.
type windowTarget interface {
	// currentWindow is the window the screen shows now.
	currentWindow() domain.TimeWindow
	// applyWindow loads a window.
	applyWindow(m *Model, w domain.TimeWindow) tea.Cmd
}

// location is the time zone times are read and shown in: the machine's
// unless Options.Location says otherwise (tests).
func (m *Model) location() *time.Location {
	if m.opts.Location != nil {
		return m.opts.Location
	}
	return time.Local
}

const (
	rangeClock     = "2006-01-02 15:04:05"
	rangeClockZone = "MST -07:00"
)

// rangePicker is the popup (b) to type the bounds of an absolute range.
type rangePicker struct {
	target   windowTarget
	from, to lineEdit
	field    int // 0: from, 1: to
	window   domain.TimeWindow
	err      string
}

func newRangePicker(m *Model, target windowTarget) *rangePicker {
	p := &rangePicker{target: target}
	loc, w := m.location(), target.currentWindow()
	var from, to string
	switch {
	case w.IsRange():
		from = w.From.In(loc).Format(rangeClock)
		if !w.To.IsZero() {
			to = w.To.In(loc).Format(rangeClock)
		}
	case w.Since > 0:
		from = "now-" + w.Label()
	default:
		from = "now-1h"
	}
	p.from.text, p.to.text = []rune(from), []rune(to)
	p.validate(m)
	return p
}

func (p *rangePicker) edit() *lineEdit {
	if p.field == 1 {
		return &p.to
	}
	return &p.from
}

// validate reads both fields: the window to load, or why it cannot be.
func (p *rangePicker) validate(m *Model) {
	p.window, p.err = domain.TimeWindow{}, ""
	w, err := domain.ParseTimeRange(p.from.String(), p.to.String(), m.opts.Now(), m.location())
	if err != nil {
		p.err = err.Error()
		return
	}
	p.window = w
}

func (p *rangePicker) update(m *Model, k tea.KeyPressMsg) tea.Cmd {
	switch key := k.String(); key {
	case "esc":
		m.popup = nil
	case "enter":
		if p.err != "" {
			return nil
		}
		m.popup = nil
		return p.target.applyWindow(m, p.window)
	case "tab", "shift+tab", "down", "up":
		p.field = 1 - p.field
	default:
		if p.edit().handle(k) {
			p.validate(m)
		}
	}
	return nil
}

// rangeBounds formats the bounds of a range in loc for a status bar: the
// day only where it changes, "now" for an open end.
func rangeBounds(w domain.TimeWindow, loc *time.Location) string {
	from := w.From.In(loc)
	clock := "15:04"
	if from.Second() != 0 || (!w.To.IsZero() && w.To.In(loc).Second() != 0) {
		clock = "15:04:05"
	}
	day := "Jan 2 "
	start := from.Format(day + clock)
	if w.To.IsZero() {
		return start + " → now"
	}
	to := w.To.In(loc)
	if sameDay(from, to) {
		return start + " → " + to.Format(clock)
	}
	return start + " → " + to.Format(day+clock)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// preview tells what the fields resolve to, in the zone they are read in.
func (p *rangePicker) preview(m *Model) string {
	loc, w := m.location(), p.window
	from := w.From.In(loc).Format(rangeClock)
	if w.To.IsZero() {
		return from + " → now, then following  " + w.From.In(loc).Format(rangeClockZone)
	}
	to := w.To.In(loc)
	end := to.Format(rangeClock)
	if sameDay(w.From.In(loc), to) {
		end = to.Format("15:04:05")
	}
	return from + " → " + end + "  " + to.Format(rangeClockZone) + "  ·  " + shortAge(w.To.Sub(w.From))
}

func (p *rangePicker) view(m *Model) string {
	t := &m.opts.Theme
	field := func(i int, name string, e *lineEdit) string {
		label := t.Dim.Render(name)
		text := e.String()
		if i == p.field {
			return t.Key.Render(name) + "  " + t.Bold.Render(text+"_")
		}
		if text == "" && i == 1 {
			text = t.Dim.Render("(empty: until now, following)")
		}
		return label + "  " + text
	}
	status := t.Dim.Render(p.preview(m))
	if p.err != "" {
		status = t.Bad.Render(p.err)
	}
	help := t.Key.Render("tab") + t.Dim.Render(" field  ") + t.Key.Render("enter") + t.Dim.Render(" load  ") +
		t.Key.Render("ctrl+u") + t.Dim.Render(" clear  ") + t.Key.Render("esc") + t.Dim.Render(" cancel")
	forms := t.Dim.Render("2026-11-01 14:30 · 14:30 · now-90m · 2026-11-01T14:30:00Z")
	lines := []string{
		t.PopupTitle.Render("Time range") + t.Dim.Render("  "+m.location().String()),
		"",
		field(0, "From", &p.from),
		field(1, "To  ", &p.to),
		"",
		status,
		"",
		forms,
		help,
	}
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	return t.Popup.Render(strings.Join(lines, "\n"))
}
