package logformat

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// stdDecode is the former encoding/json implementation, kept as an oracle:
// the fastjson decoder must read the same lines the same way.
func stdDecode(d *JSONDecoder, text string) (domain.LogEntry, map[string]string, bool) {
	var obj map[string]any
	if !strings.HasPrefix(strings.TrimSpace(text), "{") || json.Unmarshal([]byte(strings.TrimSpace(text)), &obj) != nil {
		return domain.LogEntry{}, nil, false
	}
	var e domain.LogEntry
	used := map[string]bool{}
	take := func(paths []string) (any, bool) {
		for _, p := range paths {
			if v, ok := stdLookup(obj, p); ok {
				used[p] = true
				return v, true
			}
		}
		return nil, false
	}
	if v, ok := take(d.p.Timestamp); ok {
		switch x := v.(type) {
		case string:
			e.Time, _ = parseTimeText(x)
		case float64:
			if x > 1e12 {
				e.Time = time.UnixMilli(int64(x))
			} else {
				sec := int64(x)
				e.Time = time.Unix(sec, int64((x-float64(sec))*1e9))
			}
		}
	}
	if v, ok := take(d.p.Level); ok {
		e.Level, _ = domain.ParseLevelWith(stdStringify(v), d.p.LevelAliases)
	}
	str := func(paths []string) string {
		if v, ok := take(paths); ok {
			return stdStringify(v)
		}
		return ""
	}
	e.Logger, e.Thread, e.Message = str(d.p.Logger), str(d.p.Thread), str(d.p.Message)
	e.Stack, e.TraceID, e.App, e.PID = str(d.p.Stack), str(d.p.TraceID), str(d.p.App), str(d.p.PID)
	flat := map[string]string{}
	stdFlatten("", true, obj, flat)
	if _, ambiguous := flat[collision]; ambiguous {
		return domain.LogEntry{}, nil, false
	}
	fields := map[string]string{}
	for k, v := range flat {
		consumed := used[k]
		for u := range used {
			// Only a consumed object hides its children; a literal key
			// "level.x" next to a consumed "level" is its own field (the
			// former implementation dropped it).
			if v, ok := stdLookup(obj, u); ok {
				if _, isObj := v.(map[string]any); isObj {
					consumed = consumed || strings.HasPrefix(k, u+".")
				}
			}
		}
		if !consumed && !hiddenOrUnder(d, k) {
			fields[k] = v
		}
	}
	return e, fields, true
}

// collision marks keys like "." and "" -> "" that flatten to the same
// dotted path: both implementations then keep one of them, arbitrarily.
const collision = "\x00collision"

func stdLookup(obj map[string]any, p string) (any, bool) {
	if v, ok := obj[p]; ok && v != nil {
		return v, true
	}
	cur := any(obj)
	for _, part := range strings.Split(p, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

func stdFlatten(prefix string, top bool, v any, out map[string]string) {
	m, ok := v.(map[string]any)
	if !ok {
		if _, dup := out[prefix]; dup {
			out[collision] = "" // two keys flatten to the same dotted path
		}
		out[prefix] = stdStringify(v)
		return
	}
	for k, child := range m {
		p := k
		if !top {
			p = prefix + "." + k
		}
		stdFlatten(p, false, child, out)
	}
}

func stdStringify(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(x)
		return strings.TrimSpace(b.String())
	}
}

// sameValue compares rendered values: numbers by value (fastjson keeps the
// original text, the oracle rounds through float64), nested values as JSON.
func sameValue(a, b string) bool {
	if a == b {
		return true
	}
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	if ea == nil && eb == nil {
		return fa == fb || math.Abs(fa-fb) <= 1e-9*math.Abs(fb)
	}
	var ja, jb any
	if json.Unmarshal([]byte(a), &ja) == nil && json.Unmarshal([]byte(b), &jb) == nil {
		return stdStringify(ja) == stdStringify(jb)
	}
	return false
}

// hasDuplicateKeys reports an object repeating a key, where the oracle
// (last wins) and the decoder (first wins) legitimately differ.
func hasDuplicateKeys(line string) bool {
	dec := json.NewDecoder(strings.NewReader(line))
	type frame struct {
		obj  bool
		keys map[string]bool
		key  bool // the next string token is a key
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		top := func() *frame {
			if len(stack) == 0 {
				return nil
			}
			return stack[len(stack)-1]
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				if f := top(); f != nil && f.obj {
					f.key = true
				}
				stack = append(stack, &frame{obj: v == '{', keys: map[string]bool{}, key: v == '{'})
			default:
				stack = stack[:len(stack)-1]
				if f := top(); f != nil && f.obj {
					f.key = true
				}
			}
		default:
			f := top()
			if f == nil || !f.obj {
				continue
			}
			if f.key {
				s, _ := v.(string)
				if f.keys[s] {
					return true
				}
				f.keys[s] = true
				f.key = false
			} else {
				f.key = true
			}
		}
	}
}

