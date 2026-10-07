package app

import (
	"container/heap"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// muteBudget is how many more lines than the buffer a container whose
// format mutes loggers reads, and the history keeps undecoded: muted
// lines are known only once decoded, and they must not use up the room of
// the lines shown.
const muteBudget = 2

// histItem is a history line, kept undecoded until the window is cut.
type histItem struct {
	line   domain.RawLine
	format *ports.LogFormat // decodes it
	entry  *domain.LogEntry // already decoded (heads), else nil
	seq    uint64           // read order in its container, for equal times
}

// compareItems orders history lines by source time, then pod, then read
// order: the order entries are shown in.
func compareItems(a, b *histItem) int {
	if c := a.line.Time.Compare(b.line.Time); c != 0 {
		return c
	}
	if c := strings.Compare(a.line.Pod, b.line.Pod); c != 0 {
		return c
	}
	switch {
	case a.seq < b.seq:
		return -1
	case a.seq > b.seq:
		return 1
	}
	return 0
}

// historyCollector gathers the history lines of all the containers of a
// session while they are read, keeping only those that can be shown: the
// newest (or, for a head, the oldest) up to the buffer size. Its memory is
// bounded by the buffer, whatever the number of containers and the size
// of the window; lines are decoded only once the cut is known.
//
// Each container reads its lines in time order, so the collector keeps
// one queue per container and evicts from the queue whose end is oldest
// (newest for a head), found with a heap of the queues: O(log containers)
// per line. Once it is full, the time beyond which a line cannot enter is
// published without a lock, so tailers skip such lines as they read them.
type historyCollector struct {
	oldest bool // keep the oldest lines (a head) instead of the newest
	limit  int  // entries shown

	mu      sync.Mutex
	size    int // lines kept undecoded: limit, or limit × muteBudget
	n       int // lines kept
	queues  histQueues
	evicted int
	closed  bool

	full  atomic.Bool
	bound atomic.Int64 // time (Unix ns) of the next line evicted, when full
}

func newHistoryCollector(limit int, oldest bool) *historyCollector {
	return &historyCollector{oldest: oldest, limit: limit, size: limit, queues: histQueues{oldest: oldest}}
}

// histQueue is one container's kept lines, in read order.
type histQueue struct {
	items []histItem
	first int // index of the first line still kept
	index int // position in the heap, -1 when not in it
}

func (q *histQueue) len() int { return len(q.items) - q.first }

// end is the line evicted next from this queue: its oldest, or its
// newest for a head.
func (q *histQueue) end(oldest bool) *histItem {
	if oldest {
		return &q.items[len(q.items)-1]
	}
	return &q.items[q.first]
}

// evict removes the line end returns.
func (q *histQueue) evict(oldest bool) {
	if oldest {
		q.items[len(q.items)-1] = histItem{}
		q.items = q.items[:len(q.items)-1]
		q.compact()
		return
	}
	q.items[q.first] = histItem{}
	q.first++
	q.compact()
}

// compact reuses the storage of evicted lines, and gives it back when it
// is much larger than what is kept: a container that filled the collector
// alone before the others must not keep a buffer's worth of memory.
func (q *histQueue) compact() {
	live := q.len()
	switch {
	case live == 0 && cap(q.items) > 1024:
		q.items, q.first = nil, 0 // a burst is over: give its memory back
	case live == 0:
		q.items, q.first = q.items[:0], 0
	case q.first > 1024 && q.first > len(q.items)/2 && cap(q.items) > 8*live:
		q.items, q.first = append(make([]histItem, 0, 2*live), q.items[q.first:]...), 0
	case q.first > 1024 && q.first > len(q.items)/2:
		n := copy(q.items, q.items[q.first:])
		clear(q.items[n:])
		q.items, q.first = q.items[:n], 0
	}
}

// histQueues is a heap of the non-empty queues whose root holds the line
// evicted next.
type histQueues struct {
	oldest bool
	qs     []*histQueue
}

func (h *histQueues) Len() int { return len(h.qs) }
func (h *histQueues) Less(i, j int) bool {
	c := compareItems(h.qs[i].end(h.oldest), h.qs[j].end(h.oldest))
	if h.oldest {
		return c > 0
	}
	return c < 0
}

func (h *histQueues) Swap(i, j int) {
	h.qs[i], h.qs[j] = h.qs[j], h.qs[i]
	h.qs[i].index, h.qs[j].index = i, j
}

func (h *histQueues) Push(x any) {
	q := x.(*histQueue)
	q.index = len(h.qs)
	h.qs = append(h.qs, q)
}

func (h *histQueues) Pop() any {
	n := len(h.qs) - 1
	q := h.qs[n]
	h.qs[n], q.index = nil, -1
	h.qs = h.qs[:n]
	return q
}

// newQueue returns the queue of a container's lines.
func (c *historyCollector) newQueue() *histQueue { return &histQueue{index: -1} }

// admits reports whether a line of this source time can still enter: it
// is false only for lines beyond the bound of a full collector.
func (c *historyCollector) admits(t time.Time) bool {
	if !c.full.Load() || t.IsZero() {
		return true
	}
	bound := c.bound.Load()
	if c.oldest {
		return t.UnixNano() <= bound
	}
	return t.UnixNano() >= bound
}

// offer adds lines of one container, read after those it offered before.
// muting tells that its format mutes loggers: the collector then keeps
// muteBudget times more lines. It returns false, keeping nothing, once
// the collector is closed.
func (c *historyCollector) offer(q *histQueue, items []histItem, muting bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if muting {
		c.size = c.limit * muteBudget
	}
	for _, it := range items {
		if c.n >= c.size && c.beyond(&it) { // would be evicted at once
			c.evicted++
			continue
		}
		q.items = append(q.items, it)
		c.n++
		switch {
		case q.index < 0:
			heap.Push(&c.queues, q)
		case c.oldest: // its newest line changed
			heap.Fix(&c.queues, q.index)
		}
		for c.n > c.size {
			root := c.queues.qs[0]
			root.evict(c.oldest)
			c.n--
			c.evicted++
			if root.len() == 0 {
				heap.Pop(&c.queues)
			} else {
				heap.Fix(&c.queues, 0)
			}
		}
	}
	if c.n >= c.size && c.n > 0 {
		c.bound.Store(c.queues.qs[0].end(c.oldest).line.Time.UnixNano())
		c.full.Store(true)
	}
	return true
}

// beyond reports whether a line comes after the next line evicted, in the
// eviction order: it would be evicted first. c.queues must not be empty.
func (c *historyCollector) beyond(it *histItem) bool {
	cmp := compareItems(it, c.queues.qs[0].end(c.oldest))
	if c.oldest {
		return cmp > 0
	}
	return cmp < 0
}

// isClosed reports whether the history was cut already.
func (c *historyCollector) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// close ends the collection. It returns the lines kept, in time order,
// and the number of lines left out so far.
func (c *historyCollector) close() ([]histItem, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]histItem, 0, c.n)
	for _, q := range c.queues.qs {
		items = append(items, q.items[q.first:]...)
	}
	c.queues.qs, c.closed = nil, true
	slices.SortFunc(items, func(a, b histItem) int { return compareItems(&a, &b) })
	return items, c.evicted
}

