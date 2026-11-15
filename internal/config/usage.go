package config

import (
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Defaults and bounds of the usage screen.
const (
	DefaultUsageRefresh = 15 * time.Second
	MinUsageRefresh     = 5 * time.Second
	DefaultUsageHistory = 15 * time.Minute
	DefaultUsageWarn    = 80
	DefaultUsageCrit    = 95
)

func usageDefaults(u *Usage) {
	if u.Enabled == nil {
		on := true
		u.Enabled = &on
	}
	if u.Refresh == "" {
		u.Refresh = DefaultUsageRefresh.String()
	}
	if u.History == "" {
		u.History = DefaultUsageHistory.String()
	}
	if u.Thresholds.Warn == 0 {
		u.Thresholds.Warn = DefaultUsageWarn
	}
	if u.Thresholds.Crit == 0 {
		u.Thresholds.Crit = DefaultUsageCrit
	}
}

func (v *validator) usage() {
	u := v.c.UI.Usage
	if d, err := time.ParseDuration(u.Refresh); err != nil || d < MinUsageRefresh {
		v.add(FileUI, "usage.refresh", "%q is not a duration of at least %s (for example 15s)", u.Refresh, MinUsageRefresh)
	}
	if d, err := time.ParseDuration(u.History); err != nil || d < 0 {
		v.add(FileUI, "usage.history", "%q is not a duration of 0s or more (for example 15m)", u.History)
	}
	t := u.Thresholds
	if t.Warn < 1 || t.Warn > 100 {
		v.add(FileUI, "usage.thresholds.warn", "must be between 1 and 100")
	}
	if t.Crit < 1 || t.Crit > 100 {
		v.add(FileUI, "usage.thresholds.crit", "must be between 1 and 100")
	}
	if t.Warn >= t.Crit && t.Warn >= 1 && t.Crit >= 1 {
		v.add(FileUI, "usage.thresholds.warn", "must be below crit (%d)", t.Crit)
	}
}

// RefreshEvery is the poll interval, DefaultUsageRefresh when it does not
// parse (the folder is validated, so it does).
func (u Usage) RefreshEvery() time.Duration {
	if d, err := time.ParseDuration(u.Refresh); err == nil && d >= MinUsageRefresh {
		return d
	}
	return DefaultUsageRefresh
}

// KeepHistory is how far back trends reach; zero means none.
func (u Usage) KeepHistory() time.Duration {
	if d, err := time.ParseDuration(u.History); err == nil && d >= 0 {
		return d
	}
	return DefaultUsageHistory
}

// ShareThresholds returns the warning and critical shares of a limit.
func (u Usage) ShareThresholds() domain.Thresholds {
	return domain.Thresholds{Warn: float64(u.Thresholds.Warn), Crit: float64(u.Thresholds.Crit)}
}

// On reports whether the usage screen is available.
func (u Usage) On() bool { return u.Enabled == nil || *u.Enabled }
