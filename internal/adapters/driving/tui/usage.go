package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// usageSort orders the services of a namespace, and the namespaces by their
// totals.
type usageSort int

const (
	usageSortMemory usageSort = iota
	usageSortCPU
	usageSortMemLimit
	usageSortName
	usageSorts
)

var usageSortNames = [...]string{"memory", "cpu", "memory % of limit", "name"}

func (s usageSort) String() string { return usageSortNames[s] }

type usageRowKind int

const (
	usageNamespace usageRowKind = iota
	usageService
	usageUnlisted
)

// usageRow is one line of the usage table.
type usageRow struct {
	kind usageRowKind
	ns   string
	name string
	r    domain.Rollup
	// unassigned marks a workload no repository owns.
	unassigned bool
	err        error
	collapsed  bool
}

func (r usageRow) id() string { return fmt.Sprintf("%d|%s|%s", r.kind, r.ns, r.name) }

// usageScreen shows CPU and memory usage per namespace and service, from the
// metrics of the cluster.
type usageScreen struct {
	focus string // repository to put the cursor on at first
	mode  domain.ContainerMode

	gen    int
	cancel context.CancelFunc
	snap   *ports.UsageSnapshot
	err    error // the watch did not start

	tbl       table
	rows      []usageRow
	sort      usageSort
	filter    lineEdit
	editing   bool
	collapsed map[string]bool
	selected  string
	height    int
}

type (
	usageStartedMsg struct {
		screen *usageScreen
		gen    int
		ch     <-chan ports.UsageSnapshot
		err    error
	}
	usageMsg struct {
		screen *usageScreen
		gen    int
		snap   ports.UsageSnapshot
		ch     <-chan ports.UsageSnapshot
		closed bool
	}
)

func newUsageScreen(m *Model, focus string) *usageScreen {
	return &usageScreen{
		focus: focus, mode: domain.ContainersAll, collapsed: map[string]bool{},
		tbl: table{cols: []column{
			{title: "NAMESPACE / SERVICE", width: 19, flex: true},
			{title: "PODS", width: 5, right: true},
			{title: "CPU", width: 6, right: true},
			{title: "%REQ", width: 5, right: true, drop: 1},
			{title: "%LIM", width: 5, right: true},
			{title: "MEMORY", width: 7, right: true},
			{title: "%REQ", width: 5, right: true, drop: 1},
			{title: "%LIM", width: 5, right: true},
			{title: "PEAK POD", width: 8, drop: 2},
			{title: "NOTE", width: 20, fill: true, drop: 3},
		}},
	}
}

func (s *usageScreen) crumbs() []string { return []string{"services", "usage"} }

func (s *usageScreen) thresholds(m *Model) domain.Thresholds {
	if t := m.opts.UsageThresholds; t.Warn > 0 && t.Crit > 0 {
		return t
	}
	return domain.Thresholds{Warn: 80, Crit: 95}
}

// init opens the watch.
func (s *usageScreen) init(m *Model) tea.Cmd { return s.open(m) }

func (s *usageScreen) open(m *Model) tea.Cmd {
	s.close()
	s.gen++
	s.snap, s.err = nil, nil
	if m.opts.Usage == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.opts.Context)
	s.cancel = cancel
	gen, usage := s.gen, m.opts.Usage
	q := ports.UsageQuery{Env: domain.Env(m.env.Name), Containers: s.mode}
	return func() tea.Msg {
		ch, err := usage.Watch(ctx, q)
		return usageStartedMsg{screen: s, gen: gen, ch: ch, err: err}
	}
}

func (s *usageScreen) close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *usageScreen) wait(gen int, ch <-chan ports.UsageSnapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		return usageMsg{screen: s, gen: gen, snap: snap, ch: ch, closed: !ok}
	}
}

