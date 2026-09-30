package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// servicesScreen is the home screen: one row per repository.
type servicesScreen struct {
	tbl      table
	sort     domain.SortKey
	filter   lineEdit
	editing  bool
	selected string // repo kept selected across updates and re-sorts
	openRepo string // --repo: open once it appears
	missing  string // --repo that did not appear
	height   int    // last body height, for paging
	preview  preview
	cacheKey rowsKey
	cache    []domain.ServiceSummary
	changes  statusChanges
}

func newServicesScreen(openRepo string) *servicesScreen {
	return &servicesScreen{
		openRepo: openRepo,
		tbl: table{cols: []column{
			{title: "REPO", width: 16, flex: true},
			{title: "WL", width: 2, right: true, drop: 4},
			{title: "PODS", width: 5, right: true},
			{title: "STATUS", width: 16},
			{title: "RST", width: 3, right: true},
			{title: "LAST", width: 4, right: true, drop: 3},
			{title: "VERSION", width: 15, drop: 5},
			{title: "AGE", width: 4, right: true, drop: 2},
			{title: "WHY", width: 24, fill: true, drop: 1},
		}},
	}
}

func (s *servicesScreen) crumbs() []string { return []string{"services"} }

// rows returns the visible services: filtered by name, sorted, repositories
// before unassigned workloads. The result is cached until the snapshot,
// the sort or the filter changes: a frame asks for it several times.
func (s *servicesScreen) rows(m *Model) []domain.ServiceSummary {
	if m.snap == nil {
		return nil
	}
	key := rowsKey{snap: m.snap, sort: s.sort, filter: s.filter.String()}
	if s.cacheKey == key {
		return s.cache
	}
	s.cacheKey, s.cache = key, s.sortedRows(m)
	return s.cache
}

type rowsKey struct {
	snap   *ports.CatalogSnapshot
	sort   domain.SortKey
	filter string
}

func (s *servicesScreen) sortedRows(m *Model) []domain.ServiceSummary {
	q := strings.ToLower(s.filter.String())
	var repos, orphans []domain.ServiceSummary
	for _, r := range m.snap.Services {
		if q != "" && !strings.Contains(strings.ToLower(r.Repo), q) {
			continue
		}
		if r.Unassigned {
			orphans = append(orphans, r)
		} else {
			repos = append(repos, r)
		}
	}
	domain.SortServices(repos, s.sort)
	domain.SortServices(orphans, s.sort)
	return append(repos, orphans...)
}

// sync points the cursor at the selected repository after rows changed.
func (s *servicesScreen) sync(rows []domain.ServiceSummary) {
	if i := slices.IndexFunc(rows, func(r domain.ServiceSummary) bool { return r.Repo == s.selected }); i >= 0 {
		s.tbl.cursor = i
	}
	s.tbl.cursor = max(min(s.tbl.cursor, len(rows)-1), 0)
	if len(rows) > 0 {
		s.selected = rows[s.tbl.cursor].Repo
	}
}

func (s *servicesScreen) move(rows []domain.ServiceSummary, delta int) {
	s.tbl.cursor += delta
	s.selected = ""
	s.tbl.cursor = max(min(s.tbl.cursor, len(rows)-1), 0)
	if len(rows) > 0 {
		s.selected = rows[s.tbl.cursor].Repo
	}
}

func (s *servicesScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case eventsRestMsg:
		return true, s.preview.rest(m, msg)
	case eventsMsg:
		s.preview.store(m, msg)
		return true, nil
	}
	used, cmd := s.handle(m, msg)
	return used, tea.Batch(cmd, s.preview.watch(m, s.current(m)))
}

// current returns the selected service, nil when there is none.
func (s *servicesScreen) current(m *Model) *domain.ServiceSummary {
	rows := s.rows(m)
	s.sync(rows)
	if len(rows) == 0 {
		return nil
	}
	return &rows[s.tbl.cursor]
}

func (s *servicesScreen) handle(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	rows := s.rows(m)
	s.sync(rows)
	switch msg := msg.(type) {
	case snapshotMsg:
		s.changes.observe(msg.snap, m.opts.Now())
		return false, s.openRequested(m)
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelDown:
			s.move(rows, 3)
		case tea.MouseWheelUp:
			s.move(rows, -3)
		}
	case tea.KeyPressMsg:
		if s.editing {
			return true, s.edit(msg)
		}
		return s.key(m, msg, rows)
	}
	return false, nil
}

// openRequested opens the --repo screen once the repo shows up.
func (s *servicesScreen) openRequested(m *Model) tea.Cmd {
	if s.openRepo == "" || m.snap == nil || !m.snap.Synced {
		return nil
	}
	repo := s.openRepo
	s.openRepo = ""
	if _, ok := m.findService(repo); !ok {
		s.missing = repo
		return nil
	}
	s.selected = repo
	return m.push(newLogsScreen(m, repo))
}

