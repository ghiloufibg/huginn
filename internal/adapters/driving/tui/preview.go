package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// The services preview shows the selected repository's workloads, pods,
// the recent warnings of its worst pod and its hidden containers, in the
// space the table leaves free (docs/DECISIONS.md D-028).

// previewMode is the user's choice for the preview (key p).
type previewMode int

const (
	previewAuto previewMode = iota // shown where it fits
	previewOff
	previewOn // shown, splitting the body when it does not fit
)

const (
	previewSideWidth = 200 // terminal width from which the preview sits on the right
	previewMinLines  = 8   // free lines under the table for a bottom preview
	previewLabel     = 11  // width of the section labels
	eventsRest       = 300 * time.Millisecond
	eventsTTL        = 30 * time.Second
	eventsTimeout    = 5 * time.Second
	maxWarnings      = 3
)

// podKey identifies the pod whose events are shown.
type podKey struct {
	env            domain.Env
	namespace, pod string
}

type eventsEntry struct {
	at      time.Time
	events  []domain.Event
	err     error
	loading bool
}

// eventsRestMsg fires once the cursor rested on a service; seq discards
// the rests of services the cursor already left.
type eventsRestMsg struct {
	key podKey
	seq int
}

// eventsMsg carries the events read for a pod.
type eventsMsg struct {
	key    podKey
	events []domain.Event
	err    error
}

type preview struct {
	mode   previewMode
	shown  bool   // whether the last frame drew the preview
	target podKey // worst pod of the selected service
	seq    int
	cache  map[podKey]eventsEntry
}

// toggle switches the preview off when it is visible, on otherwise.
func (p *preview) toggle() string {
	if p.shown {
		p.mode = previewOff
		return "preview off"
	}
	p.mode = previewOn
	return "preview on"
}

// place returns the side panel width or the bottom panel height for a
// w×h body whose table has n rows (0 when there is no such panel).
func (p *preview) place(w, h, n int) (side, bottom int) {
	switch {
	case p.mode == previewOff || h < 4:
		return 0, 0
	case w >= previewSideWidth:
		return w * 2 / 5, 0
	case h-(n+1) >= previewMinLines:
		return 0, h - (n + 1)
	case p.mode == previewOn:
		return 0, max(h/2, h-(n+1))
	}
	return 0, 0
}

// watch follows the selected service: when its worst pod changes, it
// schedules an events read after the cursor rests (never while scrolling).
func (p *preview) watch(m *Model, svc *domain.ServiceSummary) tea.Cmd {
	var key podKey
	if svc != nil {
		if pod, ok := domain.WorstPod(svc.Pods); ok {
			key = podKey{domain.Env(m.env.Name), pod.Namespace, pod.Name}
		}
	}
	if key == p.target {
		return nil
	}
	p.target = key
	p.seq++
	if key.pod == "" || m.opts.Events == nil || p.fresh(m, key) {
		return nil
	}
	msg := eventsRestMsg{key, p.seq}
	return tea.Tick(eventsRest, func(time.Time) tea.Msg { return msg })
}

func (p *preview) fresh(m *Model, key podKey) bool {
	e, ok := p.cache[key]
	return ok && (e.loading || m.opts.Now().Sub(e.at) < eventsTTL)
}

// rest reads the events of the pod the cursor rested on.
func (p *preview) rest(m *Model, msg eventsRestMsg) tea.Cmd {
	if msg.seq != p.seq || msg.key != p.target || p.fresh(m, msg.key) {
		return nil
	}
	if p.cache == nil {
		p.cache = map[podKey]eventsEntry{}
	}
	e := p.cache[msg.key]
	e.loading = true
	p.cache[msg.key] = e
	src, parent := m.opts.Events, m.opts.Context
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, eventsTimeout)
		defer cancel()
		evs, err := src.Recent(ctx, msg.key.env, msg.key.namespace, msg.key.pod)
		return eventsMsg{key: msg.key, events: evs, err: err}
	}
}

