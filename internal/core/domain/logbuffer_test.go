package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLogBufferWrapsAndCountsDrops(t *testing.T) {
	b := NewLogBuffer(3)
	for i := range 5 {
		b.Append(LogEntry{Message: string(rune('a' + i))})
	}
	if b.Len() != 3 || b.Cap() != 3 || b.Dropped() != 2 {
		t.Fatalf("len %d cap %d dropped %d", b.Len(), b.Cap(), b.Dropped())
	}
	got := ""
	for i := range b.Len() {
		got += b.At(i).Message
	}
	if got != "cde" {
		t.Fatalf("order %q", got)
	}
	if b.FirstSeq() != 3 || b.At(0).Seq != 3 || b.At(2).Seq != 5 {
		t.Fatalf("seqs: first %d", b.FirstSeq())
	}
	if i, ok := b.Index(4); !ok || i != 1 {
		t.Fatalf("index of 4: %d %v", i, ok)
	}
	for _, evicted := range []uint64{1, 2, 6} {
		if _, ok := b.Index(evicted); ok {
			t.Fatalf("seq %d must not be found", evicted)
		}
	}
}

func TestLogBufferReset(t *testing.T) {
	b := NewLogBuffer(2)
	b.Append(LogEntry{})
	b.Append(LogEntry{})
	b.Append(LogEntry{})
	b.Reset()
	if b.Len() != 0 || b.Dropped() != 0 {
		t.Fatal("reset")
	}
	if seq := b.Append(LogEntry{}); seq != 4 || b.FirstSeq() != 4 {
		t.Fatalf("seq after reset %d", seq)
	}
}

func BenchmarkLogBufferAppend(b *testing.B) {
	buf := NewLogBuffer(50000)
	e := LogEntry{Message: "request completed", Logger: "a.b.C", Level: LevelInfo}
	for b.Loop() {
		buf.Append(e)
	}
}

func TestInsertLate(t *testing.T) {
	at := func(s int) time.Time { return time.Date(2026, 9, 27, 8, 30, s, 0, time.UTC) }
	b := NewLogBuffer(4)
	for _, s := range []int{1, 5, 6} {
		b.Append(LogEntry{Received: at(s), Message: fmt.Sprint(s)})
	}
	renumber := b.InsertLate([]LogEntry{{Received: at(3), Message: "3"}, {Received: at(2), Message: "2"}})
	var got []string
	for i := range b.Len() {
		got = append(got, b.At(i).Message)
	}
	if strings.Join(got, ",") != "2,3,5,6" || b.Dropped() != 1 {
		t.Fatalf("got %v, dropped %d", got, b.Dropped())
	}
	if _, ok := renumber(1); ok {
		t.Error("entry 1 was evicted")
	}
	if seq, ok := renumber(2); !ok || b.At(int(seq-b.FirstSeq())).Message != "5" {
		t.Error("entry 2 (5) renumbered wrong")
	}
}
