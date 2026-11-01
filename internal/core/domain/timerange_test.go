package domain

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimeRange(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	now := time.Date(2026, 11, 1, 16, 0, 0, 0, time.UTC) // 17:00 in Paris
	at := func(h, m, s int) time.Time { return time.Date(2026, 11, 1, h, m, s, 0, time.UTC) }

	tests := []struct {
		name, from, to string
		loc            *time.Location
		wantFrom       time.Time
		wantTo         time.Time // zero: open end
		err            string
	}{
		{name: "local minutes", from: "2026-11-01 14:30", to: "2026-11-01 14:35", loc: paris, wantFrom: at(13, 30, 0), wantTo: at(13, 35, 0)},
		{name: "local seconds with T", from: "2026-11-01T14:30:05", to: "2026-11-01T14:31:00", loc: paris, wantFrom: at(13, 30, 5), wantTo: at(13, 31, 0)},
		{name: "explicit zone wins", from: "2026-11-01T14:30:00Z", to: "2026-11-01T16:30:00+01:00", loc: paris, wantFrom: at(14, 30, 0), wantTo: at(15, 30, 0)},
		{name: "date alone is midnight", from: "2026-10-31", to: "2026-11-01", loc: paris, wantFrom: time.Date(2026, 10, 30, 23, 0, 0, 0, time.UTC), wantTo: time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)},
		{name: "clock time today", from: "14:30", to: "15:00", loc: paris, wantFrom: at(13, 30, 0), wantTo: at(14, 0, 0)},
		{name: "clock time later today means yesterday", from: "18:00", to: "now", loc: paris, wantFrom: at(17, 0, 0).AddDate(0, 0, -1), wantTo: now},
		{name: "now relative", from: "now-90m", to: "now-30m", loc: time.UTC, wantFrom: now.Add(-90 * time.Minute), wantTo: now.Add(-30 * time.Minute)},
		{name: "now with spaces and days", from: "now - 1d", to: "now-2h", loc: time.UTC, wantFrom: now.Add(-24 * time.Hour), wantTo: now.Add(-2 * time.Hour)},
		{name: "open end", from: "now-15m", to: "", loc: time.UTC, wantFrom: now.Add(-15 * time.Minute)},
		{name: "open end with blanks", from: "now-15m", to: "  ", loc: time.UTC, wantFrom: now.Add(-15 * time.Minute)},
		{name: "nil location is UTC", from: "2026-11-01 10:00", to: "2026-11-01 11:00", wantFrom: at(10, 0, 0), wantTo: at(11, 0, 0)},

		{name: "empty from", from: "", to: "now", err: "from: a start time is required"},
		{name: "garbage from", from: "yesterday", to: "now", err: `from: "yesterday"`},
		{name: "garbage to", from: "now-1h", to: "soon", err: `to: "soon"`},
		{name: "bad now suffix", from: "now+1h", to: "now", err: "now-<duration>"},
		{name: "bad now duration", from: "now-xyz", to: "now", err: "now-<duration>"},
		{name: "from in the future", from: "2026-11-02 10:00", to: "", err: "from: is in the future"},
		{name: "to in the future", from: "now-1h", to: "2026-11-02 10:00", err: "leave it empty to follow"},
		{name: "from after to", from: "2026-11-01 15:00", to: "2026-11-01 14:00", loc: time.UTC, err: "start must be before the end"},
		{name: "from equals to", from: "now-1h", to: "now-1h", err: "start must be before the end"},
		{name: "too short", from: "2026-11-01 14:00:00", to: "2026-11-01 14:00:00.5", loc: time.UTC, err: "at least"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := ParseTimeRange(tt.from, tt.to, now, tt.loc)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !w.IsRange() || !w.From.Equal(tt.wantFrom) || !w.To.Equal(tt.wantTo) {
				t.Fatalf("got [%v, %v), want [%v, %v)", w.From, w.To, tt.wantFrom, tt.wantTo)
			}
			if w.Closed() != !tt.wantTo.IsZero() {
				t.Fatalf("Closed() = %v for to %q", w.Closed(), tt.to)
			}
			if w.From.Location() != time.UTC || w.To.Location() != time.UTC && !w.To.IsZero() {
				t.Fatal("bounds must be stored in UTC")
			}
		})
	}
}