func (p *preview) store(m *Model, msg eventsMsg) {
	if p.cache == nil {
		p.cache = map[podKey]eventsEntry{}
	}
	now := m.opts.Now()
	for k, e := range p.cache { // a long session visits many pods: keep the fresh ones
		if !e.loading && now.Sub(e.at) >= eventsTTL {
			delete(p.cache, k)
		}
	}
	p.cache[msg.key] = eventsEntry{at: m.opts.Now(), events: msg.events, err: msg.err}
}

// render draws the preview of svc in exactly w×h cells.
func (p *preview) render(m *Model, svc domain.ServiceSummary, w, h int) string {
	t := &m.opts.Theme
	now := m.opts.Now()
	title := " " + t.Bold.Render(svc.Repo) + " "
	title = t.Dim.Render(" ─") + title + t.Dim.Render(strings.Repeat("─", max(w-ansi.StringWidth(title)-2, 0)))

	// A narrow panel (the side one) puts the labels on their own lines
	// to leave the content the whole width, and wraps the warnings.
	stacked := stackWrap(w) > 0
	sections := []struct {
		label string
		lines []string
	}{
		{"WORKLOADS", p.workloads(m, svc)},
		{"PODS", p.pods(m, svc, now)},
		{"WARNINGS", p.warnings(m, now, stackWrap(w))},
		{"SIDECARS", p.hidden(m, svc)},
	}
	alloc := make([]int, len(sections))
	avail := h - 1
	if stacked {
		for _, s := range sections {
			if len(s.lines) > 0 {
				avail--
			}
		}
	}
	give := func(i, upTo int) {
		for alloc[i] < min(upTo, len(sections[i].lines)) && avail > 0 {
			alloc[i]++
			avail--
		}
	}
	for i := range sections { // one line each first
		give(i, 1)
	}
	give(1, len(sections[1].lines))
	if stacked {
		give(2, len(sections[2].lines))
	} else {
		give(2, maxWarnings)
	}
	give(0, len(sections[0].lines))

	lines := []string{title}
	for i, s := range sections {
		if stacked && alloc[i] > 0 {
			lines = append(lines, " "+t.TableHeader.Render(s.label))
		}
		for j := range alloc[i] {
			label := "  "
			if !stacked {
				label = strings.Repeat(" ", previewLabel)
				if j == 0 {
					label = t.TableHeader.Render(padRight(s.label, previewLabel))
				}
			}
			line := s.lines[j]
			if j == alloc[i]-1 && alloc[i] < len(s.lines) {
				line = t.Dim.Render(fmt.Sprintf("+%d more", len(s.lines)-j))
			}
			lines = append(lines, " "+label+line)
		}
	}
	return fitBlock(strings.Join(lines, "\n"), w, h)
}

func (p *preview) workloads(m *Model, svc domain.ServiceSummary) []string {
	t := &m.opts.Theme
	var out []string
	for _, w := range svc.WorkloadStates {
		line := fmt.Sprintf("%s %s  %d/%d ready  %d/%d updated", w.Ref.Kind, t.Bold.Render(w.Ref.Name),
			w.ReadyReplicas, w.DesiredReplicas, w.UpdatedReplicas, w.DesiredReplicas)
		if w.Ref.Kind == domain.KindCronJob { // no replicas: its running jobs
			line = fmt.Sprintf("%s %s  %s", w.Ref.Kind, t.Bold.Render(w.Ref.Name), plural(w.DesiredReplicas, "active job"))
		}
		if v := domain.WorkloadVersion(w.Ref.Name, svc.Pods, m.opts.Filter); v != "" {
			line += "  " + v
		}
		out = append(out, line)
	}
	return out
}

