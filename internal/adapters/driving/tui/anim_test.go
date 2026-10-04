package tui

import "testing"

// TestReleaseMemoryDebounces: quick screen navigation can call
// releaseMemory several times in succession; a second call while one is
// still in flight must be a no-op, not stack another forced
// stop-the-world collection on top of the first.
func TestReleaseMemoryDebounces(t *testing.T) {
	t.Cleanup(func() { releasing.Store(false) })

	// Simulate "a release is already running" deterministically, rather
	// than racing the real debug.FreeOSMemory call's own timing.
	releasing.Store(true)
	releaseMemory() // must return immediately, leaving the flag as it found it
	if !releasing.Load() {
		t.Fatal("a second call while one is in flight must not touch the flag")
	}
}
