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
