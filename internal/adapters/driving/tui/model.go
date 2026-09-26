package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// EnvInfo describes an environment shown in the header and the picker.
type EnvInfo struct {
	Name       string
	Context    string
	Namespaces []string
	Production bool
}

// Options configure the UI. The composition root fills them.
type Options struct {
	// Env is the environment to open; Envs lists all configured ones.
	Env     EnvInfo
	Envs    []EnvInfo
	Theme   Theme
	Keys    Keymap
	Source  string // "demo" or "kubernetes", shown in the header
	Catalog ports.ServiceCatalog
	// Filter tells application containers from sidecars.
	Filter domain.ContainerFilter
	// Repo, when set, is the repository to open directly (--repo).
	Repo string
	// Now returns the current time (injected for deterministic tests).
	Now func() time.Time
	// Context bounds every watch started by the UI.
	Context context.Context
}

// screen is one page of the UI. The root model routes messages to the
// top screen and draws the shared header and status bar around it.
type screen interface {
	// update handles a message and reports whether it consumed it.
	update(m *Model, msg tea.Msg) (bool, tea.Cmd)
	// view draws the body in exactly w×h cells.
	view(m *Model, w, h int) string
	// crumbs is the breadcrumb path of the screen.
	crumbs() []string
	// statusLeft is the left part of the status bar, after the mode chip.
	statusLeft(m *Model) string
	// hints are the key hints shown on the right of the status bar.
	hints(m *Model) []hint
	// prompt is an input line shown above the status bar ("" for none).
	prompt(m *Model) string
}

// overlay is a popup drawn over the current screen that takes all keys.
type overlay interface {
	update(m *Model, msg tea.KeyPressMsg) tea.Cmd
	view(m *Model) string
}

type hint struct{ key, what string }

// Model is the root Bubble Tea model.
type Model struct {
	opts          Options
	width, height int
	env           EnvInfo
	snap          *ports.CatalogSnapshot
	gen           int
	cancel        context.CancelFunc
	stack         []screen
	popup         overlay
	note          string
	resyncing     bool
	watchErr      error
}

// Messages from watch plumbing. gen identifies the watch so that messages
// of a cancelled watch are ignored.
type (
	watchStartedMsg struct {
		gen int
		ch  <-chan ports.CatalogSnapshot
		err error
	}
	snapshotMsg struct {
		gen    int
		snap   ports.CatalogSnapshot
		ch     <-chan ports.CatalogSnapshot
		closed bool
	}
)

// NewModel returns the root model.
func NewModel(o Options) *Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	if len(o.Envs) == 0 {
		o.Envs = []EnvInfo{o.Env}
	}
	return &Model{opts: o, width: 80, height: 24, env: o.Env, stack: []screen{newServicesScreen(o.Repo)}}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return m.startWatch() }

func (m *Model) startWatch() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if m.opts.Catalog == nil {
		return nil
	}
	m.gen++
	ctx, cancel := context.WithCancel(m.opts.Context)
	m.cancel = cancel
	gen, env, catalog := m.gen, domain.Env(m.env.Name), m.opts.Catalog
	return func() tea.Msg {
		ch, err := catalog.Watch(ctx, env)
		return watchStartedMsg{gen: gen, ch: ch, err: err}
	}
}

func waitSnapshot(gen int, ch <-chan ports.CatalogSnapshot) tea.Cmd {
	return func() tea.Msg {
		s, ok := <-ch
		return snapshotMsg{gen: gen, snap: s, ch: ch, closed: !ok}
	}
}

func (m *Model) top() screen { return m.stack[len(m.stack)-1] }

func (m *Model) push(s screen) { m.stack = append(m.stack, s) }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case watchStartedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.watchErr = msg.err
		if msg.err != nil {
			return m, nil
		}
		return m, waitSnapshot(msg.gen, msg.ch)
	case snapshotMsg:
		if msg.gen != m.gen || msg.closed {
			return m, nil
		}
		m.snap, m.resyncing = &msg.snap, false
		_, cmd := m.top().update(m, msg)
		return m, tea.Batch(cmd, waitSnapshot(msg.gen, msg.ch))
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}
	_, cmd := m.top().update(m, msg)
	return m, cmd
}

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	if m.popup != nil {
		return m.popup.update(m, k)
	}
	if done, cmd := m.top().update(m, k); done {
		return cmd
	}
	key, keys := k.String(), m.opts.Keys
	switch {
	case keys.Is(key, ActQuit):
		m.stop()
		return tea.Quit
	case keys.Is(key, ActSwitchEnv):
		m.popup = newEnvPicker(m)
	case keys.Is(key, ActRefresh):
		m.resyncing = true
		return m.startWatch()
	case keys.Is(key, ActBack) && len(m.stack) > 1:
		m.stack = m.stack[:len(m.stack)-1]
	}
	return nil
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// switchEnv opens another environment from the services screen.
func (m *Model) switchEnv(e EnvInfo) tea.Cmd {
	if e.Name == m.env.Name {
		return nil
	}
	m.note = fmt.Sprintf("switched %s -> %s at %s", m.env.Name, e.Name, m.opts.Now().Format("15:04:05"))
	m.env, m.snap, m.watchErr = e, nil, nil
	m.stack = []screen{newServicesScreen("")}
	return m.startWatch()
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "huginn · " + m.env.Name
	return v
}

