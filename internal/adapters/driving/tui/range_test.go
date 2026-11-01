package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// lastQuery is the most recent log query the screen opened.
func lastQuery(t *testing.T) ports.LogQuery {
	t.Helper()
	if len(sessions.queries) == 0 {
		t.Fatal("no query opened")
	}
	return sessions.queries[len(sessions.queries)-1]
}

// openRange opens the range popup on the logs screen and loads
// [from, to), both typed as a user would.
func openRange(t *testing.T, m *Model, from, to string) {
	t.Helper()
	press(m, "b")
	if _, ok := m.popup.(*rangePicker); !ok {
		t.Fatalf("b must open the range popup, got %T", m.popup)
	}
	press(m, "ctrl+u")
	typeText(m, from)
	press(m, "tab", "ctrl+u")
	typeText(m, to)
	press(m, "enter")
	if m.popup != nil {
		t.Fatalf("enter must load a valid range:\n%s", render(m, 120, 20))
	}
}

func TestRangePopup(t *testing.T) {
	m, _ := openLogs(t)
	press(m, "b")
	p, ok := m.popup.(*rangePicker)
	if !ok {
		t.Fatalf("b must open the range popup, got %T", m.popup)
	}
	// Prefilled with the current window (15m), the end empty: until now.
	if p.from.String() != "now-15m" || p.to.String() != "" || p.err != "" {
		t.Fatalf("prefill %q → %q, err %q", p.from.String(), p.to.String(), p.err)
	}
	golden(t, "logs_range_popup_120x20", render(m, 120, 20))

	// An invalid value says why, and enter does nothing.
	n := len(sessions.queries)
	press(m, "tab")
	typeText(m, "soon")
	if !strings.Contains(p.err, `to: "soon"`) {
		t.Fatalf("err = %q", p.err)
	}
	press(m, "enter")
	if m.popup == nil || len(sessions.queries) != n {
		t.Fatal("enter must be inert while a field is invalid")
	}
	golden(t, "logs_range_popup_error_120x20", render(m, 120, 20))

	// esc closes without loading.
	press(m, "esc")
	if m.popup != nil || len(sessions.queries) != n {
		t.Fatal("esc must close the popup without loading")
	}
}

func TestRangeLoadsAClosedRangeThatDoesNotFollow(t *testing.T) {
	m, l := openLogs(t)
	openRange(t, m, "2026-09-26 19:00", "2026-09-26 19:05")
	q := lastQuery(t)
	wantFrom := time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)
	if !q.Window.Closed() || !q.Window.From.Equal(wantFrom) || !q.Window.To.Equal(wantFrom.Add(5*time.Minute)) || q.Follow || q.Page != nil {
		t.Fatalf("query %+v", q)
	}
	feed(m, l, headBatch())
	out := render(m, 160, 16)
	golden(t, "logs_range_160x16", out)
	if !strings.Contains(out, "RANGE") || !strings.Contains(out, "Sep 26 19:00 → 19:05") {
		t.Fatalf("a closed range shows the RANGE chip and its bounds:\n%s", out)
	}
	if l.tail || l.displayCursor() != 0 {
		t.Fatal("a range opens on its first line")
	}
	// Same range again: nothing to reload.
	n := len(sessions.queries)
	openRange(t, m, "2026-09-26 19:00", "2026-09-26 19:05")
	if len(sessions.queries) != n {
		t.Fatal("loading the range already shown must not reload it")
	}
}

func TestOpenRangeFollows(t *testing.T) {
	m, l := openLogs(t)
	openRange(t, m, "now-30m", "")
	q := lastQuery(t)
	if !q.Window.IsRange() || q.Window.Closed() || !q.Follow {
		t.Fatalf("an open range follows: %+v", q)
	}
	feed(m, l, paymentBatch())
	if out := render(m, 160, 16); strings.Contains(out, "RANGE") || !strings.Contains(out, "→ now") {
		t.Fatalf("an open range is live and says → now:\n%s", out)
	}
}

func TestRangePagesAreTurnedWithBrackets(t *testing.T) {
	m, l := openLogs(t)
	openRange(t, m, "2026-09-26 18:00", "2026-09-26 19:00")
	first := lastQuery(t)
	cursor := &domain.PageCursor{Time: time.Date(2026, 9, 26, 18, 44, 2, 0, time.UTC), Skip: map[string]int{domain.SkipKey(podA, "payment-service"): 2}}
	feed(m, l, ports.LogBatch{HistoryDone: true, Entries: headBatch().Entries, More: cursor})
	out := render(m, 160, 16)
	if !strings.Contains(out, "PAGE") && !strings.Contains(out, "RANGE") || !strings.Contains(out, "page 1 · more after 18:44:02") {
		t.Fatalf("page 1 says where it ended:\n%s", out)
	}
	golden(t, "logs_range_page1_160x16", out)

	press(m, "]")
	q := lastQuery(t)
	if q.Page != cursor || !q.Window.From.Equal(cursor.Time) || !q.Window.To.Equal(first.Window.To) {
		t.Fatalf("] must open the page that starts at the cursor: %+v", q)
	}
	feed(m, l, ports.LogBatch{HistoryDone: true, Entries: paymentBatch().Entries})
	if out := render(m, 160, 16); !strings.Contains(out, "page 2 (last)") {
		t.Fatalf("page 2 is the last:\n%s", out)
	}

	n := len(sessions.queries)
	press(m, "]")
	if len(sessions.queries) != n {
		t.Fatal("] on the last page must not reload")
	}

	press(m, "[")
	q = lastQuery(t)
	if q.Page != nil || !q.Window.From.Equal(first.Window.From) {
		t.Fatalf("[ must return to the first page: %+v", q)
	}
	n = len(sessions.queries)
	press(m, "[")
	if len(sessions.queries) != n {
		t.Fatal("[ on the first page must not reload")
	}
}

