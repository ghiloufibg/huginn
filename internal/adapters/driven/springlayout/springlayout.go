// Package springlayout implements ports.LogRenderer with the Spring Boot
// console layout: "spring-compact" for the stream (time, level, thread,
// logger, message) and "spring-full" (adds PID and application name).
package springlayout

import (
	"fmt"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Layout renders entries in the Spring Boot console layout.
type Layout struct {
	full        bool
	loggerWidth int
	threadWidth int
}

// NewCompact returns the stream layout: `time LEVEL [thread] logger : msg`.
func NewCompact() *Layout { return &Layout{loggerWidth: 30, threadWidth: 15} }

// NewFull returns Spring Boot's default layout:
// `time LEVEL pid --- [app] [thread] logger : msg`.
func NewFull() *Layout { return &Layout{full: true, loggerWidth: 40, threadWidth: 15} }

// Render implements ports.LogRenderer.
func (l *Layout) Render(e domain.LogEntry, o ports.RenderOptions) []ports.Segment {
	var out []ports.Segment
	add := func(text string, role ports.Role) { out = append(out, ports.Segment{Text: text, Role: role}) }
	if ts := l.timestamp(e.Time, o); ts != "" {
		add(ts, ports.RoleTimestamp)
		add(" ", ports.RolePlain)
	}
	if !e.Structured {
		add(e.Message, ports.RoleMessage)
		return out
	}
	add(fmt.Sprintf("%5s", levelWord(e.Level)), ports.RoleLevel)
	add(" ", ports.RolePlain)
	if l.full {
		add(e.PID, ports.RolePID)
		add(" --- ", ports.RoleDim)
		if e.App != "" {
			add("["+e.App+"] ", ports.RoleDim)
		}
	}
	add("["+fitLeft(e.Thread, l.threadWidth)+"]", ports.RoleThread)
	add(" ", ports.RolePlain)
	add(fmt.Sprintf("%-*s", l.loggerWidth, Abbreviate(e.Logger, l.loggerWidth)), ports.RoleLogger)
	add(" : ", ports.RoleDim)
	add(e.Message, ports.RoleMessage)
	return out
}

func levelWord(l domain.Level) string {
	if l == domain.LevelUnknown {
		return "-"
	}
	return l.String()
}

func (l *Layout) timestamp(t time.Time, o ports.RenderOptions) string {
	if t.IsZero() {
		return ""
	}
	loc := o.Location
	if loc == nil {
		loc = time.Local
	}
	short := "15:04:05.000"
	if l.full {
		short = "2006-01-02T15:04:05.000Z07:00"
	}
	switch o.Timestamps {
	case ports.TimestampUTC:
		if l.full {
			return t.UTC().Format(short)
		}
		return t.UTC().Format(short) + "Z"
	case ports.TimestampRelative:
		return fmt.Sprintf("%8s", relative(o.Now.Sub(t)))
	case ports.TimestampDelta:
		return fmt.Sprintf("%10s", "+"+delta(t.Sub(o.DeltaFrom)))
	case ports.TimestampNone:
		return ""
	default:
		return t.In(loc).Format(short)
	}
}

// relative formats how long ago something happened: -850ms, -12s, -3m4s.
func relative(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("-%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("-%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("-%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("-%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// delta formats an elapsed time in milliseconds with thin grouping: 1 131ms.
func delta(d time.Duration) string {
	ms := d.Milliseconds()
	s := fmt.Sprint(ms)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s + "ms"
}

// fitLeft right-aligns s in width cells, keeping its end when too long
// (Logback's %15.15t keeps the last characters of the thread name).
func fitLeft(s string, width int) string {
	r := []rune(s)
	if len(r) > width {
		r = r[len(r)-width:]
	}
	return strings.Repeat(" ", width-len(r)) + string(r)
}

// Abbreviate shortens a logger name like Logback's %logger{n}: package
// segments are reduced to their first letter, from the left, until the
// name fits; the class name is never shortened, and truncated from the
// left only as a last resort.
func Abbreviate(name string, n int) string {
	if len(name) <= n {
		return name
	}
	parts := strings.Split(name, ".")
	for i := 0; i < len(parts)-1 && len(strings.Join(parts, ".")) > n; i++ {
		if len(parts[i]) > 1 {
			parts[i] = parts[i][:1]
		}
	}
	out := strings.Join(parts, ".")
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}
