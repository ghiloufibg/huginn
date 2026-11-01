package domain

import "time"

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
//
// It runs for the selected service on every frame, so the common case, a
// settled Deployment with a controller record, answers without looking at
// its pods, and no case allocates.
func LastDeployment(w Workload, pods []Pod) DeployInfo {
	if w.Ref.Kind == KindCronJob {
		return DeployInfo{}
	}
	if w.Progressing {
		if info, ok := rolloutInProgress(w, pods); ok {
			return info
		}
	}
	if !w.RolloutDone.IsZero() {
		return DeployInfo{At: w.RolloutDone, Source: DeployController}
	}
	if newest, _ := revisions(w, pods); !newest.IsZero() {
		return DeployInfo{At: newest, Source: DeployPods}
	}
	return DeployInfo{}
}

// keepsRolloutRecord tells whether the kind's controller records when it
// completes a rollout (Workload.RolloutDone), so that a zero RolloutDone
// means the last rollout is not complete. Only Deployments do.
func keepsRolloutRecord(k WorkloadKind) bool { return k == KindDeployment }

// rolloutInProgress says whether w is being rolled out, as opposed to only
// scaled: the watched Progressing flag is also true while replicas are added
// and the controller's counts lag (updatedReplicas below the new replicas),
// which is not a deployment. It reports false for such a workload.
//
//   - A second pod-template revision among the pods is a rollout: the new
//     template's pods exist beside the old ones.
//   - A Deployment whose controller record says the last rollout is complete
//     (RolloutDone) is not rolling out: that record holds through scaling and
//     changes the moment a new rollout starts. Without it, the rollout is not
//     complete.
//   - Other kinds keep no record; with a single revision they are rolling out
//     only if no replica is on the template yet (the template changed and
//     the first pod is not replaced), never when replicas merely grow.
func rolloutInProgress(w Workload, pods []Pod) (DeployInfo, bool) {
	newest, n := revisions(w, pods)
	info := DeployInfo{InProgress: true, Updated: w.UpdatedReplicas, Desired: w.DesiredReplicas}
	switch {
	case n >= 2:
		info.At, info.Source = newest, DeployPods
	case keepsRolloutRecord(w.Ref.Kind) && w.RolloutDone.IsZero():
		if n == 1 && w.UpdatedReplicas > 0 { // the only revision is the new one
			info.At, info.Source = newest, DeployPods
		}
	case !keepsRolloutRecord(w.Ref.Kind) && w.UpdatedReplicas == 0 && w.DesiredReplicas > 0:
		// the template changed and no pod is on it yet
	default:
		return DeployInfo{}, false
	}
	return info, true
}

// revFirst is a pod-template revision and the creation time of its earliest
// live pod.
type revFirst struct {
	rev string
	at  time.Time
}

// revisions returns the creation time of the earliest pod of the newest
// pod-template revision among the live pods w owns, and how many revisions
// there are. The newest revision is the one whose first pod was created last:
// after a rollout its pods all come after the old ones, and after a rollback
// the old template's pods are new pods. Services have one or two revisions,
// so the table lives on the stack.
func revisions(w Workload, pods []Pod) (newest time.Time, n int) {
	var buf [4]revFirst
	seen := buf[:0]
	for i := range pods {
		p := &pods[i]
		if p.Deleted || p.Created.IsZero() || !w.Owns(*p) {
			continue
		}
		found := false
		for j := range seen {
			if seen[j].rev == p.Revision {
				if p.Created.Before(seen[j].at) {
					seen[j].at = p.Created
				}
				found = true
				break
			}
		}
		if !found {
			seen = append(seen, revFirst{p.Revision, p.Created})
		}
	}
	for _, r := range seen {
		if r.at.After(newest) {
			newest = r.at
		}
	}
	return newest, len(seen)
}
