package domain

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Waiting reasons that mean the image cannot be pulled.
var imagePullReasons = []string{"ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull"}

// PodStatus returns the health of one pod. Precedence, worst first
// (docs/DECISIONS.md D-024):
//  1. a container waiting in CrashLoopBackOff: CrashLoopBackOff, or
//     OOMKilled when its last termination was an OOM kill;
//  2. a container terminated by OOMKilled: OOMKilled;
//  3. a container unable to pull its image: ImagePullBackOff;
//  4. phase Pending: Pending;
//  5. phase Failed, or running with a container not ready: Degraded;
//  6. phase Unknown: Unknown;
//  7. otherwise Healthy.
func PodStatus(p Pod) ServiceStatus {
	worst := StatusHealthy
	for _, c := range p.Containers {
		if c.Init && c.State == ContainerTerminated {
			continue
		}
		worst = min(worst, containerStatus(c))
	}
	switch {
	case worst < StatusDegraded:
		return worst
	case p.Phase == PodPending:
		return StatusPending
	case p.Phase == PodFailed:
		return StatusDegraded
	case p.Phase == PodUnknown:
		return min(worst, StatusUnknown)
	}
	return worst
}

func containerStatus(c Container) ServiceStatus {
	switch {
	case c.State == ContainerWaiting && c.Reason == "CrashLoopBackOff":
		if c.LastTermination != nil && c.LastTermination.Reason == "OOMKilled" {
			return StatusOOMKilled
		}
		return StatusCrashLoopBackOff
	case c.State == ContainerTerminated && c.Reason == "OOMKilled":
		return StatusOOMKilled
	case c.State == ContainerWaiting && slices.Contains(imagePullReasons, c.Reason):
		return StatusImagePullBackOff
	case !c.Init && !c.Ready:
		return StatusDegraded
	}
	return StatusHealthy
}

// Summarize computes one services-screen row from a repository's workloads
// and their pods. Workloads[0] is the primary workload whose app container
// gives the version.
func Summarize(repo string, workloads []Workload, pods []Pod, f ContainerFilter) ServiceSummary {
	s := ServiceSummary{Repo: repo, Workloads: len(workloads), Status: StatusHealthy, Pods: pods}
	progressing := false
	for i, w := range workloads {
		s.Refs = append(s.Refs, w.Ref)
		s.WorkloadStates = append(s.WorkloadStates, w)
		s.DesiredPods += w.DesiredReplicas
		s.ReadyPods += w.ReadyReplicas
		s.UpdatedPods += w.UpdatedReplicas
		if i == 0 || w.Created.Before(s.Created) {
			s.Created = w.Created
		}
		if w.Progressing || (w.DesiredReplicas > 0 && w.UpdatedReplicas < w.DesiredReplicas) {
			progressing = true
		}
	}
	for _, p := range pods {
		st := PodStatus(p)
		if progressing && (st == StatusDegraded || st == StatusPending) {
			st = StatusProgressing
		}
		s.Status = min(s.Status, st)
		for _, c := range f.AppContainers(p) {
			s.Restarts += c.Restarts
			if c.LastTermination != nil && c.LastTermination.At.After(s.LastRestart) {
				s.LastRestart = c.LastTermination.At
			}
		}
	}
	switch {
	case progressing:
		s.Status = min(s.Status, StatusProgressing)
	case s.DesiredPods == 0:
		s.Status = min(s.Status, StatusUnknown)
	case s.ReadyPods < s.DesiredPods && s.Status == StatusHealthy:
		// Missing replicas no pod explains (a pending or crashing pod
		// already names the problem).
		s.Status = StatusDegraded
	}
	if len(workloads) > 0 {
		s.Version = WorkloadVersion(workloads[0].Ref.Name, pods, f)
	}
	return s
}

// WorkloadVersion returns the app image tag of workload's pods, "old→new"
// when two versions run side by side during a rollout.
func WorkloadVersion(workload string, pods []Pod, f ContainerFilter) string {
	type seen struct {
		tag     string
		created time.Time
	}
	var tags []seen
	for _, p := range pods {
		if p.OwnerName != workload {
			continue
		}
		c, ok := f.PrimaryApp(p)
		if !ok {
			continue
		}
		tag := ImageTag(c.Image)
		i := slices.IndexFunc(tags, func(s seen) bool { return s.tag == tag })
		if i < 0 {
			tags = append(tags, seen{tag, p.Created})
		} else if p.Created.Before(tags[i].created) {
			tags[i].created = p.Created
		}
	}
	slices.SortFunc(tags, func(a, b seen) int { return a.created.Compare(b.created) })
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.tag
	}
	return strings.Join(names, "→")
}

// SortKey selects the order of the services screen.
type SortKey int

// Sort keys, cycled with the sort key.
const (
	SortByStatus SortKey = iota
	SortByName
	SortByRestarts
	SortByAge
)

var sortNames = [...]string{"status", "name", "restarts", "age"}

// String names the sort key for the status bar.
func (k SortKey) String() string { return sortNames[k] }

// Next returns the following sort key, wrapping around.
func (k SortKey) Next() SortKey { return (k + 1) % SortKey(len(sortNames)) }

// SortServices orders rows in place: status worst first, name, restarts
// (most first) or age (newest first). Ties are broken by name.
func SortServices(rows []ServiceSummary, k SortKey) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var c int
		switch k {
		case SortByStatus:
			c = cmp.Compare(a.Status, b.Status)
		case SortByRestarts:
			c = cmp.Compare(b.Restarts, a.Restarts)
		case SortByAge:
			c = b.Created.Compare(a.Created)
		}
		if c == 0 {
			c = cmp.Compare(a.Repo, b.Repo)
		}
		return c < 0
	})
}

// WorstPod returns the pod that gives the service its status: the first
// pod with the worst status, or false when there is no pod.
func WorstPod(pods []Pod) (Pod, bool) {
	best, found := Pod{}, false
	for _, p := range pods {
		if !found || PodStatus(p) < PodStatus(best) {
			best, found = p, true
		}
	}
	return best, found
}