func (s *usageScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case usageStartedMsg:
		if msg.screen != s || msg.gen != s.gen {
			return false, nil
		}
		if msg.err != nil {
			s.err = msg.err
			return true, nil
		}
		return true, s.wait(msg.gen, msg.ch)
	case usageMsg:
		if msg.screen != s || msg.gen != s.gen || msg.closed {
			return false, nil
		}
		s.snap = &msg.snap
		s.rebuild()
		s.placeFocus()
		return true, s.wait(msg.gen, msg.ch)
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelDown:
			s.move(3)
		case tea.MouseWheelUp:
			s.move(-3)
		}
		return true, nil
	case tea.KeyPressMsg:
		if s.editing {
			return true, s.edit(msg)
		}
		return s.key(m, msg)
	}
	return false, nil
}

// placeFocus puts the cursor on the repository the screen was opened from,
// once its row exists; later snapshots keep the user's selection.
func (s *usageScreen) placeFocus() {
	if s.focus == "" || len(s.rows) == 0 {
		return
	}
	if i := slices.IndexFunc(s.rows, func(r usageRow) bool { return r.kind == usageService && r.name == s.focus }); i >= 0 {
		s.tbl.cursor, s.selected = i, s.rows[i].id()
	}
	s.focus = ""
}

func (s *usageScreen) move(delta int) {
	s.tbl.cursor = max(min(s.tbl.cursor+delta, len(s.rows)-1), 0)
	if len(s.rows) > 0 {
		s.selected = s.rows[s.tbl.cursor].id()
	}
}

func (s *usageScreen) edit(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		s.editing = false
	case "esc":
		s.editing = false
		s.filter.Clear()
		s.rebuild()
	default:
		if s.filter.handle(k) {
			s.rebuild()
		}
	}
	return nil
}

func (s *usageScreen) current() *usageRow {
	if s.tbl.cursor < 0 || s.tbl.cursor >= len(s.rows) {
		return nil
	}
	return &s.rows[s.tbl.cursor]
}

func (s *usageScreen) key(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	keys, key := m.opts.Keys, k.String()
	page := max(s.height-2, 1)
	switch {
	case keys.Is(key, ActDown):
		s.move(1)
	case keys.Is(key, ActUp):
		s.move(-1)
	case keys.Is(key, ActPageDown):
		s.move(page)
	case keys.Is(key, ActPageUp):
		s.move(-page)
	case keys.Is(key, ActTop):
		s.move(-len(s.rows))
	case keys.Is(key, ActBottom):
		s.move(len(s.rows))
	case keys.Is(key, ActPause): // space: fold the namespace
		s.fold(!s.isFolded(), true)
	case keys.Is(key, ActPanLeft):
		s.fold(true, false)
	case keys.Is(key, ActPanRight):
		s.fold(false, false)
	case keys.Is(key, ActOpen):
		r := s.current()
		switch {
		case r == nil:
		case r.kind == usageNamespace:
			s.fold(!r.collapsed, true)
		case r.kind == usageService:
			return true, m.push(newLogsScreen(m, r.name))
		}
	case keys.Is(key, ActFilter):
		s.editing = true
	case keys.Is(key, ActSort):
		s.sort = (s.sort + 1) % usageSorts
		s.rebuild()
		m.flash("sort " + s.sort.String())
	case keys.Is(key, ActAllContainers):
		if s.mode == domain.ContainersAll {
			s.mode = domain.ContainersApp
		} else {
			s.mode = domain.ContainersAll
		}
		m.flash("containers: " + s.containersLabel())
		return true, s.open(m)
	case keys.Is(key, ActRefresh):
		return true, s.open(m)
	case keys.Is(key, ActBack) && s.filter.String() != "":
		s.filter.Clear()
		s.rebuild()
	default:
		return false, nil
	}
	return true, nil
}

func (s *usageScreen) containersLabel() string {
	if s.mode == domain.ContainersAll {
		return "all (sidecars included)"
	}
	return "application only"
}

// isFolded reports whether the namespace of the cursor row is folded.
func (s *usageScreen) isFolded() bool {
	r := s.current()
	return r != nil && s.collapsed[r.ns]
}

