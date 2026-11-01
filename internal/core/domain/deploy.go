package domain

import (
	"slices"
	"time"
)

// DeploySource says where a deployment time came from, which is how far it
// can be trusted.
type DeploySource string

// Sources of a deployment time.
const (
	// DeployUnknown: no record, no pod to date it from.
	DeployUnknown DeploySource = ""
	// DeployController: the workload controller's own record of the last
	// completed rollout. It survives pod replacement and scaling.
	DeployController DeploySource = "controller"
	// DeployPods: the age of the pods on the newest template revision. A
	// lower bound on the age: when every pod of the revision was replaced
	// since (an eviction, a node upgrade) it reads later than the deploy.
	DeployPods DeploySource = "pods"
)

// DeployInfo is what the preview shows about a workload's last deployment.
type DeployInfo struct {
	// At is when the current version was deployed: when the rollout
	// completed or, with InProgress, when it started. Zero when unknown.
	At     time.Time
	Source DeploySource
	// InProgress means a rollout is running; Updated of Desired replicas
	// are on the new template. With a zero At, no new pod exists yet.
	InProgress       bool
	Updated, Desired int
}

// LastDeployment says when workload w was last deployed: a change of its pod
// template that the cluster rolled out (a new image, a changed variable,
// a restart or a rollback), not a change of replicas nor a pod restarted
// after a crash. pods are those of w's repository; the ones w does not own
// are ignored. It reads state already watched and calls nothing.
//
// In order: a CronJob has no rollout; a running rollout is reported as
// such, dated by its first new pod; else the controller's record of the
// last completed rollout; else the age of the newest revision's pods.
func LastDeployment(w Workload, pods []Pod) DeployInfo {
	if w.Ref.Kind == KindCronJob {
		return DeployInfo{}
	}
	groups := revisionGroups(w, pods)
	newest := time.Time{}
	if len(groups) > 0 {
		newest = groups[len(groups)-1]
	}
	if w.Progressing {
		info := DeployInfo{InProgress: true, Updated: w.UpdatedReplicas, Desired: w.DesiredReplicas}
		// A second revision is the new template's pods. With one, they are
		// the new template's only if some replicas are already updated;
		// otherwise the rollout has produced no pod yet.
		if len(groups) >= 2 || (len(groups) == 1 && w.UpdatedReplicas > 0) {
			info.At, info.Source = newest, DeployPods
		}
		return info
	}
	switch {
	case !w.RolloutDone.IsZero():
		return DeployInfo{At: w.RolloutDone, Source: DeployController}
	case !newest.IsZero():
		return DeployInfo{At: newest, Source: DeployPods}
	}
	return DeployInfo{}
}

// revisionGroups returns, for each pod-template revision among the live pods
// w owns, the creation time of its earliest pod, oldest group first. The
// newest revision is the one whose first pod was created last: after a
// rollout its pods all come after the old ones, and after a rollback the
// old template's pods are new pods.
func revisionGroups(w Workload, pods []Pod) []time.Time {
	first := map[string]time.Time{}
	for _, p := range pods {
		if p.Deleted || p.Created.IsZero() || !w.Owns(p) {
			continue
		}
		if t, ok := first[p.Revision]; !ok || p.Created.Before(t) {
			first[p.Revision] = p.Created
		}
	}
	out := make([]time.Time, 0, len(first))
	for _, t := range first {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}
