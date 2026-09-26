package logformat

import (
	"regexp"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// FuzzDecoders feeds arbitrary lines to every decoder: they must never
// panic and always keep the line.
func FuzzDecoders(f *testing.F) {
	for _, s := range []string{`{"message":"m","level":"INFO"}`, `{"a":{"b":[1,{"c":null}]}}`, `{"@timestamp":1790431703123}`, `{`, "", `10.0.0.1 - - [26/Sep/2026:19:12:40 +0200] "GET /" 503 12`, "\x00\xff"} {
		f.Add(s)
	}
	decs := []interface {
		Decode(domain.RawLine) domain.LogEntry
	}{
		NewJSON(logstash),
		NewPlain("p"),
		NewRegex(RegexProfile{Name: "r", Pattern: regexp.MustCompile(`^(?P<remote>\S+) \S+ \S+ \[(?P<time>[^\]]+)\] "(?P<message>[^"]*)" (?P<status>\d{3})`), TimeFormat: "02/Jan/2006:15:04:05 -0700", LevelField: "status", LevelRules: []LevelRule{{"5*", domain.LevelError}, {"*", domain.LevelInfo}}}),
	}
	f.Fuzz(func(t *testing.T, line string) {
		for _, d := range decs {
			e := d.Decode(domain.RawLine{Text: line})
			if e.Raw != line {
				t.Fatalf("raw line lost: %q", line)
			}
			_ = e.HiddenFields()
		}
	})
}
