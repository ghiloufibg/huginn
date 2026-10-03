package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// keyBarSize is how much of the key bar is shown (docs/DECISIONS.md D-027).
type keyBarSize int

const (
	keyBarCompact keyBarSize = iota // one line, the most useful keys
	keyBarFull                      // two lines
	keyBarHidden                    // no line, maximum room
)

// ParseKeyBar converts the configured size ("compact", "full", "hidden").
func ParseKeyBar(s string) (keyBarSize, error) {
	switch s {
	case "", "compact":
		return keyBarCompact, nil
	case "full":
		return keyBarFull, nil
	case "hidden":
		return keyBarHidden, nil
	}
	return 0, fmt.Errorf("unknown key bar size %q (compact, full, hidden)", s)
}

func (k keyBarSize) String() string { return [...]string{"compact", "full", "hidden"}[k] }

// fullHinter is implemented by screens offering more keys in the full bar.
type fullHinter interface{ fullHints(m *Model) []hint }

// overlayHinter is implemented by popups with their own keys.
type overlayHinter interface{ hints(m *Model) []hint }

// h builds a hint from an action's current key.
func (m *Model) h(a Action, what string) hint { return hint{m.label(a), what} }

// pair builds a hint for two actions sharing a label ("n/N match").
func (m *Model) pair(a, b Action, what string) hint {
	return hint{m.label(a) + "/" + m.label(b), what}
}

// keyBarItems returns the hints of the current context: the popup's, or
// the top screen's, more of them in the full size.
func (m *Model) keyBarItems() []hint {
	if o, ok := m.popup.(overlayHinter); ok && m.popup != nil {
		return o.hints(m)
	}
	s := m.top()
	if f, ok := s.(fullHinter); ok && m.keyBar == keyBarFull {
		return f.fullHints(m)
	}
	return s.hints(m)
}

// keyBarLines renders the key bar: as many hints as fit, in order, on one
// or two lines; the help hint is kept last when present.
func (m *Model) keyBarLines() []string {
	lines := map[keyBarSize]int{keyBarCompact: 1, keyBarFull: 2}[m.keyBar]
	if lines == 0 {
		return nil
	}
	items := m.keyBarItems()
	var help *hint
	if n := len(items); n > 0 && items[n-1].what == "help" {
		last := items[n-1]
		help, items = &last, items[:n-1]
	}
	t := &m.opts.Theme
	render := func(h hint) string { return t.Key.Render(h.key) + " " + t.Dim.Render(h.what) }
	helpW := 0
	if help != nil {
		helpW = lipgloss.Width(render(*help)) + 2
	}
	var out []string
	cur := ""
	for i, it := range items {
		piece := render(it)
		room := m.width - 1
		if len(out) == lines-1 {
			room -= helpW // the last line keeps room for help
		}
		sep := ""
		if cur != "" {
			sep = "  "
		}
		if lipgloss.Width(cur)+len(sep)+lipgloss.Width(piece) > room {
			out = append(out, cur)
			if len(out) == lines {
				break
			}
			cur, sep = "", ""
		}
		cur += sep + piece
		if i == len(items)-1 {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" && len(out) < lines {
		out = append(out, cur)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	if help != nil {
		last := &out[len(out)-1]
		if *last != "" {
			*last += "  "
		}
		*last += render(*help)
	}
	for i, l := range out {
		out[i] = fitBlock(" "+l, m.width, 1)
	}
	for len(out) < lines {
		out = append(out, strings.Repeat(" ", m.width))
	}
	return out
}

// cycleKeyBar goes compact → full → hidden → compact.
func (m *Model) cycleKeyBar() {
	m.keyBar = (m.keyBar + 1) % (keyBarHidden + 1)
	m.flash("key bar " + m.keyBar.String())
}
