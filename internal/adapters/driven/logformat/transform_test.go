package logformat

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// mdc strips a "key=value… - message - key=value…" context around the
// message, the way a Logback MDC pattern writes it.
var mdc = FieldTransform{Field: "message", Pattern: regexp.MustCompile(`^(?:[\w.-]+=\S*\s+)*-\s+(?P<message>.*?)\s+-\s+(?:[\w.-]+=\S*\s*)*$`)}

func withTransforms(ts ...FieldTransform) Profile {
	p := logstash
	p.Transforms = ts
	return p
}

func TestTransformStripsMessage(t *testing.T) {
	d := NewJSON(withTransforms(mdc))
	for _, tc := range []struct{ name, message, want string }{
		{"context around", "route=R method=GET correlation-id= business_id= - Widget created - user_id=U request_id= http_status=", "Widget created"},
		{"empty context", "route= method= - Batch tick - user_id= result=", "Batch tick"},
		{"message with a dash", "route=R - Widget created - successfully - user_id=U", "Widget created - successfully"},
		{"no match keeps the value", "plain message without context", "plain message without context"},
		{"empty group", "route=R -  - user_id=U", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"message":"` + tc.message + `","logger":"a.B"}`
			e := d.Decode(raw(line))
			if e.Message != tc.want {
				t.Fatalf("message %q, want %q", e.Message, tc.want)
			}
			if e.Raw != line || e.Logger != "a.B" || len(e.Fields) != 0 {
				t.Errorf("the rest of the entry changed: %+v", e)
			}
		})
	}
}

func TestTransformOtherFieldsAndOrder(t *testing.T) {
	short := FieldTransform{Field: "logger", Pattern: regexp.MustCompile(`(?P<logger>[^.]+)$`)}
	d := NewJSON(withTransforms(mdc, short))
	e := d.Decode(raw(`{"message":"k=v - hello - k=v","logger":"a.b.Service","thread":"t-1"}`))
	if e.Message != "hello" || e.Logger != "Service" || e.Thread != "t-1" {
		t.Fatalf("got %+v", e)
	}
}

func TestTransformLeavesOtherLinesAlone(t *testing.T) {
	d := NewJSON(withTransforms(mdc))
	if e := d.Decode(raw(`{"logger":"a.B"}`)); e.Message != "" || !e.Structured {
		t.Errorf("a line without the field: %+v", e)
	}
	banner := "k=v - not json - k=v"
	if e := d.Decode(raw(banner)); e.Message != banner || e.Structured {
		t.Errorf("non-JSON lines are not transformed: %+v", e)
	}
}

func TestTransformKeepsValidUTF8(t *testing.T) {
	d := NewJSON(withTransforms(mdc))
	e := d.Decode(raw("{\"message\":\"k=\xff - caf\xc3\xa9 \xfe - k=v\"}"))
	if e.Message != "café �" || !utf8.ValidString(e.Message) {
		t.Fatalf("message %q", e.Message)
	}
}

// mdcPairs keeps the message and turns its context into fields.
var mdcPairs = FieldTransform{
	Field:   "message",
	Pattern: regexp.MustCompile(`^(?P<before>(?:[\w.-]+=\S*\s+)*)-\s+(?P<message>.*?)\s+-\s+(?P<after>(?:[\w.-]+=\S*\s*)*)$`),
	Pairs:   []string{"before", "after"},
}

func TestTransformExtractsPairs(t *testing.T) {
	d := NewJSON(withTransforms(mdcPairs))
	e := d.Decode(raw(`{"message":"route=/v1/w method=GET correlation-id=c1 business_id= - Widget created - user_id= request_id=r1 http_status=200","spanId":"s1"}`))
	want := map[string]string{"route": "/v1/w", "method": "GET", "correlation-id": "c1", "request_id": "r1", "http_status": "200", "spanId": "s1"}
	if e.Message != "Widget created" || !maps.Equal(e.Fields, want) {
		t.Fatalf("message %q fields %v", e.Message, e.Fields)
	}
	if e.LoadHidden != nil {
		t.Error("nothing is hidden")
	}
	if !search(t, &e, "correlation-id=c1") {
		t.Error("extracted fields are searchable")
	}
}

func TestTransformPairsKeepTextThatIsNotPairs(t *testing.T) {
	d := NewJSON(withTransforms(FieldTransform{
		Field: "message", Pattern: regexp.MustCompile(`^\[(?P<ctx>[^\]]*)\] (?P<message>.*)`), Pairs: []string{"ctx"},
	}))
	for _, tc := range []struct {
		message string
		want    map[string]string
	}{
		{"[a=1  b=] m", map[string]string{"a": "1"}},
		{"[a=1 oops] m", map[string]string{"ctx": "a=1 oops"}},
		{"[=1] m", map[string]string{"ctx": "=1"}},
		{"[] m", nil},
	} {
		e := d.Decode(raw(`{"message":"` + tc.message + `"}`))
		if e.Message != "m" || !maps.Equal(e.Fields, tc.want) {
			t.Errorf("%q: message %q fields %v", tc.message, e.Message, e.Fields)
		}
	}
}

func TestTransformPairPattern(t *testing.T) {
	d := NewJSON(withTransforms(FieldTransform{
		Field: "message", Pattern: regexp.MustCompile(`^\[(?P<ctx>[^\]]*)\] (?P<message>.*)`), Pairs: []string{"ctx"},
		PairPattern: regexp.MustCompile(`(?P<key>[\w.-]+): (?:"(?P<value>[^"]*)"|[^;"]*?);?`),
	}))
	for _, tc := range []struct {
		message string
		want    map[string]string
	}{
		{`[user: "bob smith"; route: "/a"] m`, map[string]string{"user": "bob smith", "route": "/a"}},
		{`[user: ""; route: "/a"] m`, map[string]string{"route": "/a"}},
		{`[user: bob] m`, map[string]string{"ctx": "user: bob"}}, // unquoted: "bob" is left over
		{`[user: "bob" oops] m`, map[string]string{"ctx": `user: "bob" oops`}},
		{`[] m`, nil},
	} {
		e := d.Decode(raw(`{"message":` + jsonString(tc.message) + `}`))
		if e.Message != "m" || !maps.Equal(e.Fields, tc.want) {
			t.Errorf("%q: message %q fields %v", tc.message, e.Message, e.Fields)
		}
	}
}

// defaultPairs is the default pair syntax written as a pair_pattern.
var defaultPairs = regexp.MustCompile(`(?P<key>[^\s=]+)=(?P<value>\S*)`)

func FuzzPairPatternMatchesDefault(f *testing.F) {
	for _, s := range []string{"a=1 b= c=x=y", "a=1 oops", "=1", "a==", "\ta=1\n b=2 ", "é=\u00a0x", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var byHand, byPattern []field
		okHand := splitPairs(s, func(k, v string) { byHand = append(byHand, field{k, v}) })
		okPattern := splitPairsWith(defaultPairs, s, func(k, v string) { byPattern = append(byPattern, field{k, v}) })
		if okHand != okPattern || !slices.Equal(byHand, byPattern) {
			t.Fatalf("%q: by hand %v %v, by pattern %v %v", s, okHand, byHand, okPattern, byPattern)
		}
	})
}

func TestTransformNamedGroups(t *testing.T) {
	d := NewJSON(withTransforms(FieldTransform{
		Field:   "message",
		Pattern: regexp.MustCompile(`^route=(?P<route>\S*) corr=(?P<trace_id>\S*) lvl=(?P<level>\S*) th=(?P<thread>\S*) - (?P<message>.*)`),
	}))
	e := d.Decode(raw(`{"message":"route=/a corr=c1 lvl=WARN th= - hello","traceId":"json-trace","thread":"t-1"}`))
	if e.Message != "hello" || e.Fields["route"] != "/a" || len(e.Fields) != 1 {
		t.Fatalf("message %q fields %v", e.Message, e.Fields)
	}
	if e.TraceID != "json-trace" || e.Thread != "t-1" {
		t.Errorf("a field the JSON set is kept: trace %q thread %q", e.TraceID, e.Thread)
	}
	if e.Level != domain.LevelWarn {
		t.Errorf("an empty standard field is filled: level %v", e.Level)
	}
	e = d.Decode(raw(`{"message":"route= corr=c1 lvl=oops th=x - hello"}`))
	if e.TraceID != "c1" || e.Thread != "x" || e.Level != domain.LevelUnknown || len(e.Fields) != 0 {
		t.Errorf("got %+v", e)
	}
}

func TestTransformJSONKeysWinAndFirstExtractedWins(t *testing.T) {
	d := NewJSON(withTransforms(FieldTransform{
		Field: "message", Pattern: regexp.MustCompile(`^(?P<a>\S+) (?P<ctx>.*) - (?P<message>.*)`), Pairs: []string{"ctx"},
	}))
	e := d.Decode(raw(`{"message":"g a=p b=p user.id=p severity=p - m","b":"json","user":{"id":"json"},"severity":"INFO"}`))
	want := map[string]string{"a": "g", "b": "json", "user.id": "json"}
	if e.Message != "m" || !maps.Equal(e.Fields, want) {
		t.Fatalf("message %q fields %v", e.Message, e.Fields)
	}
}

func TestTransformHiddenExtractedFields(t *testing.T) {
	p := withTransforms(mdcPairs)
	p.Hidden = []string{"x-*", "request_id"}
	d := NewJSON(p)
	e := d.Decode(raw(`{"message":"route=/a - m - x-forwarded-for=10.0.0.1 request_id=r1 user_id=u"}`))
	if !maps.Equal(e.Fields, map[string]string{"route": "/a", "user_id": "u"}) {
		t.Fatalf("fields %v", e.Fields)
	}
	if h := e.HiddenFields(); !maps.Equal(h, map[string]string{"x-forwarded-for": "10.0.0.1", "request_id": "r1"}) {
		t.Errorf("hidden %v", h)
	}
	if search(t, &e, "request_id=r1") {
		t.Error("hidden fields are not searched")
	}
}

func jsonString(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

// search reports whether a text filter for q finds e.
func search(t *testing.T, e *domain.LogEntry, q string) bool {
	t.Helper()
	f, err := domain.ParseTextFilter(q, false)
	if err != nil {
		t.Fatal(err)
	}
	return f.Matches(e)
}

func TestTransformLimits(t *testing.T) {
	limited := mdcPairs
	limited.MaxBytes, limited.MaxFields = 40, 2
	d := NewJSON(withTransforms(limited))
	e := d.Decode(raw(`{"message":"a=1 b=2 c=3 - m - d=4"}`))
	if e.Message != "m" || !maps.Equal(e.Fields, map[string]string{"a": "1", "b": "2"}) {
		t.Errorf("max_fields: message %q fields %v", e.Message, e.Fields)
	}
	long := "a=1 - " + strings.Repeat("x", 40) + " - b=2"
	if e := d.Decode(raw(`{"message":"` + long + `"}`)); e.Message != long || len(e.Fields) != 0 {
		t.Errorf("max_bytes: a longer value is left as it is: %q %v", e.Message, e.Fields)
	}
}

func TestTransformManyPairsIsLinear(t *testing.T) {
	var sb strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&sb, "k%d=v%d ", i%5000, i)
	}
	d := NewJSON(withTransforms(mdcPairs))
	start := time.Now()
	e := d.Decode(raw(`{"message":"` + sb.String() + `- m - a=b"}`))
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("20000 pairs took %v", took)
	}
	if e.Message != "m" || len(e.Fields) != 5001 || e.Fields["k7"] != "v7" {
		t.Errorf("message %q, %d fields, k7=%q: the first value of a key wins", e.Message, len(e.Fields), e.Fields["k7"])
	}
}

// TestTransformNeverPanics feeds transforms the validation would reject:
// the adapter must not rely on it, since hidden fields are decoded again
// on the UI goroutine.
func TestTransformNeverPanics(t *testing.T) {
	bad := []FieldTransform{
		{Field: "message"},
		{Field: "stack", Pattern: regexp.MustCompile(`(?P<stack>.*)`)},
		{Field: "message", Pattern: regexp.MustCompile(`(?P<x>.*)`), Pairs: []string{"missing", "x"}},
		{Field: "message", Pattern: regexp.MustCompile(`(?P<message>.*)`), Pairs: []string{"message"}},
		{Field: "message", Pattern: regexp.MustCompile(`(?P<ctx>.*)`), Pairs: []string{"ctx"}, PairPattern: regexp.MustCompile(`(\w+)=(\w*)`)},
		{Field: "message", Pattern: regexp.MustCompile(`(?P<ctx>.*)`), Pairs: []string{"ctx"}, PairPattern: regexp.MustCompile(`(?P<key>\w+)=\w*`)},
		{Field: "message", Pattern: regexp.MustCompile(`(?P<ctx>.*)`), Pairs: []string{"ctx"}, PairPattern: regexp.MustCompile(`(?P<key>)`)},
		{Field: "message", Pattern: regexp.MustCompile(`(?P<time>\S+) (?P<stack>\S+) (?P<level>\S+)`)},
	}
	p := withTransforms(bad...)
	p.Hidden = []string{"*"}
	d := NewJSON(p)
	for _, line := range []string{`{"message":"a=1 b=2 c"}`, `{"message":""}`, `{"message":7}`, `{"message":{"a":1}}`, `{}`} {
		e := d.Decode(raw(line))
		_ = e.HiddenFields()
	}
}

func FuzzTransform(f *testing.F) {
	for _, s := range []string{"k=v - m - k=v", "", " - ", "k= -  - ", "\xff - \xfe - k="} {
		f.Add(s)
	}
	strip, pairs := NewJSON(withTransforms(mdc)), NewJSON(withTransforms(mdcPairs))
	f.Fuzz(func(t *testing.T, message string) {
		q, _ := json.Marshal(message)
		for _, d := range []*JSONDecoder{strip, pairs} {
			e := d.Decode(raw(`{"message":` + string(q) + `}`))
			if !utf8.ValidString(e.Message) {
				t.Fatalf("invalid UTF-8 %q", e.Message)
			}
			for k, v := range e.Fields {
				if k == "" || v == "" || !utf8.ValidString(k+v) {
					t.Fatalf("field %q=%q", k, v)
				}
			}
		}
	})
}

// mdcLine is a line whose message carries a 13-key MDC context, mostly empty.
var mdcLine = raw(`{"time":"2026-09-26T18:53:10.729Z","severity":"INFO","logger":"com.example.widgets.WidgetService","thread":"http-nio-8080-exec-1","message":"route=/v1/widgets method=POST correlation-id=bc9632dd business_id= - Widget created - user_id= x-forwarded-for= request_id= http_status= result= status_code= error_code= activity_id= activity_name= process_instance_id=","service":"example-service-dev"}`)

func BenchmarkJSONTransform(b *testing.B) {
	for _, bc := range []struct {
		name string
		p    Profile
	}{{"none", logstash}, {"strip", withTransforms(mdc)}, {"pairs", withTransforms(mdcPairs)}} {
		b.Run(bc.name, func(b *testing.B) {
			d := NewJSON(bc.p)
			b.ReportAllocs()
			for b.Loop() {
				d.Decode(mdcLine)
			}
		})
	}
}
