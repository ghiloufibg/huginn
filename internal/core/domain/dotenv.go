package domain

import (
	"bufio"
	"bytes"
	"strings"
)

// ParseDotenv reads KEY=VALUE lines into secrets. Blank lines and lines
// starting with # are skipped, a leading "export " is ignored, and a value
// wrapped in matching single or double quotes is unwrapped (double quotes
// also understand \n, \t, \" and \\). Nothing after an unquoted value is
// treated as a comment: a # may be part of a password. Lines without "="
// are skipped. The last occurrence of a key wins.
func ParseDotenv(b []byte) map[string]Secret {
	out := map[string]Secret{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = NewSecret(unquote(strings.TrimSpace(v)))
	}
	return out
}

func unquote(v string) string {
	if len(v) < 2 {
		return v
	}
	switch q := v[0]; {
	case q == '\'' && v[len(v)-1] == '\'':
		return v[1 : len(v)-1]
	case q == '"' && v[len(v)-1] == '"':
		return unescape(v[1 : len(v)-1])
	}
	return v
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"', '\\':
				b.WriteByte(s[i+1])
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
