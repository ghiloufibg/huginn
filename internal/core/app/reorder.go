package app

import (
	"container/heap"
	"slices"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// reorderBuffer holds live entries while the reorder window lets lines of
// other containers catch up. Each container delivers its lines in time
// order, so it keeps one queue per container and merges their heads: taking
// the k due entries costs O(k log containers), however many entries wait
// (a sort of everything waiting on every tick grew with the line rate).
type reorderBuffer struct {
	queues map[string]*entryQueue // by namespace/pod/container
	heads  queueHeap              // non-empty queues, by their first entry
	n      int                    // entries held
}

// entryQueue is one container's waiting entries, oldest first, kept in
// the batches the tailer sent (each in time order): entries are not copied
// on their way through the reorder window.
type entryQueue struct {
	segs  [][]domain.LogEntry
	first int // index of the oldest waiting entry in segs[0]
	n     int // entries waiting
	index int // position in the heap, -1 when not in it
}

func (q *entryQueue) len() int               { return q.n }
func (q *entryQueue) head() *domain.LogEntry { return &q.segs[0][q.first] }
func (q *entryQueue) tail() *domain.LogEntry {
	last := q.segs[len(q.segs)-1]
	return &last[len(last)-1]
}

// push queues a batch of the container. The queue takes it over. A batch
// in time order after the waiting entries (the usual case) is queued as
// is; else (a source clock going back) the waiting entries and the batch
// are merged into one segment. It reports whether the head changed in a
// queue that was not empty.
func (q *entryQueue) push(batch []domain.LogEntry) (newHead bool) {
	ordered := q.n == 0 || compareEntryPtrs(q.tail(), &batch[0]) <= 0
	for i := 1; ordered && i < len(batch); i++ {
		ordered = compareEntryPtrs(&batch[i-1], &batch[i]) <= 0
	}
	if ordered {
		q.segs = append(q.segs, batch)
		q.n += len(batch)
		return false
	}
	merged := make([]domain.LogEntry, 0, q.n+len(batch))
	for i, seg := range q.segs {
		if i == 0 {
			seg = seg[q.first:]
		}
		merged = append(merged, seg...)
	}
	merged = append(merged, batch...)
	slices.SortStableFunc(merged, compareEntries)
	wasEmpty := q.n == 0
	q.segs, q.first, q.n = [][]domain.LogEntry{merged}, 0, len(merged)
	return !wasEmpty
}

// drop removes the head, once the caller copied it. Its strings are
// released at once; a batch is released once consumed.
func (q *entryQueue) drop() {
	q.segs[0][q.first] = domain.LogEntry{}
	q.first++
	q.n--
	if q.first < len(q.segs[0]) {
		return
	}
	q.segs[0] = nil
	q.segs, q.first = q.segs[1:], 0
	if len(q.segs) == 0 {
		q.segs = nil // the next batch does not grow an old array
	}
}

// add queues a batch of live entries of one container, in its read order.
// The buffer takes the batch over.
func (b *reorderBuffer) add(namespace, pod, container string, entries []domain.LogEntry) {
	if len(entries) == 0 {
		return
	}
	key := namespace + "/" + pod + "/" + container
	q := b.queues[key]
	if q == nil {
		if b.queues == nil {
			b.queues = map[string]*entryQueue{}
		}
		q = &entryQueue{index: -1}
		b.queues[key] = q
	}
	newHead := q.push(entries)
	b.n += len(entries)
	switch {
	case q.index < 0:
		heap.Push(&b.heads, q)
	case newHead:
		heap.Fix(&b.heads, q.index)
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

// drop removes the oldest waiting entry, once the caller copied it (see
// oldest). The buffer must not be empty.
func (b *reorderBuffer) drop() {
	q := b.heads[0]
	q.drop()
	b.n--
	if q.len() == 0 {
		heap.Pop(&b.heads)
	} else {
		heap.Fix(&b.heads, 0)
	}
}

// forget drops the queues of a pod that left; they are empty by then or
// soon flushed, so this only frees memory.
func (b *reorderBuffer) forget(namespace, pod string) {
	prefix := namespace + "/" + pod + "/"
	for key, q := range b.queues {
		if q.len() == 0 && strings.HasPrefix(key, prefix) {
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
