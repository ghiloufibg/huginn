package domain

import "slices"

// LogBuffer is a bounded ring buffer of log entries. When full, appending
// evicts the oldest entry and counts it as dropped. Every entry receives a
// sequence number, contiguous across the buffer's life, so a view can keep
// pointing at an entry (by Seq) while older ones are evicted.
type LogBuffer struct {
	buf     []LogEntry
	start   int // index of the oldest entry in buf
	n       int
	nextSeq uint64
	dropped uint64
}

// NewLogBuffer returns a buffer holding at most capacity entries.
func NewLogBuffer(capacity int) *LogBuffer {
	return &LogBuffer{buf: make([]LogEntry, max(capacity, 1)), nextSeq: 1}
}

// Append stores e with the next sequence number and returns that number.
func (b *LogBuffer) Append(e LogEntry) uint64 {
	e.Seq = b.nextSeq
	b.nextSeq++
	if b.n < len(b.buf) {
		b.buf[(b.start+b.n)%len(b.buf)] = e
		b.n++
		return e.Seq
	}
	b.buf[b.start] = e
	b.start = (b.start + 1) % len(b.buf)
	b.dropped++
	return e.Seq
}

// Len is the number of entries held.
func (b *LogBuffer) Len() int { return b.n }

// Cap is the maximum number of entries held.
func (b *LogBuffer) Cap() int { return len(b.buf) }

// Dropped is the number of entries evicted since creation or Reset.
func (b *LogBuffer) Dropped() uint64 { return b.dropped }

// At returns the i-th entry, 0 being the oldest held. i must be in
// [0, Len()).
func (b *LogBuffer) At(i int) *LogEntry { return &b.buf[(b.start+i)%len(b.buf)] }

// FirstSeq is the sequence number of the oldest entry held (the next one
// to be assigned when empty).
func (b *LogBuffer) FirstSeq() uint64 { return b.nextSeq - uint64(b.n) }

// Index returns the position of the entry with sequence seq, if held.
func (b *LogBuffer) Index(seq uint64) (int, bool) {
	first := b.FirstSeq()
	if seq < first || seq >= b.nextSeq {
		return 0, false
	}
	return int(seq - first), true
}

// Reset empties the buffer; sequence numbers keep increasing.
func (b *LogBuffer) Reset() {
	clear(b.buf)
	b.start, b.n, b.dropped = 0, 0, 0
}

// InsertLate places entries older than some held ones at their place by
// OrderTime (a stream recovered after an outage delivers what it missed).
// Every entry is renumbered; renumber maps a sequence number from before
// the call to the entry's new one (false when the entry was evicted).
// It is O(Len), for rare recoveries, not for the live path.
func (b *LogBuffer) InsertLate(late []LogEntry) (renumber func(old uint64) (uint64, bool)) {
	late = slices.Clone(late)
	slices.SortStableFunc(late, func(x, y LogEntry) int { return x.OrderTime().Compare(y.OrderTime()) })
	old := make([]LogEntry, b.n)
	for i := range b.n {
		old[i] = *b.At(i)
	}
	merged := make([]LogEntry, 0, len(old)+len(late))
	from := make([]uint64, 0, len(old)+len(late)) // old seq, 0 for late entries
	i, j := 0, 0
	for i < len(old) || j < len(late) {
		if j < len(late) && (i == len(old) || late[j].OrderTime().Before(old[i].OrderTime())) {
			merged, from = append(merged, late[j]), append(from, 0)
			j++
			continue
		}
		merged, from = append(merged, old[i]), append(from, old[i].Seq)
		i++
	}
	dropped := b.dropped
	b.Reset()
	b.dropped = dropped
	moved := make(map[uint64]uint64, len(old))
	for k, e := range merged {
		seq := b.Append(e)
		if from[k] != 0 {
			moved[from[k]] = seq
		}
	}
	first := b.FirstSeq()
	return func(old uint64) (uint64, bool) {
		seq, ok := moved[old]
		return seq, ok && seq >= first
	}
}
