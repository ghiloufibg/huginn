package domain

import (
	"errors"
	"fmt"
	"strings"
)

// LoggerMute hides the entries of chosen loggers (connection pool state,
// resource snapshots…) before they reach the view. A pattern is a logger
// name, matched exactly, or a name ending in "*", matched as a prefix
// ("com.example.pool.*"). Names are compared as the line writes them,
// case-sensitively. Entries without a logger are never muted.
//
// It is immutable and safe for concurrent use; a nil *LoggerMute mutes
// nothing.
type LoggerMute struct {
	exact    map[string]struct{}
	prefixes []string // the patterns ending in "*"
	keep     LevelSet // levels shown even from a muted logger
}

// NewLoggerMute compiles patterns; entries of a level in keep are never
// muted. It returns nil (mute nothing) when there are no patterns.
func NewLoggerMute(patterns []string, keep []Level) (*LoggerMute, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	m := &LoggerMute{exact: map[string]struct{}{}, keep: LevelSet{}}
	var errs []error
	for _, p := range patterns {
		if err := CheckMutePattern(p); err != nil {
			errs = append(errs, err)
			continue
		}
		if strings.HasSuffix(p, "*") {
			m.prefixes = append(m.prefixes, p)
		} else {
			m.exact[p] = struct{}{}
		}
	}
	for _, l := range keep {
		m.keep[l] = true
	}
	return m, errors.Join(errs...)
}

// CheckMutePattern tells whether p is a valid logger pattern: a non-empty
// name without spaces, with at most one "*", at the end, after at least
// one character.
func CheckMutePattern(p string) error {
	switch body := strings.TrimSuffix(p, "*"); {
	case p == "" || body == "":
		return fmt.Errorf("logger pattern %q matches every logger; name a logger or a prefix such as com.example.*", p)
	case strings.ContainsAny(p, " \t"):
		return fmt.Errorf("logger pattern %q contains a space", p)
	case strings.Contains(body, "*"):
		return fmt.Errorf("logger pattern %q: * is only allowed at the end (a prefix)", p)
	}
	return nil
}

// Mutes reports whether the entry is hidden: its logger matches a pattern
// and its level is not kept.
func (m *LoggerMute) Mutes(e *LogEntry) bool {
	_, ok := m.Match(e)
	return ok
}

// Match is Mutes, also returning the pattern that hides the entry, as
// configured. An exact name wins over a prefix.
func (m *LoggerMute) Match(e *LogEntry) (pattern string, ok bool) {
	if m == nil || e.Logger == "" || m.keep[e.Level] {
		return "", false
	}
	if _, ok := m.exact[e.Logger]; ok {
		return e.Logger, true
	}
	for _, p := range m.prefixes {
		if strings.HasPrefix(e.Logger, p[:len(p)-1]) {
			return p, true
		}
	}
	return "", false
}
