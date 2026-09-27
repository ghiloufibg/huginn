package tui

import "testing"

func TestSafeText(t *testing.T) {
	for in, want := range map[string]string{
		"plain":              "plain",
		"\x1b[31mred\x1b[0m": "\x1b[31mred\x1b[0m",
		"\x1b]8;;https://evil.example\x1b\\click\x1b]8;;\x1b\\": "click",
		"\x1b]0;TITLE\x07title":                                 "title",
		"\x1b]52;c;ZWNobw==\x07clip":                            "clip",
		"a\x1b[2J\x1b[Hb":                                       "ab",
		"x\x1b[5A\x1b[10Cy":                                     "xy",
		"\x1bPdcs\x1b\\z":                                       "z",
		"end\x1b":                                               "end",
		"\x1b]8;;unterminated":                                  "",
	} {
		if got := safeText(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
