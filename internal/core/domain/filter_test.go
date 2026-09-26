package domain

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func entry(msg string) *LogEntry {
	return &LogEntry{Structured: true, Level: LevelInfo, Message: msg, Logger: "io.acme.PaymentService", Thread: "exec-1"}
}

func TestTextFilterMatching(t *testing.T) {
	e := entry("Payment authorization FAILED orderId=ord_8f91a2")
	e.Fields = map[string]string{"extra.customer": "bob@example.com"}
	e.Stack = "java.net.SocketTimeoutException: Read timed out"
	e.Hidden = map[string]string{"kubernetes.namespace_name": "app-rec"}
	tests := []struct {
		in    string
		regex bool
		want  bool
	}{
		{"failed", false, true},
		{"PAYMENTSERVICE", false, true}, // logger
		{"exec-1", false, true},         // thread
		{"bob@example", false, true},    // field value
		{"extra.customer", false, true}, // field key
		{"sockettimeout", false, true},  // stack
		{"app-rec", false, false},       // hidden metadata is not searched
		{"!health", false, true},
		{"!failed", false, false},
		{"ord_[0-9a-f]+", true, true},
		{"ord_[0-9a-f]+", false, false},
		{`timeout|refused`, true, true},
		{"", false, true},
	}
	for _, tt := range tests {
		f, err := ParseTextFilter(tt.in, tt.regex)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Matches(e); got != tt.want {
			t.Errorf("%q regex=%v: %v", tt.in, tt.regex, got)
		}
	}
}

func TestTextFilterParse(t *testing.T) {
	if _, err := ParseTextFilter("a(b", true); err == nil || !strings.Contains(err.Error(), "invalid regex") {
		t.Fatalf("err %v", err)
	}
	f, _ := ParseTextFilter(`\!important`, false)
	if f.Invert || f.Pattern != "!important" || !f.Matches(entry("this is !important")) {
		t.Fatalf("escape: %+v", f)
	}
	if s := mustFilter(t, "!a|b", true).String(); s != "!/a|b/" {
		t.Fatalf("String %q", s)
	}
	if f, _ := ParseTextFilter("a(b", false); !f.Matches(entry("x a(b y")) {
		t.Fatal("substring mode must quote special characters")
	}
}

func mustFilter(t *testing.T, s string, regex bool) TextFilter {
	t.Helper()
	f, err := ParseTextFilter(s, regex)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestUnstructuredEntriesSearchRaw(t *testing.T) {
	e := &LogEntry{Raw: "E0926 reflector failed", Message: "E0926 reflector failed"}
	if !mustFilter(t, "reflector", false).Matches(e) {
		t.Fatal("raw line must be searched")
	}
}

func TestRanges(t *testing.T) {
	f := mustFilter(t, "ab", false)
	got := f.Ranges("xABxab")
	if fmt.Sprint(got) != "[[1 3] [4 6]]" {
		t.Fatalf("ranges %v", got)
	}
	if mustFilter(t, "!ab", false).Ranges("ab") != nil {
		t.Fatal("inverted filters highlight nothing")
	}
	lf := LogFilter{Texts: []TextFilter{mustFilter(t, "é", false), mustFilter(t, "b", false)}}
	if got := lf.Ranges("éb"); fmt.Sprint(got) != "[[0 2] [2 3]]" {
		t.Fatalf("unicode ranges %v", got)
	}
}

func seq(levels string) []*LogEntry {
	var out []*LogEntry
	for i, c := range levels {
		e := entry(fmt.Sprintf("line %d", i))
		switch c {
		case 'E':
			e.Level, e.Message = LevelError, fmt.Sprintf("line %d boom", i)
		case 'W':
			e.Level = LevelWarn
		case 'U':
			e.Level = LevelUnknown
		}
		out = append(out, e)
	}
	return out
}

func indexes(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		switch {
		case r.Gap:
			b.WriteString("|")
		}
		if r.Context {
			fmt.Fprintf(&b, "(%d)", r.Index)
		} else {
			fmt.Fprintf(&b, "%d", r.Index)
		}
		b.WriteString(" ")
	}
	return strings.TrimSpace(b.String())
}

