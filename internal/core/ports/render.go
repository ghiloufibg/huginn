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

// Column is an optional part of a rendered line. The message is never
// optional.
type Column uint8

// Optional columns.
const (
	ColTime Column = 1 << iota
	ColLevel
	ColThread
	ColLogger
	ColPID
	ColApp
)

// Columns is a set of columns.
type Columns uint8

// Has reports whether c is in the set.
func (s Columns) Has(c Column) bool { return s&Columns(c) != 0 }

// With returns the set with c added.
func (s Columns) With(c Column) Columns { return s | Columns(c) }

// Without returns the set with c removed.
func (s Columns) Without(c Column) Columns { return s &^ Columns(c) }

// RenderOptions are the display toggles that affect a rendered line.
type RenderOptions struct {
	// Hide lists columns left out of the line (zero hides nothing).
	Hide       Columns
	Timestamps TimestampMode
	Location   *time.Location
	// DeltaFrom is the reference time for TimestampDelta.
	DeltaFrom time.Time
	// Now is the reference time for TimestampRelative.
	Now time.Time
	// Full renders every field of the layout (PID, app name) instead of the
	// compact stream layout.
	Full bool
}

// LogRenderer lays out an entry as one line of segments (for example the
// Spring Boot console layout). Stack traces are rendered separately by the
// view, so Render only covers the entry's first line.
type LogRenderer interface {
	Render(e domain.LogEntry, opts RenderOptions) []Segment
}
