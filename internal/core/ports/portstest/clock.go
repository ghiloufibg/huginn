package portstest

import (
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FakeClock is a manually advanced clock. Tickers fire during Advance.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

// NewFakeClock returns a clock set to now.
func NewFakeClock(now time.Time) *FakeClock { return &FakeClock{now: now} }

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// NewTicker returns a ticker driven by Advance.
func (c *FakeClock) NewTicker(d time.Duration) ports.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{c: make(chan time.Time, 1), every: d, next: c.now.Add(d)}
	c.tickers = append(c.tickers, t)
	return t
}

// Advance moves time forward by d, firing due tickers (non-blocking: a tick
// is dropped if the previous one was not consumed, like time.Ticker).
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.tickers {
		t.fire(c.now)
	}
}

type fakeTicker struct {
	mu      sync.Mutex
	c       chan time.Time
	every   time.Duration
	next    time.Time
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }

func (t *fakeTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

func (t *fakeTicker) fire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.stopped && !now.Before(t.next) {
		select {
		case t.c <- t.next:
		default:
		}
		t.next = t.next.Add(t.every)
	}
}
