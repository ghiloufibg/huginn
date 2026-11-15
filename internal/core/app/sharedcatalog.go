package app

import (
	"context"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// SharedCatalog is a ports.ServiceCatalog that lets several readers watch
// the same environment on one upstream watch: the services screen and the
// usage screen would otherwise each start their own informers on the API
// server. The upstream watch starts with the first reader, a later reader
// first receives the latest snapshot, and the watch stops with the last
// reader.
type SharedCatalog struct {
	Upstream ports.ServiceCatalog

	mu    sync.Mutex
	feeds map[domain.Env]*sharedFeed
}

type sharedFeed struct {
	cancel context.CancelFunc
	subs   map[chan ports.CatalogSnapshot]struct{}
	last   *ports.CatalogSnapshot
}

// Watch implements ports.ServiceCatalog.
func (s *SharedCatalog) Watch(ctx context.Context, env domain.Env) (<-chan ports.CatalogSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.feeds[env]
	if f == nil {
		// The upstream watch outlives any one reader: it ends with the last.
		uctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		up, err := s.Upstream.Watch(uctx, env)
		if err != nil {
			cancel()
			return nil, err
		}
		f = &sharedFeed{cancel: cancel, subs: map[chan ports.CatalogSnapshot]struct{}{}}
		if s.feeds == nil {
			s.feeds = map[domain.Env]*sharedFeed{}
		}
		s.feeds[env] = f
		go s.pump(env, f, up)
	}
	ch := make(chan ports.CatalogSnapshot, 1)
	f.subs[ch] = struct{}{}
	if f.last != nil {
		ch <- *f.last
	}
	go func() {
		<-ctx.Done()
		s.leave(env, f, ch)
	}()
	return ch, nil
}

// pump hands each upstream snapshot to every reader, until upstream ends.
func (s *SharedCatalog) pump(env domain.Env, f *sharedFeed, up <-chan ports.CatalogSnapshot) {
	for snap := range up {
		s.mu.Lock()
		f.last = &snap
		for ch := range f.subs {
			replaceLatest(ch, snap)
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range f.subs {
		close(ch)
	}
	f.subs = map[chan ports.CatalogSnapshot]struct{}{}
	if s.feeds[env] == f {
		delete(s.feeds, env)
	}
}

// leave removes a reader and stops the upstream watch with the last one.
func (s *SharedCatalog) leave(env domain.Env, f *sharedFeed, ch chan ports.CatalogSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := f.subs[ch]; !ok { // the feed ended and closed it already
		return
	}
	delete(f.subs, ch)
	close(ch)
	if len(f.subs) == 0 {
		f.cancel()
		if s.feeds[env] == f {
			delete(s.feeds, env)
		}
	}
}
