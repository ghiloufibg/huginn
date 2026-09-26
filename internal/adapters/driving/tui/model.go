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
	// Sessions opens log sessions.
	Sessions ports.LogSession
	// Layouts draw entries, by the name of the format that decoded them;
	// Layout draws the others. Columns are the optional columns of all
	// layouts, in order, names merged (the columns picker and c use them).
	Layouts map[string]ports.LogLayout
	Layout  ports.LogLayout
	Columns []ports.ColumnSpec
	// Windows are the time-window presets (keys 1…7, then 0 for tail);
	// Window is the initial one.
	Windows []domain.TimeWindow
	Window  domain.TimeWindow
	// BufferLines bounds the entries kept per logs view.
	BufferLines int
	// Events reads a pod's recent events for the services preview; nil
	// hides the warnings section.
	Events ports.PodEvents
	// Filter tells application containers from sidecars.
	Filter domain.ContainerFilter
	// Repo, when set, is the repository to open directly (--repo).
	Repo string
	// Now returns the current time (injected for deterministic tests).
	Now func() time.Time
	// Context bounds every watch started by the UI.
	Context context.Context
	// KeyBar is the initial key bar size ("compact", "full", "hidden").
	KeyBar string
	// LogColumns are the columns shown before the message (time, pod,
	// level, thread, class); empty means automatic narrowing.
	LogColumns []string
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

// closer is implemented by screens holding resources (log sessions).
type closer interface{ close() }

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
	flashText     string
	flashID       int
	flashPending  bool
	keyBar        keyBarSize
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
	flashDoneMsg struct{ id int }
	snapshotMsg  struct {
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
	if len(o.Windows) == 0 {
		o.Windows = domain.DefaultWindowPresets(domain.DefaultTailLines)
	}
	if o.Window == (domain.TimeWindow{}) {
		o.Window = o.Windows[0]
	}
	if o.BufferLines <= 0 {
		o.BufferLines = 50000
	}
	kb, _ := ParseKeyBar(o.KeyBar) // validated by the composition root
	return &Model{opts: o, width: 80, height: 24, env: o.Env, keyBar: kb, stack: []screen{newServicesScreen(o.Repo)}}
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

// push opens a screen; its init command, if any, is returned.
func (m *Model) push(s screen) tea.Cmd {
	m.stack = append(m.stack, s)
	if i, ok := s.(interface{ init(*Model) tea.Cmd }); ok {
		return i.init(m)
	}
	return nil
}

// pop closes the top screen.
func (m *Model) pop() {
	if c, ok := m.top().(closer); ok {
		c.close()
	}
	m.stack = m.stack[:len(m.stack)-1]
}

// reset closes every screen but the services screen.
func (m *Model) reset(s screen) {
	for len(m.stack) > 0 {
		if c, ok := m.top().(closer); ok {
			c.close()
		}
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.stack = []screen{s}
}

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
		return m, tea.Batch(m.broadcast(msg), waitSnapshot(msg.gen, msg.ch))
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case flashDoneMsg:
		if msg.id == m.flashID {
			m.flashText = ""
		}
		return m, nil
	case tea.MouseWheelMsg:
		_, cmd := m.top().update(m, msg)
		return m, cmd
	}
	return m, m.broadcast(msg)
}

// broadcast delivers a non-input message to every screen of the stack, so
// a screen below the top (the logs under a zoom) keeps receiving its data.
func (m *Model) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range slices.Clone(m.stack) {
		_, cmd := s.update(m, msg)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// flash shows a short confirmation of a mode change in the status bar
// (docs/DECISIONS.md: no silent mode change).
func (m *Model) flash(text string) {
	m.flashText = text
	m.flashID++
	m.flashPending = true
}

// flashDuration is how long a confirmation stays in the status bar.
const flashDuration = 2 * time.Second

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	cmd := m.dispatchKey(k)
	if m.flashPending {
		m.flashPending = false
		id := m.flashID
		cmd = tea.Batch(cmd, tea.Tick(flashDuration, func(time.Time) tea.Msg { return flashDoneMsg{id: id} }))
	}
	return cmd
}

func (m *Model) dispatchKey(k tea.KeyPressMsg) tea.Cmd {
	if m.popup != nil {
		return m.popup.update(m, k)
	}
	if done, cmd := m.top().update(m, k); done {
		return cmd
	}
	key, keys := k.String(), m.opts.Keys
	switch {
	case keys.Is(key, ActHelp):
		if _, open := m.top().(*helpScreen); !open {
			return m.push(newHelpScreen(m, m.top()))
		}
	case keys.Is(key, ActQuit):
		m.stop()
		return tea.Quit
	case keys.Is(key, ActKeyBar):
		m.cycleKeyBar()
	case keys.Is(key, ActSwitchEnv):
		m.popup = newEnvPicker(m)
	case keys.Is(key, ActRefresh):
		m.resyncing = true
		return m.startWatch()
	case keys.Is(key, ActBack) && len(m.stack) > 1:
		m.pop()
	}
	return nil
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	for _, s := range m.stack {
		if c, ok := s.(closer); ok {
			c.close()
		}
	}
}

// switchEnv opens another environment from the services screen.
func (m *Model) switchEnv(e EnvInfo) tea.Cmd {
	if e.Name == m.env.Name {
		return nil
	}
	m.note = fmt.Sprintf("switched %s -> %s at %s", m.env.Name, e.Name, m.opts.Now().Format("15:04:05"))
	m.env, m.snap, m.watchErr = e, nil, nil
	m.reset(newServicesScreen(""))
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
	keyBar := m.keyBarLines()
	bodyH := m.height - 2 - len(keyBar)
	if prompt != "" {
		bodyH--
	}
	if bodyH < 1 {
		keyBar, bodyH = nil, bodyH+len(keyBar)
	}
	body := fitBlock(s.view(m, m.width, bodyH), m.width, bodyH)
	if m.popup != nil {
		body = placeOver(body, m.popup.view(m), m.width, bodyH)
	}
	parts := []string{m.header(), m.opts.Theme.Body.Render(body)}
	if prompt != "" {
		parts = append(parts, fill(m.opts.Theme.Status, prompt, "", m.width))
	}
	parts = append(parts, m.statusBar())
	return strings.Join(append(parts, keyBar...), "\n")
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
	bar, left := t.Status, ""
	if m.env.Production {
		bar, left = t.StatusProd, t.ChipProd.Render("PRODUCTION")+bar.Render(" ")
	}
	// A confirmation goes first so a narrow terminal never truncates it.
	if m.flashText != "" {
		left += t.Prompt.Render(m.flashText) + bar.Render(" ")
	}
	left += m.top().statusLeft(m)
	if m.note != "" && m.flashText == "" {
		left += bar.Render("  ·  ") + t.Dim.Inherit(bar).Render(m.note)
	}
	// Keys live in the key bar; when it is hidden, say how to get it back.
	right := ""
	if m.keyBar == keyBarHidden && lipgloss.Width(left)+24 <= m.width {
		right = t.Key.Inherit(bar).Render(m.label(ActKeyBar)) + bar.Render(" keys  ") +
			t.Key.Inherit(bar).Render(m.label(ActHelp)) + bar.Render(" help ")
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

// layout returns the layout drawing e.
func (m *Model) layout(e *domain.LogEntry) ports.LogLayout {
	if l, ok := m.opts.Layouts[e.Format]; ok {
		return l
	}
	return m.opts.Layout
}
