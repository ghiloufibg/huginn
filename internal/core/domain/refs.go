package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The Kafka profiles of the config folder (docs/CONFIG.md, kafka/) write
// two kinds of references in their strings, always expanded in this
// order:
//
//   - {name}: a placeholder — env, repo, repo_dir or a profile variable —
//     replaced by ExpandVars;
//   - ${KEY} or ${KEY:-default}: a key of the profile's sources, replaced
//     by ResolveKeys.
//
// So ${{account}_PASSWORD} reads ORDERS_PASSWORD when account is ORDERS.
// A "{" right after "$" opens a key, never a placeholder, and "$$" is a
// literal "$".

// MissingVarError names a placeholder with no value.
type MissingVarError struct{ Name string }

func (e *MissingVarError) Error() string { return fmt.Sprintf("unknown placeholder {%s}", e.Name) }

// MissingKeyError names a ${KEY} reference absent from the sources and
// without default.
type MissingKeyError struct{ Key string }

func (e *MissingKeyError) Error() string { return e.Key + " not found in the sources" }

// ErrRefSyntax marks a malformed ${…} reference.
var ErrRefSyntax = errors.New("malformed reference")

// isVarName reports whether s is a placeholder name: a lower-case letter,
// then lower-case letters, digits and underscores.
func isVarName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// IsVarName reports whether s may name a profile variable.
func IsVarName(s string) bool { return isVarName(s) }

// placeholderAt returns the placeholder name starting at s[i] == '{', if
// any. A "{" preceded by "$" opens a key reference instead.
func placeholderAt(s string, i int) (string, bool) {
	if i > 0 && s[i-1] == '$' {
		return "", false
	}
	end := strings.IndexByte(s[i+1:], '}')
	if end < 0 {
		return "", false
	}
	name := s[i+1 : i+1+end]
	return name, isVarName(name)
}

// VarNames returns the placeholder names used in s, in order of first use.
func VarNames(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		if name, ok := placeholderAt(s, i); ok {
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
			i += len(name) + 1
		}
	}
	return out
}

// ExpandVars replaces every {name} placeholder of s with lookup(name). An
// unknown name is a *MissingVarError. Text that is not a placeholder
// (upper-case names, a lone brace) is kept as written.
func ExpandVars(s string, lookup func(string) (string, bool)) (string, error) {
	if !strings.Contains(s, "{") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			if name, ok := placeholderAt(s, i); ok {
				v, found := lookup(name)
				if !found {
					return "", &MissingVarError{Name: name}
				}
				b.WriteString(v)
				i += len(name) + 1
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

// keyRef is one ${KEY} or ${KEY:-default} reference.
type keyRef struct {
	key, def   string
	hasDefault bool
	end        int // index just after the closing brace
}

// parseKeyRef parses the reference starting at s[i:] == "${".
func parseKeyRef(s string, i int) (keyRef, error) {
	end := strings.IndexByte(s[i+2:], '}')
	if end < 0 {
		return keyRef{}, fmt.Errorf("%w: unclosed ${", ErrRefSyntax)
	}
	body := s[i+2 : i+2+end]
	r := keyRef{end: i + 3 + end}
	r.key, r.def, r.hasDefault = strings.Cut(body, ":-")
	if r.key == "" {
		return keyRef{}, fmt.Errorf("%w: empty key in ${}", ErrRefSyntax)
	}
	for _, c := range r.key {
		if !isKeyRune(c) {
			return keyRef{}, fmt.Errorf("%w: %q is not a key name (letters, digits, '_', '.', '-')", ErrRefSyntax, r.key)
		}
	}
	return r, nil
}

func isKeyRune(c rune) bool {
	return c == '_' || c == '.' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// CheckRefs reports the first malformed ${…} reference of s (its message
// never quotes s, which may hold a credential), ignoring
// placeholders (they are checked against the known names separately).
func CheckRefs(s string) error {
	_, err := resolveKeys(s, func(string) (string, bool) { return "", true })
	return err
}

// KeyNames returns the keys referenced by s, in order of first use; s must
// have its placeholders expanded already.
func KeyNames(s string) []string {
	var out []string
	_, _ = resolveKeys(s, func(k string) (string, bool) {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
		return "", true
	})
	return out
}

// ResolveKeys replaces every ${KEY} and ${KEY:-default} of s with the value
// of KEY from lookup; the default applies when the key is absent or empty.
// A key absent without default is a *MissingKeyError; "$$" is a literal
// "$". Placeholders must be expanded first.
func ResolveKeys(s string, lookup func(string) (string, bool)) (string, error) {
	return resolveKeys(s, lookup)
}

func resolveKeys(s string, lookup func(string) (string, bool)) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '{':
			r, err := parseKeyRef(s, i)
			if err != nil {
				return "", err
			}
			v, ok := lookup(r.key)
			switch {
			case ok && v != "":
				b.WriteString(v)
			case r.hasDefault:
				b.WriteString(r.def)
			case ok:
			default:
				return "", &MissingKeyError{Key: r.key}
			}
			i = r.end - 1
		default:
			b.WriteByte('$')
		}
	}
	return b.String(), nil
}