func TestSelect(t *testing.T) {
	es := seq("IIEIIIIIEIWU")
	get := func(i int) *LogEntry { return es[i] }
	boom := mustFilter(t, "boom", false)

	all := NewLogFilter()
	if got := indexes(all.Select(len(es), get)); got != "0 1 2 3 4 5 6 7 8 9 10 11" {
		t.Errorf("no filter: %s", got)
	}
	levels := NewLogFilter()
	levels.Levels = LevelSet{LevelError: true, LevelWarn: true}
	if got := indexes(levels.Select(len(es), get)); got != "2 8 10" {
		t.Errorf("levels: %s", got)
	}
	f := NewLogFilter()
	f.Texts = []TextFilter{boom}
	if got := indexes(f.Select(len(es), get)); got != "2 8" {
		t.Errorf("filter: %s", got)
	}
	f.Context = 1
	if got := indexes(f.Select(len(es), get)); got != "(1) 2 (3) |(7) 8 (9)" {
		t.Errorf("context 1: %s", got)
	}
	f.Context = 3
	if got := indexes(f.Select(len(es), get)); got != "(0) (1) 2 (3) (4) (5) 8 (9) (10) (11)" && got != "(0) (1) 2 (3) (4) (5) (6) (7) 8 (9) (10) (11)" {
		t.Errorf("context 3: %s", got)
	}
	f.Mode, f.Context = ModeHighlight, 0
	rows := f.Select(len(es), get)
	if len(rows) != 12 || !rows[2].Match || rows[3].Match {
		t.Errorf("highlight keeps all rows and flags matches: %+v", rows[:4])
	}
	stacked := NewLogFilter()
	stacked.Texts = []TextFilter{mustFilter(t, "line", false), mustFilter(t, "!boom", false)}
	if got := indexes(stacked.Select(len(es), get)); strings.Contains(got, "2") || strings.Contains(got, " 8") {
		t.Errorf("stacked AND: %s", got)
	}
}

func BenchmarkSelectSubstring(b *testing.B) {
	benchSelect(b, "timeout", false)
}

// BenchmarkSelectRegexWorstCase: every line contains a prefilter literal,
// so the regex runs on all 50 000 lines.
func BenchmarkSelectRegexWorstCase(b *testing.B) {
	benchSelect(b, `ord_[0-9a-f]{6}|timeout`, true)
}

// BenchmarkSelectRegexAlternation: a plain alternation of words never runs
// the regex engine.
func BenchmarkSelectRegexAlternation(b *testing.B) {
	benchSelect(b, `timeout|refused|reset`, true)
}

func benchSelect(b *testing.B, pattern string, regex bool) {
	es := make([]*LogEntry, 50000)
	for i := range es {
		e := entry(fmt.Sprintf("request completed POST /v1/payments status=201 duration=%dms traceId=%08x", i%500, i))
		e.Fields = map[string]string{"spanId": "91ac07", "extra.orderId": "ord_8f91a2"}
		es[i] = e
	}
	tf, _ := ParseTextFilter(pattern, regex)
	f := NewLogFilter()
	f.Texts = []TextFilter{tf}
	get := func(i int) *LogEntry { return es[i] }
	for b.Loop() {
		f.Select(len(es), get)
	}
}

func TestRegexLiterals(t *testing.T) {
	tests := []struct {
		pattern string
		lits    string
		exact   bool
	}{
		{"timeout|refused", "timeout,refused", true},
		{"ord_[0-9a-f]{6}", "ord_", false},
		{"Gateway (timeout|reset)", "gateway ", false},
		{"(foo|bar)+x", "foo,bar", false},
		{"a.*b", "", false},
		{"[0-9]+", "", false},
		{"x|longer", "", false},
	}
	for _, tt := range tests {
		lits, exact := regexLiterals(tt.pattern)
		if strings.Join(lits, ",") != tt.lits || exact != tt.exact {
			t.Errorf("%q: %v %v", tt.pattern, lits, exact)
		}
	}
	// The prefilter must never change the result.
	for _, p := range []string{"timeout|refused", "ord_[0-9a-f]{6}", "Gateway (timeout|reset)", "PAY.*FAIL"} {
		f := mustFilter(t, p, true)
		re := regexp.MustCompile("(?i)" + p)
		for _, text := range []string{"gateway timeout here", "ord_8f91a2", "ord_zz", "payment failed", "refused!", "nothing"} {
			if got, want := f.matchLower(strings.ToLower(text)), re.MatchString(text); got != want {
				t.Errorf("%q on %q: %v, want %v", p, text, got, want)
			}
		}
	}
}
