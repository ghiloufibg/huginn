package app

import (
	"container/heap"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// reorderBuffer holds live entries while the reorder window lets lines of
// other containers catch up. Each container delivers its lines in time
// order, so it keeps one queue per container and merges their heads: taking
// the k due entries costs O(k log containers), however many entries wait
// (a sort of everything waiting on every tick grew with the line rate).
type reorderBuffer struct {
	queues map[string]*entryQueue // by pod/container
	heads  queueHeap              // non-empty queues, by their first entry
	n      int                    // entries held
}

// entryQueue is one container's waiting entries, oldest first.
type entryQueue struct {
	entries []domain.LogEntry
	first   int // index of the oldest entry still waiting
	index   int // position in the heap, -1 when not in it
}

func (q *entryQueue) len() int               { return len(q.entries) - q.first }
func (q *entryQueue) head() *domain.LogEntry { return &q.entries[q.first] }

// push adds an entry at its place by time: at the end in the usual case,
// else (a source clock going back) by binary search. It reports whether
// the entry became the head of a non-empty queue.
func (q *entryQueue) push(e domain.LogEntry) (newHead bool) {
	n := len(q.entries)
	if q.len() == 0 || compareEntryPtrs(&q.entries[n-1], &e) <= 0 {
		q.entries = append(q.entries, e)
		return false
	}
	lo, hi := q.first, n
	for lo < hi {
		mid := (lo + hi) / 2
		if compareEntryPtrs(&q.entries[mid], &e) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	q.entries = append(q.entries, domain.LogEntry{})
	copy(q.entries[lo+1:], q.entries[lo:n])
	q.entries[lo] = e
	return lo == q.first
}

// pop removes the oldest entry; the storage is reused once the queue is
// empty or mostly consumed.
func (q *entryQueue) pop() domain.LogEntry {
	e := q.entries[q.first]
	q.entries[q.first] = domain.LogEntry{} // release its strings
	q.first++
	switch {
	case q.first == len(q.entries) && cap(q.entries) > 1024:
		q.entries, q.first = nil, 0 // a burst is over: give its memory back
	case q.first == len(q.entries):
		q.entries, q.first = q.entries[:0], 0
	case q.first > 1024 && q.first > len(q.entries)/2:
		n := copy(q.entries, q.entries[q.first:])
		clear(q.entries[n:])
		q.entries, q.first = q.entries[:n], 0
	}
	return e
}

// add queues live entries of one container.
func (b *reorderBuffer) add(pod, container string, entries []domain.LogEntry) {
	if len(entries) == 0 {
		return
	}
	key := pod + "/" + container
	q := b.queues[key]
	if q == nil {
		if b.queues == nil {
			b.queues = map[string]*entryQueue{}
		}
		q = &entryQueue{index: -1}
		b.queues[key] = q
	}
	for _, e := range entries {
		newHead := q.push(e)
		b.n++
		switch {
		case q.index < 0:
			heap.Push(&b.heads, q)
		case newHead:
			heap.Fix(&b.heads, q.index)
		}
	}
}

// len is the number of entries waiting.
func (b *reorderBuffer) len() int { return b.n }

// oldest returns the oldest waiting entry, if any.
func (b *reorderBuffer) oldest() (*domain.LogEntry, bool) {
	if len(b.heads) == 0 {
		return nil, false
	}
	return b.heads[0].head(), true
}

// pop removes the oldest waiting entry. The buffer must not be empty.
func (b *reorderBuffer) pop() domain.LogEntry {
	q := b.heads[0]
	e := q.pop()
	b.n--
	if q.len() == 0 {
		heap.Pop(&b.heads)
	} else {
		heap.Fix(&b.heads, 0)
	}
	return e
}

// forget drops the queues of a container that left (its pod is gone);
// they are empty by then or soon flushed, so this only frees memory.
func (b *reorderBuffer) forget(pod string) {
	for key, q := range b.queues {
		if q.len() == 0 && strings.HasPrefix(key, pod+"/") {
			delete(b.queues, key)
		}
	}
}

// queueHeap orders the non-empty queues by their oldest entry.
type queueHeap []*entryQueue

func (h queueHeap) Len() int           { return len(h) }
func (h queueHeap) Less(i, j int) bool { return compareEntryPtrs(h[i].head(), h[j].head()) < 0 }
func (h queueHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}

func (h *queueHeap) Push(x any) {
	q := x.(*entryQueue)
	q.index = len(*h)
	*h = append(*h, q)
}

func (h *queueHeap) Pop() any {
	old := *h
	q := old[len(old)-1]
	old[len(old)-1] = nil
	q.index = -1
	*h = old[:len(old)-1]
	return q
}
