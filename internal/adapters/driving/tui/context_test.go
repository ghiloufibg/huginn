package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Context rows selected as entries arrive are those a full selection of
// the buffer gives, whatever the batches, levels and evictions.
func TestContextRowsAppendedMatchAFullSelection(t *testing.T) {
	for _, tc := range []struct{ size, seed int }{{1, 1}, {1, 2}, {3, 1}, {3, 2}, {3, 3}, {5, 1}, {5, 2}} {
		size := tc.size
		t.Run(fmt.Sprint("context ", size, " seed ", tc.seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(uint64(size), uint64(tc.seed)))
			m, l := openLogs(t)
			l.buf = domain.NewLogBuffer(1000) // evictions happen
			f, err := domain.ParseTextFilter("match", false)
			if err != nil {
				t.Fatal(err)
			}
			l.filter.Texts, l.filter.Context = []domain.TextFilter{f}, size
			l.filter.Levels = domain.LevelSet{domain.LevelInfo: true, domain.LevelWarn: true, domain.LevelError: true}
			l.rebuild()
			n := 0
			for range 300 {
				var b ports.LogBatch
				for range r.IntN(20) {
					msg := "x"
					if r.IntN(8) == 0 {
						msg = "a match"
					}
					b.Entries = append(b.Entries, logEntry(n, podA, domain.Level(r.IntN(4)+1), "c.Logger", fmt.Sprint(msg, " ", n)))
					n++
				}
				feed(m, l, b)
				got := slices.Clone(l.rows)
				l.rebuild()
				if !slices.Equal(got, l.rows) {
					i := 0
					for i < min(len(got), len(l.rows)) && got[i] == l.rows[i] {
						i++
					}
					t.Fatalf("after %d entries (first seq %d), row %d of %d/%d differs:\nappended %v\nfull     %v", n, l.buf.FirstSeq(), i, len(got), len(l.rows), got[i:min(i+4, len(got))], l.rows[i:min(i+4, len(l.rows))])
				}
			}
		})
	}
}
