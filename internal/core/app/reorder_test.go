package app

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func entryAt(pod, container string, ms int) domain.LogEntry {
	return domain.LogEntry{Pod: pod, Container: container, Received: t0.Add(time.Duration(ms) * time.Millisecond), Message: fmt.Sprintf("%s/%s@%d", pod, container, ms)}
}

// The merge yields what a stable sort of everything would.
func TestReorderBufferMergesInTimeOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var b reorderBuffer
	var all []domain.LogEntry
	ms := map[string]int{}
	for range 5000 {
		pod := fmt.Sprintf("p%d", r.IntN(7))
		key := pod + "/c"
		ms[key] += r.IntN(5)
		at := ms[key]
		if r.IntN(50) == 0 { // a source clock going back
			at -= r.IntN(20)
		}
		e := entryAt(pod, "c", at)
		batch := []domain.LogEntry{e}
		if r.IntN(3) == 0 { // a batch of several lines, in read order
			for range r.IntN(5) {
				ms[key] += r.IntN(3)
				batch = append(batch, entryAt(pod, "c", ms[key]))
			}
		}
		b.add("ns", pod, "c", batch)
		all = append(all, batch...)
	}
	slices.SortStableFunc(all, compareEntries)
	var got []domain.LogEntry
	for b.len() > 0 {
		got = append(got, popOldest(&b))
	}
	if len(got) != len(all) {
		t.Fatalf("%d entries out, %d in", len(got), len(all))
	}
	for i := range got {
		if compareEntries(got[i], all[i]) != 0 {
			t.Fatalf("entry %d: %s, want %s", i, got[i].Message, all[i].Message)
		}
	}
}

func TestReorderBufferGivesMemoryBack(t *testing.T) {
	var b reorderBuffer
	burst := make([]domain.LogEntry, 5000)
	for i := range burst {
		burst[i] = entryAt("p", "c", i)
	}
	b.add("ns", "p", "c", burst)
	for b.len() > 0 {
		popOldest(&b)
	}
	if q := b.queues["ns/p/c"]; q.segs != nil {
		t.Fatalf("an empty queue keeps %d batches", len(q.segs))
	}
	b.forget("ns", "p")
	if len(b.queues) != 0 {
		t.Fatal("forget must drop the empty queues of the pod")
	}
}

// BenchmarkReorderCommit is one flush tick at 100 000 lines/s from 20
// containers: 3 300 new entries, 25 000 waiting (the 250 ms window), the
// 3 300 oldest due. A sort of everything waiting took ~16 ms per tick.
func BenchmarkReorderCommit(b *testing.B) {
	const containers, waiting, perTick = 20, 25000, 3300
	var rb reorderBuffer
	pods := make([]string, containers)
	for c := range pods {
		pods[c] = fmt.Sprintf("p%d", c)
	}
	next := make([]int, containers)
	feed := func(n int) { // as tailers send them: a batch per container
		for c := range containers {
			batch := make([]domain.LogEntry, 0, n/containers)
			for range n / containers {
				next[c] += containers
				batch = append(batch, domain.LogEntry{Pod: pods[c], Container: "c", Received: t0.Add(time.Duration(next[c]+c) * time.Millisecond)})
			}
			rb.add("ns", pods[c], "c", batch)
		}
	}
	feed(waiting)
	b.ReportAllocs()
	for b.Loop() {
		feed(perTick)
		for range perTick {
			popOldest(&rb)
		}
	}
}

// popOldest takes the oldest entry, as the session commits it.
func popOldest(b *reorderBuffer) domain.LogEntry {
	e, _ := b.oldest()
	v := *e
	b.drop()
	return v
}
