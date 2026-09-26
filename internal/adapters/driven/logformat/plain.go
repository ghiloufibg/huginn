package logformat

import (
	"regexp"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// PlainDecoder decodes text lines: the Spring Boot / Logback console layout
// in full, otherwise just the level when a common pattern shows it.
type PlainDecoder struct{}

// NewPlain returns the plain decoder.
func NewPlain() *PlainDecoder { return &PlainDecoder{} }

// springLine matches the Spring Boot console layout, with or without the
// application name introduced in Spring Boot 3.2:
// 2026-09-26T19:12:40.104+02:00  INFO 1 --- [app] [  exec-1] c.a.Class : message
var springLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}[.,]\d{3}\S*)\s+(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+(\d+)\s+---\s+(?:\[([^\]]*)\]\s+)?\[\s*([^\]]*)\]\s+(\S+)\s*:\s?(.*)$`)

// levelWord finds a level keyword near the start of a line (Logback,
// Log4j, JUL-style "WARNING", bracketed "[ERROR]").
var levelWord = regexp.MustCompile(`\b(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE)\b`)

// klogLine matches Kubernetes klog headers: I0926 19:12:40.104 …
var klogLine = regexp.MustCompile(`^([IWEF])\d{4} \d{2}:\d{2}:\d{2}\.\d+`)

var springTimeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700", "2006-01-02 15:04:05.000", "2006-01-02 15:04:05,000", "2006-01-02T15:04:05.000"}

// Decode implements ports.LogDecoder.
func (*PlainDecoder) Decode(raw domain.RawLine) domain.LogEntry {
	e := domain.LogEntry{Time: raw.Time, Pod: raw.Pod, Container: raw.Container, Raw: raw.Text, Message: raw.Text}
	if m := springLine.FindStringSubmatch(raw.Text); m != nil {
		for _, l := range springTimeLayouts {
			if t, err := time.Parse(l, m[1]); err == nil {
				e.Time = t
				break
			}
		}
		e.Level, _ = domain.ParseLevel(m[2])
		e.PID, e.App, e.Thread, e.Logger, e.Message = m[3], m[4], strings.TrimSpace(m[5]), m[6], m[7]
		e.Structured = true
		return e
	}
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