// decodeChunk is the number of lines one goroutine decodes at a time.
const decodeChunk = 4096

// decodeKept decodes history lines (in time order) from the side that is
// kept, the newest or, for a head, the oldest, in parallel chunks, until
// limit entries are kept: lines past that are never decoded, and each round
// decodes only the chunks still needed. It returns the entries in time
// order, the lines left out and the muted lines per pattern.
func decodeKept(items []histItem, limit int, oldest bool) (entries []domain.LogEntry, dropped int, muted map[string]int) {
	type job struct {
		items []histItem
		out   []domain.LogEntry
		muted map[string]int
	}
	var chunks [][]domain.LogEntry // in decoding order: from the side kept
	kept, done := 0, 0
	workers := runtime.GOMAXPROCS(0)
	for kept < limit && done < len(items) {
		needed := (limit - kept + decodeChunk - 1) / decodeChunk // if nothing is muted
		var jobs []*job
		for range min(workers, needed) {
			if done == len(items) {
				break
			}
			n := min(decodeChunk, len(items)-done)
			lo, hi := done, done+n // from the start for a head
			if !oldest {
				lo, hi = len(items)-done-n, len(items)-done
			}
			jobs = append(jobs, &job{items: items[lo:hi]})
			done += n
		}
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Go(func() { j.out, j.muted = decodeItems(j.items) })
		}
		wg.Wait()
		for _, j := range jobs {
			chunks = append(chunks, j.out)
			kept += len(j.out)
			for p, n := range j.muted {
				if muted == nil {
					muted = map[string]int{}
				}
				muted[p] += n
			}
		}
	}
	// Keep the limit entries nearest the side kept, copied once into a
	// slice of their exact size, in time order.
	extra := max(kept-limit, 0)
	entries = make([]domain.LogEntry, 0, kept-extra)
	if oldest {
		for _, c := range chunks {
			entries = append(entries, c[:min(len(c), limit-len(entries))]...)
		}
	} else {
		skip := extra // the oldest decoded, in the last chunk decoded
		for i := len(chunks) - 1; i >= 0; i-- {
			c := chunks[i]
			n := min(skip, len(c))
			skip -= n
			entries = append(entries, c[n:]...)
		}
	}
	return entries, len(items) - done + extra, muted
}

// decodeItems decodes history lines, leaving out those of muted loggers;
// it returns their number per pattern (nil when none).
func decodeItems(items []histItem) (out []domain.LogEntry, muted map[string]int) {
	out = make([]domain.LogEntry, 0, len(items))
	for i := range items {
		it := &items[i]
		if it.entry != nil { // decoded and checked while read
			out = append(out, *it.entry)
			continue
		}
		e := safeDecode(it.format.Decoder, it.line)
		if pattern, ok := it.format.Mute.Match(&e); ok {
			if muted == nil {
				muted = map[string]int{}
			}
			muted[pattern]++
			continue
		}
		out = append(out, e)
	}
	return out, muted
}
