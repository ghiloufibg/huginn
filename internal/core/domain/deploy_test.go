package domain

import (
	"testing"
	"time"
)

var deployT0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func at(min int) time.Time { return deployT0.Add(time.Duration(min) * time.Minute) }

// dpod is a pod of Deployment "api" in namespace "ns", made from template
// revision rev at minute min.
func dpod(name, rev string, min int) Pod {
	return Pod{Namespace: "ns", Name: name, OwnerName: "api", OwnerKind: "Deployment", Revision: rev, Created: at(min), Phase: PodRunning}
}

func deployment(mods ...func(*Workload)) Workload {
	w := Workload{
		Ref:             WorkloadRef{Namespace: "ns", Kind: KindDeployment, Name: "api"},
		DesiredReplicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
	}
	for _, m := range mods {
		m(&w)
	}
	return w
}

func rolling(updated int) func(*Workload) {
	return func(w *Workload) { w.Progressing, w.UpdatedReplicas = true, updated }
}
func done(min int) func(*Workload) { return func(w *Workload) { w.RolloutDone = at(min) } }

func TestLastDeployment(t *testing.T) {
	tests := []struct {
		name string
		w    Workload
		pods []Pod
		want DeployInfo
	}{
		{
			name: "controller record wins over the pods",
			w:    deployment(done(10)),
			pods: []Pod{dpod("a", "h1", 40), dpod("b", "h1", 41)}, // pods replaced since: younger than the deploy
			want: DeployInfo{At: at(10), Source: DeployController},
		},
		{
			name: "no controller record: the newest revision's earliest pod",
			w:    Workload{Ref: WorkloadRef{Namespace: "ns", Kind: KindStatefulSet, Name: "api"}, DesiredReplicas: 2, UpdatedReplicas: 2},
			pods: []Pod{
				{Namespace: "ns", Name: "api-0", OwnerName: "api", OwnerKind: "StatefulSet", Revision: "r2", Created: at(30)},
				{Namespace: "ns", Name: "api-1", OwnerName: "api", OwnerKind: "StatefulSet", Revision: "r2", Created: at(31)},
			},
			want: DeployInfo{At: at(30), Source: DeployPods},
		},
		{
			name: "rollout in progress: dated by the first new pod, with the progress",
			w:    deployment(done(10), rolling(1)),
			pods: []Pod{dpod("old1", "h1", 0), dpod("old2", "h1", 1), dpod("new1", "h2", 50)},
			want: DeployInfo{At: at(50), Source: DeployPods, InProgress: true, Updated: 1, Desired: 2},
		},
		{
			name: "rollout in progress, no new pod yet",
			w:    deployment(done(10), rolling(0)),
			pods: []Pod{dpod("old1", "h1", 0), dpod("old2", "h1", 1)},
			want: DeployInfo{InProgress: true, Updated: 0, Desired: 2},
		},
		{
			name: "rollout in progress, the old pods are gone",
			w:    deployment(rolling(1)),
			pods: []Pod{dpod("new1", "h2", 50)},
			want: DeployInfo{At: at(50), Source: DeployPods, InProgress: true, Updated: 1, Desired: 2},
		},
		{
			name: "rollout in progress with no pod at all (scaled up from zero)",
			w:    deployment(rolling(0)),
			want: DeployInfo{InProgress: true, Desired: 2},
		},
		{
			name: "rollback: old template's pods are new pods, the newest group is dated",
			w:    deployment(done(70)), // the controller completed the rollback at 70
			pods: []Pod{dpod("a", "h1", 60), dpod("b", "h1", 61)},
			want: DeployInfo{At: at(70), Source: DeployController},
		},
		{
			name: "rollback seen only through pods: the revision with the latest first pod",
			w:    deployment(),
			pods: []Pod{dpod("a", "h2", 20), dpod("b", "h2", 21), dpod("c", "h1", 60), dpod("d", "h1", 61)},
			want: DeployInfo{At: at(60), Source: DeployPods},
		},
		{
			name: "terminating pods do not count",
			w:    deployment(),
			pods: []Pod{dpod("a", "h1", 0), func() Pod { p := dpod("z", "h2", 90); p.Deleted = true; return p }()},
			want: DeployInfo{At: at(0), Source: DeployPods},
		},
		{
			name: "pods of another workload do not count",
			w:    deployment(),
			pods: []Pod{dpod("a", "h1", 5), {Namespace: "ns", Name: "w-1", OwnerName: "worker", OwnerKind: "Deployment", Revision: "w1", Created: at(99)}},
			want: DeployInfo{At: at(5), Source: DeployPods},
		},
		{
			name: "pods of another namespace do not count",
			w:    deployment(),
			pods: []Pod{dpod("a", "h1", 5), {Namespace: "other", Name: "api-x", OwnerName: "api", OwnerKind: "Deployment", Revision: "h9", Created: at(99)}},
			want: DeployInfo{At: at(5), Source: DeployPods},
		},
		{
			name: "scaled to zero keeps the controller's record",
			w:    deployment(done(10), func(w *Workload) { w.DesiredReplicas, w.ReadyReplicas, w.UpdatedReplicas = 0, 0, 0 }),
			want: DeployInfo{At: at(10), Source: DeployController},
		},
		{
			name: "scaled to zero without a record is unknown",
			w:    deployment(func(w *Workload) { w.DesiredReplicas, w.ReadyReplicas, w.UpdatedReplicas = 0, 0, 0 }),
			want: DeployInfo{},
		},
		{
			name: "a pod without a creation time is ignored",
			w:    deployment(),
			pods: []Pod{{Namespace: "ns", Name: "a", OwnerName: "api", OwnerKind: "Deployment", Revision: "h1"}},
			want: DeployInfo{},
		},
		{
			name: "pods without a revision form one group",
			w:    deployment(),
			pods: []Pod{dpod("a", "", 7), dpod("b", "", 3)},
			want: DeployInfo{At: at(3), Source: DeployPods},
		},
		{
			name: "a CronJob has no rollout, whatever its pods say",
			w:    Workload{Ref: WorkloadRef{Namespace: "ns", Kind: KindCronJob, Name: "api"}, RolloutDone: at(1)},
			pods: []Pod{{Namespace: "ns", Name: "j-1", OwnerName: "api", OwnerKind: "CronJob", Created: at(5)}},
			want: DeployInfo{},
		},
		{
			name: "a bare pod is dated by itself",
			w:    Workload{Ref: WorkloadRef{Namespace: "ns", Kind: KindPod, Name: "tool"}, Standalone: true},
			pods: []Pod{{Namespace: "ns", Name: "tool", Created: at(12)}},
			want: DeployInfo{At: at(12), Source: DeployPods},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastDeployment(tt.w, tt.pods); got != tt.want {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// The point of preferring the controller's record: nothing that is not a
// deployment moves it.
func TestLastDeploymentIgnoresScalingAndReplacedPods(t *testing.T) {
	w := deployment(done(10))
	base := LastDeployment(w, []Pod{dpod("a", "h1", 11), dpod("b", "h1", 11)})

	scaled := w
	scaled.DesiredReplicas, scaled.ReadyReplicas, scaled.UpdatedReplicas = 5, 5, 5
	replaced := []Pod{dpod("c", "h1", 600), dpod("d", "h1", 601), dpod("e", "h1", 602)}
	for name, got := range map[string]DeployInfo{
		"scaled up":                LastDeployment(scaled, replaced),
		"every pod replaced":       LastDeployment(w, replaced),
		"crash-restarted pod only": LastDeployment(w, []Pod{dpod("c", "h1", 999)}),
	} {
		if got != base {
			t.Errorf("%s moved the deployment time: %+v, want %+v", name, got, base)
		}
	}
}
