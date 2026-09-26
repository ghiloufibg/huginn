package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
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
}

func newServicesScreen(openRepo string) *servicesScreen {
	return &servicesScreen{
		openRepo: openRepo,
		tbl: table{cols: []column{
			{title: "REPO", width: 16, flex: true},
			{title: "WORKLOADS", width: 9, right: true, drop: 3},
			{title: "PODS", width: 5, right: true},
			{title: "STATUS", width: 18},
			{title: "RESTARTS", width: 8, right: true},
			{title: "LAST RESTART", width: 12, right: true, drop: 2},
			{title: "VERSION", width: 16, drop: 4},
			{title: "AGE", width: 5, right: true, drop: 1},
		}},
	}
}

func (s *servicesScreen) crumbs() []string { return []string{"services"} }

// rows returns the visible services: filtered by name, sorted, repositories
// before unassigned workloads.
func (s *servicesScreen) rows(m *Model) []domain.ServiceSummary {
	if m.snap == nil {
		return nil
	}
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
	rows := s.rows(m)
	s.sync(rows)
	switch msg := msg.(type) {
	case snapshotMsg:
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
	case keys.Is(key, ActBack) && s.filter.String() != "":
		s.filter.Clear()
	default:
		return false, nil
	}
	return true, nil
}

func (s *servicesScreen) view(m *Model, w, h int) string {
	s.height = h
	t := m.opts.Theme
	switch {
	case m.watchErr != nil:
		return centered(t.Bad.Render("Cannot watch "+m.env.Name+": "+errKind(m.watchErr))+"\n\n"+
			t.Dim.Render(m.watchErr.Error())+"\n\n"+t.Dim.Render("press ")+t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry"), w, h)
	case m.snap == nil:
		return centered(t.Dim.Render("connecting to "+m.env.Name+"…"), w, h)
	case m.snap.Err != nil && len(m.snap.Services) == 0:
		return centered(t.Bad.Render("Cannot reach "+m.env.Name+": "+errKind(m.snap.Err))+"\n\n"+
			t.Dim.Render(m.snap.Err.Error())+"\n\n"+t.Dim.Render("retrying automatically · press ")+
			t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry now"), w, h)
	}
	rows := s.rows(m)
	if len(rows) == 0 {
		msg := "no services in " + m.env.Name
		if s.filter.String() != "" {
			msg = fmt.Sprintf("no service matches %q", s.filter.String())
		}
		return s.tbl.render(nil, w, 1, t) + "\n" + centered(t.Dim.Render(msg), w, h-1)
	}
	s.sync(rows)
	cells := make([][]cell, len(rows))
	now := m.opts.Now()
	for i, r := range rows {
		cells[i] = s.cells(r, now, t)
	}
	return s.tbl.render(cells, w, h, t)
}

func (s *servicesScreen) cells(r domain.ServiceSummary, now time.Time, t Theme) []cell {
	name := cell{text: r.Repo, style: t.Bold}
	if r.Unassigned {
		name = cell{text: r.Repo + " (no repo)", style: t.Dim}
	}
	status := cell{text: r.Status.String(), style: t.statusStyle(r.Status)}
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
	return []cell{
		name,
		{text: strconv.Itoa(r.Workloads)},
		{text: fmt.Sprintf("%d/%d", r.ReadyPods, r.DesiredPods)},
		status,
		restarts,
		{text: since(now, r.LastRestart)},
		{text: r.Version},
		{text: since(now, r.Created)},
	}
}

func (s *servicesScreen) statusLeft(m *Model) string {
	t := m.opts.Theme
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
	return chip + bar.Render(strings.Join(parts, "  ·  "))
}

func (s *servicesScreen) hints(m *Model) []hint {
	if s.editing {
		return []hint{{"enter", "keep"}, {"esc", "clear"}}
	}
	return append([]hint{{m.label(ActOpen), "logs"}}, m.hintsFor(ActFilter, ActSort, ActSwitchEnv, ActHelp, ActQuit)...)
}

func (s *servicesScreen) prompt(m *Model) string {
	if !s.editing && s.filter.String() == "" {
		return ""
	}
	t := m.opts.Theme
	text := " " + s.filter.String()
	if s.editing {
		text += "_"
	}
	return t.Prompt.Render("/") + t.Bold.Inherit(t.Status).Render(text)
}
