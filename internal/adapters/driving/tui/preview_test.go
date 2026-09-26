package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// rest delivers the cursor-rest timer of the services preview, as if
// the cursor stayed 300 ms on the selected service.
func rest(m *Model) {
	p := &m.stack[0].(*servicesScreen).preview
	_, cmd := m.Update(eventsRestMsg{key: p.target, seq: p.seq})
	run(m, cmd)
}

func TestPreviewPlacement(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		side, bottom bool
	}{
		{"80x24: no room, table only", 80, 24, false, false},
		{"120x30: room below the rows", 120, 30, false, true},
		{"160x30: room below the rows", 160, 30, false, true},
		{"160x50: room below the rows", 160, 50, false, true},
		{"220x50: side panel", 220, 50, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := newTestModel(t, 1, "")
			snapshot(m, mockupSnapshot("rec"))
			out := ansi.Strip(render(m, c.w, c.h))
			lines := strings.Split(out, "\n")
			if len(lines) != c.h {
				t.Fatalf("%d lines, want %d", len(lines), c.h)
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != c.w {
					t.Fatalf("line %d is %d cells wide, want %d: %q", i, w, c.w, l)
				}
			}
			if got := strings.Contains(out, "│"); got != c.side {
				t.Errorf("side panel = %v, want %v", got, c.side)
			}
			if got := strings.Contains(out, " ─ catalog-indexer ─"); got != (c.side || c.bottom) {
				t.Errorf("preview shown = %v, want %v", got, c.side || c.bottom)
			}
			// No row is hidden by the preview.
			if !strings.Contains(out, "legacy-cron") && c.h >= 30 {
				t.Error("the last row is hidden")
			}
		})
	}
}

func TestPreviewFollowsCursorAndReadsEventsOnRest(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	render(m, 160, 30)
	if events.calls != 0 {
		t.Fatal("events must not be read before the cursor rests")
	}
	out := ansi.Strip(render(m, 160, 30))
	if !strings.Contains(out, "reading events of 6kq2x") {
		t.Fatalf("pending events not shown:\n%s", out)
	}
	rest(m)
	golden(t, "services_preview_160x30", render(m, 160, 30))
	if events.calls != 1 {
		t.Fatalf("%d reads, want 1", events.calls)
	}
	out = ansi.Strip(render(m, 160, 30))
	for _, want := range []string{"WORKLOADS  Deployment catalog-indexer  0/2 ready", "PODS       6kq2x  CrashLoopBackOff", "WARNINGS    30s  BackOff  Back-off restarting failed container app", "(x87)", "HIDDEN     istio-proxy"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Pulled") {
		t.Error("normal events must not be listed as warnings")
	}

	press(m, "j")
	out = ansi.Strip(render(m, 160, 30))
	if !strings.Contains(out, " ─ order-orchestrator ─") {
		t.Fatalf("preview did not follow the cursor:\n%s", out)
	}
	rest(m)
	press(m, "k")
	rest(m) // cached: no second read of catalog-indexer
	if events.calls != 2 {
		t.Fatalf("%d reads, want 2 (one per pod, then cached)", events.calls)
	}
}

func TestPreviewEventsErrorKeepsTable(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	events.err = errors.New("events is forbidden")
	snapshot(m, mockupSnapshot("rec"))
	render(m, 160, 30)
	rest(m)
	out := ansi.Strip(render(m, 160, 30))
	if !strings.Contains(out, "cannot read events: events is forbidden") || !strings.Contains(out, "catalog-indexer") {
		t.Fatalf("error not shown in the panel:\n%s", out)
	}
}

func TestPreviewToggle(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	render(m, 160, 30)
	press(m, "p")
	if out := ansi.Strip(render(m, 160, 30)); strings.Contains(out, " ─ catalog-indexer ─") {
		t.Fatal("p should hide the preview")
	}
	press(m, "p")
	if out := ansi.Strip(render(m, 160, 30)); !strings.Contains(out, " ─ catalog-indexer ─") {
		t.Fatal("p should show the preview again")
	}
	// At 80×24 there is no room: p forces a bottom split.
	m, _ = newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	if out := ansi.Strip(render(m, 80, 24)); strings.Contains(out, " ─ catalog-indexer ─") {
		t.Fatal("no preview expected at 80×24")
	}
	press(m, "p")
	out := ansi.Strip(render(m, 80, 24))
	if !strings.Contains(out, " ─ catalog-indexer ─") {
		t.Fatalf("p should force the preview on a small terminal:\n%s", out)
	}
	golden(t, "services_preview_forced_80x24", render(m, 80, 24))
}

func TestPreviewSideGolden(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	render(m, 220, 40)
	rest(m)
	golden(t, "services_preview_side_220x40", render(m, 220, 40))
}
