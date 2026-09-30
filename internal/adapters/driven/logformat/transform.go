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
//   - a group listed in Pairs is split into fields, one per pair read by
//     PairPattern (groups key and value), or per key=value separated by
//     white space when PairPattern is nil;
//   - any other named group becomes a field.
//
// Otherwise nothing changes. Field is a standard field name of the
// configuration (message, logger, thread, trace_id, app, pid).
type FieldTransform struct {
	Field   string
	Pattern *regexp.Regexp
	Pairs   []string
	// PairPattern reads one pair of a Pairs group with its groups key and
	// value; nil means key=value separated by white space.
	PairPattern *regexp.Regexp
	// MaxBytes: a longer value is left as it is. MaxFields: at most this
	// many fields are extracted per line. Zero means no limit.
	MaxBytes, MaxFields int
}

// field is an extracted key and value.
type field struct{ key, value string }

// transform applies the transforms in order to e and returns the fields
// they extract, each key once (the first one wins): named groups in
// pattern order, then pairs groups in list order. Empty values are left
// out. Each transform reads the value its field had after the JSON was
// decoded or an earlier transform ran; empty fields and values longer
// than MaxBytes are left alone. It never panics, whatever the transforms.
func transform(e *domain.LogEntry, ts []FieldTransform, aliases map[string]domain.Level) []field {
	var x extracted
	for _, t := range ts {
		v := textField(e, t.Field)
		if v == nil || *v == "" || t.Pattern == nil || (t.MaxBytes > 0 && len(*v) > t.MaxBytes) {
			continue
		}
		m := t.Pattern.FindStringSubmatchIndex(*v)
		if m == nil {
			continue
		}
		value, names := *v, t.Pattern.SubexpNames()
		group := func(i int) string {
			if i < 0 || 2*i+1 >= len(m) || m[2*i] < 0 {
				return ""
			}
			return value[m[2*i]:m[2*i+1]]
		}
		x.left, x.limited = t.MaxFields, t.MaxFields > 0
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
				x.add(name, group(i))
			}
		}
		for _, p := range t.Pairs {
			g := strings.TrimSpace(group(t.Pattern.SubexpIndex(p)))
			var split bool
			if t.PairPattern != nil {
				split = splitPairsWith(t.PairPattern, g, x.add)
			} else {
				split = splitPairs(g, x.add)
			}
			if !split {
				x.add(p, g)
			}
		}
	}
	return x.fields
}

// extracted collects extracted fields, each key once, in linear time.
type extracted struct {
	fields  []field
	seen    map[string]bool // keys, once there are too many to scan
	limited bool            // the current transform has a MaxFields
	left    int             // fields the current transform may still add
}

// linearKeys is how many keys are searched by scanning before a map is
// built.
const linearKeys = 16

func (x *extracted) add(k, v string) {
	if v == "" || (x.limited && x.left == 0) || x.has(k) {
		return
	}
	x.fields = append(x.fields, field{k, v})
	x.left--
	switch {
	case x.seen != nil:
		x.seen[k] = true
	case len(x.fields) > linearKeys:
		x.seen = make(map[string]bool, 2*len(x.fields))
		for _, f := range x.fields {
			x.seen[f.key] = true
		}
	}
}

func (x *extracted) has(k string) bool {
	if x.seen != nil {
		return x.seen[k]
	}
	return slices.ContainsFunc(x.fields, func(f field) bool { return f.key == k })
}

// splitPairs calls add for each key=value token of s, separated by white
// space. It reports false, without calling add, when a token is not
// key=value: the text is then kept whole.
func splitPairs(s string, add func(k, v string)) bool {
	for rest := s; rest != ""; {
		var tok string
		tok, rest = cutSpace(rest)
		if k, _, ok := strings.Cut(tok, "="); tok != "" && (!ok || k == "") {
			return false
		}
	}
	for rest := s; rest != ""; {
		var tok string
		tok, rest = cutSpace(rest)
		if tok != "" {
			k, v, _ := strings.Cut(tok, "=")
			add(k, v)
		}
	}
	return true
}

// splitPairsWith calls add for each pair of s read by re, whose groups
// key and value are the pair. It reports false, without calling add, when
// the matches leave other text than white space, or a key is empty: the
// text is then kept whole.
func splitPairsWith(re *regexp.Regexp, s string, add func(k, v string)) bool {
	ms := re.FindAllStringSubmatchIndex(s, -1)
	ki, vi := re.SubexpIndex("key"), re.SubexpIndex("value")
	if ki < 0 {
		return false
	}
	last := 0
	for _, m := range ms {
		if !blank(s[last:m[0]]) || m[2*ki] < 0 || m[2*ki] == m[2*ki+1] {
			return false
		}
		last = m[1]
	}
	if !blank(s[last:]) {
		return false
	}
	for _, m := range ms {
		var v string
		if vi >= 0 && m[2*vi] >= 0 {
			v = s[m[2*vi]:m[2*vi+1]]
		}
		add(s[m[2*ki]:m[2*ki+1]], v)
	}
	return true
}

func blank(s string) bool {
	for i := range len(s) {
		if !isSpace(s[i]) {
			return false
		}
	}
	return true
}

// cutSpace returns the first token of s and the text after it, skipping
// white space around it. It scans bytes: strings.TrimLeft and IndexAny
// rebuilt their character set on every call, a sixth of a line's cost.
func cutSpace(s string) (tok, rest string) {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	j := i
	for j < len(s) && !isSpace(s[j]) {
		j++
	}
	k := j
	for k < len(s) && isSpace(s[k]) {
		k++
	}
	return s[i:j], s[k:]
}

// isSpace reports the white space of regexp's \s: space, \t, \n, \f, \r.
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' }

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
