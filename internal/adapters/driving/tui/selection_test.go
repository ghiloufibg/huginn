package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// withClipboards gives m both clipboards, as ui.yaml clipboard: auto.
func withClipboards(m *Model) *portstest.FakeClipboard {
	fc := &portstest.FakeClipboard{}
	m.opts.Clipboard, m.opts.ClipboardOSC52, m.opts.CopyMaxBytes = fc, true, 1<<20
	osc52Sent = nil
	return fc
}

// cursorOn puts the cursor on the entry whose message starts with prefix.
func cursorOn(t *testing.T, l *logsScreen, prefix string) {
	t.Helper()
	for i := range l.shown() {
		if e, _ := l.entryAt(i); strings.HasPrefix(e.Message, prefix) {
			l.cursor, l.tail = i, false
			return
		}
	}
	t.Fatalf("no entry %q", prefix)
}

func lastCopy(t *testing.T, fc *portstest.FakeClipboard) string {
	t.Helper()
	texts := fc.Texts()
	if len(texts) == 0 {
		t.Fatal("nothing copied")
	}
	if len(osc52Sent) == 0 {
		t.Error("the terminal (OSC 52) got nothing")
	}
	return texts[len(texts)-1]
}

func TestCopyCursorLine(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	cursorOn(t, l, "Payment authorization failed")
	press(m, "y")
	got := lastCopy(t, fc)
	want := "m8q7v 19:13:12.000 ERROR [exec-1] i.g.p.PaymentService : Payment authorization failed orderId=ord_8f91a2 traceId=7fd28c90\n" +
		"io.gimle.payment.PaymentGatewayException: upstream request timed out\n\tat io.gimle.payment.gateway.GatewayClient.charge(GatewayClient.java:184)\n" +
		"\tat org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:885)\nCaused by: java.net.SocketTimeoutException: Read timed out\n"
	if got != want {
		t.Fatalf("as shown, with the whole stack:\n%q\nwant\n%q", got, want)
	}
	if !strings.Contains(render(m, 200, 10), "copied 1 line") {
		t.Error("the flash says what was copied")
	}
	press(m, "Y")
	if got := lastCopy(t, fc); got != `{"message":"Payment authorization failed orderId=ord_8f91a2 traceId=7fd28c90"}`+"\n" {
		t.Errorf("raw: %q", got)
	}
}

