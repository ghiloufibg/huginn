package logformat

import (
	"regexp"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// PlainDecoder decodes text lines it knows nothing about: the message is
// the line, and the level is guessed when a common pattern shows it (a
// level word near the start, a klog header). Text layouts worth reading
// field by field are described with a regex format instead.
type PlainDecoder struct{ name string }

// NewPlain returns a plain decoder; name is the format it reports.
func NewPlain(name string) *PlainDecoder { return &PlainDecoder{name: name} }

// levelWord finds a level keyword near the start of a line (Logback,
// Log4j, JUL-style "WARNING", bracketed "[ERROR]").
var levelWord = regexp.MustCompile(`\b(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE)\b`)

// klogLine matches Kubernetes klog headers: I0926 19:12:40.104 …
var klogLine = regexp.MustCompile(`^([IWEF])\d{4} \d{2}:\d{2}:\d{2}\.\d+`)

// Decode implements ports.LogDecoder.
func (d *PlainDecoder) Decode(raw domain.RawLine) domain.LogEntry {
	e := domain.LogEntry{Time: raw.Time, Pod: raw.Pod, Container: raw.Container, Raw: raw.Text, Message: raw.Text, Format: d.name}
	if m := klogLine.FindStringSubmatch(raw.Text); m != nil {
		e.Level, _ = domain.ParseLevel(m[1])
		return e
	}
	head := raw.Text[:min(len(raw.Text), 48)]
	if m := levelWord.FindString(head); m != "" {
		e.Level, _ = domain.ParseLevel(m)
	}
	return e
}
