package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func mustRange(t *testing.T, from, to time.Time) domain.TimeWindow {
	t.Helper()
	w, err := domain.NewRange(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// The fixture holds a1 (-5m), b2 (-4m), a3 (-3m) and b4 (-1m).

func TestClosedRangeIsExactAndNeverFollows(t *testing.T) {
	f := newFixture(t)
	// A real source honors the start to the second and has no end.
	f.logs.SecondPrecision, f.logs.NoUpperBound = true, true
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-4*time.Minute), t0.Add(-time.Minute)), Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "b2,a3" { // [From, To): b4 is at To
		t.Fatalf("closed range: %s", got)
	}
	if n := f.logs.Following("ns", "api-1", "api") + f.logs.Following("ns", "api-2", "api"); n != 0 {
		t.Fatalf("a closed range never follows: %d streams followed", n)
	}
	if r.more != nil || len(r.notices) != 0 {
		t.Fatalf("more %v, notices %v", r.more, r.notices)
	}
}

func TestRangeStartIsExactEvenWhenTheSourceRoundsIt(t *testing.T) {
	f := newFixture(t)
	f.logs.SecondPrecision = true
	// a3 is at a whole second; starting 1ns after it, the source (which
	// rounds down) returns it anyway, and the session must drop it.
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-3*time.Minute+1), t0)})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "b4" {
		t.Fatalf("got %s", got)
	}
}

func TestOpenRangeLoadsThenFollows(t *testing.T) {
	f := newFixture(t)
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-4*time.Minute), time.Time{}), Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if got := strings.Join(r.entries, ","); got != "b2,a3,b4" {
		t.Fatalf("open range history: %s", got)
	}
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
	f.logs.Push("ns", "api-1", line("api-1", t0.Add(time.Second), "live"))
	r.until(10*time.Millisecond, func() bool { return slices.Contains(r.entries, "live") })
}

// collectPages follows a range page by page, as the view does.
func collectPages(t *testing.T, f *fixture, window domain.TimeWindow) [][]string {
	t.Helper()
	var pages [][]string
	var page *domain.PageCursor
	for range 50 {
		w := window
		if page != nil {
			w.From = page.Time
		}
		r, cancel := f.open(t, ports.LogQuery{Window: w, Page: page})
		r.until(10*time.Millisecond, func() bool { return r.history })
		cancel()
		pages = append(pages, r.entries)
		if r.more == nil {
			return pages
		}
		page = r.more
	}
	t.Fatal("paging does not end")
	return nil
}

func TestRangePagesCoverTheRangeExactly(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 2
	pages := collectPages(t, f, mustRange(t, t0.Add(-10*time.Minute), t0))
	var got []string
	for i, p := range pages {
		if len(p) == 0 || len(p) > 2 {
			t.Fatalf("page %d holds %d lines: %v", i+1, len(p), p)
		}
		got = append(got, p...)
	}
	if want := "a1,b2,a3,b4"; strings.Join(got, ",") != want || len(pages) != 2 {
		t.Fatalf("pages %v, want %s in 2 pages", pages, want)
	}
}

// Lines of one container can share an instant, and be identical: a page
// that ends between them must neither lose nor repeat any.
func TestRangePageBoundaryInsideOneInstant(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 2
	at := t0.Add(-2 * time.Minute)
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{
		line("api-1", t0.Add(-3*time.Minute), "before"),
		line("api-1", at, "same"), line("api-1", at, "same"), line("api-1", at, "same"), line("api-1", at, "same"), line("api-1", at, "same"),
		line("api-1", t0.Add(-time.Minute), "after"),
	}, nil)
	f.logs.SetLines("ns", "api-2", "api", nil, nil)
	f.logs.SecondPrecision, f.logs.NoUpperBound = true, true
	pages := collectPages(t, f, mustRange(t, t0.Add(-10*time.Minute), t0))
	var got []string
	for _, p := range pages {
		got = append(got, p...)
	}
	if want := "before,same,same,same,same,same,after"; strings.Join(got, ",") != want {
		t.Fatalf("pages %v: got %s, want %s", pages, strings.Join(got, ","), want)
	}
}

func TestRangePageBoundaryAcrossContainersAtOneInstant(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 3
	at := t0.Add(-2 * time.Minute)
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{line("api-1", at, "a-x"), line("api-1", at, "a-y"), line("api-1", at, "a-z")}, nil)
	f.logs.SetLines("ns", "api-2", "api", []domain.RawLine{line("api-2", at, "b-x"), line("api-2", at, "b-y")}, nil)
	pages := collectPages(t, f, mustRange(t, t0.Add(-10*time.Minute), t0))
	var got []string
	for _, p := range pages {
		got = append(got, p...)
	}
	slices.Sort(got)
	if want := "a-x,a-y,a-z,b-x,b-y"; strings.Join(got, ",") != want {
		t.Fatalf("pages %v: got %s, want %s", pages, strings.Join(got, ","), want)
	}
}

