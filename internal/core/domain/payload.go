package domain

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PayloadKind is what the bytes of a record key or value look like. No
// schema is declared for records: the kind is detected on each one.
type PayloadKind int

// Payload kinds.
const (
	PayloadNull   PayloadKind = iota // no value (a tombstone for a value)
	PayloadEmpty                     // zero bytes
	PayloadJSON                      // a JSON object or array
	PayloadText                      // printable UTF-8
	PayloadFramed                    // schema registry framing: 0x00 + 4-byte schema id
	PayloadBinary                    // anything else
)

// ClassifyPayload returns the kind of b and, for PayloadFramed, the schema
// id. Text may hold tabs and newlines; other control characters make it
// binary. A truncated JSON value is classified as text.
func ClassifyPayload(b []byte) (PayloadKind, uint32) {
	switch {
	case b == nil:
		return PayloadNull, 0
	case len(b) == 0:
		return PayloadEmpty, 0
	}
	if id, ok := FramedSchemaID(b); ok {
		return PayloadFramed, id
	}
	if t := bytes.TrimLeft(b, " \t\r\n"); len(t) > 0 && (t[0] == '{' || t[0] == '[') && json.Valid(b) {
		return PayloadJSON, 0
	}
	if utf8.Valid(b) && printable(b) {
		return PayloadText, 0
	}
	return PayloadBinary, 0
}

func printable(b []byte) bool {
	for _, r := range string(b) {
		if r != '\n' && r != '\t' && r != '\r' && !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// previewBytes is how much of a value its one-line preview looks at: the
// preview shows a few hundred characters, so a large value costs no more
// than a small one.
const previewBytes = 16 << 10

// PayloadPreview is b on one line of at most maxRunes runes: compact JSON,
// text with its line breaks shown as ⏎, or a description of binary data.
// Only the start of b is examined. Control characters never reach the
// terminal.
func PayloadPreview(b []byte, size int, maxRunes int) string {
	switch {
	case b == nil:
		return "∅"
	case len(b) == 0:
		return `""`
	case b[0] == 0 && len(b) >= 5:
		return fmt.Sprintf("schema %d, %s", binary.BigEndian.Uint32(b[1:5]), ByteSize(size))
	}
	head := b
	if len(head) > previewBytes {
		head = head[:previewBytes]
		for len(head) > 0 && !utf8.RuneStart(head[len(head)-1]) { // do not cut a rune
			head = head[:len(head)-1]
		}
		if len(head) > 0 {
			head = head[:len(head)-1]
		}
	}
	if !utf8.Valid(head) || !printable(head) {
		return "binary " + ByteSize(size)
	}
	s := string(head)
	if t := bytes.TrimLeft(head, " \t\r\n"); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		s = compactJSON(head)
	}
	s = strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\r", "⏎", "\t", " ").Replace(s)
	return cutRunes(escapeControls(s), maxRunes)
}

// compactJSON drops the whitespace outside strings, as json.Compact does,
// without validating: b may be the start of a document.
func compactJSON(b []byte) string {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for _, c := range b {
		switch {
		case inString:
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		default:
			out = append(out, c)
			inString = c == '"'
		}
	}
	return string(out)
}

// PayloadLines is b laid out for reading in full: indented JSON, text as
// written, or a hex dump of binary data, as lines with control characters
// escaped.
func PayloadLines(b []byte) []string {
	kind, _ := ClassifyPayload(b)
	var s string
	switch kind {
	case PayloadNull:
		return []string{"∅ (null)"}
	case PayloadEmpty:
		return []string{`"" (empty)`}
	case PayloadJSON:
		var buf bytes.Buffer
		if json.Indent(&buf, b, "", "  ") == nil {
			s = buf.String()
		} else {
			s = string(b)
		}
	case PayloadText:
		s = string(b)
	default:
		s = strings.TrimRight(hex.Dump(b), "\n")
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = escapeControls(strings.ReplaceAll(l, "\t", "    "))
	}
	return lines
}

// EscapeControls writes control characters (and the bytes of invalid
// UTF-8) as \xNN, for record data printed on a terminal.
func EscapeControls(s string) string { return escapeControls(s) }

// escapeControls writes control characters (and the bytes of invalid
// UTF-8) as \xNN, so record data cannot drive the terminal.
func escapeControls(s string) string {
	clean := true
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unicode.IsControl(r):
			if r < 0x100 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

func cutRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n-1 {
			return s[:j] + "…"
		}
		i++
	}
	return s
}

// ByteSize formats a size for people: 88 B, 1.5 KiB, 12 MiB.
func ByteSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1024)) + " KiB"
	default:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/(1<<20))) + " MiB"
	}
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }
