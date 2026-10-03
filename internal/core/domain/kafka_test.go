package domain

import (
	"bytes"
	"strings"
	"testing"
)

func TestTopicDirection(t *testing.T) {
	cases := []struct{ a, b, want TopicDirection }{
		{TopicNone, TopicConsume, TopicConsume},
		{TopicConsume, TopicNone, TopicConsume},
		{TopicConsume, TopicConsume, TopicConsume},
		{TopicConsume, TopicProduce, TopicBoth},
		{TopicBoth, TopicProduce, TopicBoth},
	}
	for _, c := range cases {
		if got := c.a.With(c.b); got != c.want {
			t.Errorf("%v.With(%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func rec(off int64, value string) KafkaRecord {
	return KafkaRecord{Topic: "t", Offset: off, Value: []byte(value), ValueSize: len(value)}
}

func TestRecordBufferBoundsCountAndBytes(t *testing.T) {
	b := NewRecordBuffer(3, 1<<20)
	for i := range 5 {
		b.Append(rec(int64(i), "v"))
	}
	if b.Len() != 3 || b.Dropped() != 2 || b.At(0).Offset != 2 || b.At(2).Seq != 5 || b.FirstSeq() != 3 {
		t.Fatalf("count bound: len %d dropped %d first %+v", b.Len(), b.Dropped(), *b.At(0))
	}
	if i, ok := b.Index(4); !ok || i != 1 {
		t.Errorf("Index(4) = %d %v", i, ok)
	}
	if _, ok := b.Index(2); ok {
		t.Error("evicted record still indexed")
	}

	big := strings.Repeat("x", 1000)
	b = NewRecordBuffer(1000, 3*(1000+65))
	for i := range 10 {
		b.Append(rec(int64(i), big))
	}
	if b.Len() != 3 || b.Bytes() > 3*(1000+65) || b.At(2).Offset != 9 {
		t.Fatalf("byte bound: len %d bytes %d", b.Len(), b.Bytes())
	}
	huge := NewRecordBuffer(10, 10)
	huge.Append(rec(1, big))
	if huge.Len() != 1 {
		t.Fatal("the newest record always stays")
	}
}

func TestRecordBufferCompactsAndResets(t *testing.T) {
	b := NewRecordBuffer(100, 1<<30)
	for i := range 10_000 {
		b.Append(rec(int64(i), "v"))
	}
	if b.Len() != 100 || b.At(99).Offset != 9999 || cap(b.recs) > 1000 {
		t.Fatalf("len %d cap %d", b.Len(), cap(b.recs))
	}
	b.Reset()
	b.Append(rec(1, "v"))
	if b.Len() != 1 || b.At(0).Seq != 10_001 || b.Dropped() != 0 {
		t.Fatalf("after reset: %+v", *b.At(0))
	}
}

func TestTruncateRecord(t *testing.T) {
	r := KafkaRecord{Key: []byte("abcdef"), Value: bytes.Repeat([]byte("v"), 100)}
	TruncateRecord(&r, 4)
	if string(r.Key) != "abcd" || len(r.Value) != 4 || r.KeySize != 6 || r.ValueSize != 100 || !r.Truncated() {
		t.Fatalf("%+v", r)
	}
	r = KafkaRecord{Value: []byte("ok")}
	TruncateRecord(&r, 0)
	if r.ValueSize != 2 || r.Truncated() {
		t.Fatalf("%+v", r)
	}
}

func TestClassifyAndPreviewPayload(t *testing.T) {
	cases := []struct {
		in   []byte
		kind PayloadKind
		want string
	}{
		{nil, PayloadNull, "∅"},
		{[]byte{}, PayloadEmpty, `""`},
		{[]byte(" {\"a\": 1,\n \"b\": [true]}"), PayloadJSON, `{"a":1,"b":[true]}`},
		{[]byte("line one\nline two\tend"), PayloadText, "line one⏎line two end"},
		{[]byte{0, 0, 0, 1, 0x9c, 'x', 'y'}, PayloadFramed, "schema 412, 7 B"},
		{[]byte{0xff, 0xfe, 0x01}, PayloadBinary, "binary 3 B"},
		{[]byte("{truncated"), PayloadText, "{truncated"},
	}
	for _, c := range cases {
		if k, _ := ClassifyPayload(c.in); k != c.kind {
			t.Errorf("%q: kind %d, want %d", c.in, k, c.kind)
		}
		if got := PayloadPreview(c.in, len(c.in), 80); got != c.want {
			t.Errorf("%q: preview %q, want %q", c.in, got, c.want)
		}
	}
	if got := PayloadPreview([]byte("abcdefghij"), 10, 5); got != "abcd…" {
		t.Errorf("cut: %q", got)
	}
}

func TestPayloadNeverCarriesControlCharacters(t *testing.T) {
	evil := []byte("ok \x1b]8;;http://x\x07click\x1b[2J")
	if k, _ := ClassifyPayload(evil); k != PayloadBinary {
		t.Fatalf("escape sequences are not text: %d", k)
	}
	text := []byte("a\u0085b \u200b") // a C1 control inside valid UTF-8 text
	for _, s := range append(PayloadLines(text), PayloadPreview(text, len(text), 80)) {
		if strings.ContainsRune(s, 0x85) || strings.ContainsRune(s, 0x1b) {
			t.Errorf("control character kept: %q", s)
		}
	}
	for _, l := range PayloadLines(evil) {
		if strings.ContainsRune(l, 0x1b) {
			t.Errorf("hex dump keeps the escape: %q", l)
		}
	}
}

func TestPayloadLines(t *testing.T) {
	got := PayloadLines([]byte(`{"a":{"b":1}}`))
	if strings.Join(got, "|") != `{|  "a": {|    "b": 1|  }|}` {
		t.Errorf("json: %q", got)
	}
	if got := PayloadLines([]byte{0, 1, 2}); len(got) != 1 || !strings.HasPrefix(got[0], "00000000  00 01 02") {
		t.Errorf("hex: %q", got)
	}
}

func TestRecordFilter(t *testing.T) {
	r := &KafkaRecord{Partition: 3, Key: []byte("Order-42"), Value: []byte(`{"status":"PAID"}`), Headers: []KafkaHeader{{Key: "traceId", Value: []byte("abc123")}}}
	cases := map[string]bool{
		"":                             true,
		"order-42":                     true,
		"Order":                        true,
		"ORDER":                        false, // upper case: exact case
		"paid":                         true,
		"key=order":                    true,
		"key=paid":                     false,
		"partition=3":                  true,
		"partition=4":                  false,
		"header.traceId=abc":           true,
		"header.traceId=zzz":           false,
		"header.other=abc":             false,
		"abc123":                       true,
		"paid partition=3":             true,
		"paid partition=2":             false,
		"key=order header.traceId=123": true,
	}
	for s, want := range cases {
		f, err := ParseRecordFilter(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if got := f.Match(r); got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
	if _, err := ParseRecordFilter("partition=x"); err == nil {
		t.Error("partition=x accepted")
	}
}

func FuzzPayload(f *testing.F) {
	f.Add([]byte(`{"a":1}`))
	f.Add([]byte{0, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, s := range append(PayloadLines(b), PayloadPreview(b, len(b), 40)) {
			if strings.ContainsAny(s, "\x1b\x07\x00") {
				t.Fatalf("control character in %q", s)
			}
		}
	})
}

// BenchmarkPayloadPreviewLargeJSON previews a 1 MiB JSON value (once per
// record: the screen caches it).
func BenchmarkPayloadPreviewLargeJSON(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString(`{"items":[`)
	for buf.Len() < 1<<20 {
		buf.WriteString(`{"id":"ORD-12345","status":"PAID","amount":125.50,"tags":["a","b"]},`)
	}
	buf.WriteString(`{}]}`)
	v := buf.Bytes()
	b.ReportAllocs()
	for b.Loop() {
		_ = PayloadPreview(v, len(v), 400)
	}
}

// BenchmarkRecordBufferAppend appends to a full buffer (eviction path).
func BenchmarkRecordBufferAppend(b *testing.B) {
	buf := NewRecordBuffer(20000, 64<<20)
	r := rec(0, `{"id":"ORD-12345","status":"PAID"}`)
	b.ReportAllocs()
	for b.Loop() {
		buf.Append(r)
	}
}

func TestPreviewOfLargeValuesLooksAtTheStart(t *testing.T) {
	big := append([]byte(`{ "a" : "x y",`+"\n"+` "é": [1, 2] `), bytes.Repeat([]byte(`, "k": "v"`), 1<<17)...)
	if got := PayloadPreview(big, len(big), 30); got != `{"a":"x y","é":[1,2],"k":"v",…` {
		t.Errorf("large JSON: %q", got)
	}
	text := append(bytes.Repeat([]byte("é"), previewBytes), 0xff) // cut inside a rune, junk far away
	if got := PayloadPreview(text, len(text), 5); got != "éééé…" {
		t.Errorf("large text: %q", got)
	}
	if got := PayloadPreview([]byte(`{"s":"a \" b  c"}`), 18, 80); got != `{"s":"a \" b  c"}` {
		t.Errorf("spaces inside strings are kept: %q", got)
	}
}
