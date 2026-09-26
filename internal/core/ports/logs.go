package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// LogRequest asks for the logs of one container.
type LogRequest struct {
	Scope     Scope
	Namespace string
	Pod       string
	Container string
	Window    domain.TimeWindow
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
