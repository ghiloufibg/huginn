package tui

import (
	"slices"
	"testing"
)

func TestTableLayoutFillColumn(t *testing.T) {
	tb := &table{cols: []column{
		{title: "REPO", width: 8, flex: true},
		{title: "PODS", width: 5},
		{title: "AGE", width: 4, drop: 2},
		{title: "WHY", width: 10, fill: true, drop: 1},
	}}
	sum := func(w []int) int {
		n, shown := 1, 0
		for _, x := range w {
			if x > 0 {
				n += x
				shown++
			}
		}
		return n + colGap*(shown-1)
	}
	cases := []struct {
		total, content int
		want           []int
	}{
		{total: 80, content: 12, want: []int{12, 5, 4, 80 - 1 - 12 - 5 - 4 - 3*colGap}}, // REPO capped, WHY fills
		{total: 30, content: 12, want: []int{12, 5, 4, 0}},                              // WHY dropped first
		{total: 17, content: 12}, // then AGE
	}
	for _, c := range cases {
		got := tb.layout(c.total, c.content)
		if c.total >= 30 && sum(got) != c.total && got[3] > 0 {
			t.Errorf("total %d: layout %v uses %d cells", c.total, got, sum(got))
		}
		if c.total == 17 {
			if got[2] != 0 || got[3] != 0 {
				t.Errorf("total 17: AGE and WHY should be dropped, got %v", got)
			}
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("total %d: layout %v, want %v", c.total, got, c.want)
		}
	}
}