func (p *preview) pods(m *Model, svc domain.ServiceSummary, now time.Time) []string {
	t := &m.opts.Theme
	if len(svc.Pods) == 0 {
		return []string{t.Dim.Render("no pod")}
	}
	type podRow struct {
		id, status, ready, restarts, last, node, age string
		st                                           domain.ServiceStatus
	}
	var rows []podRow
	var wid [6]int
	for _, pod := range svc.Pods {
		r := podRow{id: podShortID(pod.Name), node: pod.Node, age: since(now, pod.Created)}
		r.status, r.st = podLabel(pod)
		ready, restarts := 0, domain.PodRestarts(pod, m.opts.Filter)
		apps := m.opts.Filter.AppContainers(pod)
		for _, c := range apps {
			if c.Ready {
				ready++
			}
			if lt := c.LastTermination; lt != nil {
				r.last = fmt.Sprintf("last: %s exit %d, %s ago", lt.Reason, lt.ExitCode, shortAge(now.Sub(lt.At)))
			}
		}
		r.ready = fmt.Sprintf("%d/%d ready", ready, len(apps))
		r.restarts = plural(restarts, "restart")
		if r.node == "" {
			r.node = "-"
		}
		for i, v := range []string{r.id, r.status, r.ready, r.restarts, r.last, r.node} {
			wid[i] = max(wid[i], ansi.StringWidth(v))
		}
		rows = append(rows, r)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		line := padRight(r.id, wid[0]) + "  " + t.statusStyle(r.st).Render(padRight(r.status, wid[1])) + "  " +
			padRight(r.ready, wid[2]) + "  " + fmt.Sprintf("%*s", wid[3], r.restarts) + "  "
		if wid[4] > 0 {
			line += padRight(r.last, wid[4]) + "  "
		}
		out = append(out, line+t.Dim.Render(padRight(r.node, wid[5])+"  "+r.age))
	}
	return out
}

// stackWrap is the width warnings wrap at in a panel of width w, 0 when
// the panel is wide enough for one line per section item.
func stackWrap(w int) int {
	if w >= 100 {
		return 0
	}
	return max(w-5, 20)
}

func (p *preview) warnings(m *Model, now time.Time, wrap int) []string {
	t := &m.opts.Theme
	if m.opts.Events == nil || p.target.pod == "" {
		return nil
	}
	e, ok := p.cache[p.target]
	switch {
	case !ok || (e.loading && e.at.IsZero()):
		return []string{t.Key.Render(m.spinner()) + t.Dim.Render(" reading events of "+podShortID(p.target.pod))}
	case e.err != nil:
		return []string{t.Bad.Render("cannot read events: " + e.err.Error())}
	}
	var out []string
	for _, ev := range e.events {
		if ev.Type != "Warning" {
			continue
		}
		count := ""
		if ev.Count > 1 {
			count = t.Dim.Render(fmt.Sprintf(" (x%d)", ev.Count))
		}
		if wrap > 0 {
			out = append(out, fmt.Sprintf("%s  %s", since(now, ev.LastSeen), t.Warn.Render(ev.Reason))+count)
			for _, l := range strings.Split(ansi.Wordwrap(ev.Message, wrap, " "), "\n") {
				out = append(out, "  "+l)
			}
			continue
		}
		out = append(out, fmt.Sprintf("%4s  %s  %s", since(now, ev.LastSeen), t.Warn.Render(ev.Reason), ev.Message)+count)
	}
	if len(out) == 0 {
		return []string{t.Dim.Render("none for " + podShortID(p.target.pod))}
	}
	return out
}

func (p *preview) hidden(m *Model, svc domain.ServiceSummary) []string {
	var names []string
	for _, pod := range svc.Pods {
		apps := m.opts.Filter.AppContainers(pod)
		for _, c := range pod.Containers {
			if !slices.ContainsFunc(apps, func(a domain.Container) bool { return a.Name == c.Name }) && !slices.Contains(names, c.Name) {
				names = append(names, c.Name)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []string{m.opts.Theme.Dim.Render(strings.Join(names, ", "))}
}
