package bootstrap

import (
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
	"github.com/ghiloufibg/huginn/internal/diag"
)

// TestPerfHistoryLoad loads a 15 min window at 100 lines/s per container
// and reports time and memory. Run with HUGINN_PERF=1 (and HUGINN_PERF_DIR
// for profiles).
func TestPerfHistoryLoad(t *testing.T) {
	if os.Getenv("HUGINN_PERF") == "" {
		t.Skip("HUGINN_PERF not set")
	}
	c := demoConfig(t)
	clock := portstest.NewFakeClock(t0)
	cluster := demo.New(demo.Options{Seed: 42, Rate: 100, Clock: clock})
	lp, _ := logParts(c)
	s := newLogSessions(c, scopes(c, nil), cluster, clock, containerFilter(c), lp.decoders, diag.Discard())
	dir := os.Getenv("HUGINN_PERF_DIR")
	if dir != "" {
		f, _ := os.Create(dir + "/cpu.pprof")
		_ = pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	start := time.Now()
	ch, err := s.Open(t.Context(), ports.LogQuery{Env: "rec", Repo: "payment-service", Window: domain.TimeWindow{Since: 15 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	var peak uint64
	var ms runtime.MemStats
	var kept []domain.LogEntry
	for b := range ch {
		n += len(b.Entries)
		kept = append(kept, b.Entries...)
		runtime.ReadMemStats(&ms)
		peak = max(peak, ms.HeapInuse)
		if b.HistoryDone {
			break
		}
	}
	elapsed := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&ms)
	t.Logf("history: %d entries in %v; peak heap %d MB, retained %d MB (%d B/entry), total allocated %d MB",
		n, elapsed, peak>>20, ms.HeapAlloc>>20, int(ms.HeapAlloc)/max(len(kept), 1), ms.TotalAlloc>>20)
	runtime.KeepAlive(kept)
	if dir != "" {
		f, _ := os.Create(dir + "/heap.pprof")
		_ = pprof.Lookup("allocs").WriteTo(f, 0)
		g, _ := os.Create(dir + "/inuse.pprof")
		_ = pprof.WriteHeapProfile(g)
	}
}

// TestPerfTransform loads the same 15 min window of the demo repository
// whose messages carry an MDC context, read with the example transform
// (pairs), with a strip-only transform and without transform, and reports
// time and memory of each. Run with HUGINN_PERF=1.
func TestPerfTransform(t *testing.T) {
	if os.Getenv("HUGINN_PERF") == "" {
		t.Skip("HUGINN_PERF not set")
	}
	for _, variant := range []string{"none", "strip", "pairs", "none", "strip", "pairs"} {
		c := demoConfig(t)
		for i := range c.Formats {
			tr, ok := c.Formats[i].Transform["message"]
			switch {
			case !ok:
			case variant == "none":
				c.Formats[i].Transform = nil
			case variant == "strip":
				tr.Pattern = `^(?:[\w.-]+=\S*\s+)*-\s+(?P<message>.*?)\s+-\s+(?:[\w.-]+=\S*\s*)*$`
				tr.Pairs = nil
				c.Formats[i].Transform["message"] = tr
			}
		}
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		clock := portstest.NewFakeClock(t0)
		cluster := demo.New(demo.Options{Seed: 42, Rate: 100, Clock: clock})
		lp, _ := logParts(c)
		s := newLogSessions(c, scopes(c, nil), cluster, clock, containerFilter(c), lp.decoders, diag.Discard())
		start := time.Now()
		ch, err := s.Open(t.Context(), ports.LogQuery{Env: "rec", Repo: "order-orchestrator", Window: domain.TimeWindow{Since: 15 * time.Minute}})
		if err != nil {
			t.Fatal(err)
		}
		var kept []domain.LogEntry
		for b := range ch {
			kept = append(kept, b.Entries...)
			if b.HistoryDone {
				break
			}
		}
		elapsed := time.Since(start)
		var fields int
		for _, e := range kept {
			fields += len(e.Fields)
		}
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		t.Logf("%-5s %d entries in %v (%.1f µs/entry); retained %d B/entry, %.1f fields/entry; allocated %d MB",
			variant, len(kept), elapsed.Round(time.Millisecond), float64(elapsed.Microseconds())/float64(max(len(kept), 1)),
			int(ms.HeapAlloc-before.HeapAlloc)/max(len(kept), 1), float64(fields)/float64(max(len(kept), 1)), (ms.TotalAlloc-before.TotalAlloc)>>20)
		runtime.KeepAlive(kept)
	}
}
