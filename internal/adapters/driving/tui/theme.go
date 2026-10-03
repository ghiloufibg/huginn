package tui

import (
	"fmt"
	"image/color"
	"strings"

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
	// Log line roles.
	Timestamp, Thread, Logger, PID lipgloss.Style
	ErrorText, Stack               lipgloss.Style
	// Highlight marks text-filter matches (with underline, so it is
	// visible without color).
	Highlight lipgloss.Style
	// Pods are the per-pod identity colors, used with the pod's short id
	// (the id itself, not the color, identifies the pod).
	Pods []lipgloss.Style
}

// ThemeNames lists the available themes. "auto" is resolved to light or
// dark from the terminal's background (bootstrap, then the TUI when the
// terminal answers).
var ThemeNames = []string{"auto", "light", "dark", "accessible", "classic", "none"}

// NewTheme returns the named theme. light and dark take their colors from
// the fixed 256-color palette (D-051): the 16 base ANSI colors differ too
// much between terminals (ANSI white is #e5e5e5 in xterm and #555555 in
// VS Code's light terminal) for backgrounds and dim text to stay readable.
// accessible keeps the 16 base colors, so the user's palette decides.
func NewTheme(name string, paintBackground bool) (Theme, error) {
	var t Theme
	switch name {
	case "light":
		t = paletteTheme(lightPalette)
	case "dark":
		t = paletteTheme(darkPalette)
	case "accessible":
		t = ansiTheme(lipgloss.BrightWhite, lipgloss.BrightBlack, lipgloss.Yellow)
		t.Selected = lipgloss.NewStyle().Background(lipgloss.Blue).Foreground(lipgloss.BrightWhite)
	case "classic":
		t = classicTheme()
	case "none":
		t = monoTheme()
	default:
		return Theme{}, fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(ThemeNames, ", "))
	}
	t.Name = name
	if paintBackground {
		switch name {
		case "light":
			t.Body = t.Body.Background(lightPalette.bg).Foreground(lightPalette.text)
		case "dark":
			t.Body = t.Body.Background(darkPalette.bg).Foreground(darkPalette.text)
		}
	}
	return t, nil
}

// palette is the colors of a theme for one kind of terminal background.
// Every text color is chosen for its contrast on that background (checked
// by TestThemeContrast), every background for the text drawn on it.
type palette struct {
	bg, text                 color.Color // the background assumed, the main text
	dim, bar, barText, crumb color.Color // secondary text; header and status bars
	chip, chipText           color.Color // mode chips (SERVICES, SELECT, TRACE)
	ok, warn, bad, info      color.Color // meanings: healthy, degraded, failing, rolling
	accent                   color.Color // keys, brand, env tag, gutter
	logger, pid, errText     color.Color // log roles
	selected, selectedText   color.Color // the cursor row
	match, matchText         color.Color // text-filter matches
	prod, prodText           color.Color // production bars and chips
	onColor                  color.Color // text on the ok, warn and accent chips
	pods                     []color.Color
}

// c is a color of the 256-color palette, the same in every terminal.
func c(n uint8) color.Color { return lipgloss.ANSIColor(n) }

// lightPalette is for terminals with a light background.
var lightPalette = palette{
	bg: c(231), text: c(235),
	dim: c(242), bar: c(254), barText: c(235), crumb: c(241),
	chip: c(238), chipText: c(231),
	ok: c(22), warn: c(94), bad: c(160), info: c(25),
	accent: c(25), logger: c(23), pid: c(127), errText: c(124),
	selected: c(153), selectedText: c(16),
	match: c(222), matchText: c(16),
	prod: c(160), prodText: c(231), onColor: c(231),
	pods: []color.Color{c(25), c(127), c(23), c(22), c(94), c(91)},
}

// darkPalette is for terminals with a dark background.
var darkPalette = palette{
	bg: c(234), text: c(252),
	dim: c(246), bar: c(237), barText: c(252), crumb: c(248),
	chip: c(252), chipText: c(234),
	ok: c(114), warn: c(214), bad: c(203), info: c(75),
	accent: c(75), logger: c(80), pid: c(176), errText: c(210),
	selected: c(24), selectedText: c(231),
	match: c(178), matchText: c(16),
	prod: c(160), prodText: c(231), onColor: c(16),
	pods: []color.Color{c(75), c(176), c(80), c(114), c(215), c(141)},
}