// fold folds or unfolds the namespace of the cursor row; with the cursor on a
// service, a fold also moves the cursor to the namespace row, so it stays
// visible.
func (s *usageScreen) fold(folded, toggle bool) {
	r := s.current()
	if r == nil {
		return
	}
	if !toggle && r.kind != usageNamespace && !folded {
		return // unfolding from inside a namespace is already done
	}
	s.collapsed[r.ns] = folded
	s.selected = (usageRow{kind: usageNamespace, ns: r.ns, name: r.ns}).id()
	s.rebuild()
}

// rebuild computes the rows from the snapshot, the sort, the filter and the
// folded namespaces, and keeps the selected row selected.
func (s *usageScreen) rebuild() {
	s.rows = s.rows[:0]
	if s.snap == nil {
		return
	}
	q := strings.ToLower(s.filter.String())
	match := func(name string) bool { return q == "" || strings.Contains(strings.ToLower(name), q) }
	type group struct {
		row  usageRow
		kids []usageRow
	}
	var groups []group
	for _, ns := range s.snap.Namespaces {
		g := group{row: usageRow{kind: usageNamespace, ns: ns.Name, name: ns.Name, r: ns.Total, err: ns.Err, collapsed: s.collapsed[ns.Name]}}
		nsMatches := match(ns.Name)
		for _, svc := range ns.Services {
			if nsMatches || match(svc.Repo) {
				g.kids = append(g.kids, usageRow{kind: usageService, ns: ns.Name, name: svc.Repo, r: svc.Rollup, unassigned: svc.Unassigned})
			}
		}
		if q == "" && ns.Unlisted.Pods > 0 {
			g.kids = append(g.kids, usageRow{kind: usageUnlisted, ns: ns.Name, name: "(not in the catalog)", r: ns.Unlisted})
		}
		if !nsMatches && len(g.kids) == 0 {
			continue
		}
		slices.SortStableFunc(g.kids, func(a, b usageRow) int {
			if (a.kind == usageUnlisted) != (b.kind == usageUnlisted) { // the rest of the namespace last
				if a.kind == usageUnlisted {
					return 1
				}
				return -1
			}
			return s.compare(a, b)
		})
		groups = append(groups, g)
	}
	slices.SortStableFunc(groups, func(a, b group) int {
		if (a.row.err != nil) != (b.row.err != nil) { // namespaces without data last
			if a.row.err != nil {
				return 1
			}
			return -1
		}
		return s.compare(a.row, b.row)
	})
	for _, g := range groups {
		s.rows = append(s.rows, g.row)
		if !g.row.collapsed {
			s.rows = append(s.rows, g.kids...)
		}
	}
	if i := slices.IndexFunc(s.rows, func(r usageRow) bool { return r.id() == s.selected }); i >= 0 {
		s.tbl.cursor = i
	}
	s.tbl.cursor = max(min(s.tbl.cursor, len(s.rows)-1), 0)
	if len(s.rows) > 0 {
		s.selected = s.rows[s.tbl.cursor].id()
	}
}

// compare orders rows by the sort key, biggest first; ties by name.
func (s *usageScreen) compare(a, b usageRow) int {
	var x, y float64
	switch s.sort {
	case usageSortMemory:
		x, y = float64(a.r.Use.MemBytes), float64(b.r.Use.MemBytes)
	case usageSortCPU:
		x, y = float64(a.r.Use.CPUMilli), float64(b.r.Use.CPUMilli)
	case usageSortMemLimit:
		x, y = memLimitPct(a.r), memLimitPct(b.r)
	case usageSortName:
		return strings.Compare(a.name, b.name)
	}
	switch {
	case x > y:
		return -1
	case x < y:
		return 1
	}
	return strings.Compare(a.name, b.name)
}

// memLimitPct is the memory use as a share of the limit, -1 when undefined,
// so that rows without a limit sort last.
func memLimitPct(r domain.Rollup) float64 {
	if !r.LimitSet.Mem {
		return -1
	}
	pct, _ := domain.Pct(r.Use.MemBytes, r.Limit.MemBytes)
	return pct
}

