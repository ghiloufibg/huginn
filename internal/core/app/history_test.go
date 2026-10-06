package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

type countingDecoder struct{ n *int }

func (d countingDecoder) Decode(l domain.RawLine) domain.LogEntry {
	*d.n++
	return domain.LogEntry{Time: l.Time, Message: l.Text, Container: l.Container}
}

func TestDecodeHistoryDecodesOnlyWhatIsKept(t *testing.T) {
	at := time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)
	var hs []rawHistory
	decoded := make([]int, 3)
	for c := range 3 {
		var lines []domain.RawLine
		for i := range 10000 {
			lines = append(lines, domain.RawLine{Time: at.Add(time.Duration(i*3+c) * time.Millisecond), Container: fmt.Sprint(c), Text: "x"})
		}
		hs = append(hs, rawHistory{lines: lines, format: ports.LogFormat{Decoder: countingDecoder{&decoded[c]}}})
	}
	out, dropped, _ := decodeHistory(hs, 9000, false)
	if len(out) != 9000 || dropped != 21000 {
		t.Fatalf("kept %d, dropped %d", len(out), dropped)
	}
	if n := decoded[0] + decoded[1] + decoded[2]; n != 9000 {
		t.Fatalf("decoded %d lines, want only the 9000 kept", n)
	}
	if want := at.Add(21000 * time.Millisecond); !minTime(out).Equal(want) {
		t.Fatalf("oldest kept %v, want %v", minTime(out), want)
	}
}

func TestDecodeHistoryOfHeadsKeepsTheOldest(t *testing.T) {
	at := time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)
	var hs []rawHistory
	decoded := make([]int, 3)
	for c := range 3 {
		var lines []domain.RawLine
		for i := range 1000 {
			lines = append(lines, domain.RawLine{Time: at.Add(time.Duration(i*3+c) * time.Millisecond), Container: fmt.Sprint(c), Text: "x"})
		}
		hs = append(hs, rawHistory{lines: lines, format: ports.LogFormat{Decoder: countingDecoder{&decoded[c]}}})
	}
	out, dropped, _ := decodeHistory(hs, 1200, true)
	if n := decoded[0] + decoded[1] + decoded[2]; len(out) != 1200 || dropped != 1800 || n != 1200 {
		t.Fatalf("kept %d, dropped %d, decoded %v", len(out), dropped, decoded)
	}
	newest := out[0].Time
	for _, e := range out {
		if e.Time.After(newest) {
			newest = e.Time
		}
	}
	if !minTime(out).Equal(at) || !newest.Equal(at.Add(1199*time.Millisecond)) {
		t.Fatalf("kept %v … %v, want the oldest 1200 lines", minTime(out), newest)
	}
}

func TestDecodeHistoryWithoutSourceTimesKeepsAll(t *testing.T) {
	n := 0
	hs := []rawHistory{{lines: make([]domain.RawLine, 10), format: ports.LogFormat{Decoder: countingDecoder{&n}}}}
	if out, dropped, _ := decodeHistory(hs, 5, false); len(out) != 10 || dropped != 0 || n != 10 {
		t.Fatalf("no time to cut on: %d kept, %d dropped", len(out), dropped)
	}
}

// loggerDecoder reads lines "logger message".
type loggerDecoder struct{}

func (loggerDecoder) Decode(l domain.RawLine) domain.LogEntry {
	logger, msg, _ := strings.Cut(l.Text, " ")
	return domain.LogEntry{Time: l.Time, Logger: logger, Message: msg, Raw: l.Text, Structured: true}
}

func TestDecodeHistoryLeavesOutMutedLoggers(t *testing.T) {
	mute, err := domain.NewLoggerMute([]string{"pool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var lines []domain.RawLine
	for i := range 10 {
		logger := "app"
		if i%2 == 0 {
			logger = "pool"
		}
		lines = append(lines, domain.RawLine{Time: at.Add(time.Duration(i) * time.Second), Text: logger + " line"})
	}
	hs := []rawHistory{{lines: lines, format: ports.LogFormat{Decoder: loggerDecoder{}, Mute: mute}}}
	out, dropped, muted := decodeHistory(hs, 8, false)
	// The cut keeps the newest 8 raw lines, 4 of them muted.
	if len(out) != 4 || dropped != 2 || len(muted) != 1 || muted["pool"] != 4 {
		t.Fatalf("%d kept, %d dropped, muted %v; want 4, 2, pool:4", len(out), dropped, muted)
	}
	for _, e := range out {
		if e.Logger != "app" {
			t.Fatalf("muted line kept: %+v", e)
		}
	}
}

func minTime(es []domain.LogEntry) time.Time {
	m := es[0].Time
	for _, e := range es {
		if e.Time.Before(m) {
			m = e.Time
		}
	}
	return m
}
