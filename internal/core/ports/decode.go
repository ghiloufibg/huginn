package ports

import "github.com/ghiloufibg/huginn/internal/core/domain"

// LogDecoder turns a raw line into a canonical entry. It never fails: a line
// it cannot parse becomes an entry with Structured=false, Level unknown and
// Message set to the raw text. Which fields map to what is configuration of
// the implementation (a "log format profile"), not of the core.
type LogDecoder interface {
	Decode(raw domain.RawLine) domain.LogEntry
}

// LogDecoders picks the decoder of each container: the log format whose
// match rules apply to the repository and container.
type LogDecoders interface {
	For(repo, container string) LogDecoder
}

// OneDecoder decodes every container with the same decoder.
type OneDecoder struct{ LogDecoder }

// For implements LogDecoders.
func (d OneDecoder) For(string, string) LogDecoder { return d.LogDecoder }
