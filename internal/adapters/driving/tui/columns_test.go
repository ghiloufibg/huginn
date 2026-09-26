package tui

import (
	"strings"
	"testing"

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
	if !l.hide.Has(ports.ColThread) || l.hide.Has(ports.ColLogger) || !l.manualColumns {
		t.Fatalf("h must hide only the thread: %b", l.hide)
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
	if !l.hide.Has(ports.ColThread) || l.podID != podIDNone || l.hide.Has(ports.ColLogger) {
		t.Fatalf("z twice restores the previous columns: %b pod %v", l.hide, l.podID)
	}
	press(m, "C", "r", "esc")
	if l.hide != 0 || l.podID != podIDShort || l.manualColumns {
		t.Fatal("r resets to defaults and automatic narrowing")
	}
}

func TestTimestampCycleIncludesHidden(t *testing.T) {
	m, l := openLogs(t)
	for _, want := range []string{"timestamps UTC", "timestamps relative", "timestamps hidden", "timestamps local"} {
		press(m, "c")
		if m.flashText != want {
			t.Fatalf("flash %q, want %q", m.flashText, want)
		}
	}
	if l.hide.Has(ports.ColTime) {
		t.Fatal("a full cycle shows the time again")
	}
}

func TestConfiguredColumns(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	m.opts.LogColumns = []string{"time", "level"}
	l := newLogsScreen(m, "payment-service")
	if !l.manualColumns || l.podID != podIDNone || !l.hide.Has(ports.ColThread) || !l.hide.Has(ports.ColLogger) || l.hide.Has(ports.ColTime) {
		t.Fatalf("configured columns: hide %b pod %v", l.hide, l.podID)
	}
}
