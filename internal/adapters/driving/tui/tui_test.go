package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var update = flag.Bool("update", false, "rewrite golden files")

func testOptions(t *testing.T, prod bool) Options {
	t.Helper()
	theme, err := NewTheme("light", false)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewKeymap(nil)
	if err != nil {
		t.Fatal(err)
	}
	env := EnvInfo{Name: "rec", Context: "gke_acme_europe-west1_main", Namespaces: []string{"app-rec"}}
	if prod {
		env = EnvInfo{Name: "prd", Context: "gke_acme_europe-west1_main", Namespaces: []string{"app-prd"}, Production: true}
	}
	return Options{Env: env, Theme: theme, Keys: keys, Source: "demo"}
}

func render(m Model, w, h int) string {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return ansi.Strip(next.(Model).View().Content)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/adapters/driving/tui -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestShellGolden(t *testing.T) {
	golden(t, "shell_rec_120x30", render(NewModel(testOptions(t, false)), 120, 30))
	golden(t, "shell_prd_160x45", render(NewModel(testOptions(t, true)), 160, 45))
}

func TestShellFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 24}, {40, 10}} {
		out := render(NewModel(testOptions(t, false)), size[0], size[1])
		lines := strings.Split(out, "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != size[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", size[0], size[1], i, w)
			}
		}
	}
}

func TestProductionIsVisible(t *testing.T) {
	out := render(NewModel(testOptions(t, true)), 120, 20)
	if !strings.Contains(out, " PRD ") || !strings.Contains(out, "PRODUCTION") {
		t.Fatalf("production not visible:\n%s", out)
	}
}

func TestQuitKeys(t *testing.T) {
	m := NewModel(testOptions(t, false))
	for _, k := range []tea.KeyPressMsg{{Code: 'q', Text: "q"}, {Code: 'c', Mod: tea.ModCtrl}} {
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("%v did not quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v did not quit", k)
		}
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}); cmd != nil {
		t.Fatal("x must not quit")
	}
}

func TestKeymapOverrides(t *testing.T) {
	km, err := NewKeymap(map[string][]string{"follow": {"ctrl+l"}})
	if err != nil {
		t.Fatal(err)
	}
	if !km.Is("ctrl+l", ActFollow) || km.Is("f", ActFollow) {
		t.Fatal("override must replace default keys")
	}
	if !km.Is("&", ActWindow1) || !km.Is("à", ActWindowTail) {
		t.Fatal("AZERTY aliases missing")
	}
	if _, err := NewKeymap(map[string][]string{"folow": {"f"}}); err == nil || !strings.Contains(err.Error(), `unknown action "folow"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryActionHasAKey(t *testing.T) {
	for a, ks := range defaultKeys {
		if len(ks) == 0 {
			t.Errorf("%s has no default key", a)
		}
	}
}

func TestThemes(t *testing.T) {
	for _, n := range ThemeNames {
		if _, err := NewTheme(n, true); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	if _, err := NewTheme("dark", false); err == nil {
		t.Fatal("unknown theme accepted")
	}
}
