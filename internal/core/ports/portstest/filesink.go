package portstest

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// FakeFileSink keeps saved files in memory, by name; Err fails every Save.
type FakeFileSink struct {
	Err error

	mu    sync.Mutex
	files map[string]string
}

// Save implements ports.FileSink.
func (s *FakeFileSink) Save(_ context.Context, name string, write func(io.Writer) error) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	var b bytes.Buffer
	if err := write(&b); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string]string{}
	}
	s.files[name] = b.String()
	return "/saved/" + name, nil
}

// Files returns the saved files, by name.
func (s *FakeFileSink) Files() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.files))
	for k, v := range s.files {
		out[k] = v
	}
	return out
}
