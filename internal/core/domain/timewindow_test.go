package domain

import (
	"testing"
	"time"
)

func TestParseTimeWindow(t *testing.T) {
	tests := []struct {
		in      string
		want    TimeWindow
		wantErr bool
	}{
		{"15m", TimeWindow{Since: 15 * time.Minute}, false},
		{" 1H ", TimeWindow{Since: time.Hour}, false},
		{"2d", TimeWindow{Since: 48 * time.Hour}, false},
		{"90s", TimeWindow{Since: 90 * time.Second}, false},
		{"tail", TimeWindow{Tail: 300}, false},
		{"tail 200", TimeWindow{Tail: 200}, false},
		{"tail:50", TimeWindow{Tail: 50}, false},
		{"tail -1", TimeWindow{}, true},
		{"0m", TimeWindow{}, true},
		{"-5m", TimeWindow{}, true},
		{"xd", TimeWindow{}, true},
		{"", TimeWindow{}, true},
		{"soon", TimeWindow{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseTimeWindow(tt.in, 300)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseTimeWindowDefaultTail(t *testing.T) {
	got, err := ParseTimeWindow("tail", 0)
	if err != nil || got.Tail != DefaultTailLines {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestTimeWindowStringRoundTrip(t *testing.T) {
	for _, w := range DefaultWindowPresets(500) {
		back, err := ParseTimeWindow(w.String(), 1)
		if err != nil || back != w {
			t.Fatalf("%v -> %q -> %+v, %v", w, w.String(), back, err)
		}
	}
}

func TestPresetLabels(t *testing.T) {
	want := []string{"15m", "30m", "40m", "45m", "1h", "1d", "2d", "tail"}
	for i, w := range DefaultWindowPresets(500) {
		if w.Label() != want[i] {
			t.Errorf("preset %d label = %q, want %q", i, w.Label(), want[i])
		}
	}
}

func TestNextWindowWraps(t *testing.T) {
	p := DefaultWindowPresets(500)
	if got := NextWindow(p, p[len(p)-1]); got != p[0] {
		t.Fatalf("wrap: got %v", got)
	}
	if got := NextWindow(p, p[0]); got != p[1] {
		t.Fatalf("next: got %v", got)
	}
	if got := NextWindow(p, TimeWindow{Since: 7 * time.Minute}); got != p[0] {
		t.Fatalf("unknown: got %v", got)
	}
}

func TestSelectWindow(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	var lines []RawLine
	for i := 10; i >= 0; i-- {
		lines = append(lines, RawLine{Time: now.Add(-time.Duration(i) * time.Minute), Text: "x"})
	}
	if got := SelectWindow(lines, TimeWindow{Tail: 3}, now); len(got) != 3 || got[2].Time != now {
		t.Fatalf("tail: %d lines", len(got))
	}
	if got := SelectWindow(lines, TimeWindow{Tail: 50}, now); len(got) != 11 {
		t.Fatalf("tail larger than history: %d", len(got))
	}
	if got := SelectWindow(lines, TimeWindow{Since: 5 * time.Minute}, now); len(got) != 6 {
		t.Fatalf("since: %d lines", len(got))
	}
	if got := SelectWindow(lines, TimeWindow{Since: time.Minute}, now.Add(time.Hour)); got != nil {
		t.Fatalf("empty window: %d lines", len(got))
	}
}