const usageFootnote = "Averages over the metrics window: peaks between samples are not shown."

func (s *usageScreen) view(m *Model, w, h int) string {
	s.height = h
	t := &m.opts.Theme
	switch {
	case s.err != nil:
		return errorPanel(t, errTitle("Cannot read usage of", m.env.Name, s.err), s.err, usageFix(t, s.err),
			t.Dim.Render("press ")+t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry"), w, h)
	case s.snap == nil:
		return centered(t.Key.Render(m.spinner())+t.Dim.Render(" reading usage of "+m.env.Name), w, h)
	case s.snap.Err != nil:
		return errorPanel(t, errTitle("No usage for", m.env.Name, s.snap.Err), s.snap.Err, usageFix(t, s.snap.Err), m.retryKeys(s.snap.Err), w, h)
	case len(s.rows) == 0:
		msg := "no services in " + m.env.Name
		if s.filter.String() != "" {
			msg = fmt.Sprintf("nothing matches %q", s.filter.String())
		}
		return s.tbl.render(0, nil, 0, w, 1, t) + "\n" + centered(t.Dim.Render(msg), w, h-1)
	}
	th := s.thresholds(m)
	flex := 0
	for _, r := range s.rows {
		flex = max(flex, textWidth(usageName(r)))
	}
	get := func(i int) []cell { return s.cells(s.rows[i], t, th) }
	if h >= 6 {
		return s.tbl.render(len(s.rows), get, flex, w, h-1, t) + "\n" + " " + t.Dim.Render(usageFootnote)
	}
	return s.tbl.render(len(s.rows), get, flex, w, h, t)
}

// usageFix says what to do about a usage error.
func usageFix(t *Theme, err error) string {
	switch {
	case errors.Is(err, domain.ErrMetricsUnavailable):
		return "This cluster serves no pod metrics: check " + t.Key.Render("kubectl get apiservice v1beta1.metrics.k8s.io") + " (metrics-server)."
	case errors.Is(err, domain.ErrForbidden):
		return "Ask for read access to " + t.Key.Render("pods.metrics.k8s.io") + " in this namespace."
	}
	return errFix(t, err)
}

func usageName(r usageRow) string {
	switch r.kind {
	case usageNamespace:
		if r.collapsed {
			return "▸ " + r.name
		}
		return "▾ " + r.name
	case usageService:
		if r.unassigned {
			return "  " + r.name + " (no repo)"
		}
	}
	return "  " + r.name
}

// cells draws one row. The values are always printed: colour only adds to
// the words of the NOTE column.
func (s *usageScreen) cells(r usageRow, t *Theme, th domain.Thresholds) []cell {
	plain := lipgloss.NewStyle()
	name := cell{text: usageName(r)}
	switch r.kind {
	case usageNamespace:
		name.style = t.Bold
	case usageUnlisted:
		name.style = t.Dim
	}
	if r.err != nil {
		return []cell{name, {}, {}, {}, {}, {}, {}, {}, {}, {text: usageErrNote(r.err), style: t.Warn}}
	}
	ro := r.r
	reporting := ro.Reporting > 0
	dash := func(ok bool, text string, st lipgloss.Style) cell {
		if !ok || !reporting {
			return cell{text: "—", style: t.Dim}
		}
		return cell{text: text, style: st}
	}
	pct := func(use, ref int64) string {
		p, _ := domain.Pct(use, ref)
		return fmt.Sprintf("%.0f%%", p)
	}
	sev := func(use, ref int64, ok bool) lipgloss.Style {
		if p, defined := domain.Pct(use, ref); ok && defined {
			switch th.Severity(p) {
			case domain.SeverityCrit:
				return t.Bad
			case domain.SeverityWarn:
				return t.Warn
			}
		}
		return plain
	}
	pods := cell{text: fmt.Sprintf("%d/%d", ro.Reporting, ro.Pods)}
	if ro.Reporting < ro.Pods {
		pods.style = t.Warn
	}
	cpu, mem := cell{text: "—", style: t.Dim}, cell{text: "—", style: t.Dim}
	if reporting {
		cpu, mem = cell{text: formatCPU(ro.Use.CPUMilli)}, cell{text: formatMemory(ro.Use.MemBytes)}
	}
	peak := cell{}
	if ro.Reporting > 1 && ro.HottestPod != "" {
		peak = cell{text: podShortID(ro.HottestPod), style: sevStyle(t, th, ro.HottestMemPct)}
	}
	return []cell{
		name, pods, cpu,
		dash(ro.RequestSet.CPU, pct(ro.Use.CPUMilli, ro.Request.CPUMilli), plain),
		dash(ro.LimitSet.CPU, pct(ro.Use.CPUMilli, ro.Limit.CPUMilli), sev(ro.Use.CPUMilli, ro.Limit.CPUMilli, ro.LimitSet.CPU)),
		mem,
		dash(ro.RequestSet.Mem, pct(ro.Use.MemBytes, ro.Request.MemBytes), plain),
		dash(ro.LimitSet.Mem, pct(ro.Use.MemBytes, ro.Limit.MemBytes), sev(ro.Use.MemBytes, ro.Limit.MemBytes, ro.LimitSet.Mem)),
		peak,
		s.note(r, t, th),
	}
}

func sevStyle(t *Theme, th domain.Thresholds, pct float64) lipgloss.Style {
	switch th.Severity(pct) {
	case domain.SeverityCrit:
		return t.Bad
	case domain.SeverityWarn:
		return t.Warn
	}
	return lipgloss.NewStyle()
}

func usageErrNote(err error) string {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return "no access: pods.metrics.k8s.io"
	case errors.Is(err, domain.ErrMetricsUnavailable):
		return "no metrics served"
	}
	return "usage unavailable: " + errKind(err)
}

