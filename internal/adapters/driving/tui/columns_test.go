package tui

import (
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func TestAutomaticNarrowing(t *testing.T) {
	m, l := openLogs(t)
	wide := render(m, 160, 12)
	if !strings.Contains(wide, "[exec-1]") || !strings.Contains(wide, "i.g.p.PaymentController") {
		t.Fatalf("wide terminals show every column:\n%s", wide)
	}
	mid := render(m, 120, 12)
	if strings.Contains(mid, "[exec-1]") || !strings.Contains(mid, "i.g.p.PaymentController") {
		t.Fatalf("below 140 cells the thread is hidden:\n%s", mid)
	}
	narrow := render(m, 100, 12)
	if strings.Contains(narrow, "i.g.p.PaymentController") {
		t.Fatalf("below 110 cells the class is hidden:\n%s", narrow)
	}
	if l.manualColumns {
		t.Fatal("rendering must not count as a manual choice")
	}
}

func TestColumnsPickerAndFocus(t *testing.T) {
	m, l := openLogs(t)
	render(m, 160, 12)
	press(m, "C")
	golden(t, "columns_picker_160x20", render(m, 160, 20))
	press(m, "h")
	if !l.hide.Has("thread") || l.hide.Has("class") || !l.manualColumns {
		t.Fatalf("h must hide only the thread: %v", l.hide)
	}
	press(m, "p", "esc")
	out := render(m, 160, 12)
	if strings.Contains(out, "[exec-1]") || strings.Contains(out, " x4k2p 19:") || !strings.Contains(out, "cols time level class msg") {
		t.Fatalf("thread and pod hidden:\n%s", out)
	}
	press(m, "z")
	golden(t, "logs_focus_120x12", render(m, 120, 12))
	if !strings.Contains(render(m, 120, 12), "  INFO health probe succeeded") {
		t.Fatal("focus layout: message right after the level")
	}
	press(m, "z")
	if !l.hide.Has("thread") || l.podID != podIDNone || l.hide.Has("class") {
		t.Fatalf("z twice restores the previous columns: %v pod %v", l.hide, l.podID)
	}
	press(m, "C", "r", "esc")
	if len(l.hide) != 0 || l.podID != podIDShort || l.manualColumns {
		t.Fatal("r resets to defaults and automatic narrowing")
	}
}

func TestTimeFormatNeverHides(t *testing.T) {
	m, l := openLogs(t)
	for _, want := range []string{"time UTC", "time relative", "time local", "time UTC"} {
		press(m, "ctrl+t")
		if m.flashText != want || l.hide.Has("time") {
			t.Fatalf("flash %q (time hidden %v), want %q", m.flashText, l.hide.Has("time"), want)
		}
	}
	press(m, "c") // hides the time
	press(m, "ctrl+t")
	if l.hide.Has("time") || l.cycling || l.timestamps != ports.TimestampRelative {
		t.Fatalf("ctrl+t shows a hidden time in the next format and ends the cycle: hide %v cycling %v", l.hide, l.cycling)
	}
	press(m, "C", "f")
	if l.timestamps != ports.TimestampLocal {
		t.Fatal("f in the picker cycles the format")
	}
}

func TestColumnCycle(t *testing.T) {
	cases := []struct {
		width int
		want  []string
	}{
		{160, []string{"time hidden", "level hidden", "thread hidden", "class hidden", "columns restored"}},
		{120, []string{"time hidden", "level hidden", "class hidden", "columns restored"}}, // thread hidden by width
		{100, []string{"time hidden", "level hidden", "columns restored"}},                 // thread and class too
	}
	for _, c := range cases {
		m, l := openLogs(t)
		render(m, c.width, 20)
		for i, want := range c.want {
			press(m, "c")
			if m.flashText != want {
				t.Fatalf("width %d press %d: flash %q, want %q", c.width, i+1, m.flashText, want)
			}
		}
		if len(l.hide) != 0 || l.manualColumns || l.cycling {
			t.Fatalf("width %d: the last press restores the layout from before (hide %v manual %v)", c.width, l.hide, l.manualColumns)
		}
	}
}

func TestColumnCycleEndsOnOtherColumnChanges(t *testing.T) {
	for _, keys := range [][]string{{"z"}, {"I"}, {"C", "p", "esc"}, {"R"}} {
		m, l := openLogs(t)
		render(m, 160, 20)
		press(m, "c", "c")
		press(m, keys...)
		if l.cycling {
			t.Fatalf("%v should end the cycle", keys)
		}
		before := columnState{hide: l.hide, podID: l.podID, manual: l.manualColumns}
		press(m, "c")
		if keys[0] == "z" { // time and level hidden by c, the rest by z: nothing left to hide
			if m.flashText != "columns shown" || len(l.effectiveHide(m, 160)) != 0 {
				t.Fatalf("z then c: flash %q, hide %v", m.flashText, l.hide)
			}
			continue
		}
		if !l.cycling || !sameState(l.beforeCycle, before) {
			t.Fatalf("%v then c starts a new cycle from the current layout", keys)
		}
	}
}

func TestResetDisplayKeepsTheData(t *testing.T) {
	m, l := openLogs(t)
	render(m, 160, 20)
	press(m, "/", "t", "i", "m", "e", "o", "u", "t", "enter", "e", "c", "c", "ctrl+t", "I", "W", "L")
	window := l.window
	press(m, "R")
	if len(l.hide) != 0 || l.manualColumns || l.cycling || l.focus || l.podID != podIDShort ||
		l.timestamps != ports.TimestampLocal || l.pan != 0 || l.wrap {
		t.Fatalf("R resets the display: hide %v manual %v pod %v time %v pan %d wrap %v", l.hide, l.manualColumns, l.podID, l.timestamps, l.pan, l.wrap)
	}
	if !l.filter.Active() || l.window != window || m.flashText != "display reset" {
		t.Fatalf("R keeps filters and window (filter %v, flash %q)", l.filter.Active(), m.flashText)
	}
}

func TestWarnMessagesColoredWhenLevelHidden(t *testing.T) {
	m, l := openLogs(t)
	render(m, 160, 20)
	e := domain.LogEntry{Level: domain.LevelWarn, Message: "slow downstream", Structured: true}
	plain := strings.Join(l.renderEntry(m, &e, viewRow{}, 160), "")
	press(m, "c", "c") // time, level
	colored := strings.Join(l.renderEntry(m, &e, viewRow{}, 160), "")
	if !strings.Contains(colored, m.opts.Theme.Warn.Render("slow downstream")) || strings.Contains(plain, m.opts.Theme.Warn.Render("slow downstream")) {
		t.Fatalf("WARN message colored only when the level is hidden:\n%q\n%q", plain, colored)
	}
}

func TestConfiguredColumns(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	m.opts.LogColumns = []string{"time", "level"}
	l := newLogsScreen(m, "payment-service")
	if !l.manualColumns || l.podID != podIDNone || !l.hide.Has("thread") || !l.hide.Has("class") || l.hide.Has("time") {
		t.Fatalf("configured columns: hide %v pod %v", l.hide, l.podID)
	}
}

func TestColumnCycleGolden(t *testing.T) {
	m, _ := openLogs(t)
	render(m, 160, 16)
	press(m, "c", "c")
	golden(t, "logs_cycle_time_level_160x16", render(m, 160, 16))
	press(m, "c", "c")
	golden(t, "logs_cycle_all_160x16", render(m, 160, 16))
}
