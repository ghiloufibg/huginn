package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func lines(from, n int, at time.Time) []domain.LogEntry {
	out := make([]domain.LogEntry, n)
	for i := range out {
		out[i] = domain.LogEntry{Pod: "payment-service-1", Received: at.Add(time.Duration(from+i) * time.Millisecond), Message: fmt.Sprintf("line %d", from+i)}
	}
	return out
}

// E6: a pause longer than the buffer keeps the paused view and counts
// what could not be kept.
func TestPauseKeepsTheViewWhenTheBufferOverflows(t *testing.T) {
	m, l := openLogs(t)
	l.buf = domain.NewLogBuffer(100)
	l.rows = nil
	l.rebuild()
	at := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	feed(m, l, ports.LogBatch{Entries: lines(0, 50, at)})
	press(m, "space")
	before := render(m, 120, 20)
	for i := range 5 {
		feed(m, l, ports.LogBatch{Entries: lines(50+i*50, 50, at)})
	}
	out := render(m, 120, 20)
	if !strings.Contains(out, "line 49") || !strings.Contains(out, "PAUSED +250 (150 dropped)") {
		t.Fatalf("paused view changed or wrong count:\n%s\n---\n%s", before, out)
	}
	press(m, "space")
	if !strings.Contains(m.flashText, "150 lines dropped while paused") {
		t.Errorf("flash %q", m.flashText)
	}
	if out := render(m, 120, 20); !strings.Contains(out, "line 299") {
		t.Fatalf("resumed view:\n%s", out)
	}
}

// E5: late entries (recovered after an outage) are placed by time.
func TestLateEntriesArePlacedByTime(t *testing.T) {
	m, l := openLogs(t)
	at := time.Now().Add(time.Hour)
	feed(m, l, ports.LogBatch{Entries: []domain.LogEntry{
		{Pod: "payment-service-1", Received: at, Message: "a first"},
		{Pod: "payment-service-1", Received: at.Add(3 * time.Second), Message: "d last"},
	}})
	before := l.buf.Len()
	feed(m, l, ports.LogBatch{Late: []domain.LogEntry{
		{Pod: "payment-service-2", Received: at.Add(2 * time.Second), Message: "c recovered"},
		{Pod: "payment-service-2", Received: at.Add(time.Second), Message: "b recovered"},
	}})
	if l.buf.Len() != before || len(l.lateWaiting) != 2 || !l.lateTick {
		t.Fatalf("late entries are merged on the next late tick, not per batch (buffer %d)", l.buf.Len())
	}
	feed(m, l, ports.LogBatch{Late: []domain.LogEntry{{Pod: "payment-service-3", Received: at.Add(1500 * time.Millisecond), Message: "b2 recovered"}}})
	m.Update(lateTickMsg{screen: l, gen: l.gen})
	var got []string
	for i := l.buf.Len() - 5; i < l.buf.Len(); i++ {
		got = append(got, l.buf.At(i).Message)
	}
	if strings.Join(got, ",") != "a first,b recovered,b2 recovered,c recovered,d last" || l.lateTick || l.lateWaiting != nil {
		t.Fatalf("order %v", got)
	}
	if n := len(l.rows); n != l.buf.Len() {
		t.Errorf("rows %d, buffer %d", n, l.buf.Len())
	}
}

// E7: with two application containers in a pod, lines name their container.
func TestContainerShownWhenAPodHasSeveral(t *testing.T) {
	m, l := openLogs(t)
	pod := domain.Pod{Name: "multi-app-54c447db78-9d5px"}
	at := time.Now().Add(time.Hour)
	feed(m, l, ports.LogBatch{
		Pods: []ports.PodState{{Pod: pod, Containers: []string{"api", "worker"}}},
		Entries: []domain.LogEntry{
			{Pod: pod.Name, Container: "api", Received: at, Message: "api tick"},
			{Pod: pod.Name, Container: "worker", Received: at.Add(time.Second), Message: "worker tick"},
		},
	})
	out := render(m, 160, 30)
	if !strings.Contains(out, "9d5px/api    ") || !strings.Contains(out, "9d5px/worker ") {
		t.Fatalf("container not shown:\n%s", out)
	}
}

// E13: a repository removed while its logs are open is said so.
func TestRemovedRepositoryIsSaid(t *testing.T) {
	m, l := openLogs(t)
	s := mockupSnapshot("rec")
	s.Services = slices.DeleteFunc(s.Services, func(r domain.ServiceSummary) bool { return r.Repo == "payment-service" })
	snapshot(m, s)
	out := render(m, 200, 24)
	if !strings.Contains(out, "REMOVED") || !strings.Contains(out, "payment-service no longer exists in rec") {
		t.Fatalf("removed repository not said:\n%s", out)
	}
	_ = l
}

type accessLayout struct{ compactRenderer }

func (accessLayout) Columns() []ports.ColumnSpec { return testColumns[:1] } // time only

// E22: the columns offered are those of the layouts in view.
func TestColumnsOfTheLayoutsInView(t *testing.T) {
	m, l := openLogs(t)
	m.opts.Layouts = map[string]ports.LogLayout{"nginx": accessLayout{}}
	l.formats = map[string]bool{"nginx": true}
	if cols := l.viewColumns(m); len(cols) != 1 || cols[0].Name != "time" {
		t.Fatalf("nginx view offers %v", cols)
	}
	l.noteFormat("json") // falls back to the default layout: all its columns
	if cols := l.viewColumns(m); len(cols) != len(testColumns) {
		t.Fatalf("mixed view offers %d columns", len(cols))
	}
}
