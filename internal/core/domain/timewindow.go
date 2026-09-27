package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeWindow selects which past logs to load before (optionally) following.
// Exactly one of Since, Tail and Head is set: Since for a relative window
// such as 15m, Tail for "the last N lines", Head for "the first N lines"
// (of what the source keeps; a head never follows).
type TimeWindow struct {
	Since time.Duration
	Tail  int
	Head  int
}

// DefaultTailLines is the tail size used when "tail" is given without N.
const DefaultTailLines = 500

// DefaultHeadLines is the head size used when "head" is given without N.
const DefaultHeadLines = 500

// IsTail reports whether the window is the last lines.
func (w TimeWindow) IsTail() bool { return w.Tail > 0 }

// IsHead reports whether the window is the first lines.
func (w TimeWindow) IsHead() bool { return w.Head > 0 }

// String renders the window as accepted by ParseTimeWindow.
func (w TimeWindow) String() string {
	switch {
	case w.IsTail():
		return "tail " + strconv.Itoa(w.Tail)
	case w.IsHead():
		return "head " + strconv.Itoa(w.Head)
	}
	return formatDuration(w.Since)
}

// Label is the short form shown in the status bar ("15m", "tail", "head").
func (w TimeWindow) Label() string {
	switch {
	case w.IsTail():
		return "tail"
	case w.IsHead():
		return "head"
	}
	return formatDuration(w.Since)
}

// ParseTimeWindow parses "15m", "1h", "2d", "90s", "tail", "tail 200",
// "tail:200", "head", "head 200" or "head:200". Days are supported in
// addition to Go durations. defaultTail and defaultHead are the sizes of a
// bare "tail" and "head" (DefaultTailLines, DefaultHeadLines when not
// positive).
func ParseTimeWindow(s string, defaultTail, defaultHead int) (TimeWindow, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return TimeWindow{}, fmt.Errorf("empty time window")
	}
	if rest, ok := strings.CutPrefix(s, "tail"); ok {
		n, err := lineCount("tail", rest, defaultTail, DefaultTailLines)
		return TimeWindow{Tail: n}, err
	}
	if rest, ok := strings.CutPrefix(s, "head"); ok {
		n, err := lineCount("head", rest, defaultHead, DefaultHeadLines)
		return TimeWindow{Head: n}, err
	}
	d, err := parseDuration(s)
	if err != nil {
		return TimeWindow{}, err
	}
	return TimeWindow{Since: d}, nil
}

// lineCount parses the size after "tail" or "head".
func lineCount(kind, rest string, def, fallback int) (int, error) {
	rest = strings.TrimLeft(rest, " :=")
	if rest == "" {
		if def <= 0 {
			def = fallback
		}
		return def, nil
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s size %q: want a positive number of lines", kind, rest)
	}
	return n, nil
}

func parseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid time window %q: want e.g. 15m, 1h, 2d or tail", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid time window %q: want e.g. 15m, 1h, 2d or tail", s)
	}
	return d, nil
}

func formatDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	default:
		return d.String()
	}
}

// DefaultWindowPresets returns the presets bound to the numeric keys:
// index 0 is key "1" (15m) … index 6 is key "7" (2d); the tail preset
// (key "0") and the head preset (key "9") are returned last, in that order.
func DefaultWindowPresets(tail, head int) []TimeWindow {
	if tail <= 0 {
		tail = DefaultTailLines
	}
	if head <= 0 {
		head = DefaultHeadLines
	}
	return []TimeWindow{
		{Since: 15 * time.Minute},
		{Since: 30 * time.Minute},
		{Since: 40 * time.Minute},
		{Since: 45 * time.Minute},
		{Since: time.Hour},
		{Since: 24 * time.Hour},
		{Since: 48 * time.Hour},
		{Tail: tail},
		{Head: head},
	}
}

// NextWindow returns the preset following current, wrapping around. If
// current is not a preset, the first preset is returned.
func NextWindow(presets []TimeWindow, current TimeWindow) TimeWindow {
	for i, p := range presets {
		if p == current {
			return presets[(i+1)%len(presets)]
		}
	}
	return presets[0]
}

// SelectWindow returns the part of lines (ordered by time) that falls in w:
// the last w.Tail lines for a tail window, the first w.Head lines for a
// head window, else the lines at or after now-w.Since. Lines without a
// timestamp are kept for Since windows.
func SelectWindow(lines []RawLine, w TimeWindow, now time.Time) []RawLine {
	if w.IsHead() {
		if len(lines) > w.Head {
			return lines[:w.Head]
		}
		return lines
	}
	if w.IsTail() {
		if len(lines) > w.Tail {
			return lines[len(lines)-w.Tail:]
		}
		return lines
	}
	if w.Since <= 0 {
		return lines
	}
	cutoff := now.Add(-w.Since)
	for i, l := range lines {
		if l.Time.IsZero() || !l.Time.Before(cutoff) {
			return lines[i:]
		}
	}
	return nil
}
