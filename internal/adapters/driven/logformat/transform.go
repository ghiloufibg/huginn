package logformat

import (
	"regexp"
	"slices"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// FieldTransform reads the value of a standard field with a regular
// expression. When Pattern matches:
//   - its group named Field becomes the value;
//   - a group named like another standard field fills that field when it is
//     still empty;
//   - a group listed in Pairs is split into key=value fields;
//   - any other named group becomes a field.
//
// Otherwise nothing changes. Field is a standard field name of the
// configuration (message, logger, thread, trace_id, app, pid).
type FieldTransform struct {
	Field   string
	Pattern *regexp.Regexp
	Pairs   []string
}

// field is an extracted key and value.
type field struct{ key, value string }

// transform applies the transforms in order to e and returns the fields
// they extract, each key once (the first one wins): named groups in
// pattern order, then pairs groups in list order. Empty values are left
// out. Each transform reads the value its field had after the JSON was
// decoded or an earlier transform ran; empty fields are left alone.
func transform(e *domain.LogEntry, ts []FieldTransform, aliases map[string]domain.Level) []field {
	var out []field
	add := func(k, v string) {
		if v != "" && !slices.ContainsFunc(out, func(f field) bool { return f.key == k }) {
			out = append(out, field{k, v})
		}
	}
	for _, t := range ts {
		v := textField(e, t.Field)
		if v == nil || *v == "" {
			continue
		}
		m := t.Pattern.FindStringSubmatchIndex(*v)
		if m == nil {
			continue
		}
		value, names := *v, t.Pattern.SubexpNames()
		group := func(i int) string {
			if m[2*i] < 0 {
				return ""
			}
			return value[m[2*i]:m[2*i+1]]
		}
		for i, name := range names {
			switch {
			case name == "" || slices.Contains(t.Pairs, name):
			case name == t.Field:
				*v = group(i)
			case isField(name):
				if g := group(i); g != "" && isEmpty(e, name) {
					setField(e, name, g, aliases)
				}
			default:
				add(name, group(i))
			}
		}
		for _, p := range t.Pairs {
			g := strings.TrimSpace(group(t.Pattern.SubexpIndex(p)))
			if !splitPairs(g, add) {
				add(p, g)
			}
		}
	}
	return out
}

// splitPairs calls add for each key=value token of s, separated by white
// space. It reports false, without calling add, when a token is not
// key=value: the text is then kept whole.
func splitPairs(s string, add func(k, v string)) bool {
	for rest := s; rest != ""; {
		var tok string
		tok, rest = cutSpace(rest)
		if k, _, ok := strings.Cut(tok, "="); !ok || k == "" {
			return false
		}
	}
	for rest := s; rest != ""; {
		var tok string
		tok, rest = cutSpace(rest)
		k, v, _ := strings.Cut(tok, "=")
		add(k, v)
	}
	return true
}

// cutSpace returns the first token of s and the text after it, skipping
// white space around it.
func cutSpace(s string) (tok, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// isField reports whether name is a standard field a transform can set.
func isField(name string) bool {
	return name == "level" || textField(&domain.LogEntry{}, name) != nil
}

// isEmpty reports whether the standard field name of e has no value.
func isEmpty(e *domain.LogEntry, name string) bool {
	if name == "level" {
		return e.Level == domain.LevelUnknown
	}
	return *textField(e, name) == ""
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

// setField sets the level or the text standard field named name from a
// group value, and reports whether name is one of them.
func setField(e *domain.LogEntry, name, v string, aliases map[string]domain.Level) bool {
	if name == "level" {
		e.Level, _ = domain.ParseLevelWith(v, aliases)
		return true
	}
	p := textField(e, name)
	if p == nil {
		return false
	}
	if name == "thread" {
		v = strings.TrimSpace(v)
	}
	*p = v
	return true
}
