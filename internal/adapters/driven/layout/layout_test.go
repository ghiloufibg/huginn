package layout

import (
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// spring mirrors examples/config/layouts/spring.yaml: the Spring Boot
// console layout must render exactly as the former Go implementation did.
func spring(t *testing.T) *Layout {
	t.Helper()
	thread := "[{thread|last:15|right:15}]"
	l, errs := New(Spec{
		Stream: LineSpec{
			TimeFormat: "15:04:05.000",
			Columns: []ColumnSpec{
				{Name: "time", Key: "t", Show: "{time}", Role: ports.RoleTimestamp, Visible: true},
				{Name: "level", Key: "l", Show: "{level|right:5}", Role: ports.RoleLevel, Visible: true},
				{Name: "thread", Key: "h", Show: thread, Role: ports.RoleThread, HideBelow: 140, Visible: true},
				{Name: "class", Key: "c", Show: "{logger|abbrev:30|left:30}", Role: ports.RoleLogger, HideBelow: 110, Visible: true},
			},
			Separator: ": ", SeparatorAfter: []string{"thread", "class"},
		},
		Zoom: LineSpec{
			TimeFormat: "2006-01-02T15:04:05.000Z07:00",
			Columns: []ColumnSpec{
				{Name: "time", Show: "{time}", Role: ports.RoleTimestamp},
				{Name: "level", Show: "{level|right:5}", Role: ports.RoleLevel},
				{Name: "pid", Show: "{pid}", Role: ports.RolePID},
				{Name: "dashes", Show: "---", Role: ports.RoleDim},
				{Name: "app", Show: "[{app}]", Role: ports.RoleDim},
				{Name: "thread", Show: thread, Role: ports.RoleThread},
				{Name: "class", Show: "{logger|abbrev:40|left:40}", Role: ports.RoleLogger},
			},
			Separator: ": ", SeparatorAfter: []string{"thread", "class"},
		},
		FrameworkPrefixes: []string{"java.", "org.springframework."},
	})
	if errs != nil {
		t.Fatal(errs)
	}
	return l
}

var at = time.Date(2026, 9, 26, 17, 12, 40, 104e6, time.UTC)

var entry = domain.LogEntry{
	Time: at, Level: domain.LevelWarn, Structured: true, PID: "18472", App: "payment-service",
	Thread: "http-nio-8080-exec-7", Logger: "io.gimle.payment.gateway.GatewayClient", Message: "latency high p95=842ms",
	Fields: map[string]string{"http.status": "503"},
}

func text(segs []ports.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

func TestSpringCompact(t *testing.T) {
	paris := time.FixedZone("CEST", 2*3600)
	got := text(spring(t).Render(entry, ports.RenderOptions{Location: paris}))
	want := "19:12:40.104  WARN [nio-8080-exec-7] i.g.p.gateway.GatewayClient    : latency high p95=842ms"
	if got != want {
		t.Fatalf("\ngot  %q\nwant %q", got, want)
	}
}

func TestSpringFull(t *testing.T) {
	got := text(spring(t).Render(entry, ports.RenderOptions{Timestamps: ports.TimestampUTC, Full: true}))
	want := "2026-09-26T17:12:40.104Z  WARN 18472 --- [payment-service] [nio-8080-exec-7] io.gimle.payment.gateway.GatewayClient   : latency high p95=842ms"
	if got != want {
		t.Fatalf("\ngot  %q\nwant %q", got, want)
	}
	e := entry
	e.App = ""
	if got := text(spring(t).Render(e, ports.RenderOptions{Timestamps: ports.TimestampUTC, Full: true})); strings.Contains(got, "[]") {
		t.Fatalf("a column whose fields are empty is left out: %q", got)
	}
}

func TestTimestampModes(t *testing.T) {
	l := spring(t)
	tests := map[ports.TimestampMode]string{
		ports.TimestampUTC:      "17:12:40.104Z",
		ports.TimestampRelative: "  -3m05s",
		ports.TimestampDelta:    "  +1 131ms",
	}
	for mode, prefix := range tests {
		o := ports.RenderOptions{Timestamps: mode, Now: at.Add(3*time.Minute + 5*time.Second), DeltaFrom: at.Add(-1131 * time.Millisecond)}
		if got := text(l.Render(entry, o)); !strings.HasPrefix(got, prefix+" ") {
			t.Errorf("%v: %q does not start with %q", mode, got, prefix)
		}
	}
	if got := text(l.Render(entry, ports.RenderOptions{Timestamps: ports.TimestampNone})); !strings.HasPrefix(got, " WARN") {
		t.Errorf("none: %q", got)
	}
}

func TestUndecodedLineKeepsTimeAndMessage(t *testing.T) {
	e := domain.LogEntry{Time: at, Message: " :: Spring Boot ::  (v3.4.1)"}
	if got := text(spring(t).Render(e, ports.RenderOptions{Location: time.UTC})); got != "17:12:40.104  :: Spring Boot ::  (v3.4.1)" {
		t.Fatalf("%q", got)
	}
}

func TestRoles(t *testing.T) {
	roles := map[ports.Role]string{}
	for _, s := range spring(t).Render(entry, ports.RenderOptions{Full: true}) {
		roles[s.Role] += s.Text
	}
	if roles[ports.RoleLevel] != " WARN" || roles[ports.RolePID] != "18472" || roles[ports.RoleMessage] != entry.Message {
		t.Fatalf("roles %v", roles)
	}
}

func TestHiddenColumns(t *testing.T) {
	l := spring(t)
	tests := []struct {
		hide []string
		want string
	}{
		{nil, "17:12:40.104  WARN [nio-8080-exec-7] i.g.p.gateway.GatewayClient    : latency high p95=842ms"},
		{[]string{"thread"}, "17:12:40.104  WARN i.g.p.gateway.GatewayClient    : latency high p95=842ms"},
		{[]string{"class"}, "17:12:40.104  WARN [nio-8080-exec-7] : latency high p95=842ms"},
		{[]string{"thread", "class"}, "17:12:40.104  WARN latency high p95=842ms"},
		{[]string{"time", "level"}, "[nio-8080-exec-7] i.g.p.gateway.GatewayClient    : latency high p95=842ms"},
	}
	for _, tc := range tests {
		o := ports.RenderOptions{Location: time.UTC, Hide: ports.ColumnSet{}.With(tc.hide...)}
		if got := text(l.Render(entry, o)); got != tc.want {
			t.Errorf("hide %v:\ngot  %q\nwant %q", tc.hide, got, tc.want)
		}
	}
}

func TestTemplateFeatures(t *testing.T) {
	l, errs := New(Spec{Stream: LineSpec{TimeFormat: "15:04", Columns: []ColumnSpec{
		{Name: "status", Show: "{field:http.status|default:---}"},
		{Name: "trace", Show: "{trace_id|default:-|upper|first:4}"},
		{Name: "braces", Show: "{{{level|lower}}}"},
	}}})
	if errs != nil {
		t.Fatal(errs)
	}
	e := entry
	e.TraceID = "abcdef"
	if got := text(l.Render(e, ports.RenderOptions{})); got != "503 ABCD {warn} latency high p95=842ms" {
		t.Fatalf("%q", got)
	}
	if cols := l.Columns(); len(cols) != 3 || cols[0].Name != "status" {
		t.Fatalf("columns %+v", cols)
	}
}

func TestTemplateErrors(t *testing.T) {
	for show, want := range map[string]string{
		"{thred}":         `unknown field "thred"`,
		"{thread|pad:3}":  `unknown filter "pad"`,
		"{thread|right}":  "right needs a width",
		"{field}":         "needs a path",
		"{time:x}":        "takes no path",
		"[{thread}":       "",
		"{thread":         "missing }",
		"thread}":         "unexpected }",
		"{level|upper:3}": "takes no argument",
		"{level|default}": "needs a value",
	} {
		_, errs := New(Spec{Stream: LineSpec{Columns: []ColumnSpec{{Name: "x", Show: show}}}})
		switch {
		case want == "" && errs != nil:
			t.Errorf("%q: unexpected %v", show, errs)
		case want != "" && (len(errs) != 1 || errs[0].Path != "stream.columns[0].show" || !strings.Contains(errs[0].Msg, want)):
			t.Errorf("%q: got %v, want %q", show, errs, want)
		}
	}
}

func TestFrameworkFrame(t *testing.T) {
	l := spring(t)
	for frame, want := range map[string]bool{
		"java.base/java.util.Arrays.copyOf(Arrays.java:3541)":                true,
		"org.springframework.web.servlet.FrameworkServlet.service(F.java:1)": true,
		"io.gimle.payment.PaymentController.pay(PaymentController.java:42)":  false,
	} {
		if got := l.FrameworkFrame(frame); got != want {
			t.Errorf("%s: %v", frame, got)
		}
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
