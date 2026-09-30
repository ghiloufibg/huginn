package logformat

import (
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/valyala/fastjson"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// JSONDecoder decodes JSON log lines with a Profile. Lines that are not a
// JSON object are handed to the plain decoder. When an object repeats a
// key, the first occurrence wins.
//
// It uses fastjson rather than encoding/json: the line is parsed once into
// a reusable tree (no reflection, no map[string]any), the profile's paths
// are read from it and hidden subtrees are skipped without being
// materialized. Strings are copied out of the parser before it is reused.
type JSONDecoder struct {
	p     Profile
	plain *PlainDecoder
}

// NewJSON returns a decoder for profile p.
func NewJSON(p Profile) *JSONDecoder {
	p.LevelRules = sortRules(p.LevelRules)
	return &JSONDecoder{p: p, plain: NewPlain(p.Name)}
}

// parsers are reused across lines and goroutines.
var parsers fastjson.ParserPool

// Decode implements ports.LogDecoder.
func (d *JSONDecoder) Decode(raw domain.RawLine) domain.LogEntry {
	text := strings.TrimSpace(raw.Text)
	if !strings.HasPrefix(text, "{") {
		return d.plain.Decode(raw)
	}
	p := parsers.Get()
	defer parsers.Put(p)
	root, err := p.Parse(text)
	if err != nil || root.Type() != fastjson.TypeObject {
		return d.plain.Decode(raw)
	}
	e := domain.LogEntry{Pod: raw.Pod, Container: raw.Container, Raw: raw.Text, Structured: true, Time: raw.Time, Format: d.p.Name}
	var usedBuf [16]string
	used := d.standard(&e, root, usedBuf[:0])
	extracted := transform(&e, d.p.Transforms, d.p.LevelAliases)
	var hasHidden, hiddenExtracted bool
	e.Fields, hasHidden = d.rest(root, used)
	for _, f := range extracted {
		switch {
		case lookup(root, f.key) != nil: // a JSON key wins over extracted text
		case d.hidden(f.key):
			hasHidden, hiddenExtracted = true, true
		default:
			if e.Fields == nil {
				e.Fields = map[string]string{}
			}
			e.Fields[f.key] = f.value
		}
	}
	if d.p.LevelField != "" {
		if v, ok := levelValue(root, d.p.LevelField, extracted); ok {
			e.Level = raise(e.Level, d.p.LevelRules, v)
		}
	}
	if hasHidden {
		raw := text
		e.LoadHidden = func() map[string]string { return d.hiddenOf(raw, hiddenExtracted) }
	}
	return e
}

// levelValue is the value of the level_from field of a line: the JSON
// path p, hidden or not, else the field p extracted by a transform.
func levelValue(root *fastjson.Value, p string, extracted []field) (string, bool) {
	if v := lookup(root, p); v != nil {
		return stringify(v), true
	}
	for _, f := range extracted {
		if f.key == p {
			return f.value, true
		}
	}
	return "", false
}

// standard reads the standard fields of the profile from root into e and
// returns used with the paths it read appended.
func (d *JSONDecoder) standard(e *domain.LogEntry, root *fastjson.Value, used []string) []string {
	take := func(paths []string) *fastjson.Value {
		for _, p := range paths {
			if v := lookup(root, p); v != nil {
				used = append(used, p)
				return v
			}
		}
		return nil
	}
	if v := take(d.p.Timestamp); v != nil {
		if t, ok := parseTime(v); ok {
			e.Time = t
		}
	}
	if v := take(d.p.Level); v != nil {
		e.Level, _ = domain.ParseLevelWith(stringify(v), d.p.LevelAliases)
	}
	str := func(paths []string) string {
		if v := take(paths); v != nil {
			return stringify(v)
		}
		return ""
	}
	e.Logger, e.Thread, e.Message = str(d.p.Logger), str(d.p.Thread), str(d.p.Message)
	e.Stack, e.TraceID, e.App, e.PID = str(d.p.Stack), str(d.p.TraceID), str(d.p.App), str(d.p.PID)
	return used
}

// rest flattens the visible fields not consumed by the profile. Hidden
// fields are skipped, whole hidden objects without being walked; it only
// reports whether there were any, so they can be read again on demand.
func (d *JSONDecoder) rest(root *fastjson.Value, used []string) (fields map[string]string, hasHidden bool) {
	var walk func(prefix string, top bool, o *fastjson.Object)
	walk = func(prefix string, top bool, o *fastjson.Object) {
		var seenBuf [32]string
		seen := seenBuf[:0]
		o.Visit(func(key []byte, v *fastjson.Value) {
			p := text(key)
			if slices.Contains(seen, p) {
				return // a duplicate key: the first occurrence wins, as in lookup
			}
			seen = append(seen, p)
			if !top {
				p = prefix + "." + p
			}
			if slices.Contains(used, p) {
				return
			}
			if d.hidden(p) {
				hasHidden = true
				return
			}
			if v.Type() == fastjson.TypeObject {
				child, _ := v.Object()
				if d.hidden(p + ".\x00") { // every key below is hidden
					hasHidden = hasHidden || child.Len() > 0
					return
				}
				walk(p, false, child)
				return
			}
			if fields == nil {
				fields = map[string]string{}
			}
			fields[p] = stringify(v)
		})
	}
	o, _ := root.Object()
	walk("", true, o)
	return fields, hasHidden
}

// hiddenOf decodes the hidden fields of a line again, for the zoom view
// and layout columns; transforms run again only when they extracted hidden
// fields. It runs on the UI goroutine, where a panic would end the
// program, so a line it cannot read gives no hidden fields instead.
func (d *JSONDecoder) hiddenOf(raw string, transformed bool) (out map[string]string) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	p := parsers.Get()
	defer parsers.Put(p)
	root, err := p.Parse(raw)
	if err != nil {
		return nil
	}
	out = map[string]string{}
	flatten("", true, root, func(k string, v *fastjson.Value) {
		if d.hidden(k) {
			out[k] = stringify(v)
		}
	})
	if transformed {
		var e domain.LogEntry
		var usedBuf [16]string
		d.standard(&e, root, usedBuf[:0])
		for _, f := range transform(&e, d.p.Transforms, d.p.LevelAliases) {
			if d.hidden(f.key) && lookup(root, f.key) == nil {
				out[f.key] = f.value
			}
		}
	}
	return out
}