func (s *servicesScreen) edit(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		s.editing = false
	case "esc":
		s.editing = false
		s.filter.Clear()
	default:
		s.filter.handle(k)
	}
	return nil
}

func (s *servicesScreen) key(m *Model, k tea.KeyPressMsg, rows []domain.ServiceSummary) (bool, tea.Cmd) {
	keys, key := m.opts.Keys, k.String()
	page := max(s.height-2, 1)
	switch {
	case keys.Is(key, ActDown):
		s.move(rows, 1)
	case keys.Is(key, ActUp):
		s.move(rows, -1)
	case keys.Is(key, ActPageDown):
		s.move(rows, page)
	case keys.Is(key, ActPageUp):
		s.move(rows, -page)
	case keys.Is(key, ActTop):
		s.move(rows, -len(rows))
	case keys.Is(key, ActBottom):
		s.move(rows, len(rows))
	case keys.Is(key, ActOpen):
		if len(rows) > 0 {
			return true, m.push(newLogsScreen(m, rows[s.tbl.cursor].Repo))
		}
	case keys.Is(key, ActFilter):
		s.editing = true
	case keys.Is(key, ActSort):
		s.sort = s.sort.Next()
		m.flash("sort " + s.sort.String())
	case keys.Is(key, ActPreview):
		m.flash(s.preview.toggle())
	case keys.Is(key, ActBack) && s.filter.String() != "":
		s.filter.Clear()
	default:
		return false, nil
	}
	return true, nil
}