// paletteTheme builds a theme from a palette.
func paletteTheme(p palette) Theme {
	s := lipgloss.NewStyle
	chip := s().Bold(true).Padding(0, 1)
	pods := make([]lipgloss.Style, len(p.pods))
	for i, col := range p.pods {
		pods[i] = s().Bold(true).Foreground(col)
	}
	return Theme{
		Header:       s().Background(p.bar).Foreground(p.barText),
		HeaderProd:   s().Background(p.prod).Foreground(p.prodText),
		Brand:        s().Bold(true).Foreground(p.accent),
		BrandProd:    s().Bold(true).Foreground(p.prodText),
		EnvTag:       chip.Background(p.accent).Foreground(p.onColor),
		EnvTagProd:   chip.Background(p.prodText).Foreground(p.prod),
		Crumb:        s().Foreground(p.crumb),
		CrumbCurrent: s().Bold(true),
		Status:       s().Background(p.bar).Foreground(p.barText),
		StatusProd:   s().Background(p.bar).Foreground(p.bad),
		Chip:         chip.Background(p.chip).Foreground(p.chipText),
		ChipProd:     chip.Background(p.prod).Foreground(p.prodText),
		ChipLive:     chip.Background(p.ok).Foreground(p.onColor),
		ChipPaused:   chip.Background(p.warn).Foreground(p.onColor),
		Key:          s().Bold(true).Foreground(p.accent),
		Dim:          s().Foreground(p.dim),
		Bold:         s().Bold(true),
		Ok:           s().Foreground(p.ok),
		Warn:         s().Bold(true).Foreground(p.warn),
		Bad:          s().Bold(true).Foreground(p.bad),
		Info:         s().Foreground(p.info),
		Body:         s(),
		Selected:     s().Background(p.selected).Foreground(p.selectedText),
		TableHeader:  s().Bold(true).Foreground(p.dim),
		Prompt:       chip.Background(p.chip).Foreground(p.chipText),
		Popup:        s().Border(lipgloss.NormalBorder()).BorderForeground(p.dim).Padding(0, 1),
		PopupTitle:   s().Bold(true).Foreground(p.accent),
		Timestamp:    s().Foreground(p.dim),
		Thread:       s().Foreground(p.dim),
		Logger:       s().Foreground(p.logger),
		PID:          s().Foreground(p.pid),
		ErrorText:    s().Foreground(p.errText),
		Stack:        s().Foreground(p.errText),
		Highlight:    s().Background(p.match).Foreground(p.matchText).Underline(true),
		Pods:         pods,
	}
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
		Timestamp:    lipgloss.NewStyle().Foreground(lipgloss.BrightBlack),
		Thread:       lipgloss.NewStyle().Foreground(lipgloss.BrightBlack),
		Logger:       lipgloss.NewStyle().Foreground(lipgloss.Cyan),
		PID:          lipgloss.NewStyle().Foreground(lipgloss.Magenta),
		ErrorText:    lipgloss.NewStyle().Foreground(lipgloss.Red),
		Stack:        lipgloss.NewStyle().Foreground(lipgloss.Red),
		Highlight:    lipgloss.NewStyle().Background(lipgloss.Yellow).Foreground(lipgloss.Black).Underline(true),
		Pods: []lipgloss.Style{
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Cyan),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Magenta),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Blue),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Green),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.BrightMagenta),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.BrightBlue),
		},
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
		Timestamp: plain, Thread: plain, Logger: plain, PID: plain, ErrorText: bold, Stack: plain,
		Highlight: rev.Underline(true),
		Pods:      []lipgloss.Style{bold},
	}
}

// statusStyle returns the style of a service status word. Failures are red
// and bold, degraded states use the warning color, rollouts the info
// color; the word itself always carries the meaning.
func (t *Theme) statusStyle(s domain.ServiceStatus) lipgloss.Style {
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

// levelStyle returns the style of a level word.
func (t *Theme) levelStyle(l domain.Level) lipgloss.Style {
	switch l {
	case domain.LevelError:
		return t.Bad
	case domain.LevelWarn:
		return t.Warn
	case domain.LevelInfo:
		return t.Ok
	case domain.LevelDebug:
		return t.Dim
	default:
		return lipgloss.NewStyle()
	}
}

// podStyle returns the identity style of the i-th pod.
func (t *Theme) podStyle(i int) lipgloss.Style { return t.Pods[i%len(t.Pods)] }
