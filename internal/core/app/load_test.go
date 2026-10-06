package app

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// wallClock is the real clock: the reorder window then holds what a real
// stream would hold during 250 ms.
type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) NewTicker(d time.Duration) ports.Ticker { return wallTicker{time.NewTicker(d)} }

type wallTicker struct{ t *time.Ticker }

func (w wallTicker) C() <-chan time.Time { return w.t.C }
func (w wallTicker) Stop()               { w.t.Stop() }

// generator is a log source that produces lines as fast as they are taken:
// each followed container sends its share from its own goroutine, stamped
// with the time it is sent, without any lock shared between containers.
type generator struct {
	perContainer int
	sent         atomic.Int64
}

type genStream struct{ ch chan domain.RawLine }

func (s genStream) Lines() <-chan domain.RawLine { return s.ch }
func (genStream) Err() error                     { return nil }

func (g *generator) Stream(ctx context.Context, req ports.LogRequest) (ports.LogStream, error) {
	st := genStream{ch: make(chan domain.RawLine, 4096)}
	if !req.Follow {
		close(st.ch) // no history
		return st, nil
	}
	go func() {
		text := fmt.Sprintf("request completed POST /v1/payments status=201 pod=%s", req.Pod)
		for range g.perContainer {
			select {
			case st.ch <- domain.RawLine{Time: time.Now(), Pod: req.Pod, Container: req.Container, Text: text}:
				g.sent.Add(1)
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
		close(st.ch)
	}()
	return st, nil
}

// BenchmarkSessionLoad streams b.N live lines from 50 pods, produced as fast
// as the session takes them, through a session with a 50 000-line buffer,
// and reads every batch: the cost per line of the whole live path under a
// load no source limits (decoding aside). The reorder window fills (250 ms
// of lines at the rate reached) and the buffer bound applies.
func BenchmarkSessionLoad(b *testing.B) {
	const pods = 50
	fc := portstest.NewFakeCluster()
	fc.AddWorkload(deployment("ns", "api", "shop", pods, pods))
	for i := range pods {
		fc.PutPod(podWithSidecar(fmt.Sprintf("api-%02d", i), time.Now().Add(-time.Hour)))
	}
	gen := &generator{perContainer: b.N/pods + 1}
	s := &LogSessions{
		Cluster: fc, Logs: gen, Clock: wallClock{}, Decoders: ports.OneDecoder{LogDecoder: passthrough{}},
		Resolver: LabelResolver{Keys: []string{"app.kubernetes.io/part-of"}}, Scopes: scopes("ns"),
		Filter: domain.ContainerFilter{Deny: []string{"istio-proxy"}}, MaxHistory: 50000,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.ReportAllocs()
	b.ResetTimer()
	ch, err := s.Open(ctx, ports.LogQuery{Env: "rec", Repo: "shop", Window: domain.TimeWindow{Tail: 1}, Follow: true})
	if err != nil {
		b.Fatal(err)
	}
	var peak atomic.Uint64 // heap in use, sampled
	done := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
			}
		}
	}()
	defer close(done)
	want := int64(pods * gen.perContainer)
	var got int64
	for batch := range ch {
		got += int64(len(batch.Entries)+len(batch.Late)) + int64(batch.Skipped)
		if got >= want {
			break
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(want)/b.Elapsed().Seconds(), "lines/s")
	b.ReportMetric(float64(peak.Load())/(1<<20), "peak-heap-MB")
}
