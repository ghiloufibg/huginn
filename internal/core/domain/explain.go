package domain

import (
	"fmt"
	"strings"
	"time"
)

// FormatAge formats a duration like kubectl: 45s, 12m, 3h, 12d.
func FormatAge(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// recentRestart is how long a restart stays worth mentioning for a
// healthy service.
const recentRestart = 24 * time.Hour

// Explain says in one line why a service has its status, from the state
// already watched (no events): the failing container's last exit, the
// scheduler's message, the rollout progress… It returns "" for a healthy
// service with nothing worth saying.
func Explain(s ServiceSummary, f ContainerFilter, now time.Time) string {
	ago := func(t time.Time) string { return FormatAge(now.Sub(t)) + " ago" }
	switch s.Status {
	case StatusCrashLoopBackOff, StatusOOMKilled:
		c, ok := worstContainer(s.Pods, s.Status)
		if !ok {
			return ""
		}
		var parts []string
		if t := c.LastTermination; t != nil {
			if t.Reason == "OOMKilled" {
				parts = append(parts, fmt.Sprintf("OOMKilled exit %d, %s", t.ExitCode, ago(t.At)))
			} else {
				parts = append(parts, fmt.Sprintf("exit %d (%s) %s", t.ExitCode, t.Reason, ago(t.At)))
			}
		} else if c.Reason == "OOMKilled" {
			parts = append(parts, "OOMKilled")
		}
		if s.Status == StatusOOMKilled && c.Resources.MemoryLimit != "" {
			parts = append(parts, "limit "+c.Resources.MemoryLimit)
		}
		parts = append(parts, fmt.Sprintf("%d restarts", s.Restarts))
		if c.Message != "" {
			parts = append(parts, firstClause(c.Message))
		}
		return c.Name + ": " + strings.Join(parts, " · ")
	case StatusImagePullBackOff:
		c, ok := worstContainer(s.Pods, s.Status)
		if !ok {
			return ""
		}
		msg := "cannot pull " + c.Name + ":" + ImageTag(c.Image)
		if c.Message != "" {
			msg += " — " + firstClause(c.Message)
		}
		return msg
	case StatusPending:
		n, why := 0, ""
		for _, p := range s.Pods {
			if PodStatus(p) != StatusPending {
				continue
			}
			n++
			if why == "" {
				why = podWhy(p)
			}
		}
		if n == 0 {
			return ""
		}
		out := plural(n, "pod") + " pending"
		if why != "" {
			out += ": " + why
		}
		return out
	case StatusDegraded:
		notReady := 0
		var last *Termination
		for _, p := range s.Pods {
			if PodStatus(p) == StatusDegraded {
				notReady++
			}
			for _, c := range f.AppContainers(p) {
				if t := c.LastTermination; t != nil && (last == nil || t.At.After(last.At)) {
					last = t
				}
			}
		}
		out := fmt.Sprintf("%d of %d pods not ready", notReady, max(s.DesiredPods, len(s.Pods)))
		if notReady == 0 {
			out = fmt.Sprintf("%d of %d replicas available", s.ReadyPods, s.DesiredPods)
		}
		if last != nil {
			out += fmt.Sprintf(" · last exit %d (%s) %s", last.ExitCode, last.Reason, ago(last.At))
		}
		return out
	case StatusProgressing:
		starting := 0
		for _, p := range s.Pods {
			if PodStatus(p) != StatusHealthy {
				starting++
			}
		}
		out := fmt.Sprintf("rollout %s: %d/%d updated", s.Version, s.UpdatedPods, s.DesiredPods)
		if starting > 0 {
			out += fmt.Sprintf(", %d starting", starting)
		}
		return out
	case StatusUnknown:
		if s.DesiredPods == 0 {
			return "scaled to 0"
		}
		return "pod state unknown"
	}
	if !s.LastRestart.IsZero() && now.Sub(s.LastRestart) < recentRestart {
		reason := ""
		for _, p := range s.Pods {
			for _, c := range f.AppContainers(p) {
				if t := c.LastTermination; t != nil && t.At.Equal(s.LastRestart) {
					reason = " (" + t.Reason + ")"
				}
			}
		}
		return "last restart " + ago(s.LastRestart) + reason
	}
	return ""
}

// worstContainer returns the first container whose state gives st.
func worstContainer(pods []Pod, st ServiceStatus) (Container, bool) {
	for _, p := range pods {
		for _, c := range p.Containers {
			if (!c.Init || c.State != ContainerTerminated) && containerStatus(c) == st {
				return c, true
			}
		}
	}
	return Container{}, false
}

// podWhy explains a pending pod: the scheduler's message, else the
// reason its containers are waiting.
func podWhy(p Pod) string {
	if p.Message != "" {
		return firstClause(p.Message)
	}
	if p.Reason != "" {
		return p.Reason
	}
	for _, c := range p.Containers {
		if !c.Init && c.Reason != "" {
			return c.Reason
		}
	}
	return ""
}

// firstClause keeps a Kubernetes message short: its first sentence,
// without a trailing period.
func firstClause(msg string) string {
	msg = strings.TrimSpace(msg)
	if i := strings.Index(msg, " container="); i > 0 { // kubelet back-off detail
		msg = msg[:i]
	}
	if i := strings.Index(msg, ". "); i > 0 {
		msg = msg[:i]
	}
	return strings.TrimSuffix(msg, ".")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
