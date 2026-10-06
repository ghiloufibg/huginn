package app

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

type countingDecoder struct{ n *atomic.Int64 }

func (d countingDecoder) Decode(l domain.RawLine) domain.LogEntry {
	d.n.Add(1)
	return domain.LogEntry{Time: l.Time, Pod: l.Pod, Message: l.Text, Container: l.Container}
}

// loggerDecoder reads lines "logger message".
type loggerDecoder struct{}

func (loggerDecoder) Decode(l domain.RawLine) domain.LogEntry {
	logger, msg, _ := strings.Cut(l.Text, " ")
	return domain.LogEntry{Time: l.Time, Pod: l.Pod, Logger: logger, Message: msg, Raw: l.Text, Structured: true}
}

var histAt = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

// collect offers the lines of each container from its own goroutine, as
// tailers do, skipping what the collector no longer admits; it returns the
// lines skipped so.
func collect(c *historyCollector, f *ports.LogFormat, containers [][]domain.RawLine) int {
	var wg sync.WaitGroup
	var skipped atomic.Int64
	for _, lines := range containers {
		wg.Go(func() {
			q := c.newQueue()
			var batch []histItem
			for i, l := range lines {
				if !c.admits(l.Time) {
					skipped.Add(1)
					continue
				}
				batch = append(batch, histItem{line: l, format: f, seq: uint64(i)})
				if len(batch) == historyOfferBatch {
					c.offer(q, batch, f.Mute != nil)
					batch = batch[:0]
				}
			}
			c.offer(q, batch, f.Mute != nil)
		})
	}
	wg.Wait()
	return int(skipped.Load())
}

// interleaved returns n lines per container, the containers' lines
// interleaved in time.
func interleaved(containers, n int, text func(c, i int) string) [][]domain.RawLine {
	out := make([][]domain.RawLine, containers)
	for c := range containers {
		for i := range n {
			out[c] = append(out[c], domain.RawLine{
				Time: histAt.Add(time.Duration(i*containers+c) * time.Millisecond), Pod: fmt.Sprint("p", c), Text: text(c, i),
			})
		}
	}
	return out
}

func TestHistoryKeepsTheNewestAndDecodesOnlyThem(t *testing.T) {
	var decoded atomic.Int64
	f := &ports.LogFormat{Decoder: countingDecoder{&decoded}}
	c := newHistoryCollector(9000, false)
	skipped := collect(c, f, interleaved(3, 10000, func(int, int) string { return "x" }))
	items, evicted := c.close()
	out, notDecoded, _ := decodeKept(items, 9000, false)
	if len(out) != 9000 || skipped+evicted+notDecoded != 21000 {
		t.Fatalf("kept %d, left out %d+%d+%d", len(out), skipped, evicted, notDecoded)
	}
	if decoded.Load() != 9000 {
		t.Fatalf("decoded %d lines, want only the 9000 kept", decoded.Load())
	}
	if want := histAt.Add(21000 * time.Millisecond); !out[0].Time.Equal(want) {
		t.Fatalf("oldest kept %v, want %v", out[0].Time, want)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Time.Before(out[i-1].Time) {
			t.Fatalf("entry %d out of order", i)
		}
	}
}

func TestHistoryOfHeadsKeepsTheOldest(t *testing.T) {
	var decoded atomic.Int64
	f := &ports.LogFormat{Decoder: countingDecoder{&decoded}}
	c := newHistoryCollector(1200, true)
	collect(c, f, interleaved(3, 1000, func(int, int) string { return "x" }))
	items, _ := c.close()
	out, _, _ := decodeKept(items, 1200, true)
	if len(out) != 1200 || decoded.Load() != 1200 {
		t.Fatalf("kept %d, decoded %d", len(out), decoded.Load())
	}
	if !out[0].Time.Equal(histAt) || !out[1199].Time.Equal(histAt.Add(1199*time.Millisecond)) {
		t.Fatalf("kept %v … %v, want the oldest 1200 lines", out[0].Time, out[1199].Time)
	}
}

