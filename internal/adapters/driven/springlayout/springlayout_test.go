package springlayout

import (
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

var at = time.Date(2026, 9, 26, 17, 12, 40, 104e6, time.UTC)

var entry = domain.LogEntry{
	Time: at, Level: domain.LevelWarn, Structured: true, PID: "18472", App: "payment-service",
	Thread: "http-nio-8080-exec-7", Logger: "io.gimle.payment.gateway.GatewayClient", Message: "latency high p95=842ms",
}

func text(segs []ports.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

func TestCompact(t *testing.T) {
	paris := time.FixedZone("CEST", 2*3600)
	got := text(NewCompact().Render(entry, ports.RenderOptions{Location: paris}))
	want := "19:12:40.104  WARN [nio-8080-exec-7] i.g.p.gateway.GatewayClient    : latency high p95=842ms"
	if got != want {
		t.Fatalf("\ngot  %q\nwant %q", got, want)
	}
}

func TestFull(t *testing.T) {
	got := text(NewFull().Render(entry, ports.RenderOptions{Timestamps: ports.TimestampUTC}))
	want := "2026-09-26T17:12:40.104Z  WARN 18472 --- [payment-service] [nio-8080-exec-7] io.gimle.payment.gateway.GatewayClient   : latency high p95=842ms"
	if got != want {
		t.Fatalf("\ngot  %q\nwant %q", got, want)
	}
}

func TestTimestampModes(t *testing.T) {
	l := NewCompact()
	tests := map[ports.TimestampMode]string{
		ports.TimestampUTC:      "17:12:40.104Z",
		ports.TimestampRelative: "  -3m05s",
		ports.TimestampDelta:    "  +1 131ms",
		ports.TimestampNone:     "",
	}
	for mode, prefix := range tests {
		o := ports.RenderOptions{Timestamps: mode, Now: at.Add(3*time.Minute + 5*time.Second), DeltaFrom: at.Add(-1131 * time.Millisecond)}
		got := text(l.Render(entry, o))
		if mode == ports.TimestampNone {
			if !strings.HasPrefix(got, " WARN") {
				t.Errorf("none: %q", got)
			}
			continue
		}
		if !strings.HasPrefix(got, prefix+" ") {
			t.Errorf("%v: %q does not start with %q", mode, got, prefix)
		}
	}
}

func TestUnstructuredLineShowsRawMessage(t *testing.T) {
	e := domain.LogEntry{Time: at, Message: " :: Spring Boot ::  (v3.4.1)"}
	got := text(NewCompact().Render(e, ports.RenderOptions{Location: time.UTC}))
	if got != "17:12:40.104  :: Spring Boot ::  (v3.4.1)" {
		t.Fatalf("%q", got)
	}
}

func TestRoles(t *testing.T) {
	roles := map[ports.Role]string{}
	for _, s := range NewFull().Render(entry, ports.RenderOptions{}) {
		roles[s.Role] += s.Text
	}
	if roles[ports.RoleLevel] != " WARN" || roles[ports.RolePID] != "18472" || roles[ports.RoleMessage] != entry.Message {
		t.Fatalf("roles %v", roles)
	}
}

func TestAbbreviate(t *testing.T) {
	tests := map[string]string{
		"io.gimle.payment.gateway.GatewayClient": "i.g.p.gateway.GatewayClient",
		"com.zaxxer.hikari.pool.HikariPool":      "c.z.hikari.pool.HikariPool",
		"short.Name":                             "short.Name",
		"a.VeryVeryVeryVeryLongClassNameIndeed":  "eryVeryVeryLongClassNameIndeed",
	}
	for in, want := range tests {
		if got := Abbreviate(in, 30); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