// note says in words what stands out in a row.
func (s *usageScreen) note(r usageRow, t *Theme, th domain.Thresholds) cell {
	ro := r.r
	var parts []string
	worst := domain.SeverityOK
	add := func(text string, sev domain.Severity) {
		parts = append(parts, text)
		worst = max(worst, sev)
	}
	if r.kind == usageUnlisted {
		return cell{text: "other pods of the namespace", style: t.Dim}
	}
	if ro.Reporting > 0 {
		if p, ok := domain.Pct(ro.Use.MemBytes, ro.Limit.MemBytes); ok && ro.LimitSet.Mem && th.Severity(p) > domain.SeverityOK {
			add(fmt.Sprintf("MEM %.0f%% of limit", p), th.Severity(p))
		} else if th.Severity(ro.HottestMemPct) > domain.SeverityOK && ro.Reporting > 1 {
			add(fmt.Sprintf("pod %s at %.0f%% of its memory limit", podShortID(ro.HottestPod), ro.HottestMemPct), th.Severity(ro.HottestMemPct))
		}
		if p, ok := domain.Pct(ro.Use.CPUMilli, ro.Limit.CPUMilli); ok && ro.LimitSet.CPU && th.Severity(p) > domain.SeverityOK {
			add(fmt.Sprintf("CPU %.0f%% of limit", p), th.Severity(p))
		}
		if r.kind == usageService || r.kind == usageNamespace && len(parts) == 0 {
			if ro.Provision(th) == domain.ProvisioningOver {
				add("over-provisioned", domain.SeverityOK)
			}
		}
		if !ro.LimitSet.Mem && r.kind == usageService {
			add("no limit", domain.SeverityOK)
		}
	}
	if ro.Reporting < ro.Pods {
		add(plural(ro.Pods-ro.Reporting, "pod")+" not reporting", domain.SeverityOK)
	}
	st := t.Dim
	switch worst {
	case domain.SeverityCrit:
		st = t.Bad
	case domain.SeverityWarn:
		st = t.Warn
	}
	return cell{text: strings.Join(parts, " · "), style: st}
}

