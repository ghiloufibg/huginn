package logformat

import (
	"encoding/json"
	"regexp"
	"testing"
	"unicode/utf8"
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

func FuzzTransform(f *testing.F) {
	for _, s := range []string{"k=v - m - k=v", "", " - ", "k= -  - ", "\xff - \xfe - k="} {
		f.Add(s)
	}
	d := NewJSON(withTransforms(mdc))
	f.Fuzz(func(t *testing.T, message string) {
		q, _ := json.Marshal(message)
		e := d.Decode(raw(`{"message":` + string(q) + `}`))
		if !utf8.ValidString(e.Message) {
			t.Fatalf("invalid UTF-8 %q", e.Message)
		}
	})
}

// mdcLine is a line whose message carries a 13-key MDC context, mostly empty.
var mdcLine = raw(`{"time":"2026-09-26T18:53:10.729Z","severity":"INFO","logger":"com.example.widgets.WidgetService","thread":"http-nio-8080-exec-1","message":"route=/v1/widgets method=POST correlation-id=bc9632dd business_id= - Widget created - user_id= x-forwarded-for= request_id= http_status= result= status_code= error_code= activity_id= activity_name= process_instance_id=","service":"example-service-dev"}`)

func BenchmarkJSONTransform(b *testing.B) {
	for _, bc := range []struct {
		name string
		p    Profile
	}{{"none", logstash}, {"strip", withTransforms(mdc)}} {
		b.Run(bc.name, func(b *testing.B) {
			d := NewJSON(bc.p)
			b.ReportAllocs()
			for b.Loop() {
				d.Decode(mdcLine)
			}
		})
	}
}
