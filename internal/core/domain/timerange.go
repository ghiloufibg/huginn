package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MinRange is the shortest range accepted: the Kubernetes log API cuts its
// start to the second, so anything shorter cannot be honored.
const MinRange = time.Second

// NewRange returns the absolute range [from, to); a zero to is an open end
// (to now, then following). Both bounds are stored in UTC without a
// monotonic reading, so ranges compare with ==. It fails when from is zero,
// when to is not after from, or when the range is shorter than MinRange.
func NewRange(from, to time.Time) (TimeWindow, error) {
	if from.IsZero() {
		return TimeWindow{}, errors.New("a range needs a start")
	}
	w := TimeWindow{From: from.Round(0).UTC()}
	if !to.IsZero() {
		w.To = to.Round(0).UTC()
		if !w.To.After(w.From) {
			return TimeWindow{}, errors.New("the start must be before the end")
		}
		if w.To.Sub(w.From) < MinRange {
			return TimeWindow{}, fmt.Errorf("the range must last at least %s", MinRange)
		}
	}
	return w, nil
}

// rangeLayouts are the local-time forms ParseTimeRange accepts, longest first.
var rangeLayouts = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseTimeRange parses the bounds of an absolute range. from is required;
// an empty to is an open end. Each bound is:
//
//	2026-11-01 14:30[:05]   local time in loc (also with a T, or a date alone)
//	2026-11-01T14:30:05Z    an explicit zone (RFC 3339) wins over loc
//	14:30[:05]              the latest such time not after now (today, else yesterday)
//	now, now-90m, now-2h, now-1d   relative to now
//
// now and loc are injected so the result is deterministic. It fails with a
// message fit for the user when a bound cannot be read, when a bound is in
// the future (an open end follows instead), or when the bounds do not make
// a range of at least MinRange.
func ParseTimeRange(from, to string, now time.Time, loc *time.Location) (TimeWindow, error) {
	if loc == nil {
		loc = time.UTC
	}
	if strings.TrimSpace(from) == "" {
		return TimeWindow{}, errors.New("from: a start time is required")
	}
	f, err := parseBound(from, now, loc)
	if err != nil {
		return TimeWindow{}, fmt.Errorf("from: %w", err)
	}
	if f.After(now) {
		return TimeWindow{}, errors.New("from: is in the future")
	}
	var t time.Time
	if strings.TrimSpace(to) != "" {
		if t, err = parseBound(to, now, loc); err != nil {
			return TimeWindow{}, fmt.Errorf("to: %w", err)
		}
		if t.After(now) {
			return TimeWindow{}, errors.New("to: is in the future (leave it empty to follow)")
		}
	}
	w, err := NewRange(f, t)
	if err != nil {
		return TimeWindow{}, err
	}
	return w, nil
}

// parseBound parses one bound of a range.
func parseBound(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	if rest, ok := strings.CutPrefix(lower, "now"); ok {
		rest = strings.ReplaceAll(rest, " ", "")
		if rest == "" {
			return now, nil
		}
		d, ok := strings.CutPrefix(rest, "-")
		if !ok {
			return time.Time{}, fmt.Errorf("%q: want now or now-<duration>, e.g. now-90m", s)
		}
		off, err := parseDuration(d)
		if err != nil {
			return time.Time{}, fmt.Errorf("%q: want now-<duration>, e.g. now-90m, now-2h or now-1d", s)
		}
		return now.Add(-off), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	for _, layout := range rangeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if c, err := time.ParseInLocation(layout, s, loc); err == nil {
			return latestClock(c, now, loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q: want 2026-11-01 14:30, 14:30, now-90m or an RFC 3339 time", s)
}

// latestClock returns the latest time not after now whose clock time in loc
// is c's: today's, else yesterday's.
func latestClock(c, now time.Time, loc *time.Location) time.Time {
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), c.Hour(), c.Minute(), c.Second(), 0, loc)
	if t.After(now) {
		t = time.Date(n.Year(), n.Month(), n.Day()-1, c.Hour(), c.Minute(), c.Second(), 0, loc)
	}
	return t
}

// selectRange returns the lines of w's range: [From, To), lines without a
// timestamp kept.
func selectRange(lines []RawLine, w TimeWindow) []RawLine {
	var out []RawLine
	for _, l := range lines {
		if !l.Time.IsZero() && (l.Time.Before(w.From) || (!w.To.IsZero() && !l.Time.Before(w.To))) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// PageCursor marks where a page of a range ended, so the next page starts
// right after it, with no line lost or shown twice. Lines are ordered by
// time, and the kubelet's clock gives several lines the same instant; Skip
// counts, per container, the lines at exactly Time that were already
// delivered, which a stream restarted at Time returns again first.
type PageCursor struct {
	// Time is the source time of the last line delivered.
	Time time.Time
	// Skip is the number of lines at Time already delivered, by SkipKey.
	Skip map[string]int
}

// SkipKey is the key of a container in PageCursor.Skip.
func SkipKey(pod, container string) string { return pod + "\x00" + container }

// SkipFor returns how many lines at c.Time the container already delivered.
func (c *PageCursor) SkipFor(pod, container string) int {
	if c == nil {
		return 0
	}
	return c.Skip[SkipKey(pod, container)]
}
