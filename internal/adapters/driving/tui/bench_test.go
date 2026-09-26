package tui

import (
	"fmt"
	"testing"

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
		l.apply(batch)
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
		l.apply(batch)
		_ = m.View()
	}
}