func (s *usageScreen) statusLeft(m *Model) string {
	t := &m.opts.Theme
	bar := t.Status
	chip := t.Chip.Render("USAGE") + bar.Render("  ")
	if m.env.Production {
		chip = ""
	}
	if s.snap == nil {
		return chip + bar.Render("loading")
	}
	var parts []string
	if since := s.snap.StaleSince; !since.IsZero() {
		parts = append(parts, t.Warn.Inherit(bar).Render(fmt.Sprintf("stale since %s (%s)",
			since.In(time.Local).Format("15:04"), shortAge(m.opts.Now().Sub(since)))))
	}
	if at := s.snap.SampledAt; !at.IsZero() {
		sampled := "sampled " + shortAge(m.opts.Now().Sub(at)) + " ago"
		if w := s.snap.Window; w > 0 {
			sampled += " · " + shortAge(w) + " window"
		}
		parts = append(parts, bar.Render(sampled))
	}
	parts = append(parts, bar.Render("containers "+s.containersLabel()), bar.Render("sort "+s.sort.String()))
	if f := s.filter.String(); f != "" {
		parts = append(parts, bar.Render("filter "+f))
	}
	return chip + strings.Join(parts, bar.Render("  ·  "))
}

func (s *usageScreen) hints(m *Model) []hint {
	if s.editing {
		return []hint{{"enter", "keep"}, {"esc", "clear"}, {"ctrl+u", "erase"}}
	}
	return []hint{
		m.h(ActOpen, "fold / logs"), m.h(ActFilter, "filter"), m.h(ActSort, "sort"), m.h(ActAllContainers, "containers"),
		m.h(ActRefresh, "reload"), m.h(ActKeyBar, "keys"), m.h(ActBack, "back"), m.h(ActQuit, "quit"), m.h(ActHelp, "help"),
	}
}

func (s *usageScreen) fullHints(m *Model) []hint {
	if s.editing {
		return s.hints(m)
	}
	return []hint{
		m.pair(ActDown, ActUp, "move"), m.pair(ActTop, ActBottom, "top/bottom"), m.pair(ActPageDown, ActPageUp, "page"),
		m.h(ActOpen, "fold a namespace / open a service's logs"), m.h(ActPause, "fold or unfold"),
		m.pair(ActPanLeft, ActPanRight, "fold/unfold"), m.h(ActFilter, "filter by name"),
		m.h(ActSort, "sort: memory, cpu, memory % of limit, name"), m.h(ActAllContainers, "all containers / application only"),
		m.h(ActRefresh, "reload (restarts the history)"), m.h(ActBack, "back"), m.h(ActKeyBar, "keys"), m.h(ActQuit, "quit"), m.h(ActHelp, "help"),
	}
}

func (s *usageScreen) prompt(m *Model) string {
	if !s.editing && s.filter.String() == "" {
		return ""
	}
	t := &m.opts.Theme
	text := " " + s.filter.String()
	if s.editing {
		text += "_"
	}
	return t.Prompt.Render("/") + t.Bold.Inherit(t.Status).Render(text)
}

// formatCPU writes millicores as "250m", or as cores from 1000m: "1.9".
func formatCPU(milli int64) string {
	switch {
	case milli < 1000:
		return fmt.Sprintf("%dm", milli)
	case milli < 10_000:
		return fmt.Sprintf("%.2f", float64(milli)/1000)
	}
	return fmt.Sprintf("%.1f", float64(milli)/1000)
}

// formatMemory writes bytes in binary units: "512Mi", "7.2Gi".
func formatMemory(b int64) string {
	const ki = 1024
	switch {
	case b < ki*ki:
		return fmt.Sprintf("%dKi", b/ki)
	case b < 10*ki*ki*ki && b >= ki*ki*ki:
		return fmt.Sprintf("%.1fGi", float64(b)/(ki*ki*ki))
	case b < ki*ki*ki:
		return fmt.Sprintf("%dMi", b/(ki*ki))
	}
	return fmt.Sprintf("%dGi", b/(ki*ki*ki))
}
