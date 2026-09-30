package logformat

import (
	"fmt"
	"sync"
	"testing"
)

func TestHiddenCache(t *testing.T) {
	c := newHiddenCache([]string{"kubernetes.*", "host*"})
	for range 2 { // computed, then remembered
		if h := c.get("kubernetes.pod_name"); !h.self {
			t.Error("kubernetes.pod_name is hidden")
		}
		if h := c.get("kubernetes"); h.self || !h.below {
			t.Errorf("kubernetes: not hidden itself, every key below is: %+v", h)
		}
		if h := c.get("message"); h.self || h.below {
			t.Errorf("message: %+v", h)
		}
	}
	if len(c.m) != 3 {
		t.Errorf("remembered %d keys", len(c.m))
	}
	if h := newHiddenCache(nil).get("anything"); h.self || h.below {
		t.Error("no globs, nothing hidden")
	}
}

func TestHiddenCacheIsBounded(t *testing.T) {
	c := newHiddenCache([]string{"x*"})
	for i := range hiddenCacheMax + 100 {
		c.get(fmt.Sprint("k", i))
	}
	if len(c.m) != hiddenCacheMax {
		t.Fatalf("%d keys remembered, want at most %d", len(c.m), hiddenCacheMax)
	}
	if h := c.get("x-new"); !h.self {
		t.Error("past the bound, decisions are still right")
	}
}

// TestHiddenCacheConcurrent: one decoder serves every container of a
// session; run with -race.
func TestHiddenCacheConcurrent(t *testing.T) {
	d := NewJSON(logstash)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 500 {
				e := d.Decode(raw(fmt.Sprintf(`{"message":"m","k%d_%d":1,"kubernetes":{"pod_name":"p"}}`, g, i%50)))
				if len(e.Fields) != 1 || e.LoadHidden == nil {
					t.Errorf("fields %v", e.Fields)
					return
				}
			}
		})
	}
	wg.Wait()
}

// BenchmarkJSONDecodeParallel decodes from every CPU at once, as the
// containers of a session do, to see the cache's lock under contention.
func BenchmarkJSONDecodeParallel(b *testing.B) {
	d := NewJSON(logstash)
	line := raw(`{"@timestamp":"2026-09-26T18:53:10.729Z","@version":"1","message":"request completed","logger_name":"a.B","thread_name":"t","level":"INFO","level_value":20000,"traceId":"bc9632dd","app":"payment-service","pid":"1","kubernetes":{"namespace_name":"app-rec","pod_name":"p","labels":{"app":"x"}}}`)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			d.Decode(line)
		}
	})
}
