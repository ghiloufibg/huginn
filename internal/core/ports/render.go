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

// RenderOptions are the display toggles that affect a rendered line.
type RenderOptions struct {
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