// Muted lines do not use up the room of the lines shown: a format that
// mutes keeps twice the buffer undecoded, and decoding goes on until the
// buffer is full.
func TestHistoryMutedLinesDoNotUseTheRoom(t *testing.T) {
	mute, err := domain.NewLoggerMute([]string{"pool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &ports.LogFormat{Decoder: loggerDecoder{}, Mute: mute}
	c := newHistoryCollector(100, false)
	collect(c, f, interleaved(2, 500, func(_, i int) string {
		if i%2 == 0 {
			return "pool stats"
		}
		return "app line"
	}))
	items, _ := c.close()
	if len(items) != 200 {
		t.Fatalf("%d lines kept undecoded, want 200 (twice the buffer)", len(items))
	}
	out, _, muted := decodeKept(items, 100, false)
	if len(out) != 100 || muted["pool"] != 100 {
		t.Fatalf("%d shown, muted %v; want 100 and pool:100", len(out), muted)
	}
	for _, e := range out {
		if e.Logger != "app" {
			t.Fatalf("muted line shown: %+v", e)
		}
	}
}

// Lines without a source time cannot be cut by time; they are still
// bounded, and kept in read order.
func TestHistoryWithoutSourceTimesStaysBounded(t *testing.T) {
	var decoded atomic.Int64
	f := &ports.LogFormat{Decoder: countingDecoder{&decoded}}
	lines := make([]domain.RawLine, 10)
	for i := range lines {
		lines[i] = domain.RawLine{Pod: "p", Text: fmt.Sprint(i)}
	}
	c := newHistoryCollector(5, false)
	collect(c, f, [][]domain.RawLine{lines})
	items, _ := c.close()
	out, _, _ := decodeKept(items, 5, false)
	if len(out) != 5 || out[0].Message != "5" || out[4].Message != "9" {
		t.Fatalf("kept %v", out)
	}
}

func TestHistoryRefusesOnceClosed(t *testing.T) {
	c := newHistoryCollector(10, false)
	c.close()
	if c.offer(c.newQueue(), []histItem{{line: domain.RawLine{Time: histAt}}}, false) || !c.isClosed() {
		t.Fatal("a closed collector accepted lines")
	}
}

// BenchmarkHistory20Pods loads a window of 20 pods × 50 000 lines into a
// 50 000-line buffer: what tailers offer, the cut and the decoding. The
// former implementation held every raw line and sorted 1 000 000 times
// (278 ms and 24 MB for the sort alone).
func BenchmarkHistory20Pods(b *testing.B) {
	containers := interleaved(20, 50000, func(int, int) string { return "x" })
	var decoded atomic.Int64
	f := &ports.LogFormat{Decoder: countingDecoder{&decoded}}
	b.ReportAllocs()
	for b.Loop() {
		c := newHistoryCollector(50000, false)
		collect(c, f, containers)
		items, _ := c.close()
		decodeKept(items, 50000, false)
	}
}

// A container that fills the collector alone, before the others, gives the
// memory back as its lines are evicted: the collector holds about one
// buffer, not one per container.
func TestHistoryQueuesGiveMemoryBack(t *testing.T) {
	f := &ports.LogFormat{Decoder: loggerDecoder{}}
	c := newHistoryCollector(10000, false)
	containers := interleaved(10, 10000, func(int, int) string { return "x" })
	var qs []*histQueue
	for _, lines := range containers { // one after the other: the worst case
		q := c.newQueue()
		qs = append(qs, q)
		for i := 0; i < len(lines); i += historyOfferBatch {
			var batch []histItem
			for j, l := range lines[i:min(i+historyOfferBatch, len(lines))] {
				batch = append(batch, histItem{line: l, format: f, seq: uint64(i + j)})
			}
			c.offer(q, batch, false)
		}
	}
	capacity := 0
	for _, q := range qs {
		capacity += cap(q.items)
	}
	if capacity > 3*10000 {
		t.Fatalf("queues hold %d lines of capacity for a 10 000-line buffer", capacity)
	}
}
