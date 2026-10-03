package portstest

import (
	"context"
	"sync"
)

// FakeClipboard records what is copied; Err, when set, is returned by
// every Copy.
type FakeClipboard struct {
	Err error

	mu    sync.Mutex
	texts []string
}

// Copy implements ports.Clipboard.
func (c *FakeClipboard) Copy(_ context.Context, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return c.Err
}

// Texts returns what was copied, oldest first.
func (c *FakeClipboard) Texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}
