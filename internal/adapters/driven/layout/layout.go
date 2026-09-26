// Package layout implements ports.LogLayout from configured templates
// (layouts/<name>.yaml, docs/CONFIG.md): the stream line with its optional
// columns, the zoom line, and the framework stack frames. It holds no
// layout of its own; the Spring Boot console layout is a config file.
package layout

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Spec is a layout as configured.
type Spec struct {
	Stream, Zoom      LineSpec
	FrameworkPrefixes []string
}

// LineSpec is one line: columns, then the separator, then the message.
type LineSpec struct {
	TimeFormat     string
	Columns        []ColumnSpec
	Separator      string
	SeparatorAfter []string
}

// ColumnSpec is one configured column.
type ColumnSpec struct {
	Name, Key, Show string
	Role            ports.Role
	HideBelow       int
	Visible         bool
}

// FieldError locates a template error, e.g. at "stream.columns[2].show".
type FieldError struct {
	Path, Msg string
}

// Layout draws entries from compiled templates.
type Layout struct {
	stream, zoom line
	columns      []ports.ColumnSpec
	prefixes     []string
}

type line struct {
	timeFormat string
	columns    []column
	sep        string
	sepAfter   []string
}

type column struct {
	name     string
	role     ports.Role
	tmpl     template
	timeOnly bool // uses the time field only: kept for undecoded lines
}

// New compiles a layout; it returns every template error.
func New(s Spec) (*Layout, []FieldError) {
	var errs []FieldError
	l := &Layout{prefixes: s.FrameworkPrefixes}
	l.stream = compile(s.Stream, "stream", &errs)
	l.zoom = compile(s.Zoom, "zoom", &errs)
	for _, c := range s.Stream.Columns {
		l.columns = append(l.columns, ports.ColumnSpec{Name: c.Name, Key: c.Key, Role: c.Role, HideBelow: c.HideBelow, Visible: c.Visible})
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return l, nil
}

func compile(s LineSpec, path string, errs *[]FieldError) line {
	out := line{timeFormat: s.TimeFormat, sep: s.Separator, sepAfter: s.SeparatorAfter}
	for i, c := range s.Columns {
		t, err := parseTemplate(c.Show)
		if err != nil {
			*errs = append(*errs, FieldError{Path: fmt.Sprintf("%s.columns[%d].show", path, i), Msg: err.Error()})
			continue
		}
		col := column{name: c.Name, role: c.Role, tmpl: t, timeOnly: true}
		fields := 0
		for _, p := range t.parts {
			if p.field != "" {
				fields++
				col.timeOnly = col.timeOnly && p.field == "time"
			}
		}
		col.timeOnly = col.timeOnly && fields > 0
		out.columns = append(out.columns, col)
	}
	return out
}

// Columns implements ports.LogLayout.
func (l *Layout) Columns() []ports.ColumnSpec { return l.columns }

// FrameworkFrame implements ports.LogLayout. A module prefix ending with
// "/" (java.base/) is ignored.
func (l *Layout) FrameworkFrame(frame string) bool {
	if i := strings.LastIndex(frame, "/"); i > 0 && !strings.ContainsAny(frame[:i], " (") {
		frame = frame[i+1:]
	}
	for _, p := range l.prefixes {
		if strings.HasPrefix(frame, p) {
			return true
		}
	}
	return false
}

// Render implements ports.LogRenderer: the shown columns, each followed by
// a space, the separator, then the message. A column whose fields are all
// empty (and have no default filter) is left out; undecoded lines keep
// only time columns.
func (l *Layout) Render(e domain.LogEntry, o ports.RenderOptions) []ports.Segment {
	ln := l.stream
	if o.Full {
		ln = l.zoom
	}
	out := make([]ports.Segment, 0, 2*len(ln.columns)+2)
	var shownBuf [8]string
	shown := shownBuf[:0]
	for _, c := range ln.columns {
		if (!o.Full && o.Hide.Has(c.name)) || (!e.Structured && !c.timeOnly) {
			continue
		}
		text, ok := c.render(e, o, ln.timeFormat)
		if !ok {
			continue
		}
		out = append(out, ports.Segment{Text: text, Role: c.role}, ports.Segment{Text: " ", Role: ports.RolePlain})
		shown = append(shown, c.name)
	}
	if e.Structured && ln.sep != "" && showSeparator(ln.sepAfter, shown) {
		out = append(out, ports.Segment{Text: ln.sep, Role: ports.RoleDim})
	}
	return append(out, ports.Segment{Text: e.Message, Role: ports.RoleMessage})
}

func showSeparator(after, shown []string) bool {
	if len(after) == 0 {
		return len(shown) > 0
	}
	for _, n := range after {
		if slices.Contains(shown, n) {
			return true
		}
	}
	return false
}

// render draws a column; ok is false when it uses fields and all are empty.
func (c column) render(e domain.LogEntry, o ports.RenderOptions, timeFormat string) (string, bool) {
	var b strings.Builder
	fields, filled := 0, 0
	for _, p := range c.tmpl.parts {
		if p.field == "" {
			b.WriteString(p.literal)
			continue
		}
		v := value(e, p, o, timeFormat)
		fields++
		if v != "" || p.hasDefault() {
			filled++
		}
		b.WriteString(p.apply(v))
	}
	if fields > 0 && filled == 0 {
		return "", false
	}
	return b.String(), b.Len() > 0
}

func value(e domain.LogEntry, p part, o ports.RenderOptions, timeFormat string) string {
	switch p.field {
	case "time":
		return formatTime(e.Time, o, timeFormat)
	case "level":
		if e.Level == domain.LevelUnknown {
			return "-"
		}
		return e.Level.String()
	case "logger":
		return e.Logger
	case "thread":
		return e.Thread
	case "message":
		return e.Message
	case "trace_id":
		return e.TraceID
	case "app":
		return e.App
	case "pid":
		return e.PID
	case "field":
		if v, ok := e.Fields[p.path]; ok {
			return v
		}
		return e.HiddenFields()[p.path] // decoded again: prefer visible fields in columns
	}
	return ""
}

// formatTime draws {time} in the view's timestamp mode.
func formatTime(t time.Time, o ports.RenderOptions, layout string) string {
	if t.IsZero() {
		return ""
	}
	loc := o.Location
	if loc == nil {
		loc = time.Local
	}
	switch o.Timestamps {
	case ports.TimestampUTC:
		s := t.UTC().Format(layout)
		if !hasZone(layout) {
			s += "Z"
		}
		return s
	case ports.TimestampRelative:
		return fmt.Sprintf("%8s", relative(o.Now.Sub(t)))
	case ports.TimestampDelta:
		return fmt.Sprintf("%10s", "+"+delta(t.Sub(o.DeltaFrom)))
	case ports.TimestampNone:
		return ""
	default:
		return t.In(loc).Format(layout)
	}
}

func hasZone(layout string) bool {
	for _, z := range []string{"Z07", "-07", "MST", "Z0700"} {
		if strings.Contains(layout, z) {
			return true
		}
	}
	return false
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
