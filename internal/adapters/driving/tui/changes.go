package tui

import (
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// changeHighlight is how long a service whose status changed stands out.
const changeHighlight = 5 * time.Second

// statusChanges remembers which services changed status recently, so the
// services screen can show a crash or a recovery as it happens.
type statusChanges struct {
	env    domain.Env
	status map[string]domain.ServiceStatus
	at     map[string]time.Time
}

// observe compares a snapshot with the previous one of the same
// environment. The first snapshot of an environment marks nothing.
func (c *statusChanges) observe(s ports.CatalogSnapshot, now time.Time) {
	first := c.status == nil || s.Env != c.env
	if first {
		c.env, c.status, c.at = s.Env, map[string]domain.ServiceStatus{}, map[string]time.Time{}
	}
	for _, r := range s.Services {
		if prev, ok := c.status[r.Repo]; ok && prev != r.Status && !first {
			c.at[r.Repo] = now
		}
		c.status[r.Repo] = r.Status
	}
}

// recent reports whether repo changed status less than changeHighlight ago.
func (c *statusChanges) recent(repo string, now time.Time) bool {
	at, ok := c.at[repo]
	return ok && now.Sub(at) < changeHighlight
}

// pending reports whether a highlight is still shown, pruning old ones.
func (c *statusChanges) pending(now time.Time) bool {
	for repo, at := range c.at {
		if now.Sub(at) >= changeHighlight {
			delete(c.at, repo)
		}
	}
	return len(c.at) > 0
}

// ticking: the clock ends the highlights.
func (s *servicesScreen) ticking(m *Model) bool { return s.changes.pending(m.opts.Now()) }
