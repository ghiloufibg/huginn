package domain

import (
	"strings"
	"time"
)

// RawLine is one line as delivered by a log source, before decoding.
type RawLine struct {
	// Time is the timestamp added by the source (the kubelet when
	// timestamps are requested); zero if unknown.
	Time      time.Time
	Pod       string
	Container string
	Text      string
}

// LogEntry is a decoded log entry in canonical form. Decoders fill what they
// can; renderers and filters rely only on these fields.
type LogEntry struct {
	// Seq is a monotonically increasing number assigned on ingestion.
	Seq       uint64
	Time      time.Time
	Pod       string
	Container string
	Level     Level
	Logger    string
	Thread    string
	Message   string
	// Stack is a multi-line stack trace attached to the entry, if any.
	Stack string
	// TraceID is the correlation identifier used by the trace view.
	TraceID string
	App     string
	PID     string
	// Fields holds other structured fields worth showing in zoom mode.
	Fields map[string]string
	// Hidden holds fields the format profile hides by default (for example
	// Kubernetes enrichment metadata).
	Hidden map[string]string
	// Raw is the original line, kept for the raw view and exports.
	Raw string
	// Structured is true when Raw was parsed as a structured record.
	Structured bool
	// Format is the name of the log format that decoded the entry; the
	// view draws it with that format's layout.
	Format string

	search string // cached lower-cased searchable text, see searchText
}

// searchText is what text filters search, lower-cased, joined with
// newlines so a match cannot span two fields: message, logger, thread, trace id, stack trace
// and visible fields ("key=value"), or the raw line of unstructured
// entries. Hidden metadata is excluded. It is computed once per entry.
func (e *LogEntry) searchText() string {
	if e.search != "" {
		return e.search
	}
	var b strings.Builder
	if !e.Structured {
		b.WriteString(e.Raw)
		if e.Message != e.Raw {
			b.WriteString("\n" + e.Message)
		}
	} else {
		for _, s := range [...]string{e.Message, e.Logger, e.Thread, e.TraceID, e.Stack} {
			if s != "" {
				b.WriteString(s)
				b.WriteByte('\n')
			}
		}
		for k, v := range e.Fields {
			b.WriteString(k + "=" + v + "\n")
		}
	}
	e.search = strings.ToLower(b.String())
	if e.search == "" {
		e.search = "\n"
	}
	return e.search
}
