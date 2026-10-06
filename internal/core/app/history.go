package app

import (
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// rawHistory is one container's history, not decoded yet.
type rawHistory struct {
	lines  []domain.RawLine
	format ports.LogFormat
}

// decodeChunk is the number of lines one goroutine decodes at a time.
const decodeChunk = 4096

// decodeHistory keeps the newest limit lines of all containers (by source
// time), or the oldest ones when oldest is set (a head), and decodes only
// those, in parallel. Each container may return up to limit lines, so
// without the cut a repository with n containers would decode n times what
// the view can hold. It returns the entries and the number of lines
// skipped. Without source times (zero), nothing is cut. The lines of muted
// loggers are left out after the cut; it returns their number too.
func decodeHistory(hs []rawHistory, limit int, oldest bool) (entries []domain.LogEntry, dropped, muted int) {
	total := 0
	timed := true
	for _, h := range hs {
		total += len(h.lines)
		for _, l := range h.lines {
			timed = timed && !l.Time.IsZero()
		}
	}
	cut := time.Time{}
	if total > limit && timed {
		times := make([]time.Time, 0, total)
		for _, h := range hs {
			for _, l := range h.lines {
				times = append(times, l.Time)
			}
		}
		slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
		cut = times[total-limit]
		if oldest {
			cut = times[limit-1]
		}
	}
	type job struct {
		lines  []domain.RawLine
		format ports.LogFormat
		out    []domain.LogEntry
		muted  int
	}
	var jobs []*job
	kept := 0
	for _, h := range hs {
		lines := h.lines
		switch {
		case !cut.IsZero() && oldest:
			// Lines of one container arrive in time order: skip the end.
			i, _ := slices.BinarySearchFunc(lines, cut, func(l domain.RawLine, t time.Time) int {
				if l.Time.After(t) {
					return 1
				}
				return -1
			})
			lines = lines[:i]
		case !cut.IsZero():
			// Skip the start.
			i, _ := slices.BinarySearchFunc(lines, cut, func(l domain.RawLine, t time.Time) int { return l.Time.Compare(t) })
			lines = lines[i:]
		}
		kept += len(lines)
		for len(lines) > 0 {
			n := min(decodeChunk, len(lines))
			jobs = append(jobs, &job{lines: lines[:n], format: h.format})
			lines = lines[n:]
		}
	}
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			j.out, j.muted = decodeAll(j.format, j.lines)
		}()
	}
	wg.Wait()
	out := make([]domain.LogEntry, 0, kept)
	for _, j := range jobs {
		out = append(out, j.out...)
		muted += j.muted
	}
	return out, total - kept, muted
}

// decodeAll decodes lines, leaving out those of muted loggers; it returns
// their number.
func decodeAll(f ports.LogFormat, lines []domain.RawLine) (out []domain.LogEntry, muted int) {
	out = make([]domain.LogEntry, 0, len(lines))
	for _, l := range lines {
		e := safeDecode(f.Decoder, l)
		if f.Mute.Mutes(&e) {
			muted++
			continue
		}
		out = append(out, e)
	}
	return out, muted
}
