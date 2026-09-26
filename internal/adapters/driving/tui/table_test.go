package tui

import (
	"slices"
	"strings"
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

func TestStatusGroupsOnlyWhenTheyFit(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	has := func(out string) bool {
		return strings.Contains(out, " FAILING 3") && strings.Contains(out, " HEALTHY 7")
	}
	if out := render(m, 80, 24); !has(out) || !strings.Contains(out, "legacy-cron") {
		t.Fatalf("groups expected when every row fits:\n%s", out)
	}
	if out := render(m, 80, 20); has(out) {
		t.Fatalf("groups must never cost a row:\n%s", out)
	}
	if out := render(m, 160, 30); has(out) || !strings.Contains(out, " ─ catalog-indexer ─") {
		t.Fatalf("groups must not hide the preview:\n%s", out)
	}
	if out := render(m, 160, 50); !has(out) || !strings.Contains(out, " ─ catalog-indexer ─") {
		t.Fatalf("groups and preview expected on a tall terminal:\n%s", out)
	}
	press(m, "s") // sort by name
	if out := render(m, 80, 24); has(out) {
		t.Fatalf("groups only when sorted by status:\n%s", out)
	}
}
