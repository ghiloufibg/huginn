package tui

import "strings"

// safeText keeps the colours an application writes in its logs (SGR
// sequences) and removes every other escape sequence: hyperlinks (OSC 8,
// whose text can hide another address), titles, clipboard writes, cursor
// moves. Log lines are untrusted input drawn on the user's terminal.
func safeText(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[': // CSI: parameters, then a final byte in 0x40–0x7e
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				b.WriteString(s[i : j+1])
			}
			i = j + 1
		case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS: up to BEL or ST
			j := i + 2
			for j < len(s) && s[j] != 0x07 && (s[j] != 0x1b || j+1 >= len(s) || s[j+1] != '\\') {
				j++
			}
			switch {
			case j >= len(s):
				i = j
			case s[j] == 0x07:
				i = j + 1
			default:
				i = j + 2
			}
		default: // two-byte sequence
			i += 2
		}
	}
	return b.String()
}
