package logformat

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func BenchmarkTransformSize(b *testing.B) {
	limited := mdcPairs
	limited.MaxBytes, limited.MaxFields = 16<<10, 64 // the configuration's defaults
	for _, n := range []int{1 << 10, 64 << 10, 1 << 20} {
		for _, bc := range []struct {
			name string
			p    Profile
		}{{"none", logstash}, {"strip", withTransforms(mdc)}, {"pairs", withTransforms(mdcPairs)}, {"pairs-limited", withTransforms(limited)}} {
			msg := "route=/a method=GET - " + strings.Repeat("x", n) + " - user_id=u"
			line := raw(`{"message":"` + msg + `"}`)
			b.Run(fmt.Sprintf("%s/%dKiB", bc.name, n>>10), func(b *testing.B) {
				d := NewJSON(bc.p)
				b.SetBytes(int64(len(msg)))
				for b.Loop() {
					d.Decode(line)
				}
			})
		}
	}
}

func BenchmarkTransformManyPairs(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		var sb strings.Builder
		for i := range n {
			fmt.Fprintf(&sb, "k%d=v ", i)
		}
		line := raw(`{"message":"` + sb.String() + `- m - a=b"}`)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			d := NewJSON(withTransforms(mdcPairs))
			for b.Loop() {
				d.Decode(line)
			}
		})
	}
}

// TestTransformConcurrentDecode decodes from several goroutines with one
// decoder, as log sessions do; run with -race.
func TestTransformConcurrentDecode(t *testing.T) {
	p := withTransforms(mdcPairs)
	p.Hidden = []string{"request_id"}
	d := NewJSON(p)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				e := d.Decode(mdcLine)
				if e.Message != "Widget created" || e.Fields["correlation-id"] != "bc9632dd" {
					t.Errorf("got %q %v", e.Message, e.Fields)
					return
				}
				_ = e.HiddenFields()
			}
		})
	}
	wg.Wait()
}

// BenchmarkSearchTransformed is the cost of a text filter over a buffer of
// entries, each searched for the first time (the searchable text is built
// once per entry, with its extracted fields).
func BenchmarkSearchTransformed(b *testing.B) {
	for _, bc := range []struct {
		name string
		p    Profile
	}{{"none", logstash}, {"pairs", withTransforms(mdcPairs)}} {
		b.Run(bc.name, func(b *testing.B) {
			d := NewJSON(bc.p)
			f, _ := domain.ParseTextFilter("correlation-id=bc9632dd", false)
			for b.Loop() {
				b.StopTimer()
				es := make([]domain.LogEntry, 1000)
				for i := range es {
					es[i] = d.Decode(mdcLine)
				}
				b.StartTimer()
				for i := range es {
					f.Matches(&es[i])
				}
			}
		})
	}
}

// BenchmarkHiddenFieldsTransformed is what a layout column naming a hidden
// field costs per drawn row: the line's hidden fields are decoded again.
func BenchmarkHiddenFieldsTransformed(b *testing.B) {
	for _, bc := range []struct {
		name string
		p    Profile
	}{{"none", logstash}, {"pairs", withTransforms(mdcPairs)}} {
		b.Run(bc.name, func(b *testing.B) {
			bc.p.Hidden = append([]string{"service", "request_id"}, bc.p.Hidden...)
			e := NewJSON(bc.p).Decode(mdcLine)
			b.ReportAllocs()
			for b.Loop() {
				_ = e.HiddenFields()
			}
		})
	}
}

// BenchmarkJSONLevelFrom is the cost level_from adds to a JSON line.
func BenchmarkJSONLevelFrom(b *testing.B) {
	line := raw(`{"@timestamp":"2026-09-26T18:53:10.729Z","message":"request completed","level":"INFO","http":{"status":503},"traceId":"bc9632dd","app":"payment-service","pid":"1"}`)
	with := logstash
	with.LevelField, with.LevelRules = "http.status", []LevelRule{{"5*", domain.LevelError}, {"4*", domain.LevelWarn}, {"2*", domain.LevelInfo}}
	for _, bc := range []struct {
		name string
		p    Profile
	}{{"none", logstash}, {"level_from", with}} {
		b.Run(bc.name, func(b *testing.B) {
			d := NewJSON(bc.p)
			b.ReportAllocs()
			for b.Loop() {
				d.Decode(line)
			}
		})
	}
}
