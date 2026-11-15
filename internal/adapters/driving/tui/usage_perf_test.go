package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// bigUsageSnapshot is n services spread over ns namespaces, three pods each.
// tick moves the values, so that a memory sort reorders the rows from one
// snapshot to the next.
func bigUsageSnapshot(n, ns, tick int) ports.UsageSnapshot {
	snap := ports.UsageSnapshot{Env: "rec", Synced: true, SampledAt: t0, Window: time.Minute}
	for k := range ns {
		nu := domain.NamespaceUsage{Name: fmt.Sprintf("ns-%02d", k), History: rampHistory()}
		for i := range n / ns {
			j := k*(n/ns) + i
			mem := int64((j*7919+tick*104729)%900+100) * mi
			r := withPeak(rollup(3, 3, int64(100+j%900), 1000, 2000, mem, 1*gi, 2*gi), fmt.Sprintf("svc-%04d-aaaa-x1", j), float64(j%100))
			pods := []domain.PodDetail{}
			for p := range 3 {
				pods = append(pods, domain.PodDetail{
					Pod:    domain.Pod{Name: fmt.Sprintf("svc-%04d-aaaa-p%d", j, p)},
					Rollup: rollup(1, 1, 100, 300, 600, mem/3, 300*mi, 700*mi),
					Containers: []domain.ContainerDetail{
						{Name: "app", Measured: true, Use: domain.Amount{CPUMilli: 90, MemBytes: mem / 3}, Request: domain.Amount{CPUMilli: 300, MemBytes: 300 * mi}, Limit: domain.Amount{CPUMilli: 600, MemBytes: 700 * mi}},
					},
				})
			}
			nu.Services = append(nu.Services, domain.ServiceUsage{
				Repo: fmt.Sprintf("svc-%04d", j), Rollup: r, History: rampHistory(), PodRows: pods,
			})
			nu.Total.Pods += 3
			nu.Total.Reporting += 3
			nu.Total.Use.MemBytes += mem
		}
		snap.Namespaces = append(snap.Namespaces, nu)
	}
	return snap
}

func bigUsage(b testing.TB, n, ns int) (*Model, *usageScreen) {
	b.Helper()
	m, s, _ := openUsage(b, "")
	feedUsage(m, s, bigUsageSnapshot(n, ns, 0))
	render(m, 200, 60)
	return m, s
}

// BenchmarkUsageRebuild is the rows computed from one snapshot (sort, filter,
// folds), without drawing.
func BenchmarkUsageRebuild(b *testing.B) {
	for _, n := range []int{50, 500, 2000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			_, s := bigUsage(b, n, 10)
			b.ReportAllocs()
			for b.Loop() {
				s.rebuild()
			}
		})
	}
}

// BenchmarkUsageFrame is one frame of the usage screen.
func BenchmarkUsageFrame(b *testing.B) {
	for _, n := range []int{50, 500, 2000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m, _ := bigUsage(b, n, 10)
			b.ReportAllocs()
			for b.Loop() {
				_ = m.View()
			}
		})
	}
}

// BenchmarkUsageSnapshotAndFrame is what the screen does for each snapshot
// the use case emits (at most two a second): receive it, rebuild, draw.
func BenchmarkUsageSnapshotAndFrame(b *testing.B) {
	for _, n := range []int{50, 500, 2000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m, s := bigUsage(b, n, 10)
			snaps := []ports.UsageSnapshot{bigUsageSnapshot(n, 10, 1), bigUsageSnapshot(n, 10, 2)}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				feedUsage(m, s, snaps[i%2])
				_ = m.View()
				i++
			}
		})
	}
}

