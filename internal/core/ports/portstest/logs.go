package portstest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FakeLogSource serves fixed lines per container. Window and Previous are
// honored; Follow keeps the stream open until ctx is cancelled, delivering
// lines pushed with Push.
type FakeLogSource struct {
	// SecondPrecision honors SinceTime to the second, as the Kubernetes
	// API does.
	SecondPrecision bool

	mu       sync.Mutex
	clock    ports.Clock
	lines    map[string][]domain.RawLine
	previous map[string][]domain.RawLine
	live     map[string][]chan domain.RawLine
	err      error
}

// SetErr makes every new stream fail with err (nil clears it).
func (s *FakeLogSource) SetErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// NewFakeLogSource returns an empty source using clock for Since windows.
func NewFakeLogSource(clock ports.Clock) *FakeLogSource {
	return &FakeLogSource{clock: clock, lines: map[string][]domain.RawLine{}, previous: map[string][]domain.RawLine{}, live: map[string][]chan domain.RawLine{}}
}

func key(ns, pod, container string) string { return ns + "/" + pod + "/" + container }

// SetLines sets the history of a container; previous sets the history of
// its previous instance (nil: none).
func (s *FakeLogSource) SetLines(ns, pod, container string, lines, previous []domain.RawLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines[key(ns, pod, container)] = lines
	if previous != nil {
		s.previous[key(ns, pod, container)] = previous
	}
}

// Push appends a live line and delivers it to following streams.
func (s *FakeLogSource) Push(ns, pod string, l domain.RawLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(ns, pod, l.Container)
	s.lines[k] = append(s.lines[k], l)
	for _, ch := range s.live[k] {
		ch <- l
	}
}

type stream struct {
	ch  chan domain.RawLine
	mu  sync.Mutex
	err error
}

func (st *stream) Lines() <-chan domain.RawLine { return st.ch }
func (st *stream) Err() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.err
}

// Stream implements ports.LogSource.
func (s *FakeLogSource) Stream(ctx context.Context, req ports.LogRequest) (ports.LogStream, error) {
	s.mu.Lock()
	if s.err != nil {
		defer s.mu.Unlock()
		return nil, s.err
	}
	k := key(req.Namespace, req.Pod, req.Container)
	src, ok := s.lines[k]
	if req.Previous {
		src, ok = s.previous[k]
	}
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("logs of %s: %w", k, domain.ErrNotFound)
	}
	hist := domain.SelectWindow(src, req.Window, s.clock.Now())
	if !req.SinceTime.IsZero() {
		since := req.SinceTime
		if s.SecondPrecision {
			since = since.Truncate(time.Second)
		}
		hist = sinceTime(src, since)
	}
	if req.Limit > 0 && len(hist) > req.Limit {
		hist = hist[len(hist)-req.Limit:]
	}
	st := &stream{ch: make(chan domain.RawLine, len(hist)+256)}
	for _, l := range hist {
		st.ch <- l
	}
	follow := req.Follow && !req.Previous
	if follow {
		s.live[k] = append(s.live[k], st.ch)
	}
	s.mu.Unlock()
	if !follow {
		close(st.ch)
		return st, nil
	}
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.live[k]
		for i, ch := range subs {
			if ch == st.ch {
				s.live[k] = append(subs[:i], subs[i+1:]...)
				close(st.ch) // not closed yet by Close
				break
			}
		}
	}()
	return st, nil
}

func sinceTime(src []domain.RawLine, t time.Time) []domain.RawLine {
	for i, l := range src {
		if !l.Time.Before(t) {
			return src[i:]
		}
	}
	return nil
}

// Close ends every following stream of a container, as when the
// connection drops.
func (s *FakeLogSource) Close(ns, pod, container string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(ns, pod, container)
	for _, ch := range s.live[k] {
		close(ch)
	}
	s.live[k] = nil
}

// Following returns how many streams currently follow a container.
func (s *FakeLogSource) Following(ns, pod, container string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live[key(ns, pod, container)])
}
