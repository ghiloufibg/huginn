package ports

import (
	"context"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// LogRequest asks for the logs of one container.
type LogRequest struct {
	Scope     Scope
	Namespace string
	Pod       string
	Container string
	// Window selects the history. A head window (Window.Head) asks for the
	// first Head lines of what the source keeps, of the previous instance
	// with Previous; Follow must then be false, and SinceTime and Limit do
	// not apply. A source may stop after Head lines; consumers ignore any
	// extra lines.
	Window domain.TimeWindow
	// SinceTime, when set, replaces Window: lines at or after this time
	// (used to resume a stream without reloading its history). Sources may
	// honor it with second precision only (the Kubernetes API does):
	// lines of the same second before SinceTime can come back, and
	// consumers must skip them.
	SinceTime time.Time
	// Limit, when positive, caps the history to the most recent Limit
	// lines of the window (Kubernetes tailLines combined with the window),
	// so a long window over a chatty container stays bounded.
	Limit int
	// Follow keeps the stream open and delivers new lines as they arrive.
	Follow bool
	// Previous reads the previous (terminated) instance of the container.
	Previous bool
}

// LogStream is an open log stream. Lines is closed when the stream ends
// (end of history without Follow, context cancelled, or failure); Err then
// reports why, nil meaning a normal end.
type LogStream interface {
	Lines() <-chan domain.RawLine
	Err() error
}

// LogSource opens log streams. Lines must carry Time when the backend can
// provide it, so that multi-pod streams can be merged by time.
// Unknown pods or containers, and Previous without a previous instance,
// fail with domain.ErrNotFound.
type LogSource interface {
	Stream(ctx context.Context, req LogRequest) (LogStream, error)
}