func (m *Model) render() string {
	if m.width < 20 || m.height < 4 {
		return "huginn: terminal too small"
	}
	s := m.top()
	prompt := s.prompt(m)
	bodyH := m.height - 2
	if prompt != "" {
		bodyH--
	}
	body := fitBlock(s.view(m, m.width, bodyH), m.width, bodyH)
	if m.popup != nil {
		body = placeOver(body, m.popup.view(m), m.width, bodyH)
	}
	parts := []string{m.header(), m.opts.Theme.Body.Render(body)}
	if prompt != "" {
		parts = append(parts, fill(m.opts.Theme.Status, prompt, "", m.width))
	}
	return strings.Join(append(parts, m.statusBar()), "\n")
}

func (m *Model) header() string {
	t, e := m.opts.Theme, m.env
	bar, brand, tag := t.Header, t.Brand, t.EnvTag
	if e.Production {
		bar, brand, tag = t.HeaderProd, t.BrandProd, t.EnvTagProd
	}
	where := e.Context
	if where == "" {
		where = "current context"
	}
	if len(e.Namespaces) > 0 {
		where += " / " + strings.Join(e.Namespaces, ",")
	}
	crumbs := m.top().crumbs()
	path := t.Crumb.Inherit(bar).Render(strings.Join(crumbs[:len(crumbs)-1], " / "))
	if len(crumbs) > 1 {
		path += t.Crumb.Inherit(bar).Render(" / ")
	}
	current := t.CrumbCurrent
	if e.Production {
		current = current.Foreground(bar.GetForeground())
	}
	path += current.Inherit(bar).Render(crumbs[len(crumbs)-1])
	right := m.connection(bar) + bar.Render(" ")
	head := brand.Inherit(bar).Render(" huginn ") + tag.Render(strings.ToUpper(e.Name)) + bar.Render(" ")
	// The context is the first thing to shorten on narrow terminals: the
	// environment and the breadcrumb matter more.
	room := m.width - lipgloss.Width(head) - lipgloss.Width(path) - lipgloss.Width(right) - 6
	if room < 8 {
		where = ""
	} else if len(where) > room {
		where = ansi.Truncate(where, room, "…")
	}
	left := head
	if where != "" {
		left += t.Crumb.Inherit(bar).Render(where) + bar.Render("   ")
	}
	return fill(bar, left+path, right, m.width)
}

// connection describes the watch state on the right of the header.
func (m *Model) connection(bar lipgloss.Style) string {
	t := m.opts.Theme
	src := bar.Render(m.opts.Source + " · ")
	switch {
	case m.watchErr != nil:
		return src + t.Bad.Inherit(bar).Render("error: "+errKind(m.watchErr))
	case m.snap == nil || m.resyncing:
		return src + bar.Render("connecting…")
	case m.snap.Err != nil:
		return src + t.Bad.Inherit(bar).Render("error: "+errKind(m.snap.Err)) + bar.Render(" · retrying")
	default:
		state := "watching"
		if !m.snap.Synced {
			state = "partial"
		}
		return src + bar.Render(state+" · synced "+m.snap.UpdatedAt.In(time.Local).Format("15:04:05"))
	}
}

func (m *Model) statusBar() string {
	t := m.opts.Theme
	bar, chip, label := t.Status, t.Chip, strings.ToUpper(m.top().crumbs()[0])
	if m.env.Production {
		bar, chip, label = t.StatusProd, t.ChipProd, "PRODUCTION"
	}
	left := chip.Render(label) + bar.Render("  ") + m.top().statusLeft(m)
	if m.note != "" {
		left += bar.Render("  ·  ") + t.Dim.Inherit(bar).Render(m.note)
	}
	// Hints give way to state on narrow terminals: drop them from the end.
	hints := m.top().hints(m)
	var right string
	for n := len(hints); n >= 0; n-- {
		right = ""
		for _, h := range hints[:n] {
			right += t.Key.Inherit(bar).Render(h.key) + bar.Render(" "+h.what+"  ")
		}
		if lipgloss.Width(left)+lipgloss.Width(right)+2 <= m.width {
			break
		}
	}
	return fill(bar, left, right, m.width)
}

// label returns the display label of an action's first key.
func (m *Model) label(a Action) string { return m.opts.Keys.Label(a) }

// fill lays out left and right on one line of exactly width cells, padding
// with the bar style and truncating left first when space runs out.
func fill(bar lipgloss.Style, left, right string, width int) string {
	rw := lipgloss.Width(right)
	if rw >= width {
		return ansi.Truncate(right, width, "")
	}
	room := width - rw
	if lipgloss.Width(left) > room {
		left = ansi.Truncate(left, room-1, "…")
	}
	gap := room - lipgloss.Width(left)
	return left + bar.Render(strings.Repeat(" ", gap)) + right
}

// fitBlock clips or pads s to exactly h lines of exactly w cells.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		l = ansi.Truncate(l, w, "")
		lines[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	return strings.Join(lines, "\n")
}

// centered draws msg in the middle of a w×h block.
func centered(msg string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
}

// findService returns the row of repo in the latest snapshot.
func (m *Model) findService(repo string) (domain.ServiceSummary, bool) {
	if m.snap == nil {
		return domain.ServiceSummary{}, false
	}
	i := slices.IndexFunc(m.snap.Services, func(s domain.ServiceSummary) bool { return s.Repo == repo })
	if i < 0 {
		return domain.ServiceSummary{}, false
	}
	return m.snap.Services[i], true
}
