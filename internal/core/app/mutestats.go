package app

import (
	"maps"
	"sync"
	"sync/atomic"
)

// muteStats counts the lines of muted loggers left out by a session, in
// total and per pattern. Tailers add to it concurrently; the session loop
// reads it on each batch.
type muteStats struct {
	total     atomic.Uint64 // read without the lock to tell a change
	mu        sync.Mutex
	byPattern map[string]uint64
}

// add counts n lines muted by pattern.
func (s *muteStats) add(pattern string, n uint64) {
	s.mu.Lock()
	if s.byPattern == nil {
		s.byPattern = map[string]uint64{}
	}
	s.byPattern[pattern] += n
	s.mu.Unlock()
	s.total.Add(n)
}

// addAll counts the lines muted per pattern.
func (s *muteStats) addAll(counts map[string]int) {
	for p, n := range counts {
		s.add(p, uint64(n))
	}
}

// snapshot returns a copy of the counts per pattern.
func (s *muteStats) snapshot() map[string]uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.byPattern)
}

// sum is the number of lines in counts.
func sum(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}
