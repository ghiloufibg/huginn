package logformat

import (
	"bytes"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// JSONDecoder decodes JSON log lines with a Profile. Lines that are not a
// JSON object are handed to the plain decoder.
type JSONDecoder struct {
	p     Profile
	plain *PlainDecoder
}

// NewJSON returns a decoder for profile p.
func NewJSON(p Profile) *JSONDecoder { return &JSONDecoder{p: p, plain: NewPlain(p.Name)} }

// Decode implements ports.LogDecoder.
func (d *JSONDecoder) Decode(raw domain.RawLine) domain.LogEntry {
	text := strings.TrimSpace(raw.Text)
	if !strings.HasPrefix(text, "{") {
		return d.plain.Decode(raw)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return d.plain.Decode(raw)
	}
	e := domain.LogEntry{Pod: raw.Pod, Container: raw.Container, Raw: raw.Text, Structured: true, Time: raw.Time, Format: d.p.Name}
	used := map[string]bool{}
	take := func(paths []string) (any, bool) {
		for _, p := range paths {
			if v, ok := lookup(obj, p); ok {
				used[p] = true
				return v, true
			}
		}
		return nil, false
	}
	if v, ok := take(d.p.Timestamp); ok {
		if t, ok := parseTime(v); ok {
			e.Time = t
		}
	}
	if v, ok := take(d.p.Level); ok {
		e.Level, _ = domain.ParseLevelWith(stringify(v), d.p.LevelAliases)
	}
	str := func(paths []string) string {
		if v, ok := take(paths); ok {
			return stringify(v)
		}
		return ""
	}
	e.Logger, e.Thread, e.Message = str(d.p.Logger), str(d.p.Thread), str(d.p.Message)
	e.Stack, e.TraceID, e.App, e.PID = str(d.p.Stack), str(d.p.TraceID), str(d.p.App), str(d.p.PID)
	var hasHidden bool
	e.Fields, hasHidden = d.rest(obj, used)
	if hasHidden {
		raw := text
		e.LoadHidden = func() map[string]string { return d.hiddenOf(raw) }
	}
	return e
}

// rest flattens the visible fields not consumed by the profile. Hidden
// fields are skipped, whole hidden objects without being walked; it only
// reports whether there were any, so they can be read again on demand.
func (d *JSONDecoder) rest(obj map[string]any, used map[string]bool) (fields map[string]string, hasHidden bool) {
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			if used[p] {
				continue
			}
			if d.hidden(p) {
				hasHidden = true
				continue
			}
			if child, ok := v.(map[string]any); ok {
				if d.hidden(p + ".\x00") { // every key below is hidden
					hasHidden = hasHidden || len(child) > 0
					continue
				}
				walk(p, child)
				continue
			}
			if fields == nil {
				fields = map[string]string{}
			}
			fields[p] = stringify(v)
		}
	}
	walk("", obj)
	return fields, hasHidden
}

// hiddenOf decodes the hidden fields of a line again, for the zoom view.
func (d *JSONDecoder) hiddenOf(raw string) map[string]string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	flat := map[string]string{}
	flatten("", obj, flat)
	out := map[string]string{}
	for k, v := range flat {
		if d.hidden(k) {
			out[k] = v
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

// lookup finds p as a literal key, then as a dotted path.
func lookup(obj map[string]any, p string) (any, bool) {
	if v, ok := obj[p]; ok && v != nil {
		return v, true
	}
	cur := any(obj)
	for _, part := range strings.Split(p, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

func flatten(prefix string, v any, out map[string]string) {
	m, ok := v.(map[string]any)
	if !ok {
		out[prefix] = stringify(v)
		return
	}
	for k, child := range m {
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		flatten(p, child, out)
	}
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return ""
		}
		return strings.TrimSpace(b.String())
	}
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700", "2006-01-02 15:04:05.000Z07:00", "2006-01-02 15:04:05.000", "2006-01-02T15:04:05.000"}

// parseTime accepts RFC 3339 variants and epoch seconds or milliseconds.
func parseTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		for _, l := range timeLayouts {
			if t, err := time.Parse(l, x); err == nil {
				return t, true
			}
		}
	case float64:
		f := x
		if f > 1e12 {
			return time.UnixMilli(int64(f)), true
		}
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)), true
	}
	return time.Time{}, false
}
