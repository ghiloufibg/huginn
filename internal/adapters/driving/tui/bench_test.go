package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// bigLogs opens the logs screen with n entries from three pods.
func bigLogs(b *testing.B, n int) (*Model, *logsScreen) {
	b.Helper()
	m, l := openLogs(b)
	pods := []string{podA, podB, podC}
	batch := ports.LogBatch{}
	for i := range n {
		e := logEntry(i/10, pods[i%3], domain.Level(i%4+1), "io.gimle.payment.gateway.GatewayClient", fmt.Sprintf("request %d completed POST /v1/payments status=201 duration=%dms", i, i%500))
		if i%97 == 0 {
			e.Stack = "java.lang.IllegalStateException: boom\n\tat x.Y.z(Y.java:1)"
		}
		batch.Entries = append(batch.Entries, e)
	}
	feed(m, l, batch)
	render(m, 200, 60)
	return m, l
}

// BenchmarkLogsFrameTail is one frame of the logs screen following the tail.
func BenchmarkLogsFrameTail(b *testing.B) {
	m, _ := bigLogs(b, 50000)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkLogsFrameFarJump draws a frame after the cursor jumped far from
// the viewport (a match or an error found by n or >).
func BenchmarkLogsFrameFarJump(b *testing.B) {
	m, l := bigLogs(b, 50000)
	b.ReportAllocs()
	for b.Loop() {
		l.tail, l.offset, l.cursor = false, 0, 40000
		_ = m.View()
	}
}

// BenchmarkLogsIngest30Hz feeds one 33 ms batch at 10 000 lines/s and draws
// the frame, as a live stream does 30 times a second.
func BenchmarkLogsIngest30Hz(b *testing.B) {
	m, l := bigLogs(b, 50000)
	batch := ports.LogBatch{}
	for i := range 330 {
		batch.Entries = append(batch.Entries, logEntry(i, podA, domain.LevelInfo, "i.g.p.PaymentController", "live line"))
	}
	b.ReportAllocs()
	for b.Loop() {
		l.apply(batch, t0)
		_ = m.View()
	}
}

// BenchmarkServicesFrame500 draws the services screen with 500 repositories.
func BenchmarkServicesFrame500(b *testing.B) {
	m, _ := newTestModel(b, 1, "")
	s := mockupSnapshot("rec")
	base := s.Services
	s.Services = nil
	for i := range 500 {
		r := base[i%len(base)]
		r.Repo = fmt.Sprintf("%s-%03d", r.Repo, i)
		s.Services = append(s.Services, r)
	}
	snapshot(m, s)
	render(m, 200, 60)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkLogsIngestWithContext is BenchmarkLogsIngest30Hz with a text
// filter and 3 context lines: context depends on neighbours.
func BenchmarkLogsIngestWithContext(b *testing.B) {
	m, l := bigLogs(b, 50000)
	press(m, "/", "4", "2", "enter", "X", "X")
	batch := ports.LogBatch{}
	for i := range 330 {
		batch.Entries = append(batch.Entries, logEntry(i, podA, domain.LevelInfo, "i.g.p.PaymentController", fmt.Sprintf("live line %d", i)))
	}
	b.ReportAllocs()
	for b.Loop() {
		l.apply(batch, t0)
		_ = m.View()
	}
}

// TestFarJumpStaysFast guards against the viewport scanning the whole
// buffer: a jump of 40 000 entries used to hang the UI.
func TestFarJumpStaysFast(t *testing.T) {
	m, l := openLogs(t)
	var batch ports.LogBatch
	for i := range 50000 {
		batch.Entries = append(batch.Entries, logEntry(i, podA, domain.LevelInfo, "a.B", "line"))
	}
	feed(m, l, batch)
	render(m, 200, 60)
	start := time.Now()
	l.tail, l.offset, l.cursor = false, 0, 40000
	render(m, 200, 60)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a far jump took %v", d)
	}
	if l.offset < 40000-60 {
		t.Fatalf("the cursor is not visible: offset %d", l.offset)
	}
}

// bigKafka opens a topic's records screen with n records.
func bigKafka(b *testing.B, n int) (*Model, *kafkaRecordsScreen) {
	b.Helper()
	m, _ := newKafkaModel(b)
	m.opts.KafkaMaxRecords, m.opts.KafkaMaxBytes = n, 64<<20
	selectRepo(b, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(n), HistoryDone: true})
	render(m, 200, 60)
	return m, r
}

// BenchmarkKafkaRecordsFrame is one frame of a topic holding 20 000 records.
func BenchmarkKafkaRecordsFrame(b *testing.B) {
	m, _ := bigKafka(b, 20000)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkKafkaFilter applies a filter to 20 000 records (each keystroke).
func BenchmarkKafkaFilter(b *testing.B) {
	_, r := bigKafka(b, 20000)
	b.ReportAllocs()
	for b.Loop() {
		r.input.text = []rune("pay-0001")
		r.setFilter()
	}
}
