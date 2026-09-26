package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// EnvInfo describes the environment shown in the header.
type EnvInfo struct {
	Name       string
	Context    string
	Namespaces []string
	Production bool
}

// Options configure the UI. The composition root fills them.
type Options struct {
	Env    EnvInfo
	Theme  Theme
	Keys   Keymap
	Source string // "demo" or "kubernetes", shown in the header
	// Repo, when set, is the repository to open directly (--repo).
	Repo string
}

// Model is the root Bubble Tea model. In M0 it renders the application
// shell: header, placeholder body and status bar.
type Model struct {
	opts          Options
	width, height int
}

// NewModel returns the root model.
func NewModel(o Options) Model { return Model{opts: o, width: 80, height: 24} }

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		if m.opts.Keys.Is(msg.String(), ActQuit) {
			return m, tea.Quit
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "huginn · " + m.opts.Env.Name
	return v
}

func (m Model) render() string {
	if m.width < 20 || m.height < 3 {
		return "huginn: terminal too small"
	}
	body := m.body(m.height - 2)
	return strings.Join([]string{m.header(), body, m.statusBar()}, "\n")
}

func (m Model) header() string {
	t, e := m.opts.Theme, m.opts.Env
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
	crumb := t.CrumbCurrent
	if e.Production {
		crumb = crumb.Foreground(bar.GetForeground())
	}
	left := brand.Inherit(bar).Render(" huginn ") + tag.Render(strings.ToUpper(e.Name)) +
		bar.Render(" ") + t.Crumb.Inherit(bar).Render(where) + bar.Render("   ") + crumb.Inherit(bar).Render(breadcrumbOf(m.opts))
	right := bar.Render(m.opts.Source + " ")
	return fill(bar, left, right, m.width)
}

func breadcrumbOf(o Options) string {
	if o.Repo != "" {
		return "services / " + o.Repo
	}
	return "services"
}

func (m Model) body(h int) string {
	t := m.opts.Theme
	msg := t.Bold.Render("Services screen arrives in milestone M1.") + "\n\n" +
		t.Dim.Render("The shell, configuration, ports and demo cluster are in place.") + "\n" +
		t.Dim.Render("Press ") + t.Key.Render(m.opts.Keys.Label(ActQuit)) + t.Dim.Render(" to quit.")
	return t.Body.Render(fitBlock(lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, msg), m.width, h))
}

func (m Model) statusBar() string {
	t := m.opts.Theme
	bar, chip, label := t.Status, t.Chip, "SERVICES"
	if m.opts.Env.Production {
		bar, chip, label = t.StatusProd, t.ChipProd, "PRODUCTION"
	}
	left := chip.Render(label) + bar.Render("  read-only")
	right := t.Key.Inherit(bar).Render(m.opts.Keys.Label(ActHelp)) + bar.Render(" help  ") +
		t.Key.Inherit(bar).Render(m.opts.Keys.Label(ActQuit)) + bar.Render(" quit ")
	return fill(bar, left, right, m.width)
}

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