func TestSelectRangeAndMarks(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	cursorOn(t, l, "Card declined")
	press(m, "V", "k", "k", "V")
	cursorOn(t, l, "request completed POST")
	press(m, "m")
	golden(t, "logs_select_140x14", render(m, 140, 14))
	out := render(m, 220, 14)
	for _, want := range []string{"SELECT", "selected 3 lines + 1 marked"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	press(m, "y")
	var msgs []string
	for _, line := range strings.Split(strings.TrimSpace(lastCopy(t, fc)), "\n") {
		msgs = append(msgs, strings.TrimSpace(line[strings.Index(line, " ")+1:][13:]))
	}
	want := []string{
		"INFO [exec-1] i.g.p.PaymentController : request completed POST /v1/payments status=201 duration=96ms",
		":: Spring Boot ::                (v3.4.1)",
		"INFO [exec-1] i.g.p.PaymentApplication : Started PaymentApplication in 7.412 seconds",
		"ERROR [exec-1] i.g.p.card.CardController : Card declined by issuer orderId=ord_72bf10 retryable=false",
	}
	if got := strings.Join(msgs, " | "); got != strings.Join(want, " | ") {
		t.Fatalf("the mark and the range, in display order:\n%s", got)
	}
	press(m, "esc")
	if l.sel.active() || m.top() != l {
		t.Fatal("esc clears the selection first, and stays on the logs")
	}
}

func TestSelectionFollowsFilters(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	cursorOn(t, l, "request completed POST")
	press(m, "V")
	cursorOn(t, l, "Card declined")
	press(m, "V", "e") // errors only: the lines between are hidden
	press(m, "y")
	got := lastCopy(t, fc)
	if strings.Contains(got, "request completed") || !strings.Contains(got, "Card declined") || !strings.Contains(got, "Payment authorization failed") {
		t.Fatalf("only displayed lines are copied:\n%s", got)
	}
}

func TestCopyLimitsAndModes(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	m.opts.CopyMaxBytes = 10
	press(m, "y")
	if len(fc.Texts()) != 0 || !strings.Contains(render(m, 200, 10), "selection too large") {
		t.Fatal("past copy.max_bytes nothing is copied, and the flash says so")
	}
	m.opts.CopyMaxBytes = 1 << 20
	m.opts.Clipboard, m.opts.ClipboardOSC52 = nil, false
	press(m, "y")
	if !strings.Contains(render(m, 200, 10), "copying is off") {
		t.Error("clipboard: off")
	}
	m.opts.Clipboard, m.opts.ClipboardOSC52 = &portstest.FakeClipboard{Err: errors.New("no clipboard command found")}, true
	press(m, "y")
	if out := render(m, 220, 10); !strings.Contains(out, "via the terminal (OSC 52)") {
		t.Errorf("a failing system clipboard falls back on the terminal:\n%s", out)
	}
	m.opts.ClipboardOSC52 = false
	press(m, "y")
	if out := render(m, 220, 10); !strings.Contains(out, "copy failed: no clipboard command found") {
		t.Errorf("system only, failing:\n%s", out)
	}
	_ = l
}

func TestCopyFromZoom(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	cursorOn(t, l, "Payment authorization failed")
	press(m, "enter", "Y")
	if got := lastCopy(t, fc); !strings.HasPrefix(got, `{"message":"Payment authorization failed`) || m.top() == l {
		t.Fatalf("Y in zoom copies the zoomed entry raw and stays in zoom: %q", got)
	}
}

func TestPruneSelection(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	l := newLogsScreen(m, "r")
	l.buf = domain.NewLogBuffer(3)
	for i := range 5 {
		l.buf.Append(logEntry(i, podA, domain.LevelInfo, "a.B", fmt.Sprint("line ", i)))
	}
	l.sel = selection{anchor: 2, end: 4, marks: map[uint64]bool{1: true, 5: true}}
	l.pruneSelection()
	if l.sel.anchor != 0 || len(l.sel.marks) != 1 || !l.sel.marks[5] {
		t.Fatalf("evicted entries leave the selection: %+v", l.sel)
	}
}

func TestCleanCopy(t *testing.T) {
	for in, want := range map[string]string{
		"plain\ttext\n":           "plain\ttext\n",
		"a\x1b]52;c;evil\x07b":    "a]52;c;evilb",
		"red \x1b[31mx\x1b[0m":    "red [31mx[0m",
		"c1 \u009b31m é \xff end": "c1 31m é \ufffd end",
		"del\x7f":                 "del",
	} {
		if got := cleanCopy(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// BenchmarkLogsFrameSelecting draws a frame with a range over the screen
// and 1 000 marked lines: the gutter must stay within the frame budget.
func BenchmarkLogsFrameSelecting(b *testing.B) {
	m, l := bigLogs(b, 50000)
	l.sel.marks = map[uint64]bool{}
	for i := range 1000 {
		if e, ok := l.entryAt(i * 50); ok {
			l.sel.marks[e.Seq] = true
		}
	}
	if e, ok := l.entryAt(l.shown() - 30); ok {
		l.sel.anchor, l.sel.extending = e.Seq, true
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkCopy10000 builds the text of a 10 000-line copy, as shown.
func BenchmarkCopy10000(b *testing.B) {
	m, l := bigLogs(b, 50000)
	m.opts.CopyMaxBytes, m.opts.ClipboardOSC52 = 64<<20, true
	if e, ok := l.entryAt(l.shown() - 10000); ok {
		l.sel.anchor, l.sel.extending = e.Seq, true
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = l.copyLines(m, l.selected(), copyShown)
	}
}
