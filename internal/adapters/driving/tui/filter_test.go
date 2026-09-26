package tui

import (
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// typeText sends each rune as a key press.
func typeText(m *Model, s string) {
	for _, r := range s {
		press(m, string(r))
	}
}

// settle applies a pending debounced filter.
func settle(m *Model, l *logsScreen) { m.Update(filterTickMsg{screen: l}) }

func messages(l *logsScreen) []string {
	var out []string
	for i := range l.shown() {
		e, _ := l.entryAt(i)
		out = append(out, e.Message)
	}
	return out
}

func TestLiveFilterAndHighlightGolden(t *testing.T) {
	m, l := openLogs(t)
	press(m, "/")
	typeText(m, "gateway")
	settle(m, l)
	if l.shown() != 3 { // two messages and a stack trace mention the gateway
		t.Fatalf("live filter: %d rows %v", l.shown(), messages(l))
	}
	press(m, "ctrl+r")
	typeText(m, "|declined")
	settle(m, l)
	golden(t, "filter_prompt_regex_160x16", render(m, 160, 16))
	press(m, "ctrl+x")
	settle(m, l)
	golden(t, "filter_highlight_160x16", render(m, 160, 16))
	press(m, "enter")
	if l.editing || l.shown() != 9 {
		t.Fatalf("highlight keeps all rows: %d", l.shown())
	}
	press(m, "g", "n")
	if e, _ := l.entryAt(l.displayCursor()); !strings.Contains(e.Message, "gateway latency") {
		t.Fatalf("n: %q", e.Message)
	}
	press(m, "n", "n")
	if e, _ := l.entryAt(l.displayCursor()); !strings.Contains(e.Message, "Card declined") {
		t.Fatalf("n n n: %q", e.Message)
	}
	if out := render(m, 200, 16); !strings.Contains(out, "match 3 of 4") {
		t.Fatalf("status must show the match position:\n%s", out)
	}
	press(m, "n", "n")
	if e, _ := l.entryAt(l.displayCursor()); !strings.Contains(e.Message, "gateway latency") {
		t.Fatalf("n must wrap: %q", e.Message)
	}
}

func TestFilterContextGolden(t *testing.T) {
	m, l := openLogs(t)
	press(m, "/")
	typeText(m, "declined")
	press(m, "enter", "X")
	if l.filter.Context != 1 {
		t.Fatalf("context %d", l.filter.Context)
	}
	golden(t, "filter_context_140x10", render(m, 140, 10))
	if got := strings.Join(messages(l), " | "); !strings.Contains(got, "Started PaymentApplication") || !strings.Contains(got, "health probe") {
		t.Fatalf("context rows: %s", got)
	}
	press(m, "/", "ctrl+u")
	typeText(m, "gateway latency|declined")
	press(m, "ctrl+r", "enter")
	golden(t, "filter_context_groups_140x12", render(m, 140, 12))
	if !strings.Contains(render(m, 140, 12), " --") {
		t.Fatal("separate context groups need a separator")
	}
}

func TestInvertStackAndClear(t *testing.T) {
	m, l := openLogs(t)
	press(m, "/")
	typeText(m, "!health")
	press(m, "enter")
	if l.shown() != 8 {
		t.Fatalf("!health: %d rows", l.shown())
	}
	press(m, "/", "ctrl+a")
	typeText(m, "payment")
	press(m, "enter")
	for _, msg := range messages(l) {
		if strings.Contains(msg, "health") || !strings.Contains(strings.ToLower(msg+" i.g.p.PaymentService i.g.p.PaymentController i.g.p.PaymentApplication"), "payment") {
			t.Fatalf("stacked filters let %q through", msg)
		}
	}
	if len(l.filter.Texts) != 2 {
		t.Fatalf("stack: %v", l.filter.Texts)
	}
	press(m, "esc")
	if len(l.filter.Texts) != 1 || l.shown() != 8 {
		t.Fatalf("esc must clear the last filter: %v %d", l.filter.Texts, l.shown())
	}
	press(m, "esc")
	if l.filter.Active() || l.shown() != 9 {
		t.Fatal("second esc clears the stacked filter")
	}
	press(m, "esc")
	if m.top() == l {
		t.Fatal("esc without filters goes back")
	}
}

func TestInvalidRegexKeepsLastValid(t *testing.T) {
	m, l := openLogs(t)
	press(m, "/", "ctrl+r")
	typeText(m, "declin")
	settle(m, l)
	typeText(m, "(")
	settle(m, l)
	if l.inputErr == "" || l.shown() != 1 {
		t.Fatalf("err %q rows %d", l.inputErr, l.shown())
	}
	if out := render(m, 200, 12); !strings.Contains(out, "invalid regex") {
		t.Fatalf("prompt must explain:\n%s", out)
	}
}

func TestLevelKeysAndPicker(t *testing.T) {
	m, l := openLogs(t)
	press(m, "e")
	if l.shown() != 2 {
		t.Fatalf("e: %d", l.shown())
	}
	press(m, "w")
	if l.shown() != 3 {
		t.Fatalf("w: %d", l.shown())
	}
	press(m, "a")
	if l.shown() != 9 {
		t.Fatalf("a: %d", l.shown())
	}
	press(m, "l")
	golden(t, "level_picker_120x16", render(m, 120, 16))
	press(m, "j", "j", "j", "j", "space", "enter") // untick unknown
	if l.shown() != 8 || !strings.Contains(render(m, 200, 10), "levels ERROR WARN INFO DEBUG") {
		t.Fatalf("picker: %d rows", l.shown())
	}
}

func TestNewEntriesAreFiltered(t *testing.T) {
	m, l := openLogs(t)
	press(m, "/")
	typeText(m, "declined")
	press(m, "enter")
	feed(m, l, ports.LogBatch{Entries: []domain.LogEntry{
		logEntry(1, podA, domain.LevelError, "a.B", "Card declined again"),
		logEntry(2, podA, domain.LevelInfo, "a.B", "unrelated"),
	}})
	if got := messages(l); len(got) != 2 || got[1] != "Card declined again" {
		t.Fatalf("incremental filter: %v", got)
	}
}

func TestFlashConfirmsModeChanges(t *testing.T) {
	m, _ := openLogs(t)
	press(m, "W")
	if out := render(m, 200, 10); !strings.Contains(out, "wrap on") {
		t.Fatalf("flash missing:\n%s", out)
	}
	m.Update(flashDoneMsg{id: m.flashID})
	if out := render(m, 200, 10); strings.Contains(out, "wrap on") {
		t.Fatal("flash must expire")
	}
	m.Update(flashDoneMsg{id: m.flashID - 1})
}

func TestHelpGolden(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	press(m, "?")
	golden(t, "help_services_100x30", render(m, 100, 30))
	press(m, "/")
	typeText(m, "sort")
	if out := render(m, 100, 10); !strings.Contains(out, "sort: status, name") || strings.Contains(out, "resync") {
		t.Fatalf("help search:\n%s", out)
	}
	press(m, "esc", "esc")
	if _, ok := m.top().(*servicesScreen); !ok {
		t.Fatal("esc must close help")
	}
}

func TestHelpOnLogsFollowsKeymap(t *testing.T) {
	m, _ := openLogs(t)
	km, err := NewKeymap(map[string][]string{"follow": {"ctrl+l"}})
	if err != nil {
		t.Fatal(err)
	}
	m.opts.Keys = km
	m.handleKey(keyMsgF1())
	out := render(m, 120, 80)
	if !strings.Contains(out, "ctrl+l") || !strings.Contains(out, "IN THE FILTER PROMPT") || !strings.Contains(out, "follow on/off") {
		t.Fatalf("logs help:\n%s", out)
	}
	golden(t, "help_logs_120x80", out)
}