func (s *servicesScreen) view(m *Model, w, h int) string {
	s.height = h
	t := &m.opts.Theme
	switch {
	case m.watchErr != nil:
		return centered(t.Bad.Render("Cannot watch "+m.env.Name+": "+errKind(m.watchErr))+"\n\n"+
			t.Dim.Render(wrapErr(m.watchErr, w))+"\n\n"+t.Dim.Render("press ")+t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry"), w, h)
	case m.snap == nil:
		return centered(t.Key.Render(m.spinner())+t.Dim.Render(" connecting to "+m.env.Name), w, h)
	case m.snap.Err != nil && len(m.snap.Services) == 0:
		return centered(t.Bad.Render("Cannot reach "+m.env.Name+": "+errKind(m.snap.Err))+"\n\n"+
			t.Dim.Render(wrapErr(m.snap.Err, w))+"\n\n"+t.Dim.Render(errAdvice(m.snap.Err)+" · press ")+
			t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry now"), w, h)
	}
	rows := s.rows(m)
	if len(rows) == 0 {
		msg := "no services in " + m.env.Name
		if s.filter.String() != "" {
			msg = fmt.Sprintf("no service matches %q", s.filter.String())
		}
		return s.tbl.render(0, nil, 0, w, 1, t) + "\n" + centered(t.Dim.Render(msg), w, h-1)
	}
	s.sync(rows)
	now := m.opts.Now()
	get := func(i int) []cell { return s.cells(rows[i], now, t, m.opts.Filter) }
	flex := 0
	for _, r := range rows {
		n := len(r.Repo)
		if r.Unassigned {
			n += len(" (no repo)")
		}
		flex = max(flex, n)
	}
	s.tbl.titles = nil
	side, bottom := s.preview.place(w, h, len(rows))
	if titles := s.groupTitles(rows); titles != nil && len(rows)+len(titles)+1 <= h {
		// Group titles only use lines nothing else needs: they never
		// hide the preview.
		gs, gb := s.preview.place(w, h, len(rows)+len(titles))
		if gs > 0 || gb > 0 || (side == 0 && bottom == 0) {
			s.tbl.titles, side, bottom = titles, gs, gb
		}
	}
	s.preview.shown = side > 0 || bottom > 0
	switch {
	case side > 0:
		tbl := strings.Split(s.tbl.render(len(rows), get, flex, w-side, h, t), "\n")
		pane := strings.Split(s.preview.render(m, rows[s.tbl.cursor], side-1, h), "\n")
		for i := range tbl {
			tbl[i] += t.Dim.Render("│") + pane[i]
		}
		return strings.Join(tbl, "\n")
	case bottom > 0:
		s.height = h - bottom
		return s.tbl.render(len(rows), get, flex, w, h-bottom, t) + "\n" + s.preview.render(m, rows[s.tbl.cursor], w, bottom)
	}
	return s.tbl.render(len(rows), get, flex, w, h, t)
}

// groupTitles returns the status group titles by first row index when
// sorted by status: "FAILING 3", "HEALTHY 8"…; nil otherwise.
func (s *servicesScreen) groupTitles(rows []domain.ServiceSummary) map[int]string {
	if s.sort != domain.SortByStatus || len(rows) == 0 {
		return nil
	}
	name := func(r domain.ServiceSummary) string {
		if r.Unassigned {
			return "WITHOUT REPO"
		}
		return strings.ToUpper(statusGroups[statusGroup(r.Status)])
	}
	titles := map[int]string{}
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && name(rows[j]) == name(rows[i]) {
			j++
		}
		titles[i] = fmt.Sprintf("%s %d", name(rows[i]), j-i)
		i = j
	}
	return titles
}

func (s *servicesScreen) cells(r domain.ServiceSummary, now time.Time, t *Theme, filter domain.ContainerFilter) []cell {
	name := cell{text: r.Repo, style: t.Bold}
	if r.Unassigned {
		name = cell{text: r.Repo + " (no repo)", style: t.Dim}
		if len(r.WorkloadStates) > 0 && r.WorkloadStates[0].Standalone {
			// a pod group no workload owns: say what it is
			name.text = r.Repo + " (" + string(r.WorkloadStates[0].Ref.Kind) + ")"
		}
	}
	status := cell{text: r.Status.String(), style: t.statusStyle(r.Status)}
	if s.changes.recent(r.Repo, now) {
		// Just changed: stand out until the eye finds it, even after a
		// re-sort moved the row.
		status.style = status.style.Reverse(true)
	}
	if r.Status == domain.StatusUnknown && r.DesiredPods == 0 {
		status.text = "scaled to 0"
	}
	restarts := cell{text: strconv.Itoa(r.Restarts)}
	switch {
	case r.Restarts >= 5:
		restarts.style = t.Bad
	case r.Restarts > 0:
		restarts.style = t.Warn
	}
	why := cell{text: domain.Explain(r, filter, now), style: t.Dim}
	if r.Status < domain.StatusProgressing {
		why.style = lipgloss.NewStyle()
	}
	return []cell{
		name,
		{text: strconv.Itoa(r.Workloads)},
		{text: fmt.Sprintf("%d/%d", r.ReadyPods, r.DesiredPods)},
		status,
		restarts,
		{text: since(now, r.LastRestart)},
		{text: r.Version},
		{text: since(now, r.Created)},
		why,
	}
}

func (s *servicesScreen) statusLeft(m *Model) string {
	t := &m.opts.Theme
	bar := t.Status
	if m.env.Production {
		bar = t.StatusProd
	}
	chip := t.Chip.Render("SERVICES") + bar.Render("  ")
	if m.env.Production {
		chip = ""
	}
	if m.snap == nil {
		return chip + bar.Render("loading")
	}
	var parts []string
	if s.missing != "" {
		parts = append(parts, fmt.Sprintf("repo %q not found in %s", s.missing, m.env.Name))
	}
	parts = append(parts, statusCounts(s.rows(m)), "sort "+s.sort.String())
	if f := s.filter.String(); f != "" {
		parts = append(parts, "filter "+f)
	}
	if n := len(m.snap.NamespaceErrs); n > 0 {
		var nss []string
		for ns, err := range m.snap.NamespaceErrs {
			nss = append(nss, ns+": "+errKind(err))
		}
		slices.Sort(nss)
		parts = append(parts, "namespace "+strings.Join(nss, ", "))
	}
	parts = append(parts, m.snap.Warnings...)
	return chip + bar.Render(strings.Join(parts, "  ·  "))
}

func (s *servicesScreen) hints(m *Model) []hint {
	if s.editing {
		return []hint{{"enter", "keep"}, {"esc", "clear"}, {"ctrl+u", "erase"}}
	}
	return []hint{
		m.h(ActOpen, "logs"), m.h(ActFilter, "filter"), m.h(ActSort, "sort"), m.h(ActPreview, "preview"), m.h(ActRefresh, "resync"),
		m.h(ActSwitchEnv, "env"), m.h(ActKeyBar, "keys"), m.h(ActQuit, "quit"), m.h(ActHelp, "help"),
	}
}

func (s *servicesScreen) fullHints(m *Model) []hint {
	if s.editing {
		return s.hints(m)
	}
	return []hint{
		m.pair(ActDown, ActUp, "move"), m.pair(ActTop, ActBottom, "top/bottom"), m.pair(ActPageDown, ActPageUp, "page"),
		m.h(ActOpen, "logs"), m.h(ActFilter, "filter by name"), m.h(ActSort, "sort: status, name, restarts, age"),
		m.h(ActPreview, "preview on/off"), m.h(ActRefresh, "resync"), m.h(ActSwitchEnv, "switch env"), m.h(ActBack, "clear filter"),
		m.h(ActKeyBar, "keys"), m.h(ActQuit, "quit"), m.h(ActHelp, "help"),
	}
}

func (s *servicesScreen) prompt(m *Model) string {
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

// wrapErr wraps an error message to the screen, for the centered error
// views: the message names what to fix and must be read whole.
func wrapErr(err error, w int) string {
	return ansi.Wrap(err.Error(), max(w-8, 20), " ")
}
