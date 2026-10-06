package ports

import "github.com/ghiloufibg/huginn/internal/core/domain"

// LogDecoder turns a raw line into a canonical entry. It never fails: a line
// it cannot parse becomes an entry with Structured=false, Level unknown and
// Message set to the raw text. Which fields map to what is configuration of
// the implementation (a "log format profile"), not of the core.
type LogDecoder interface {
	Decode(raw domain.RawLine) domain.LogEntry
}

// LogFormat is how a container's lines are read: its decoder, and the
// loggers whose entries are hidden (nil: none).
type LogFormat struct {
	Decoder LogDecoder
	Mute    *domain.LoggerMute
}

// LogDecoders picks the format of each container: the log format whose
// match rules apply to the repository and container.
type LogDecoders interface {
	For(repo, container string) LogFormat
}

// OneDecoder decodes every container with the same decoder and mutes
// nothing.
type OneDecoder struct{ LogDecoder }

// For implements LogDecoders.
func (d OneDecoder) For(string, string) LogFormat { return LogFormat{Decoder: d.LogDecoder} }
