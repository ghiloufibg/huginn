package layout

import (
	"fmt"
	"strconv"
	"strings"
)

// A template is literal text with placeholders: "[{thread|last:15|right:15}]".
// A placeholder names a field, then filters separated by "|". The field is
// a standard field or "field:<path>", a JSON path or regex group kept in
// the entry's extra fields. "{{" and "}}" are literal braces.
type template struct {
	parts []part
}

type part struct {
	literal string
	field   string // standard field name, or "" for a literal
	path    string // for field "field": the extra field path
	filters []filter
}

type filter struct {
	name string
	n    int
	arg  string
}

// standardFields are the fields a placeholder may name.
var standardFields = []string{"time", "level", "logger", "thread", "message", "trace_id", "app", "pid", "field"}

// filters take a width (n) or a text argument.
var (
	widthFilters = map[string]bool{"left": true, "right": true, "first": true, "last": true, "abbrev": true}
	plainFilters = map[string]bool{"upper": true, "lower": true}
)

func parseTemplate(s string) (template, error) {
	var t template
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.parts = append(t.parts, part{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "{{"), strings.HasPrefix(s[i:], "}}"):
			lit.WriteByte(s[i])
			i++
		case s[i] == '}':
			return t, fmt.Errorf("unexpected } at %d (write }} for a literal brace)", i+1)
		case s[i] == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return t, fmt.Errorf("missing } after {%s", s[i+1:])
			}
			p, err := parsePlaceholder(s[i+1 : i+end])
			if err != nil {
				return t, err
			}
			flush()
			t.parts = append(t.parts, p)
			i += end
		default:
			lit.WriteByte(s[i])
		}
	}
	flush()
	return t, nil
}

func parsePlaceholder(s string) (part, error) {
	items := strings.Split(s, "|")
	name, path, _ := strings.Cut(strings.TrimSpace(items[0]), ":")
	p := part{field: name, path: path}
	switch {
	case name == "":
		return p, fmt.Errorf("empty placeholder {%s}", s)
	case !contains(standardFields, name):
		return p, fmt.Errorf("unknown field %q in {%s} (fields: %s, or field:<path>)", name, s, strings.Join(standardFields[:len(standardFields)-1], ", "))
	case name == "field" && path == "":
		return p, fmt.Errorf("{field:<path>} needs a path, e.g. {field:http.status}")
	case name != "field" && path != "":
		return p, fmt.Errorf("{%s} takes no path; use {field:<path>} for other fields", s)
	}
	for _, raw := range items[1:] {
		fname, arg, hasArg := strings.Cut(strings.TrimSpace(raw), ":")
		f := filter{name: fname, arg: arg}
		switch {
		case widthFilters[fname]:
			n, err := strconv.Atoi(arg)
			if err != nil || n < 1 {
				return p, fmt.Errorf("filter %s needs a width, e.g. %s:15", fname, fname)
			}
			f.n = n
		case plainFilters[fname]:
			if hasArg {
				return p, fmt.Errorf("filter %s takes no argument", fname)
			}
		case fname == "default":
			if !hasArg {
				return p, fmt.Errorf("filter default needs a value, e.g. default:-")
			}
		default:
			return p, fmt.Errorf("unknown filter %q (filters: left:N, right:N, first:N, last:N, abbrev:N, upper, lower, default:x)", fname)
		}
		p.filters = append(p.filters, f)
	}
	return p, nil
}

func (p part) hasDefault() bool {
	for _, f := range p.filters {
		if f.name == "default" {
			return true
		}
	}
	return false
}

// apply runs the filters on a field value.
func (p part) apply(v string) string {
	for _, f := range p.filters {
		switch f.name {
		case "left":
			v += strings.Repeat(" ", max(f.n-len([]rune(v)), 0))
		case "right":
			v = strings.Repeat(" ", max(f.n-len([]rune(v)), 0)) + v
		case "first":
			if r := []rune(v); len(r) > f.n {
				v = string(r[:f.n])
			}
		case "last":
			if r := []rune(v); len(r) > f.n {
				v = string(r[len(r)-f.n:])
			}
		case "abbrev":
			v = Abbreviate(v, f.n)
		case "upper":
			v = strings.ToUpper(v)
		case "lower":
			v = strings.ToLower(v)
		case "default":
			if v == "" {
				v = f.arg
			}
		}
	}
	return v
}

// Abbreviate shortens a dotted name like Logback's %logger{n}: package
// segments are reduced to their first letter, from the left, until the
// name fits; the last segment is never shortened, and truncated from the
// left only as a last resort.
func Abbreviate(name string, n int) string {
	if len(name) <= n {
		return name
	}
	parts := strings.Split(name, ".")
	for i := 0; i < len(parts)-1 && len(strings.Join(parts, ".")) > n; i++ {
		if len(parts[i]) > 1 {
			parts[i] = parts[i][:1]
		}
	}
	out := strings.Join(parts, ".")
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
