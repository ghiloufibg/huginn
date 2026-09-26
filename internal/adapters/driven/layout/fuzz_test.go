package layout

import (
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FuzzTemplates compiles arbitrary templates and draws a line with the
// valid ones: errors are allowed, panics are not.
func FuzzTemplates(f *testing.F) {
	for _, s := range []string{"{time}", "[{thread|last:15|right:15}]", "{logger|abbrev:3}", "{{x}}", "{field:a.b|default:-}", "{", "}", "{level|right:0}"} {
		f.Add(s, "io.gimle.payment.Gateway", "héllo wörld")
	}
	f.Fuzz(func(t *testing.T, show, logger, msg string) {
		l, errs := New(Spec{Stream: LineSpec{TimeFormat: "15:04:05", Columns: []ColumnSpec{{Name: "x", Show: show}}}})
		if errs != nil {
			return
		}
		e := domain.LogEntry{Structured: true, Logger: logger, Message: msg, Thread: msg, Fields: map[string]string{"a.b": msg}}
		l.Render(e, ports.RenderOptions{})
		l.Render(e, ports.RenderOptions{Full: true, Timestamps: ports.TimestampRelative})
	})
}