func TestChangingTheWindowStartsAgainOnPageOne(t *testing.T) {
	m, l := openLogs(t)
	openRange(t, m, "2026-09-26 18:00", "2026-09-26 19:00")
	feed(m, l, ports.LogBatch{HistoryDone: true, Entries: headBatch().Entries, More: &domain.PageCursor{Time: t0.Add(-time.Hour)}})
	press(m, "]")
	if len(l.pages) != 1 {
		t.Fatal("] must move to page 2")
	}
	press(m, "1")
	if len(l.pages) != 0 || lastQuery(t).Page != nil || lastQuery(t).Window.Since != 15*time.Minute {
		t.Fatalf("a preset resets the pages: %+v", lastQuery(t))
	}
}

func TestFollowLeavesAClosedRange(t *testing.T) {
	m, l := openLogs(t)
	openRange(t, m, "2026-09-26 18:00", "2026-09-26 19:00")
	feed(m, l, ports.LogBatch{HistoryDone: true, Entries: headBatch().Entries, More: &domain.PageCursor{Time: t0.Add(-time.Hour)}})
	press(m, "]")
	press(m, "f")
	q := lastQuery(t)
	if q.Window.IsRange() || !q.Follow || q.Page != nil || len(l.pages) != 0 {
		t.Fatalf("f on a closed range goes back to the default window, following, on page 1: %+v", q)
	}
}

func TestEmptyRangeSaysWhy(t *testing.T) {
	m, l := openLogs(t)
	openRange(t, m, "2026-09-26 19:00", "2026-09-26 19:05")
	feed(m, l, ports.LogBatch{HistoryDone: true})
	out := render(m, 140, 16)
	golden(t, "logs_range_empty_140x16", out)
	for _, want := range []string{"no line Sep 26 19:00 → 19:05", "no longer exist", "rotated away"} {
		if !strings.Contains(out, want) {
			t.Fatalf("empty range lacks %q:\n%s", want, out)
		}
	}
}

func TestWindowPickerOpensTheRangePopup(t *testing.T) {
	m, _ := openLogs(t)
	press(m, "T", "b")
	if _, ok := m.popup.(*rangePicker); !ok {
		t.Fatalf("b in the window picker must open the range popup, got %T", m.popup)
	}
	press(m, "esc", "T")
	for range len(m.opts.Windows) { // down to the last row
		press(m, "j")
	}
	press(m, "enter")
	if _, ok := m.popup.(*rangePicker); !ok {
		t.Fatalf("the last row of the picker must open the range popup, got %T", m.popup)
	}
}

func TestRangeBounds(t *testing.T) {
	utc := time.UTC
	paris := time.FixedZone("CET", 3600)
	at := func(d, h, mi, s int) time.Time { return time.Date(2026, 11, d, h, mi, s, 0, utc) }
	mk := func(from, to time.Time) domain.TimeWindow {
		w, err := domain.NewRange(from, to)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	for _, c := range []struct {
		name string
		w    domain.TimeWindow
		loc  *time.Location
		want string
	}{
		{"same day", mk(at(1, 14, 30, 0), at(1, 14, 35, 0)), utc, "Nov 1 14:30 → 14:35"},
		{"other day", mk(at(1, 14, 30, 0), at(2, 9, 0, 0)), utc, "Nov 1 14:30 → Nov 2 09:00"},
		{"seconds", mk(at(1, 14, 30, 5), at(1, 14, 31, 0)), utc, "Nov 1 14:30:05 → 14:31:00"},
		{"open", mk(at(1, 14, 30, 0), time.Time{}), utc, "Nov 1 14:30 → now"},
		{"zone", mk(at(1, 14, 30, 0), at(1, 14, 35, 0)), paris, "Nov 1 15:30 → 15:35"},
		{"day changes in the zone", mk(at(1, 23, 30, 0), at(2, 0, 30, 0)), paris, "Nov 2 00:30 → 01:30"},
	} {
		if got := rangeBounds(c.w, c.loc); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRangeTypedInTheConfiguredZone(t *testing.T) {
	m, _ := openLogs(t)
	m.opts.Location = time.FixedZone("CET", 3600)
	openRange(t, m, "2026-09-26 20:00", "2026-09-26 20:05")
	q := lastQuery(t)
	if want := time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC); !q.Window.From.Equal(want) {
		t.Fatalf("20:00 CET is 19:00 UTC, got %v", q.Window.From)
	}
}

func TestPageAndRangeKeysAreFree(t *testing.T) {
	km, err := NewKeymap(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Action{ActWindowRange, ActPageNext, ActPagePrev} {
		key := km.First(a)
		for other, ks := range defaultKeys {
			if other == a {
				continue
			}
			for _, k := range ks {
				// Keys may be shared by actions of different screens (p, M, tab);
				// these three belong to the logs screen and must be unique there.
				if k == key {
					t.Errorf("%s uses %q, already the key of %s", a, key, other)
				}
			}
		}
	}
}
