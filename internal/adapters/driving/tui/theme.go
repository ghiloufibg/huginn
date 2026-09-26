package tui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Theme holds every style the UI uses, by role. Screens never pick colors
// themselves, so a theme change touches this file only.
type Theme struct {
	Name string

	Header, HeaderProd   lipgloss.Style
	Brand, BrandProd     lipgloss.Style
	EnvTag, EnvTagProd   lipgloss.Style
	Crumb, CrumbCurrent  lipgloss.Style
	Status, StatusProd   lipgloss.Style
	Chip, ChipProd       lipgloss.Style
	ChipLive, ChipPaused lipgloss.Style
	Key, Dim, Bold       lipgloss.Style
	Ok, Warn, Bad        lipgloss.Style
	Info                 lipgloss.Style
	Body                 lipgloss.Style
	Selected             lipgloss.Style
	TableHeader          lipgloss.Style
	Prompt               lipgloss.Style
	Popup, PopupTitle    lipgloss.Style
}

// ThemeNames lists the available themes.
var ThemeNames = []string{"light", "accessible", "classic", "none"}

// NewTheme returns the named theme. The light and accessible themes use the
// 16 base ANSI colors only, so the user's terminal palette decides the
// exact shades and any terminal can display them.
func NewTheme(name string, paintBackground bool) (Theme, error) {
	var t Theme
	switch name {
	case "light":
		t = ansiTheme(lipgloss.Black, lipgloss.White, lipgloss.Magenta)
	case "accessible":
		t = ansiTheme(lipgloss.BrightWhite, lipgloss.BrightBlack, lipgloss.Yellow)
		t.Selected = lipgloss.NewStyle().Background(lipgloss.Blue).Foreground(lipgloss.BrightWhite)
	case "classic":
		t = classicTheme()
	case "none":
		t = monoTheme()
	default:
		return Theme{}, fmt.Errorf("unknown theme %q (available: light, accessible, classic, none)", name)
	}
	t.Name = name
	if paintBackground && name == "light" {
		t.Body = t.Body.Background(lipgloss.BrightWhite).Foreground(lipgloss.Black)
	}
	return t, nil
}

// ansiTheme builds a 16-color theme; fg is the text color used on bars,
// bar the bar background and warn the warning color (magenta on light
// backgrounds where yellow is unreadable).
func ansiTheme(fg, bar, warn color.Color) Theme {
	white := lipgloss.BrightWhite
	chip := lipgloss.NewStyle().Bold(true).Foreground(white).Padding(0, 1)
	return Theme{
		Header:       lipgloss.NewStyle().Background(bar).Foreground(fg),
		HeaderProd:   lipgloss.NewStyle().Background(lipgloss.Red).Foreground(white),
		Brand:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Blue),
		BrandProd:    lipgloss.NewStyle().Bold(true).Foreground(white),
		EnvTag:       lipgloss.NewStyle().Bold(true).Background(lipgloss.Blue).Foreground(white).Padding(0, 1),
		EnvTagProd:   lipgloss.NewStyle().Bold(true).Background(white).Foreground(lipgloss.Red).Padding(0, 1),
		Crumb:        lipgloss.NewStyle().Foreground(lipgloss.BrightBlack),
		CrumbCurrent: lipgloss.NewStyle().Bold(true),
		Status:       lipgloss.NewStyle().Background(bar).Foreground(fg),
		StatusProd:   lipgloss.NewStyle().Background(bar).Foreground(lipgloss.Red),
		Chip:         chip.Background(lipgloss.Black),
		ChipProd:     chip.Background(lipgloss.Red),
		ChipLive:     chip.Background(lipgloss.Green),
		ChipPaused:   chip.Background(warn),
		Key:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Blue),
		Dim:          lipgloss.NewStyle().Foreground(lipgloss.BrightBlack),
		Bold:         lipgloss.NewStyle().Bold(true),
		Ok:           lipgloss.NewStyle().Foreground(lipgloss.Green),
		Warn:         lipgloss.NewStyle().Bold(true).Foreground(warn),
		Bad:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Red),
		Info:         lipgloss.NewStyle().Foreground(lipgloss.Blue),
		Body:         lipgloss.NewStyle(),
		Selected:     lipgloss.NewStyle().Background(lipgloss.Cyan).Foreground(lipgloss.Black),
		TableHeader:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.BrightBlack),
		Prompt:       lipgloss.NewStyle().Bold(true).Background(fg).Foreground(bar).Padding(0, 1),
		Popup:        lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(fg).Padding(0, 1),
		PopupTitle:   lipgloss.NewStyle().Bold(true),
	}
}

// classicTheme is a 256-color, k9s-inspired dark theme.
func classicTheme() Theme {
	t := ansiTheme(lipgloss.ANSIColor(252), lipgloss.ANSIColor(236), lipgloss.ANSIColor(214))
	t.Brand = t.Brand.Foreground(lipgloss.ANSIColor(81))
	t.Key = t.Key.Foreground(lipgloss.ANSIColor(81))
	t.EnvTag = t.EnvTag.Background(lipgloss.ANSIColor(33))
	t.Selected = lipgloss.NewStyle().Background(lipgloss.ANSIColor(24)).Foreground(lipgloss.ANSIColor(255))
	return t
}

// monoTheme uses attributes only (bold, reverse), as NO_COLOR asks.
func monoTheme() Theme {
	plain := lipgloss.NewStyle()
	bold := plain.Bold(true)
	rev := plain.Reverse(true)
	return Theme{
		Header: rev, HeaderProd: rev.Bold(true), Brand: bold, BrandProd: bold,
		EnvTag: bold.Padding(0, 1), EnvTagProd: bold.Underline(true).Padding(0, 1),
		Crumb: plain, CrumbCurrent: bold, Status: rev, StatusProd: rev.Bold(true),
		Chip: bold.Padding(0, 1), ChipProd: bold.Underline(true).Padding(0, 1),
		ChipLive: bold.Padding(0, 1), ChipPaused: bold.Padding(0, 1),
		Key: bold, Dim: plain.Faint(true), Bold: bold,
		Ok: plain, Warn: bold, Bad: bold.Underline(true), Info: plain, Body: plain,
		Selected: rev, TableHeader: bold, Prompt: rev.Padding(0, 1),
		Popup: plain.Border(lipgloss.NormalBorder()).Padding(0, 1), PopupTitle: bold,
	}
}

// statusStyle returns the style of a service status word. Failures are red
// and bold, degraded states use the warning color, rollouts the info
// color; the word itself always carries the meaning.
func (t Theme) statusStyle(s domain.ServiceStatus) lipgloss.Style {
	switch s {
	case domain.StatusCrashLoopBackOff, domain.StatusOOMKilled, domain.StatusImagePullBackOff:
		return t.Bad
	case domain.StatusDegraded:
		return t.Warn
	case domain.StatusPending, domain.StatusUnknown:
		return t.Dim.Bold(true)
	case domain.StatusProgressing:
		return t.Info
	default:
		return t.Ok
	}
}
