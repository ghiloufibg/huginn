package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// podSelector is the fullscreen selection (S) of the pods and containers
// whose lines the logs show: the two lists combine (pods × containers),
// so "istio-proxy of every pod" and "one pod's one container" are both a
// few keys (docs/DECISIONS.md D-038).
type podSelector struct {
	logs    *logsScreen
	names   []string // pods
	chosen  map[string]bool
	ctrs    []ctrRow
	picked  map[string]bool // containers
	section int             // 0 pods, 1 containers
	cursor  [2]int
	pattern lineEdit
	editing bool
}

// ctrRow is a container name found in the repository's pods.
type ctrRow struct {
	name string
	role domain.ContainerRole
	pods int // pods that have it
}

func newPodSelector(m *Model, l *logsScreen) *podSelector {
	s := &podSelector{logs: l, names: l.podNames(), chosen: map[string]bool{}, picked: map[string]bool{}}
	for _, n := range s.names {
		s.chosen[n] = l.inScope(n)
	}
	s.ctrs = containerRows(m, l)
	streamed := l.streamedContainers()
	for _, c := range s.ctrs {
		if l.containerScope != nil {
			s.picked[c.name] = l.containerScope[c.name]
		} else {
			s.picked[c.name] = streamed[c.name]
		}
	}
	return s
}

// containerRows lists the containers of the repository's pods:
// application ones first, then sidecars, then init containers.
func containerRows(m *Model, l *logsScreen) []ctrRow {
	byName := map[string]*ctrRow{}
	for _, p := range l.pods {
		for _, c := range p.Pod.Containers {
			r, ok := byName[c.Name]
			if !ok {
				r = &ctrRow{name: c.Name, role: m.opts.Filter.Role(p.Pod, c)}
				byName[c.Name] = r
			}
			r.pods++
		}
	}
	rows := make([]ctrRow, 0, len(byName))
	for _, r := range byName {
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(a, b ctrRow) int {
		return cmp.Or(cmp.Compare(a.role, b.role), strings.Compare(a.name, b.name))
	})
	return rows
}

// streamedContainers are the containers the session follows.
func (l *logsScreen) streamedContainers() map[string]bool {
	out := map[string]bool{}
	for _, p := range l.pods {
		for _, c := range p.Containers {
			out[c] = true
		}
	}
	return out
}

func (s *podSelector) crumbs() []string {
	return []string{"services", s.logs.repo, "logs", "pods and containers"}
}

// items is the active section's names and choices.
func (s *podSelector) items() ([]string, map[string]bool) {
	if s.section == 1 {
		names := make([]string, len(s.ctrs))
		for i, c := range s.ctrs {
			names[i] = c.name
		}
		return names, s.picked
	}
	return s.names, s.chosen
}

