package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// countingCatalog is an upstream that records how often it is watched and
// lets the test send snapshots.
type countingCatalog struct {
	mu       sync.Mutex
	watches  int
	live     int
	out      chan ports.CatalogSnapshot
	stopped  chan struct{}
	watchErr error
}

func newCountingCatalog() *countingCatalog {
	return &countingCatalog{out: make(chan ports.CatalogSnapshot, 8), stopped: make(chan struct{}, 8)}
}

func (c *countingCatalog) Watch(ctx context.Context, _ domain.Env) (<-chan ports.CatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watchErr != nil {
		return nil, c.watchErr
	}
	c.watches++
	c.live++
	ch := make(chan ports.CatalogSnapshot, 1)
	out := c.out
	go func() {
		defer close(ch)
		for {
			select {
			case s := <-out:
				ch <- s
			case <-ctx.Done():
				c.mu.Lock()
				c.live--
				c.mu.Unlock()
				c.stopped <- struct{}{}
				return
			}
		}
	}()
	return ch, nil
}

func recv(t *testing.T, ch <-chan ports.CatalogSnapshot) (ports.CatalogSnapshot, bool) {
	t.Helper()
	select {
	case s, ok := <-ch:
		return s, ok
	case <-time.After(2 * time.Second):
		t.Fatal("no snapshot")
		return ports.CatalogSnapshot{}, false
	}
}

func TestSharedCatalogOneUpstreamForManyReaders(t *testing.T) {
	up := newCountingCatalog()
	s := &SharedCatalog{Upstream: up}
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	a, err := s.Watch(ctxA, "rec")
	if err != nil {
		t.Fatal(err)
	}
	up.out <- ports.CatalogSnapshot{Env: "rec", Synced: true, Warnings: []string{"one"}}
	if got, _ := recv(t, a); len(got.Warnings) != 1 {
		t.Fatalf("first reader got %+v", got)
	}

	b, err := s.Watch(ctxB, "rec") // a later reader starts with the latest snapshot
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := recv(t, b); len(got.Warnings) != 1 {
		t.Errorf("late reader got %+v, want the latest snapshot at once", got)
	}
	if up.watches != 1 {
		t.Errorf("%d upstream watches for two readers", up.watches)
	}

	up.out <- ports.CatalogSnapshot{Env: "rec", Synced: true, Warnings: []string{"one", "two"}}
	for name, ch := range map[string]<-chan ports.CatalogSnapshot{"a": a, "b": b} {
		if got, _ := recv(t, ch); len(got.Warnings) != 2 {
			t.Errorf("reader %s got %+v", name, got)
		}
	}

	cancelA() // one reader left: the upstream keeps running
	if _, open := recv(t, a); open {
		t.Error("the channel of a cancelled reader stays open")
	}
	select {
	case <-up.stopped:
		t.Fatal("upstream stopped while a reader remains")
	case <-time.After(50 * time.Millisecond):
	}

	cancelB() // last reader: the upstream stops
	select {
	case <-up.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream still running without readers")
	}
}

func TestSharedCatalogRestartsAfterTheLastReader(t *testing.T) {
	up := newCountingCatalog()
	s := &SharedCatalog{Upstream: up}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := s.Watch(ctx, "rec"); err != nil {
			t.Fatal(err)
		}
		cancel()
		<-up.stopped
	}
	if up.watches != 2 {
		t.Errorf("%d upstream watches, want one per period of use", up.watches)
	}
}

func TestSharedCatalogEnvironmentsAreSeparate(t *testing.T) {
	up := newCountingCatalog()
	s := &SharedCatalog{Upstream: up}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, e := range []domain.Env{"rec", "prd"} {
		if _, err := s.Watch(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if up.watches != 2 {
		t.Errorf("%d upstream watches for two environments", up.watches)
	}
}

func TestSharedCatalogUpstreamError(t *testing.T) {
	up := newCountingCatalog()
	up.watchErr = domain.ErrConfig
	s := &SharedCatalog{Upstream: up}
	if _, err := s.Watch(context.Background(), "rec"); err == nil {
		t.Fatal("error not returned")
	}
	up.watchErr = nil // a failed start leaves nothing behind
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.Watch(ctx, "rec"); err != nil || up.watches != 1 {
		t.Errorf("retry: %v, %d watches", err, up.watches)
	}
}
