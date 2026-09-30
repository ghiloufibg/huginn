package logformat

import (
	"regexp"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// FieldTransform keeps part of the value of a standard field: when Pattern
// matches, its group named Field becomes the value; otherwise the value is
// left as it is. Field is a standard field name of the configuration
// (message, logger, thread, trace_id, app, pid).
type FieldTransform struct {
	Field   string
	Pattern *regexp.Regexp
}

// transform applies the transforms in order. Each one reads the value its
// field had after the JSON was decoded or an earlier transform ran; empty
// fields are left alone.
func transform(e *domain.LogEntry, ts []FieldTransform) {
	for _, t := range ts {
		v := textField(e, t.Field)
		if v == nil || *v == "" {
			continue
		}
		i := t.Pattern.SubexpIndex(t.Field)
		if i < 0 {
			continue
		}
		m := t.Pattern.FindStringSubmatchIndex(*v)
		if m == nil || m[2*i] < 0 {
			continue
		}
		*v = (*v)[m[2*i]:m[2*i+1]]
	}
}

// textField returns the text standard field named name, or nil.
func textField(e *domain.LogEntry, name string) *string {
	switch name {
	case "message":
		return &e.Message
	case "logger":
		return &e.Logger
	case "thread":
		return &e.Thread
	case "trace_id":
		return &e.TraceID
	case "app":
		return &e.App
	case "pid":
		return &e.PID
	}
	return nil
}
