package tui

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func TestStatusChangesAreHighlightedBriefly(t *testing.T) {
	now := t0
	m, _ := newTestModel(t, 1, "")
	m.opts.Now = func() time.Time { return now }
	s := mockupSnapshot("rec")
	snapshot(m, s)
	scr := m.stack[0].(*servicesScreen)
	if scr.changes.pending(now) {
		t.Fatal("the first snapshot marks nothing")
	}
	s.Services[7].Status = domain.StatusCrashLoopBackOff // payment-service breaks
	now = t0.Add(time.Second)
	snapshot(m, s)
	if !scr.changes.recent("payment-service", now) || scr.changes.recent("user-api", now) || !m.ticking() {
		t.Fatal("payment-service must stand out, alone, and the clock must run to end it")
	}
	now = t0.Add(7 * time.Second)
	if scr.changes.recent("payment-service", now) || m.ticking() {
		t.Fatal("the highlight ends after 5 s")
	}
	s.Env = "prd"
	snapshot(m, s)
	if scr.changes.pending(now) {
		t.Fatal("switching environment marks nothing")
	}
}
