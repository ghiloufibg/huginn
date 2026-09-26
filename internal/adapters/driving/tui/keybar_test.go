package tui

import (
	"strings"
	"testing"
)

func lastLines(out string, n int) []string {
	lines := strings.Split(out, "\n")
	return lines[len(lines)-n:]
}

func TestKeyBarSizes(t *testing.T) {
	m, _ := openLogs(t)
	compact := render(m, 160, 20)
	if bar := lastLines(compact, 1)[0]; !strings.HasPrefix(bar, " / filter  l levels") || !strings.Contains(bar, "? help") {
		t.Fatalf("compact key bar: %q", bar)
	}
	press(m, "f2")
	full := render(m, 160, 20)
	golden(t, "keybar_full_160x20", full)
	two := lastLines(full, 2)
	if !strings.Contains(two[0], "context") || !strings.HasSuffix(strings.TrimRight(two[1], " "), "? help") {
		t.Fatalf("full key bar:\n%s\n%s", two[0], two[1])
	}
	press(m, "f2")
	hidden := render(m, 160, 20)
	if status := lastLines(hidden, 1)[0]; !strings.Contains(status, "f2 keys  ? help") || strings.Contains(hidden, "l levels") {
		t.Fatalf("hidden key bar: %q", status)
	}
	if strings.Count(hidden, "\n") != 19 {
		t.Fatal("the screen keeps its height")
	}
	press(m, "f2")
	if m.keyBar != keyBarCompact {
		t.Fatal("f2 cycles back to compact")
	}
}

func TestKeyBarFollowsContext(t *testing.T) {
	m, _ := openLogs(t)
	press(m, "/")
	if bar := lastLines(render(m, 160, 12), 1)[0]; !strings.Contains(bar, "ctrl+r regex") || strings.Contains(bar, "l levels") {
		t.Fatalf("prompt key bar: %q", bar)
	}
	press(m, "esc", "space")
	if bar := lastLines(render(m, 160, 12), 1)[0]; !strings.Contains(bar, "space resume") {
		t.Fatalf("paused key bar: %q", bar)
	}
	press(m, "space", "C")
	if bar := lastLines(render(m, 160, 20), 1)[0]; !strings.Contains(bar, "h thread") {
		t.Fatalf("columns popup key bar: %q", bar)
	}
}

func TestKeyBarKeepsHelpOnNarrowTerminals(t *testing.T) {
	m, _ := openLogs(t)
	for _, w := range []int{80, 60, 40} {
		bar := lastLines(render(m, w, 14), 1)[0]
		if !strings.HasSuffix(strings.TrimRight(bar, " "), "? help") {
			t.Errorf("%d cells: %q", w, bar)
		}
	}
}

func TestKeyBarShowsRemappedKeys(t *testing.T) {
	m, _ := openLogs(t)
	km, err := NewKeymap(map[string][]string{"filter": {"ctrl+f"}})
	if err != nil {
		t.Fatal(err)
	}
	m.opts.Keys = km
	if bar := lastLines(render(m, 160, 12), 1)[0]; !strings.HasPrefix(bar, " ctrl+f filter") {
		t.Fatalf("remapped key: %q", bar)
	}
}

func TestParseKeyBar(t *testing.T) {
	for in, want := range map[string]keyBarSize{"": keyBarCompact, "compact": keyBarCompact, "full": keyBarFull, "hidden": keyBarHidden} {
		if got, err := ParseKeyBar(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseKeyBar("big"); err == nil {
		t.Fatal("unknown size accepted")
	}
}
