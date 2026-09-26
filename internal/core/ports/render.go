package ports

import (
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Role names the semantic part of a rendered segment. The TUI maps roles to
// theme styles, so renderers never deal with colors.
type Role string

// Segment roles.
const (
	RolePlain     Role = "plain"
	RoleDim       Role = "dim"
	RoleTimestamp Role = "timestamp"
	RoleLevel     Role = "level"
	RolePID       Role = "pid"
	RoleThread    Role = "thread"
	RoleLogger    Role = "logger"
	RoleMessage   Role = "message"
	RoleStack     Role = "stack"
	RolePod       Role = "pod"
)

// Segment is a piece of a rendered line.
type Segment struct {
	Text string
	Role Role
}

// TimestampMode selects how times are displayed.
type TimestampMode int

// Timestamp modes. The timestamp key cycles local, UTC, relative and none;
// delta is used by the trace view.
const (
	TimestampLocal TimestampMode = iota
	TimestampUTC
	TimestampRelative
	TimestampNone
	TimestampDelta
)

// ColumnSpec describes an optional column of a layout's stream line. The
// columns come from configuration (layouts/<name>.yaml); the message is
// never optional.
type ColumnSpec struct {
	Name string
	// Key toggles the column in the columns picker; empty if none.
	Key  string
	Role Role
	// HideBelow hides the column automatically on terminals narrower than
	// this many cells, until the user chooses columns.
	HideBelow int
	// Visible tells whether the column is shown when a view opens.
	Visible bool
}

// ColumnSet is a set of column names. Its methods never modify the
// receiver, so sets can be shared.
type ColumnSet map[string]bool

// Has reports whether name is in the set.
func (s ColumnSet) Has(name string) bool { return s[name] }

// With returns a copy of the set with name added.
func (s ColumnSet) With(names ...string) ColumnSet {
	out := make(ColumnSet, len(s)+len(names))
	for k := range s {
		out[k] = true
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}

// Without returns a copy of the set with name removed.
func (s ColumnSet) Without(names ...string) ColumnSet {
	out := make(ColumnSet, len(s))
	for k := range s {
		out[k] = true
	}
	for _, n := range names {
		delete(out, n)
	}
	return out
}

// RenderOptions are the display toggles that affect a rendered line.
type RenderOptions struct {
	// Hide lists the names of columns left out of the line.
	Hide       ColumnSet
	Timestamps TimestampMode
	Location   *time.Location
	// DeltaFrom is the reference time for TimestampDelta.
	DeltaFrom time.Time
	// Now is the reference time for TimestampRelative.
	Now time.Time
	// Full renders the zoom line of the layout instead of the stream line.
	Full bool
}

// LogRenderer lays out an entry as one line of segments (for example the
// Spring Boot console layout). Stack traces are rendered separately by the
// view, so Render only covers the entry's first line.
type LogRenderer interface {
	Render(e domain.LogEntry, opts RenderOptions) []Segment
}

// LogLayout is a configured way of drawing entries: the stream line with
// its optional columns, the zoom line, and which stack frames belong to
// frameworks.
type LogLayout interface {
	LogRenderer
	// Columns are the optional columns of the stream line, in order.
	Columns() []ColumnSpec
	// FrameworkFrame reports whether a stack frame belongs to a framework
	// rather than to the application's own code.
	FrameworkFrame(frame string) bool
}
