package layout

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// E14: an entry from another day shows its date.
func TestFormatTimeShowsOtherDays(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 30, 0, 0, time.UTC)
	o := ports.RenderOptions{Now: now, Location: time.UTC}
	if got := formatTime(now.Add(-time.Minute), o, "15:04:05.000"); got != "08:29:00.000" {
		t.Errorf("today: %q", got)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := formatTime(old, o, "15:04:05.000"); got != "2020-01-01 00:00:00.000" {
		t.Errorf("old: %q", got)
	}
	if got := formatTime(old, o, "2006-01-02 15:04"); got != "2020-01-01 00:00" {
		t.Errorf("layout with a date: %q", got)
	}
	o.Timestamps = ports.TimestampUTC
	if got := formatTime(old, o, "15:04:05"); got != "2020-01-01 00:00:00Z" {
		t.Errorf("utc: %q", got)
	}
}
