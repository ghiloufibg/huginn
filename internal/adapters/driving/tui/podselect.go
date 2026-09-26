package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// podSelector is the fullscreen pod selection (S), for services with many
// pods: toggle pods, select all, or select by name pattern.
type podSelector struct {
	logs    *logsScreen
	names   []string
	chosen  map[string]bool
	cursor  int
	pattern lineEdit
	editing bool
}

func newPodSelector(l *logsScreen) *podSelector {
	s := &podSelector{logs: l, names: l.podNames(), chosen: map[string]bool{}}
	for _, n := range s.names {
		s.chosen[n] = l.inScope(n)
	}
	return s
}

func (s *podSelector) crumbs() []string { return []string{"services", s.logs.repo, "logs", "pods"} }

func (s *podSelector) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	if s.editing {
		switch k.String() {
		case "enter":
			s.editing = false
			q := strings.ToLower(s.pattern.String())
			for _, n := range s.names {
				s.chosen[n] = strings.Contains(strings.ToLower(n), q)
			}
		case "esc":
			s.editing = false
			s.pattern.Clear()
		default:
			s.pattern.handle(k)
		}
		return true, nil
	}
	keys, key := m.opts.Keys, k.String()
	switch {
	case keys.Is(key, ActDown):
		s.cursor = min(s.cursor+1, len(s.names)-1)
	case keys.Is(key, ActUp):
		s.cursor = max(s.cursor-1, 0)
	case key == "space" && len(s.names) > 0:
		n := s.names[s.cursor]
		s.chosen[n] = !s.chosen[n]
	case keys.Is(key, ActAllLevels): // "a": select all
		for _, n := range s.names {
			s.chosen[n] = true
		}
	case keys.Is(key, ActFilter):
		s.editing = true
		s.pattern.Clear()
	case keys.Is(key, ActOpen):
		s.apply()
		m.pop()
	default:
		return false, nil
	}
	return true, nil
}

// apply sets the logs scope; all pods selected (or none) means all.
func (s *podSelector) apply() {
	scope := map[string]bool{}
	for n, ok := range s.chosen {
		if ok {
			scope[n] = true
		}
	}
	if len(scope) == 0 || len(scope) == len(s.names) {
		scope = nil
	}
	s.logs.scope = scope
	s.logs.rebuild()
}

func (s *podSelector) view(m *Model, w, h int) string {
	t := m.opts.Theme
	out := []string{t.Bold.Render(fmt.Sprintf(" Select pods of %s", s.logs.repo)) + t.Dim.Render(fmt.Sprintf("   %d pods", len(s.names))), ""}
	for i, n := range s.names {
		box := "[ ]"
		if s.chosen[n] {
			box = "[x]"
		}
		status := ""
		for _, p := range s.logs.pods {
			if p.Pod.Name == n {
				st := domain.PodStatus(p.Pod)
				status = t.statusStyle(st).Render(st.String())
				if p.Terminated {
					status = t.Dim.Render("terminated")
				}
			}
		}
		line := fmt.Sprintf(" %s %s  ", box, n) + status
		if i == s.cursor {
			line = t.Selected.Render(fmt.Sprintf(" %s %s", box, n)) + "  " + status
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (s *podSelector) statusLeft(m *Model) string {
	n := 0
	for _, ok := range s.chosen {
		if ok {
			n++
		}
	}
	return m.opts.Theme.Chip.Render("PODS") + m.opts.Theme.Status.Render(fmt.Sprintf("  %d of %d selected", n, len(s.names)))
}

func (s *podSelector) hints(m *Model) []hint {
	if s.editing {
		return []hint{{"enter", "select matching"}, {"esc", "cancel"}}
	}
	return []hint{
		m.pair(ActDown, ActUp, "move"),
		{"space", "toggle"},
		m.h(ActAllLevels, "all"), m.h(ActFilter, "by name"),
		m.h(ActOpen, "apply"), m.h(ActBack, "cancel"), m.h(ActHelp, "help"),
	}
}

func (s *podSelector) prompt(m *Model) string {
	if !s.editing {
		return ""
	}
	t := m.opts.Theme
	return t.Prompt.Render("pods matching") + t.Bold.Inherit(t.Status).Render(" "+s.pattern.String()+"_")
}
