package domain

import "time"

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
}
