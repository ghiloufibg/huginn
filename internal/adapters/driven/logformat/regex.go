package logformat

import (
	"regexp"
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
	// LevelField, when set, raises the level from this group's value with
	// LevelRules (globs, longest first); see raise.
	LevelField string
	LevelRules []LevelRule
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
	p.LevelRules = sortRules(p.LevelRules)
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
		switch {
		case name == "time":
			if t, ok := d.time(v); ok {
				e.Time = t
			}
		case setField(&e, name, v, d.p.LevelAliases):
		default:
			if e.Fields == nil {
				e.Fields = map[string]string{}
			}
			e.Fields[name] = v
		}
	}
	if i := d.p.Pattern.SubexpIndex(d.p.LevelField); d.p.LevelField != "" && i >= 0 && i < len(m) {
		e.Level = raise(e.Level, d.p.LevelRules, m[i])
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
