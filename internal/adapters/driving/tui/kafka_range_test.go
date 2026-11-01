package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// openKafkaRecords opens the records of the first consumed topic.
func openKafkaRecords(t *testing.T) (*Model, *fakeKafka, *kafkaRecordsScreen) {
	t.Helper()
	m, fk := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r, ok := m.top().(*kafkaRecordsScreen)
	if !ok {
		t.Fatalf("top is %T", m.top())
	}
	return m, fk, r
}

func lastKafkaQuery(fk *fakeKafka) ports.KafkaQuery {
	return fk.session.queries[len(fk.session.queries)-1]
}

func TestKafkaRangePopupLoadsARange(t *testing.T) {
	m, fk, r := openKafkaRecords(t)
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true})
	press(m, "b")
	p, ok := m.popup.(*rangePicker)
	if !ok {
		t.Fatalf("b must open the range popup, got %T", m.popup)
	}
	// The window shown is a tail, so the popup offers a default to edit.
	if p.from.String() != "now-1h" || p.err != "" {
		t.Fatalf("prefill %q, err %q", p.from.String(), p.err)
	}
	press(m, "ctrl+u")
	typeText(m, "2026-09-26 18:00")
	press(m, "tab", "ctrl+u")
	typeText(m, "2026-09-26 19:00")
	press(m, "enter")
	q := lastKafkaQuery(fk)
	if !q.Window.Closed() || q.After != nil || q.Follow {
		t.Fatalf("query %+v", q)
	}
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true})
	out := render(m, 140, 14)
	golden(t, "kafka_records_range_140x14", out)
	if !strings.Contains(out, "Sep 26 18:00 → 19:00") {
		t.Fatalf("the status bar says the range:\n%s", out)
	}
	if r.tail || r.cursor != 0 {
		t.Fatal("a range opens on its first record")
	}
}

func TestKafkaRangePages(t *testing.T) {
	m, fk, r := openKafkaRecords(t)
	press(m, "b", "ctrl+u")
	typeText(m, "2026-09-26 18:00")
	press(m, "tab", "ctrl+u")
	typeText(m, "2026-09-26 19:00")
	press(m, "enter")
	next := map[int32]int64{0: 5, 1: 3}
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true, Next: next})
	if out := render(m, 140, 14); !strings.Contains(out, "page 1 · more") {
		t.Fatalf("page 1 says there is more:\n%s", out)
	}

	press(m, "]")
	q := lastKafkaQuery(fk)
	if q.After[0] != 5 || q.After[1] != 3 || !q.Window.Closed() {
		t.Fatalf("] opens the page that starts at the offsets: %+v", q)
	}
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(2), HistoryDone: true})
	if out := render(m, 140, 14); !strings.Contains(out, "page 2 (last)") {
		t.Fatalf("page 2 is the last:\n%s", out)
	}

	n := len(fk.session.queries)
	press(m, "]")
	if len(fk.session.queries) != n {
		t.Fatal("] on the last page must not read again")
	}
	press(m, "[")
	if q := lastKafkaQuery(fk); q.After != nil {
		t.Fatalf("[ goes back to the first page: %+v", q)
	}
	press(m, "1") // a preset starts over
	if q := lastKafkaQuery(fk); q.After != nil || q.Window.IsRange() || len(r.pages) != 0 {
		t.Fatalf("a preset clears the pages: %+v", q)
	}
}

func TestKafkaClosedRangeDoesNotFollow(t *testing.T) {
	m, fk, r := openKafkaRecords(t)
	press(m, "b", "ctrl+u")
	typeText(m, "2026-09-26 18:00")
	press(m, "tab", "ctrl+u")
	typeText(m, "2026-09-26 19:00")
	press(m, "enter")
	n := len(fk.session.queries)
	press(m, "f")
	if len(fk.session.queries) != n || r.follow {
		t.Fatal("f on a closed range must not turn following on")
	}
}

func TestKafkaRangeRunsToNowWhenItsEndIsEmpty(t *testing.T) {
	m, fk, _ := openKafkaRecords(t)
	press(m, "b", "ctrl+u")
	typeText(m, "now-30m")
	press(m, "enter")
	if q := lastKafkaQuery(fk); !q.Window.IsRange() || q.Window.Closed() || !q.Window.From.Equal(t0.Add(-30*time.Minute)) {
		t.Fatalf("an open range: %+v", q.Window)
	}
}
