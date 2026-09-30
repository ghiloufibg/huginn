package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// ink is a style reduced to the escape sequences around its text. Log
// lines are made of many small segments; lipgloss.Style.Render handles
// borders, margins and widths for each of them, which dominated the frame
// time. An ink is computed once per style and painting is a concatenation.
// Only inline styles (colors, bold, underline) may become inks.
type ink struct{ pre, post string }

const inkMark = "" // private-use rune, never in log text

func newInk(s lipgloss.Style) ink {
	r := s.Render(inkMark)
	i := strings.Index(r, inkMark)
	if i < 0 {
		return ink{}
	}
	return ink{pre: r[:i], post: r[i+len(inkMark):]}
}

func (k ink) paint(text string) string {
	if k.pre == "" && k.post == "" || text == "" {
		return text
	}
	return k.pre + text + k.post
}

// ink returns the cached ink of a style, keyed by a name unique per style.
// The theme never changes during a model's life, so the cache never goes
// stale.
func (m *Model) ink(key string, style func() lipgloss.Style) ink {
	if k, ok := m.inks[key]; ok {
		return k
	}
	if m.inks == nil {
		m.inks = map[string]ink{}
	}
	k := newInk(style())
	m.inks[key] = k
	return k
}

// segmentInk is segmentStyle as an ink.
func (m *Model) segmentInk(e *domain.LogEntry, r ports.Role) ink {
	t := &m.opts.Theme // not a copy: the theme is dozens of styles, and this runs per drawn segment
	switch r {
	case ports.RoleTimestamp:
		return m.ink("ts", func() lipgloss.Style { return t.Timestamp })
	case ports.RoleLevel:
		return m.ink(levelInkKey(e.Level), func() lipgloss.Style { return t.levelStyle(e.Level) })
	case ports.RoleThread:
		return m.ink("thread", func() lipgloss.Style { return t.Thread })
	case ports.RoleLogger:
		return m.ink("logger", func() lipgloss.Style { return t.Logger })
	case ports.RolePID:
		return m.ink("pid", func() lipgloss.Style { return t.PID })
	case ports.RoleDim:
		return m.dim()
	case ports.RoleMessage:
		if e.Level == domain.LevelError {
			return m.ink("errtext", func() lipgloss.Style { return t.ErrorText })
		}
	}
	return ink{}
}

func (m *Model) dim() ink { return m.ink("dim", func() lipgloss.Style { return m.opts.Theme.Dim }) }

func (m *Model) podInk(i int) ink {
	n := i % max(len(m.opts.Theme.Pods), 1)
	return m.ink(podInkKey(n), func() lipgloss.Style { return m.opts.Theme.podStyle(n) })
}

// Ink keys built once: concatenating them on every drawn row allocated.
var (
	levelInkKeys = func() (k [domain.LevelError + 1]string) {
		for i := range k {
			k[i] = "level:" + strconv.Itoa(i)
		}
		return k
	}()
	podInkKeys = func() (k [32]string) {
		for i := range k {
			k[i] = "pod:" + strconv.Itoa(i)
		}
		return k
	}()
)

func levelInkKey(l domain.Level) string {
	if l >= 0 && int(l) < len(levelInkKeys) {
		return levelInkKeys[l]
	}
	return "level:" + strconv.Itoa(int(l))
}

func podInkKey(n int) string {
	if n >= 0 && n < len(podInkKeys) {
		return podInkKeys[n]
	}
	return "pod:" + strconv.Itoa(n)
}
