package logformat

import (
	"path"
	"sync"
)

// hiddenCacheMax bounds the keys whose hidden decision is remembered.
// Real logs repeat a few hundred keys; past this, as with hostile or
// ever-changing keys, decisions are computed each time.
const hiddenCacheMax = 4096

// hiddenCache remembers, per flattened key, whether the hidden globs hide
// it and every key below it. Matching the globs was about a quarter of a
// JSON line's decoding, for keys that repeat on every line. The decoder
// is shared by the goroutines of a session: reads take a read lock, which
// never waits once the keys are known.
type hiddenCache struct {
	globs []string
	mu    sync.RWMutex
	m     map[string]hiddenKey
}

// hiddenKey is the decision for one key: hidden itself, and hidden with
// every key below it (an object skipped without being walked).
type hiddenKey struct{ self, below bool }

func newHiddenCache(globs []string) *hiddenCache {
	return &hiddenCache{globs: globs, m: map[string]hiddenKey{}}
}

// get returns the decision for key k.
func (c *hiddenCache) get(k string) hiddenKey {
	if len(c.globs) == 0 {
		return hiddenKey{}
	}
	c.mu.RLock()
	h, ok := c.m[k]
	c.mu.RUnlock()
	if ok {
		return h
	}
	h = hiddenKey{self: c.match(k), below: c.match(k + ".\x00")}
	c.mu.Lock()
	if len(c.m) < hiddenCacheMax {
		c.m[k] = h
	}
	c.mu.Unlock()
	return h
}

func (c *hiddenCache) match(k string) bool {
	for _, g := range c.globs {
		if ok, _ := path.Match(g, k); ok {
			return true
		}
	}
	return false
}
