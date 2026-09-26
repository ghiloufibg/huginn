package logformat

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// RegexProfile describes text lines read with a regular expression whose
// named groups are fields.
type RegexProfile struct {
	Name    string
	Pattern *regexp.Regexp
	// TimeFormat is the Go layout of the time group (default RFC 3339).
	TimeFormat   string
	LevelAliases map[string]domain.Level
	// LevelField, when set, derives the level from this group with
	// LevelRules (globs, longest first).
	LevelField string
	LevelRules []LevelRule
}

// LevelRule maps a glob on a group value to a level.
type LevelRule struct {
	Glob  string
	Level domain.Level
}

// RegexDecoder decodes text lines with a RegexProfile. Lines the pattern
// does not match are decoded as plain text.
type RegexDecoder struct {
	p     RegexProfile
	names []string
	plain *PlainDecoder
}

// NewRegex returns a decoder for p. Level rules are tried longest glob
// first, so "5*" wins over "*".
func NewRegex(p RegexProfile) *RegexDecoder {
	p.LevelRules = slices.Clone(p.LevelRules)
	slices.SortStableFunc(p.LevelRules, func(a, b LevelRule) int { return len(b.Glob) - len(a.Glob) })
	return &RegexDecoder{p: p, names: p.Pattern.SubexpNames(), plain: NewPlain(p.Name)}
}

// Decode implements ports.LogDecoder.
func (d *RegexDecoder) Decode(raw domain.RawLine) domain.LogEntry {
	m := d.p.Pattern.FindStringSubmatch(raw.Text)
	if m == nil {
		return d.plain.Decode(raw)
	}
	e := domain.LogEntry{Time: raw.Time, Pod: raw.Pod, Container: raw.Container, Raw: raw.Text, Structured: true, Format: d.p.Name}
	for i, name := range d.names {
		if name == "" || i >= len(m) {
			continue
		}
		v := m[i]
		switch name {
		case "time":
			if t, ok := d.time(v); ok {
				e.Time = t
			}
		case "level":
			e.Level, _ = domain.ParseLevelWith(v, d.p.LevelAliases)
		case "logger":
			e.Logger = v
		case "thread":
			e.Thread = strings.TrimSpace(v)
		case "message":
			e.Message = v
		case "trace_id":
			e.TraceID = v
		case "app":
			e.App = v
		case "pid":
			e.PID = v
		default:
			if e.Fields == nil {
				e.Fields = map[string]string{}
			}
			e.Fields[name] = v
		}
		if name == d.p.LevelField {
			e.Level = d.levelFrom(v)
		}
	}
	return e
}

func (d *RegexDecoder) time(v string) (time.Time, bool) {
	if d.p.TimeFormat != "" {
		t, err := time.Parse(d.p.TimeFormat, v)
		return t, err == nil
	}
	return parseTimeText(v)
}

func (d *RegexDecoder) levelFrom(v string) domain.Level {
	for _, r := range d.p.LevelRules {
		if ok, _ := path.Match(r.Glob, v); ok {
			return r.Level
		}
	}
	return domain.LevelUnknown
}
