package tui

import (
	"fmt"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// count adds (delta 1) or removes (delta -1) a view row from the level
// counts. Context rows are only shown around matches and are not counted.
func (l *logsScreen) count(r viewRow, delta int) {
	if !r.context && int(r.level) < len(l.levels) {
		l.levels[r.level] += delta
	}
}

// levelCounts summarizes the view's errors and warnings for the status
// bar, colored so problems stand out without scrolling (> and < jump to
// them). Empty when there are none.
func (l *logsScreen) levelCounts(m *Model) string {
	t := &m.opts.Theme
	var parts []string
	if n := l.levels[domain.LevelError]; n > 0 {
		parts = append(parts, t.Bad.Inherit(l.bar(m)).Render(plural(n, "error")))
	}
	if n := l.levels[domain.LevelWarn]; n > 0 {
		parts = append(parts, t.Warn.Inherit(l.bar(m)).Render(plural(n, "warning")))
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += l.bar(m).Render(" · ")
		}
		out += p
	}
	return out
}

// rateMeter measures live lines per second over the last few seconds.
type rateMeter struct {
	samples []rateSample
}

type rateSample struct {
	at time.Time
	n  int
}

// rateWindow is the span the rate is averaged over.
const rateWindow = 5 * time.Second

func (r *rateMeter) add(now time.Time, n int) {
	r.samples = append(r.samples, rateSample{now, n})
	r.prune(now)
}

func (r *rateMeter) prune(now time.Time) {
	i := 0
	for i < len(r.samples) && now.Sub(r.samples[i].at) > rateWindow {
		i++
	}
	r.samples = r.samples[i:]
}

// perSecond is the average rate over the window ending at now.
func (r *rateMeter) perSecond(now time.Time) float64 {
	r.prune(now)
	total := 0
	for _, s := range r.samples {
		total += s.n
	}
	return float64(total) / rateWindow.Seconds()
}

// label formats the rate for the LIVE chip: "42/s", "0.4/s", or "" when
// nothing arrived during the window.
func (r *rateMeter) label(now time.Time) string {
	switch v := r.perSecond(now); {
	case v <= 0:
		return ""
	case v < 10:
		return fmt.Sprintf("%.1f/s", v)
	default:
		return fmt.Sprintf("%.0f/s", v)
	}
}
