package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Two timers keep the screen alive without wasting frames: a fast one
// animates the spinner while something is being waited for, a slow one
// refreshes what changes with the clock (the live rate, relative
// timestamps) while a stream is followed. Each runs only while needed and
// at most one of each is pending.

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	animEvery  = 100 * time.Millisecond
	clockEvery = time.Second
)

type (
	animMsg  struct{}
	clockMsg struct{}
)

// busy is implemented by screens that wait for something (the spinner
// runs); ticking by screens showing clock-dependent data.
type (
	busyScreen    interface{ busy(m *Model) bool }
	tickingScreen interface{ ticking(m *Model) bool }
)

// spinner returns the current spinner frame.
func (m *Model) spinner() string { return spinnerFrames[m.frame%len(spinnerFrames)] }

func (m *Model) busy() bool {
	if m.watchErr == nil && m.opts.Catalog != nil && (m.snap == nil || m.resyncing) {
		return true
	}
	s, ok := m.top().(busyScreen)
	return ok && s.busy(m)
}

func (m *Model) ticking() bool {
	s, ok := m.top().(tickingScreen)
	return ok && s.ticking(m)
}

// schedule starts the timers that are needed and not pending.
func (m *Model) schedule() tea.Cmd {
	var cmds []tea.Cmd
	if !m.animPending && m.busy() {
		m.animPending = true
		cmds = append(cmds, tea.Tick(animEvery, func(time.Time) tea.Msg { return animMsg{} }))
	}
	if !m.clockPending && m.ticking() {
		m.clockPending = true
		cmds = append(cmds, tea.Tick(clockEvery, func(time.Time) tea.Msg { return clockMsg{} }))
	}
	return tea.Batch(cmds...)
}

// tick handles the timers; a redraw follows every message.
func (m *Model) tick(msg tea.Msg) (bool, tea.Cmd) {
	switch msg.(type) {
	case animMsg:
		m.animPending = false
		m.frame++
	case clockMsg:
		m.clockPending = false
	default:
		return false, nil
	}
	return true, m.schedule()
}

// busy: the logs screen waits for its history.
func (l *logsScreen) busy(*Model) bool { return l.loading && l.err == nil }

// ticking: the live rate decays and relative times age while following.
func (l *logsScreen) ticking(*Model) bool {
	return (l.live && l.follow && !l.paused) || l.timestamps != 0
}

// busy: the services preview reads events.
func (s *servicesScreen) busy(*Model) bool {
	e, ok := s.preview.cache[s.preview.target]
	return ok && e.loading
}
