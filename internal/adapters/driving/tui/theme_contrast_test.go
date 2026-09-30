package tui

import (
	"fmt"
	"image/color"
	"math"
	"testing"
)

// luminance is the WCAG relative luminance of c.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	ch := func(v uint32) float64 {
		x := float64(v) / 0xffff
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(r) + 0.7152*ch(g) + 0.0722*ch(b)
}

// contrast is the WCAG contrast ratio of two colors (1 to 21).
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func rgb(hex uint32) color.Color {
	return color.RGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

// TestThemeContrast checks that every text of the light and dark themes
// reads on the backgrounds it is drawn on (WCAG AA: 4.5 for text), on
// several real terminal backgrounds of each kind, not only the one assumed.
func TestThemeContrast(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    palette
		bgs  map[string]color.Color // terminal backgrounds of this kind
	}{
		{"light", lightPalette, map[string]color.Color{"white": rgb(0xffffff), "VS Code light": rgb(0xffffff), "One Half Light": rgb(0xfafafa), "Solarized light": rgb(0xfdf6e3)}},
		{"dark", darkPalette, map[string]color.Color{"black": rgb(0x000000), "VS Code dark": rgb(0x1e1e1e), "JetBrains dark": rgb(0x2b2b2b), "Solarized dark": rgb(0x002b36)}},
	} {
		p := tc.p
		check := func(what string, fg, bg color.Color, min float64) {
			t.Helper()
			if r := contrast(fg, bg); r < min {
				t.Errorf("%s: %s contrast %.2f, want at least %.1f", tc.name, what, r, min)
			}
		}
		texts := map[string]color.Color{
			"text": p.text, "dim": p.dim, "ok": p.ok, "warn": p.warn, "bad": p.bad, "info": p.info,
			"accent": p.accent, "logger": p.logger, "pid": p.pid, "error text": p.errText,
		}
		for i, pod := range p.pods {
			texts[fmt.Sprint("pod ", i)] = pod
		}
		for bgName, bg := range tc.bgs {
			for name, fg := range texts {
				check(name+" on "+bgName, fg, bg, 4.5)
			}
		}
		check("bar text on the bar", p.barText, p.bar, 7)
		check("crumbs on the bar", p.crumb, p.bar, 4.5)
		for name, fg := range map[string]color.Color{"accent": p.accent, "bad": p.bad, "ok": p.ok, "warn": p.warn, "dim": p.dim} {
			check(name+" on the bar", fg, p.bar, 3) // bold words: level counts, brand, hints
		}
		check("chip text", p.chipText, p.chip, 7)
		for name, bg := range map[string]color.Color{"live chip": p.ok, "paused chip": p.warn, "env tag": p.accent} {
			check("text on the "+name, p.onColor, bg, 4.5)
		}
		check("text on production bars", p.prodText, p.prod, 4.5)
		check("the cursor row", p.selectedText, p.selected, 7) // drawn in one color over plain text
		check("a filter match", p.matchText, p.match, 7)
	}
}
