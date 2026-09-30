package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

func withFiles(m *Model) *portstest.FakeFileSink {
	fs := &portstest.FakeFileSink{}
	m.opts.Files = fs
	return fs
}

// onlyFile returns the one saved file.
func onlyFile(t *testing.T, fs *portstest.FakeFileSink) (string, string) {
	t.Helper()
	files := fs.Files()
	if len(files) != 1 {
		t.Fatalf("%d files saved", len(files))
	}
	for name, content := range files {
		return name, content
	}
	return "", ""
}

func TestSaveWholeView(t *testing.T) {
	m, l := openLogs(t)
	fs := withFiles(m)
	press(m, "ctrl+s")
	name, content := onlyFile(t, fs)
	if name != "payment-service-rec-20260926-191402.log" {
		t.Errorf("name %q", name)
	}
	lines := strings.Count(content, "\n") - strings.Count(content, "\n\tat ") - strings.Count(content, "\nio.") - strings.Count(content, "\nCaused")
	if lines != l.shown() || !strings.Contains(content, "Caused by: java.net.SocketTimeoutException") || strings.Contains(content, "\x1b") {
		t.Fatalf("every displayed line, as shown, whole stacks: %d lines\n%s", lines, content)
	}
	if out := render(m, 220, 10); !strings.Contains(out, "saved 9 lines") || !strings.Contains(out, "/saved/payment-service-rec-") {
		t.Errorf("the flash gives the count and the path:\n%s", out)
	}
}

func TestSaveSelectionRawAndRedacted(t *testing.T) {
	m, l := openLogs(t)
	fs := withFiles(m)
	withClipboards(m)
	r, err := domain.NewRedactor([]string{`orderId=\S+`})
	if err != nil {
		t.Fatal(err)
	}
	m.opts.Redactor = r
	cursorOn(t, l, "Payment authorization failed")
	press(m, "V", "j", "V", "Y", "ctrl+s")
	name, content := onlyFile(t, fs)
	want := `{"message":"Payment authorization failed [redacted] traceId=7fd28c90"}` + "\n" +
		`{"message":"request completed GET /v1/payments/pay_802ae status=200 duration=14ms"}` + "\n"
	if !strings.HasSuffix(name, ".raw.log") || content != want {
		t.Fatalf("the selection, raw after Y, redacted: %q\n%q", name, content)
	}
}

func TestCopyIsRedactedButNotTheScreen(t *testing.T) {
	m, l := openLogs(t)
	fc := withClipboards(m)
	m.opts.Redactor, _ = domain.NewRedactor([]string{`orderId=\S+`})
	cursorOn(t, l, "Card declined")
	press(m, "y")
	if got := lastCopy(t, fc); !strings.Contains(got, "Card declined by issuer [redacted] retryable=false") {
		t.Fatalf("copy: %q", got)
	}
	if !strings.Contains(render(m, 200, 20), "orderId=ord_72bf10") {
		t.Error("the screen shows the logs as they are")
	}
}

// TestSaveSnapshot: the lines are copied before the file is written away
// from the UI goroutine, whose buffer keeps changing.
func TestSaveSnapshot(t *testing.T) {
	m, l := openLogs(t)
	fs := withFiles(m)
	cmd := l.save(m)
	for i := range l.buf.Len() {
		l.buf.At(i).Message = "CHANGED"
	}
	run(m, cmd)
	if _, content := onlyFile(t, fs); strings.Contains(content, "CHANGED") || !strings.Contains(content, "Card declined") {
		t.Fatalf("the saved lines are those of the moment of ctrl+s:\n%s", content)
	}
}

func TestSaveFailures(t *testing.T) {
	m, _ := openLogs(t)
	press(m, "ctrl+s")
	if !strings.Contains(render(m, 200, 10), "saving is not available") {
		t.Error("no file sink")
	}
	m.opts.Files = &portstest.FakeFileSink{Err: errors.New("save directory /nope does not exist (ui.yaml save.dir)")}
	press(m, "ctrl+s")
	if out := render(m, 220, 10); !strings.Contains(out, "save failed: save directory /nope does not exist") {
		t.Errorf("the flash says why:\n%s", out)
	}
}

func TestSaveName(t *testing.T) {
	if got := saveName("team/payment service", "..rec", "20260930-194122", copyShown); got != "team_payment_service-rec-20260930-194122.log" {
		t.Errorf("shown: %q", got)
	}
	if got := saveName("", "prd", "x", copyRaw); got != "logs-prd-x.raw.log" {
		t.Errorf("raw: %q", got)
	}
}

// BenchmarkSave50000 saves a whole view of 50 000 lines: the snapshot on
// the UI goroutine (what the user waits for), then the writing.
func BenchmarkSave50000(b *testing.B) {
	m, l := bigLogs(b, 50000)
	m.opts.Files = discardSink{}
	b.Run("snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = l.save(m)
		}
	})
	b.Run("write", func(b *testing.B) {
		cmd := l.save(m)
		b.ReportAllocs()
		for b.Loop() {
			_ = cmd()
		}
	})
}

type discardSink struct{}

func (discardSink) Save(_ context.Context, _ string, write func(io.Writer) error) (string, error) {
	return "/dev/null", write(io.Discard)
}