func (s *podSelector) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	names, chosen := s.items()
	if s.editing {
		switch k.String() {
		case "enter":
			s.editing = false
			q := strings.ToLower(s.pattern.String())
			for _, n := range names {
				chosen[n] = strings.Contains(strings.ToLower(n), q)
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
	cur := &s.cursor[s.section]
	switch {
	case key == "tab" && len(s.ctrs) > 0:
		s.section = 1 - s.section
	case keys.Is(key, ActDown):
		*cur = min(*cur+1, len(names)-1)
	case keys.Is(key, ActUp):
		*cur = max(*cur-1, 0)
	case key == "space" && len(names) > 0:
		n := names[*cur]
		chosen[n] = !chosen[n]
	case keys.Is(key, ActAllLevels): // "a": select all of the section
		for _, n := range names {
			chosen[n] = true
		}
	case keys.Is(key, ActFilter):
		s.editing = true
		s.pattern.Clear()
	case keys.Is(key, ActOpen):
		cmd := s.apply(m)
		m.pop()
		return true, cmd
	default:
		return false, nil
	}
	return true, nil
}

// apply sets the logs scope. All pods selected (or none) means all; the
// containers selected become the container scope, nil when they are all
// the streamed ones. Choosing a container that is not streamed (a
// sidecar in app mode) switches to all containers, which reopens.
func (s *podSelector) apply(m *Model) tea.Cmd {
	l := s.logs
	scope := map[string]bool{}
	for n, ok := range s.chosen {
		if ok {
			scope[n] = true
		}
	}
	if len(scope) == 0 || len(scope) == len(s.names) {
		scope = nil
	}
	l.scope = scope

	picked := map[string]bool{}
	for n, ok := range s.picked {
		if ok {
			picked[n] = true
		}
	}
	mode := l.containerMode
	streamed := l.streamedContainers()
	for n := range picked {
		if !streamed[n] {
			mode = domain.ContainersAll
		}
	}
	if mode == domain.ContainersAll {
		streamed = map[string]bool{}
		for _, c := range s.ctrs {
			streamed[c.name] = true
		}
	}
	if len(picked) == 0 || maps.Equal(picked, streamed) {
		picked = nil
	}
	l.containerScope = picked
	if mode != l.containerMode {
		l.containerMode = mode
		m.flash("all containers (sidecars and init)")
		return l.open(m)
	}
	l.rebuild()
	return nil
}

func (s *podSelector) view(m *Model, w, h int) string {
	t := m.opts.Theme
	out := []string{t.Bold.Render(fmt.Sprintf(" Select pods and containers of %s", s.logs.repo)) +
		t.Dim.Render(fmt.Sprintf("   %s · %s", plural(len(s.names), "pod"), plural(len(s.ctrs), "container"))), ""}
	header := func(title string, section int) string {
		if s.section == section {
			return " " + t.TableHeader.Render(title)
		}
		return " " + t.Dim.Render(title)
	}
	box := func(on bool) string {
		if on {
			return "[x]"
		}
		return "[ ]"
	}
	line := func(section, i int, label, extra string) string {
		if s.section == section && i == s.cursor[section] {
			return t.Selected.Render(" "+label) + "  " + extra
		}
		return " " + label + "  " + extra
	}
	width := 0
	for _, n := range s.names {
		width = max(width, len(n))
	}
	for _, c := range s.ctrs {
		width = max(width, len(c.name))
	}
	out = append(out, header("PODS", 0))
	for i, n := range s.names {
		status := ""
		for _, p := range s.logs.pods {
			if p.Pod.Name == n {
				desc, st := podLabel(p.Pod)
				status = t.statusStyle(st).Render(desc)
				if p.Terminated {
					status = t.Dim.Render("terminated")
				}
			}
		}
		out = append(out, line(0, i, box(s.chosen[n])+" "+padRight(n, width), status))
	}
	if len(s.ctrs) > 0 {
		out = append(out, "", header("CONTAINERS", 1))
		for i, c := range s.ctrs {
			desc := c.role.String()
			if c.pods < len(s.names) {
				desc += " · " + plural(c.pods, "pod")
			}
			if c.role != domain.RoleApp && s.logs.containerMode == domain.ContainersApp {
				desc += " · not streamed (A)"
			}
			out = append(out, line(1, i, box(s.picked[c.name])+" "+padRight(c.name, width), t.Dim.Render(desc)))
		}
	}
	return strings.Join(out, "\n")
}

func (s *podSelector) statusLeft(m *Model) string {
	count := func(chosen map[string]bool) int {
		n := 0
		for _, ok := range chosen {
			if ok {
				n++
			}
		}
		return n
	}
	return m.opts.Theme.Chip.Render("SELECT") + m.opts.Theme.Status.Render(fmt.Sprintf("  %d of %d pods · %d of %d containers",
		count(s.chosen), len(s.names), count(s.picked), len(s.ctrs)))
}

func (s *podSelector) hints(m *Model) []hint {
	if s.editing {
		return []hint{{"enter", "select matching"}, {"esc", "cancel"}}
	}
	return []hint{
		m.pair(ActDown, ActUp, "move"),
		{"tab", "pods/containers"},
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
	what := "pods"
	if s.section == 1 {
		what = "containers"
	}
	return t.Prompt.Render(what+" matching") + t.Bold.Inherit(t.Status).Render(" "+s.pattern.String()+"_")
}