func compareWithOracle(t *testing.T, d *JSONDecoder, line string) {
	t.Helper()
	if hasDuplicateKeys(line) {
		return
	}
	want, wantFields, ok := stdDecode(d, line)
	got := d.Decode(domain.RawLine{Text: line})
	if !ok {
		if got.Structured {
			// fastjson accepts a few inputs encoding/json rejects (for
			// example invalid UTF-8): harmless, the line is still shown.
			return
		}
		return
	}
	if !got.Structured {
		t.Fatalf("%q: not decoded", line)
	}
	for _, c := range []struct{ name, got, want string }{
		{"logger", got.Logger, want.Logger},
		{"thread", got.Thread, want.Thread},
		{"message", got.Message, want.Message},
		{"stack", got.Stack, want.Stack},
		{"trace", got.TraceID, want.TraceID},
		{"app", got.App, want.App},
		{"pid", got.PID, want.PID},
	} {
		if !sameValue(c.got, c.want) {
			t.Fatalf("%q: %s = %q, want %q", line, c.name, c.got, c.want)
		}
	}
	if got.Level != want.Level || !got.Time.Equal(want.Time) && !want.Time.IsZero() {
		t.Fatalf("%q: level/time %v %v, want %v %v", line, got.Level, got.Time, want.Level, want.Time)
	}
	if len(got.Fields) != len(wantFields) {
		t.Fatalf("%q: fields %v, want %v", line, got.Fields, wantFields)
	}
	for k, v := range wantFields {
		if !sameValue(got.Fields[k], v) {
			t.Fatalf("%q: field %s = %q, want %q", line, k, got.Fields[k], v)
		}
	}
}

var oracleLines = []string{
	`{"@timestamp":"2026-09-26T18:53:10.729Z","@version":"1","message":"Request processing failed","logger_name":"io.gimle.payment.service.PaymentService","thread_name":"http-nio-8080-exec-1","level":"ERROR","level_value":40000,"stack_trace":"java.lang.IllegalStateException: boom\n\tat x.Y.z(Y.java:1)","traceId":"bc9632dd","spanId":"0011","app":"payment-service","pid":"1","kubernetes":{"namespace_name":"app-rec","labels":{"app":"payment"}},"extra":{"orderId":"ord_1","amount":12.50,"tags":["a","b"],"ok":true,"none":null}}`,
	`{"time":1790431703123,"level":30,"msg":"hi","req":{"id":"r1","method":"POST"}}`,
	`{"log":{"level":"warn","logger":"a.B"},"message":"m","error":{"stack_trace":"s"},"trace":{"id":"t1"}}`,
	`{"log.level":"warn","log.logger":"a.B","message":"m","error.stack_trace":"s","trace.id":"t1"}`,
	`{"message":null,"msg":"fallback","level":null,"severity":"error"}`,
	`{"message":"unicode é ✓ é \"quoted\" <tag>","id":12345678901234567}`,
	`  {"message":"padded"}  `,
	`{"message":{"nested":"object as message"}}`,
	`{"a":{"b":{"c":{"d":1}}},"message":"deep"}`,
}

func TestFastjsonMatchesEncodingJSON(t *testing.T) {
	d := NewJSON(logstash)
	for _, line := range oracleLines {
		compareWithOracle(t, d, line)
	}
}

func TestLargeIntegersKeepTheirDigits(t *testing.T) {
	e := NewJSON(logstash).Decode(raw(`{"message":"m","orderId":1790431703123456789}`))
	if e.Fields["orderId"] != "1790431703123456789" {
		t.Fatalf("got %q: numbers must not be rounded through float64", e.Fields["orderId"])
	}
}

func FuzzFastjsonMatchesEncodingJSON(f *testing.F) {
	for _, l := range oracleLines {
		f.Add(l)
	}
	d := NewJSON(logstash)
	f.Fuzz(func(t *testing.T, line string) {
		compareWithOracle(t, d, line)
	})
}

func TestDuplicateKeysFirstWins(t *testing.T) {
	e := NewJSON(logstash).Decode(raw(`{"message":"first","message":"second","x":1,"x":{"y":2}}`))
	if e.Message != "first" || e.Fields["x"] != "1" || len(e.Fields) != 1 {
		t.Fatalf("got %q %v", e.Message, e.Fields)
	}
}

// hiddenOrUnder applies the decoder's rule: a hidden key hides its whole
// object (the former implementation leaked its children as fields).
func hiddenOrUnder(d *JSONDecoder, k string) bool {
	for i := range k {
		if k[i] == '.' && d.hidden(k[:i]) {
			return true
		}
	}
	return d.hidden(k)
}