func (d *JSONDecoder) hidden(k string) bool {
	for _, g := range d.p.Hidden {
		if ok, _ := path.Match(g, k); ok {
			return true
		}
	}
	return false
}

// lookup finds p as a literal key, then as a dotted path through objects.
// null counts as absent.
func lookup(root *fastjson.Value, p string) *fastjson.Value {
	if v := root.Get(p); v != nil && v.Type() != fastjson.TypeNull {
		return v
	}
	if !strings.Contains(p, ".") {
		return nil
	}
	cur := root
	for rest := p; ; {
		part, next, more := strings.Cut(rest, ".")
		if cur.Type() != fastjson.TypeObject {
			return nil
		}
		if cur = cur.Get(part); cur == nil {
			return nil
		}
		if !more {
			break
		}
		rest = next
	}
	if cur.Type() == fastjson.TypeNull {
		return nil
	}
	return cur
}

// flatten calls leaf for every non-object value, with its dotted path. top
// tells the root from an object under an empty key.
func flatten(prefix string, top bool, v *fastjson.Value, leaf func(string, *fastjson.Value)) {
	if v.Type() != fastjson.TypeObject {
		leaf(prefix, v)
		return
	}
	o, _ := v.Object()
	o.Visit(func(key []byte, child *fastjson.Value) {
		p := text(key)
		if !top {
			p = prefix + "." + p
		}
		flatten(p, false, child, leaf)
	})
}

// stringify renders a value as shown to the user: strings unquoted,
// numbers as written in the line (never rounded through float64), other
// values as compact JSON. The result never refers to the parser's memory.
func stringify(v *fastjson.Value) string {
	switch v.Type() {
	case fastjson.TypeString:
		return text(v.GetStringBytes())
	case fastjson.TypeTrue:
		return "true"
	case fastjson.TypeFalse:
		return "false"
	case fastjson.TypeNull:
		return "null"
	default: // numbers, arrays, objects
		return string(v.MarshalTo(nil))
	}
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700", "2006-01-02 15:04:05.000Z07:00", "2006-01-02 15:04:05.000", "2006-01-02T15:04:05.000"}

// parseTime accepts RFC 3339 variants and epoch seconds or milliseconds.
func parseTime(v *fastjson.Value) (time.Time, bool) {
	switch v.Type() {
	case fastjson.TypeString:
		return parseTimeText(text(v.GetStringBytes()))
	case fastjson.TypeNumber:
		f := v.GetFloat64()
		if f > 1e12 {
			return time.UnixMilli(int64(f)), true
		}
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)), true
	}
	return time.Time{}, false
}

// parseTimeText accepts RFC 3339 variants.
func parseTimeText(x string) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, x); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// text copies b out of the parser's memory, replacing each byte of an
// invalid UTF-8 sequence with U+FFFD as encoding/json does, so a broken
// line never sends raw bytes to the terminal.
func text(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune(utf8.RuneError)
		} else {
			sb.Write(b[:size])
		}
		b = b[size:]
	}
	return sb.String()
}
