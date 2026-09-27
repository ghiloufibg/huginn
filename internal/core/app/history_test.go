package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
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
		hs = append(hs, rawHistory{lines: lines, dec: countingDecoder{&decoded[c]}})
	}
	out, dropped := decodeHistory(hs, 9000, false)
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
		hs = append(hs, rawHistory{lines: lines, dec: countingDecoder{&decoded[c]}})
	}
	out, dropped := decodeHistory(hs, 1200, true)
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
	hs := []rawHistory{{lines: make([]domain.RawLine, 10), dec: countingDecoder{&n}}}
	if out, dropped := decodeHistory(hs, 5, false); len(out) != 10 || dropped != 0 || n != 10 {
		t.Fatalf("no time to cut on: %d kept, %d dropped", len(out), dropped)
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