// TestUsageSelectionSurvivesSnapshots feeds many snapshots whose values
// reorder the rows under every sort, with the cursor on an open container,
// and checks that the cursor stays on the same row and nothing folds.
func TestUsageSelectionSurvivesSnapshots(t *testing.T) {
	for _, sort := range []usageSort{usageSortMemory, usageSortCPU, usageSortMemLimit, usageSortName} {
		t.Run(sort.String(), func(t *testing.T) {
			m, s := bigUsage(t, 60, 3)
			s.sort = sort
			s.rebuild()

			// open svc-0017 and its first pod, put the cursor on the container
			var svc usageRow
			for _, r := range s.rows {
				if r.kind == usageService && r.name == "svc-0017" {
					svc = r
				}
			}
			s.expanded[svc.id()] = true
			s.rebuild()
			for _, r := range s.rows {
				if r.kind == usagePod && r.repo == "svc-0017" && r.name == "svc-0017-aaaa-p0" {
					s.expanded[r.id()] = true
				}
			}
			s.rebuild()
			at := -1
			for i, r := range s.rows {
				if r.kind == usageContainer && r.repo == "svc-0017" && r.pod.Pod.Name == "svc-0017-aaaa-p0" {
					at = i
				}
			}
			if at < 0 {
				t.Fatal("the container row is not shown")
			}
			s.tbl.cursor, s.selected = at, s.rows[at].id()
			want := s.selected

			for tick := 1; tick <= 40; tick++ {
				feedUsage(m, s, bigUsageSnapshot(60, 3, tick))
				_ = m.View()
				if s.tbl.cursor < 0 || s.tbl.cursor >= len(s.rows) {
					t.Fatalf("tick %d: cursor %d outside %d rows", tick, s.tbl.cursor, len(s.rows))
				}
				if got := s.rows[s.tbl.cursor].id(); got != want {
					t.Fatalf("tick %d: cursor moved to %q, want %q", tick, got, want)
				}
				if !s.expanded[svc.id()] {
					t.Fatalf("tick %d: the service closed", tick)
				}
			}
		})
	}
}

// TestUsageSnapshotsDoNotMoveFold checks a folded namespace stays folded and
// the filter stays applied across snapshots.
func TestUsageSnapshotsDoNotMoveFold(t *testing.T) {
	m, s := bigUsage(t, 30, 3)
	s.collapsed["ns-01"] = true
	s.filter.text = []rune("svc-00")
	s.rebuild()
	n := len(s.rows)
	for tick := 1; tick <= 20; tick++ {
		feedUsage(m, s, bigUsageSnapshot(30, 3, tick))
		if len(s.rows) != n {
			t.Fatalf("tick %d: %d rows, want %d", tick, len(s.rows), n)
		}
		for _, r := range s.rows {
			if r.kind == usageService && r.ns == "ns-01" {
				t.Fatalf("tick %d: a row of the folded namespace is shown", tick)
			}
		}
	}
}

// TestUsageSelectedRowDisappears checks the cursor lands on a valid row when
// the selected service leaves the snapshot, and an emptied snapshot does not
// panic.
func TestUsageSelectedRowDisappears(t *testing.T) {
	m, s := bigUsage(t, 30, 3)
	s.tbl.cursor = len(s.rows) - 1
	s.selected = s.rows[s.tbl.cursor].id()
	feedUsage(m, s, bigUsageSnapshot(3, 3, 0))
	_ = m.View()
	if s.tbl.cursor < 0 || s.tbl.cursor >= len(s.rows) {
		t.Fatalf("cursor %d outside %d rows", s.tbl.cursor, len(s.rows))
	}
	feedUsage(m, s, ports.UsageSnapshot{Env: "rec", Synced: true})
	_ = m.View()
	feedUsage(m, s, bigUsageSnapshot(30, 3, 1))
	_ = m.View()
	if len(s.rows) == 0 {
		t.Fatal("no rows after the data came back")
	}
}

// TestUsageLateSnapshotIsIgnored checks that a snapshot of a closed watch
// (an old generation, after r or A) does not replace the current rows.
func TestUsageLateSnapshotIsIgnored(t *testing.T) {
	m, s := bigUsage(t, 30, 3)
	rows := len(s.rows)
	old := s.gen
	press(m, "r")
	if s.gen == old {
		t.Fatal("reload did not open a new watch")
	}
	m.Update(usageMsg{screen: s, gen: old, snap: ports.UsageSnapshot{Env: "rec", Synced: true}, ch: make(chan ports.UsageSnapshot)})
	if len(s.rows) != rows {
		t.Fatalf("a late snapshot replaced the rows: %d, want %d", len(s.rows), rows)
	}
}