func TestParseTimeRangeDST(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	// 2026-10-25 02:30 happens twice in Paris (clocks go back at 03:00).
	w, err := ParseTimeRange("2026-10-25 02:30", "2026-10-25 04:00", now, paris)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.To.Sub(w.From); got <= 0 || got > 3*time.Hour {
		t.Fatalf("range across the DST change lasts %v", got)
	}
	// 2026-03-29 02:30 does not exist in Paris: it must still resolve to some instant.
	if _, err := ParseTimeRange("2026-03-29 02:30", "2026-03-29 04:00", now, paris); err != nil {
		t.Fatalf("a time skipped by DST must still parse: %v", err)
	}
}

func TestRangeWindow(t *testing.T) {
	from := time.Date(2026, 11, 1, 14, 30, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	w, err := NewRange(from.Add(0), to)
	if err != nil {
		t.Fatal(err)
	}
	open, err := NewRange(from, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case !w.IsRange() || !w.Closed() || !w.FromStart() || w.IsTail() || w.IsHead():
		t.Fatalf("closed range predicates wrong: %+v", w)
	case !open.IsRange() || open.Closed() || !open.FromStart():
		t.Fatalf("open range predicates wrong: %+v", open)
	case (TimeWindow{Since: time.Hour}).IsRange() || (TimeWindow{Tail: 5}).FromStart() || !(TimeWindow{Head: 5}).FromStart():
		t.Fatal("other windows misclassified")
	}
	if got, want := w.String(), "2026-11-01T14:30:00Z..2026-11-01T14:35:00Z"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := open.String(), "2026-11-01T14:30:00Z.."; got != want {
		t.Errorf("open String() = %q, want %q", got, want)
	}
	if w.Label() != "range" {
		t.Errorf("Label() = %q", w.Label())
	}
	if !w.Equal(w) || w.Equal(open) || w.Equal(TimeWindow{Since: time.Hour}) {
		t.Error("Equal is wrong")
	}
	// Zone and monotonic reading must not matter.
	if paris := time.FixedZone("x", 3600); !w.Equal(TimeWindow{From: from.In(paris), To: to.In(paris)}) {
		t.Error("Equal must compare instants")
	}
	if w != (TimeWindow{From: from, To: to}) {
		t.Error("NewRange must store UTC bounds so == works")
	}
	for _, c := range []struct{ from, to time.Time }{
		{time.Time{}, to}, {from, from}, {from, from.Add(-time.Second)}, {from, from.Add(time.Millisecond)},
	} {
		if _, err := NewRange(c.from, c.to); err == nil {
			t.Errorf("NewRange(%v, %v) must fail", c.from, c.to)
		}
	}
}

func TestSelectWindowRange(t *testing.T) {
	base := time.Date(2026, 11, 1, 14, 0, 0, 0, time.UTC)
	line := func(m int, text string) RawLine {
		return RawLine{Time: base.Add(time.Duration(m) * time.Minute), Text: text}
	}
	lines := []RawLine{line(0, "a"), line(30, "b"), line(31, "c"), {Text: "no time"}, line(35, "d"), line(36, "e")}
	w, err := NewRange(base.Add(30*time.Minute), base.Add(35*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range SelectWindow(lines, w, base) {
		got = append(got, l.Text)
	}
	if want := "b,c,no time"; strings.Join(got, ",") != want { // From inclusive, To exclusive
		t.Fatalf("closed range = %v, want %s", got, want)
	}
	open, _ := NewRange(base.Add(31*time.Minute), time.Time{})
	got = nil
	for _, l := range SelectWindow(lines, open, base) {
		got = append(got, l.Text)
	}
	if want := "c,no time,d,e"; strings.Join(got, ",") != want {
		t.Fatalf("open range = %v, want %s", got, want)
	}
}

func TestPageCursorSkipFor(t *testing.T) {
	var none *PageCursor
	if none.SkipFor("p", "c") != 0 {
		t.Fatal("a nil cursor skips nothing")
	}
	c := &PageCursor{Skip: map[string]int{SkipKey("p", "c"): 2}}
	if c.SkipFor("p", "c") != 2 || c.SkipFor("p", "other") != 0 || c.SkipFor("q", "c") != 0 {
		t.Fatal("SkipFor must be exact per pod and container")
	}
}
