package domain

import (
	"strings"
	"testing"
)

func TestRedactor(t *testing.T) {
	r, err := NewRedactor([]string{`(?i)bearer [a-z0-9._-]+`, `[\w.+-]+@[\w-]+\.[\w.]+`, `"password":"[^"]*"`})
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"Authorization: Bearer abc.def-123 ok": "Authorization: [redacted] ok",
		"user bob@example.com and a@b.io":      "user [redacted] and [redacted]",
		`{"user":"x","password":"s3cr$t"}`:     `{"user":"x",[redacted]}`,
		"nothing to hide":                      "nothing to hide",
		"bearer x then bearer y":               "[redacted] then [redacted]",
	} {
		if got := r.Redact(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if !r.Active() || (Redactor{}).Active() || (Redactor{}).Redact("bearer x") != "bearer x" {
		t.Error("the zero Redactor hides nothing")
	}
	if _, err := NewRedactor([]string{"("}); err == nil || !strings.Contains(err.Error(), `redact pattern "("`) {
		t.Errorf("invalid pattern: %v", err)
	}
}

func BenchmarkRedactNoMatch(b *testing.B) {
	r, _ := NewRedactor([]string{`(?i)bearer [a-z0-9._-]+`, `[\w.+-]+@[\w-]+\.[\w.]+`})
	line := "m8q7v 19:13:12.000 ERROR [exec-1] i.g.p.PaymentService : Payment authorization failed orderId=ord_8f91a2 traceId=7fd28c90"
	b.ReportAllocs()
	for b.Loop() {
		r.Redact(line)
	}
}
