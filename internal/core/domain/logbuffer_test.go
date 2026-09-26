package domain

import "testing"

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
