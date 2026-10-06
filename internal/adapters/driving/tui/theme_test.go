package tui

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAutoThemeFollowsTheTerminal(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	dark, _ := NewTheme("dark", false)
	m.opts.Theme, m.opts.AutoTheme = dark, true
	_ = m.dim() // an ink painted with the dark theme
	m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 0xfa, G: 0xfa, B: 0xfa, A: 0xff}})
	if m.opts.Theme.Name != "light" || m.inks != nil {
		t.Fatalf("a light terminal switches to light and drops the old inks: %q", m.opts.Theme.Name)
	}
	m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 0x1e, G: 0x1e, B: 0x1e, A: 0xff}})
	if m.opts.Theme.Name != "dark" {
		t.Fatalf("a dark terminal switches to dark: %q", m.opts.Theme.Name)
	}
	m.opts.AutoTheme = false
	m.Update(tea.BackgroundColorMsg{Color: color.White})
	if m.opts.Theme.Name != "dark" {
		t.Fatal("a theme chosen by name never switches")
	}
}

func TestThemes(t *testing.T) {
	for _, name := range ThemeNames {
		if name == "auto" {
			continue // resolved by bootstrap
		}
		th, err := NewTheme(name, true)
		if err != nil || th.Name != name || len(th.Pods) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := NewTheme("solarized", false); err == nil {
		t.Error("unknown theme accepted")
	}
}
