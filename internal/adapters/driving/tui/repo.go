package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// repoScreen shows a repository's pods. It is the entry point of the logs
// screen, which replaces its body in M2.
type repoScreen struct {
	repo string
	tbl  table
}

func newRepoScreen(repo string) *repoScreen {
	return &repoScreen{repo: repo, tbl: table{cols: []column{
		{title: "POD", width: 30, flex: true},
		{title: "STATUS", width: 18},
		{title: "READY", width: 5},
		{title: "RESTARTS", width: 8, right: true},
		{title: "VERSION", width: 16, drop: 2},
		{title: "NODE", width: 22, drop: 1},
		{title: "AGE", width: 5, right: true},
	}}}
}

func (r *repoScreen) crumbs() []string { return []string{"services", r.repo} }

func (r *repoScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	switch key := k.String(); {
	case m.opts.Keys.Is(key, ActDown):
		r.tbl.cursor++
	case m.opts.Keys.Is(key, ActUp):
		r.tbl.cursor = max(r.tbl.cursor-1, 0)
	default:
		return false, nil
	}
	return true, nil
}

func (r *repoScreen) view(m *Model, w, h int) string {
	t := m.opts.Theme
	svc, ok := m.findService(r.repo)
	if !ok {
		return centered(t.Dim.Render(fmt.Sprintf("%s is not in %s any more", r.repo, m.env.Name)), w, h)
	}
	filter := m.opts.Filter
	now := m.opts.Now()
	cells := make([][]cell, len(svc.Pods))
	for i, p := range svc.Pods {
		st := domain.PodStatus(p)
		app, _ := filter.PrimaryApp(p)
		ready := "no"
		if app.Ready {
			ready = "yes"
		}
		cells[i] = []cell{
			{text: p.Name, style: t.Bold},
			{text: st.String(), style: t.statusStyle(st)},
			{text: ready},
			{text: strconv.Itoa(p.Restarts())},
			{text: domain.ImageTag(app.Image)},
			{text: p.Node, style: t.Dim},
			{text: since(now, p.Created)},
		}
	}
	title := t.Bold.Render(fmt.Sprintf(" %s  ", r.repo)) + t.Dim.Render(fmt.Sprintf("%d pods · %d workloads · %s", len(svc.Pods), svc.Workloads, svc.Status))
	var hidden []string
	for _, p := range svc.Pods {
		for _, c := range p.Containers {
			if !filter.IsApp(c, p.OwnerName) && !contains(hidden, c.Name) {
				hidden = append(hidden, c.Name)
			}
		}
	}
	foot := t.Dim.Render(" hidden containers: " + strings.Join(hidden, ", "))
	if len(hidden) == 0 {
		foot = ""
	}
	tableH := max(h-5, 2)
	body := []string{title, "", r.tbl.render(cells, w, tableH, t), foot, t.Dim.Render(" Log streaming for this repository arrives in milestone M2.")}
	return strings.Join(body, "\n")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (r *repoScreen) statusLeft(m *Model) string {
	bar := m.opts.Theme.Status
	if m.env.Production {
		bar = m.opts.Theme.StatusProd
	}
	return bar.Render(r.repo + " · pods of application workloads only")
}

func (r *repoScreen) hints(m *Model) []hint {
	return []hint{{m.label(ActBack), "back"}, {m.label(ActSwitchEnv), "env"}, {m.label(ActQuit), "quit"}}
}

func (r *repoScreen) prompt(*Model) string { return "" }
