package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// helpScreen lists the keys of the screen it was opened from, then the
// global keys, from the live keymap (remapped keys show as remapped).
type helpScreen struct {
	from    screen
	lines   []helpLine
	search  lineEdit
	editing bool
	offset  int
	height  int
}

type helpLine struct {
	section string // non-empty for a section title
	keys    string
	desc    string
}

// helpActions lists what each screen offers.
func helpActions(s screen) (string, []Action) {
	switch s.(type) {
	case *servicesScreen:
		return "Services", []Action{ActUp, ActDown, ActPageUp, ActPageDown, ActTop, ActBottom, ActOpen, ActFilter, ActSort, ActPreview, ActRefresh}
	case *logsScreen:
		return "Logs", []Action{
			ActUp, ActDown, ActPageUp, ActPageDown, ActTop, ActBottom, ActNextError, ActPrevError,
			ActFollow, ActPause, ActPreviousLogs, ActWindowNext, ActWindowPick, ActWindow1, ActWindow2, ActWindow3, ActWindow4, ActWindow5, ActWindow6, ActWindow7, ActWindowTail,
			ActFilter, ActFilterMode, ActRegex, ActAddFilter, ActContext, ActNextMatch, ActPrevMatch, ActLevels, ActErrorsOnly, ActWarnAndError, ActAllLevels,
			ActOpen, ActPodScope, ActPodSelector,
			ActOrder, ActCycleColumns, ActTimestamps, ActPodID, ActColumns, ActFocus, ActResetDisplay, ActWrap, ActPanLeft, ActPanRight, ActPanLeftHalf, ActPanRightHalf, ActFullscreen,
		}
	case *zoomScreen:
		return "Zoom", []Action{ActUp, ActDown, ActPageUp, ActPageDown, ActTop, ActNextEntry, ActPrevEntry, ActJSONView, ActOpen}
	case *podSelector:
		return "Pod selector", []Action{ActUp, ActDown, ActAllLevels, ActFilter, ActOpen}
	}
	return "", nil
}

var globalActions = []Action{ActHelp, ActKeyBar, ActBack, ActSwitchEnv, ActQuit}

func newHelpScreen(m *Model, from screen) *helpScreen {
	title, own := helpActions(from)
	h := &helpScreen{from: from}
	h.lines = append(h.lines, helpLine{section: strings.ToUpper(title)})
	h.lines = append(h.lines, h.grouped(m, own)...)
	h.lines = append(h.lines, helpLine{}, helpLine{section: "GLOBAL"})
	for _, a := range globalActions {
		h.lines = append(h.lines, h.line(m, a))
	}
	if _, ok := from.(*logsScreen); ok {
		h.lines = append(h.lines, helpLine{}, helpLine{section: "IN THE FILTER PROMPT"},
			helpLine{keys: "ctrl+r", desc: "regex on/off"}, helpLine{keys: "ctrl+x", desc: "filter or highlight"},
			helpLine{keys: "!", desc: "invert (as first character; \\! for a literal !)"},
			helpLine{keys: "ctrl+a", desc: "stack another filter (AND)"},
			helpLine{keys: "enter / esc", desc: "keep / cancel the edit"})
	}
	return h
}

// grouped orders actions by help group, one section per group.
func (h *helpScreen) grouped(m *Model, as []Action) []helpLine {
	var out []helpLine
	for _, g := range helpGroups {
		var group []helpLine
		for _, a := range as {
			if actions[a].group == g {
				group = append(group, h.line(m, a))
			}
		}
		if len(group) > 0 {
			out = append(out, helpLine{section: "  " + g})
			out = append(out, group...)
		}
	}
	return out
}

func (h *helpScreen) line(m *Model, a Action) helpLine {
	return helpLine{keys: strings.Join(m.opts.Keys.Keys(a), "  "), desc: actions[a].desc}
}

func (h *helpScreen) crumbs() []string {
	c := slices.Clone(h.from.crumbs())
	return append(c, "help")
}

func (h *helpScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	if h.editing {
		switch k.String() {
		case "enter":
			h.editing = false
		case "esc":
			h.editing = false
			h.search.Clear()
		default:
			h.search.handle(k)
		}
		h.offset = 0
		return true, nil
	}
	keys, key := m.opts.Keys, k.String()
	switch {
	case keys.Is(key, ActDown):
		h.offset++
	case keys.Is(key, ActUp):
		h.offset = max(h.offset-1, 0)
	case keys.Is(key, ActPageDown):
		h.offset += max(h.height-2, 1)
	case keys.Is(key, ActPageUp):
		h.offset = max(h.offset-max(h.height-2, 1), 0)
	case keys.Is(key, ActFilter):
		h.editing = true
	case keys.Is(key, ActHelp), keys.Is(key, ActBack):
		m.pop()
	case keys.Is(key, ActQuit):
		return false, nil
	}
	return true, nil
}

func (h *helpScreen) visible() []helpLine {
	q := strings.ToLower(h.search.String())
	if q == "" {
		return h.lines
	}
	var out []helpLine
	for _, l := range h.lines {
		if l.section == "" && (strings.Contains(strings.ToLower(l.desc), q) || strings.Contains(strings.ToLower(l.keys), q)) {
			out = append(out, l)
		}
	}
	return out
}

func (h *helpScreen) view(m *Model, w, height int) string {
	h.height = height
	t := m.opts.Theme
	lines := h.visible()
	width := 0
	for _, l := range lines {
		width = max(width, len([]rune(l.keys)))
	}
	var out []string
	for _, l := range lines {
		switch {
		case l.section != "":
			out = append(out, " "+t.Bold.Render(l.section))
		case l.keys == "" && l.desc == "":
			out = append(out, "")
		default:
			pad := strings.Repeat(" ", width-len([]rune(l.keys)))
			out = append(out, "     "+t.Key.Render(l.keys)+pad+"   "+l.desc)
		}
	}
	if len(out) == 0 {
		out = []string{t.Dim.Render(fmt.Sprintf(" no key matches %q", h.search.String()))}
	}
	h.offset = min(h.offset, max(len(out)-height, 0))
	return strings.Join(out[h.offset:], "\n")
}

func (h *helpScreen) statusLeft(m *Model) string {
	return m.opts.Theme.Chip.Render("HELP") + m.opts.Theme.Status.Render("  keys follow keymap in ui.yaml")
}

func (h *helpScreen) hints(m *Model) []hint {
	if h.editing {
		return []hint{{"enter", "keep"}, {"esc", "clear"}}
	}
	return []hint{m.pair(ActDown, ActUp, "scroll"), m.h(ActFilter, "search"), m.h(ActBack, "close")}
}

func (h *helpScreen) prompt(m *Model) string {
	if !h.editing && h.search.String() == "" {
		return ""
	}
	t := m.opts.Theme
	return t.Prompt.Render("search help") + t.Bold.Inherit(t.Status).Render(" "+h.search.String()+"_")
}
