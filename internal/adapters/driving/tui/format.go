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
	case errors.Is(err, domain.ErrConfig):
		return "configuration error"
	case errors.Is(err, domain.ErrNotStarted):
		return "not started"
	default:
		return "error"
	}
}

// errAdvice tells what to do about a cluster error: permanent errors are
// not retried, so the user must act.
func errAdvice(err error) string {
	switch {
	case domain.Permanent(err):
		return "fix the configuration (environments.yaml, kubeconfig) and restart huginn"
	case errors.Is(err, domain.ErrUnauthorized):
		return "log in again (on GKE: gcloud auth login) · retrying automatically"
	}
	return "retrying automatically"
}

// statusCounts summarizes rows for the status bar.
// statusGroups name the status groups, from the most urgent.
var statusGroups = [...]string{"failing", "degraded/pending", "rolling", "healthy"}

// statusGroup returns the index in statusGroups of a status.
func statusGroup(st domain.ServiceStatus) int {
	switch st {
	case domain.StatusCrashLoopBackOff, domain.StatusOOMKilled, domain.StatusImagePullBackOff:
		return 0
	case domain.StatusDegraded, domain.StatusPending, domain.StatusUnknown:
		return 1
	case domain.StatusProgressing:
		return 2
	}
	return 3
}

func statusCounts(rows []domain.ServiceSummary) string {
	var repos, orphans int
	var groups [len(statusGroups)]int
	for _, r := range rows {
		if r.Unassigned {
			orphans++
		} else {
			repos++
		}
		groups[statusGroup(r.Status)]++
	}
	parts := []string{plural(repos, "repo")}
	if orphans > 0 {
		parts[0] += fmt.Sprintf(" + %d without repo", orphans)
	}
	for i, n := range groups {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, statusGroups[i]))
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

// podShortID is the random suffix of a pod name (m8q7v), enough to tell
// the pods of one workload apart.
func podShortID(pod string) string {
	if i := strings.LastIndex(pod, "-"); i >= 0 {
		return pod[i+1:]
	}
	return pod
}
