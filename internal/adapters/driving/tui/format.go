package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// shortAge formats a duration like kubectl: 45s, 12m, 3h, 12d.
func shortAge(d time.Duration) string { return domain.FormatAge(d) }

// since formats the age of t at now, or "-" for a zero time.
func since(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return shortAge(now.Sub(t))
}

// errKind names a domain error kind for compact display.
func errKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, domain.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		return "forbidden"
	case errors.Is(err, domain.ErrUnreachable):
		return "unreachable"
	case errors.Is(err, domain.ErrNotFound):
		return "not found"
	case errors.Is(err, domain.ErrNotImplemented):
		return "not available"
	default:
		return "error"
	}
}

// statusCounts summarizes rows for the status bar.
func statusCounts(rows []domain.ServiceSummary) string {
	var repos, orphans, failing, degraded, rolling, healthy int
	for _, r := range rows {
		if r.Unassigned {
			orphans++
		} else {
			repos++
		}
		switch r.Status {
		case domain.StatusCrashLoopBackOff, domain.StatusOOMKilled, domain.StatusImagePullBackOff:
			failing++
		case domain.StatusDegraded, domain.StatusPending, domain.StatusUnknown:
			degraded++
		case domain.StatusProgressing:
			rolling++
		default:
			healthy++
		}
	}
	parts := []string{plural(repos, "repo")}
	if orphans > 0 {
		parts[0] += fmt.Sprintf(" + %d without repo", orphans)
	}
	for _, p := range []struct {
		n    int
		what string
	}{{failing, "failing"}, {degraded, "degraded/pending"}, {rolling, "rolling"}, {healthy, "healthy"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return strings.Join(parts, " · ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