func TestPageOfAnOpenRangeDoesNotFollow(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 2
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-10*time.Minute), time.Time{}), Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if r.more == nil || strings.Join(r.entries, ",") != "a1,b2" {
		t.Fatalf("first page %v, more %v", r.entries, r.more)
	}
	r.until(10*time.Millisecond, func() bool {
		return f.logs.Following("ns", "api-1", "api")+f.logs.Following("ns", "api-2", "api") == 0
	})
	// What arrives now belongs to a later page, or to the last page's follow.
	f.logs.Push("ns", "api-1", line("api-1", t0.Add(time.Second), "live"))
	for range 50 {
		select {
		case b := <-r.ch:
			for _, e := range b.Entries {
				r.entries = append(r.entries, e.Message)
			}
		case <-time.After(time.Millisecond):
			f.clock.Advance(100 * time.Millisecond)
		}
	}
	if slices.Contains(r.entries, "live") {
		t.Fatalf("a page that has a successor must not receive live lines: %v", r.entries)
	}
}

func TestLastPageOfAnOpenRangeFollows(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 10
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-10*time.Minute), time.Time{}), Follow: true})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	if r.more != nil {
		t.Fatalf("whole range fits one page, more %v", r.more)
	}
	r.until(10*time.Millisecond, func() bool { return f.logs.Following("ns", "api-1", "api") == 1 })
}

// slowSource serves n lines one second apart through an unbuffered channel,
// counting how many were taken: a reader that stops early leaves most unsent.
type slowSource struct {
	n    int
	sent atomic.Int64
	wg   sync.WaitGroup
}

type slowStream struct{ ch chan domain.RawLine }

func (s slowStream) Lines() <-chan domain.RawLine { return s.ch }
func (s slowStream) Err() error                   { return nil }

func (s *slowSource) Stream(ctx context.Context, req ports.LogRequest) (ports.LogStream, error) {
	ch := make(chan domain.RawLine)
	s.wg.Go(func() {
		defer close(ch)
		for i := range s.n {
			l := domain.RawLine{Time: t0.Add(-time.Duration(s.n-i) * time.Second), Pod: req.Pod, Container: req.Container, Text: fmt.Sprint("l", i)}
			select {
			case ch <- l:
				s.sent.Add(1)
			case <-ctx.Done():
				return
			}
		}
	})
	return slowStream{ch}, nil
}

func TestFullPageStopsReadingTheSource(t *testing.T) {
	f := newFixture(t)
	f.s.MaxHistory = 10
	src := &slowSource{n: 100_000}
	f.s.Logs = src
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-48*time.Hour), t0)})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	cancel()
	src.wg.Wait()
	if got := len(r.entries); got != 10 || r.more == nil {
		t.Fatalf("page holds %d lines, more %v", got, r.more)
	}
	// Two containers, each read up to the first batch that fills the page,
	// then stopped: nowhere near the 100000 lines of each.
	if n := src.sent.Load(); n > 2_000 {
		t.Fatalf("the source delivered %d lines for a page of 10", n)
	}
}

func TestPreviousInstanceIgnoresARange(t *testing.T) {
	f := newFixture(t)
	restarted := podWithSidecar("api-1", t0.Add(-time.Hour))
	restarted.Containers[0].Restarts = 3
	f.cluster.PutPod(restarted)
	f.logs.SetLines("ns", "api-1", "api", []domain.RawLine{line("api-1", t0, "current")},
		[]domain.RawLine{line("api-1", t0.Add(-3*time.Hour), "old boot"), line("api-1", t0.Add(-2*time.Hour), "crash")})
	r, cancel := f.open(t, ports.LogQuery{Previous: true, Window: mustRange(t, t0.Add(-time.Minute), t0)})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history && len(r.pods) == 2 })
	if !slices.Equal(r.entries, []string{"old boot", "crash"}) {
		t.Fatalf("the whole previous instance: %v", r.entries)
	}
}

func TestRangeRetentionNotice(t *testing.T) {
	f := newFixture(t)
	// api-1 started 2h ago but its logs begin at -5m: the node dropped the rest.
	r, cancel := f.open(t, ports.LogQuery{Window: mustRange(t, t0.Add(-time.Hour), t0)})
	defer cancel()
	r.until(10*time.Millisecond, func() bool { return r.history })
	found := false
	for _, n := range r.notices {
		found = found || strings.Contains(n, "available from")
	}
	if !found {
		t.Fatalf("notices %v", r.notices)
	}
}
